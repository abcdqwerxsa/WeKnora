package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// TestLoadWorkflowTemplatesFromRepo loads the real config/workflow_templates
// directory and asserts the shipped templates are all valid.
func TestLoadWorkflowTemplatesFromRepo(t *testing.T) {
	if err := LoadWorkflowTemplates("../../../config"); err != nil {
		t.Fatalf("LoadWorkflowTemplates: %v", err)
	}
	list := ListWorkflowTemplates()
	if len(list) < 8 {
		t.Fatalf("want >= 8 shipped templates, got %d", len(list))
	}
	seen := map[string]bool{}
	for _, tpl := range list {
		if tpl.ID == "" {
			t.Fatal("template with empty id")
		}
		if seen[tpl.ID] {
			t.Errorf("duplicate template id %q", tpl.ID)
		}
		seen[tpl.ID] = true
		if tpl.Localized("default").Name == "" {
			t.Errorf("template %q: default locale has empty name", tpl.ID)
		}
		if _, err := Compile(tpl.DSL, Deps{}); err != nil {
			t.Errorf("template %q does not compile: %v", tpl.ID, err)
		}
		if tpl.NodeCount() != len(tpl.DSL.Components) {
			t.Errorf("template %q: nodeCount %d != components %d", tpl.ID, tpl.NodeCount(), len(tpl.DSL.Components))
		}
	}
}

// TestTemplateBindKBPlaceholders verifies instantiation binding: every
// placeholder is replaced, other params survive, and the template itself
// is untouched.
func TestTemplateBindKBPlaceholders(t *testing.T) {
	if err := LoadWorkflowTemplates("../../../config"); err != nil {
		t.Fatalf("LoadWorkflowTemplates: %v", err)
	}
	var tpl *WorkflowTemplate
	for _, t2 := range ListWorkflowTemplates() {
		if len(t2.KBPlaceholders) > 0 {
			tpl = t2
			break
		}
	}
	if tpl == nil {
		t.Fatal("no shipped template declares kb placeholders")
	}
	bindings := map[string]string{}
	for _, p := range tpl.KBPlaceholders {
		bindings[p] = "kb-" + strings.ToLower(strings.TrimPrefix(p, "KB_"))
	}
	bound, err := tpl.BindKBPlaceholders(bindings)
	if err != nil {
		t.Fatalf("BindKBPlaceholders: %v", err)
	}
	for id, comp := range bound.Components {
		for _, kbID := range KBIDsOf(comp.Obj.Params) {
			if kbPlaceholderPattern.MatchString(kbID) {
				t.Errorf("node %q still carries unbound placeholder %q", id, kbID)
			}
		}
	}
	// The shared template keeps its placeholders (defensive copy contract).
	for _, comp := range tpl.DSL.Components {
		for _, kbID := range KBIDsOf(comp.Obj.Params) {
			if !kbPlaceholderPattern.MatchString(kbID) {
				t.Errorf("template %q was mutated by BindKBPlaceholders", tpl.ID)
			}
		}
	}
	// Bound DSL still compiles.
	if _, err := Compile(bound, Deps{}); err != nil {
		t.Fatalf("bound template %q does not compile: %v", tpl.ID, err)
	}
}

// TestRenderSysDotPathRef pins the {sys.files.0} contract: dotted sys refs
// walk map keys and array indices exactly like node-output refs.
func TestRenderSysDotPathRef(t *testing.T) {
	st := &CanvasState{Sys: map[string]any{"query": "什么是工作流", "files": []string{"a.pdf", "b.pdf"}}}
	got, err := nodes.Render("第一个 {sys.files.1}，问题 {sys.query}", st)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "第一个 b.pdf，问题 什么是工作流"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	// {sys.files.2} is out of range: a clear error, not silent emptiness.
	if _, err := nodes.Render("{sys.files.2}", st); err == nil {
		t.Error("out-of-range sys.files index must error")
	}
}

// TestRunAllShippedTemplates is the guard compile-only checks cannot be: it
// executes every shipped template end-to-end with stub deps, so reference
// contract violations (wrong output key, unsupported sys path, aggregator
// shape) fail here instead of at a user's first run.
func TestRunAllShippedTemplates(t *testing.T) {
	if err := LoadWorkflowTemplates("../../../config"); err != nil {
		t.Fatalf("LoadWorkflowTemplates: %v", err)
	}
	for _, tpl := range ListWorkflowTemplates() {
		t.Run(tpl.ID, func(t *testing.T) {
			// The generic LLM reply satisfies the doc-generation gate; extractor
			// and iteration templates override it below.
			llmReply := `{"passed": true, "checks": []}`
			for _, comp := range tpl.DSL.Components {
				if comp.Obj.ComponentName == "ParameterExtractor" {
					// Required params missing from the model reply are a node
					// error, so name every declared parameter.
					obj := map[string]string{}
					if decls, ok := comp.Obj.Params["parameters"].([]any); ok {
						for _, d := range decls {
							if dm, ok := d.(map[string]any); ok {
								if name, _ := dm["name"].(string); name != "" {
									obj[name] = "ok"
								}
							}
						}
					}
					if len(obj) > 0 {
						if raw, err := json.Marshal(obj); err == nil {
							llmReply = string(raw)
						}
					}
					break
				}
			}
			for _, comp := range tpl.DSL.Components {
				if comp.Obj.ComponentName == "Iteration" && llmReply[0] != '[' {
					// Iteration items must render to a JSON array.
					llmReply = `["第一项", "第二项"]`
					break
				}
			}
			deps := Deps{
				LLMFunc: func(_ context.Context, _ nodes.LLMRequest) (string, error) { return llmReply, nil },
				AgentFunc: func(_ context.Context, _ nodes.AgentRequest) (string, error) {
					return `{"passed": true, "checks": []}`, nil
				},
				RetrievalFunc: func(_ context.Context, req nodes.RetrievalRequest) (*nodes.RetrievalResult, error) {
					return &nodes.RetrievalResult{
						Chunks:  []map[string]any{{"content": "chunk for " + req.Query}},
						DocAggs: []map[string]any{{"doc_name": "doc1"}},
					}, nil
				},
				WebSearchFunc: func(_ context.Context, _ nodes.WebSearchRequest) ([]nodes.WebSearchResultItem, error) {
					return []nodes.WebSearchResultItem{{}}, nil
				},
			}
			wf, err := Compile(tpl.DSL, deps)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			res, err := wf.Run(context.Background(), "smoke test query", []string{"file-a.pdf", "file-b.pdf"})
			if err != nil {
				t.Fatalf("Run: %v (reference contract violation?)", err)
			}
			if res.Answer == "" {
				t.Fatal("Run completed with empty answer")
			}
		})
	}
}

// TestLoadWorkflowTemplatesMissingDir asserts a config dir without the
// templates subdirectory loads an empty registry without error.
func TestLoadWorkflowTemplatesMissingDir(t *testing.T) {
	if err := LoadWorkflowTemplates(t.TempDir()); err != nil {
		t.Fatalf("missing dir must not error: %v", err)
	}
	if got := len(ListWorkflowTemplates()); got != 0 {
		t.Fatalf("want 0 templates from empty dir, got %d", got)
	}
}
