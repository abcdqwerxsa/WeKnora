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
	"time"
	"unicode/utf8"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
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
	repo   interfaces.WorkflowRepository
	models interfaces.ModelService
	kbs    interfaces.KnowledgeBaseService
}

// NewWorkflowService creates a new workflow service. models/kbs back the
// engine adapters injected into every compiled run (LLMFunc → ModelService
// GetChatModel, RetrievalFunc → KnowledgeBaseService HybridSearch).
func NewWorkflowService(
	repo interfaces.WorkflowRepository,
	models interfaces.ModelService,
	kbs interfaces.KnowledgeBaseService,
) interfaces.WorkflowService {
	return &workflowService{repo: repo, models: models, kbs: kbs}
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
// Lifecycle: a workflow_runs row is created in "running" state before
// execution so every attempt is observable, then flipped to succeeded or
// failed after the engine returns. Compile errors and execution errors both
// leave a persisted failed row; the caller receives (run, err) and may
// present the run record itself as the outcome.
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

	var dsl wfengine.DSL
	if err := json.Unmarshal(wf.DSL, &dsl); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}
	normalized, err := wfengine.Normalize(&dsl)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowInvalidDSL, err)
	}

	inputDoc, _ := json.Marshal(req)
	run := &types.WorkflowRun{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		WorkflowID: id,
		Status:     types.WorkflowRunStatusRunning,
		Input:      types.JSON(inputDoc),
	}
	if err := s.repo.CreateWorkflowRun(ctx, run); err != nil {
		return nil, err
	}

	compiled, cerr := wfengine.Compile(normalized, wfengine.Deps{
		LLMFunc:       s.runLLM,
		RetrievalFunc: s.runRetrieval,
		OnNodeEvent: func(ev wfengine.NodeEvent) {
			logger.Infof(ctx, "[workflow:%s run:%s] node %s %s (%dms)",
				id, run.ID, ev.NodeID, ev.Phase, ev.DurationMS)
		},
	})
	if cerr != nil {
		s.failWorkflowRun(ctx, run, cerr)
		return run, cerr
	}

	runCtx, cancel := context.WithTimeout(ctx, workflowRunTimeout)
	defer cancel()
	result, rerr := compiled.Run(runCtx, req.Query, req.Files)
	if rerr != nil {
		s.failWorkflowRun(ctx, run, rerr)
		return run, rerr
	}

	outDoc := map[string]any{
		"answer":  result.Answer,
		"path":    result.Path,
		"outputs": result.Outputs,
	}
	outJSON, merr := json.Marshal(outDoc)
	if merr != nil {
		s.failWorkflowRun(ctx, run, merr)
		return run, merr
	}
	run.Status = types.WorkflowRunStatusSucceeded
	run.Output = types.JSON(outJSON)
	if uerr := s.repo.UpdateWorkflowRun(ctx, run); uerr != nil {
		logger.Errorf(ctx, "workflow run %s terminal update failed: %v", run.ID, uerr)
	}
	return run, nil
}

// failWorkflowRun persists the terminal failed state of a run.
func (s *workflowService) failWorkflowRun(ctx context.Context, run *types.WorkflowRun, cause error) {
	run.Status = types.WorkflowRunStatusFailed
	run.Error = cause.Error()
	if err := s.repo.UpdateWorkflowRun(ctx, run); err != nil {
		logger.Errorf(ctx, "workflow run %s failure update failed: %v", run.ID, err)
	}
}

// runLLM adapts the engine's LLMFunc onto the platform ModelService.
// The node's model param must name a model visible to the caller's tenant;
// there is no cross-tenant fallback.
func (s *workflowService) runLLM(ctx context.Context, req nodes.LLMRequest) (string, error) {
	if strings.TrimSpace(req.Model) == "" {
		return "", errors.New("workflow LLM node requires a model id in its params")
	}
	model, err := s.models.GetChatModel(ctx, req.Model)
	if err != nil {
		return "", fmt.Errorf("workflow LLM model %q unavailable: %w", req.Model, err)
	}
	resp, err := model.Chat(ctx, []chat.Message{{Role: "user", Content: req.Prompt}}, &chat.ChatOptions{
		Temperature: req.Temperature,
	})
	if err != nil {
		return "", fmt.Errorf("workflow LLM call failed: %w", err)
	}
	return resp.Content, nil
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
