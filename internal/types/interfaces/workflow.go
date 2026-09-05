// Package interfaces — workflow storage and CRUD contracts.
//
// The workflow-orchestration feature is split into parallel slices; this
// slice owns persistence and REST CRUD only. The engine package (a sibling
// slice) is intentionally NOT imported here: the DSL is validated with a
// minimal local structure and stored verbatim, so the two slices have no
// compile-time dependency on each other.
package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// WorkflowRepository defines the tenant-scoped persistence contract for
// workflows and their run history. Every read and write filters on
// tenant_id — callers pass it explicitly, there is no cross-tenant path.
type WorkflowRepository interface {
	// CreateWorkflow inserts a new workflow row (ID/TenantID must be set).
	CreateWorkflow(ctx context.Context, workflow *types.Workflow) error

	// GetWorkflowByIDAndTenant returns the workflow with the given id only
	// when it belongs to tenantID; ErrWorkflowNotFound otherwise.
	GetWorkflowByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.Workflow, error)

	// ListWorkflowsByTenantID returns one page of the tenant's workflows,
	// newest first, plus the total row count for pagination.
	ListWorkflowsByTenantID(ctx context.Context, tenantID uint64, offset, limit int) ([]*types.Workflow, int64, error)

	// UpdateWorkflow saves mutated fields of an existing workflow row.
	UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error

	// DeleteWorkflow soft-deletes the workflow with the given id inside tenantID.
	DeleteWorkflow(ctx context.Context, id string, tenantID uint64) error

	// CreateWorkflowRun inserts a new run row (used by the execution wiring).
	CreateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error

	// UpdateWorkflowRun saves the terminal (or intermediate) state of a run
	// row (status/output/error) — used by the execution wiring.
	UpdateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error

	// ListWorkflowRunsByTenantAndWorkflow returns the run history of one
	// workflow, newest first.
	ListWorkflowRunsByTenantAndWorkflow(ctx context.Context, tenantID uint64, workflowID string) ([]*types.WorkflowRun, error)

	// GetWorkflowRunByIDAndTenant returns the run row only when it belongs to
	// tenantID; ErrWorkflowNotFound otherwise. Used by the SSE endpoint to
	// validate the subscription target before streaming.
	GetWorkflowRunByIDAndTenant(ctx context.Context, runID string, tenantID uint64) (*types.WorkflowRun, error)

	// MarkWorkflowRunCancelled flips a pending/running run to cancelled with
	// a state-guarded UPDATE. Returns ErrWorkflowRunNotCancellable when the
	// row already reached a terminal state (callers re-read and surface the
	// current row — cancellation is idempotent at the API level).
	MarkWorkflowRunCancelled(ctx context.Context, runID string, tenantID uint64) error
}

// WorkflowService defines the CRUD surface exposed over REST. Tenant and
// caller identity always come from the request context, never from payloads.
type WorkflowService interface {
	// CreateWorkflow validates and creates a workflow; CreatorID/TenantID are
	// taken from ctx (a request-supplied creator_id is ignored).
	CreateWorkflow(ctx context.Context, workflow *types.Workflow) (*types.Workflow, error)

	// GetWorkflowByID returns the workflow in the caller's tenant.
	GetWorkflowByID(ctx context.Context, id string) (*types.Workflow, error)

	// ListWorkflows returns one page of the caller's tenant workflows.
	ListWorkflows(ctx context.Context, page, pageSize int) ([]*types.Workflow, int64, error)

	// UpdateWorkflow replaces the mutable fields (name/description/dsl/status)
	// of the workflow in the caller's tenant and bumps its version.
	UpdateWorkflow(ctx context.Context, id string, req *types.UpdateWorkflowRequest) (*types.Workflow, error)

	// DeleteWorkflow soft-deletes the workflow in the caller's tenant.
	DeleteWorkflow(ctx context.Context, id string) error

	// ListWorkflowRuns returns the run history of a workflow in the caller's
	// tenant, newest first (populated by RunWorkflow).
	ListWorkflowRuns(ctx context.Context, workflowID string) ([]*types.WorkflowRun, error)

	// RunWorkflow executes one run of the workflow in the caller's tenant:
	// compiles the stored DSL through the engine package, runs it with the
	// platform LLM / knowledge-search adapters injected, and persists a
	// workflow_runs row. req.Async=true enqueues a workflow:run task and
	// returns the pending run immediately; otherwise execution is
	// synchronous (120s cap).
	RunWorkflow(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error)

	// CancelWorkflowRun best-effort cancels a pending/running run: flips the
	// row to cancelled (state-guarded), aborts an in-process execution via
	// the run-scoped context, and closes SSE subscribers. Terminal runs are
	// returned as-is (idempotent); the run row is the source of truth.
	CancelWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error)

	// ResumeWorkflowRun re-drives a FAILED run from its checkpoint side-car
	// (completed nodes replay, the failed attempt re-runs) by enqueueing a
	// Resume-marked workflow:run task; the row stays failed until a worker
	// picks it up. Returns ErrWorkflowRunNotResumable (HTTP 409) for
	// non-failed rows.
	ResumeWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error)

	// ProcessWorkflowRun is the asynq handler for types.TypeWorkflowRun. It
	// restores the tenant context from the payload and drives the pending
	// run to a terminal state. Returns nil for execution failures (the run
	// row is the outcome); errors only on infrastructure faults.
	ProcessWorkflowRun(ctx context.Context, t *asynq.Task) error

	// GetWorkflowRun returns one run of a workflow in the caller's tenant.
	GetWorkflowRun(ctx context.Context, workflowID, runID string) (*types.WorkflowRun, error)

	// SubscribeWorkflowRunEvents attaches a live feed to one run's node
	// events: the process-local broker merged with the run's redis pubsub
	// channel (workflow:run:{run_id}) when redis is configured, deduplicated
	// by kind|node|phase|duration. Lite mode (no redis) stays process-local.
	// The returned cancel detaches the subscriber; the channel is closed
	// after the terminal frame is delivered (or on cancel).
	SubscribeWorkflowRunEvents(runID string) (<-chan types.WorkflowRunEvent, func())
}
