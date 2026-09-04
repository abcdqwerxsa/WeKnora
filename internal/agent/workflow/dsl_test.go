package workflow

import (
	"encoding/json"
	"reflect"
	"testing"
)

func linearDSL() *DSL {
	return &DSL{
		Version: 1,
		Components: map[string]*Component{
			"start": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"retr"}},
			"retr": {
				Obj:        ComponentObj{ComponentName: "Retrieval", Params: map[string]any{"query": "{sys.query}", "kb_ids": []any{"kb1"}}},
				Upstream:   []string{"start"},
				Downstream: []string{"llm"},
			},
			"llm": {
				Obj:        ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "Q: {retr@chunks}"}},
				Upstream:   []string{"retr"},
				Downstream: []string{"ans"},
			},
			"ans": {
				Obj:      ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "{llm@content}"}},
				Upstream: []string{"llm"},
			},
		},
	}
}

func TestNormalizeComponentsToGraph(t *testing.T) {
	out, err := Normalize(linearDSL())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.Graph == nil {
		t.Fatal("expected default layout graph to be generated")
	}
	if len(out.Graph.Nodes) != 4 || len(out.Graph.Edges) != 3 {
		t.Fatalf("graph = %d nodes / %d edges, want 4/3", len(out.Graph.Nodes), len(out.Graph.Edges))
	}
	// entry node must be leftmost (layer 0)
	byID := map[string]GraphNode{}
	for _, n := range out.Graph.Nodes {
		byID[n.ID] = n
	}
	if byID["start"].Position.X != 0 {
		t.Errorf("start x = %v, want 0 (entry layer)", byID["start"].Position.X)
	}
	if byID["ans"].Position.X != 600 {
		t.Errorf("answer x = %v, want 600 (3 layers * 200)", byID["ans"].Position.X)
	}
	// params must round-trip into node.Data
	params, _ := byID["llm"].Data["params"].(map[string]any)
	if params["prompt"] != "Q: {retr@chunks}" {
		t.Errorf("llm params did not round-trip: %#v", params)
	}
}

func TestNormalizeGraphToComponents(t *testing.T) {
	raw := `{"version":1,"graph":{"nodes":[
		{"id":"s","type":"Start","position":{"x":0,"y":0}},
		{"id":"a","type":"Answer","position":{"x":200,"y":0},"data":{"params":{"template":"hi {sys.query}"}}}
	],"edges":[{"id":"e1","source":"s","target":"a"}]}}`
	var dsl DSL
	if err := json.Unmarshal([]byte(raw), &dsl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Normalize(&dsl)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	c := out.Components["a"]
	if c == nil || c.Obj.ComponentName != "Answer" {
		t.Fatalf("component a not derived: %+v", c)
	}
	if c.Obj.Params["template"] != "hi {sys.query}" {
		t.Errorf("params from node.Data not picked up: %#v", c.Obj.Params)
	}
	if !reflect.DeepEqual(c.Upstream, []string{"s"}) {
		t.Errorf("a.Upstream = %v, want [s]", c.Upstream)
	}
	if !reflect.DeepEqual(out.Components["s"].Downstream, []string{"a"}) {
		t.Errorf("s.Downstream = %v, want [a]", out.Components["s"].Downstream)
	}
}

func TestNormalizeDefensiveCopy(t *testing.T) {
	in := linearDSL()
	out, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	// mutate the INPUT after normalize — output must not change
	in.Components["llm"].Obj.Params["prompt"] = "tampered"
	in.Components["start"].Downstream = nil
	if out.Components["llm"].Obj.Params["prompt"] != "Q: {retr@chunks}" {
		t.Error("output shares params map with input")
	}
	if len(out.Components["start"].Downstream) != 1 {
		t.Error("output shares downstream slice with input")
	}
}

func TestNormalizeErrors(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if _, err := Normalize(nil); err == nil {
			t.Error("nil DSL must error")
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, err := Normalize(&DSL{}); err == nil {
			t.Error("empty DSL must error")
		}
	})
	t.Run("unknown component", func(t *testing.T) {
		d := &DSL{Components: map[string]*Component{
			"x": {Obj: ComponentObj{ComponentName: "Nonsense"}},
		}}
		_, err := Normalize(d)
		if err == nil {
			t.Fatal("unknown component must error")
		}
	})
	t.Run("no entry", func(t *testing.T) {
		d := &DSL{Components: map[string]*Component{
			"a": {Obj: ComponentObj{ComponentName: "Start"}, Upstream: []string{"b"}},
			"b": {Obj: ComponentObj{ComponentName: "Answer"}, Upstream: []string{"a"}},
		}}
		if _, err := Normalize(d); err == nil {
			t.Error("cycle with no entry must error")
		}
	})
}

func TestNormalizeSkipsMalformedEdges(t *testing.T) {
	raw := `{"version":1,"graph":{"nodes":[
		{"id":"s","type":"Start","position":{"x":0,"y":0}},
		{"id":"a","type":"Answer","position":{"x":200,"y":0},"data":{"params":{"template":"x"}}}
	],"edges":[
		{"id":"e1","source":"ghost","target":"a"},
		{"id":"e2","source":"s","target":"s"},
		{"id":"e3","source":"s","target":"a"}
	]}}`
	var dsl DSL
	if err := json.Unmarshal([]byte(raw), &dsl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Normalize(&dsl)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got := out.Components["s"].Downstream; !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("s.Downstream = %v, want [a] (malformed edges skipped)", got)
	}
}

func TestNormalizeVersionDefault(t *testing.T) {
	out, err := Normalize(&DSL{Components: map[string]*Component{
		"s": {Obj: ComponentObj{ComponentName: "Start"}, Downstream: []string{"a"}},
		"a": {Obj: ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "x"}}, Upstream: []string{"s"}},
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.Version != DSLVersion {
		t.Errorf("version = %d, want %d", out.Version, DSLVersion)
	}
}
