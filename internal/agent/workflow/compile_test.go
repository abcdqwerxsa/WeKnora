package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	if !containsNode(res.Path, "llm_a") || containsNode(res.Path, "llm_b") {
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
		_, err := Compile(mk(func(c map[string]*Component) {
			c["llm"].Downstream = nil // ans and retr both terminal
		}), linearDeps(&eventLog{}))
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Errorf("err = %v, want terminal error", err)
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

func containsNode(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}
