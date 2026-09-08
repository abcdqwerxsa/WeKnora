package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrWorkflowScheduleNotFound is the sentinel for a schedule id that does not
// exist inside the requested tenant (or was soft-deleted). Handlers map it to
// 404.
var ErrWorkflowScheduleNotFound = errors.New("workflow schedule not found")

// workflowScheduleRepository implements interfaces.WorkflowScheduleRepository.
// Every query filters on tenant_id — there is deliberately no tenant-less
// read path.
type workflowScheduleRepository struct {
	db *gorm.DB
}

// NewWorkflowScheduleRepository creates a new workflow schedule repository.
func NewWorkflowScheduleRepository(db *gorm.DB) interfaces.WorkflowScheduleRepository {
	return &workflowScheduleRepository{db: db}
}

// CreateWorkflowSchedule inserts a new schedule row.
func (r *workflowScheduleRepository) CreateWorkflowSchedule(ctx context.Context, schedule *types.WorkflowSchedule) error {
	return r.db.WithContext(ctx).Create(schedule).Error
}

// GetWorkflowScheduleByIDAndTenant returns the schedule only when it belongs
// to tenantID; ErrWorkflowScheduleNotFound otherwise.
func (r *workflowScheduleRepository) GetWorkflowScheduleByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.WorkflowSchedule, error) {
	var schedule types.WorkflowSchedule
	if err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&schedule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkflowScheduleNotFound
		}
		return nil, err
	}
	return &schedule, nil
}

// ListWorkflowSchedulesByTenantAndWorkflow returns the workflow's schedules,
// newest first.
func (r *workflowScheduleRepository) ListWorkflowSchedulesByTenantAndWorkflow(ctx context.Context, tenantID uint64, workflowID string) ([]*types.WorkflowSchedule, error) {
	var schedules []*types.WorkflowSchedule
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).
		Order("created_at DESC").
		Find(&schedules).Error; err != nil {
		return nil, err
	}
	return schedules, nil
}

// ListEnabledWorkflowSchedules returns every enabled schedule across all
// tenants (scheduler startup path; not exposed over REST).
func (r *workflowScheduleRepository) ListEnabledWorkflowSchedules(ctx context.Context) ([]*types.WorkflowSchedule, error) {
	var schedules []*types.WorkflowSchedule
	if err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Find(&schedules).Error; err != nil {
		return nil, err
	}
	return schedules, nil
}

// UpdateWorkflowSchedule saves mutated fields of an existing schedule row.
func (r *workflowScheduleRepository) UpdateWorkflowSchedule(ctx context.Context, schedule *types.WorkflowSchedule) error {
	return r.db.WithContext(ctx).Model(schedule).
		Where("id = ? AND tenant_id = ?", schedule.ID, schedule.TenantID).
		Select("cron", "query", "inputs", "enabled", "updated_at").
		Updates(schedule).Error
}

// DeleteWorkflowSchedule soft-deletes the schedule inside tenantID.
func (r *workflowScheduleRepository) DeleteWorkflowSchedule(ctx context.Context, id string, tenantID uint64) error {
	res := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.WorkflowSchedule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrWorkflowScheduleNotFound
	}
	return nil
}
