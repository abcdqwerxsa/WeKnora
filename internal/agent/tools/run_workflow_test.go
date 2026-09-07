package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeWorkflowRunner struct {
	workflows []*types.Workflow
	runFn     func(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error)
	lastReq   *types.RunWorkflowRequest
	lastID    string
}

func (r *fakeWorkflowRunner) ListWorkflows(context.Context, int, int) ([]*types.Workflow, int64, error) {
	return r.workflows, int64(len(r.workflows)), nil
}

func (r *fakeWorkflowRunner) RunWorkflow(ctx context.Context, id string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error) {
	r.lastID, r.lastReq = id, req
	return r.runFn(ctx, id, req)
}

func published(id, name string) *types.Workflow {
	return &types.Workflow{ID: id, Name: name, Status: types.WorkflowStatusPublished}
}

func TestListWorkflowsToolOnlyShowsPublished(t *testing.T) {
	runner := &fakeWorkflowRunner{workflows: []*types.Workflow{
		published("wf-a", "Intranet Lookup"),
		{ID: "wf-draft", Name: "WIP", Status: types.WorkflowStatusDraft},
	}}
	out, err := NewListWorkflowsTool(runner).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out.Success || out.Output == "" {
		t.Fatalf("out = %+v", out)
	}
	if !strings.Contains(out.Output, "wf-a") || strings.Contains(out.Output, "wf-draft") {
		t.Errorf("draft leaked into catalog: %q", out.Output)
	}

	empty := &fakeWorkflowRunner{}
	out, err = NewListWorkflowsTool(empty).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !out.Success {
		t.Fatalf("empty workspace: out=%+v err=%v", out, err)
	}
	if !strings.Contains(out.Output, "No published workflows") {
		t.Errorf("empty output = %q", out.Output)
	}
}

func TestRunWorkflowToolResolvesIdThenPublishedName(t *testing.T) {
	runner := &fakeWorkflowRunner{workflows: []*types.Workflow{published("wf-1", "Daily Report")}}
	runner.runFn = func(_ context.Context, _ string, req *types.RunWorkflowRequest) (*types.WorkflowRun, error) {
		return &types.WorkflowRun{ID: "run-9", Status: types.WorkflowRunStatusSucceeded,
			Output: types.JSON(`{"answer":"report attached"}`)}, nil
	}
	tool := NewRunWorkflowTool(runner)

	// By exact name.
	out, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "Daily Report", "query": "today"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out.Success || out.Output != "report attached" {
		t.Fatalf("out = %+v", out)
	}
	if runner.lastID != "wf-1" || runner.lastReq.Query != "today" {
		t.Errorf("resolved id=%q req=%+v", runner.lastID, runner.lastReq)
	}

	// Inputs pass through.
	if _, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "wf-1", "query": "q", "inputs": map[string]any{"city": "SZ"}})); err != nil {
		t.Fatalf("Execute inputs: %v", err)
	}
	if runner.lastReq.Inputs["city"] != "SZ" {
		t.Errorf("inputs = %v", runner.lastReq.Inputs)
	}
}

func TestRunWorkflowToolRejectsUnknownAndDraft(t *testing.T) {
	runner := &fakeWorkflowRunner{workflows: []*types.Workflow{
		published("wf-1", "Live"),
		{ID: "wf-2", Name: "Hidden", Status: types.WorkflowStatusDraft},
	}}
	tool := NewRunWorkflowTool(runner)

	if _, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "nope", "query": "q"})); err == nil {
		t.Error("unknown workflow must error")
	}
	if _, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "Hidden", "query": "q"})); err == nil {
		t.Error("draft name must not resolve")
	}
	if _, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "", "query": "q"})); err == nil {
		t.Error("empty workflow must error")
	}
}

func TestRunWorkflowToolSurfacesFailedRun(t *testing.T) {
	runner := &fakeWorkflowRunner{workflows: []*types.Workflow{published("wf-1", "X")}}
	runner.runFn = func(context.Context, string, *types.RunWorkflowRequest) (*types.WorkflowRun, error) {
		return &types.WorkflowRun{Status: types.WorkflowRunStatusFailed, Error: "kb down"}, nil
	}
	out, err := NewRunWorkflowTool(runner).Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "wf-1", "query": "q"}))
	if err != nil {
		t.Fatalf("failed run is a tool result, not a transport error: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "kb down") {
		t.Errorf("out = %+v", out)
	}

	transportErr := errors.New("boom")
	runner.runFn = func(context.Context, string, *types.RunWorkflowRequest) (*types.WorkflowRun, error) {
		return nil, transportErr
	}
	if _, err := NewRunWorkflowTool(runner).Execute(context.Background(), mustJSON(t, map[string]any{"workflow": "wf-1", "query": "q"})); err == nil {
		t.Error("transport error must propagate")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
