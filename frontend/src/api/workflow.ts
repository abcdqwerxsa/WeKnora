import { get, post, put, del } from '@/utils/request'
import { WORKFLOW_NODE_TYPES } from './workflowContract'
import type { WorkflowNodeType, WorkflowStatus } from './workflowContract'

/**
 * Workflow orchestration client.
 *
 * Node `type` doubles as the DSL `component_name`. The DSL keeps two
 * synchronized views:
 *  - `graph`: canvas layout (nodes with positions + edges), consumed by the
 *    vue-flow editor;
 *  - `components`: execution topology (upstream/downstream + params),
 *    consumed by the backend engine.
 *
 * Param keys inside `components[id].obj.params` MUST match the engine
 * registry verbatim (snake_case, e.g. `kb_ids`, `system_prompt`,
 * `top_k`) — the engine reads them by literal key and fails on missing
 * required ones. Typed interfaces below document that contract; the DSL
 * itself keeps `params` as a loose record because not every field is set.
 *
 * The pure type/constant contract (WorkflowNodeType, WORKFLOW_NODE_TYPES,
 * ...) lives in ./workflowContract (zero imports); this module re-exports
 * it so existing importers keep working.
 */
export type {
  WorkflowNodeType,
  WorkflowStatus,
} from './workflowContract'
export { WORKFLOW_NODE_TYPES, NODE_OUTPUT_PARAMS } from './workflowContract'

// ---- Typed param shapes (per-node property forms) --------------------

export interface TemplateOp {
  op: 'upper' | 'lower' | 'trim' | 'replace' | 'regex_extract'
  from?: string
  to?: string
  pattern?: string
  group?: number
}

/** One comparison inside a Switch case group. */
export interface SwitchCondition {
  /** Left operand template, e.g. "{llm@content}". */
  ref: string
  /** Operator id (eq/ne/contains/not_contains/starts_with/ends_with/empty/not_empty/gt/gte/lt/lte/regex/in/not_in). */
  op: string
  /** Right operand literal/template; ignored by empty/not_empty. */
  value: string
}

/** One Switch routing rule: condition group + target node. */
export interface SwitchCaseGroup {
  conditions: SwitchCondition[]
  logic: 'and' | 'or'
  to: string
}

/** Start-node input form field declaration. */
export interface StartField {
  name: string
  label?: string
  type: 'text' | 'paragraph' | 'number' | 'select'
  required?: boolean
  default?: string
  /** select choices */
  options?: string[]
}

export interface VariableRef {
  name: string
  ref: string
}

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
  message?: string
  // Backend wraps the page: { workflows: Workflow[], total, page, page_size }.
  data: {
    workflows: Workflow[]
    total: number
    page?: number
    page_size?: number
  }
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

/** One persisted per-node record of a run's trace (run-detail endpoint). */
export interface WorkflowRunTraceEntry {
  node_id: string
  /** Component name ("LLM", "Retrieval", ...); empty when unknown. */
  kind?: string
  /** Terminal phase of this attempt: finished | failed. */
  phase: string
  duration_ms?: number
  outputs?: Record<string, unknown>
  error?: string
  /** true when restored from a checkpoint (resume), duration 0. */
  replayed?: boolean
}

export interface WorkflowRun {
  id: string
  tenant_id?: number
  workflow_id: string
  status: WorkflowRunStatus
  input?: unknown
  output?: WorkflowRunOutput | null
  /** Present on run-detail responses; history list rows omit it. */
  trace?: WorkflowRunTraceEntry[] | null
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
  /** Node outputs on finished frames (run-debugging payload). */
  outputs?: Record<string, unknown>
  /** true when the finished frame replayed from a checkpoint (resume). */
  replayed?: boolean
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
  payload: { query: string; files?: string[]; inputs?: Record<string, unknown>; async?: boolean },
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

/**
 * Run detail — the full row including the per-node trace (execution order).
 * History list rows deliberately omit `trace`; this endpoint is the payload
 * source for the run-detail / debug panel.
 */
export const getWorkflowRun = (workflowId: string, runId: string): Promise<{
  success: boolean
  data?: WorkflowRun
  message?: string
}> => get(`/api/v1/workflows/${workflowId}/runs/${runId}`)

/** Path-only SSE URL; the stream composable adds base URL + auth headers. */
export function workflowRunEventsUrl(workflowId: string, runId: string): string {
  return `/api/v1/workflows/${workflowId}/runs/${runId}/events`
}
