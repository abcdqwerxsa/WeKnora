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

// ---------------------------------------------------------------------------
// Run execution + progress (consumes the stage-2 backend contract).
//
// Envelope note: create-run answers with a bare { run } object (sync 200 /
// async 202 / failed-with-record 200), while run history follows the
// repository-wide { success, data } envelope — the shapes are kept apart in
// the response types below instead of papered over.
// ---------------------------------------------------------------------------

export type WorkflowRunStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface WorkflowRunOutput {
  answer?: string
  path?: string[]
  outputs?: Record<string, Record<string, unknown>>
}

export interface WorkflowRun {
  id: string
  tenant_id?: number
  workflow_id: string
  status: WorkflowRunStatus
  input?: unknown
  output?: WorkflowRunOutput | null
  error?: string
  created_at?: string
  updated_at?: string
}

/** One SSE frame of GET /workflows/:id/runs/:runId/events. */
export interface WorkflowRunEventFrame {
  workflow_id: string
  run_id: string
  kind: 'node' | 'run'
  /** Set for kind=node frames; matches the canvas node id. */
  node_id?: string
  /** node frames: started|finished|failed · run frames: terminal run status. */
  phase: string
  error?: string
  duration_ms?: number
  status?: WorkflowRunStatus
}

export interface WorkflowRunResponse {
  success?: boolean
  run?: WorkflowRun
  message?: string
}

export interface WorkflowRunListResponse {
  success: boolean
  data?: { runs: WorkflowRun[]; total: number }
  message?: string
}

export const runWorkflow = (
  id: string,
  payload: { query: string; files?: string[]; async?: boolean },
): Promise<WorkflowRunResponse> => post(`/api/v1/workflows/${id}/runs`, payload)

/**
 * Cancel a pending/running run (best-effort engine stop + async-task
 * dequeue). Terminal runs answer idempotently with their current state —
 * cancelling an already-finished run is not an error. The SSE stream (if
 * attached) receives a kind=run phase=cancelled terminal frame, then closes.
 */
export const cancelWorkflowRun = (workflowId: string, runId: string): Promise<WorkflowRunResponse> =>
  post(`/api/v1/workflows/${workflowId}/runs/${runId}/cancel`)

/**
 * Resume a failed run from its checkpoint: the run row flips back to
 * running and re-executes asynchronously, skipping nodes whose outputs are
 * already checkpointed. Only status=failed runs are resumable — the server
 * answers 409 for other terminal states and 404 for unknown ids; a failed
 * run without checkpoint state simply re-executes from the start.
 */
export const resumeWorkflowRun = (workflowId: string, runId: string): Promise<WorkflowRunResponse> =>
  post(`/api/v1/workflows/${workflowId}/runs/${runId}/resume`)

export const listWorkflowRuns = (id: string): Promise<WorkflowRunListResponse> =>
  get(`/api/v1/workflows/${id}/runs`)

/** Path-only SSE URL; the stream composable adds base URL + auth headers. */
export function workflowRunEventsUrl(workflowId: string, runId: string): string {
  return `/api/v1/workflows/${workflowId}/runs/${runId}/events`
}
