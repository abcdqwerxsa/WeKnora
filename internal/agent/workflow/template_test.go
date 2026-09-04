package workflow

import (
	"strings"
	"testing"
)

func TestExtractRefs(t *testing.T) {
	s := "Preamble {sys.query} then {llm@content} and again {llm@content}, env {env.model}, files {sys.files}, brace-tolerant {{retr@chunks}}"
	got := ExtractRefs(s)
	want := []string{"sys.query", "llm@content", "env.model", "sys.files", "retr@chunks"}
	if len(got) != len(want) {
		t.Fatalf("ExtractRefs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractRefs = %v, want %v", got, want)
		}
	}
}

func TestExtractRefsNone(t *testing.T) {
	if got := ExtractRefs("plain text {not-a-ref} {node without at-sign}"); got != nil {
		t.Fatalf("ExtractRefs = %v, want nil", got)
	}
}

func TestResolveTemplate(t *testing.T) {
	st := NewCanvasState(
		map[string]any{"query": "what is weknora", "files": []string{"a.pdf"}},
		map[string]any{"model": "deepseek"},
	)
	st.SetOutput("llm", "content", "WeKnora is a RAG framework")
	st.SetOutput("retr", "count", 3)

	got, err := ResolveTemplate("Q: {sys.query}\nA: {llm@content} (n={retr@count}, m={env.model})", st)
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	want := "Q: what is weknora\nA: WeKnora is a RAG framework (n=3, m=deepseek)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveTemplateMissingRef(t *testing.T) {
	st := NewCanvasState(nil, nil)
	_, err := ResolveTemplate("x {ghost@param} y", st)
	if err == nil {
		t.Fatal("missing node ref must error")
	}
	if !strings.Contains(err.Error(), "ghost@param") {
		t.Errorf("error must name the ref: %v", err)
	}
	_, err = ResolveTemplate("{sys.nope}", st)
	if err == nil || !strings.Contains(err.Error(), "sys.nope") {
		t.Errorf("missing sys ref must error with name: %v", err)
	}
	_, err = ResolveTemplate("{env.nope}", st)
	if err == nil || !strings.Contains(err.Error(), "env.nope") {
		t.Errorf("missing env ref must error with name: %v", err)
	}
}

func TestResolveTemplateEmptyAndNil(t *testing.T) {
	if s, err := ResolveTemplate("", NewCanvasState(nil, nil)); err != nil || s != "" {
		t.Errorf("empty template: %q %v", s, err)
	}
	if s, err := ResolveTemplate("no refs", nil); err != nil || s != "no refs" {
		t.Errorf("nil state with plain text: %q %v", s, err)
	}
}

func TestStateConcurrency(t *testing.T) {
	st := NewCanvasState(nil, nil)
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				st.SetOutput("node", "k", i)
				st.GetOutput("node", "k")
				st.SysValue("query")
				st.AppendPath("node")
				st.Snapshot()
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	if len(st.PathCopy()) != 800 {
		t.Errorf("path length = %d, want 800", len(st.PathCopy()))
	}
}

func TestStateSnapshotIsolation(t *testing.T) {
	st := NewCanvasState(map[string]any{"query": "q"}, nil)
	st.SetOutput("n", "p", "v1")
	snap := st.Snapshot()

	st.SetOutput("n", "p", "v2")
	st.Sys["query"] = "changed"

	if snap.Outputs["n"]["p"] != "v1" {
		t.Error("snapshot outputs mutated by later writes")
	}
	if snap.Sys["query"] != "q" {
		t.Error("snapshot sys mutated by later writes")
	}

	restored := NewCanvasState(nil, nil)
	restored.Restore(snap)
	if v, _ := restored.GetOutput("n", "p"); v != "v1" {
		t.Errorf("restore got %v, want v1", v)
	}
}
