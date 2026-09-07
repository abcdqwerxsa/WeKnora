package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- runAgent adapter -------------------------------------------------------

// fakeAgentService implements just CreateAgentEngine: it captures the config
// and returns a fake engine whose Execute echoes the query.
type fakeAgentService struct {
	interfaces.AgentService
	cfg        *types.AgentConfig
	executeErr error
	answer     string
}

type fakeAgentEngine struct {
	interfaces.AgentEngine
	answer string
}

func (e *fakeAgentEngine) Execute(_ context.Context, _, _, query string, _ []chat.Message, _ ...[]string) (*types.AgentState, error) {
	return &types.AgentState{FinalAnswer: e.answer + ":" + query}, nil
}

func (s *fakeAgentService) CreateAgentEngine(_ context.Context, cfg *types.AgentConfig, _ chat.Chat, _ rerank.Reranker, _ *event.EventBus, _, _ string) (interfaces.AgentEngine, error) {
	s.cfg = cfg
	return &fakeAgentEngine{answer: s.answer}, nil
}

// withAgents returns the concrete service with the agent runtime attached
// (test-only wiring: newTestWFService has no agent/mcp params).
func (s *workflowService) withAgents(agents interfaces.AgentService) *workflowService {
	s.agents = agents
	return s
}

func (s *workflowService) withMCP(mgr mcpClientProvider, services interfaces.MCPServiceService) *workflowService {
	s.mcpClients = mgr
	s.mcpServices = services
	return s
}

func agentSvc(t *testing.T, agents interfaces.AgentService) *workflowService {
	t.Helper()
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, Name: "wf", DSL: types.JSON(linearDSL), Status: types.WorkflowStatusPublished}
	return newTestWFService(newRunRepoStub(wf), &wfStubModelSvc{reply: "x"}, nil).(*workflowService).withAgents(agents)
}

func TestRunAgentBuildsScopedEngine(t *testing.T) {
	fake := &fakeAgentService{answer: "ans"}
	svc := agentSvc(t, fake)
	answer, err := svc.runAgent(agentCtx(), nodes.AgentRequest{
		Prompt: "q", Model: "m-1", KBIDs: []string{"kb-1", "kb-2"}, Temperature: 0.4,
	})
	require.NoError(t, err)
	assert.Equal(t, "ans:q", answer)
	// Config: scoped tools + search targets carry tenant + kb ids.
	assert.Equal(t, []string{"knowledge_search"}, fake.cfg.AllowedTools)
	require.Len(t, fake.cfg.SearchTargets, 2)
	assert.Equal(t, "kb-1", fake.cfg.SearchTargets[0].KnowledgeBaseID)
	assert.Equal(t, uint64(10001), fake.cfg.SearchTargets[0].TenantID)
	assert.Equal(t, 0.4, fake.cfg.Temperature)
	// Empty KB list → no search targets, no tools.
	fake2 := &fakeAgentService{answer: "a2"}
	_, err = agentSvc(t, fake2).runAgent(agentCtx(), nodes.AgentRequest{Prompt: "p", Model: "m-1"})
	require.NoError(t, err)
	assert.Empty(t, fake2.cfg.SearchTargets)
}

func TestRunAgentUnavailableAndModelResolution(t *testing.T) {
	// nil agent runtime → clear error.
	svc := agentSvc(t, nil)
	_, err := svc.runAgent(agentCtx(), nodes.AgentRequest{Prompt: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent runtime unavailable")

	// no tenant → tenant error
	svc2 := agentSvc(t, &fakeAgentService{answer: "x"})
	_, err = svc2.runAgent(context.Background(), nodes.AgentRequest{Prompt: "p"})
	assert.ErrorIs(t, err, ErrWorkflowTenantRequired)

	// missing model service → model resolution error surfaces
	svc3 := newTestWFService(newRunRepoStub(nil), nil, nil).(*workflowService).withAgents(&fakeAgentService{answer: "x"})
	_, err = svc3.runAgent(agentCtx(), nodes.AgentRequest{Prompt: "p", Model: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

func agentCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
}

// ---- runMCPTool adapter ----------------------------------------------------

type fakeMCPManager struct {
	service *types.MCPService
	client  mcp.MCPClient
}

type fakeMCPClient struct {
	mcp.MCPClient
	calledTool string
	calledArgs map[string]any
	result     *mcp.CallToolResult
	err        error
}

func (c *fakeMCPClient) CallTool(_ context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.calledTool, c.calledArgs = name, args
	return c.result, c.err
}

func (m *fakeMCPManager) GetOrCreateClient(_ context.Context, service *types.MCPService) (mcp.MCPClient, error) {
	if m.client == nil {
		return nil, errors.New("no client")
	}
	return m.client, nil
}

type fakeMCPServiceSvc struct {
	interfaces.MCPServiceService
	services []*types.MCPService
	err      error
}

func (s *fakeMCPServiceSvc) ListMCPServicesByIDs(_ context.Context, _ uint64, _ []string) ([]*types.MCPService, error) {
	return s.services, s.err
}

func mcpSvc(services []*types.MCPService, client mcp.MCPClient) *workflowService {
	return newTestWFService(newRunRepoStub(nil), nil, nil).(*workflowService).
		withAgents(nil).
		withMCP(&fakeMCPManager{client: client}, &fakeMCPServiceSvc{services: services})
}

func TestRunMCPToolHappyPath(t *testing.T) {
	svc := mcpSvc(
		[]*types.MCPService{{ID: "s1", Name: "docs", Enabled: true}},
		&fakeMCPClient{result: &mcp.CallToolResult{Content: []mcp.ContentItem{{Type: "text", Text: "hit"}}}},
	)
	result, text, err := svc.runMCPTool(agentCtx(), nodes.MCPToolRequest{
		ServiceID: "s1", Tool: "search", ArgsJSON: `{"q":"x"}`, TimeoutSeconds: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, "hit", text)
	assert.Equal(t, "hit", result)
}

func TestRunMCPToolFailurePaths(t *testing.T) {
	// OAuth service → loud error naming the fix.
	svc := mcpSvc([]*types.MCPService{{ID: "s1", Enabled: true, AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth}}},
		&fakeMCPClient{})
	_, _, err := svc.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "s1", Tool: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth")
	assert.Contains(t, err.Error(), "api_key/bearer")

	// Unknown service id.
	svc2 := mcpSvc(nil, &fakeMCPClient{})
	_, _, err = svc2.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "ghost", Tool: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Disabled service.
	svc3 := mcpSvc([]*types.MCPService{{ID: "s1", Enabled: false}}, &fakeMCPClient{})
	_, _, err = svc3.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "s1", Tool: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")

	// Tool-level error result (IsError).
	svc4 := mcpSvc([]*types.MCPService{{ID: "s1", Enabled: true}},
		&fakeMCPClient{result: &mcp.CallToolResult{IsError: true, Content: []mcp.ContentItem{{Text: "boom"}}}})
	_, _, err = svc4.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "s1", Tool: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	// Bad args JSON.
	svc5 := mcpSvc([]*types.MCPService{{ID: "s1", Enabled: true}}, &fakeMCPClient{})
	_, _, err = svc5.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "s1", Tool: "t", ArgsJSON: "not-json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON object")

	// Runtime missing.
	svc6 := newTestWFService(newRunRepoStub(nil), nil, nil).(*workflowService).withAgents(nil)
	_, _, err = svc6.runMCPTool(agentCtx(), nodes.MCPToolRequest{ServiceID: "s", Tool: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MCP runtime unavailable")
}
