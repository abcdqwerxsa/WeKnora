import type { WorkflowNodeType } from '@/api/workflow'

/**
 * Node presentation metadata: palette grouping, canvas colors and the
 * parameter summary shown on node cards. Pure data — no component logic.
 */

export interface NodePaletteEntry {
  kind: WorkflowNodeType
  /** Palette group id (i18n: workflow.palette.groups.<id>). */
  group: 'start' | 'basic' | 'transform' | 'data' | 'network'
}

/** Palette order: groups in display order, kinds inside in palette order. */
export const NODE_PALETTE: NodePaletteEntry[] = [
  { kind: 'Start', group: 'start' },
  { kind: 'LLM', group: 'basic' },
  { kind: 'Retrieval', group: 'basic' },
  { kind: 'Switch', group: 'basic' },
  { kind: 'Answer', group: 'basic' },
  { kind: 'Template', group: 'transform' },
  { kind: 'VariableAggregator', group: 'transform' },
  { kind: 'DataOps', group: 'data' },
  { kind: 'HTTP', group: 'network' },
]

export const PALETTE_GROUPS: Array<NodePaletteEntry['group']> = ['start', 'basic', 'transform', 'data', 'network']

/** Canvas accent per node kind (WfNodeCard badge + top border). */
export const NODE_COLORS: Record<WorkflowNodeType, string> = {
  Start: '#34c77b',
  LLM: '#8e7cf0',
  Retrieval: '#4d9fff',
  Switch: '#f0a24a',
  Answer: '#9aa4b2',
  Template: '#2ba8a0',
  VariableAggregator: '#c25bd1',
  DataOps: '#5a6acf',
  HTTP: '#d1605a',
}

/** Node-side parameter badge icon (subset of tdesign icon names). */
export const NODE_ICONS: Record<WorkflowNodeType, string> = {
  Start: 'play-circle',
  LLM: 'chat-bubble',
  Retrieval: 'search',
  Switch: 'fork',
  Answer: 'chat',
  Template: 'format-letter-case',
  VariableAggregator: 'merge',
  DataOps: 'server',
  HTTP: 'link',
}

/** Upstream output params a reference picker may offer for a node kind. */
export function outputParamsOf(kind: WorkflowNodeType, params?: Record<string, unknown>): string[] {
  switch (kind) {
    case 'Start':
      return ['query']
    case 'LLM':
      return ['content']
    case 'Retrieval':
      return ['chunks', 'doc_aggs']
    case 'Template':
      return ['text']
    case 'HTTP':
      return ['status_code', 'body', 'headers']
    case 'DataOps':
      return ['columns', 'rows', 'row_count']
    case 'VariableAggregator': {
      // Outputs are the user-declared variable names.
      const vars = params?.variables
      if (Array.isArray(vars)) {
        return vars.map((v) => String((v as { name?: unknown })?.name ?? '')).filter(Boolean)
      }
      return []
    }
    default:
      return []
  }
}

/** Short parameter summary rendered as the node-card subtitle. */
export function paramSummary(kind: WorkflowNodeType, params?: Record<string, unknown>): string {
  if (!params) return ''
  const count = (v: unknown): number => (Array.isArray(v) ? v.length : 0)
  switch (kind) {
    case 'LLM':
      return typeof params.model === 'string' && params.model ? params.model : ''
    case 'Retrieval': {
      const n = count(params.kb_ids)
      return n > 0 ? `${n} KB` : ''
    }
    case 'Switch':
      return count(params.cases) > 0 ? `${count(params.cases)} cases` : ''
    case 'Template':
      return count(params.ops) > 0 ? `${count(params.ops)} ops` : ''
    case 'VariableAggregator':
      return count(params.variables) > 0 ? `${count(params.variables)} vars` : ''
    case 'HTTP': {
      const method = typeof params.method === 'string' ? params.method : ''
      let host = ''
      if (typeof params.url === 'string' && params.url) {
        try {
          host = new URL(params.url).host
        } catch {
          host = params.url.slice(0, 24)
        }
      }
      return [method, host].filter(Boolean).join(' ')
    }
    case 'DataOps':
      return typeof params.sql === 'string' && params.sql.trim() ? 'SQL' : ''
    default:
      return ''
  }
}
