package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failedRunStub seeds the repo with a terminal failed run row.
func seedFailedRun(repo *runRepoStub, status string) *types.WorkflowRun {
	input, _ := json.Marshal(types.RunWorkflowRequest{Query: "original-query"})
	run := &types.WorkflowRun{
		ID:         "run-f1",
		TenantID:   10001,
		WorkflowID: "wf-async",
		Status:     status,
		Input:      types.JSON(input),
		Error:      "boom",
	}
	if repo.runs == nil {
		repo.runs = map[string]*types.WorkflowRun{}
	}
	repo.runs[run.ID] = run
	return run
}

// Resume of a failed run: original query restored from the Input document,
// one Resume-marked task enqueued, the row left in failed state until a
// worker picks it up.
func TestResumeWorkflowRun_FailedRunEnqueuesResumeTask(t *testing.T) {
	svc, repo, enq := newAsyncTestService(t, linearDSL)
	seedFailedRun(repo, types.WorkflowRunStatusFailed)

	run, err := svc.ResumeWorkflowRun(asyncTestCtx(), "wf-async", "run-f1")
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusFailed, run.Status, "row stays failed until the worker picks it up")

	require.Len(t, enq.tasks, 1)
	var payload types.WorkflowRunPayload
	require.NoError(t, json.Unmarshal(enq.tasks[0].Payload(), &payload))
	assert.Equal(t, "run-f1", payload.RunID)
	assert.True(t, payload.Resume, "payload must carry the resume marker")
	assert.Equal(t, "original-query", payload.Query, "original query restored from run.Input")
}

// Non-failed rows are 409 semantics: cancelled (explicit user stop) and
// succeeded (nothing to do) never resume; pending would double-dispatch.
func TestResumeWorkflowRun_NonFailedRejected(t *testing.T) {
	for _, status := range []string{
		types.WorkflowRunStatusCancelled,
		types.WorkflowRunStatusSucceeded,
		types.WorkflowRunStatusPending,
		types.WorkflowRunStatusRunning,
	} {
		svc, repo, _ := newAsyncTestService(t, linearDSL)
		seedFailedRun(repo, status)
		_, err := svc.ResumeWorkflowRun(asyncTestCtx(), "wf-async", "run-f1")
		require.ErrorIs(t, err, ErrWorkflowRunNotResumable, "status=%s", status)
	}
}

// The asynq handler widens its row guard for Resume payloads: a failed row
// is re-driven; any other state no-ops.
func TestProcessWorkflowRun_ResumeGuard(t *testing.T) {
	svc, repo, _ := newAsyncTestService(t, linearDSL)
	seedFailedRun(repo, types.WorkflowRunStatusFailed)

	payload, _ := json.Marshal(types.WorkflowRunPayload{
		RunID: "run-f1", WorkflowID: "wf-async", TenantID: 10001, Query: "original-query", Resume: true,
	})
	require.NoError(t, svc.ProcessWorkflowRun(context.Background(), asynq.NewTask(types.TypeWorkflowRun, payload)))

	// The failed row was driven to a terminal state by the inline Lite-mode
	// enqueuer-free execution path.
	final := repo.runs["run-f1"]
	require.NotNil(t, final)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, final.Status)

	// A resume re-delivery after terminal state no-ops (guard skips).
	require.NoError(t, svc.ProcessWorkflowRun(context.Background(), asynq.NewTask(types.TypeWorkflowRun, payload)))
}

// Unknown run ids and cross-workflow ids map to the shared 404 sentinel.
func TestResumeWorkflowRun_NotFound(t *testing.T) {
	svc, _, _ := newAsyncTestService(t, linearDSL)
	_, err := svc.ResumeWorkflowRun(asyncTestCtx(), "wf-async", "nope")
	require.Error(t, err)

	svc2, repo2, _ := newAsyncTestService(t, linearDSL)
	seedFailedRun(repo2, types.WorkflowRunStatusFailed)
	_, err = svc2.ResumeWorkflowRun(asyncTestCtx(), "wf-other", "run-f1")
	require.Error(t, err)
}
