package service

import (
	"context"
	"errors"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingModelSvc returns a chat model whose Chat blocks until ctx is
// cancelled — driving the cancel-while-running path through the real
// executeWorkflowRun (registry -> runCtx -> adapter -> node Invoke).
type blockingModelSvc struct {
	interfaces.ModelService
	started chan struct{}
}

type blockingChat struct {
	started chan struct{}
}

func (c *blockingChat) Chat(ctx context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *blockingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented in stub")
}
func (c *blockingChat) GetModelName() string { return "blocking-stub" }
func (c *blockingChat) GetModelID() string   { return "blocking-stub" }

func (m *blockingModelSvc) GetChatModel(_ context.Context, _ string) (chat.Chat, error) {
	return &blockingChat{started: m.started}, nil
}

const blockingLLMDSL = `{"version":1,"components":{
	"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["llm"]},
	"llm":{"obj":{"component_name":"LLM","params":{"prompt":"{start@query}","model":"m-1"}},"upstream":["start"],"downstream":[]}}}`

// newCancelTestService wires a blocking model service so the run parks
// inside the LLM node until cancel aborts it. Returns the parking signal.
func newCancelTestService(t *testing.T) (interfaces.WorkflowService, *runRepoStub, chan struct{}) {
	t.Helper()
	wf := &types.Workflow{ID: "wf-c", TenantID: 42, Name: "wf", DSL: types.JSON(blockingLLMDSL)}
	repo := newRunRepoStub(wf)
	started := make(chan struct{})
	svc := NewWorkflowService(repo, &blockingModelSvc{started: started}, nil, nil)
	return svc, repo, started
}

func TestCancelWorkflowRun_AbortsInProcessRunAndKeepsCancelledTerminal(t *testing.T) {
	svc, repo, started := newCancelTestService(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	type res struct {
		err error
	}
	done := make(chan res, 1)
	go func() {
		_, err := svc.RunWorkflow(ctx, "wf-c", &types.RunWorkflowRequest{Query: "q"})
		done <- res{err}
	}()

	// Wait until the engine is parked inside the LLM node.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run never reached the LLM node")
	}
	runID := repo.lastCreatedRunID()
	require.NotEmpty(t, runID)

	cancelled, err := svc.CancelWorkflowRun(ctx, "wf-c", runID)
	require.NoError(t, err)
	require.NotNil(t, cancelled)
	assert.Equal(t, types.WorkflowRunStatusCancelled, cancelled.Status)

	// The engine aborts and its failure write is suppressed by the
	// cancelled-row guard — cancelled stays the terminal state.
	select {
	case r := <-done:
		require.Error(t, r.err, "sync run must surface an error after cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled run never returned")
	}
	final := repo.runRow(runID)
	require.NotNil(t, final)
	assert.Equal(t, types.WorkflowRunStatusCancelled, final.Status,
		"terminal write must not overwrite cancelled with failed/succeeded")
}

func TestCancelWorkflowRun_TerminalRunIsIdempotent(t *testing.T) {
	wf := &types.Workflow{ID: "wf-t", TenantID: 42, Name: "wf", DSL: types.JSON(linearDSL)}
	repo := newRunRepoStub(wf)
	repo.seedRun(&types.WorkflowRun{ID: "run-done", TenantID: 42, WorkflowID: "wf-t", Status: types.WorkflowRunStatusSucceeded})
	svc := NewWorkflowService(repo, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	run, err := svc.CancelWorkflowRun(ctx, "wf-t", "run-done")
	require.NoError(t, err, "cancelling a terminal run is idempotent, not a conflict")
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, repo.runRow("run-done").Status, "row must not change")
}

func TestCancelWorkflowRun_UnknownRunIs404(t *testing.T) {
	svc, _, _ := newCancelTestService(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	_, err := svc.CancelWorkflowRun(ctx, "wf-c", "nope")
	require.ErrorIs(t, err, apprepo.ErrWorkflowNotFound)
}

func TestCancelWorkflowRun_WorkflowMismatchIs404(t *testing.T) {
	wf := &types.Workflow{ID: "wf-a", TenantID: 42, Name: "wf", DSL: types.JSON(linearDSL)}
	repo := newRunRepoStub(wf)
	repo.seedRun(&types.WorkflowRun{ID: "run-x", TenantID: 42, WorkflowID: "wf-other", Status: types.WorkflowRunStatusRunning})
	svc := NewWorkflowService(repo, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	_, err := svc.CancelWorkflowRun(ctx, "wf-a", "run-x")
	require.ErrorIs(t, err, apprepo.ErrWorkflowNotFound)
}
