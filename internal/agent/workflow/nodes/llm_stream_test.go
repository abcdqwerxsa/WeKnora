package nodes

import (
	"context"
	"strings"
	"testing"
)

func TestLLMNodeStreamsDeltasAndRecordsFullContent(t *testing.T) {
	var deltas []string
	n, err := New(ComponentLLM, map[string]any{"prompt": "{start@query}"}, Deps{
		LLMFunc: func(context.Context, LLMRequest) (string, error) {
			t.Fatal("non-streaming func must not run when the stream func is present")
			return "", nil
		},
		LLMStreamFunc: func(_ context.Context, _ LLMRequest, onDelta func(string)) (string, error) {
			for _, chunk := range []string{"Hello", " ", "stream", "ing"} {
				onDelta(chunk)
			}
			return "Hello streaming", nil
		},
		OnDelta: func(delta string) { deltas = append(deltas, delta) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "hi"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["content"] != "Hello streaming" {
		t.Errorf("content = %v — the stream func's return is the source of truth", out["content"])
	}
	if strings.Join(deltas, "") != "Hello streaming" {
		t.Errorf("deltas = %q", strings.Join(deltas, ""))
	}
}

func TestLLMNodeNilDeltaSinkStillStreams(t *testing.T) {
	n, err := New(ComponentLLM, map[string]any{"prompt": "p"}, Deps{
		LLMStreamFunc: func(_ context.Context, _ LLMRequest, onDelta func(string)) (string, error) {
			if onDelta != nil {
				onDelta("chunk") // nil sink must simply be skipped, not panic
			}
			return "done", nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{}))
	if err != nil || out["content"] != "done" {
		t.Errorf("out=%v err=%v", out, err)
	}
}
