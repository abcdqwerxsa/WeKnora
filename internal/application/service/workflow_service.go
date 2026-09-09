// Package service — workflow CRUD + execution wiring.
//
// The workflow-orchestration feature was assembled from parallel slices:
// CRUD/RBAC landed first (DSL validated with a minimal local shape and
// stored verbatim), the engine package (internal/agent/workflow) landed in
// parallel. This file now wires the two: RunWorkflow compiles the stored
// DSL through the engine and injects platform LLM / knowledge-search
// adapters, persisting every run into workflow_runs.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Workflow validation limits.
const (
	WorkflowNameMinLen        = 1
	WorkflowNameMaxLen        = 255
	WorkflowDescriptionMaxLen = 2000
	// WorkflowDSLVersion is the only DSL document version this slice accepts.
	WorkflowDSLVersion = 1
)

// Workflow CRUD sentinel errors. Handlers map them onto HTTP error bodies.
var (
	// ErrWorkflowNameRequired: name missing or blank.
	ErrWorkflowNameRequired = errors.New("workflow name is required")
	// ErrWorkflowNameTooLong: name exceeds WorkflowNameMaxLen.
	ErrWorkflowNameTooLong = fmt.Errorf("workflow name must be at most %d characters", WorkflowNameMaxLen)
	// ErrWorkflowDescriptionTooLong: description exceeds the limit.
	ErrWorkflowDescriptionTooLong = fmt.Errorf("workflow description must be at most %d characters", WorkflowDescriptionMaxLen)
	// ErrWorkflowInvalidStatus: status outside the closed set.
	ErrWorkflowInvalidStatus = fmt.Errorf("workflow status must be one of %v", types.ValidWorkflowStatuses)
	// ErrWorkflowDSLRequired: DSL document missing or empty.
	ErrWorkflowDSLRequired = errors.New("workflow dsl is required")
	// ErrWorkflowInvalidDSL: DSL document violates the structural contract
	// (wraps the specific reason; mapped to HTTP 400 by the handler).
	ErrWorkflowInvalidDSL = errors.New("invalid workflow dsl")
	// ErrWorkflowTenantRequired: no tenant on the request context.
	ErrWorkflowTenantRequired = errors.New("workspace context required")
	// ErrWorkflowMissingInput: a Start-node form field marked required is
	// absent/blank in the run request (HTTP 400).
	ErrWorkflowMissingInput = errors.New("workflow run input missing required field")
	// ErrWorkflowNotDebuggable: an unpublished (draft/archived) workflow may
	// only be run by its creator or an Admin — the editor debug affordance.
	ErrWorkflowNotDebuggable = errors.New("unpublished workflow can only be run by its creator or an admin (publish it for general access)")
	// ErrWorkflowNotPublishable: publish prerequisites failed (wraps the
	// reason, e.g. DSL does not compile).
	ErrWorkflowNotPublishable = errors.New("workflow cannot be published")
)

// workflowDSLShape is the minimal structural view used to validate the DSL
// document without importing the engine package. It mirrors the dual-view
// contract: `components` carries the execution topology (required),
// `graph` the canvas layout (optional at the storage layer).
type workflowDSLShape struct {
	Version    int                                  `json:"version"`
	Components map[string]workflowDSLShapeComponent `json:"components"`
	Graph      *workflowDSLShapeGraph               `json:"graph,omitempty"`
	Variables  map[string]json.RawMessage           `json:"variables,omitempty"`
}

type workflowDSLShapeComponent struct {
	Obj        workflowDSLShapeObj `json:"obj"`
	Upstream   []string            `json:"upstream"`
	Downstream []string            `json:"downstream"`
}

type workflowDSLShapeObj struct {
	ComponentName string         `json:"component_name"`
	Params        map[string]any `json:"params"`
}

type workflowDSLShapeGraph struct {
	Nodes []json.RawMessage `json:"nodes"`
	Edges []json.RawMessage `json:"edges"`
}

// ValidateWorkflowDSL checks the structural contract of a workflow DSL
// document and returns it verbatim (no rewriting):
//
//   - valid JSON object with version == 1,
//   - non-empty components map,
//   - every component has a non-empty obj.component_name,
//   - at least one entry component (empty upstream list).
//
// Semantics (node kinds, params, edge endpoints) are validated by the engine
// at compile time, not here.
func ValidateWorkflowDSL(dsl types.JSON) (types.JSON, error) {
	if len(dsl) == 0 {
		return nil, ErrWorkflowDSLRequired
	}
	var shape workflowDSLShape
	if err := json.Unmarshal(dsl, &shape); err != nil {
		return nil, fmt.Errorf("%w: not valid JSON: %v", ErrWorkflowInvalidDSL, err)
	}
	if shape.Version != WorkflowDSLVersion {
		return nil, fmt.Errorf("%w: version must be %d, got %d", ErrWorkflowInvalidDSL, WorkflowDSLVersion, shape.Version)
	}
	if len(shape.Components) == 0 {
		return nil, fmt.Errorf("%w: must declare at least one component", ErrWorkflowInvalidDSL)
	}
	entryFound := false
	for id, comp := range shape.Components {
		if comp.Obj.ComponentName == "" {
			return nil, fmt.Errorf("%w: component %q has an empty component_name", ErrWorkflowInvalidDSL, id)
		}
		if len(comp.Upstream) == 0 {
			entryFound = true
		}
	}
	if !entryFound {
		return nil, fmt.Errorf("%w: must have at least one entry component (empty upstream)", ErrWorkflowInvalidDSL)
	}
	return dsl, nil
}

// validateWorkflowFields checks the shared field constraints for create and
// update. dsl may be nil for updates that keep the current DSL (status-only
// transitions); name is always required on both paths.
func validateWorkflowFields(name, description, status string, dsl types.JSON) error {
	nameLen := utf8.RuneCountInString(name)
	if nameLen < WorkflowNameMinLen || name == "" {
		return ErrWorkflowNameRequired
	}
	if nameLen > WorkflowNameMaxLen {
		return ErrWorkflowNameTooLong
	}
	if utf8.RuneCountInString(description) > WorkflowDescriptionMaxLen {
		return ErrWorkflowDescriptionTooLong
	}
	if status != "" && !types.IsValidWorkflowStatus(status) {
		return ErrWorkflowInvalidStatus
	}
	if dsl != nil {
		if _, err := ValidateWorkflowDSL(dsl); err != nil {
			return err
		}
	}
	return nil
}

// AgentServiceRef defers AgentService resolution to first use.
// WorkflowService and AgentService depend on each other only at run time
// (the workflow Agent node calls AgentService.CreateAgentEngine; AgentService
// builds workflow tools off WorkflowService), but wiring both through their
// constructors is a cycle uber/dig rejects at startup. WorkflowService holds
// this ref; NewAgentService fills it when the container is invoked, which
// main guarantees completes before the HTTP server accepts traffic.
type AgentServiceRef struct{ svc interfaces.AgentService }

func (r *AgentServiceRef) Set(svc interfaces.AgentService) { r.svc = svc }
func (r *AgentServiceRef) Get() interfaces.AgentService    { return r.svc }

// workflowService implements interfaces.WorkflowService.
type workflowService struct {
	repo     interfaces.WorkflowRepository
	models   interfaces.ModelService
	kbs      interfaces.KnowledgeBaseService
	enqueuer interfaces.TaskEnqueuer
	// webSearch backs the WebSearch node adapter (admin-configured search
	// providers; nil in Lite mode → node fails with a clear message).
	webSearch interfaces.WebSearchService
	// webSearchProviders resolves the tenant-default provider when a node
	// does not pin one.
	webSearchProviders interfaces.WebSearchProviderRepository
	// sandboxes resolves the tenant's sandbox backend for the Code node
	// (nil in Lite mode → the node fails with a clear message).
	sandboxes sandbox.TenantSandboxResolver
	// agents runs one ReAct turn for the Agent node. Held via AgentServiceRef
	// to break the WorkflowService ⇄ AgentService constructor cycle (see
	// AgentServiceRef). Resolved at run time; nil → clear node error.
	agents *AgentServiceRef
	// mcpClients + mcpServices back the MCPTool node adapter. mcpClients is
	// a one-method view of *mcp.MCPManager so tests can fake the client pool.
	mcpClients  mcpClientProvider
	mcpServices interfaces.MCPServiceService
	// tempDocs backs run attachments: files uploaded before a run and
	// resolved into LLM context (same machinery as chat attachments, under
	// the synthetic session scope "workflow-<id>"). Nil in Lite mode →
	// carrying files on a run fails with a clear message.
	tempDocs interfaces.TemporaryDocumentService
	// redis, when non-nil (full mode), bridges run frames across instances
	// for SSE. Lite mode gets nil and stays process-local.
	redis *redis.Client
	// ckptKV adapts the same redis client onto the engine's KVStore for
	// run checkpoints (resume). nil in Lite mode — runs there execute
	// inline and do not survive process restarts anyway, so no KV.
	ckptKV  wfengine.KVStore
	runs    *workflowRunBroker
	cancels *workflowRunCancels
}

// NewWorkflowService creates a new workflow service. models/kbs back the
// engine adapters injected into every compiled run (LLMFunc → ModelService
// GetChatModel, RetrievalFunc → KnowledgeBaseService HybridSearch);
// enqueuer backs the async run mode (asynq client in full mode, inline
// sync executor in Lite mode); webSearch/webSearchProviders back the
// WebSearch node (nil is legal — the node then errors at run time).
func NewWorkflowService(
	repo interfaces.WorkflowRepository,
	models interfaces.ModelService,
	kbs interfaces.KnowledgeBaseService,
	enqueuer interfaces.TaskEnqueuer,
	redisClient *redis.Client,
	webSearch interfaces.WebSearchService,
	webSearchProviders interfaces.WebSearchProviderRepository,
	sandboxes sandbox.TenantSandboxResolver,
	agents *AgentServiceRef,
	mcpManager *mcp.MCPManager,
	mcpServices interfaces.MCPServiceService,
	tempDocs interfaces.TemporaryDocumentService,
) interfaces.WorkflowService {
	// Lite (nil redis): no checkpoint KV — engine treats nil Deps.CheckpointKV
	// as "no persistence" and every run executes fresh (current behaviour).
	var ckptKV wfengine.KVStore
	if redisClient != nil {
		ckptKV = newRedisCheckpointKV(redisClient)
	}
	return &workflowService{
		repo:               repo,
		models:             models,
		kbs:                kbs,
		enqueuer:           enqueuer,
		webSearch:          webSearch,
		webSearchProviders: webSearchProviders,
		sandboxes:          sandboxes,
		agents:             agents,
		mcpClients:         mcpManager,
		mcpServices:        mcpServices,
		tempDocs:           tempDocs,
		redis:              redisClient,
		ckptKV:             ckptKV,
		runs:               newWorkflowRunBroker(),
		cancels:            newWorkflowRunCancels(),
	}
}

// CreateWorkflow validates and creates a workflow. TenantID and CreatorID
// are taken from the request context; request-supplied values for either are
// overwritten, so a client cannot forge ownership.
func (s *workflowService) CreateWorkflow(ctx context.Context, workflow *types.Workflow) (*types.Workflow, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	status := workflow.Status
	if status == "" {
		status = types.WorkflowStatusDraft
	}
	if err := validateWorkflowFields(workflow.Name, workflow.Description, status, workflow.DSL); err != nil {
		return nil, err
	}
	creatorID, _ := types.UserIDFromContext(ctx)
	created := &types.Workflow{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		CreatorID:   creatorID,
		Name:        workflow.Name,
		Description: workflow.Description,
		DSL:         workflow.DSL,
		Status:      status,
		Version:     1,
	}
	if err := s.repo.CreateWorkflow(ctx, created); err != nil {
		return nil, err
	}
	return created, nil
}

// GetWorkflowByID returns the workflow in the caller's tenant.
func (s *workflowService) GetWorkflowByID(ctx context.Context, id string) (*types.Workflow, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	return s.repo.GetWorkflowByIDAndTenant(ctx, id, tenantID)
}

// ListWorkflows returns one page of the caller's tenant workflows.
func (s *workflowService) ListWorkflows(ctx context.Context, page, pageSize int) ([]*types.Workflow, int64, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, 0, ErrWorkflowTenantRequired
	}
	offset := 0
	if page > 1 {
		offset = (page - 1) * pageSize
	}
	return s.repo.ListWorkflowsByTenantID(ctx, tenantID, offset, pageSize)
}

// UpdateWorkflow replaces the mutable fields of the workflow in the caller's
// tenant and bumps its version. An empty dsl keeps the stored document; an
// empty status keeps the current status.
func (s *workflowService) UpdateWorkflow(ctx context.Context, id string, req *types.UpdateWorkflowRequest) (*types.Workflow, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	existing, err := s.repo.GetWorkflowByIDAndTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	name := req.Name
	if name == "" {
		name = existing.Name
	}
	description := req.Description
	status := req.Status
	dsl := req.DSL
	var dslForValidation types.JSON
	if len(dsl) == 0 {
		// keep current DSL; validate nothing new
		dsl = existing.DSL
	} else {
		dslForValidation = dsl
	}
	if status == "" {
		status = existing.Status
	}
	if err := validateWorkflowFields(name, description, status, dslForValidation); err != nil {
		return nil, err
	}
	existing.Name = name
	existing.Description = description
	existing.DSL = dsl
	existing.Status = status
	existing.Version = existing.Version + 1
	if err := s.repo.UpdateWorkflow(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// DeleteWorkflow soft-deletes the workflow in the caller's tenant.
func (s *workflowService) DeleteWorkflow(ctx context.Context, id string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return ErrWorkflowTenantRequired
	}
	return s.repo.DeleteWorkflow(ctx, id, tenantID)
}

// ListWorkflowRuns returns the run history of a workflow in the caller's
// tenant, newest first; rows are written by RunWorkflow.
func (s *workflowService) ListWorkflowRuns(ctx context.Context, workflowID string) ([]*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	if _, err := s.repo.GetWorkflowByIDAndTenant(ctx, workflowID, tenantID); err != nil {
		return nil, err
	}
	return s.repo.ListWorkflowRunsByTenantAndWorkflow(ctx, tenantID, workflowID)
}

// workflowRunTimeout bounds one synchronous workflow run. Long-running
// graphs should move to the async task queue in a follow-up; the MVP keeps
// execution synchronous behind this cap.
const workflowRunTimeout = 120 * time.Second

// workflowTraceNodeBytes caps one node's serialized outputs inside SSE
// frames and the persisted trace. Oversized maps are replaced by a
// truncated marker — the debug panel stays responsive and the run row
// bounded no matter how large a retrieval or LLM payload is.
// ponytail: fixed cap, no per-node override; raise via constant if a
// workflow ever legitimately needs bigger payloads inspected.
const workflowTraceNodeBytes = 256 * 1024

// capTraceOutputs returns ev's outputs size-capped: maps whose marshalled
// size exceeds the cap collapse to {"_truncated": true, "bytes": n}.
func capTraceOutputs(ev wfengine.NodeEvent) wfengine.NodeEvent {
	if len(ev.Outputs) == 0 {
		return ev
	}
	data, err := json.Marshal(ev.Outputs)
	if err == nil && len(data) <= workflowTraceNodeBytes {
		return ev
	}
	ev.Outputs = map[string]any{"_truncated": true, "bytes": len(data)}
	return ev
}

// validateRunInputs checks the run request's inputs against the workflow's
// Start-node form: every declared required field must be present and
// non-blank. Unknown keys pass through untouched (forward compatibility —
// the engine materialises only declared fields anyway).
func validateRunInputs(normalized *wfengine.DSL, req *types.RunWorkflowRequest) error {
	for _, comp := range normalized.Components {
		if !strings.EqualFold(comp.Obj.ComponentName, nodes.ComponentStart) {
			continue
		}
		fields, err := nodes.StartFieldsOf(comp.Obj.Params)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
		}
		for _, f := range fields {
			if !f.Required {
				continue
			}
			v, ok := req.Inputs[f.Name]
			blank := !ok || v == nil
			if !blank {
				if s, isStr := v.(string); isStr {
					blank = strings.TrimSpace(s) == ""
				}
			}
			if blank {
				return fmt.Errorf("%w: %q", ErrWorkflowMissingInput, f.Name)
			}
		}
		break // one Start node per graph (compile enforces a single entry)
	}
	return nil
}

// dslForRun picks the DSL a run executes: published workflows run their
// frozen snapshot (legacy rows without one fall back to the draft DSL);
// drafts/archived run the live DSL (creator/admin only, see RunWorkflow).
func dslForRun(wf *types.Workflow) types.JSON {
	if wf.Status == types.WorkflowStatusPublished && len(wf.PublishedDSL) > 0 {
		return wf.PublishedDSL
	}
	return wf.DSL
}

// canDebugUnpublished reports whether the caller may run a draft/archived
// workflow: the creator or Admin+ (the editor's debug affordance).
func canDebugUnpublished(ctx context.Context, wf *types.Workflow) bool {
	if types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
		return true
	}
	userID, _ := types.UserIDFromContext(ctx)
	return userID != "" && userID == wf.CreatorID
}

// PublishWorkflow freezes the current DSL as the published snapshot and
// flips the workflow to published. The DSL must normalize (compile-shape
// check) before it is frozen — publishing a broken graph is rejected.
func (s *workflowService) PublishWorkflow(ctx context.Context, id string) (*types.Workflow, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	wf, err := s.repo.GetWorkflowByIDAndTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if _, err := s.normalizeWorkflowDSL(wf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowNotPublishable, err)
	}
	wf.PublishedDSL = wf.DSL
	wf.Status = types.WorkflowStatusPublished
	wf.Version = wf.Version + 1
	if err := s.repo.UpdateWorkflow(ctx, wf); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[workflow:%s] published (version %d)", id, wf.Version)
	return wf, nil
}

// SetWorkflowStatus flips draft/archived (and back). Publishing must go
// through PublishWorkflow (snapshot semantics); passing published here is
// rejected. Unpublishing keeps the snapshot so a later re-publish is a
// no-op diff; archived workflows keep their run history.
func (s *workflowService) SetWorkflowStatus(ctx context.Context, id string, status string) (*types.Workflow, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	if !types.IsValidWorkflowStatus(status) || status == types.WorkflowStatusPublished {
		return nil, fmt.Errorf("%w: use the publish endpoint to publish (got %q)", ErrWorkflowInvalidStatus, status)
	}
	wf, err := s.repo.GetWorkflowByIDAndTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	wf.Status = status
	wf.Version = wf.Version + 1
	if err := s.repo.UpdateWorkflow(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// RunWorkflow executes one run of a workflow in the caller's tenant.
//
// Lifecycle: a workflow_runs row is created in "pending" state first so
// every attempt is observable. req.Async then enqueues a workflow:run task
// (executed by ProcessWorkflowRun) and returns immediately; the sync path
// drives executeWorkflowRun inline. DSL shape errors fail before the row is
// created (400 semantics); compile/execution failures flip the row to
// failed and are also delivered as a terminal run event.
func (s *workflowService) RunWorkflow(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	if req == nil {
		req = &types.RunWorkflowRequest{}
	}
	wf, err := s.repo.GetWorkflowByIDAndTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	// Run gate (Dify draft/published model): published workflows run their
	// frozen snapshot for everyone; drafts/archived are creator/admin-only
	// debugging.
	if wf.Status != types.WorkflowStatusPublished && !canDebugUnpublished(ctx, wf) {
		return nil, ErrWorkflowNotDebuggable
	}

	runDSL := dslForRun(wf)
	normalized, err := s.normalizeDSLBytes(runDSL)
	if err != nil {
		return nil, err
	}
	if verr := validateRunInputs(normalized, req); verr != nil {
		return nil, verr
	}

	inputDoc, _ := json.Marshal(req)
	run := &types.WorkflowRun{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		WorkflowID: id,
		Status:     types.WorkflowRunStatusPending,
		Input:      types.JSON(inputDoc),
	}
	if err := s.repo.CreateWorkflowRun(ctx, run); err != nil {
		return nil, err
	}

	if req.Async {
		payload, merr := json.Marshal(types.WorkflowRunPayload{
			RunID:      run.ID,
			WorkflowID: id,
			TenantID:   tenantID,
			Query:      req.Query,
			Files:      req.Files,
			Inputs:     req.Inputs,
		})
		if merr != nil {
			s.failWorkflowRun(ctx, run, merr)
			return run, merr
		}
		task := asynq.NewTask(types.TypeWorkflowRun, payload,
			asynq.Queue(types.QueueDefault),
			// The worker's own execution cap stays workflowRunTimeout; the
			// extra 30s headroom keeps asynq from killing the task before
			// the run records its terminal state.
			asynq.Timeout(workflowRunTimeout+30*time.Second),
			// The run row is the retry authority: the handler no-ops on
			// non-pending rows, so retries cannot double-execute; failed
			// outcomes are terminal by design (rerun via the API).
			asynq.MaxRetry(2),
		)
		if _, eerr := s.enqueuer.Enqueue(task); eerr != nil {
			s.failWorkflowRun(ctx, run, eerr)
			return run, eerr
		}
		logger.Infof(ctx, "[workflow:%s] run %s enqueued (async)", id, run.ID)
		return run, nil
	}

	return run, s.executeWorkflowRun(ctx, run, wf, normalized, req)
}

// ProcessWorkflowRun is the asynq handler for types.TypeWorkflowRun.
//
// Tenant context is restored from the payload (worker requests carry none).
// It returns nil for execution failures — the run row records the outcome —
// and an error only on infrastructure faults (enqueue-side state missing,
// repo down), letting asynq retry those. A re-delivery of a run that has
// already left "pending" is a no-op, which makes the handler idempotent.
func (s *workflowService) ProcessWorkflowRun(ctx context.Context, t *asynq.Task) error {
	var payload types.WorkflowRunPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		// Platform convention: never retry on unparseable payloads.
		logger.Errorf(ctx, "workflow run payload unmarshal failed: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	run, err := s.repo.GetWorkflowRunByIDAndTenant(ctx, payload.RunID, payload.TenantID)
	if err != nil {
		// Row vanished (deleted?) — nothing to drive, not an infra fault.
		logger.Errorf(ctx, "[workflow:%s] run %s lookup failed: %v", payload.WorkflowID, payload.RunID, err)
		return nil
	}
	if payload.Resume {
		// Resume re-delivery: only a row still in failed state may be
		// re-driven (the resume endpoint validated this at enqueue time;
		// anything else — already picked up, cancelled meanwhile — no-ops).
		if run.Status != types.WorkflowRunStatusFailed {
			logger.Infof(ctx, "[workflow:%s] resume of run %s skipped (status=%s)",
				payload.WorkflowID, payload.RunID, run.Status)
			return nil
		}
	} else if run.Status != types.WorkflowRunStatusPending {
		logger.Infof(ctx, "[workflow:%s] run %s already %s, skipping re-delivery",
			payload.WorkflowID, payload.RunID, run.Status)
		return nil
	}

	wf, err := s.repo.GetWorkflowByIDAndTenant(ctx, payload.WorkflowID, payload.TenantID)
	if err != nil {
		// Workflow gone between enqueue and execution: fail the run row.
		s.failWorkflowRun(ctx, run, fmt.Errorf("workflow %s not found: %w", payload.WorkflowID, err))
		return nil
	}
	normalized, nerr := s.normalizeWorkflowDSL(wf)
	if nerr != nil {
		s.failWorkflowRun(ctx, run, nerr)
		return nil
	}
	req := &types.RunWorkflowRequest{Query: payload.Query, Files: payload.Files, Inputs: payload.Inputs, Async: true}
	// Execution errors are already persisted as the run's terminal state.
	_ = s.executeWorkflowRun(ctx, run, wf, normalized, req)
	return nil
}

// normalizeDSLBytes unmarshals and normalizes a raw DSL document (the
// draft or a published snapshot). Shape errors are ErrWorkflowInvalidDSL.
func (s *workflowService) normalizeDSLBytes(raw types.JSON) (*wfengine.DSL, error) {
	var dsl wfengine.DSL
	if err := json.Unmarshal(raw, &dsl); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}
	normalized, err := wfengine.Normalize(&dsl)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}
	return normalized, nil
}

// normalizeWorkflowDSL unmarshals and normalizes the stored DSL document.
// Shape errors are ErrWorkflowInvalidDSL (400 semantics, no run row).
func (s *workflowService) normalizeWorkflowDSL(wf *types.Workflow) (*wfengine.DSL, error) {
	return s.normalizeDSLBytes(dslForRun(wf))
}

// answerStreamSource reports the node id whose LLM deltas are the live
// answer: the terminal Answer node's template must be EXACTLY one
// {node@param} reference — mixed templates cannot stream coherently
// (literals and other refs render only at completion).
var answerSingleRef = regexp.MustCompile(`^\{([a-zA-Z0-9_-]+)@[a-zA-Z0-9_.-]+\}$`)

func answerStreamSource(normalized *wfengine.DSL) string {
	for _, comp := range normalized.Components {
		if len(comp.Downstream) != 0 || !strings.EqualFold(comp.Obj.ComponentName, nodes.ComponentAnswer) {
			continue
		}
		tmpl, _ := comp.Obj.Params["template"].(string)
		m := answerSingleRef.FindStringSubmatch(strings.TrimSpace(tmpl))
		if m == nil {
			return ""
		}
		// Only an LLM node streams; anything else (retrieval etc.) has no
		// deltas to forward.
		if src, ok := normalized.Components[m[1]]; ok && strings.EqualFold(src.Obj.ComponentName, nodes.ComponentLLM) {
			return m[1]
		}
		return ""
	}
	return ""
}

// executeWorkflowRun drives a freshly created (pending) run row to a
// terminal state: pending→running→succeeded|failed. Every node lifecycle
// frame is logged, published to the per-run broker (SSE subscribers) and
// emitted to the global event bus (observability); the terminal frame
// closes subscriber channels. Returns the execution error (already
// persisted) so the sync caller can surface it.
func (s *workflowService) executeWorkflowRun(
	ctx context.Context,
	run *types.WorkflowRun,
	wf *types.Workflow,
	normalized *wfengine.DSL,
	req *types.RunWorkflowRequest,
) error {
	run.Status = types.WorkflowRunStatusRunning
	if err := s.repo.UpdateWorkflowRun(ctx, run); err != nil {
		return err
	}

	// Trace accumulator: one entry per terminal node frame, execution
	// order. Persisted on the run row at both terminal paths so the
	// run-detail panel can replay any attempt (replayed nodes included —
	// the engine emits Replayed frames for checkpoint resume).
	trace := make([]types.WorkflowRunTraceEntry, 0, 8)
	traceMu := sync.Mutex{}
	appendTrace := func(entry types.WorkflowRunTraceEntry) {
		traceMu.Lock()
		defer traceMu.Unlock()
		trace = append(trace, entry)
	}
	traceJSON := func() types.JSON {
		traceMu.Lock()
		defer traceMu.Unlock()
		if len(trace) == 0 {
			return nil
		}
		data, err := json.Marshal(trace)
		if err != nil {
			return nil
		}
		return types.JSON(data)
	}
	// nodeKind resolves a node id to its component_name for trace entries;
	// unknown ids (should not happen) degrade to "".
	nodeKind := func(nodeID string) string {
		if comp, ok := normalized.Components[nodeID]; ok {
			return comp.Obj.ComponentName
		}
		return ""
	}

	// answerSourceID: when the terminal node is an Answer whose template is
	// exactly one {node@param} ref, that node's LLM deltas are the live
	// answer stream (Dify-style pass-through); empty = no answer streaming.
	answerSourceID := answerStreamSource(normalized)

	publishNode := func(ev wfengine.NodeEvent) {
		ev = capTraceOutputs(ev)
		if ev.Phase == wfengine.PhaseDelta {
			// Best-effort live deltas: no logging (spam), no trace (terminal
			// phases carry the full content), no event-bus mirror.
			frame := types.WorkflowRunEvent{
				WorkflowID: wf.ID,
				RunID:      run.ID,
				Kind:       "delta",
				NodeID:     ev.NodeID,
				Phase:      string(ev.Phase),
				Content:    ev.Content,
			}
			if ev.NodeID == answerSourceID {
				frame.Stream = "answer"
			}
			s.runs.publish(frame)
			s.publishFrameRedis(ctx, frame)
			return
		}
		logger.Infof(ctx, "[workflow:%s run:%s] node %s %s (%dms)",
			wf.ID, run.ID, ev.NodeID, ev.Phase, ev.DurationMS)
		frame := types.WorkflowRunEvent{
			WorkflowID: wf.ID,
			RunID:      run.ID,
			Kind:       "node",
			NodeID:     ev.NodeID,
			Phase:      string(ev.Phase),
			DurationMS: ev.DurationMS,
			Outputs:    ev.Outputs,
			Replayed:   ev.Replayed,
		}
		if ev.Err != nil {
			frame.Err = ev.Err.Error()
		}
		s.runs.publish(frame)
		s.publishFrameRedis(ctx, frame)
		_ = event.Emit(ctx, event.Event{
			Type:      event.EventWorkflowNode,
			SessionID: run.ID,
			Data:      frame,
		})
		// Terminal node phases land in the persisted trace.
		if ev.Phase == wfengine.PhaseFinished || ev.Phase == wfengine.PhaseFailed {
			appendTrace(types.WorkflowRunTraceEntry{
				NodeID:     ev.NodeID,
				Kind:       nodeKind(ev.NodeID),
				Phase:      string(ev.Phase),
				DurationMS: ev.DurationMS,
				Outputs:    ev.Outputs,
				Err:        frame.Err,
				Replayed:   ev.Replayed,
			})
		}
	}

	// Run attachments: files uploaded for this run resolve into extra LLM
	// context (same BuildPrompt formatting chat uses). The wrappers resolve
	// lazily and at most once per run, then delegate to the plain adapters.
	llmFunc := s.runLLM
	llmStreamFunc := s.runLLMStream
	agentFunc := s.runAgent
	if len(req.Files) > 0 {
		scope := WorkflowAttachmentScope(wf.ID)
		llmFunc = func(ctx context.Context, r nodes.LLMRequest) (string, error) {
			return s.runLLMWithAttachments(ctx, scope, req.Query, req.Files, r)
		}
		llmStreamFunc = func(ctx context.Context, r nodes.LLMRequest, onDelta func(string)) (string, error) {
			return s.runLLMStreamWithAttachments(ctx, scope, req.Query, req.Files, r, onDelta)
		}
		agentFunc = func(ctx context.Context, r nodes.AgentRequest) (string, error) {
			return s.runAgentWithAttachments(ctx, scope, req.Query, req.Files, r)
		}
	}
	compiled, cerr := wfengine.Compile(normalized, wfengine.Deps{
		LLMFunc:       llmFunc,
		LLMStreamFunc: llmStreamFunc,
		RetrievalFunc: s.runRetrieval,
		HTTPFunc:      s.runHTTP,
		DataOpsFunc:   s.runDataOps,
		WebSearchFunc: s.runWebSearch,
		CodeFunc:      s.runCode,
		AgentFunc:     agentFunc,
		MCPFunc:       s.runMCPTool,
		OnNodeEvent:   publishNode,
		// Checkpoint persistence (full mode only): eino persists completed-
		// node state per run, and the engine keeps a CanvasState side-car —
		// together these make a failed/timed-out run resumable via
		// POST /:id/runs/:run_id/resume. Lite mode: nil, runs stay fresh.
		CheckpointKV:  s.ckptKV,
		CheckpointTTL: workflowCheckpointTTL,
	})
	if cerr != nil {
		s.failWorkflowRunWithTrace(ctx, run, cerr, traceJSON)
		return cerr
	}

	runCtx, cancel := context.WithTimeout(ctx, workflowRunTimeout)
	// Expose the run's abort handle so CancelWorkflowRun can stop this
	// execution in-process. Registered for the whole run; engine propagation
	// then flows runCtx -> node Invoke -> adapters.
	s.cancels.register(run.ID, cancel)
	defer func() {
		s.cancels.unregister(run.ID)
		cancel()
	}()
	result, rerr := compiled.RunWithOptions(runCtx, req.Query, req.Files, wfengine.RunOptions{
		// Checkpoint resume identity = run id. First attempts persist per-
		// node side-cars (crash/timeout-safe); a later ResumeWorkflowRun
		// re-executes with the same id and completed nodes are replayed, not
		// re-invoked. Lite mode (nil KV) degrades to fresh runs.
		CheckpointID: run.ID,
		Inputs:       req.Inputs,
	})
	if rerr != nil {
		s.failWorkflowRunWithTrace(ctx, run, rerr, traceJSON)
		return rerr
	}

	outDoc := map[string]any{
		"answer":  result.Answer,
		"path":    result.Path,
		"outputs": result.Outputs,
	}
	outJSON, merr := json.Marshal(outDoc)
	if merr != nil {
		s.failWorkflowRunWithTrace(ctx, run, merr, traceJSON)
		return merr
	}
	if s.runAlreadyCancelled(ctx, run) {
		// Cancel raced the successful completion — cancelled wins; the
		// outputs are discarded with the row (rerun is the recovery path).
		logger.Infof(ctx, "[workflow:%s] run %s success suppressed: row already cancelled", wf.ID, run.ID)
		return fmt.Errorf("workflow run %s cancelled", run.ID)
	}
	run.Status = types.WorkflowRunStatusSucceeded
	run.Output = types.JSON(outJSON)
	run.Trace = traceJSON()
	// A resumed run may carry the failed attempt's error text; success is
	// terminal and the row must not keep advertising the old failure (the
	// map-based repo update persists "" verbatim).
	run.Error = ""
	if uerr := s.repo.UpdateWorkflowRun(ctx, run); uerr != nil {
		logger.Errorf(ctx, "workflow run %s terminal update failed: %v", run.ID, uerr)
		return uerr
	}
	s.emitRunFinished(ctx, run, types.WorkflowRunStatusSucceeded, "")
	logger.Infof(ctx, "[workflow:%s] run %s succeeded", wf.ID, run.ID)
	return nil
}

// failWorkflowRun persists the terminal failed state of a run and emits the
// terminal run event. A row already flipped to cancelled (user cancel
// racing the failure) is left alone — cancelled is also terminal and the
// cancel path already closed SSE subscribers.
func (s *workflowService) failWorkflowRun(ctx context.Context, run *types.WorkflowRun, cause error) {
	s.failWorkflowRunWithTrace(ctx, run, cause, nil)
}

// failWorkflowRunWithTrace is failWorkflowRun with the run's accumulated
// node trace (nil traceFn for failures before any node executed — enqueue,
// compile — where there is nothing to record).
func (s *workflowService) failWorkflowRunWithTrace(ctx context.Context, run *types.WorkflowRun, cause error, traceJSON func() types.JSON) {
	if s.runAlreadyCancelled(ctx, run) {
		logger.Infof(ctx, "[workflow:%s] run %s failure suppressed: row already cancelled (%v)",
			run.WorkflowID, run.ID, cause)
		return
	}
	run.Status = types.WorkflowRunStatusFailed
	run.Error = cause.Error()
	if traceJSON != nil {
		run.Trace = traceJSON()
	}
	if err := s.repo.UpdateWorkflowRun(ctx, run); err != nil {
		logger.Errorf(ctx, "workflow run %s failure update failed: %v", run.ID, err)
	}
	s.emitRunFinished(ctx, run, types.WorkflowRunStatusFailed, cause.Error())
}

// runAlreadyCancelled re-reads the run row and reports whether a cancel
// landed while the engine was aborting. Terminal-write suppression relies
// on this instead of comparing in-memory state, because CancelWorkflowRun
// may run on a different request goroutine (and, for async runs, a
// different instance entirely).
func (s *workflowService) runAlreadyCancelled(ctx context.Context, run *types.WorkflowRun) bool {
	cur, err := s.repo.GetWorkflowRunByIDAndTenant(ctx, run.ID, run.TenantID)
	if err != nil {
		// Read failure: fall through to the plain write — the state-guarded
		// MarkWorkflowRunCancelled is the hard barrier; this is a soft check.
		return false
	}
	return cur.Status == types.WorkflowRunStatusCancelled
}

// emitRunFinished delivers the terminal frame to SSE subscribers (closing
// their channels) and mirrors it onto the global event bus.
func (s *workflowService) emitRunFinished(ctx context.Context, run *types.WorkflowRun, status, errText string) {
	frame := types.WorkflowRunEvent{
		WorkflowID: run.WorkflowID,
		RunID:      run.ID,
		Kind:       "run",
		Phase:      status,
		Status:     status,
		Err:        errText,
	}
	s.runs.publishTerminal(frame)
	s.publishFrameRedis(ctx, frame)
	_ = event.Emit(ctx, event.Event{
		Type:      event.EventWorkflowRunFinished,
		SessionID: run.ID,
		Data:      frame,
	})
}

// workflowRunRedisChannel is the per-run pubsub channel bridging frames
// between instances: workflow:run:{run_id}.
func workflowRunRedisChannel(runID string) string {
	return "workflow:run:" + runID
}

// publishFrameRedis mirrors one frame onto the run's redis channel so SSE
// clients connected to OTHER instances observe the same progress. Lite mode
// (nil client) is a no-op; publish errors are logged, never propagated —
// the local broker and the run row remain the sources of truth.
func (s *workflowService) publishFrameRedis(ctx context.Context, frame types.WorkflowRunEvent) {
	if s.redis == nil {
		return
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		logger.Errorf(ctx, "workflow run frame marshal failed: %v", err)
		return
	}
	if _, err := s.redis.Publish(ctx, workflowRunRedisChannel(frame.RunID), payload).Result(); err != nil {
		logger.Warnf(ctx, "workflow run frame redis publish failed (run=%s): %v", frame.RunID, err)
	}
}

// workflowRunCancels tracks the context.CancelFunc of every run currently
// executing in THIS process, keyed by run id. CancelWorkflowRun uses it to
// abort in-process executions; async runs executing on another instance are
// handled by the row-level guard alone (the idempotent asynq handler skips
// non-pending rows, so a cancelled row never re-executes).
type workflowRunCancels struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func newWorkflowRunCancels() *workflowRunCancels {
	return &workflowRunCancels{m: make(map[string]context.CancelFunc)}
}

func (c *workflowRunCancels) register(runID string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[runID] = cancel
}

func (c *workflowRunCancels) unregister(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, runID)
}

// cancel aborts the run's execution context if it executes here. Returns
// whether an in-process execution was signalled.
func (c *workflowRunCancels) cancel(runID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.m[runID]; ok {
		cancel()
		return true
	}
	return false
}

// CancelWorkflowRun best-effort cancels a pending/running run.
//
// Idempotency choice: cancelling an already-terminal run returns the current
// row with 200 instead of 409. The row is the source of truth (same
// philosophy as TaskInspector), and concurrent cancel-vs-finish races must
// not turn into client-facing conflicts.
func (s *workflowService) CancelWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	run, err := s.repo.GetWorkflowRunByIDAndTenant(ctx, runID, tenantID)
	if err != nil {
		return nil, err
	}
	if run.WorkflowID != workflowID {
		return nil, apprepo.ErrWorkflowNotFound
	}
	if run.Status != types.WorkflowRunStatusPending && run.Status != types.WorkflowRunStatusRunning {
		logger.Infof(ctx, "[workflow:%s] cancel of terminal run %s (status=%s) is a no-op",
			workflowID, runID, run.Status)
		return run, nil
	}
	if err := s.repo.MarkWorkflowRunCancelled(ctx, runID, tenantID); err != nil {
		if errors.Is(err, apprepo.ErrWorkflowRunNotCancellable) {
			// Lost the race against a terminal write — surface the winner.
			return s.repo.GetWorkflowRunByIDAndTenant(ctx, runID, tenantID)
		}
		return nil, err
	}
	inProcess := s.cancels.cancel(runID)
	run.Status = types.WorkflowRunStatusCancelled
	// Close SSE subscribers with the cancelled terminal frame; the in-process
	// execution's own terminal write is suppressed by the cancelled-row guard.
	s.emitRunFinished(ctx, run, types.WorkflowRunStatusCancelled, "")
	logger.Infof(ctx, "[workflow:%s] run %s cancelled (in_process=%v)", workflowID, runID, inProcess)
	return run, nil
}

// ErrWorkflowRunNotResumable maps to HTTP 409: only failed runs resume.
// Cancelled is deliberately excluded — a user-initiated cancel is an
// explicit "don't continue this attempt"; the recovery path for it is a
// NEW run, not a resume. Succeeded runs obviously need nothing.
var ErrWorkflowRunNotResumable = errors.New("workflow run is not resumable (only failed runs resume)")

// ResumeWorkflowRun re-drives a FAILED run from its checkpoint: completed
// nodes are replayed from the persisted CanvasState side-car instead of
// re-executed, and the attempt that failed re-runs (see engine
// RunWithOptions). The row keeps status=failed until a worker picks the
// task up — failed is the resumable marker, there is no transient pending
// state that could be mistaken for a fresh run.
func (s *workflowService) ResumeWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	run, err := s.repo.GetWorkflowRunByIDAndTenant(ctx, runID, tenantID)
	if err != nil {
		return nil, err
	}
	if run.WorkflowID != workflowID {
		return nil, apprepo.ErrWorkflowNotFound
	}
	if run.Status != types.WorkflowRunStatusFailed {
		return nil, fmt.Errorf("%w (status=%s)", ErrWorkflowRunNotResumable, run.Status)
	}
	// Same run gate as fresh runs: unpublished workflows resume only for
	// the creator/admin (the worker executes without a user context).
	wf, err := s.repo.GetWorkflowByIDAndTenant(ctx, workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	if wf.Status != types.WorkflowStatusPublished && !canDebugUnpublished(ctx, wf) {
		return nil, ErrWorkflowNotDebuggable
	}

	// Original inputs: the run's Input document is the marshalled
	// RunWorkflowRequest from the first attempt.
	var orig types.RunWorkflowRequest
	if len(run.Input) > 0 {
		if uerr := json.Unmarshal(run.Input, &orig); uerr != nil {
			return nil, fmt.Errorf("%w: input document unparseable: %v", ErrWorkflowRunNotResumable, uerr)
		}
	}

	// Fail fast on a workflow whose DSL no longer compiles BEFORE handing
	// the row to the queue (400 semantics, row untouched).
	if _, nerr := s.normalizeWorkflowDSL(wf); nerr != nil {
		return nil, nerr
	}

	payload, merr := json.Marshal(types.WorkflowRunPayload{
		RunID:      run.ID,
		WorkflowID: workflowID,
		TenantID:   tenantID,
		Query:      orig.Query,
		Files:      orig.Files,
		Inputs:     orig.Inputs,
		Resume:     true,
	})
	if merr != nil {
		return nil, merr
	}
	task := asynq.NewTask(types.TypeWorkflowRun, payload,
		asynq.Queue(types.QueueDefault),
		asynq.Timeout(workflowRunTimeout+30*time.Second),
		asynq.MaxRetry(2),
	)
	if _, eerr := s.enqueuer.Enqueue(task); eerr != nil {
		return run, eerr
	}
	logger.Infof(ctx, "[workflow:%s] run %s resume enqueued (async, checkpoint=%s)",
		workflowID, runID, func() string {
			if s.ckptKV != nil {
				return runID
			}
			return "none (lite mode: fresh run)"
		}())
	return run, nil
}

// GetWorkflowRun returns one run of a workflow in the caller's tenant.
func (s *workflowService) GetWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	run, err := s.repo.GetWorkflowRunByIDAndTenant(ctx, runID, tenantID)
	if err != nil {
		return nil, err
	}
	if run.WorkflowID != workflowID {
		// Same sentinel the repo uses, so handlers map it to 404 uniformly.
		return nil, apprepo.ErrWorkflowNotFound
	}
	return run, nil
}

// SubscribeWorkflowRunEvents attaches a live feed to one run's frames.
func (s *workflowService) SubscribeWorkflowRunEvents(runID string) (<-chan types.WorkflowRunEvent, func()) {
	localCh, localCancel := s.runs.subscribe(runID)
	if s.redis == nil {
		// Lite mode: process-local broker only.
		return localCh, localCancel
	}

	// Full mode: merge the local broker with the run's redis channel. The
	// same frame can arrive twice when the run executes on THIS instance
	// (local publish + redis echo), so frames are deduplicated by
	// kind|node|phase|duration — bounded, progress frames carry no payload
	// differences worth finer keys.
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := s.redis.Subscribe(ctx, workflowRunRedisChannel(runID))
	out := make(chan types.WorkflowRunEvent, brokerChanSize)
	var once sync.Once
	seen := make(map[string]struct{})
	var mu sync.Mutex

	closeOut := func() {
		once.Do(func() {
			cancel()
			_ = pubsub.Close()
			close(out)
		})
	}

	deliver := func(frame types.WorkflowRunEvent) {
		// Dedup key: kind|node|phase|duration, plus content for delta frames —
		// sequential deltas from one node share every other field, and the
		// local+redis echo carries identical content. ponytail: two consecutive
		// IDENTICAL delta chunks from one node would collide (a repeated token
		// dropped); add per-run sequence numbers if that ever matters.
		key := frame.Kind + "|" + frame.NodeID + "|" + frame.Phase + "|" + strconv.FormatInt(frame.DurationMS, 10)
		if frame.Kind == "delta" {
			key += "|" + frame.Content
		}
		mu.Lock()
		if _, dup := seen[key]; dup {
			mu.Unlock()
			return
		}
		seen[key] = struct{}{}
		mu.Unlock()
		if frame.Kind == "run" {
			// Terminal frame always reaches the subscriber, then closes.
			select {
			case out <- frame:
			case <-ctx.Done():
				return
			}
			closeOut()
			return
		}
		select {
		case out <- frame:
		default:
			// Best-effort progress; the run row is durable state.
		}
	}

	go func() {
		for frame := range localCh {
			deliver(frame)
		}
	}()
	go func() {
		for msg := range pubsub.Channel() {
			var frame types.WorkflowRunEvent
			if err := json.Unmarshal([]byte(msg.Payload), &frame); err != nil {
				continue
			}
			deliver(frame)
		}
	}()

	stop := func() {
		localCancel()
		closeOut()
	}
	return out, stop
}

// runWebSearch adapts the engine's WebSearchFunc onto the platform search
// service. Provider resolution: the node's provider_id param wins; empty
// falls back to the tenant's default provider. No provider configured →
// loud error naming the fix (configure one in settings).
func (s *workflowService) runWebSearch(ctx context.Context, req nodes.WebSearchRequest) ([]nodes.WebSearchResultItem, error) {
	if s.webSearch == nil {
		return nil, errors.New("workflow WebSearch: search service unavailable")
	}
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		if s.webSearchProviders == nil {
			return nil, errors.New("workflow WebSearch: no provider configured for this node and no default provider lookup available")
		}
		tenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || tenantID == 0 {
			return nil, ErrWorkflowTenantRequired
		}
		def, err := s.webSearchProviders.GetDefault(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("workflow WebSearch: default provider lookup failed: %w", err)
		}
		if def == nil {
			return nil, errors.New("workflow WebSearch: no search provider configured (set one as default in settings or pin provider_id on the node)")
		}
		providerID = def.ID
	}
	config := types.EffectiveWebSearchConfig(nil)
	if req.MaxResults > 0 {
		config.MaxResults = req.MaxResults
	}
	results, err := s.webSearch.Search(ctx, providerID, config, req.Query)
	if err != nil {
		return nil, fmt.Errorf("workflow WebSearch: search failed: %w", err)
	}
	items := make([]nodes.WebSearchResultItem, 0, len(results))
	for _, r := range results {
		if r == nil {
			continue
		}
		items = append(items, nodes.WebSearchResultItem{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	return items, nil
}

// codeInputEnvVar carries the rendered variables object into the sandbox.
// Env, not stdin: stdin passes the injection validator and arbitrary user
// variable values could trip it on false positives.
const codeInputEnvVar = "WEKNORA_WORKFLOW_INPUT"

// runCode adapts the engine's CodeFunc onto the tenant's sandbox backend.
// Empty SessionID selects the ephemeral one-shot sandbox (allocated for this
// script, torn down right after), so workflow Code nodes never touch the
// session-persistent sandboxes skill runs use. The script must print a JSON
// object; every key becomes a node output.
func (s *workflowService) runCode(ctx context.Context, req nodes.CodeRequest) (map[string]any, error) {
	if s.sandboxes == nil {
		return nil, errors.New("workflow Code: no sandbox backend configured for workflows")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	manager, err := s.sandboxes.Resolve(ctx, tenantID, "")
	if err != nil {
		return nil, fmt.Errorf("workflow Code: sandbox resolve failed: %w", err)
	}
	ext := ".py"
	if req.Language == "node" {
		ext = ".js"
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 || timeout > 120 {
		timeout = 120 // bounded by the run cap anyway
	}
	result, err := manager.Execute(ctx, &sandbox.ExecuteConfig{
		Script:        "workflow_code" + ext, // basename → upload name + interpreter
		ScriptContent: req.Code,
		// SessionID empty = ephemeral sandbox, created and disposed per run.
		Env:     map[string]string{codeInputEnvVar: req.InputJSON},
		Timeout: time.Duration(timeout) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow Code: execution failed: %w", err)
	}
	if !result.IsSuccess() {
		return nil, fmt.Errorf("workflow Code: script failed (exit %d): %s", result.ExitCode, tail(result.Stderr, 400))
	}
	out := lastJSONObject(result.Stdout)
	if out == nil {
		return nil, fmt.Errorf("workflow Code: script printed no JSON object (stdout: %s)", tail(result.Stdout, 200))
	}
	return out, nil
}

// lastJSONObject returns the final complete top-level {...} object in s
// (quote- and escape-aware brace matching), or nil. Scripts may print
// progress lines — and nested objects — before the result object.
func lastJSONObject(s string) map[string]any {
	var best string
	depth := 0
	inString, escaped := false, false
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					best = s[start : i+1]
				}
			}
		}
	}
	if best == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(best), &out); err != nil {
		return nil
	}
	return out
}

// tail returns the last n bytes of s (for bounded error messages).
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// runAgent adapts the engine's AgentFunc onto the platform ReAct engine via
// the ready-made programmatic entry (AgentService.CreateAgentEngine — same
// surface tenant_skill_install.go uses outside the chat path). One stateless
// turn: synthetic session/message ids, no history, no session or message
// persistence; the engine's event bus is a throwaway with no subscribers.
func (s *workflowService) runAgent(ctx context.Context, req nodes.AgentRequest) (string, error) {
	if s.agents == nil || s.agents.Get() == nil {
		return "", errors.New("workflow Agent: agent runtime unavailable")
	}
	if s.models == nil {
		return "", errors.New("workflow Agent: model service unavailable")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return "", ErrWorkflowTenantRequired
	}
	modelID := strings.TrimSpace(req.Model)
	if modelID == "" {
		fallback, ferr := s.defaultChatModelID(ctx)
		if ferr != nil {
			return "", ferr
		}
		modelID = fallback
	}
	chatModel, err := s.models.GetChatModel(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("workflow Agent: model %q unavailable: %w", modelID, err)
	}

	cfg := &types.AgentConfig{
		MaxIterations: 5,
		SystemPrompt:  req.SystemPrompt,
		Temperature:   req.Temperature,
	}
	if len(req.KBIDs) > 0 {
		cfg.AllowedTools = []string{"knowledge_search"}
		for _, kbID := range req.KBIDs {
			cfg.SearchTargets = append(cfg.SearchTargets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: kbID,
				TenantID:        tenantID,
			})
		}
	}
	// A throwaway event bus keeps engine-internal streaming a no-op.
	engine, err := s.agents.Get().CreateAgentEngine(ctx, cfg, chatModel, nil, event.NewEventBus(), "workflow-agent-node", "")
	if err != nil {
		return "", fmt.Errorf("workflow Agent: engine setup failed: %w", err)
	}
	if engine == nil {
		return "", errors.New("workflow Agent: engine setup returned no engine")
	}
	// llmContext nil = fresh single turn; synthetic ids are logging metadata only.
	state, err := engine.Execute(ctx, "workflow-agent-node", "workflow-agent-node", req.Prompt, nil)
	if err != nil {
		return "", fmt.Errorf("workflow Agent: turn failed: %w", err)
	}
	if state == nil || strings.TrimSpace(state.FinalAnswer) == "" {
		return "", errors.New("workflow Agent: turn produced no final answer")
	}
	return state.FinalAnswer, nil
}

// runMCPTool adapts the engine's MCPFunc onto the tenant MCP manager.
// Interactive OAuth services are rejected loudly (a workflow node has no
// consent UI); none/api_key/bearer auth call synchronously.
func (s *workflowService) runMCPTool(ctx context.Context, req nodes.MCPToolRequest) (any, string, error) {
	if s.mcpClients == nil || s.mcpServices == nil {
		return nil, "", errors.New("workflow MCPTool: MCP runtime unavailable")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, "", ErrWorkflowTenantRequired
	}
	services, err := s.mcpServices.ListMCPServicesByIDs(ctx, tenantID, []string{req.ServiceID})
	if err != nil {
		return nil, "", fmt.Errorf("workflow MCPTool: service lookup failed: %w", err)
	}
	var service *types.MCPService
	for _, svc := range services {
		if svc != nil && svc.ID == req.ServiceID {
			service = svc
			break
		}
	}
	if service == nil {
		return nil, "", fmt.Errorf("workflow MCPTool: MCP service %q not found in this workspace", req.ServiceID)
	}
	if !service.Enabled {
		return nil, "", fmt.Errorf("workflow MCPTool: MCP service %q is disabled", service.Name)
	}
	if service.AuthConfig.IsOAuth() {
		return nil, "", fmt.Errorf("workflow MCPTool: service %q uses OAuth and needs interactive authorization — workflow nodes only support none/api_key/bearer auth", service.Name)
	}

	args := map[string]any{}
	if trimmed := strings.TrimSpace(req.ArgsJSON); trimmed != "" && trimmed != "{}" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return nil, "", fmt.Errorf("workflow MCPTool: args must render to a JSON object: %w", err)
		}
	}
	if req.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	client, err := s.mcpClients.GetOrCreateClient(ctx, service)
	if err != nil {
		return nil, "", fmt.Errorf("workflow MCPTool: connect to %q failed: %w", service.Name, err)
	}
	result, err := client.CallTool(ctx, req.Tool, args)
	if err != nil {
		return nil, "", fmt.Errorf("workflow MCPTool: call %q failed: %w", req.Tool, err)
	}
	if result.IsError {
		return nil, "", fmt.Errorf("workflow MCPTool: tool %q returned an error: %s", req.Tool, mcpContentText(result.Content))
	}
	raw := mcpContentRaw(result.Content)
	return raw, mcpContentText(result.Content), nil
}

// mcpContentText renders the textual parts of an MCP tool result.
func mcpContentText(items []mcp.ContentItem) string {
	var b strings.Builder
	for _, item := range items {
		if item.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(item.Text)
		}
	}
	return b.String()
}

// mcpContentRaw maps content items to plain values for {node@result} refs.
func mcpContentRaw(items []mcp.ContentItem) any {
	texts := make([]any, 0, len(items))
	for _, item := range items {
		if item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	if len(texts) == 1 {
		return texts[0]
	}
	return texts
}

// mcpClientProvider is the one-method slice of *mcp.MCPManager the
// MCPTool adapter needs (test seam; production passes the manager).
type mcpClientProvider interface {
	GetOrCreateClient(ctx context.Context, service *types.MCPService) (mcp.MCPClient, error)
}

// WorkflowAttachmentScope derives the temporary-document session scope for
// a workflow's run attachments. Deterministic on both the upload side (the
// run-attachments endpoints) and the resolve side (run execution), so no
// real chat session is needed.
//
// The temporary_documents.session_id column is VARCHAR(36) (sized for chat
// session UUIDs), so the scope compacts the workflow UUID: "wf-" + the 32
// hex chars (dashes stripped) = 35 chars. Uniqueness is preserved (same
// UUID, minus cosmetic dashes).
func WorkflowAttachmentScope(workflowID string) string {
	return "wf-" + strings.ReplaceAll(workflowID, "-", "")
}

// attachmentPrompt resolves run files into the prompt section prepended to
// the system message. Errors surface loudly: silently dropping attached
// documents would produce confidently wrong answers.
func (s *workflowService) attachmentPrompt(ctx context.Context, scope, query string, files []string) (string, error) {
	if s.tempDocs == nil {
		return "", errors.New("workflow: run attachments are not available on this deployment")
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	res, err := s.tempDocs.ResolveForPrompt(ctx, tenantID, scope, files, query)
	if err != nil {
		return "", fmt.Errorf("workflow: resolve run attachments: %w", err)
	}
	if len(res.Attachments) == 0 {
		return "", nil
	}
	atts := make(types.MessageAttachments, 0, len(res.Attachments))
	atts = append(atts, res.Attachments...)
	return atts.BuildPrompt(), nil
}

func (s *workflowService) runLLMWithAttachments(ctx context.Context, scope, query string, files []string, req nodes.LLMRequest) (string, error) {
	extra, aerr := s.attachmentPrompt(ctx, scope, query, files)
	if aerr != nil {
		return "", aerr
	}
	if extra != "" {
		req.SystemPrompt = strings.Join(nonEmpty(req.SystemPrompt, extra), "\n\n")
	}
	return s.runLLM(ctx, req)
}

func (s *workflowService) runLLMStreamWithAttachments(ctx context.Context, scope, query string, files []string, req nodes.LLMRequest, onDelta func(string)) (string, error) {
	extra, aerr := s.attachmentPrompt(ctx, scope, query, files)
	if aerr != nil {
		return "", aerr
	}
	if extra != "" {
		req.SystemPrompt = strings.Join(nonEmpty(req.SystemPrompt, extra), "\n\n")
	}
	return s.runLLMStream(ctx, req, onDelta)
}

func (s *workflowService) runAgentWithAttachments(ctx context.Context, scope, query string, files []string, req nodes.AgentRequest) (string, error) {
	extra, aerr := s.attachmentPrompt(ctx, scope, query, files)
	if aerr != nil {
		return "", aerr
	}
	if extra != "" {
		req.SystemPrompt = strings.Join(nonEmpty(req.SystemPrompt, extra), "\n\n")
	}
	return s.runAgent(ctx, req)
}

// nonEmpty filters empty strings.
func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runLLMStream is the streaming sibling of runLLM: identical resolution
// and message assembly, but consumes ChatStream and forwards each chunk to
// onDelta while accumulating the full text. Errors mid-stream fail the call
// (partial content is discarded — the run's error path takes over).
func (s *workflowService) runLLMStream(ctx context.Context, req nodes.LLMRequest, onDelta func(string)) (string, error) {
	modelID := strings.TrimSpace(req.Model)
	if modelID == "" {
		fallback, ferr := s.defaultChatModelID(ctx)
		if ferr != nil {
			return "", ferr
		}
		modelID = fallback
	}
	model, err := s.models.GetChatModel(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("workflow LLM model %q unavailable: %w", modelID, err)
	}
	msgs := make([]chat.Message, 0, 2)
	if req.SystemPrompt != "" {
		msgs = append(msgs, chat.Message{Role: "system", Content: req.SystemPrompt})
	}
	msgs = append(msgs, chat.Message{Role: "user", Content: req.Prompt})
	opts := &chat.ChatOptions{Temperature: req.Temperature, ExplicitTemperature: req.TemperatureSet, Thinking: req.Thinking}
	if req.MaxTokens > 0 {
		// MaxTokens (wire: max_tokens), not MaxCompletionTokens: most
		// OpenAI-compatible backends (Ollama, DeepSeek, vLLM, proxies)
		// ignore max_completion_tokens (#2604). OpenAI o-series/GPT-5 is
		// still covered — shapeOpenAIReasoning migrates max_tokens at
		// request-shaping time.
		opts.MaxTokens = req.MaxTokens
	}
	ch, err := model.ChatStream(ctx, msgs, opts)
	if err != nil {
		return "", fmt.Errorf("workflow LLM stream start failed: %w", err)
	}
	var answer strings.Builder
	var thinking strings.Builder
	var streamErr string
	for resp := range ch {
		if resp.Content == "" {
			continue
		}
		switch resp.ResponseType {
		case types.ResponseTypeError:
			// Mid-stream provider failures arrive as error frames; remember
			// the last one so a content-less stream fails with the real
			// cause instead of an opaque "produced no content".
			streamErr = resp.Content
		case types.ResponseTypeThinking:
			// Thinking track: never becomes node output directly, but keep
			// it — some mixed-routing backends occasionally stream the whole
			// reply (reasoning AND answer) through reasoning_content only.
			thinking.WriteString(resp.Content)
		case types.ResponseTypeAnswer, "":
			// The answer track: error/tool frames stay excluded.
			answer.WriteString(resp.Content)
			if onDelta != nil {
				onDelta(resp.Content)
			}
		}
	}
	if answer.Len() == 0 && streamErr != "" {
		// A provider-side failure beats both fallbacks: the partial thinking
		// text of a dead stream is not an answer.
		return "", fmt.Errorf("workflow LLM stream failed: %s", streamErr)
	}
	if answer.Len() == 0 && thinking.Len() > 0 {
		// Fallback: the provider misrouted everything into the thinking
		// track. Promote it (sans optional <think> wrapper) rather than
		// failing the node with no content.
		promoted := chat.StripLeadingThinkTags(strings.TrimSpace(thinking.String()))
		if promoted != "" {
			if onDelta != nil {
				onDelta(promoted)
			}
			return promoted, nil
		}
	}
	if answer.Len() == 0 {
		return "", errors.New("workflow LLM stream produced no content")
	}
	return answer.String(), nil
}

// runLLM adapts the engine's LLMFunc onto the platform ModelService.
// The node's model param may be empty: then the tenant's default chat
// (KnowledgeQA-type, is_default) model is used when one exists — the
// cheap opportunistic fallback, no schema involved. No cross-tenant path.
func (s *workflowService) runLLM(ctx context.Context, req nodes.LLMRequest) (string, error) {
	modelID := strings.TrimSpace(req.Model)
	if modelID == "" {
		fallback, ferr := s.defaultChatModelID(ctx)
		if ferr != nil {
			return "", ferr
		}
		modelID = fallback
	}
	model, err := s.models.GetChatModel(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("workflow LLM model %q unavailable: %w", modelID, err)
	}
	// System prompt first (when configured), then the rendered user prompt —
	// the same message shape the chat pipeline assembles for its LLM calls.
	msgs := make([]chat.Message, 0, 2)
	if req.SystemPrompt != "" {
		msgs = append(msgs, chat.Message{Role: "system", Content: req.SystemPrompt})
	}
	msgs = append(msgs, chat.Message{Role: "user", Content: req.Prompt})
	opts := &chat.ChatOptions{Temperature: req.Temperature, ExplicitTemperature: req.TemperatureSet, Thinking: req.Thinking}
	if req.MaxTokens > 0 {
		// See runLLMStream: max_tokens is the field OpenAI-compatible
		// backends actually honor.
		opts.MaxTokens = req.MaxTokens
	}
	resp, err := model.Chat(ctx, msgs, opts)
	if err != nil {
		return "", fmt.Errorf("workflow LLM call failed: %w", err)
	}
	return resp.Content, nil
}

// defaultChatModelID resolves the tenant's default chat model via the
// existing ListModels surface (models.is_default, no schema change).
// ListModels is tenant-scoped, so no other tenant's default can surface.
// When several rows carry is_default (legacy data), the most recently
// updated one wins — a documented tie-break, not a schema constraint.
func (s *workflowService) defaultChatModelID(ctx context.Context) (string, error) {
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("workflow LLM: default-model lookup failed: %w", err)
	}
	var best *types.Model
	for _, m := range models {
		if m == nil || !m.IsDefault || m.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if best == nil || m.UpdatedAt.After(best.UpdatedAt) {
			best = m
		}
	}
	if best != nil {
		return best.ID, nil
	}
	return "", errors.New("workflow LLM node requires a model id in its params (no default chat model configured for this workspace)")
}

// runRetrieval adapts the engine's RetrievalFunc onto the platform
// KnowledgeBaseService. Every KB is searched with the caller's tenant
// context, so cross-tenant KB ids fail closed inside HybridSearch.
func (s *workflowService) runRetrieval(ctx context.Context, req nodes.RetrievalRequest) (*nodes.RetrievalResult, error) {
	if len(req.KBIDs) == 0 {
		return nil, errors.New("workflow Retrieval node requires at least one kb_id")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}
	result := &nodes.RetrievalResult{Chunks: []map[string]any{}, DocAggs: []map[string]any{}}
	for _, kbID := range req.KBIDs {
		hits, err := s.kbs.HybridSearch(ctx, kbID, types.SearchParams{
			// Zero thresholds keep the retrievers' no-filter semantics
			// (pgvector treats 0 as "score >= 0", i.e. rank-only).
			QueryText:        req.Query,
			MatchCount:       topK,
			VectorThreshold:  req.VectorThreshold,
			KeywordThreshold: req.KeywordThreshold,
		})
		if err != nil {
			return nil, fmt.Errorf("workflow retrieval on kb %s failed: %w", kbID, err)
		}
		for _, h := range hits {
			result.Chunks = append(result.Chunks, map[string]any{
				"id":              h.ID,
				"content":         h.Content,
				"knowledge_id":    h.KnowledgeID,
				"knowledge_title": h.KnowledgeTitle,
				"chunk_index":     h.ChunkIndex,
				"score":           h.Score,
			})
		}
	}
	if req.UseRerank {
		if err := s.rerankChunks(ctx, req, result, topK); err != nil {
			return nil, err
		}
	}
	result.DocAggs = aggregateDocAggs(result.Chunks)
	return result, nil
}

// aggregateDocAggs folds the retrieved chunks into one row per source
// document (id, title, hit count, best score) — the "cited sources" view
// the chat pipeline shows; workflows expose it as {ret@doc_aggs}.
func aggregateDocAggs(chunks []map[string]any) []map[string]any {
	type agg struct {
		title string
		count int
		best  float64
	}
	order := []string{}
	byDoc := map[string]*agg{}
	for _, c := range chunks {
		id, _ := c["knowledge_id"].(string)
		title, _ := c["knowledge_title"].(string)
		score, _ := c["score"].(float64)
		// After rerank the chunk carries rerank_score; the aggregation's
		// "best" must reflect the ranking the workflow actually used.
		if rs, ok := c["rerank_score"].(float64); ok {
			score = rs
		}
		a, ok := byDoc[id]
		if !ok {
			a = &agg{title: title}
			byDoc[id] = a
			order = append(order, id)
		}
		a.count++
		if score > a.best {
			a.best = score
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		a := byDoc[id]
		out = append(out, map[string]any{
			"knowledge_id": id, "knowledge_title": a.title,
			"chunk_count": a.count, "score": a.best,
		})
	}
	return out
}

// rerankChunks reranks the merged hits of a multi-KB retrieval with the
// requested rerank model and trims to topK. HybridSearch itself never
// reranks (its callers do), so the workflow adapter applies the same
// pattern the chat pipeline uses: rerank the passage texts, then reorder
// by the returned index. Model resolution is tenant-scoped through
// ModelService, so a cross-tenant rerank model id fails closed.
func (s *workflowService) rerankChunks(ctx context.Context, req nodes.RetrievalRequest, result *nodes.RetrievalResult, topK int) error {
	if len(result.Chunks) == 0 {
		return nil
	}
	reranker, err := s.models.GetRerankModel(ctx, req.RerankModelID)
	if err != nil {
		return fmt.Errorf("workflow Retrieval: rerank model %q unavailable: %w", req.RerankModelID, err)
	}
	documents := make([]string, len(result.Chunks))
	for i, c := range result.Chunks {
		documents[i], _ = c["content"].(string)
	}
	ranked, err := reranker.Rerank(ctx, req.Query, documents)
	if err != nil {
		return fmt.Errorf("workflow Retrieval: rerank call failed: %w", err)
	}
	reordered := make([]map[string]any, 0, len(ranked))
	for _, r := range ranked {
		if r.Index < 0 || r.Index >= len(result.Chunks) {
			continue
		}
		chunk := result.Chunks[r.Index]
		chunk["rerank_score"] = r.RelevanceScore
		reordered = append(reordered, chunk)
	}
	if len(reordered) > topK {
		reordered = reordered[:topK]
	}
	result.Chunks = reordered
	return nil
}
