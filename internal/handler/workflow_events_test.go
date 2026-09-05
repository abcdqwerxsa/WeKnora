package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wfEventsTestEnv wires a REAL workflow service (stub repo + stub model/kb
// adapters + capture enqueuer) behind the handler, so the SSE endpoint is
// exercised against the actual broker lifecycle.
type wfEventsTestEnv struct {
	handler *WorkflowHandler
}

func newWorkflowEventsTestEnv(t *testing.T, dsl string) *wfEventsTestEnv {
	t.Helper()
	wf := &types.Workflow{ID: "wf-sse", TenantID: 7, Name: "wf", DSL: types.JSON(dsl)}
	repo := &wfEventsRepoStub{base: &wfEventsBaseRepo{saved: wf}}
	svc := service.NewWorkflowService(repo, nil, nil, &wfEventsEnqueuer{})
	return &wfEventsTestEnv{handler: NewWorkflowHandler(svc)}
}

// The real runRepoStub lives in the service package's test files; a minimal
// standalone variant keeps this file self-contained.
type wfEventsBaseRepo struct {
	saved *types.Workflow
}

func (r *wfEventsBaseRepo) CreateWorkflow(_ context.Context, wf *types.Workflow) error {
	r.saved = wf
	return nil
}
func (r *wfEventsBaseRepo) GetWorkflowByIDAndTenant(_ context.Context, id string, tenantID uint64) (*types.Workflow, error) {
	if r.saved != nil && r.saved.ID == id && r.saved.TenantID == tenantID {
		return r.saved, nil
	}
	return nil, wfEventsNotFound
}
func (r *wfEventsBaseRepo) ListWorkflowsByTenantID(_ context.Context, _ uint64, _, _ int) ([]*types.Workflow, int64, error) {
	return nil, 0, nil
}
func (r *wfEventsBaseRepo) UpdateWorkflow(_ context.Context, wf *types.Workflow) error { r.saved = wf; return nil }
func (r *wfEventsBaseRepo) DeleteWorkflow(_ context.Context, _ string, _ uint64) error { return nil }
func (r *wfEventsBaseRepo) CreateWorkflowRun(_ context.Context, _ *types.WorkflowRun) error {
	return nil
}
func (r *wfEventsBaseRepo) UpdateWorkflowRun(_ context.Context, _ *types.WorkflowRun) error { return nil }
func (r *wfEventsBaseRepo) ListWorkflowRunsByTenantAndWorkflow(context.Context, uint64, string) ([]*types.WorkflowRun, error) {
	return nil, nil
}

type wfEventsRepoStub struct {
	base *wfEventsBaseRepo
	runs map[string]*types.WorkflowRun
}

var wfEventsNotFound = apprepo.ErrWorkflowNotFound // real sentinel: handler maps it to 404

func (r *wfEventsRepoStub) CreateWorkflow(_ context.Context, wf *types.Workflow) error {
	return r.base.CreateWorkflow(nil, wf)
}
func (r *wfEventsRepoStub) GetWorkflowByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.Workflow, error) {
	return r.base.GetWorkflowByIDAndTenant(ctx, id, tenantID)
}
func (r *wfEventsRepoStub) ListWorkflowsByTenantID(ctx context.Context, t uint64, o, l int) ([]*types.Workflow, int64, error) {
	return r.base.ListWorkflowsByTenantID(ctx, t, o, l)
}
func (r *wfEventsRepoStub) UpdateWorkflow(ctx context.Context, wf *types.Workflow) error {
	return r.base.UpdateWorkflow(ctx, wf)
}
func (r *wfEventsRepoStub) DeleteWorkflow(ctx context.Context, id string, t uint64) error {
	return r.base.DeleteWorkflow(ctx, id, t)
}
func (r *wfEventsRepoStub) CreateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	if r.runs == nil {
		r.runs = map[string]*types.WorkflowRun{}
	}
	cp := *run
	r.runs[run.ID] = &cp
	return nil
}
func (r *wfEventsRepoStub) UpdateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	cp := *run
	r.runs[run.ID] = &cp
	return nil
}
func (r *wfEventsRepoStub) ListWorkflowRunsByTenantAndWorkflow(context.Context, uint64, string) ([]*types.WorkflowRun, error) {
	return nil, nil
}
func (r *wfEventsRepoStub) GetWorkflowRunByIDAndTenant(_ context.Context, runID string, tenantID uint64) (*types.WorkflowRun, error) {
	run, ok := r.runs[runID]
	if !ok || run.TenantID != tenantID {
		return nil, wfEventsNotFound
	}
	return run, nil
}

type wfEventsEnqueuer struct{}

func (wfEventsEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, nil
}

func wfTenantCtx() (context.Context, *gin.Context, *httptest.ResponseRecorder) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	return ctx, c, rec
}

func decodeWorkflowFrames(t *testing.T, body string) []types.WorkflowRunEvent {
	t.Helper()
	var frames []types.WorkflowRunEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var f types.WorkflowRunEvent
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &f))
		frames = append(frames, f)
	}
	return frames
}

const wfSSELinearDSL = `{"version":1,"components":{
	"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["a"]},
	"a":{"obj":{"component_name":"Answer","params":{"template":"echo {start@query}"}},"upstream":["start"],"downstream":[]}}}`

// A run that already reached a terminal state streams exactly one terminal
// frame and returns — no hanging stream for late subscribers.
func TestWorkflowRunEvents_TerminalRunTerminatesImmediately(t *testing.T) {
	env := newWorkflowEventsTestEnv(t, wfSSELinearDSL)
	ctx, c, rec := wfTenantCtx()

	// Execute a run synchronously to a terminal state first.
	svcRun, err := runSyncForSSETest(t, env, ctx)
	require.NoError(t, err)
	require.Equal(t, types.WorkflowRunStatusSucceeded, svcRun.Status)

	c.Params = gin.Params{{Key: "id", Value: "wf-sse"}, {Key: "run_id", Value: svcRun.ID}}
	env.handler.GetWorkflowRunEvents(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	frames := decodeWorkflowFrames(t, rec.Body.String())
	require.Len(t, frames, 1)
	assert.Equal(t, "run", frames[0].Kind)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, frames[0].Status)
}

// Unknown run ids fail closed as 404 before any SSE headers are written.
// (Direct-handler invocation: the status is written by the error-handler
// middleware in production, so assert the attached AppError — repo convention,
// see message_suggestion_test.go.)
func TestWorkflowRunEvents_UnknownRunIs404(t *testing.T) {
	env := newWorkflowEventsTestEnv(t, wfSSELinearDSL)
	_, c, rec := wfTenantCtx()

	c.Params = gin.Params{{Key: "id", Value: "wf-sse"}, {Key: "run_id", Value: "nope"}}
	env.handler.GetWorkflowRunEvents(c)

	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	assert.Equal(t, http.StatusNotFound, appErr.HTTPCode)
	assert.NotContains(t, rec.Header().Get("Content-Type"), "text/event-stream")
}

// runSyncForSSETest drives one synchronous run through the real service so
// the run row (and its terminal state) exists for the SSE assertions.
func runSyncForSSETest(t *testing.T, env *wfEventsTestEnv, ctx context.Context) (*types.WorkflowRun, error) {
	t.Helper()
	svc := env.handler.service
	runCh := make(chan *types.WorkflowRun, 1)
	errCh := make(chan error, 1)
	go func() {
		run, err := svc.RunWorkflow(ctx, "wf-sse", &types.RunWorkflowRequest{Query: "hi"})
		if err != nil {
			// Terminal failed runs are still runs — record row + error.
			if run != nil {
				runCh <- run
			}
			errCh <- err
			return
		}
		runCh <- run
	}()
	select {
	case run := <-runCh:
		return run, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Second):
		t.Fatal("sync run did not finish")
		return nil, nil
	}
}
