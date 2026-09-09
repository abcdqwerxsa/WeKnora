package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
)

// workflowScheduler fires cron-scheduled runs of published workflows.
//
// robfig/cron fires at absolute wall-clock times, so multiple instances
// trigger the same minute. Dedup mirrors the datasource scheduler:
// a deterministic asynq.TaskID "wfsched:<scheduleID>:<minute>" — the first
// Enqueue wins, losers get ErrTaskIDConflict and cancel their run row.
//
// The tick re-checks enabled + published with fresh reads, so unpublishing,
// archiving or deleting a workflow stops firing without touching schedules.
type workflowScheduler struct {
	cron      *cron.Cron
	schedules interfaces.WorkflowScheduleRepository
	workflows interfaces.WorkflowRepository
	runs      interfaces.WorkflowRepository
	enqueuer  interfaces.TaskEnqueuer

	mu      sync.Mutex
	entries map[string]cron.EntryID // scheduleID → cron entry ID
}

// NewWorkflowScheduler creates the scheduler (dig-provided; started via
// startWorkflowScheduler).
func NewWorkflowScheduler(
	schedules interfaces.WorkflowScheduleRepository,
	workflows interfaces.WorkflowRepository,
	enqueuer interfaces.TaskEnqueuer,
) interfaces.WorkflowScheduler {
	return &workflowScheduler{
		// Standard 5-field parser (no seconds), panic recovery per job.
		cron:      cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		schedules: schedules,
		workflows: workflows,
		runs:      workflows, // run rows live on the same repository
		enqueuer:  enqueuer,
		entries:   make(map[string]cron.EntryID),
	}
}

// Start loads all enabled schedules and starts the cron runner.
func (s *workflowScheduler) Start(ctx context.Context) error {
	enabled, err := s.schedules.ListEnabledWorkflowSchedules(ctx)
	if err != nil {
		return fmt.Errorf("load enabled workflow schedules: %w", err)
	}
	for _, schedule := range enabled {
		if err := s.AddOrUpdate(schedule); err != nil {
			logger.Warnf(ctx, "[WorkflowScheduler] register schedule=%s cron=%q: %v", schedule.ID, schedule.Cron, err)
		}
	}
	s.cron.Start()
	logger.Infof(ctx, "[WorkflowScheduler] started with %d cron entries", s.EntryCount())
	return nil
}

// Stop gracefully stops the runner.
func (s *workflowScheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// AddOrUpdate registers (or re-registers) the cron entry for a schedule.
func (s *workflowScheduler) AddOrUpdate(schedule *types.WorkflowSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID, ok := s.entries[schedule.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, schedule.ID)
	}
	if !schedule.Enabled {
		return nil
	}
	scheduleID, tenantID := schedule.ID, schedule.TenantID
	entryID, err := s.cron.AddFunc(schedule.Cron, func() {
		s.tick(scheduleID, tenantID)
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", schedule.Cron, err)
	}
	s.entries[scheduleID] = entryID
	return nil
}

// Remove drops the cron entry for a schedule id.
func (s *workflowScheduler) Remove(scheduleID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID, ok := s.entries[scheduleID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, scheduleID)
	}
}

// EntryCount returns the number of registered entries (monitoring/tests).
func (s *workflowScheduler) EntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// tick fires one scheduled run. Fresh reads gate on enabled + published;
// then the run row is created and the async task enqueued exactly like the
// RunWorkflow async path (row-then-enqueue, ProcessWorkflowRun drives it).
func (s *workflowScheduler) tick(scheduleID string, tenantID uint64) {
	ctx := context.Background()

	schedule, err := s.schedules.GetWorkflowScheduleByIDAndTenant(ctx, scheduleID, tenantID)
	if err != nil || schedule == nil || !schedule.Enabled {
		logger.Infof(ctx, "[WorkflowScheduler] skip schedule=%s (missing or disabled)", scheduleID)
		return
	}
	wf, err := s.workflows.GetWorkflowByIDAndTenant(ctx, schedule.WorkflowID, tenantID)
	if err != nil || wf == nil || wf.Status != types.WorkflowStatusPublished {
		logger.Infof(ctx, "[WorkflowScheduler] skip schedule=%s (workflow %s not published)", scheduleID, schedule.WorkflowID)
		return
	}

	// Input document mirrors RunWorkflow's async request marshalling so the
	// run row and the ProcessWorkflowRun payload agree on query/inputs.
	req := &types.RunWorkflowRequest{Query: schedule.Query}
	if len(schedule.Inputs) > 0 {
		var inputs map[string]any
		if json.Unmarshal(schedule.Inputs, &inputs) == nil && len(inputs) > 0 {
			req.Inputs = inputs
		}
	}
	if len(schedule.Files) > 0 {
		var files []string
		if json.Unmarshal(schedule.Files, &files) == nil && len(files) > 0 {
			req.Files = files
		}
	}
	inputDoc, _ := json.Marshal(req)
	run := &types.WorkflowRun{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		WorkflowID: schedule.WorkflowID,
		Status:     types.WorkflowRunStatusPending,
		Input:      types.JSON(inputDoc),
	}
	if err := s.runs.CreateWorkflowRun(ctx, run); err != nil {
		logger.Errorf(ctx, "[WorkflowScheduler] create run row for schedule=%s: %v", scheduleID, err)
		return
	}

	payload, merr := json.Marshal(types.WorkflowRunPayload{
		RunID:      run.ID,
		WorkflowID: schedule.WorkflowID,
		TenantID:   tenantID,
		Query:      req.Query,
		Inputs:     req.Inputs,
	})
	if merr != nil {
		logger.Errorf(ctx, "[WorkflowScheduler] payload marshal schedule=%s: %v", scheduleID, merr)
		return
	}
	task := asynq.NewTask(types.TypeWorkflowRun, payload)
	// Deterministic TaskID: all instances in the same minute collide on
	// purpose — only one enqueue wins.
	taskID := fmt.Sprintf("wfsched:%s:%s", scheduleID, time.Now().UTC().Truncate(time.Minute).Format("200601021504"))
	if _, err := s.enqueuer.Enqueue(task,
		asynq.Queue(types.QueueDefault),
		asynq.Timeout(workflowRunTimeout+30*time.Second),
		asynq.MaxRetry(2),
		asynq.TaskID(taskID),
	); err != nil {
		if err == asynq.ErrTaskIDConflict {
			// Another instance won this minute: cancel our redundant row so
			// it does not sit pending forever (its task was never enqueued).
			_ = s.runs.MarkWorkflowRunCancelled(ctx, run.ID, tenantID)
			logger.Infof(ctx, "[WorkflowScheduler] deduplicated schedule=%s (another instance fired first)", scheduleID)
			return
		}
		logger.Errorf(ctx, "[WorkflowScheduler] enqueue failed schedule=%s: %v", scheduleID, err)
		return
	}
	logger.Infof(ctx, "[WorkflowScheduler] scheduled run enqueued: schedule=%s workflow=%s run=%s", scheduleID, schedule.WorkflowID, run.ID)
}
