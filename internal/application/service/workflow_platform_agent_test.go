package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stubs for the agent_id reuse path. Each embeds its wide interface (nil) and
// overrides only the methods runPlatformAgent touches.

type wfStubCustomAgents struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (s *wfStubCustomAgents) GetAgentByID(_ context.Context, _ string) (*types.CustomAgent, error) {
	return s.agent, nil
}

type wfStubPlatformAgentSvc struct {
	interfaces.AgentService
	cfg    *types.AgentConfig
	prompt string
}

func (s *wfStubPlatformAgentSvc) CreateAgentEngine(
	_ context.Context, cfg *types.AgentConfig, _ chat.Chat, _ rerank.Reranker,
	_ *event.EventBus, _, _ string,
) (interfaces.AgentEngine, error) {
	s.cfg = cfg
	return &wfStubPlatformEngine{prompt: &s.prompt}, nil
}

type wfStubPlatformEngine struct{ prompt *string }

func (e *wfStubPlatformEngine) Execute(_ context.Context, _, _, query string, _ []chat.Message, _ ...[]string) (*types.AgentState, error) {
	*e.prompt = query
	return &types.AgentState{FinalAnswer: "platform-answer"}, nil
}
func (e *wfStubPlatformEngine) SetMemoryPrompt(string) {}

// The server-side guard mirrors the UI filter: a quick-answer agent must be
// rejected instead of silently running as a full ReAct agent.
func TestRunPlatformAgent_RejectsQuickAnswerAgent(t *testing.T) {
	ca := &types.CustomAgent{ID: "ag-qa", TenantID: 10001, Config: types.CustomAgentConfig{
		AgentMode: "quick-answer",
		ModelID:   "m-1",
	}}
	ref := &AgentServiceRef{}
	ref.Set(&wfStubPlatformAgentSvc{})
	svc := &workflowService{
		customAgents: &wfStubCustomAgents{agent: ca},
		models:       &wfStubModelSvc{reply: "x"},
		agents:       ref,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	_, err := svc.runPlatformAgent(ctx, 10001, "ag-qa", "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quick-answer")
}

// The agent_id reuse path must rebuild the CustomAgent's full config —
// prompt, tools, KBs, MCP selection — and run one stateless turn with the
// node's rendered prompt as query.
func TestRunPlatformAgent_ReusesCustomAgentConfig(t *testing.T) {
	ca := &types.CustomAgent{ID: "ag-1", TenantID: 10001, Config: types.CustomAgentConfig{
		AgentMode:        "smart-reasoning",
		ModelID:          "m-1",
		SystemPrompt:     "You are the KB bot",
		Temperature:      0.7,
		MaxIterations:    12,
		KnowledgeBases:   []string{"kb-1", "kb-2"},
		MCPSelectionMode: "selected",
		MCPServices:      []string{"svc-1"},
	}}
	agentSvc := &wfStubPlatformAgentSvc{}
	ref := &AgentServiceRef{}
	ref.Set(agentSvc)
	svc := &workflowService{
		customAgents: &wfStubCustomAgents{agent: ca},
		models:       &wfStubModelSvc{reply: "x"},
		agents:       ref,
	}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	answer, err := svc.runPlatformAgent(ctx, 10001, "ag-1", "hello")

	require.NoError(t, err)
	assert.Equal(t, "platform-answer", answer)
	assert.Equal(t, "hello", agentSvc.prompt, "node prompt becomes the turn query")
	c := agentSvc.cfg
	require.NotNil(t, c)
	assert.Equal(t, "You are the KB bot", c.SystemPrompt)
	assert.True(t, c.UseCustomSystemPrompt)
	assert.Equal(t, 12, c.MaxIterations)
	assert.InDelta(t, 0.7, c.Temperature, 0.001)
	assert.Equal(t, []string{"kb-1", "kb-2"}, c.KnowledgeBases)
	require.Len(t, c.SearchTargets, 2)
	assert.Equal(t, uint64(10001), c.SearchTargets[0].TenantID)
	assert.Equal(t, []string{"svc-1"}, c.MCPServices)
	assert.Equal(t, "selected", c.MCPSelectionMode)
}
