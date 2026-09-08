package service

import (
	"context"
	"strings"
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
	svc := newTestWFService(nil, m, k)
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
	assert.Equal(t, 321, m.chat.opts.MaxTokens)
	assert.Equal(t, 0.3, m.chat.opts.Temperature)
}

func TestRunLLMWithoutSystemPromptKeepsSingleMessage(t *testing.T) {
	svc, m, _ := newParamsTestService()
	_, err := svc.runLLM(context.Background(), nodes.LLMRequest{Prompt: "hi", Model: "m"})
	require.NoError(t, err)
	require.Len(t, m.chat.msgs, 1)
	assert.Equal(t, "user", m.chat.msgs[0].Role)
	assert.Zero(t, m.chat.opts.MaxTokens, "0 must stay provider-default")
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

// thinkingStreamChat streams a thinking-model-shaped channel: reasoning
// frames (ResponseTypeThinking) followed by answer frames.
type thinkingStreamChat struct {
	deltas []string
}

func (c *thinkingStreamChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: "unused"}, nil
}

func (c *thinkingStreamChat) ChatStream(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	go func() {
		defer close(ch)
		for _, d := range c.deltas {
			kind := types.ResponseTypeAnswer
			if strings.HasPrefix(d, "THINK:") {
				kind = types.ResponseTypeThinking
				d = strings.TrimPrefix(d, "THINK:")
			}
			ch <- types.StreamResponse{ResponseType: kind, Content: d}
		}
	}()
	return ch, nil
}
func (c *thinkingStreamChat) GetModelName() string { return "stub" }
func (c *thinkingStreamChat) GetModelID() string   { return "stub" }

// streamModelSvc serves the streamer through the ModelService slice.
type streamModelSvc struct {
	interfaces.ModelService
	m   chat.Chat
	rer *stubReranker
}

func (s *streamModelSvc) GetChatModel(context.Context, string) (chat.Chat, error) { return s.m, nil }
func (s *streamModelSvc) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return s.rer, nil
}

// TestRunLLMStreamSkipsThinkingFrames: thinking models emit their reasoning
// as ResponseTypeThinking frames on the same channel as the answer; the
// recorded node output must contain ONLY the answer track.
func TestRunLLMStreamSkipsThinkingFrames(t *testing.T) {
	m := &thinkingStreamChat{deltas: []string{
		"THINK:step one ", "THINK:step two ", "final ", "answer", "THINK:trailing",
	}}
	svc := newTestWFService(nil, &streamModelSvc{m: m, rer: &stubReranker{}}, &captureKBSvc{}).(*workflowService)
	var seen []string
	out, err := svc.runLLMStream(context.Background(), nodes.LLMRequest{Prompt: "p", Model: "m"}, func(d string) { seen = append(seen, d) })
	require.NoError(t, err)
	assert.Equal(t, "final answer", out, "thinking frames must not leak into the recorded content")
	assert.Equal(t, []string{"final ", "answer"}, seen, "delta sink must carry answer chunks only")
}

// TestRunLLMStreamPromotesThinkingOnlyStream: mixed-routing backends
// occasionally deliver the entire reply through reasoning_content frames;
// the node must promote that text instead of failing with no content.
func TestRunLLMStreamPromotesThinkingOnlyStream(t *testing.T) {
	m := &thinkingStreamChat{deltas: []string{
		"THINK:<think>step ", "THINK:by step</think>", "THINK:你是小破破呀！",
	}}
	svc := newTestWFService(nil, &streamModelSvc{m: m, rer: &stubReranker{}}, &captureKBSvc{}).(*workflowService)
	var seen []string
	out, err := svc.runLLMStream(context.Background(), nodes.LLMRequest{Prompt: "p", Model: "m"}, func(d string) { seen = append(seen, d) })
	require.NoError(t, err)
	assert.Equal(t, "你是小破破呀！", out, "thinking-only stream must promote its text sans <think> wrapper")
	assert.Equal(t, []string{"你是小破破呀！"}, seen)
}
