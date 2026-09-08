package service

import (
	"context"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cronEntryID aliases cron.EntryID (stub field typing).
type cronEntryID = cron.EntryID

// ---- stubs -----------------------------------------------------------------

type scheduleRepoStub struct {
	schedules map[string]*types.WorkflowSchedule
	created   []*types.WorkflowSchedule
	updated   []*types.WorkflowSchedule
	deleted   []string
}

func newScheduleRepoStub() *scheduleRepoStub {
	return &scheduleRepoStub{schedules: map[string]*types.WorkflowSchedule{}}
}

func (r *scheduleRepoStub) CreateWorkflowSchedule(_ context.Context, s *types.WorkflowSchedule) error {
	cp := *s
	r.created = append(r.created, &cp)
	r.schedules[s.ID] = &cp
	return nil
}
func (r *scheduleRepoStub) GetWorkflowScheduleByIDAndTenant(_ context.Context, id string, tenantID uint64) (*types.WorkflowSchedule, error) {
	if s, ok := r.schedules[id]; ok && s.TenantID == tenantID {
		cp := *s
		return &cp, nil
	}
	return nil, apprepo.ErrWorkflowScheduleNotFound
}
func (r *scheduleRepoStub) ListWorkflowSchedulesByTenantAndWorkflow(_ context.Context, tenantID uint64, workflowID string) ([]*types.WorkflowSchedule, error) {
	var out []*types.WorkflowSchedule
	for _, s := range r.schedules {
		if s.TenantID == tenantID && s.WorkflowID == workflowID {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *scheduleRepoStub) ListEnabledWorkflowSchedules(context.Context) ([]*types.WorkflowSchedule, error) {
	var out []*types.WorkflowSchedule
	for _, s := range r.schedules {
		if s.Enabled {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *scheduleRepoStub) UpdateWorkflowSchedule(_ context.Context, s *types.WorkflowSchedule) error {
	cp := *s
	r.updated = append(r.updated, &cp)
	r.schedules[s.ID] = &cp
	return nil
}
func (r *scheduleRepoStub) DeleteWorkflowSchedule(_ context.Context, id string, _ uint64) error {
	if _, ok := r.schedules[id]; !ok {
		return apprepo.ErrWorkflowScheduleNotFound
	}
	delete(r.schedules, id)
	r.deleted = append(r.deleted, id)
	return nil
}

// fakeScheduler captures AddOrUpdate/Remove calls without running cron.
type fakeScheduler struct {
	added   map[string]*types.WorkflowSchedule
	removed []string
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{added: map[string]*types.WorkflowSchedule{}}
}
func (f *fakeScheduler) Start(context.Context) error { return nil }
func (f *fakeScheduler) Stop()                       {}
func (f *fakeScheduler) AddOrUpdate(s *types.WorkflowSchedule) error {
	f.added[s.ID] = s
	return nil
}
func (f *fakeScheduler) Remove(id string) { f.removed = append(f.removed, id) }

// scheduleWorkflowRepoStub reuses the workflow stub with a published row.
type scheduleWorkflowRepoStub struct {
	*stubWorkflowRepo
	runsCreated  []*types.WorkflowRun
	cancelled    []string
	lastWorkflow *types.Workflow
}

func (r *scheduleWorkflowRepoStub) CreateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	cp := *run
	r.runsCreated = append(r.runsCreated, &cp)
	return nil
}
func (r *scheduleWorkflowRepoStub) MarkWorkflowRunCancelled(_ context.Context, runID string, _ uint64) error {
	r.cancelled = append(r.cancelled, runID)
	return nil
}
func (r *scheduleWorkflowRepoStub) GetWorkflowByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.Workflow, error) {
	if r.lastWorkflow != nil && r.lastWorkflow.ID == id && r.lastWorkflow.TenantID == tenantID {
		cp := *r.lastWorkflow
		return &cp, nil
	}
	return nil, errWorkflowNotFoundStub
}

// ---- CRUD service ----------------------------------------------------------

func newScheduleSvc(wf *types.Workflow) (interfaces.WorkflowScheduleService, *scheduleRepoStub, *fakeScheduler) {
	schedules := newScheduleRepoStub()
	workflows := &scheduleWorkflowRepoStub{stubWorkflowRepo: &stubWorkflowRepo{}, lastWorkflow: wf}
	sched := newFakeScheduler()
	return NewWorkflowScheduleService(schedules, workflows, sched), schedules, sched
}

func scheduleCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
}

func TestScheduleCreateValidatesCron(t *testing.T) {
	svc, _, _ := newScheduleSvc(publishedWorkflow())
	_, err := svc.CreateWorkflowSchedule(scheduleCtx(), "wf-1", &types.CreateWorkflowScheduleRequest{Cron: "not a cron"})
	assert.ErrorIs(t, err, ErrWorkflowScheduleBadCron)
	_, err = svc.CreateWorkflowSchedule(scheduleCtx(), "wf-1", &types.CreateWorkflowScheduleRequest{Cron: "*/5 * * *"})
	assert.ErrorIs(t, err, ErrWorkflowScheduleBadCron)
}

func TestScheduleCreateRejectsUnpublished(t *testing.T) {
	wf := publishedWorkflow()
	wf.Status = types.WorkflowStatusDraft
	svc, _, _ := newScheduleSvc(wf)
	_, err := svc.CreateWorkflowSchedule(scheduleCtx(), "wf-1", &types.CreateWorkflowScheduleRequest{Cron: "*/5 * * * *"})
	assert.ErrorIs(t, err, ErrWorkflowScheduleNotPublished)
}

func TestScheduleCreateListRoundTrip(t *testing.T) {
	svc, repo, sched := newScheduleSvc(publishedWorkflow())
	created, err := svc.CreateWorkflowSchedule(scheduleCtx(), "wf-1", &types.CreateWorkflowScheduleRequest{
		Cron:   "*/5 * * * *",
		Query:  "nightly digest",
		Inputs: map[string]any{"city": "SZ"},
	})
	require.NoError(t, err)
	assert.True(t, created.Enabled)
	assert.Equal(t, "*/5 * * * *", created.Cron)
	assert.Contains(t, string(created.Inputs), "SZ")
	assert.NotNil(t, sched.added[created.ID], "cron entry registered on create")

	list, err := svc.ListWorkflowSchedules(scheduleCtx(), "wf-1")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Disabled schedules are stored but not registered.
	disabled, err := svc.SetWorkflowScheduleEnabled(scheduleCtx(), "wf-1", created.ID, false)
	require.NoError(t, err)
	assert.False(t, disabled.Enabled)
	assert.False(t, sched.added[created.ID].Enabled, "entry re-registered with enabled=false (AddOrUpdate no-ops the cron)")

	reEnabled, err := svc.SetWorkflowScheduleEnabled(scheduleCtx(), "wf-1", created.ID, true)
	require.NoError(t, err)
	assert.True(t, reEnabled.Enabled)

	require.NoError(t, svc.DeleteWorkflowSchedule(scheduleCtx(), "wf-1", created.ID))
	assert.Equal(t, []string{created.ID}, sched.removed)
	assert.Empty(t, repo.schedules)
}

func TestScheduleCreateDisabledByRequest(t *testing.T) {
	svc, _, sched := newScheduleSvc(publishedWorkflow())
	no := false
	created, err := svc.CreateWorkflowSchedule(scheduleCtx(), "wf-1", &types.CreateWorkflowScheduleRequest{Cron: "0 3 * * *", Enabled: &no})
	require.NoError(t, err)
	assert.False(t, created.Enabled)
	assert.NotNil(t, sched.added[created.ID])
	assert.False(t, sched.added[created.ID].Enabled)
}

// ---- scheduler tick --------------------------------------------------------

func publishedWorkflow() *types.Workflow {
	return &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", Status: types.WorkflowStatusPublished, DSL: types.JSON(`{"version":1}`)}
}

// fakeEnqueuer captures tasks; conflictEveryN makes every Nth enqueue fail
// with ErrTaskIDConflict (dedup path).
type fakeScheduleEnqueuer struct {
	tasks     []*asynq.Task
	opts      [][]asynq.Option
	conflictN int
	calls     int
}

func (e *fakeScheduleEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	if e.conflictN > 0 && e.calls%e.conflictN == 0 {
		return nil, asynq.ErrTaskIDConflict
	}
	cp := task
	e.tasks = append(e.tasks, cp)
	e.opts = append(e.opts, opts)
	return &asynq.TaskInfo{}, nil
}

func newTestableScheduler(schedules interfaces.WorkflowScheduleRepository, workflows *scheduleWorkflowRepoStub, enqueuer interfaces.TaskEnqueuer) *workflowScheduler {
	return &workflowScheduler{
		cron:      nil, // tick() invoked directly; cron runner unused in tests
		schedules: schedules,
		workflows: workflows,
		runs:      workflows,
		enqueuer:  enqueuer,
		entries:   map[string]cronEntryID{},
	}
}

func TestSchedulerTickFiresOnlyWhenEnabledAndPublished(t *testing.T) {
	schedules := newScheduleRepoStub()
	workflows := &scheduleWorkflowRepoStub{stubWorkflowRepo: &stubWorkflowRepo{}, lastWorkflow: publishedWorkflow()}
	enqueuer := &fakeScheduleEnqueuer{}
	sched := newTestableScheduler(schedules, workflows, enqueuer)

	row := &types.WorkflowSchedule{ID: "sch-1", TenantID: 10001, WorkflowID: "wf-1", Cron: "*/5 * * * *", Query: "q", Enabled: true}
	schedules.schedules["sch-1"] = row

	// Enabled + published → run row + enqueue.
	sched.tick("sch-1", 10001)
	require.Len(t, workflows.runsCreated, 1)
	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, "wf-1", workflows.runsCreated[0].WorkflowID)
	assert.Contains(t, string(workflows.runsCreated[0].Input), `"query":"q"`)

	// Disabled → no fire.
	row.Enabled = false
	sched.tick("sch-1", 10001)
	assert.Len(t, enqueuer.tasks, 1)

	// Enabled but unpublished → no fire.
	row.Enabled = true
	unpub := publishedWorkflow()
	unpub.Status = types.WorkflowStatusDraft
	workflows.lastWorkflow = unpub
	sched.tick("sch-1", 10001)
	assert.Len(t, enqueuer.tasks, 1)
}

func TestSchedulerTickDedupCancelsRow(t *testing.T) {
	schedules := newScheduleRepoStub()
	workflows := &scheduleWorkflowRepoStub{stubWorkflowRepo: &stubWorkflowRepo{}, lastWorkflow: publishedWorkflow()}
	enqueuer := &fakeScheduleEnqueuer{conflictN: 1} // every enqueue conflicts
	sched := newTestableScheduler(schedules, workflows, enqueuer)

	schedules.schedules["sch-1"] = &types.WorkflowSchedule{ID: "sch-1", TenantID: 10001, WorkflowID: "wf-1", Cron: "*/5 * * * *", Query: "q", Enabled: true}
	sched.tick("sch-1", 10001)

	require.Len(t, workflows.runsCreated, 1, "row created before enqueue attempt")
	require.Len(t, workflows.cancelled, 1, "redundant row cancelled on TaskID conflict")
	assert.Equal(t, workflows.runsCreated[0].ID, workflows.cancelled[0])
	assert.Empty(t, enqueuer.tasks)
}
