import { get, post } from '@/utils/request'

/**
 * Built-in workflow template client.
 *
 * Templates are read-only presets served from the engine's startup-loaded
 * registry (config/workflow_templates). Instantiate copies one into the
 * current workspace as a published workflow, binding every declared KB
 * placeholder to a real knowledge base id.
 */

export interface WorkflowTemplateSummary {
  id: string
  category?: string
  name: string
  description?: string
  kb_placeholders?: string[]
  node_count?: number
}

export interface WorkflowTemplateDetail extends WorkflowTemplateSummary {
  dsl?: unknown
}

export interface InstantiateWorkflowTemplatePayload {
  /** Overrides the template's localized display name. */
  name?: string
  /** kb placeholder → knowledge base id in the current workspace. */
  kb_bindings?: Record<string, string>
}

export const listWorkflowTemplates = (): Promise<{ success: boolean; data: WorkflowTemplateSummary[] }> =>
  get('/api/v1/workflow-templates')

export const instantiateWorkflowTemplate = (
  id: string,
  payload: InstantiateWorkflowTemplatePayload,
): Promise<{ success: boolean; data?: { id: string }; message?: string }> =>
  post(`/api/v1/workflow-templates/${id}/instantiate`, payload)
