package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Workflow schedule CRUD sentinel errors. Handlers map them onto HTTP error
// bodies (scheduleHTTPError).
var (
	// ErrWorkflowScheduleNotFound: schedule id absent in the tenant.
	ErrWorkflowScheduleNotFound = errors.New("workflow schedule not found")
	// ErrWorkflowScheduleBadCron: the expression is not a standard 5-field
	// cron spec (HTTP 400).
	ErrWorkflowScheduleBadCron = errors.New("workflow schedule cron expression is invalid")
	// ErrWorkflowScheduleNotPublished: only published workflows may be
	// scheduled (HTTP 400 — publish first).
	ErrWorkflowScheduleNotPublished = errors.New("only published workflows can be scheduled")
)

// workflowScheduleService implements interfaces.WorkflowScheduleService.
type workflowScheduleService struct {
	schedules interfaces.WorkflowScheduleRepository
	workflows interfaces.WorkflowRepository
	scheduler interfaces.WorkflowScheduler
}

// NewWorkflowScheduleService creates the schedule service. The scheduler
// keeps its cron entries in sync with every mutation (dig wires the shared
// singleton scheduler instance into both the service and the container's
// start function).
func NewWorkflowScheduleService(
	schedules interfaces.WorkflowScheduleRepository,
	workflows interfaces.WorkflowRepository,
	scheduler interfaces.WorkflowScheduler,
) interfaces.WorkflowScheduleService {
	return &workflowScheduleService{schedules: schedules, workflows: workflows, scheduler: scheduler}
}

// CreateWorkflowSchedule validates cron + published status, stores the row
// and registers the cron entry.
func (s *workflowScheduleService) CreateWorkflowSchedule(ctx context.Context, workflowID string, req *types.CreateWorkflowScheduleRequest) (*types.WorkflowSchedule, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	if _, err := cron.ParseStandard(req.Cron); err != nil {
		return nil, fmt.Errorf("%w: %q (%v)", ErrWorkflowScheduleBadCron, req.Cron, err)
	}
	wf, err := s.workflows.GetWorkflowByIDAndTenant(ctx, workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	if wf.Status != types.WorkflowStatusPublished {
		return nil, fmt.Errorf("%w (status=%s)", ErrWorkflowScheduleNotPublished, wf.Status)
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var inputs types.JSON
	if len(req.Inputs) > 0 {
		doc, merr := json.Marshal(req.Inputs)
		if merr != nil {
			return nil, merr
		}
		inputs = doc
	}
	var files types.JSON
	if len(req.Files) > 0 {
		doc, merr := json.Marshal(req.Files)
		if merr != nil {
			return nil, merr
		}
		files = doc
	}
	creatorID, _ := types.UserIDFromContext(ctx)
	schedule := &types.WorkflowSchedule{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		WorkflowID: workflowID,
		CreatorID:  creatorID,
		Cron:       req.Cron,
		Query:      req.Query,
		Inputs:     inputs,
		Files:      files,
		Enabled:    enabled,
	}
	if err := s.schedules.CreateWorkflowSchedule(ctx, schedule); err != nil {
		return nil, err
	}
	if err := s.scheduler.AddOrUpdate(schedule); err != nil {
		// Stored but unregistered: the entry rejoins on the next process
		// start (Start loads all enabled rows); log, don't fail the write.
		logger.Warnf(ctx, "[workflow-schedule] stored %s but cron registration failed: %v", schedule.ID, err)
	}
	return schedule, nil
}

// ListWorkflowSchedules returns the workflow's schedules, newest first.
func (s *workflowScheduleService) ListWorkflowSchedules(ctx context.Context, workflowID string) ([]*types.WorkflowSchedule, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	if _, err := s.workflows.GetWorkflowByIDAndTenant(ctx, workflowID, tenantID); err != nil {
		return nil, err
	}
	return s.schedules.ListWorkflowSchedulesByTenantAndWorkflow(ctx, tenantID, workflowID)
}

// DeleteWorkflowSchedule removes the row and unregisters the entry.
func (s *workflowScheduleService) DeleteWorkflowSchedule(ctx context.Context, workflowID, scheduleID string) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return ErrWorkflowTenantRequired
	}
	schedule, err := s.schedules.GetWorkflowScheduleByIDAndTenant(ctx, scheduleID, tenantID)
	if err != nil {
		return err
	}
	if schedule.WorkflowID != workflowID {
		return ErrWorkflowScheduleNotFound
	}
	if err := s.schedules.DeleteWorkflowSchedule(ctx, scheduleID, tenantID); err != nil {
		return err
	}
	s.scheduler.Remove(scheduleID)
	return nil
}

// SetWorkflowScheduleEnabled flips enabled and syncs the cron entry.
func (s *workflowScheduleService) SetWorkflowScheduleEnabled(ctx context.Context, workflowID, scheduleID string, enabled bool) (*types.WorkflowSchedule, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrWorkflowTenantRequired
	}
	schedule, err := s.schedules.GetWorkflowScheduleByIDAndTenant(ctx, scheduleID, tenantID)
	if err != nil {
		return nil, err
	}
	if schedule.WorkflowID != workflowID {
		return nil, ErrWorkflowScheduleNotFound
	}
	schedule.Enabled = enabled
	if err := s.schedules.UpdateWorkflowSchedule(ctx, schedule); err != nil {
		return nil, err
	}
	if err := s.scheduler.AddOrUpdate(schedule); err != nil {
		logger.Warnf(ctx, "[workflow-schedule] %s cron re-registration failed: %v", scheduleID, err)
	}
	return schedule, nil
}
