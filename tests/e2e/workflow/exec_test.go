package e2e

// Execution-plane scenarios: sync/async runs, SSE progress, cancel, resume,
// schedules, history/detail.
import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowLLMComps builds start -> llm(long) -> answer for streaming/cancel tests.
func slowLLMComps(maxTokens int) map[string]comp {
	return linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("llm", "LLM", map[string]any{
			"model":         chatModelID,
			"prompt":        "Count slowly from 1 to 100, one number per line, explaining each.",
			"system_prompt": "Always produce the fullest possible answer.",
			"max_tokens":    maxTokens,
		}),
		node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
	)
}

// #23 Sync run reaches a terminal state with an answer.
func TestSyncRunTerminal(t *testing.T) {
	id := createWorkflow(t, "exec-sync", slowLLMComps(256))
	r := runSync(t, id, nil)
	require.Equal(t, "succeeded", r.Status, "%s", r.Error)
	assert.NotEmpty(t, answerOf(t, r))
}

// #24 Async run is accepted, executes in the queue, and records a full trace.
func TestAsyncRunCompletes(t *testing.T) {
	id := createWorkflow(t, "exec-async", slowLLMComps(256))
	r := runAsyncAwait(t, id, nil, 120*time.Second)
	require.Equal(t, "succeeded", r.Status, "%s", r.Error)
	require.NotEmpty(t, r.Trace, "async run must persist its trace")
	assert.NotNil(t, traceOf(t, r, "llm"))
}

// #25 SSE stream carries node frames and a terminal run frame.
func TestSSEProgressFrames(t *testing.T) {
	id := createWorkflow(t, "exec-sse", slowLLMComps(256))
	body := map[string]any{"async": true, "query": "count slowly"}
	out := startRun(t, id, body)
	frames := collectSSE(t, id, out.ID, 120*time.Second)
	require.NotEmpty(t, frames, "stream must deliver frames")
	var nodeFrames, terminal int
	for _, f := range frames {
		switch f["kind"] {
		case "node":
			nodeFrames++
		case "run":
			terminal++
		}
	}
	assert.GreaterOrEqual(t, nodeFrames, 2, "expect started+finished node frames; got %d", nodeFrames)
	assert.Equal(t, 1, terminal, "exactly one terminal run frame; got %d", terminal)
}

// #26 Cancel stops a running workflow.
func TestCancelRunningWorkflow(t *testing.T) {
	id := createWorkflow(t, "exec-cancel", slowLLMComps(8192))
	body := map[string]any{"async": true, "query": "count slowly"}
	out := startRun(t, id, body)
	resp := call(t, "POST", "/api/v1/workflows/"+id+"/runs/"+out.ID+"/cancel", nil)
	require.Equal(t, http.StatusOK, resp.Status, "cancel: %s", resp.Body)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		cur := getRun(t, id, out.ID)
		if isTerminal(cur.Status) {
			assert.Equal(t, "cancelled", cur.Status, "run should end cancelled, not %s", cur.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("run did not reach terminal state after cancel")
}

// #27 Resume: a failed run recovers after the DSL is repaired; completed
// nodes are replayed from the checkpoint instead of re-executed.
func TestResumeFailedRun(t *testing.T) {
	dead := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"dead"}},
		"dead": {Kind: "HTTP", Params: map[string]any{
			"method": "GET", "url": "http://10.255.255.1:1/", "timeout_seconds": 1,
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("recovered"), Upstream: []string{"dead"}},
	}
	id := createWorkflow(t, "exec-resume", dead)
	first := runSync(t, id, nil)
	require.Equal(t, "failed", first.Status, "setup: run must fail first")

	// Repair the node, then resume the failed run.
	dead["dead"] = comp{Kind: "HTTP", Params: map[string]any{
		"method": "GET", "url": baseURL + "/api/v1/none", "timeout_seconds": 5,
	}, Upstream: []string{"start"}, Downstream: []string{"ans"}}
	updateWorkflowDSL(t, id, dead)

	resp := call(t, "POST", "/api/v1/workflows/"+id+"/runs/"+first.ID+"/resume", nil)
	require.Equal(t, http.StatusOK, resp.Status, "resume: %s", resp.Body)
	var resumed run
	decodeInto(t, resp.Run, &resumed)

	// The resume response itself still reads status=failed (the resumable
	// marker); the worker flips it to running and then a terminal state.
	// Poll for success, tolerating the transient failed marker.
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		cur := getRun(t, id, first.ID)
		if cur.Status == "succeeded" {
			require.NotEmpty(t, cur.Trace)
			if e := traceOf(t, cur, "start"); e != nil {
				assert.True(t, e.Replayed, "completed start node must be replayed, not re-executed")
			}
			assert.Equal(t, "recovered", answerOf(t, cur))
			return
		}
		time.Sleep(700 * time.Millisecond)
	}
	last := getRun(t, id, first.ID)
	t.Fatalf("resumed run did not succeed within deadline (last: %s %s)", last.Status, last.Error)
}

// #28 Schedules fire for published workflows and stop when disabled.
func TestScheduleFiresAndDisables(t *testing.T) {
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("ans", "Answer", map[string]any{"template": "tick:{sys.query}"}),
	)
	id := createWorkflow(t, "exec-schedule", comps)
	publishWorkflow(t, id)

	resp := call(t, "POST", "/api/v1/workflows/"+id+"/schedules", map[string]any{
		"cron": "* * * * *", "query": "scheduled-probe",
	})
	require.Equal(t, http.StatusCreated, resp.Status, "create schedule: %s", resp.Body)
	var sched struct {
		ID string `json:"id"`
	}
	decodeInto(t, resp.Data, &sched)
	require.NotEmpty(t, sched.ID)
	defer call(t, "DELETE", "/api/v1/workflows/"+id+"/schedules/"+sched.ID, nil)

	// Wait for at least one tick (cron granularity is one minute).
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) && len(listRuns(t, id)) == 0 {
		time.Sleep(3 * time.Second)
	}
	require.NotEmpty(t, listRuns(t, id), "schedule must fire within one cron period")

	// Disable and confirm no additional runs appear after a grace period.
	resp = call(t, "POST", "/api/v1/workflows/"+id+"/schedules/"+sched.ID+"/disable", nil)
	require.Equal(t, http.StatusOK, resp.Status, "disable: %s", resp.Body)
	before := len(listRuns(t, id))
	time.Sleep(70 * time.Second)
	after := len(listRuns(t, id))
	assert.LessOrEqual(t, after, before+1, "disabled schedule must stop firing (±1 boundary tick)")
}

// #29 Run history lists newest-first with complete detail rows.
func TestRunHistoryAndDetail(t *testing.T) {
	id := createWorkflow(t, "exec-history", slowLLMComps(128))
	r1 := requireRunSucceeded(t, id, map[string]any{"query": "first"})
	r2 := requireRunSucceeded(t, id, map[string]any{"query": "second"})

	runs := listRuns(t, id)
	require.GreaterOrEqual(t, len(runs), 2)
	assert.Equal(t, r2.ID, runs[0].ID, "history must be newest-first")
	assert.Equal(t, r1.ID, runs[1].ID)

	detail := getRun(t, id, r1.ID)
	assert.Equal(t, "succeeded", detail.Status)
	assert.NotEmpty(t, detail.Trace, "detail endpoint must include the trace")
	var input map[string]any
	require.NoError(t, json.Unmarshal(firstRunInput(t, id, r1.ID), &input))
	assert.Equal(t, "first", input["query"], "run detail must record the input document")
}

// firstRunInput fetches the raw input JSON of one run.
func firstRunInput(t *testing.T, id, runID string) json.RawMessage {
	t.Helper()
	r := call(t, "GET", "/api/v1/workflows/"+id+"/runs/"+runID, nil)
	require.Equal(t, http.StatusOK, r.Status)
	var row struct {
		Input json.RawMessage `json:"input"`
	}
	decodeInto(t, r.Data, &row)
	return row.Input
}
