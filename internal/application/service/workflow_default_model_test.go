package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultModelSvc stubs ListModels (tenant-scoped by contract) and records
// which model id the LLM adapter actually resolved.
type defaultModelSvc struct {
	interfaces.ModelService // nil-embedded: only ListModels/GetChatModel are called
	listed                  []*types.Model
	// resolved records every GetChatModel id, proving which path ran.
	resolved []string
}

func (s *defaultModelSvc) ListModels(_ context.Context) ([]*types.Model, error) {
	return s.listed, nil
}

func (s *defaultModelSvc) GetChatModel(_ context.Context, modelId string) (chat.Chat, error) {
	s.resolved = append(s.resolved, modelId)
	return &wfStubChat{reply: "from:" + modelId}, nil
}

const emptyModelLLMDSL = `{"version":1,"components":{
	"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["llm"]},
	"llm":{"obj":{"component_name":"LLM","params":{"prompt":"{start@query}"}},"upstream":["start"],"downstream":["ans"]},
	"ans":{"obj":{"component_name":"Answer","params":{"template":"{llm@content}"}},"upstream":["llm"],"downstream":[]}}}`

func runWithDefaultModels(t *testing.T, listed []*types.Model) (*types.WorkflowRun, error, *defaultModelSvc) {
	t.Helper()
	wf := &types.Workflow{ID: "wf-def", TenantID: 9, Name: "wf", DSL: types.JSON(emptyModelLLMDSL)}
	svcModels := &defaultModelSvc{listed: listed}
	svc := NewWorkflowService(newRunRepoStub(wf), svcModels, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))
	run, err := svc.RunWorkflow(ctx, "wf-def", &types.RunWorkflowRequest{Query: "q"})
	return run, err, svcModels
}

// Empty model param falls back to the tenant's default KnowledgeQA model.
func TestDefaultModelFallback_EmptyModelUsesDefault(t *testing.T) {
	run, runErr, models := runWithDefaultModels(t, []*types.Model{
		{ID: "m-old", IsDefault: true, Type: types.ModelTypeKnowledgeQA, UpdatedAt: time.Now().Add(-time.Hour)},
	})
	require.NoError(t, runErr)
	require.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	require.Contains(t, string(run.Output), "from:m-old")
	assert.Equal(t, []string{"m-old"}, models.resolved, "exactly one resolution, via the default")
}

// Several is_default rows: the most recently updated wins (documented
// tie-break).
func TestDefaultModelFallback_TiebreakLatestUpdated(t *testing.T) {
	now := time.Now()
	run, runErr, models := runWithDefaultModels(t, []*types.Model{
		{ID: "m-stale", IsDefault: true, Type: types.ModelTypeKnowledgeQA, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "m-fresh", IsDefault: true, Type: types.ModelTypeKnowledgeQA, UpdatedAt: now},
		{ID: "m-fresh-wrongtype", IsDefault: true, Type: "embedding", UpdatedAt: now.Add(time.Hour)},
	})
	require.NoError(t, runErr)
	require.Contains(t, string(run.Output), "from:m-fresh")
	assert.Equal(t, []string{"m-fresh"}, models.resolved)
}

// No default configured: the run fails with the explicit guidance error
// and the row records it (no silent model guessing).
func TestDefaultModelFallback_NoDefaultFailsRun(t *testing.T) {
	run, runErr, models := runWithDefaultModels(t, []*types.Model{
		{ID: "m-nodefault", IsDefault: false, Type: types.ModelTypeKnowledgeQA},
	})
	require.Error(t, runErr)
	require.NotNil(t, run, "failure must stay observable in the run row")
	assert.Equal(t, types.WorkflowRunStatusFailed, run.Status)
	assert.Contains(t, run.Error, "requires a model id")
	assert.Empty(t, models.resolved, "no model resolution may happen")
}
