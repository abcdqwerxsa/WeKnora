package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// eventLog is a thread-safe NodeEvent collector.
type eventLog struct {
	mu     sync.Mutex
	events []NodeEvent
}

func (l *eventLog) record(ev NodeEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *eventLog) phases(id string) []NodePhase {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []NodePhase
	for _, ev := range l.events {
		if ev.NodeID == id {
			out = append(out, ev.Phase)
		}
	}
	return out
}

func linearDeps(log *eventLog) Deps {
	return Deps{
		LLMFunc: func(_ context.Context, req nodes.LLMRequest) (string, error) {
			return fmt.Sprintf("answer about: %s", req.Prompt), nil
		},
		RetrievalFunc: func(_ context.Context, req nodes.RetrievalRequest) (*nodes.RetrievalResult, error) {
			return &nodes.RetrievalResult{
				Chunks:  []map[string]any{{"content": "chunk for " + req.Query}},
				DocAggs: []map[string]any{{"doc": "doc1"}},
			}, nil
		},
		OnNodeEvent: log.record,
	}
}

func TestCompileRunLinearGraph(t *testing.T) {
	log := &eventLog{}
	wf, err := Compile(linearDSL(), linearDeps(log))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := wf.Run(context.Background(), "what is weknora", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(res.Answer, "answer about:") {
		t.Errorf("Answer = %q, want llm stub echo", res.Answer)
	}
	if got, want := res.Path, []string{"start", "retr", "llm", "ans"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Path = %v, want %v", got, want)
	}
	// outputs recorded per node
	if v, ok := res.Outputs.Outputs["retr"]["chunks"]; !ok {
		t.Errorf("retr outputs missing chunks: %+v", res.Outputs.Outputs["retr"])
	} else if _, isList := v.([]map[string]any); !isList {
		t.Errorf("chunks type = %T, want []map[string]any", v)
	}
	// sys namespace populated
	if v, _ := res.Outputs.Sys["query"]; v != "what is weknora" {
		t.Errorf("sys.query = %v", v)
	}
	// events: started+finished for every node, in path order, no failures
	for _, id := range []string{"start", "retr", "llm", "ans"} {
		got := log.phases(id)
		if len(got) != 2 || got[0] != PhaseStarted || got[1] != PhaseFinished {
			t.Errorf("node %s phases = %v, want [started finished]", id, got)
		}
	}
}

func TestCompileRunSwitchBothBranches(t *testing.T) {
	// start -> switch; switch routes by sys.query; both branches converge on ans.
	dsl := &DSL{
		Version: 1,
		Components: map[string]*Component{
			"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"sw"}},
			"sw": {
				Obj: ComponentObj{ComponentName: "Switch", Params: map[string]any{
					"value": "{sys.query}",
					"cases": []any{
						map[string]any{"value": "tech", "to": "llm_a"},
						map[string]any{"value": "legal", "to": "llm_b"},
					},
					"default": "llm_b",
				}},
				Upstream:   []string{"start"},
				Downstream: []string{"llm_a", "llm_b"},
			},
			"llm_a": {
				Obj:        ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "A:{sys.query}"}},
				Upstream:   []string{"sw"},
				Downstream: []string{"ans"},
			},
			"llm_b": {
				Obj:        ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "B:{sys.query}"}},
				Upstream:   []string{"sw"},
				Downstream: []string{"ans"},
			},
			"ans": {
				Obj:      ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{sys.query}"}},
				Upstream: []string{"llm_a", "llm_b"},
			},
		},
	}

	var mu sync.Mutex
	prompts := []string{}
	deps := linearDeps(&eventLog{})
	deps.LLMFunc = func(_ context.Context, req nodes.LLMRequest) (string, error) {
		mu.Lock()
		prompts = append(prompts, req.Prompt)
		mu.Unlock()
		return "stub:" + req.Prompt, nil
	}

	wf, err := Compile(dsl, deps)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	res, err := wf.Run(context.Background(), "tech", nil)
	if err != nil {
		t.Fatalf("Run(tech): %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "A:tech" {
		t.Errorf("tech run prompts = %v, want [A:tech]", prompts)
	}
	if !slices.Contains(res.Path, "llm_a") || slices.Contains(res.Path, "llm_b") {
		t.Errorf("tech run path = %v, must include llm_a only", res.Path)
	}

	res, err = wf.Run(context.Background(), "legal", nil)
	if err != nil {
		t.Fatalf("Run(legal): %v", err)
	}
	if len(prompts) != 2 || prompts[1] != "B:legal" {
		t.Errorf("legal run prompts = %v, want [..., B:legal]", prompts)
	}

	// default route: unmatched value falls to llm_b
	res, err = wf.Run(context.Background(), "anything-else", nil)
	if err != nil {
		t.Fatalf("Run(default): %v", err)
	}
	if len(prompts) != 3 || prompts[2] != "B:anything-else" {
		t.Errorf("default run prompts = %v, want [..., B:anything-else]", prompts)
	}
}

func TestCompileRunSwitchNoMatchNoDefaultFails(t *testing.T) {
	dsl := &DSL{
		Version: 1,
		Components: map[string]*Component{
			"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"sw"}},
			"sw": {
				Obj: ComponentObj{ComponentName: "Switch", Params: map[string]any{
					"value": "{sys.query}",
					"cases": []any{map[string]any{"value": "x", "to": "ans"}},
				}},
				Upstream:   []string{"start"},
				Downstream: []string{"ans"},
			},
			"ans": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{sys.query}"}}, Upstream: []string{"sw"}},
		},
	}
	wf, err := Compile(dsl, linearDeps(&eventLog{}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = wf.Run(context.Background(), "nomatch", nil)
	if err == nil {
		t.Fatal("switch with no match and no default must fail the run")
	}
}

func TestCompileTopologyErrors(t *testing.T) {
	mk := func(mutate func(map[string]*Component)) *DSL {
		comps := linearDSL().Components
		mutate(comps)
		return &DSL{Version: 1, Components: comps}
	}
	t.Run("two entries", func(t *testing.T) {
		_, err := Compile(mk(func(c map[string]*Component) {
			c["start"].Downstream = nil // detach: start and retr both become entries
			c["retr"].Upstream = nil
		}), linearDeps(&eventLog{}))
		if err == nil || !strings.Contains(err.Error(), "entry") {
			t.Errorf("err = %v, want entry error", err)
		}
	})
	t.Run("two terminals", func(t *testing.T) {
		// RELAXED: parallel branches need not converge — two terminals are
		// legal; every executed branch records outputs. The run result comes
		// from the CanvasState, not the (merged-empty) graph output.
		wf, err := Compile(mk(func(c map[string]*Component) {
			c["llm"].Downstream = nil // ans and retr both terminal
		}), linearDeps(&eventLog{}))
		if err != nil {
			t.Fatalf("multi-terminal must compile: %v", err)
		}
		if _, err := wf.Run(context.Background(), "q", nil); err != nil {
			t.Errorf("multi-terminal run: %v", err)
		}
	})
	t.Run("unknown downstream", func(t *testing.T) {
		_, err := Compile(mk(func(c map[string]*Component) {
			c["retr"].Downstream = []string{"ghost"}
		}), linearDeps(&eventLog{}))
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Errorf("err = %v, want unknown downstream error", err)
		}
	})
	t.Run("missing factory param", func(t *testing.T) {
		_, err := Compile(mk(func(c map[string]*Component) {
			delete(c["ans"].Obj.Params, "template")
		}), linearDeps(&eventLog{}))
		if err == nil {
			t.Error("Answer without template must fail compile")
		}
	})
}

func TestCompileMissingDependencyFailsAtRun(t *testing.T) {
	deps := linearDeps(&eventLog{})
	deps.LLMFunc = nil // graph contains an LLM node but no LLMFunc injected
	wf, err := Compile(linearDSL(), deps)
	if err != nil {
		t.Fatalf("Compile should succeed without LLMFunc (deferred to run): %v", err)
	}
	_, err = wf.Run(context.Background(), "q", nil)
	if err == nil || !strings.Contains(err.Error(), "LLMFunc") {
		t.Errorf("err = %v, want clear LLMFunc-missing error", err)
	}
}

func TestRunTemplateFailureNamesRef(t *testing.T) {
	// llm references a node param that is never produced
	dsl := linearDSL()
	dsl.Components["llm"].Obj.Params["prompt"] = "{ghost@param}"
	wf, err := Compile(dsl, linearDeps(&eventLog{}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = wf.Run(context.Background(), "q", nil)
	if err == nil || !strings.Contains(err.Error(), "ghost@param") {
		t.Errorf("err = %v, want unresolved-ref error naming ghost@param", err)
	}
}

// ---- Phase 5: node error policy (continue / route_to / retry) -------------

// errDeps returns Deps whose LLM always fails (error-policy tests).
func errDeps(log *eventLog, calls *int32) Deps {
	return Deps{
		LLMFunc: func(_ context.Context, _ nodes.LLMRequest) (string, error) {
			if calls != nil {
				atomic.AddInt32(calls, 1)
			}
			return "", fmt.Errorf("llm exploded")
		},
		OnNodeEvent: log.record,
	}
}

func TestErrorPolicyContinueRecordsDefaults(t *testing.T) {
	log := &eventLog{}
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start", Params: map[string]any{}}, Downstream: []string{"llm"}},
		"llm": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{
			"prompt": "{start@query}", "model": "m",
			"on_error": map[string]any{
				"action":          "continue",
				"default_outputs": map[string]any{"content": "fallback text"},
			},
		}}, Downstream: []string{"ans"}},
		"ans": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{llm@content}"}}, Downstream: nil},
	}}
	wf, err := Compile(dsl, errDeps(log, nil))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, rerr := wf.Run(context.Background(), "q", nil)
	if rerr != nil {
		t.Fatalf("continue policy must not fail the run: %v", rerr)
	}
	if res.Answer != "fallback text" {
		t.Errorf("answer=%q want fallback text", res.Answer)
	}
	// The node still shows as finished (with _error recorded).
	if v, ok := res.Outputs.Outputs["llm"]["_error"]; !ok || v == "" {
		t.Errorf("llm outputs must record _error, got %v", res.Outputs.Outputs["llm"])
	}
}

func TestErrorPolicyRouteToBranchesOnFailure(t *testing.T) {
	log := &eventLog{}
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start", Params: map[string]any{}}, Downstream: []string{"llm"}},
		"llm": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{
			"prompt": "{start@query}", "model": "m",
			"on_error": map[string]any{
				"action":   "route_to",
				"route_to": "fixer",
			},
		}}, Upstream: []string{"start"}, Downstream: []string{"ans", "fixer"}},
		// Both branch arms converge on the single terminal.
		"ans":   {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "never reached"}}, Upstream: []string{"llm"}, Downstream: []string{"end"}},
		"fixer": {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "handled"}}, Upstream: []string{"llm"}, Downstream: []string{"end"}},
		"end":   {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{fixer@text}"}}, Upstream: []string{"ans", "fixer"}},
	}}
	wf, err := Compile(dsl, errDeps(log, nil))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, rerr := wf.Run(context.Background(), "q", nil)
	if rerr != nil {
		t.Fatalf("route_to must not fail the run: %v", rerr)
	}
	if res.Answer != "handled" {
		t.Errorf("answer=%q want the error-branch handler output", res.Answer)
	}
}

func TestErrorPolicyRouteToRequiresSingleDownstream(t *testing.T) {
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start", Params: map[string]any{}}, Downstream: []string{"llm"}},
		"llm": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{
			"prompt": "p", "model": "m",
			"on_error": map[string]any{"action": "route_to", "route_to": "h"},
		}}, Downstream: []string{"a", "b"}},
		"a": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "a"}}, Downstream: nil},
		"b": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "b"}}, Downstream: nil},
		"h": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "h"}}, Downstream: nil},
	}}
	// Multi-terminal is also invalid here; either way compilation must fail
	// with the route_to constraint, not mis-route at run time.
	if _, err := Compile(dsl, Deps{}); err == nil {
		t.Error("route_to with fan-out downstream must fail compilation")
	}
}

func TestNodeRetryRetriesFailedInvokes(t *testing.T) {
	log := &eventLog{}
	var calls int32
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start", Params: map[string]any{}}, Downstream: []string{"llm"}},
		"llm": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{
			"prompt": "{start@query}", "model": "m",
			"retry": map[string]any{"count": 2, "delay_ms": 1},
		}}, Downstream: []string{"ans"}},
		"ans": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{llm@content}"}}, Downstream: nil},
	}}
	wf, err := Compile(dsl, errDeps(log, &calls))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, rerr := wf.Run(context.Background(), "q", nil); rerr == nil {
		t.Fatal("exhausted retries must still fail the run")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("invoke calls=%d want 3", calls)
	}
}

// ---- multi-terminal / parallel relaxation ----------------------------------

func TestCompileRunParallelFanOutMultiTerminal(t *testing.T) {
	// start fans out to two LLM branches that NEVER converge; both are
	// terminals. Every branch must execute and record its outputs.
	log := &eventLog{}
	dsl := &DSL{
		Version: 1, Components: map[string]*Component{
			"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"llm_a", "llm_b"}},
			"llm_a": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "A:{sys.query}"}}, Upstream: []string{"start"}},
			"llm_b": {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "B:{sys.query}"}}, Upstream: []string{"start"}},
		},
	}
	deps := linearDeps(log)
	wf, err := Compile(dsl, deps)
	if err != nil {
		t.Fatalf("Compile (multi-terminal): %v", err)
	}
	res, err := wf.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Both branches executed and recorded outputs.
	if _, ok := res.Outputs.Outputs["llm_a"]["content"]; !ok {
		t.Errorf("llm_a outputs missing: %+v", res.Outputs.Outputs["llm_a"])
	}
	if _, ok := res.Outputs.Outputs["llm_b"]["content"]; !ok {
		t.Errorf("llm_b outputs missing: %+v", res.Outputs.Outputs["llm_b"])
	}
	// Path contains start + both branches (order is completion order).
	if len(res.Path) != 3 {
		t.Errorf("Path = %v, want 3 nodes", res.Path)
	}
	// No Answer node ran → empty answer, run still succeeds.
	if res.Answer != "" {
		t.Errorf("Answer = %q, want empty", res.Answer)
	}
}

func TestCompileRunParallelMultipleAnswerTerminals(t *testing.T) {
	// Two Answer terminals in parallel: the first to complete in path order
	// wins the run's Answer (documented nondeterminism).
	dsl := &DSL{
		Version: 1, Components: map[string]*Component{
			"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"ans_a", "ans_b"}},
			"ans_a": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "from a"}}, Upstream: []string{"start"}},
			"ans_b": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "from b"}}, Upstream: []string{"start"}},
		},
	}
	wf, err := Compile(dsl, linearDeps(&eventLog{}))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := wf.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "from a" && res.Answer != "from b" {
		t.Errorf("Answer = %q, want one of the branches", res.Answer)
	}
}

func TestCompileStillRejectsZeroTerminals(t *testing.T) {
	// A pure cycle has no terminal — still rejected.
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"a"}},
		"a":     {Obj: ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "x"}}, Upstream: []string{"start", "b"}, Downstream: []string{"b"}},
		"b":     {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "y"}}, Upstream: []string{"a"}, Downstream: []string{"a"}},
	}}
	if _, err := Compile(dsl, Deps{}); err == nil {
		t.Error("cycle without terminal must fail compilation")
	}
}
