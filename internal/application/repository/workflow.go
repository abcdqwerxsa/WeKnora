package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrWorkflowNotFound is the sentinel returned when a workflow id does not
// exist inside the requested tenant (or has been soft-deleted). Handlers map
// it to 404; the RBAC creator-lookup maps it to ErrResourceNotFound so the
// middleware lets the handler answer with a proper 404 instead of a fake 403.
var ErrWorkflowNotFound = errors.New("workflow not found")

// workflowRepository implements interfaces.WorkflowRepository. Every query
// filters on tenant_id — there is deliberately no tenant-less read path.
type workflowRepository struct {
	db *gorm.DB
}

// NewWorkflowRepository creates a new workflow repository.
func NewWorkflowRepository(db *gorm.DB) interfaces.WorkflowRepository {
	return &workflowRepository{db: db}
}

// CreateWorkflow inserts a new workflow row.
func (r *workflowRepository) CreateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	return r.db.WithContext(ctx).Create(workflow).Error
}

// GetWorkflowByIDAndTenant returns the workflow with the given id only when
// it belongs to tenantID (enforces tenant isolation).
func (r *workflowRepository) GetWorkflowByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.Workflow, error) {
	var wf types.Workflow
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&wf).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowNotFound
		}
		return nil, err
	}
	return &wf, nil
}

// ListWorkflowsByTenantID returns one page of the tenant's workflows, newest
// first, plus the total count for pagination.
func (r *workflowRepository) ListWorkflowsByTenantID(ctx context.Context, tenantID uint64, offset, limit int) ([]*types.Workflow, int64, error) {
	var (
		workflows []*types.Workflow
		total     int64
	)
	if err := r.db.WithContext(ctx).Model(&types.Workflow{}).
		Where("tenant_id = ?", tenantID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Offset(offset).Limit(limit).
		Find(&workflows).Error; err != nil {
		return nil, 0, err
	}
	return workflows, total, nil
}

// UpdateWorkflow saves mutated fields of an existing workflow row.
func (r *workflowRepository) UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	return r.db.WithContext(ctx).Model(workflow).
		Where("id = ? AND tenant_id = ?", workflow.ID, workflow.TenantID).
		Select("name", "description", "dsl", "status", "version", "updated_at").
		Updates(workflow).Error
}

// DeleteWorkflow soft-deletes the workflow (gorm.DeletedAt) inside tenantID.
func (r *workflowRepository) DeleteWorkflow(ctx context.Context, id string, tenantID uint64) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.Workflow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

// CreateWorkflowRun inserts a new run row (used by the execution wiring).
func (r *workflowRepository) CreateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

// UpdateWorkflowRun saves the terminal (or intermediate) state of a run row.
// The run row is always created inside the caller's tenant by the service
// layer, so the primary-key save cannot escape tenant scope.
func (r *workflowRepository) UpdateWorkflowRun(ctx context.Context, run *types.WorkflowRun) error {
	return r.db.WithContext(ctx).Model(&types.WorkflowRun{}).
		Where("id = ? AND tenant_id = ?", run.ID, run.TenantID).
		Updates(map[string]any{
			"status": run.Status,
			"output": run.Output,
			"error":  run.Error,
		}).Error
}

// GetWorkflowRunByIDAndTenant returns the run row only when it belongs to
// tenantID — cross-tenant run ids fail closed as not-found.
func (r *workflowRepository) GetWorkflowRunByIDAndTenant(ctx context.Context, runID string, tenantID uint64) (*types.WorkflowRun, error) {
	var run types.WorkflowRun
	if err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", runID, tenantID).
		First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowNotFound
		}
		return nil, err
	}
	return &run, nil
}

// ListWorkflowRunsByTenantAndWorkflow returns the run history of one
// workflow, newest first.
func (r *workflowRepository) ListWorkflowRunsByTenantAndWorkflow(ctx context.Context, tenantID uint64, workflowID string) ([]*types.WorkflowRun, error) {
	var runs []*types.WorkflowRun
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).
		Order("created_at DESC").
		Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// ErrWorkflowRunNotCancellable is returned by MarkWorkflowRunCancelled when
// the row already reached a terminal state; callers re-read and surface the
// current row instead of failing the API call.
var ErrWorkflowRunNotCancellable = errors.New("workflow run already terminal")

// MarkWorkflowRunCancelled flips a pending/running run to cancelled using a
// state-guarded UPDATE, so a concurrent terminal write cannot be overwritten
// by a late cancel (and vice versa).
func (r *workflowRepository) MarkWorkflowRunCancelled(ctx context.Context, runID string, tenantID uint64) error {
	res := r.db.WithContext(ctx).Model(&types.WorkflowRun{}).
		Where("id = ? AND tenant_id = ? AND status IN (?, ?)",
			runID, tenantID, types.WorkflowRunStatusPending, types.WorkflowRunStatusRunning).
		Update("status", types.WorkflowRunStatusCancelled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrWorkflowRunNotCancellable
	}
	return nil
}
