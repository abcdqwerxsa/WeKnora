package nodes

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCodeNodeRendersVariablesAndReturnsOutputs(t *testing.T) {
	var got CodeRequest
	n, err := New(ComponentCode, map[string]any{
		"language": "python3",
		"code":     "print(json.dumps({'answer': 42}))",
		"variables": []any{
			map[string]any{"name": "q", "ref": "{start@query}"},
			map[string]any{"name": "lang", "ref": "{sys.query}"},
		},
	}, Deps{CodeFunc: func(_ context.Context, req CodeRequest) (map[string]any, error) {
		got = req
		return map[string]any{"answer": float64(42)}, nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		sys:     map[string]any{"query": "hello"},
		outputs: map[string]map[string]any{"start": {"query": "hello"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got.Language != "python3" || got.Code == "" {
		t.Errorf("req = %+v", got)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(got.InputJSON), &input); err != nil {
		t.Fatalf("InputJSON not valid: %v", err)
	}
	if input["q"] != "hello" || input["lang"] != "hello" {
		t.Errorf("input = %v", input)
	}
	if out["answer"] != float64(42) {
		t.Errorf("out = %v", out)
	}
}

func TestCodeNodeDefaultsAndValidation(t *testing.T) {
	// language defaults to python3; node rejects unknown languages and
	// empty names; missing CodeFunc fails at Invoke with a clear message.
	n, err := New(ComponentCode, map[string]any{"code": "x"}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := n.Invoke(context.Background(), withState(nil, &fakeState{})); err == nil {
		t.Error("missing CodeFunc must fail")
	}
	if _, err := New(ComponentCode, map[string]any{"code": "x", "language": "ruby"}, Deps{}); err == nil {
		t.Error("unknown language must fail compilation")
	}
	if _, err := New(ComponentCode, map[string]any{
		"code":      "x",
		"variables": []any{map[string]any{"name": "", "ref": "{sys.query}"}},
	}, Deps{}); err == nil {
		t.Error("empty variable name must fail compilation")
	}
	if _, err := New(ComponentCode, map[string]any{}, Deps{}); err == nil {
		t.Error("missing code must fail compilation")
	}
}
