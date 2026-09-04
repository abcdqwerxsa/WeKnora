import { get, post, put, del } from '@/utils/request'

/**
 * Workflow orchestration MVP client.
 *
 * Node `type` doubles as the DSL `component_name` (Start / LLM / Retrieval /
 * Switch / Answer). The DSL keeps two synchronized views:
 *  - `graph`: canvas layout (nodes with positions + edges), consumed by the
 *    vue-flow editor;
 *  - `components`: execution topology (upstream/downstream + params),
 *    consumed by the backend engine.
 *
 * List/mutation responses follow the repository-wide `{ success, data }`
 * envelope (same as storage-backends / agents lists).
 */
export type WorkflowNodeType = 'Start' | 'LLM' | 'Retrieval' | 'Switch' | 'Answer'
export type WorkflowStatus = 'draft' | 'published' | 'archived'

export const WORKFLOW_NODE_TYPES: WorkflowNodeType[] = ['Start', 'LLM', 'Retrieval', 'Switch', 'Answer']

export interface WFPosition {
  x: number
  y: number
}

export interface WFNode {
  id: string
  type: WorkflowNodeType
  position: WFPosition
  data?: Record<string, unknown>
}

export interface WFEdge {
  id: string
  source: string
  target: string
  sourceHandle?: string
}

export interface WFComponent {
  obj: {
    component_name: string
    params: Record<string, unknown>
  }
  upstream: string[]
  downstream: string[]
}

export interface WorkflowDSL {
  version: 1
  graph: {
    nodes: WFNode[]
    edges: WFEdge[]
  }
  components: Record<string, WFComponent>
  variables?: Record<string, unknown>
}

export interface Workflow {
  id: string
  tenant_id?: number
  creator_id?: string
  name: string
  description?: string
  dsl: WorkflowDSL
  status: WorkflowStatus
  version?: number
  created_at?: string
  updated_at?: string
}

export interface WorkflowListResponse {
  success: boolean
  data: Workflow[]
}

export interface WorkflowMutationResponse {
  success: boolean
  data?: Workflow
  message?: string
}

export const listWorkflows = (): Promise<WorkflowListResponse> => get('/api/v1/workflows')

export const getWorkflow = (id: string): Promise<{ success: boolean; data?: Workflow; message?: string }> =>
  get(`/api/v1/workflows/${id}`)

export const createWorkflow = (payload: {
  name: string
  description?: string
  dsl?: WorkflowDSL
}): Promise<WorkflowMutationResponse> => post('/api/v1/workflows', payload)

export const updateWorkflow = (
  id: string,
  payload: { name?: string; description?: string; dsl?: WorkflowDSL },
): Promise<WorkflowMutationResponse> => put(`/api/v1/workflows/${id}`, payload)

export const deleteWorkflow = (id: string): Promise<{ success: boolean }> => del(`/api/v1/workflows/${id}`)
