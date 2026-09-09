package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
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

// TestRunLLMForwardsThinkingFlag: the node's tri-state thinking must reach
// ChatOptions.Thinking untouched (nil = provider default).
func TestRunLLMForwardsThinkingFlag(t *testing.T) {
	svc, m, _ := newParamsTestService()
	off := false
	_, err := svc.runLLM(context.Background(), nodes.LLMRequest{Prompt: "p", Model: "m", Thinking: &off})
	require.NoError(t, err)
	require.NotNil(t, m.chat.opts.Thinking)
	assert.False(t, *m.chat.opts.Thinking)
	_, err = svc.runLLM(context.Background(), nodes.LLMRequest{Prompt: "p", Model: "m"})
	require.NoError(t, err)
	assert.Nil(t, m.chat.opts.Thinking, "absent thinking must stay nil (provider default)")
}

// TestRunRetrievalAggregatesDocAggs: doc_aggs folds chunks into one row per
// source document with hit counts and best score.
func TestRunRetrievalAggregatesDocAggs(t *testing.T) {
	svc, _, k := newParamsTestService()
	k.hits = []*types.SearchResult{
		{ID: "c0", Content: "a", KnowledgeID: "doc-1", KnowledgeTitle: "Doc One", Score: 0.9},
		{ID: "c1", Content: "b", KnowledgeID: "doc-1", KnowledgeTitle: "Doc One", Score: 0.7},
		{ID: "c2", Content: "c", KnowledgeID: "doc-2", KnowledgeTitle: "Doc Two", Score: 0.5},
	}
	res, err := svc.runRetrieval(context.Background(), nodes.RetrievalRequest{Query: "q", KBIDs: []string{"kb1"}})
	require.NoError(t, err)
	require.Len(t, res.DocAggs, 2)
	first := res.DocAggs[0]
	assert.Equal(t, "doc-1", first["knowledge_id"])
	assert.Equal(t, "Doc One", first["knowledge_title"])
	assert.Equal(t, 2, first["chunk_count"])
	assert.InDelta(t, 0.9, first["score"], 1e-9)
}

// stubTempDocs fakes the temporary-document service for attachment tests.
type stubTempDocs struct {
	ids    []string
	prompt string
}

func (s *stubTempDocs) Create(context.Context, uint64, string, string, string, int64, io.Reader, types.TemporaryDocumentCreateOptions) (*types.TemporaryDocument, error) {
	return nil, errors.New("not implemented")
}
func (s *stubTempDocs) Get(context.Context, uint64, string, string) (*types.TemporaryDocument, error) {
	return nil, errors.New("not implemented")
}
func (s *stubTempDocs) OpenFile(context.Context, uint64, string, string) (io.ReadCloser, string, error) {
	return nil, "", errors.New("not implemented")
}
func (s *stubTempDocs) List(context.Context, uint64, string) ([]*types.TemporaryDocument, error) {
	return nil, nil
}
func (s *stubTempDocs) Delete(context.Context, uint64, string, string) error { return nil }
func (s *stubTempDocs) Process(context.Context, *asynq.Task) error           { return nil }
func (s *stubTempDocs) CleanupExpired(context.Context) error                 { return nil }
func (s *stubTempDocs) ResolveForPrompt(_ context.Context, _ uint64, scope string, ids []string, query string) (*types.TemporaryDocumentPromptResult, error) {
	s.ids = ids
	if query != "the query" {
		return nil, errors.New("unexpected query")
	}
	if scope != WorkflowAttachmentScope("wf-1") {
		return nil, errors.New("unexpected scope: " + scope)
	}
	return &types.TemporaryDocumentPromptResult{
		Attachments: types.MessageAttachments{{ID: ids[0], FileName: "notes.md", Content: s.prompt}},
	}, nil
}

// TestRunLLMWithAttachmentsPrependsContext: run files resolve into the
// system prompt via MessageAttachments.BuildPrompt, ahead of the node's own
// system prompt.
func TestRunLLMWithAttachmentsPrependsContext(t *testing.T) {
	ms := &captureModelSvc{rer: &stubReranker{}}
	td := &stubTempDocs{prompt: "SECRET-ATTACHMENT-CONTENT"}
	svc := NewWorkflowService(nil, ms, &captureKBSvc{}, nil, nil, nil, nil, nil, nil, nil, nil, td).(*workflowService)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	out, err := svc.runLLMWithAttachments(ctx, WorkflowAttachmentScope("wf-1"), "the query", []string{"doc-1"},
		nodes.LLMRequest{Prompt: "p", SystemPrompt: "be brief", Model: "m"})
	require.NoError(t, err)
	assert.Equal(t, "reply", out)
	require.Len(t, ms.chat.msgs, 2)
	assert.Equal(t, "system", ms.chat.msgs[0].Role)
	assert.Contains(t, ms.chat.msgs[0].Content, "be brief")
	assert.Contains(t, ms.chat.msgs[0].Content, "SECRET-ATTACHMENT-CONTENT",
		"attachment context must be part of the system prompt")
}

// TestRunLLMStreamSurfacesErrorFrames: a stream that dies mid-flight emits
// ResponseTypeError frames; the failure must carry the provider's message
// instead of the generic "produced no content".
func TestRunLLMStreamSurfacesErrorFrames(t *testing.T) {
	m := &thinkingStreamChat{deltas: []string{
		"THINK:partial", "ERR:upstream connection reset",
	}}
	// thinkingStreamChat has no error mode — extend via a tiny wrapper.
	m2 := errFrameChat{thinkingStreamChat: m}
	svc := newTestWFService(nil, &streamModelSvc{m: &m2, rer: &stubReranker{}}, &captureKBSvc{}).(*workflowService)
	_, err := svc.runLLMStream(context.Background(), nodes.LLMRequest{Prompt: "p", Model: "m"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream connection reset")
}

// errFrameChat re-marks ERR: prefixed deltas as error frames.
type errFrameChat struct {
	*thinkingStreamChat
}

func (c errFrameChat) ChatStream(ctx context.Context, msgs []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch, err := c.thinkingStreamChat.ChatStream(ctx, msgs, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		for r := range ch {
			if strings.HasPrefix(r.Content, "ERR:") {
				r.Content = strings.TrimPrefix(r.Content, "ERR:")
				r.ResponseType = types.ResponseTypeError
			}
			out <- r
		}
	}()
	return out, nil
}

// TestWorkflowAttachmentScopeFitsColumn: temporary_documents.session_id is
// VARCHAR(36); the scope must stay within it for UUID workflow ids.
func TestWorkflowAttachmentScopeFitsColumn(t *testing.T) {
	id := "fd88dfdf-e30c-4650-9df7-82c7ef6ccccc"
	scope := WorkflowAttachmentScope(id)
	assert.Len(t, scope, 35, "wf- + 32 hex chars")
	assert.True(t, strings.HasPrefix(scope, "wf-"))
}
