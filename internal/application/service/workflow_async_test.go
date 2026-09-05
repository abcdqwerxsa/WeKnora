package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureEnqueuer records enqueued asynq tasks without touching redis.
type captureEnqueuer struct {
	mu    sync.Mutex
	tasks []*asynq.Task
}

func (e *captureEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{}, nil
}

func newAsyncTestService(t *testing.T, dsl string) (*workflowService, *runRepoStub, *captureEnqueuer) {
	t.Helper()
	wf := &types.Workflow{ID: "wf-async", TenantID: 10001, Name: "wf", DSL: types.JSON(dsl)}
	repo := newRunRepoStub(wf)
	enq := &captureEnqueuer{}
	svc := NewWorkflowService(repo, &wfStubModelSvc{reply: "llm-answer"}, &wfStubKBSvc{}, enq)
	return svc.(*workflowService), repo, enq
}

func asyncTestCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
}

// Async=true returns a pending run immediately and enqueues exactly one
// workflow:run task carrying the tenant + run identity.
func TestRunWorkflowAsync_EnqueuesTaskAndReturnsPending(t *testing.T) {
	svc, repo, enq := newAsyncTestService(t, linearDSL)

	run, err := svc.RunWorkflow(asyncTestCtx(), "wf-async", &types.RunWorkflowRequest{Query: "hello", Async: true})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.WorkflowRunStatusPending, run.Status)

	require.Len(t, enq.tasks, 1)
	task := enq.tasks[0]
	assert.Equal(t, types.TypeWorkflowRun, task.Type())
	var payload types.WorkflowRunPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	assert.Equal(t, run.ID, payload.RunID)
	assert.Equal(t, "wf-async", payload.WorkflowID)
	assert.Equal(t, uint64(10001), payload.TenantID)
	assert.Equal(t, "hello", payload.Query)

	// Only the pending row exists so far — no execution happened yet.
	require.Len(t, repo.created, 1)
	assert.Empty(t, repo.updated, "async dispatch must not execute inline")
}

// The asynq handler restores the tenant context from the payload, drives
// the pending run to succeeded, and a re-delivery of the same task is a
// no-op (the run row is the retry authority).
func TestProcessWorkflowRun_DrivesRunToTerminalAndIsIdempotent(t *testing.T) {
	svc, repo, enq := newAsyncTestService(t, linearDSL)

	_, err := svc.RunWorkflow(asyncTestCtx(), "wf-async", &types.RunWorkflowRequest{Query: "hello", Async: true})
	require.NoError(t, err)
	require.Len(t, enq.tasks, 1)
	task := enq.tasks[0]

	// Worker context carries NO tenant — the payload is the only source.
	require.NoError(t, svc.ProcessWorkflowRun(context.Background(), task))
	final := repo.updated[len(repo.updated)-1]
	assert.Equal(t, types.WorkflowRunStatusSucceeded, final.Status)
	assert.Contains(t, string(final.Output), "result: llm-answer")

	// Re-delivery: status is terminal, handler must not touch it again.
	updatesBefore := len(repo.updated)
	require.NoError(t, svc.ProcessWorkflowRun(context.Background(), task))
	assert.Len(t, repo.updated, updatesBefore, "re-delivery of a non-pending run must be a no-op")
}

// Execution failure inside the handler still returns nil (the run row is
// the outcome); the row is left failed.
func TestProcessWorkflowRun_FailureIsTerminalRowNotTaskError(t *testing.T) {
	// Retrieval node with zero kb_ids fails at Invoke time.
	bad := `{"version":1,"components":{
		"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["r"]},
		"r":{"obj":{"component_name":"Retrieval","params":{"query":"{start@query}","kb_ids":[]}},"upstream":["start"],"downstream":[]}}}`
	svc, repo, enq := newAsyncTestService(t, bad)

	_, err := svc.RunWorkflow(asyncTestCtx(), "wf-async", &types.RunWorkflowRequest{Query: "q", Async: true})
	require.NoError(t, err)

	err = svc.ProcessWorkflowRun(context.Background(), enq.tasks[0])
	assert.NoError(t, err, "execution failures must not become asynq retries")
	final := repo.updated[len(repo.updated)-1]
	assert.Equal(t, types.WorkflowRunStatusFailed, final.Status)
	assert.NotEmpty(t, final.Error)
}

// A subscriber attached before execution receives node frames plus the
// terminal run frame, and the channel closes afterwards.
func TestWorkflowRunEvents_BrokerDeliversFramesAndCloses(t *testing.T) {
	// Subscribe BEFORE executing — simulate an SSE client on a pending run.
	svc2, repo2, _ := newAsyncTestService(t, linearDSL)
	run2, err := svc2.RunWorkflow(asyncTestCtx(), "wf-async", &types.RunWorkflowRequest{Query: "hi", Async: true})
	require.NoError(t, err)
	ch, cancel := svc2.SubscribeWorkflowRunEvents(run2.ID)
	defer cancel()

	require.NoError(t, svc2.ProcessWorkflowRun(context.Background(), asynq.NewTask(
		types.TypeWorkflowRun, mustJSON(t, types.WorkflowRunPayload{
			RunID: run2.ID, WorkflowID: "wf-async", TenantID: 10001, Query: "hi",
		}))))

	var frames []types.WorkflowRunEvent
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				break loop
			}
			frames = append(frames, f)
			if f.Kind == "run" {
				// Next receive must be the channel close.
			}
		case <-timeout:
			t.Fatal("broker did not close the channel after the terminal frame")
		}
	}
	require.NotEmpty(t, frames)
	terminal := frames[len(frames)-1]
	assert.Equal(t, "run", terminal.Kind)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, terminal.Status)
	assert.Equal(t, repo2.updated[len(repo2.updated)-1].Status, terminal.Status)
}

// Node frames are mirrored onto the global event bus (observability path).
func TestWorkflowRunEvents_GlobalBusEmission(t *testing.T) {
	event.Clear()
	defer event.Clear()

	var mu sync.Mutex
	var seen []event.Event
	event.On(event.EventWorkflowNode, func(_ context.Context, e event.Event) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, e)
		return nil
	})
	event.On(event.EventWorkflowRunFinished, func(_ context.Context, e event.Event) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, e)
		return nil
	})

	svc, _, _ := newAsyncTestService(t, linearDSL)
	_, err := svc.RunWorkflow(asyncTestCtx(), "wf-async", &types.RunWorkflowRequest{Query: "hello"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seen)
	hasNode := false
	hasTerminal := false
	for _, e := range seen {
		frame, ok := e.Data.(types.WorkflowRunEvent)
		require.True(t, ok, "event data must carry the frame struct")
		if e.Type == event.EventWorkflowNode {
			hasNode = true
			assert.Equal(t, "node", frame.Kind)
		}
		if e.Type == event.EventWorkflowRunFinished {
			hasTerminal = true
			assert.Equal(t, types.WorkflowRunStatusSucceeded, frame.Status)
		}
	}
	assert.True(t, hasNode, "node frames must reach the global bus")
	assert.True(t, hasTerminal, "terminal frame must reach the global bus")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
