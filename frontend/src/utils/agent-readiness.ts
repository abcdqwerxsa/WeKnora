import type { CustomAgentConfig } from '@/api/agent'
import type { ModelConfig } from '@/api/model'

export type AgentNotReadyReasonKey = 'summary_model' | 'rerank_model' | 'allowed_tools'

/**
 * An agent is chat-ready when it has a usable chat model configured, or when
 * its creator intentionally deferred model selection to the conversation
 * (C-mode, 2026-09-10) and the tenant has at least one KnowledgeQA model
 * available for backend fallback.
 *
 *   - If config.model_id is set, the referenced model must exist locally
 *     (KnowledgeQA type) unless this is a shared agent whose model lives in
 *     the source tenant — in that case the backend does the existence check.
 *   - If config.model_id is empty, we consider the agent ready as long as
 *     there's any local KnowledgeQA model the backend can fall back to
 *     (KB summary_model_id -> tenant default -> first available).
 *
 * For shared agents the model belongs to the source tenant, so the caller can
 * require a non-empty ID but must leave existence validation to the backend.
 */
export function agentHasConfiguredChatModel(
  config: Pick<CustomAgentConfig, 'model_id'> | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  sourceModelIsRemote = false,
): boolean {
  const modelID = config?.model_id?.trim()
  if (!modelID) {
    // C-mode: empty model_id is acceptable when a local KnowledgeQA fallback
    // exists. Backend's resolveChatModelID picks the right model at request
    // time (request override -> KB -> tenant default).
    return models.some(model => model.type === 'KnowledgeQA')
  }
  if (sourceModelIsRemote) return true
  return models.some(model => model.type === 'KnowledgeQA' && model.id === modelID)
}

/**
 * Keep this aligned with backend agentRequiresRerankModel.
 *
 * Rerank is needed only when knowledge_search can actually run. Explicitly
 * disabling the knowledge-base scope makes KB tools ineffective, even if an
 * older agent configuration still contains knowledge_search in allowed_tools.
 */
export function agentRequiresRerankModel(
  config: Pick<CustomAgentConfig, 'kb_selection_mode' | 'allowed_tools'> | undefined,
): boolean {
  if (!config || config.kb_selection_mode === 'none') return false

  const allowedTools = config.allowed_tools || []
  // The backend falls back to DefaultAllowedTools when the list is empty,
  // and that default includes knowledge_search.
  if (allowedTools.length === 0) return true

  return allowedTools.includes('knowledge_search')
}

export function getAgentNotReadyReasonKeys(
  config: Pick<
    CustomAgentConfig,
    'model_id' | 'rerank_model_id' | 'kb_selection_mode' | 'allowed_tools' | 'agent_mode'
  > | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  options: { isAgentMode: boolean; isSharedAgent: boolean },
): AgentNotReadyReasonKey[] {
  const reasons: AgentNotReadyReasonKey[] = []

  if (!agentHasConfiguredChatModel(config, models, options.isSharedAgent)) {
    reasons.push('summary_model')
  }

  if (options.isAgentMode && agentRequiresRerankModel(config)) {
    const rerankModelID = config?.rerank_model_id?.trim()
    const rerankExists = !!rerankModelID && (
      options.isSharedAgent
      || models.some(model => model.type === 'Rerank' && model.id === rerankModelID)
    )
    if (!rerankExists) {
      reasons.push('rerank_model')
    }
  }

  return reasons
}

/** Shared agents are owned by another tenant; receivers cannot edit their config. */
export function canLocallyConfigureAgent(sourceTenantId?: string): boolean {
  return !sourceTenantId
}

/** Map missing-config reasons to the agent editor section that fixes them. */
export function resolveAgentNotReadySection(reasons: AgentNotReadyReasonKey[]): string {
  if (reasons.includes('summary_model') || reasons.includes('rerank_model')) {
    return 'model'
  }
  if (reasons.includes('allowed_tools')) {
    return 'tools'
  }
  return 'model'
}

/** First missing item — used to highlight the corresponding editor field after navigation. */
export function resolveAgentNotReadyHighlight(
  reasons: AgentNotReadyReasonKey[],
): AgentNotReadyReasonKey | undefined {
  return reasons[0]
}
