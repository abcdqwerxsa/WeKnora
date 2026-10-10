package nodes

import (
	"context"
	"testing"
)

func TestAgentNodeRendersAndRecordsAnswer(t *testing.T) {
	var got AgentRequest
	n, err := New(ComponentAgent, map[string]any{
		"prompt":        "research {start@query}",
		"system_prompt": "be terse",
		"model":         "m-1",
		"kb_ids":        []any{"kb-a", "kb-b"},
		"temperature":   0.3,
		"agent_id":      "ag-9",
	}, Deps{AgentFunc: func(_ context.Context, req AgentRequest) (string, error) {
		got = req
		return "agent says hi", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "topic"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["answer"] != "agent says hi" {
		t.Errorf("out = %v", out)
	}
	if got.Prompt != "research topic" || got.SystemPrompt != "be terse" || got.Model != "m-1" {
		t.Errorf("req = %+v", got)
	}
	if len(got.KBIDs) != 2 || got.KBIDs[0] != "kb-a" {
		t.Errorf("kbIDs = %v", got.KBIDs)
	}
	if got.Temperature != 0.3 {
		t.Errorf("temperature = %v", got.Temperature)
	}
	if got.AgentID != "ag-9" {
		t.Errorf("agentID = %q, want ag-9", got.AgentID)
	}
}

func TestAgentNodeValidation(t *testing.T) {
	if _, err := New(ComponentAgent, map[string]any{}, Deps{}); err == nil {
		t.Error("missing prompt must fail compilation")
	}
	n, err := New(ComponentAgent, map[string]any{"prompt": "p"}, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := n.Invoke(context.Background(), withState(nil, &fakeState{})); err == nil {
		t.Error("missing AgentFunc must fail at invoke")
	}
}

func TestMCPToolNodeCallsWithRenderedArgs(t *testing.T) {
	var got MCPToolRequest
	n, err := New(ComponentMCPTool, map[string]any{
		"service_id":      "svc-1",
		"tool":            "search_docs",
		"args":            `{"q": "{start@query}", "limit": 5}`,
		"timeout_seconds": 15,
	}, Deps{MCPFunc: func(_ context.Context, req MCPToolRequest) (any, string, error) {
		got = req
		return []any{"a"}, "a", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "hello"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got.ServiceID != "svc-1" || got.Tool != "search_docs" || got.TimeoutSeconds != 15 {
		t.Errorf("req = %+v", got)
	}
	if got.ArgsJSON != `{"q": "hello", "limit": 5}` {
		t.Errorf("args = %q", got.ArgsJSON)
	}
	if out["result_text"] != "a" {
		t.Errorf("out = %v", out)
	}
}

func TestMCPToolNodeDefaultsAndValidation(t *testing.T) {
	// No args template → "{}"; missing service_id/tool fail at build.
	var got MCPToolRequest
	n, err := New(ComponentMCPTool, map[string]any{
		"service_id": "s", "tool": "t",
	}, Deps{MCPFunc: func(_ context.Context, req MCPToolRequest) (any, string, error) {
		got = req
		return nil, "", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := n.Invoke(context.Background(), withState(nil, &fakeState{})); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got.ArgsJSON != "{}" || got.TimeoutSeconds != 30 {
		t.Errorf("defaults: %+v", got)
	}
	if _, err := New(ComponentMCPTool, map[string]any{"tool": "t"}, Deps{}); err == nil {
		t.Error("missing service_id must fail")
	}
	if _, err := New(ComponentMCPTool, map[string]any{"service_id": "s"}, Deps{}); err == nil {
		t.Error("missing tool must fail")
	}
	n2, _ := New(ComponentMCPTool, map[string]any{"service_id": "s", "tool": "t"}, Deps{})
	if _, err := n2.Invoke(context.Background(), withState(nil, &fakeState{})); err == nil {
		t.Error("missing MCPFunc must fail at invoke")
	}
}
