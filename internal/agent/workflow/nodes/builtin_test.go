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

func TestLLMNodePassesSystemPromptAndMaxTokens(t *testing.T) {
	var got LLMRequest
	n, err := New("LLM", map[string]any{
		"prompt":        "{s@q}",
		"system_prompt": "You are terse.",
		"model":         "m1",
		"temperature":   0.2,
		"max_tokens":    512,
	}, Deps{LLMFunc: func(_ context.Context, req LLMRequest) (string, error) {
		got = req
		return "ok", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		StateInputKey: &fakeState{outputs: map[string]map[string]any{"s": {"q": "hi"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "You are terse." || got.MaxTokens != 512 || got.Temperature != 0.2 || got.Model != "m1" {
		t.Errorf("request = %+v, want system/max_tokens/temp/model wired", got)
	}
	if out["content"] != "ok" {
		t.Errorf("content = %v", out["content"])
	}
}

func TestRetrievalNodePassesThresholdsAndRerank(t *testing.T) {
	var got RetrievalRequest
	n, err := New("Retrieval", map[string]any{
		"query":                "q",
		"kb_ids":               []any{"kb1"},
		"similarity_threshold": 0.55,
		"keyword_threshold":    0.2,
		"use_rerank":           true,
		"rerank_model_id":      "rr",
	}, Deps{RetrievalFunc: func(_ context.Context, req RetrievalRequest) (*RetrievalResult, error) {
		got = req
		return &RetrievalResult{Chunks: []map[string]any{{"content": "x"}}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Invoke(context.Background(), map[string]any{StateInputKey: &fakeState{}}); err != nil {
		t.Fatal(err)
	}
	if got.VectorThreshold != 0.55 || got.KeywordThreshold != 0.2 || !got.UseRerank || got.RerankModelID != "rr" {
		t.Errorf("request = %+v, want thresholds+rerank wired", got)
	}
}

func TestRetrievalNodeVectorThresholdWinsOverSimilarity(t *testing.T) {
	n, err := New("Retrieval", map[string]any{
		"query": "q", "kb_ids": []any{"kb1"},
		"similarity_threshold": 0.1, "vector_threshold": 0.7,
	}, Deps{RetrievalFunc: func(_ context.Context, req RetrievalRequest) (*RetrievalResult, error) {
		if req.VectorThreshold != 0.7 {
			t.Errorf("vector threshold = %v, want explicit 0.7", req.VectorThreshold)
		}
		return &RetrievalResult{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Invoke(context.Background(), map[string]any{StateInputKey: &fakeState{}}); err != nil {
		t.Fatal(err)
	}
}

func TestRetrievalNodeRerankWithoutModelIsConfigError(t *testing.T) {
	_, err := New("Retrieval", map[string]any{
		"query": "q", "kb_ids": []any{"kb1"}, "use_rerank": true,
	}, Deps{})
	if err == nil || !strings.Contains(err.Error(), "rerank_model_id") {
		t.Errorf("err = %v, want rerank_model_id required", err)
	}
}

// ---- Switch condition operators (Phase 2) --------------------------------

func condCase(ref, op, value, to string) map[string]any {
	return map[string]any{
		"conditions": []any{map[string]any{"ref": ref, "op": op, "value": value}},
		"to":         to,
	}
}

func TestSwitchConditionOperators(t *testing.T) {
	n, err := New("Switch", map[string]any{
		"cases": []any{
			condCase("{sys.text}", "contains", "err", "has_err"),
			condCase("{sys.text}", "starts_with", "OK", "ok_prefix"),
			condCase("{sys.score}", "gte", "80", "high"),
		},
		"default": "fallback",
	}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name string
		sys  map[string]any
		want string
	}{
		{"contains matches substring", map[string]any{"text": "server errored"}, "has_err"},
		{"starts_with matches prefix", map[string]any{"text": "OK: done"}, "ok_prefix"},
		{"numeric gte passes", map[string]any{"score": "93.5"}, "high"},
		{"numeric gte fails → default", map[string]any{"score": "12"}, "fallback"},
		{"unrelated sys → default", map[string]any{"text": "hello"}, "fallback"},
	}
	for _, tc := range cases {
		out, err := n.Invoke(context.Background(), withState(nil, &fakeState{sys: tc.sys}))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if out[RouteOutputKey] != tc.want {
			t.Errorf("%s: route=%v want %s", tc.name, out[RouteOutputKey], tc.want)
		}
	}
}

func TestSwitchConditionOperatorsEdgeSet(t *testing.T) {
	// Ordered alternatives: each input text must satisfy exactly its own
	// case (earlier cases arranged not to swallow later ones).
	n, err := New("Switch", map[string]any{
		"cases": []any{
			condCase("{sys.text}", "empty", "", "is_empty"),
			condCase("{sys.text}", "regex", `^\d+$`, "numeric"),
			condCase("{sys.text}", "in", "a, b ,c", "in_set"),
			condCase("{sys.text}", "ends_with", "!", "bang"),
			condCase("{sys.text}", "not_empty", "", "has_text"),
		},
		"default": "fallback",
	}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		text string
		want string
	}{
		{"", "is_empty"},
		{"12345", "numeric"},
		{"b", "in_set"},
		{"wow!", "bang"},
		{"hello", "has_text"},
	}

	// Negative operators (ne / not_in / not_contains) are catch-alls in an
	// ordered list — each gets a dedicated single-case switch.
	for _, tc := range []struct {
		op, value, text string
		want            string
	}{
		{"ne", "zzz", "hello", "hit"},
		{"ne", "zzz", "zzz", "fallback"},
		{"not_in", "a,b", "q", "hit"},
		{"not_in", "a,b", "b", "fallback"},
		{"not_contains", "x", "no letter here", "hit"},
		{"not_contains", "x", "x-ray", "fallback"},
	} {
		nn, nerr := New("Switch", map[string]any{
			"cases":   []any{condCase("{sys.text}", tc.op, tc.value, "hit")},
			"default": "fallback",
		}, Deps{})
		if nerr != nil {
			t.Fatalf("New(%s): %v", tc.op, nerr)
		}
		out, ierr := nn.Invoke(context.Background(), withState(nil, &fakeState{sys: map[string]any{"text": tc.text}}))
		if ierr != nil {
			t.Fatalf("%s text=%q: %v", tc.op, tc.text, ierr)
		}
		if out[RouteOutputKey] != tc.want {
			t.Errorf("%s text=%q route=%v want %s", tc.op, tc.text, out[RouteOutputKey], tc.want)
		}
	}
	for _, tc := range cases {
		st := &fakeState{sys: map[string]any{"text": tc.text}}
		out, err := n.Invoke(context.Background(), withState(nil, st))
		if err != nil {
			t.Fatalf("text=%q: %v", tc.text, err)
		}
		if out[RouteOutputKey] != tc.want {
			t.Errorf("text=%q route=%v want %s", tc.text, out[RouteOutputKey], tc.want)
		}
	}
}

func TestSwitchLogicOrAndNumericErrors(t *testing.T) {
	// OR group: second condition holds.
	n, err := New("Switch", map[string]any{
		"cases": []any{map[string]any{
			"conditions": []any{
				map[string]any{"ref": "{sys.a}", "op": "eq", "value": "1"},
				map[string]any{"ref": "{sys.b}", "op": "eq", "value": "2"},
			},
			"logic": "or",
			"to":    "either",
		}},
		"default": "none",
	}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{sys: map[string]any{"a": "9", "b": "2"}}))
	if err != nil || out[RouteOutputKey] != "either" {
		t.Errorf("or-group: out=%v err=%v, want either", out, err)
	}

	// Numeric operator with non-numeric operand fails the node (loud).
	n2, err := New("Switch", map[string]any{
		"cases": []any{condCase("{sys.a}", "gt", "10", "big")},
	}, Deps{})
	if err != nil {
		t.Fatalf("New2: %v", err)
	}
	if _, err := n2.Invoke(context.Background(), withState(nil, &fakeState{sys: map[string]any{"a": "not-a-number"}})); err == nil {
		t.Error("numeric op on non-numeric operand must error, not silently route")
	}
}

func TestSwitchUnknownOperatorRejectedAtBuild(t *testing.T) {
	if _, err := New("Switch", map[string]any{
		"cases": []any{condCase("{sys.x}", "wat", "1", "a")},
	}, Deps{}); err == nil {
		t.Error("unknown operator must fail compilation")
	}
	if _, err := New("Switch", map[string]any{
		"cases": []any{condCase("{sys.x}", "regex", "([unclosed", "a")},
	}, Deps{}); err == nil {
		t.Error("invalid regex must fail compilation")
	}
}

func TestStartNodeMaterializesFormInputs(t *testing.T) {
	n, err := New("Start", map[string]any{
		"fields": []any{
			map[string]any{"name": "city", "type": "text", "required": true},
			map[string]any{"name": "days", "type": "number", "default": "3"},
			map[string]any{"name": "mode", "type": "select", "options": []any{"fast", "slow"}, "default": "fast"},
		},
	}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		"query":  "hello",
		"inputs": map[string]any{"city": "Shenzhen"},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["city"] != "Shenzhen" {
		t.Errorf("city = %v, want Shenzhen", out["city"])
	}
	if out["days"] != "3" {
		t.Errorf("days default = %v, want 3", out["days"])
	}
	if out["mode"] != "fast" {
		t.Errorf("mode default = %v, want fast", out["mode"])
	}
	if out["query"] != "hello" {
		t.Errorf("query = %v, want hello", out["query"])
	}
}

func TestStartFieldsBadTypeRejected(t *testing.T) {
	if _, err := New("Start", map[string]any{
		"fields": []any{map[string]any{"name": "x", "type": "date"}},
	}, Deps{}); err == nil {
		t.Error("unknown field type must fail compilation")
	}
}

// TestLLMNodeThinkingTriState: absent = nil (provider default), explicit
// false/true pass through; string forms from DSL exports coerce too.
func TestLLMNodeThinkingTriState(t *testing.T) {
	mk := func(params map[string]any) *LLMRequest {
		t.Helper()
		var captured *LLMRequest
		n, err := New(ComponentLLM, params, Deps{LLMFunc: func(_ context.Context, r LLMRequest) (string, error) {
			captured = &r
			return "ok", nil
		}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := n.Invoke(context.Background(), withState(nil, &fakeState{})); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		return captured
	}
	if r := mk(map[string]any{"prompt": "p"}); r.Thinking != nil {
		t.Fatalf("absent thinking must stay nil, got %v", *r.Thinking)
	}
	if r := mk(map[string]any{"prompt": "p", "thinking": false}); r.Thinking == nil || *r.Thinking {
		t.Fatalf("explicit false must disable thinking")
	}
	if r := mk(map[string]any{"prompt": "p", "thinking": "true"}); r.Thinking == nil || !*r.Thinking {
		t.Fatalf("string \"true\" must enable thinking")
	}
}
