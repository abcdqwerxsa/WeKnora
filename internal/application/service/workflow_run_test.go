package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRepoStub persists run rows so tests can assert the run lifecycle.
type runRepoStub struct {
	*stubWorkflowRepo
	created []*types.WorkflowRun
	updated []*types.WorkflowRun
	runs    map[string]*types.WorkflowRun
	mu      sync.Mutex
}

func newRunRepoStub(wf *types.Workflow) *runRepoStub {
	base := &stubWorkflowRepo{saved: wf}
	return &runRepoStub{stubWorkflowRepo: base}
}

func (r *runRepoStub) CreateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	// History copies (status flips never rewrite history) plus the runs map
	// as the single current-state store the lookup reads.
	cp := *run
	r.mu.Lock()
	r.created = append(r.created, &cp)
	if r.runs == nil {
		r.runs = map[string]*types.WorkflowRun{}
	}
	cur := cp
	r.runs[run.ID] = &cur
	r.mu.Unlock()
	return nil
}
func (r *runRepoStub) UpdateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	cp := *run
	r.mu.Lock()
	r.updated = append(r.updated, &cp)
	r.runs[run.ID] = &cp
	r.mu.Unlock()
	return nil
}
func (r *runRepoStub) GetWorkflowRunByIDAndTenant(_ context.Context, runID string, tenantID uint64) (*types.WorkflowRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[runID]; ok && run.TenantID == tenantID {
		return run, nil
	}
	// Real sentinel: service/handler errors.Is(apprepo.ErrWorkflowNotFound)
	// mapping must behave exactly like production.
	return nil, apprepo.ErrWorkflowNotFound
}

// wfStubModelSvc embeds the wide ModelService interface (nil) and
// overrides only GetChatModel — the single method the wiring touches.
type wfStubModelSvc struct {
	interfaces.ModelService
	reply string
}

func (s *wfStubModelSvc) GetChatModel(_ context.Context, _ string) (chat.Chat, error) {
	return &wfStubChat{reply: s.reply}, nil
}

// ListModels answers empty so default-model fallback paths fail cleanly
// instead of panicking on the nil embedded interface.
func (s *wfStubModelSvc) ListModels(_ context.Context) ([]*types.Model, error) {
	return nil, nil
}

type wfStubChat struct {
	reply string
}

func (c *wfStubChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: c.reply}, nil
}
func (c *wfStubChat) ChatStream(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	// Stream the reply in two chunks so delta emission is observable.
	ch := make(chan types.StreamResponse, 2)
	half := len(c.reply) / 2
	ch <- types.StreamResponse{Content: c.reply[:half]}
	ch <- types.StreamResponse{Content: c.reply[half:], Done: true}
	close(ch)
	return ch, nil
}
func (c *wfStubChat) GetModelName() string { return "wf-stub-chat" }
func (c *wfStubChat) GetModelID() string   { return "wf-stub-chat" }

// wfStubKBSvc embeds KnowledgeBaseService (nil) and overrides HybridSearch.
type wfStubKBSvc struct {
	interfaces.KnowledgeBaseService
	hits []*types.SearchResult
}

func (s *wfStubKBSvc) HybridSearch(_ context.Context, _ string, _ types.SearchParams) ([]*types.SearchResult, error) {
	return s.hits, nil
}

// newTestWFService builds a workflowService with the common test wiring:
// repo + optional models/kbs; no enqueuer, no redis, no web search.
func newTestWFService(repo interfaces.WorkflowRepository, models interfaces.ModelService, kbs interfaces.KnowledgeBaseService) interfaces.WorkflowService {
	return NewWorkflowService(repo, models, kbs, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func runTestWorkflow(t *testing.T, dsl string) (*runRepoStub, *types.WorkflowRun, error) {
	t.Helper()
	// Published so the run gate (draft = creator/admin only) stays out of
	// the way for these mechanics-focused tests.
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", DSL: types.JSON(dsl), Status: types.WorkflowStatusPublished}
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, &wfStubModelSvc{reply: "llm-answer"}, &wfStubKBSvc{
		hits: []*types.SearchResult{{ID: "c1", Content: "chunk text", KnowledgeTitle: "doc"}},
	}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	run, err := svc.RunWorkflow(ctx, "wf-1", &types.RunWorkflowRequest{Query: "hello"})
	return repo, run, err
}

const linearDSL = `{
  "version": 1,
  "components": {
    "start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["llm"]},
    "llm":    {"obj": {"component_name": "LLM", "params": {"prompt": "{start@query}", "model": "m-1"}}, "upstream": ["start"], "downstream": ["ans"]},
    "ans":    {"obj": {"component_name": "Answer", "params": {"template": "result: {llm@content}"}}, "upstream": ["llm"], "downstream": []}
  }
}`

func TestRunWorkflow_LinearSucceeds(t *testing.T) {
	repo, run, err := runTestWorkflow(t, linearDSL)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Equal(t, "", run.Error)
	assert.Equal(t, uint64(10001), run.TenantID)
	assert.Contains(t, string(run.Output), "result: llm-answer")
	// Lifecycle: one row created (pending), then updated running→succeeded.
	require.Len(t, repo.created, 1)
	assert.Equal(t, types.WorkflowRunStatusPending, repo.created[0].Status)
	require.Len(t, repo.updated, 2)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, repo.updated[1].Status)
}

const retrievalDSL = `{
  "version": 1,
  "components": {
    "start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["ret"]},
    "ret":   {"obj": {"component_name": "Retrieval", "params": {"query": "{start@query}", "kb_ids": ["kb-1"], "top_k": 5}}, "upstream": ["start"], "downstream": ["ans"]},
    "ans":   {"obj": {"component_name": "Answer", "params": {"template": "{ret@chunks}"}}, "upstream": ["ret"], "downstream": []}
  }
}`

func TestRunWorkflow_RetrievalSucceeds(t *testing.T) {
	_, run, err := runTestWorkflow(t, retrievalDSL)
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Contains(t, string(run.Output), "chunk text")
}

func TestRunWorkflow_TracePersistsNodeOutputs(t *testing.T) {
	// Phase 1 run-debugging: the run row must carry a per-node trace in
	// execution order, each entry with the node's kind and recorded
	// outputs (the payload the detail panel renders).
	_, run, err := runTestWorkflow(t, linearDSL)
	require.NoError(t, err)
	require.NotEmpty(t, run.Trace)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 3) // start → llm → ans
	assert.Equal(t, "Start", trace[0].Kind)
	assert.Equal(t, "hello", trace[0].Outputs["query"])
	assert.Equal(t, "LLM", trace[1].Kind)
	assert.Equal(t, "llm-answer", trace[1].Outputs["content"])
	assert.Equal(t, "Answer", trace[2].Kind)
	assert.Equal(t, "result: llm-answer", trace[2].Outputs["answer"])
	for _, e := range trace {
		assert.Equal(t, "finished", e.Phase)
		assert.GreaterOrEqual(t, e.DurationMS, int64(0))
	}
}

func TestRunWorkflow_FailedNodeTraceRecordsError(t *testing.T) {
	// A node-level failure must land in the trace with its error text so
	// the detail panel can point at the failing node.
	failDSL := `{"version":1,"components":{
		"start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["ret"]},
		"ret":   {"obj": {"component_name": "Retrieval", "params": {"query": "{start@query}", "kb_ids": ["kb-1"]}}, "upstream": ["start"], "downstream": ["ans"]},
		"ans":   {"obj": {"component_name": "Answer", "params": {"template": "x"}}, "upstream": ["ret"], "downstream": []}
	}}`
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", DSL: types.JSON(failDSL), Status: types.WorkflowStatusPublished}
	repo := newRunRepoStub(wf)
	svc := newTestWFService(repo, &wfStubModelSvc{reply: "x"}, &failingKBSvc{})
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	run, err := svc.RunWorkflow(ctx, "wf-1", &types.RunWorkflowRequest{Query: "hello"})
	require.Error(t, err)
	require.NotNil(t, run)
	var trace []types.WorkflowRunTraceEntry
	require.NoError(t, json.Unmarshal(run.Trace, &trace))
	require.Len(t, trace, 2) // start finished, ret failed
	assert.Equal(t, "finished", trace[0].Phase)
	assert.Equal(t, "failed", trace[1].Phase)
	assert.Equal(t, "Retrieval", trace[1].Kind)
	assert.NotEmpty(t, trace[1].Err)
}

// failingKBSvc makes every HybridSearch call fail (node-failure trace test).
type failingKBSvc struct {
	interfaces.KnowledgeBaseService
}

func (s *failingKBSvc) HybridSearch(_ context.Context, _ string, _ types.SearchParams) ([]*types.SearchResult, error) {
	return nil, errors.New("kb down")
}

func TestRunWorkflow_InvalidDSLIsRejectedWithoutRunRow(t *testing.T) {
	// Shape-level failure (unknown component name) is a 400-style error:
	// it must NOT leave a run row — the workflow never started.
	badDSL := `{"version":1,"components":{"a":{"obj":{"component_name":"Nope"},"upstream":[]}}}`
	repo, run, err := runTestWorkflow(t, badDSL)
	require.Error(t, err)
	assert.Nil(t, run)
	assert.Empty(t, repo.created, "invalid DSL must not persist a run")
	assert.Empty(t, repo.updated)
}

func TestRunWorkflow_CompileCyclePersistsFailedRun(t *testing.T) {
	// A cycle passes shape validation (entry exists, names registered) but
	// eino rejects it at compile time — this failure happens after the run
	// row exists and must surface as a persisted failed run.
	cycleDSL := `{"version":1,"components":{
		"start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["a"]},
		"a": {"obj": {"component_name": "LLM", "params": {"prompt": "{start@query}", "model": "m-1"}}, "upstream": ["start", "b"], "downstream": ["b"]},
		"b": {"obj": {"component_name": "Answer", "params": {"template": "{a@content}"}}, "upstream": ["a"], "downstream": ["a"]}
	}}`
	repo, run, err := runTestWorkflow(t, cycleDSL)
	require.Error(t, err)
	require.NotNil(t, run, "a compile-time failure after row creation must stay observable")
	assert.Equal(t, types.WorkflowRunStatusFailed, run.Status)
	assert.NotEmpty(t, run.Error)
	// Lifecycle writes: create(pending) → update(running) → update(failed).
	assert.Len(t, repo.created, 1)
	assert.Equal(t, types.WorkflowRunStatusPending, repo.created[0].Status)
	require.Len(t, repo.updated, 2)
	assert.Equal(t, types.WorkflowRunStatusRunning, repo.updated[0].Status)
	assert.Equal(t, types.WorkflowRunStatusFailed, repo.updated[1].Status)
}

func TestRunWorkflow_MissingTenantRejected(t *testing.T) {
	svc := newTestWFService(newRunRepoStub(nil), nil, nil)
	_, err := svc.RunWorkflow(context.Background(), "wf-1", &types.RunWorkflowRequest{Query: "q"})
	assert.ErrorIs(t, err, ErrWorkflowTenantRequired)
}

// MarkWorkflowRunCancelled mirrors the real repo's state-guarded update:
// only pending/running rows flip; terminal rows report not-cancellable.
func (r *runRepoStub) MarkWorkflowRunCancelled(_ context.Context, runID string, tenantID uint64) error {
	cur := r.runs[runID]
	if cur == nil || cur.TenantID != tenantID {
		return apprepo.ErrWorkflowNotFound
	}
	if cur.Status != types.WorkflowRunStatusPending && cur.Status != types.WorkflowRunStatusRunning {
		return apprepo.ErrWorkflowRunNotCancellable
	}
	cur.Status = types.WorkflowRunStatusCancelled
	return nil
}

// seedRun inserts a pre-existing run row (terminal-state tests).
func (r *runRepoStub) seedRun(run *types.WorkflowRun) {
	if r.runs == nil {
		r.runs = map[string]*types.WorkflowRun{}
	}
	cp := *run
	r.runs[run.ID] = &cp
}

// lastCreatedRunID returns the id of the most recently created run.
func (r *runRepoStub) lastCreatedRunID() string {
	if len(r.created) == 0 {
		return ""
	}
	return r.created[len(r.created)-1].ID
}

// runRow returns the current stored copy of a run row (nil when absent).
func (r *runRepoStub) runRow(runID string) *types.WorkflowRun {
	return r.runs[runID]
}

func TestRunWorkflow_RequiredStartInputValidated(t *testing.T) {
	// A Start node declaring a required field rejects blank input before a
	// run row exists; providing the field runs normally.
	dsl := `{"version":1,"components":{
		"start": {"obj": {"component_name": "Start", "params": {"fields": [{"name": "city", "type": "text", "required": true}]}}, "upstream": [], "downstream": ["ans"]},
		"ans":   {"obj": {"component_name": "Answer", "params": {"template": "city: {start@city}"}}, "upstream": ["start"], "downstream": []}
	}}`
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", DSL: types.JSON(dsl), Status: types.WorkflowStatusPublished}
	repo := newRunRepoStub(wf)
	svc := newTestWFService(repo, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))

	if _, err := svc.RunWorkflow(ctx, "wf-1", &types.RunWorkflowRequest{Query: "hi"}); !errors.Is(err, ErrWorkflowMissingInput) {
		t.Fatalf("missing required input: err=%v, want ErrWorkflowMissingInput", err)
	}
	assert.Empty(t, repo.created, "rejected run must not persist a row")

	run, err := svc.RunWorkflow(ctx, "wf-1", &types.RunWorkflowRequest{Query: "hi", Inputs: map[string]any{"city": "Shenzhen"}})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Contains(t, string(run.Output), "city: Shenzhen")
}

func TestAnswerStreamSourceDetection(t *testing.T) {
	mk := func(answerTemplate, srcID, srcKind string) *wfengine.DSL {
		return &wfengine.DSL{Version: 1, Components: map[string]*wfengine.Component{
			"start": {Obj: wfengine.ComponentObj{ComponentName: "Start"}, Downstream: []string{srcID}},
			srcID:   {Obj: wfengine.ComponentObj{ComponentName: srcKind}, Downstream: []string{"ans"}},
			"ans":   {Obj: wfengine.ComponentObj{ComponentName: "Answer", Params: map[string]any{"template": answerTemplate}}},
		}}
	}
	if got := answerStreamSource(mk("{llm1@content}", "llm1", "LLM")); got != "llm1" {
		t.Errorf("single LLM ref = %q, want llm1", got)
	}
	if got := answerStreamSource(mk("result: {llm1@content}", "llm1", "LLM")); got != "" {
		t.Errorf("mixed template = %q, want empty (cannot stream coherently)", got)
	}
	if got := answerStreamSource(mk("{retr@chunks}", "retr", "Retrieval")); got != "" {
		t.Errorf("non-LLM source = %q, want empty (no deltas to forward)", got)
	}
}
