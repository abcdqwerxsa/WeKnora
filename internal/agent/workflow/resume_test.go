package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// okLLM is a plain succeeding LLMFunc with an invocation counter.
func okLLM(reply string) (nodes.LLMFunc, *int64) {
	var n int64
	return func(_ context.Context, _ nodes.LLMRequest) (string, error) {
		atomic.AddInt64(&n, 1)
		return reply, nil
	}, &n
}

// blockingFirstLLM blocks on its FIRST invocation until the run context is
// cancelled (simulating a node hanging on a slow model); later invocations
// succeed immediately. Only usable with a cancellable run context.
func blockingFirstLLM(reply string) (nodes.LLMFunc, *int64) {
	var n int64
	return func(ctx context.Context, _ nodes.LLMRequest) (string, error) {
		if atomic.AddInt64(&n, 1) == 1 {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return reply, nil
	}, &n
}

// resumableGraph: start -> llm -> answer. The answer template references
// BOTH the skipped node's output ({start@query}) and the re-executed
// node's output ({llm@content}) — proving both sides of the resume
// contract (eino channel restore + CanvasState side-car).
func resumableGraph() *DSL {
	return &DSL{
		Version: 1,
		Components: map[string]*Component{
			"start": {
				Obj:        ComponentObj{ComponentName: "Start", Params: map[string]any{}},
				Upstream:   []string{},
				Downstream: []string{"llm"},
			},
			"llm": {
				Obj:        ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "{start@query}", "model": "m-1"}},
				Upstream:   []string{"start"},
				Downstream: []string{"ans"},
			},
			"ans": {
				Obj:        ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "res:{start@query}:{llm@content}"}},
				Upstream:   []string{"llm"},
				Downstream: []string{},
			},
		},
	}
}

func TestRunWithOptions_CheckpointResume(t *testing.T) {
	llm, calls := blockingFirstLLM("llm-reply")

	var mu sync.Mutex
	events := []NodeEvent{}
	deps := Deps{
		LLMFunc: llm,
		OnNodeEvent: func(ev NodeEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
		CheckpointKV:  NewMemKV(),
		CheckpointTTL: time.Hour,
	}

	compiled, err := Compile(resumableGraph(), deps)
	require.NoError(t, err)

	// ---- First run: cancel while the LLM node hangs. ----
	runCtx, cancel := context.WithCancel(context.Background())
	go func() {
		// Let the graph complete "start" and enter "llm", then abort.
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, firstErr := compiled.RunWithOptions(runCtx, "hello", nil, RunOptions{CheckpointID: "run-1"})
	require.Error(t, firstErr, "first run must abort with the context error")
	require.EqualValues(t, 1, atomic.LoadInt64(calls))

	// The CanvasState side-car was persisted despite the failure — that is
	// the resume input.
	kv := deps.CheckpointKV
	_, ok, gerr := kv.Get(context.Background(), ctxStateKey("run-1"))
	require.NoError(t, gerr)
	assert.True(t, ok, "CanvasState side-car must be persisted on failure")

	// ---- Resume: same CheckpointID, fresh context. ----
	res, rerr := compiled.RunWithOptions(context.Background(), "hello", nil, RunOptions{CheckpointID: "run-1"})
	require.NoError(t, rerr)

	// The hanging node re-executed (its first attempt never completed); the
	// start node did NOT (no second finished event for it).
	require.EqualValues(t, 2, atomic.LoadInt64(calls), "hanging node re-runs on resume")

	mu.Lock()
	startFinished := 0
	for _, ev := range events {
		if ev.NodeID == "start" && ev.Phase == PhaseFinished {
			startFinished++
		}
	}
	mu.Unlock()
	assert.Equal(t, 1, startFinished, "completed node must not re-execute on resume")

	// Template refs resolve across the resume boundary: {start@query} comes
	// from the restored CanvasState, {llm@content} from the re-run node.
	assert.Equal(t, "res:hello:llm-reply", res.Answer)

	// Success cleans up the side-car (best-effort delete).
	_, ok, gerr = kv.Get(context.Background(), ctxStateKey("run-1"))
	require.NoError(t, gerr)
	assert.False(t, ok, "side-car deleted on success")
}

// Without a CheckpointID the behaviour is identical to Run: no KV traffic,
// fresh execution, nothing hangs on non-blocking deps.
func TestRunWithOptions_NoCheckpointIsFreshRun(t *testing.T) {
	llm, calls := okLLM("r2")
	compiled, err := Compile(resumableGraph(), Deps{
		LLMFunc:      llm,
		CheckpointKV: NewMemKV(),
	})
	require.NoError(t, err)

	res, err := compiled.Run(context.Background(), "q", nil)
	require.NoError(t, err)
	assert.Equal(t, "res:q:r2", res.Answer)
	assert.EqualValues(t, 1, atomic.LoadInt64(calls))
}

// A CheckpointID whose KV has no entry (fresh id) is a normal run — the
// documented KV-miss degradation.
func TestRunWithOptions_KVMissRunsFresh(t *testing.T) {
	llm, calls := okLLM("r3")
	compiled, err := Compile(resumableGraph(), Deps{
		LLMFunc:      llm,
		CheckpointKV: NewMemKV(),
	})
	require.NoError(t, err)

	res, err := compiled.RunWithOptions(context.Background(), "q", nil, RunOptions{CheckpointID: "never-seen"})
	require.NoError(t, err)
	assert.Equal(t, "res:q:r3", res.Answer)
	assert.EqualValues(t, 1, atomic.LoadInt64(calls))
}
