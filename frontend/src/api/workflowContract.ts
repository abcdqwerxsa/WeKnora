/**
 * Workflow contract: types + constants only, ZERO imports.
 *
 * Pure data that mirrors the Go engine registry (internal/agent/workflow).
 * Kept dependency-free so editor utilities and node tests can consume it
 * without pulling the request/i18n chain (api/workflow.ts imports those).
 */

export type WorkflowNodeType =
  | 'Start'
  | 'LLM'
  | 'Retrieval'
  | 'Switch'
  | 'Answer'
  | 'Template'
  | 'VariableAggregator'
  | 'HTTP'
  | 'DataOps'
  | 'WebSearch'
  | 'QuestionClassifier'
  | 'ParameterExtractor'

export type WorkflowStatus = 'draft' | 'published' | 'archived'

export const WORKFLOW_NODE_TYPES: WorkflowNodeType[] = [
  'Start',
  'LLM',
  'Retrieval',
  'Switch',
  'Answer',
  'Template',
  'VariableAggregator',
  'HTTP',
  'DataOps',
  'WebSearch',
  'QuestionClassifier',
  'ParameterExtractor',
]

/** Engine output keys per node kind (source of {nodeId@param} references). */
export const NODE_OUTPUT_PARAMS: Partial<Record<WorkflowNodeType, string[]>> = {
  Start: ['query'],
  LLM: ['content'],
  Retrieval: ['chunks', 'doc_aggs'],
  Template: ['text'],
  HTTP: ['status_code', 'body', 'headers'],
  DataOps: ['columns', 'rows', 'row_count'],
}
