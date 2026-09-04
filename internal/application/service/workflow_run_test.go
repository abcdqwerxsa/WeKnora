package service

import (
	"context"
	"errors"
	"testing"

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
}

func newRunRepoStub(wf *types.Workflow) *runRepoStub {
	base := &stubWorkflowRepo{saved: wf}
	return &runRepoStub{stubWorkflowRepo: base}
}

func (r *runRepoStub) CreateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	// Store a copy so later mutations of the same pointer (status flips)
	// don't rewrite history — the test asserts the create-time status.
	cp := *run
	r.created = append(r.created, &cp)
	return nil
}
func (r *runRepoStub) UpdateWorkflowRun(_ context.Context, run *types.WorkflowRun) error {
	r.updated = append(r.updated, run)
	return nil
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

type wfStubChat struct {
	reply string
}

func (c *wfStubChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: c.reply}, nil
}
func (c *wfStubChat) ChatStream(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented in stub")
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

func runTestWorkflow(t *testing.T, dsl string) (*runRepoStub, *types.WorkflowRun, error) {
	t.Helper()
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", DSL: types.JSON(dsl)}
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, &wfStubModelSvc{reply: "llm-answer"}, &wfStubKBSvc{
		hits: []*types.SearchResult{{ID: "c1", Content: "chunk text", KnowledgeTitle: "doc"}},
	})
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
	// Lifecycle: one row created (running) then updated (succeeded).
	require.Len(t, repo.created, 1)
	assert.Equal(t, types.WorkflowRunStatusRunning, repo.created[0].Status)
	require.Len(t, repo.updated, 1)
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
	assert.Len(t, repo.created, 1)
	assert.Len(t, repo.updated, 1)
}

func TestRunWorkflow_MissingTenantRejected(t *testing.T) {
	svc := NewWorkflowService(newRunRepoStub(nil), nil, nil)
	_, err := svc.RunWorkflow(context.Background(), "wf-1", &types.RunWorkflowRequest{Query: "q"})
	assert.ErrorIs(t, err, ErrWorkflowTenantRequired)
}
