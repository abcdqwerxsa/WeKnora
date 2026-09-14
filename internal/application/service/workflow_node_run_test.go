package service

import (
	"context"
	"encoding/json"
	"testing"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// draftCtx returns a creator context so the draft-run gate passes.
func draftCtx(t *testing.T, tenantID uint64, creatorID string) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return context.WithValue(ctx, types.UserIDContextKey, creatorID)
}

const nodeRunDSL = `{
  "version": 1,
  "graph": {
    "nodes": [
      {"id": "start", "type": "Start", "position": {"x": 0, "y": 0}},
      {"id": "ans", "type": "Answer", "position": {"x": 200, "y": 0}},
      {"id": "float", "type": "WebSearch", "position": {"x": 400, "y": 0}}
    ],
    "edges": [{"id": "e1", "source": "start", "target": "ans"}]
  },
  "components": {
    "start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["ans"]},
    "ans":   {"obj": {"component_name": "Answer", "params": {"template": "got: {start@query}"}}, "upstream": ["start"], "downstream": []},
    "float": {"obj": {"component_name": "WebSearch", "params": {"query": "x", "provider_id": "", "max_results": 3}}, "upstream": [], "downstream": []}
  }
}`

func nodeRunWorkflow() *types.Workflow {
	return &types.Workflow{
		ID:        "wf-node",
		TenantID:  10001,
		Name:      "wf",
		CreatorID: "creator-1",
		DSL:       types.JSON(nodeRunDSL),
		Status:    types.WorkflowStatusDraft,
	}
}

// Regression (prod report): a node run with NO upstream inputs must still
// resolve {start@query} — the Start seed synthesis fills query/defaults.
// Also covers field defaults: the DSL's start has no fields, so the seed
// is just query=""; the template renders it instead of failing unresolved.
func TestRunWorkflowNode_EmptyInputsStillResolveStartRefs(t *testing.T) {
	wf := nodeRunWorkflow()
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, err := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{
		Inputs: map[string]any{"start": map[string]any{}},
	})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1)
	assert.Equal(t, "got: ", trace[0].Outputs["answer"])
}

// Field defaults declared on the Start node surface in the synthesized
// seed (mirrors a full run's Start output shape).
func TestRunWorkflowNode_StartSeedFillsFieldDefaults(t *testing.T) {
	wf := nodeRunWorkflow()
	var saved map[string]any
	require.NoError(t, json.Unmarshal(wf.DSL, &saved))
	comps := saved["components"].(map[string]any)
	start := comps["start"].(map[string]any)
	start["obj"] = map[string]any{
		"component_name": "Start",
		"params": map[string]any{"fields": []any{map[string]any{
			"name": "topic", "type": "text", "default": "默认题",
		}}},
	}
	ans := comps["ans"].(map[string]any)
	ans["obj"] = map[string]any{"component_name": "Answer", "params": map[string]any{"template": "got: {start@topic}"}}
	dsl, err := json.Marshal(saved)
	require.NoError(t, err)
	wf.DSL = types.JSON(dsl)

	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, rerr := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{})
	require.NoError(t, rerr)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1)
	assert.Equal(t, "got: 默认题", trace[0].Outputs["answer"])
}

// Floating top-level nodes (no edges either way) are dropped on the full
// run path: a stray node on the canvas must not turn into a second graph
// entry ("multiple entries" run failure). Iterations stay (their body is
// the runnable part).
func TestDropFloatingComponents(t *testing.T) {
	dsl := &wfengine.DSL{Version: 1, Components: map[string]*wfengine.Component{
		"start":    {Obj: wfengine.ComponentObj{ComponentName: "Start"}, Downstream: []string{"ans"}},
		"ans":      {Obj: wfengine.ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": "ok"}}, Upstream: []string{"start"}},
		"stray":    {Obj: wfengine.ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "x", "model": "m"}}},
		"iter":     {Obj: wfengine.ComponentObj{ComponentName: "Iteration", Params: map[string]any{"items": "[]", "item_var": "item", "index_var": "index", "output_ref": "", "output_var": "results"}}},
		"llm_body": {Obj: wfengine.ComponentObj{ComponentName: "LLM", Params: map[string]any{"prompt": "x", "model": "m"}}, Parent: "iter"},
	}}
	dropFloatingComponents(dsl)
	if _, ok := dsl.Components["stray"]; ok {
		t.Error("floating stray node must be dropped")
	}
	if _, ok := dsl.Components["start"]; !ok {
		t.Error("wired start must stay")
	}
	if _, ok := dsl.Components["ans"]; !ok {
		t.Error("wired ans must stay")
	}
	if _, ok := dsl.Components["iter"]; !ok {
		t.Error("outer-floating Iteration must stay (its body is runnable)")
	}
	if _, ok := dsl.Components["llm_body"]; !ok {
		t.Error("single-node loop body member must stay (legal body shape)")
	}
}

// Regression (review): a full run of an iteration workflow with a
// single-node body must survive the floating-component drop and compile.
func TestRunWorkflow_IterationSingleNodeBodySurvivesFloatingDrop(t *testing.T) {
	dsl := `{
  "version": 1,
  "components": {
    "iter": {"obj": {"component_name": "Iteration", "params": {"items": "[1,2]", "item_var": "item", "index_var": "index", "output_ref": "{body@content}", "output_var": "results"}}, "upstream": [], "downstream": ["ans"], "parent": ""},
    "body": {"obj": {"component_name": "LLM", "params": {"prompt": "n={item}", "model": "m"}}, "upstream": [], "downstream": [], "parent": "iter"},
    "ans":  {"obj": {"component_name": "Answer", "params": {"template": "done"}}, "upstream": ["iter"], "downstream": [], "parent": ""},
    "stray": {"obj": {"component_name": "LLM", "params": {"prompt": "x", "model": "m"}}, "upstream": [], "downstream": [], "parent": ""}
  }
}`
	wf := &types.Workflow{ID: "wf-iter", TenantID: 10001, Name: "wf", CreatorID: "creator-1", DSL: types.JSON(dsl), Status: types.WorkflowStatusPublished}
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, &wfStubModelSvc{reply: "llm-answer"}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).(*workflowService)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))

	run, err := svc.RunWorkflow(ctx, "wf-iter", &types.RunWorkflowRequest{Query: "q"})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status, "run error: %s", run.Error)
}

func TestRunWorkflowNode_SucceedsWithInjectedState(t *testing.T) {
	wf := nodeRunWorkflow()
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	// The Answer template references {start@query}; the start node never
	// runs — the ref resolves from the injected upstream outputs.
	run, err := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{
		Inputs: map[string]any{
			"start": map[string]any{"query": "hello"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Equal(t, "", run.Error)

	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1, "only the target node executes")
	assert.Equal(t, "ans", trace[0].NodeID)
	assert.Equal(t, "Answer", trace[0].Kind)
	assert.Equal(t, "got: hello", trace[0].Outputs["answer"])
}

func TestRunWorkflowNode_FailedRunSurfacesUnresolvedRef(t *testing.T) {
	// A ref to a param the Start seed never synthesizes (unknown field):
	// the node fails. The sync contract is the established RunWorkflow one
	// — the failed run row is persisted AND the execution error is returned;
	// the handler maps it to 200 + run. (Before seed synthesis this test
	// used {start@query}; that ref now resolves via the synthesized seed.)
	wf := nodeRunWorkflow()
	var saved map[string]any
	require.NoError(t, json.Unmarshal(wf.DSL, &saved))
	comps := saved["components"].(map[string]any)
	comps["ans"] = map[string]any{
		"obj":        map[string]any{"component_name": "Answer", "params": map[string]any{"template": "got: {start@ghost}"}},
		"upstream":   []any{"start"},
		"downstream": []any{},
	}
	dsl, err := json.Marshal(saved)
	require.NoError(t, err)
	wf.DSL = types.JSON(dsl)
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, rerr := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{})
	require.Error(t, rerr)
	require.NotNil(t, run)
	assert.Equal(t, types.WorkflowRunStatusFailed, run.Status)
	require.NotEmpty(t, run.Trace)
	assert.Contains(t, string(run.Trace), "start@ghost")
}

func TestRunWorkflowNode_NodeGuards(t *testing.T) {
	wf := nodeRunWorkflow()
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	// Missing node.
	_, err := svc.RunWorkflowNode(ctx, "wf-node", "nope", &types.RunWorkflowNodeRequest{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowNodeNotRunnable)

	// Start node (changed contract): runnable in isolation — the filled form
	// is promoted to the run request (query + inputs) and the node's echo
	// materialises it as outputs.
	run, serr := svc.RunWorkflowNode(ctx, "wf-node", "start", &types.RunWorkflowNodeRequest{
		Inputs: map[string]any{"start": map[string]any{"query": "你好"}},
	})
	require.NoError(t, serr)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1)
	assert.Equal(t, "你好", trace[0].Outputs["query"], "seeded form values materialise as Start outputs")

	// Iteration node: needs its loop body context — not runnable in isolation.
	var saved map[string]any
	require.NoError(t, json.Unmarshal(wf.DSL, &saved))
	comps := saved["components"].(map[string]any)
	comps["iter"] = map[string]any{
		"obj":        map[string]any{"component_name": "Iteration", "params": map[string]any{"items": "", "item_var": "item", "index_var": "index", "output_ref": "", "output_var": "results"}},
		"upstream":   []any{},
		"downstream": []any{},
	}
	iterDSL, err := json.Marshal(saved)
	require.NoError(t, err)
	wf.DSL = types.JSON(iterDSL)

	_, err = svc.RunWorkflowNode(ctx, "wf-node", "iter", &types.RunWorkflowNodeRequest{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowNodeNotRunnable)
}

func TestRunWorkflowNode_DraftGate(t *testing.T) {
	// A draft workflow may only be node-run by its creator (draft gate).
	wf := nodeRunWorkflow()
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "someone-else")

	_, err := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowNotDebuggable)
}

func TestCompile_IgnoresGraphOnlyFloatingNodes(t *testing.T) {
	// The editor saves floating (unwired) nodes in the GRAPH view only —
	// the components view excludes them (S1 fix). The engine must honor
	// that: the extra graph node must not become a second entry point.
	wf := nodeRunWorkflow()
	var saved map[string]any
	require.NoError(t, json.Unmarshal(wf.DSL, &saved))
	require.Contains(t, saved["components"], "float", "fixture sanity: the draft has a floating node")
	delete(saved["components"].(map[string]any), "float")
	cleaned, err := json.Marshal(saved)
	require.NoError(t, err)

	wf.DSL = types.JSON(cleaned)
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, err := svc.RunWorkflow(ctx, "wf-node", &types.RunWorkflowRequest{Query: "hello"})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
}

// Start's test step with attachments: the fixture's files array is
// promoted to the run request and echoes back through Start.Invoke.
func TestRunWorkflowNode_StartFixtureFilesEcho(t *testing.T) {
	wf := nodeRunWorkflow()
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, err := svc.RunWorkflowNode(ctx, "wf-node", "start", &types.RunWorkflowNodeRequest{
		Inputs: map[string]any{"start": map[string]any{
			"query": "q",
			"files": []any{"att-1", "att-2"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1)
	files, ok := trace[0].Outputs["files"].([]any)
	require.True(t, ok, "files = %#v", trace[0].Outputs["files"])
	assert.Equal(t, []any{"att-1", "att-2"}, files)
}

// Downstream test steps inherit the Start fixture's files: the seed scan
// promotes them to the run request, visible via {sys.files} rendering.
func TestRunWorkflowNode_DownstreamInheritsStartFiles(t *testing.T) {
	wf := nodeRunWorkflow()
	var saved map[string]any
	require.NoError(t, json.Unmarshal(wf.DSL, &saved))
	comps := saved["components"].(map[string]any)
	comps["ans"] = map[string]any{
		"obj":        map[string]any{"component_name": "Answer", "params": map[string]any{"template": "files={sys.files}"}},
		"upstream":   []any{"start"},
		"downstream": []any{},
	}
	dsl, err := json.Marshal(saved)
	require.NoError(t, err)
	wf.DSL = types.JSON(dsl)

	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := draftCtx(t, 10001, "creator-1")

	run, rerr := svc.RunWorkflowNode(ctx, "wf-node", "ans", &types.RunWorkflowNodeRequest{
		Inputs: map[string]any{"start": map[string]any{"files": []any{"att-1"}}},
	})
	require.NoError(t, rerr)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 1)
	assert.Contains(t, trace[0].Outputs["answer"], "att-1")
}
