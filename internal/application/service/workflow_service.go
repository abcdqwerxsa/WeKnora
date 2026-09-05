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
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
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
)

// workflowDSLShape is the minimal structural view used to validate the DSL
// document without importing the engine package. It mirrors the dual-view
// contract: `components` carries the execution topology (required),
// `graph` the canvas layout (optional at the storage layer).
type workflowDSLShape struct {
	Version    int                                    `json:"version"`
	Components map[string]workflowDSLShapeComponent   `json:"components"`
	Graph      *workflowDSLShapeGraph                 `json:"graph,omitempty"`
	Variables  map[string]json.RawMessage             `json:"variables,omitempty"`
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

// workflowService implements interfaces.WorkflowService.
type workflowService struct {
	repo     interfaces.WorkflowRepository
	models   interfaces.ModelService
	kbs      interfaces.KnowledgeBaseService
	enqueuer interfaces.TaskEnqueuer
	runs     *workflowRunBroker
	cancels  *workflowRunCancels
}

// NewWorkflowService creates a new workflow service. models/kbs back the
// engine adapters injected into every compiled run (LLMFunc → ModelService
// GetChatModel, RetrievalFunc → KnowledgeBaseService HybridSearch);
// enqueuer backs the async run mode (asynq client in full mode, inline
// sync executor in Lite mode).
func NewWorkflowService(
	repo interfaces.WorkflowRepository,
	models interfaces.ModelService,
	kbs interfaces.KnowledgeBaseService,
	enqueuer interfaces.TaskEnqueuer,
) interfaces.WorkflowService {
	return &workflowService{
		repo:     repo,
		models:   models,
		kbs:      kbs,
		enqueuer: enqueuer,
		runs:     newWorkflowRunBroker(),
		cancels:  newWorkflowRunCancels(),
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

	normalized, err := s.normalizeWorkflowDSL(wf)
	if err != nil {
		return nil, err
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
	if run.Status != types.WorkflowRunStatusPending {
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
	req := &types.RunWorkflowRequest{Query: payload.Query, Files: payload.Files, Async: true}
	// Execution errors are already persisted as the run's terminal state.
	_ = s.executeWorkflowRun(ctx, run, wf, normalized, req)
	return nil
}

// normalizeWorkflowDSL unmarshals and normalizes the stored DSL document.
// Shape errors are ErrWorkflowInvalidDSL (400 semantics, no run row).
func (s *workflowService) normalizeWorkflowDSL(wf *types.Workflow) (*wfengine.DSL, error) {
	var dsl wfengine.DSL
	if err := json.Unmarshal(wf.DSL, &dsl); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}
	normalized, err := wfengine.Normalize(&dsl)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}
	return normalized, nil
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

	publishNode := func(ev wfengine.NodeEvent) {
		logger.Infof(ctx, "[workflow:%s run:%s] node %s %s (%dms)",
			wf.ID, run.ID, ev.NodeID, ev.Phase, ev.DurationMS)
		frame := types.WorkflowRunEvent{
			WorkflowID: wf.ID,
			RunID:      run.ID,
			Kind:       "node",
			NodeID:     ev.NodeID,
			Phase:      string(ev.Phase),
			DurationMS: ev.DurationMS,
		}
		if ev.Err != nil {
			frame.Err = ev.Err.Error()
		}
		s.runs.publish(frame)
		_ = event.Emit(ctx, event.Event{
			Type:      event.EventWorkflowNode,
			SessionID: run.ID,
			Data:      frame,
		})
	}

	compiled, cerr := wfengine.Compile(normalized, wfengine.Deps{
		LLMFunc:       s.runLLM,
		RetrievalFunc: s.runRetrieval,
		OnNodeEvent:   publishNode,
	})
	if cerr != nil {
		s.failWorkflowRun(ctx, run, cerr)
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
	result, rerr := compiled.Run(runCtx, req.Query, req.Files)
	if rerr != nil {
		s.failWorkflowRun(ctx, run, rerr)
		return rerr
	}

	outDoc := map[string]any{
		"answer":  result.Answer,
		"path":    result.Path,
		"outputs": result.Outputs,
	}
	outJSON, merr := json.Marshal(outDoc)
	if merr != nil {
		s.failWorkflowRun(ctx, run, merr)
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
	if s.runAlreadyCancelled(ctx, run) {
		logger.Infof(ctx, "[workflow:%s] run %s failure suppressed: row already cancelled (%v)",
			run.WorkflowID, run.ID, cause)
		return
	}
	run.Status = types.WorkflowRunStatusFailed
	run.Error = cause.Error()
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
	_ = event.Emit(ctx, event.Event{
		Type:      event.EventWorkflowRunFinished,
		SessionID: run.ID,
		Data:      frame,
	})
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
	return s.runs.subscribe(runID)
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
	resp, err := model.Chat(ctx, []chat.Message{{Role: "user", Content: req.Prompt}}, &chat.ChatOptions{
		Temperature: req.Temperature,
	})
	if err != nil {
		return "", fmt.Errorf("workflow LLM call failed: %w", err)
	}
	return resp.Content, nil
}

// defaultChatModelID resolves the tenant's default chat model via the
// existing ListModels surface (models.is_default, no schema change).
func (s *workflowService) defaultChatModelID(ctx context.Context) (string, error) {
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("workflow LLM: default-model lookup failed: %w", err)
	}
	for _, m := range models {
		if m != nil && m.IsDefault && m.Type == types.ModelTypeKnowledgeQA {
			return m.ID, nil
		}
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
			QueryText:  req.Query,
			MatchCount: topK,
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
	return result, nil
}
