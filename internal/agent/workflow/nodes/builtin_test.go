package nodes

import (
	"context"
	"strings"
	"testing"
)

type fakeState struct {
	outputs map[string]map[string]any
	sys     map[string]any
	env     map[string]any
}

func (f *fakeState) GetOutput(nodeID, param string) (any, bool) {
	if m, ok := f.outputs[nodeID]; ok {
		v, ok := m[param]
		return v, ok
	}
	return nil, false
}
func (f *fakeState) SysValue(key string) (any, bool) { v, ok := f.sys[key]; return v, ok }
func (f *fakeState) EnvValue(key string) (any, bool) { v, ok := f.env[key]; return v, ok }

func withState(in map[string]any, st StateView) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[StateInputKey] = st
	return out
}

func TestStartNodeEchoesRequest(t *testing.T) {
	n, err := New("start", nil, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{"query": "q1", "files": []string{"f"}})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["query"] != "q1" {
		t.Errorf("query = %v", out["query"])
	}
	if _, ok := out["files"].([]string); !ok {
		t.Errorf("files type = %T", out["files"])
	}
}

func TestAnswerRendersTemplate(t *testing.T) {
	st := &fakeState{sys: map[string]any{"query": "hello"}}
	n, err := New("Answer", map[string]any{"template": "Q={sys.query}"}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, st))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["answer"] != "Q=hello" {
		t.Errorf("answer = %v", out["answer"])
	}
}

func TestAnswerRequiresTemplate(t *testing.T) {
	if _, err := New("Answer", nil, Deps{}); err == nil {
		t.Error("Answer without template must fail construction")
	}
}

func TestLLMNodeRendersAndCalls(t *testing.T) {
	st := &fakeState{sys: map[string]any{"query": "q"}, outputs: map[string]map[string]any{
		"retr": {"chunks": "c1"},
	}}
	called := false
	n, err := New("LLM", map[string]any{
		"prompt":      "ctx={retr@chunks} q={sys.query}",
		"model":       "m1",
		"temperature": 0.5,
	}, Deps{LLMFunc: func(_ context.Context, req LLMRequest) (string, error) {
		called = true
		if req.Prompt != "ctx=c1 q=q" {
			t.Errorf("prompt = %q", req.Prompt)
		}
		if req.Model != "m1" || req.Temperature != 0.5 {
			t.Errorf("req = %+v", req)
		}
		return "gen", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, st))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !called || out["content"] != "gen" {
		t.Errorf("out = %v called=%v", out, called)
	}
}

func TestLLMNodeWithoutFuncErrors(t *testing.T) {
	n, err := New("LLM", map[string]any{"prompt": "x"}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = n.Invoke(context.Background(), withState(nil, &fakeState{}))
	if err == nil || !strings.Contains(err.Error(), "LLMFunc") {
		t.Errorf("err = %v, want LLMFunc-missing error", err)
	}
}

func TestRetrievalNode(t *testing.T) {
	st := &fakeState{sys: map[string]any{"query": "find this"}}
	n, err := New("Retrieval", map[string]any{
		"query":  "{sys.query}",
		"kb_ids": []any{"kb1", "kb2"},
		"top_k":  float64(5),
	}, Deps{RetrievalFunc: func(_ context.Context, req RetrievalRequest) (*RetrievalResult, error) {
		if req.Query != "find this" {
			t.Errorf("query = %q", req.Query)
		}
		if len(req.KBIDs) != 2 || req.TopK != 5 {
			t.Errorf("req = %+v", req)
		}
		return &RetrievalResult{Chunks: []map[string]any{{"id": "c1"}}}, nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, st))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(out["chunks"].([]map[string]any)) != 1 {
		t.Errorf("chunks = %#v", out["chunks"])
	}
	if aggs, ok := out["doc_aggs"].([]map[string]any); !ok || len(aggs) != 0 {
		t.Errorf("doc_aggs = %#v (must default to empty list)", out["doc_aggs"])
	}
}

func TestRetrievalWithoutFuncErrors(t *testing.T) {
	n, err := New("Retrieval", map[string]any{"query": "{sys.query}"}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = n.Invoke(context.Background(), withState(nil, &fakeState{}))
	if err == nil || !strings.Contains(err.Error(), "RetrievalFunc") {
		t.Errorf("err = %v", err)
	}
}

func TestSwitchRouting(t *testing.T) {
	params := map[string]any{
		"value": "{sys.lang}",
		"cases": []any{
			map[string]any{"value": "go", "to": "node_a"},
			map[string]any{"value": "py", "to": "node_b"},
		},
		"default": "node_b",
	}
	n, err := New("Switch", params, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, tc := range []struct{ lang, want string }{
		{"go", "node_a"},
		{"py", "node_b"},
		{"other", "node_b"}, // default
	} {
		st := &fakeState{sys: map[string]any{"lang": tc.lang}}
		out, err := n.Invoke(context.Background(), withState(nil, st))
		if err != nil {
			t.Fatalf("Invoke(%s): %v", tc.lang, err)
		}
		if out[RouteOutputKey] != tc.want {
			t.Errorf("lang=%s route=%v, want %s", tc.lang, out[RouteOutputKey], tc.want)
		}
	}
}

func TestSwitchNoMatchNoDefault(t *testing.T) {
	n, err := New("Switch", map[string]any{
		"value": "{sys.lang}",
		"cases": []any{map[string]any{"value": "go", "to": "node_a"}},
	}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	st := &fakeState{sys: map[string]any{"lang": "rust"}}
	if _, err := n.Invoke(context.Background(), withState(nil, st)); err == nil {
		t.Error("no match + no default must error")
	}
}

func TestRouteTargets(t *testing.T) {
	params := map[string]any{
		"value":   "x",
		"cases":   []any{map[string]any{"value": "x", "to": "a"}, map[string]any{"value": "y", "to": "b"}},
		"default": "c",
	}
	targets, err := RouteTargets("Switch", params)
	if err != nil {
		t.Fatalf("RouteTargets: %v", err)
	}
	if strings.Join(targets, ",") != "a,b,c" {
		t.Errorf("targets = %v, want [a b c]", targets)
	}
	// non-routing component
	if targets, err := RouteTargets("LLM", map[string]any{}); err != nil || targets != nil {
		t.Errorf("LLM targets = %v err=%v, want nil/nil", targets, err)
	}
}

func TestRegistryUnknownAndCaseInsensitive(t *testing.T) {
	if _, err := New("Nope", nil, Deps{}); err == nil {
		t.Error("unknown component must error")
	}
	if err := IsKnown("llm"); !err {
		t.Error("lookup must be case-insensitive")
	}
	if !strings.Contains(strings.Join(Known(), ","), "llm") {
		t.Errorf("Known() should list canonical lowercase names: %v", Known())
	}
}

func TestRenderNilStatePassthrough(t *testing.T) {
	if s, err := Render("plain", nil); err != nil || s != "plain" {
		t.Errorf("Render(nil state) = %q, %v", s, err)
	}
}
