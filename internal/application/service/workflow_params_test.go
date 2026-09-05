package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureChat records the messages and options of one Chat call.
type captureChat struct {
	msgs []chat.Message
	opts *chat.ChatOptions
}

func (c *captureChat) Chat(_ context.Context, msgs []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	c.msgs = msgs
	c.opts = opts
	return &types.ChatResponse{Content: "reply"}, nil
}
func (c *captureChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (c *captureChat) GetModelName() string { return "stub" }
func (c *captureChat) GetModelID() string   { return "stub" }

type captureModelSvc struct {
	interfaces.ModelService
	chat captureChat
	rer  *stubReranker
}

func (m *captureModelSvc) GetChatModel(context.Context, string) (chat.Chat, error) {
	return &m.chat, nil
}
func (m *captureModelSvc) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return m.rer, nil
}

type stubReranker struct{ order []int }

func (r *stubReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.RankResult, error) {
	out := make([]rerank.RankResult, 0, len(r.order))
	for _, idx := range r.order {
		if idx < len(docs) {
			out = append(out, rerank.RankResult{Index: idx, RelevanceScore: float64(len(out))})
		}
	}
	return out, nil
}
func (r *stubReranker) GetModelName() string { return "stub-rerank" }
func (r *stubReranker) GetModelID() string   { return "stub-rerank" }

type captureKBSvc struct {
	interfaces.KnowledgeBaseService
	params []types.SearchParams
	hits   []*types.SearchResult
}

func (k *captureKBSvc) HybridSearch(_ context.Context, _ string, p types.SearchParams) ([]*types.SearchResult, error) {
	k.params = append(k.params, p)
	return k.hits, nil
}

func newParamsTestService() (*workflowService, *captureModelSvc, *captureKBSvc) {
	m := &captureModelSvc{rer: &stubReranker{}}
	k := &captureKBSvc{hits: []*types.SearchResult{
		{ID: "c0", Content: "zero"},
		{ID: "c1", Content: "one"},
		{ID: "c2", Content: "two"},
	}}
	svc := NewWorkflowService(nil, m, k, nil, nil)
	return svc.(*workflowService), m, k
}

func TestRunLLMAssemblesSystemAndUserMessages(t *testing.T) {
	svc, m, _ := newParamsTestService()
	out, err := svc.runLLM(context.Background(), nodes.LLMRequest{
		Prompt: "hi", SystemPrompt: "be brief", Model: "m", MaxTokens: 321, Temperature: 0.3,
	})
	require.NoError(t, err)
	assert.Equal(t, "reply", out)
	require.Len(t, m.chat.msgs, 2)
	assert.Equal(t, "system", m.chat.msgs[0].Role)
	assert.Equal(t, "be brief", m.chat.msgs[0].Content)
	assert.Equal(t, "user", m.chat.msgs[1].Role)
	assert.Equal(t, 321, m.chat.opts.MaxCompletionTokens)
	assert.Equal(t, 0.3, m.chat.opts.Temperature)
}

func TestRunLLMWithoutSystemPromptKeepsSingleMessage(t *testing.T) {
	svc, m, _ := newParamsTestService()
	_, err := svc.runLLM(context.Background(), nodes.LLMRequest{Prompt: "hi", Model: "m"})
	require.NoError(t, err)
	require.Len(t, m.chat.msgs, 1)
	assert.Equal(t, "user", m.chat.msgs[0].Role)
	assert.Zero(t, m.chat.opts.MaxCompletionTokens, "0 must stay provider-default")
}

func TestRunRetrievalForwardsThresholds(t *testing.T) {
	svc, _, k := newParamsTestService()
	_, err := svc.runRetrieval(context.Background(), nodes.RetrievalRequest{
		Query: "q", KBIDs: []string{"kb1"}, TopK: 2,
		VectorThreshold: 0.66, KeywordThreshold: 0.11,
	})
	require.NoError(t, err)
	require.Len(t, k.params, 1)
	assert.Equal(t, 0.66, k.params[0].VectorThreshold)
	assert.Equal(t, 0.11, k.params[0].KeywordThreshold)
	assert.Equal(t, 2, k.params[0].MatchCount)
}

func TestRunRetrievalRerankReordersAndTrims(t *testing.T) {
	svc, m, _ := newParamsTestService()
	m.rer = &stubReranker{order: []int{2, 0}} // rank c2 first, then c0
	res, err := svc.runRetrieval(context.Background(), nodes.RetrievalRequest{
		Query: "q", KBIDs: []string{"kb1"}, TopK: 1,
		UseRerank: true, RerankModelID: "rr",
	})
	require.NoError(t, err)
	require.Len(t, res.Chunks, 1, "rerank must trim to topK")
	assert.Equal(t, "c2", res.Chunks[0]["id"])
	assert.Contains(t, res.Chunks[0], "rerank_score")
}
