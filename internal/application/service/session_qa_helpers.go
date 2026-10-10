package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---------------------------------------------------------------------------
// Shared QA helpers: KB resolution, model resolution, retrieval tenant
// ---------------------------------------------------------------------------

// resolveKnowledgeBases resolves the effective knowledge base IDs and knowledge IDs
// for a QA request. Priority:
//  1. Explicit @mentions (request-specified kbIDs / knowledgeIDs)
//  2. RetrieveKBOnlyWhenMentioned -> disable KB if no mention
//  3. Agent's configured knowledge bases (via KBSelectionMode)
func (s *sessionService) resolveKnowledgeBases(
	ctx context.Context,
	req *types.QARequest,
) (kbIDs []string, knowledgeIDs []string, err error) {
	kbIDs = req.KnowledgeBaseIDs
	knowledgeIDs = req.KnowledgeIDs
	requestedKBIDs := append([]string(nil), req.KnowledgeBaseIDs...)
	customAgent := req.CustomAgent

	hasExplicitMention := len(kbIDs) > 0 || len(knowledgeIDs) > 0 || len(req.TagScopes) > 0
	if customAgent != nil {
		logger.Infof(ctx, "KB resolution: hasExplicitMention=%v, RetrieveKBOnlyWhenMentioned=%v, KBSelectionMode=%s",
			hasExplicitMention, customAgent.Config.RetrieveKBOnlyWhenMentioned, customAgent.Config.KBSelectionMode)
	}

	if hasExplicitMention {
		logger.Infof(ctx, "Using request-specified targets: kbs=%v, docs=%v", kbIDs, knowledgeIDs)
		// When using a shared agent, restrict @mentions to the agent's allowed KB scope
		// to prevent users from injecting KB/knowledge IDs outside the agent's configured range.
		if customAgent != nil && req.Session != nil && req.Session.TenantID != customAgent.TenantID {
			kbIDs, knowledgeIDs = s.restrictMentionsToAgentScope(ctx, customAgent, req.Session.TenantID, kbIDs, knowledgeIDs)
			req.TagScopes = s.restrictTagScopesToAgentScope(ctx, customAgent, req.Session.TenantID, req.TagScopes)
		}
	} else if customAgent != nil && customAgent.Config.RetrieveKBOnlyWhenMentioned {
		kbIDs = nil
		knowledgeIDs = nil
		logger.Infof(ctx, "RetrieveKBOnlyWhenMentioned is enabled and no @ mention found, KB retrieval disabled for this request")
	} else if customAgent != nil {
		kbIDs = s.resolveKnowledgeBasesFromAgent(ctx, customAgent, req.Session.TenantID)
	}

	if err := types.AuthorizeTenantAPIKeyKnowledgeTargets(ctx, requestedKBIDs, req.KnowledgeIDs); err != nil {
		return nil, nil, err
	}
	kbIDs, err = types.FilterKnowledgeBasesForTenantAPIKeyScope(ctx, requestedKBIDs, kbIDs)
	if err != nil {
		return nil, nil, err
	}
	return kbIDs, knowledgeIDs, nil
}

func (s *sessionService) restrictTagScopesToAgentScope(
	ctx context.Context,
	agent *types.CustomAgent,
	sessionTenantID uint64,
	tagScopes []types.TagScope,
) []types.TagScope {
	if len(tagScopes) == 0 {
		return nil
	}
	allowedKBIDs := s.resolveKnowledgeBasesFromAgent(ctx, agent, sessionTenantID)
	allowedSet := make(map[string]bool, len(allowedKBIDs))
	for _, id := range allowedKBIDs {
		allowedSet[id] = true
	}
	filtered := make([]types.TagScope, 0, len(tagScopes))
	for _, scope := range tagScopes {
		if allowedSet[scope.KnowledgeBaseID] {
			filtered = append(filtered, scope)
			continue
		}
		logger.Warnf(ctx, "Blocking @mentioned tag scope for KB %s: not in shared agent's allowed scope", scope.KnowledgeBaseID)
	}
	return filtered
}

// resolveChatModelID resolves the effective chat model ID for a QA request.
//
// When a user-configured agent is selected, its model configuration must be
// complete and valid. The internal wiki fixer is the one exception: it is not
// exposed in the agent UI, so an empty model_id falls back to KB/system model
// selection. A request-level override may choose another valid model for this
// request, but it must not make an unconfigured or stale agent appear usable.
//
// resolveChatModelID picks the chat model for a request.
// Priority (2026-09-10, C-mode):
//   1. Request's SummaryModelID (user-picked at chat time) - highest, wins
//      over everything else so the UI can offer free model choice per
//      conversation.
//   2. Custom agent's configured model_id (when set) - preserves the
//      specialized-agent contract that some agents (e.g. the risk
//      annotator) pin to a specific good model.
//   3. selectChatModelID fallback chain - KB summary model, then any
//      tenant KnowledgeQA model, then the system default.
//
// When the custom agent is present but its model_id is empty, we no
// longer hard-fail: instead we warn and fall through to the request
// override and finally the KB / system default. This is what makes the
// agent's model_id truly optional and lets every user pick a model at
// chat time even for an agent that was saved without one.
//
// Without an agent, the legacy KB / session / system fallback remains
// available for non-agent callers.
func (s *sessionService) resolveChatModelID(
	ctx context.Context,
	req *types.QARequest,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
) (string, error) {
	summaryModelID := strings.TrimSpace(req.SummaryModelID)
	customAgent := req.CustomAgent
	session := req.Session

	// 1. Request-level override (highest priority).
	if summaryModelID != "" {
		if model, err := s.modelService.GetModelByID(ctx, summaryModelID); err == nil && model != nil &&
			model.Type == types.ModelTypeKnowledgeQA {
			logger.Infof(ctx, "Using request's summary model override: %s", summaryModelID)
			return summaryModelID, nil
		}
		logger.Warnf(ctx, "Request provided invalid summary model ID %s, falling back", summaryModelID)
	}

	// 2. Custom agent's configured model_id (when set).
	if customAgent != nil {
		configuredAgentModelID := strings.TrimSpace(customAgent.Config.ModelID)
		if configuredAgentModelID != "" {
			model, err := s.modelService.GetModelByID(ctx, configuredAgentModelID)
			if err != nil || model == nil || model.Type != types.ModelTypeKnowledgeQA {
				logger.Warnf(ctx, "Configured chat model %s is unavailable for agent %s, falling back",
					configuredAgentModelID, customAgent.ID)
			} else {
				logger.Infof(ctx, "Using custom agent's model_id: %s", configuredAgentModelID)
				return configuredAgentModelID, nil
			}
		} else if customAgent.ID == types.BuiltinWikiFixerID {
			// Wiki fixer is internal and intentionally omittable from the
			// agent-management UI; it can never receive a user-configured
			// model_id and is expected to fall through to KB / system default.
			logger.Infof(ctx, "No model_id configured for internal wiki fixer %s, using KB/system fallback", customAgent.ID)
		} else {
			// Other agents with empty model_id are intentionally allowed
			// (C-mode): the agent creator chose to defer model choice to the
			// conversation-time picker, so we fall through to KB / system
			// default instead of erroring out.
			logger.Infof(ctx, "No model_id configured for agent %s, using KB/system fallback", customAgent.ID)
		}
	}

	return s.selectChatModelID(ctx, session, knowledgeBaseIDs, knowledgeIDs)
}

// resolveRetrievalTenantID determines the tenant ID to use for retrieval scope.
// Priority: agent's tenant > context tenant > session tenant.
func (s *sessionService) resolveRetrievalTenantID(
	ctx context.Context,
	req *types.QARequest,
) uint64 {
	session := req.Session
	customAgent := req.CustomAgent

	retrievalTenantID := session.TenantID
	if customAgent != nil && customAgent.TenantID != 0 {
		retrievalTenantID = customAgent.TenantID
		logger.Infof(ctx, "Using agent tenant %d for retrieval scope", retrievalTenantID)
	} else if v := ctx.Value(types.TenantIDContextKey); v != nil {
		if tid, ok := v.(uint64); ok && tid != 0 {
			retrievalTenantID = tid
			logger.Infof(ctx, "Using effective tenant %d for retrieval from context", retrievalTenantID)
		}
	}
	return retrievalTenantID
}

// applyAgentOverridesToChatManage applies custom agent configuration overrides
// to a ChatManage object that was initialized with system defaults.
// This covers: system prompt, context template, temperature, max tokens, thinking,
// citation output, retrieval thresholds, rewrite settings, fallback settings, FAQ strategy,
// and history turns.
func (s *sessionService) applyAgentOverridesToChatManage(
	ctx context.Context,
	customAgent *types.CustomAgent,
	cm *types.ChatManage,
) {
	if customAgent == nil {
		return
	}

	// Ensure defaults are set
	customAgent.EnsureDefaults()

	// Override summary config fields
	if customAgent.Config.SystemPrompt != "" {
		cm.SummaryConfig.Prompt = customAgent.Config.SystemPrompt
		logger.Infof(ctx, "Using custom agent's system_prompt")
	}
	if customAgent.Config.ContextTemplate != "" {
		cm.SummaryConfig.ContextTemplate = customAgent.Config.ContextTemplate
		logger.Infof(ctx, "Using custom agent's context_template")
	}
	if customAgent.Config.Temperature >= 0 {
		cm.SummaryConfig.Temperature = customAgent.Config.Temperature
		logger.Infof(ctx, "Using custom agent's temperature: %f", customAgent.Config.Temperature)
	}
	if customAgent.Config.MaxCompletionTokens > 0 {
		cm.SummaryConfig.MaxCompletionTokens = customAgent.Config.MaxCompletionTokens
		logger.Infof(ctx, "Using custom agent's max_completion_tokens: %d", customAgent.Config.MaxCompletionTokens)
	}
	// Agent-level thinking setting takes full control (no global fallback).
	// EnsureDefaults pins nil to explicit false so thinking_control wire formats
	// always receive a value.
	cm.SummaryConfig.Thinking = customAgent.Config.Thinking
	cm.CitationEnabled = customAgent.Config.CitationEnabled
	if customAgent.Config.Thinking != nil {
		logger.Infof(ctx, "Using custom agent's thinking: %v", *customAgent.Config.Thinking)
	} else {
		logger.Warnf(ctx, "Custom agent thinking is unset after EnsureDefaults; model thinking param will be omitted")
	}

	// Override retrieval strategy settings
	if customAgent.Config.EmbeddingTopK > 0 {
		cm.EmbeddingTopK = customAgent.Config.EmbeddingTopK
	}
	if customAgent.Config.KeywordThreshold > 0 {
		cm.KeywordThreshold = customAgent.Config.KeywordThreshold
	}
	if customAgent.Config.VectorThreshold > 0 {
		cm.VectorThreshold = customAgent.Config.VectorThreshold
	}
	if customAgent.Config.RerankTopK > 0 {
		cm.RerankTopK = customAgent.Config.RerankTopK
	}
	cm.RerankThreshold = customAgent.Config.RerankThreshold
	if customAgent.Config.RerankModelID != "" {
		cm.RerankModelID = customAgent.Config.RerankModelID
	}

	// Override rewrite settings
	cm.EnableRewrite = customAgent.Config.EnableRewrite
	cm.EnableQueryExpansion = customAgent.Config.EnableQueryExpansion
	if customAgent.Config.RewritePromptSystem != "" {
		cm.RewritePromptSystem = customAgent.Config.RewritePromptSystem
	}
	if customAgent.Config.RewritePromptUser != "" {
		cm.RewritePromptUser = customAgent.Config.RewritePromptUser
	}
	if customAgent.Config.QueryUnderstandModelID != "" {
		cm.QueryUnderstandModelID = customAgent.Config.QueryUnderstandModelID
		logger.Infof(ctx, "Using custom agent's query_understand_model_id: %s",
			customAgent.Config.QueryUnderstandModelID)
	}

	// Override fallback settings
	if customAgent.Config.FallbackStrategy != "" {
		cm.FallbackStrategy = types.FallbackStrategy(customAgent.Config.FallbackStrategy)
	}
	if customAgent.Config.FallbackResponse != "" {
		cm.FallbackResponse = customAgent.Config.FallbackResponse
	}
	if customAgent.Config.FallbackPrompt != "" {
		cm.FallbackPrompt = customAgent.Config.FallbackPrompt
	}

	// Override web search settings
	if customAgent.Config.WebSearchMaxResults > 0 {
		cm.WebSearchMaxResults = customAgent.Config.WebSearchMaxResults
	}

	// Override history turns
	if customAgent.Config.HistoryTurns > 0 {
		cm.MaxRounds = customAgent.Config.HistoryTurns
		logger.Infof(ctx, "Using custom agent's history_turns: %d", cm.MaxRounds)
	}
	if !customAgent.Config.MultiTurnEnabled {
		cm.MaxRounds = 0
		logger.Infof(ctx, "Multi-turn disabled by custom agent, clearing history")
	}

	// FAQ strategy settings
	cm.FAQPriorityEnabled = customAgent.Config.FAQPriorityEnabled
	cm.FAQDirectAnswerThreshold = customAgent.Config.FAQDirectAnswerThreshold
	cm.FAQScoreBoost = customAgent.Config.FAQScoreBoost
	if cm.FAQPriorityEnabled {
		logger.Infof(ctx, "FAQ priority enabled: threshold=%.2f, boost=%.2f",
			cm.FAQDirectAnswerThreshold, cm.FAQScoreBoost)
	}

	// Data-analysis pipeline stage (opt-in, default off).
	cm.DataAnalysisEnabled = customAgent.Config.DataAnalysisEnabled
	if cm.DataAnalysisEnabled {
		logger.Infof(ctx, "Data analysis pipeline stage enabled by custom agent")
	}

	if len(customAgent.Config.IntentPrompts) > 0 {
		cm.IntentPromptOverrides = customAgent.Config.IntentPrompts
		logger.Infof(ctx, "Using custom agent's intent_prompts (%d overrides)", len(cm.IntentPromptOverrides))
	}
}

// restrictMentionsToAgentScope filters user-provided @mention targets (KB IDs
// and knowledge IDs) so that only those within the shared agent's allowed KB
// scope are retained. This prevents users from bypassing the agent's
// KBSelectionMode by injecting arbitrary KB/knowledge IDs into the request.
func (s *sessionService) restrictMentionsToAgentScope(
	ctx context.Context,
	agent *types.CustomAgent,
	sessionTenantID uint64,
	kbIDs []string,
	knowledgeIDs []string,
) ([]string, []string) {
	allowedKBIDs := s.resolveKnowledgeBasesFromAgent(ctx, agent, sessionTenantID)
	if len(allowedKBIDs) == 0 {
		logger.Warnf(ctx, "Shared agent has no allowed KBs, blocking all @mentions")
		return nil, nil
	}

	allowedSet := make(map[string]bool, len(allowedKBIDs))
	for _, id := range allowedKBIDs {
		allowedSet[id] = true
	}

	filteredKBs := make([]string, 0, len(kbIDs))
	for _, id := range kbIDs {
		if allowedSet[id] {
			filteredKBs = append(filteredKBs, id)
		} else {
			logger.Warnf(ctx, "Blocking @mentioned KB %s: not in shared agent's allowed scope", id)
		}
	}

	filteredKnowledge := knowledgeIDs
	if len(knowledgeIDs) > 0 {
		knowledgeList, err := s.knowledgeService.GetKnowledgeBatch(ctx, agent.TenantID, knowledgeIDs)
		if err != nil {
			logger.Warnf(ctx, "Failed to validate knowledge IDs against agent scope: %v, blocking all", err)
			filteredKnowledge = nil
		} else {
			filteredKnowledge = make([]string, 0, len(knowledgeList))
			for _, k := range knowledgeList {
				if k != nil && allowedSet[k.KnowledgeBaseID] {
					filteredKnowledge = append(filteredKnowledge, k.ID)
				} else if k != nil {
					logger.Warnf(ctx, "Blocking @mentioned knowledge %s (KB %s): not in shared agent's allowed scope",
						k.ID, k.KnowledgeBaseID)
				}
			}
		}
	}

	logger.Infof(ctx, "Restricted @mentions to agent scope: kbs %d->%d, knowledge %d->%d",
		len(kbIDs), len(filteredKBs), len(knowledgeIDs), len(filteredKnowledge))

	return filteredKBs, filteredKnowledge
}
