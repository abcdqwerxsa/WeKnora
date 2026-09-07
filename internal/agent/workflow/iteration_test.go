package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// iterDeps answers prompts deterministically: the body LLM echoes
// "<prefix><item>": the test proves item exposure + outer refs.
func iterDeps() Deps {
	return Deps{
		LLMFunc: func(_ context.Context, req nodes.LLMRequest) (string, error) {
			return fmt.Sprintf("got:%s", req.Prompt), nil
		},
	}
}

// iterationSquareDSL: Start → Iteration(items=[1,2,3]) body: LLM(prompt =
// "{iter1@item} * {start@query}") → collect {llm_body@content}.
func iterationSquareDSL() *DSL {
	return &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"iter1"}},
		"iter1": {
			Obj: ComponentObj{ComponentName: "Iteration", Params: map[string]any{
				"items":      `[1, 2, 3]`,
				"item_var":   "n",
				"output_ref": "{llm_body@content}",
				"output_var": "squares",
			}},
			Downstream: []string{"ans"},
		},
		"ans": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{iter1@count} items"}}, Upstream: []string{"iter1"}},
		// Body (parent=iter1): single LLM entry/terminal.
		"llm_body": {
			Obj:    ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "item={iter1@n} q={start@query}"}},
			Parent: "iter1",
		},
	}}
}

func TestIterationLoopsBodyPerItem(t *testing.T) {
	log := &eventLog{}
	deps := iterDeps()
	deps.OnNodeEvent = log.record
	wf, err := Compile(iterationSquareDSL(), deps)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := wf.Run(context.Background(), "outer-q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	squares, ok := res.Outputs.Outputs["iter1"]["squares"].([]any)
	if !ok {
		t.Fatalf("iter1 outputs = %+v", res.Outputs.Outputs["iter1"])
	}
	if len(squares) != 3 {
		t.Fatalf("squares len = %d, want 3", len(squares))
	}
	for i, want := range []string{"got:item=1 q=outer-q", "got:item=2 q=outer-q", "got:item=3 q=outer-q"} {
		if squares[i] != want {
			t.Errorf("squares[%d] = %q, want %q", i, squares[i], want)
		}
	}
	// The body node's events flow with its own id (live progress per item).
	bodyRuns := 0
	for _, ev := range log.events {
		if ev.NodeID == "llm_body" && ev.Phase == PhaseFinished {
			bodyRuns++
		}
	}
	if bodyRuns != 3 {
		t.Errorf("body finished events = %d, want 3", bodyRuns)
	}
	// Outer path contains the outer nodes only (body path lives in children).
	if !slices.Contains(res.Path, "iter1") || slices.Contains(res.Path, "llm_body") {
		t.Errorf("Path = %v", res.Path)
	}
}

func TestIterationCollectsRawNonStringValues(t *testing.T) {
	// Body terminal is a Code-shaped node? Keep it engine-pure: use a
	// Template body returning numbers via raw outputs — Template only
	// returns strings, so drive the raw path with VariableAggregator-free
	// setup: output_ref to iter's own item (raw value round-trip).
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"iter1"}},
		"iter1": {
			Obj:        ComponentObj{ComponentName: "Iteration", Params: map[string]any{"items": `["a","b"]`, "output_ref": "{iter1@item}"}},
			Downstream: []string{"ans"},
		},
		"ans":  {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{iter1@count}"}}, Upstream: []string{"iter1"}},
		"noop": {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "x"}}, Parent: "iter1"},
	}}
	wf, err := Compile(dsl, iterDeps())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := wf.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := res.Outputs.Outputs["iter1"]["results"].([]any)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("results = %#v, want [a b] (raw values, not rendered strings)", got)
	}
}

func TestIterationNestedLoop(t *testing.T) {
	// Outer iteration over [1,2]; body contains an inner iteration over the
	// OUTER item exposed as {outer@n} — proves recursion + snapshot seeding.
	dsl := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"outer"}},
		"outer": {
			Obj:        ComponentObj{ComponentName: "Iteration", Params: map[string]any{"items": `[1,2]`, "item_var": "n", "output_ref": "{inner@count}"}},
			Downstream: []string{"ans"},
		},
		"ans": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "ok"}}, Upstream: []string{"outer"}},
		"inner": {
			Obj:    ComponentObj{ComponentName: "Iteration", Params: map[string]any{"items": "[{outer@n}, 9]", "output_ref": "{tpl@text}"}},
			Parent: "outer",
		},
		"tpl": {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "v={inner@item}"}}, Parent: "inner"},
	}}
	wf, err := Compile(dsl, iterDeps())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := wf.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	counts, _ := res.Outputs.Outputs["outer"]["results"].([]any)
	if len(counts) != 2 {
		t.Fatalf("outer results = %#v", counts)
	}
	// outer item 1 → inner [1,9] → 2 items; outer item 2 → inner [2,9] → 2.
	for i, c := range counts {
		if n, ok := c.(float64); !ok || n != 2 {
			if n, ok := c.(int); !ok || n != 2 {
				t.Errorf("counts[%d] = %#v, want 2", i, c)
			}
		}
	}
}

func TestIterationItemFailureFailsNode(t *testing.T) {
	deps := iterDeps()
	deps.LLMFunc = func(_ context.Context, req nodes.LLMRequest) (string, error) {
		if strings.Contains(req.Prompt, "item=2") {
			return "", fmt.Errorf("boom on 2")
		}
		return "ok", nil
	}
	wf, err := Compile(iterationSquareDSL(), deps)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := wf.Run(context.Background(), "q", nil); err == nil {
		t.Error("item failure must fail the iteration node")
	} else if !strings.Contains(err.Error(), "boom on 2") {
		t.Errorf("item failure cause must propagate: %v", err)
	}
}

func TestIterationParamValidation(t *testing.T) {
	build := func(iterParams map[string]any, withBody bool) *DSL {
		comps := map[string]*Component{
			"iter1": {Obj: ComponentObj{ComponentName: "Iteration", Params: iterParams}},
		}
		if withBody {
			comps["x"] = &Component{
				Obj:    ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "t"}},
				Parent: "iter1",
			}
		}
		return &DSL{Version: 1, Components: comps}
	}
	full := map[string]any{"items": "[1]", "output_ref": "{x@text}"}
	if _, err := Compile(build(full, true), Deps{}); err != nil {
		t.Fatalf("valid minimal iteration must compile: %v", err)
	}
	if _, err := Compile(build(full, false), Deps{}); err == nil {
		t.Error("Iteration without body must fail compilation")
	}
	noItems := map[string]any{"output_ref": "{x@text}"}
	if _, err := Compile(build(noItems, true), Deps{}); err == nil {
		t.Error("missing items must fail compilation")
	}
	noRef := map[string]any{"items": "[1]"}
	if _, err := Compile(build(noRef, true), Deps{}); err == nil {
		t.Error("missing output_ref must fail compilation")
	}
	// Parent must name an Iteration node.
	badParent := &DSL{Version: 1, Components: map[string]*Component{
		"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"a"}},
		"a":     {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "t"}}},
		"orphan": {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "t"}},
			Parent: "a"},
	}}
	if _, err := Compile(badParent, Deps{}); err == nil {
		t.Error("parent pointing at a non-Iteration node must fail")
	}
}

func TestIterationItemsMustBeJSONArray(t *testing.T) {
	wf, err := Compile(&DSL{Version: 1, Components: map[string]*Component{
		"iter1": {Obj: ComponentObj{ComponentName: "Iteration", Params: map[string]any{
			"items": `"\"just a string\""`, "output_ref": "{x@text}",
		}}},
		"x": {Obj: ComponentObj{ComponentName: "Template", Params: map[string]any{"template": "t"}}, Parent: "iter1"},
	}}, Deps{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := wf.Run(context.Background(), "q", nil); err == nil {
		t.Error("non-array items must fail at run time")
	}
}
