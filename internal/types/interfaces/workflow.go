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

	// ListWorkflowRunsByTenantAndWorkflow returns the run history of one
	// workflow, newest first.
	ListWorkflowRunsByTenantAndWorkflow(ctx context.Context, tenantID uint64, workflowID string) ([]*types.WorkflowRun, error)
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
	// tenant (currently always empty until execution wiring lands).
	ListWorkflowRuns(ctx context.Context, workflowID string) ([]*types.WorkflowRun, error)
}
