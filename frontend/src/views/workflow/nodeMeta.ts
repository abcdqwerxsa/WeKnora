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
  { kind: 'QuestionClassifier', group: 'basic' },
  { kind: 'ParameterExtractor', group: 'basic' },
  { kind: 'Agent', group: 'basic' },
  { kind: 'Answer', group: 'basic' },
  { kind: 'Template', group: 'transform' },
  { kind: 'Code', group: 'transform' },
  { kind: 'Iteration', group: 'transform' },
  { kind: 'VariableAggregator', group: 'transform' },
  { kind: 'DataOps', group: 'data' },
  { kind: 'WebSearch', group: 'network' },
  { kind: 'MCPTool', group: 'network' },
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
  WebSearch: '#e8b339',
  QuestionClassifier: '#f07f6c',
  ParameterExtractor: '#6ca87f',
  Code: '#7a8b3f',
  Iteration: '#b06a3f',
  Agent: '#c25b74',
  MCPTool: '#4d8bc9',
}

/** Node-side parameter badge icon (subset of tdesign icon names). */
export const NODE_ICONS: Record<WorkflowNodeType, string> = {
  Start: 'play-circle',
  LLM: 'chat-bubble',
  Retrieval: 'search',
  Switch: 'fork',
  Answer: 'chat',
  Template: 'transform',
  VariableAggregator: 'git-merge',
  DataOps: 'server',
  HTTP: 'link',
  WebSearch: 'internet',
  QuestionClassifier: 'tag',
  ParameterExtractor: 'filter',
  Code: 'code',
  Iteration: 'refresh',
  Agent: 'root-list',
  MCPTool: 'server',
}

/** Upstream output params a reference picker may offer for a node kind. */
export function outputParamsOf(kind: WorkflowNodeType, params?: Record<string, unknown>): string[] {
  switch (kind) {
    case 'Start': {
      // Declared form fields are materialised into Start outputs by name.
      const fields = params?.fields
      const names = Array.isArray(fields)
        ? fields.map((f) => String((f as { name?: unknown })?.name ?? '')).filter(Boolean)
        : []
      return ['query', ...names]
    }
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
    case 'WebSearch':
      return ['results', 'result_count']
    case 'QuestionClassifier':
      return ['class']
    case 'ParameterExtractor': {
      const decls = params?.parameters
      return Array.isArray(decls)
        ? decls.map((p) => String((p as { name?: unknown })?.name ?? '')).filter(Boolean)
        : []
    }
    case 'Iteration': {
      const vars = params
      const out = typeof vars?.output_var === 'string' && vars.output_var ? vars.output_var : 'results'
      return [out, 'count']
    }
    case 'Agent':
      return ['answer']
    case 'MCPTool':
      return ['result', 'result_text']
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

// ---- upstream reference suggestions (shared by RefTextarea / picker) ----

/** One suggestion entry for {ref} autocompletion. */
export interface RefSuggestion {
  ref: string
  hint?: string
}

/**
 * Every reference resolvable from currentNodeId: upstream node outputs
 * ({nodeId@param}, ancestors only — the walk mirrors the engine's render
 * order), sys.query/sys.files, and env.<name> for each workflow variable.
 */
export function upstreamRefSuggestions(
  currentNodeId: string,
  nodes: Array<{ id: string; kind: WorkflowNodeType; params?: Record<string, unknown> }>,
  edges: Array<{ source: string; target: string }>,
  envNames: string[] = [],
): RefSuggestion[] {
  const ancestors = upstreamNodeIds(currentNodeId, edges)
  const out: RefSuggestion[] = []
  for (const node of nodes) {
    if (node.id === currentNodeId || !ancestors.has(node.id)) continue
    for (const param of outputParamsOf(node.kind, node.params)) {
      out.push({ ref: `${node.id}@${param}`, hint: node.kind })
    }
  }
  out.push({ ref: 'sys.query', hint: 'sys' }, { ref: 'sys.files', hint: 'sys' })
  for (const name of envNames) {
    if (name) out.push({ ref: `env.${name}`, hint: 'env' })
  }
  return out
}

/**
 * The set of node ids reachable by walking upstream from currentNodeId
 * (BFS over the canvas edges) — the only nodes whose outputs the engine
 * can have produced when this node runs.
 */
export function upstreamNodeIds(
  currentNodeId: string,
  edges: Array<{ source: string; target: string }>,
): Set<string> {
  const upstream = new Map<string, string[]>()
  for (const edge of edges) {
    const list = upstream.get(edge.target) ?? []
    list.push(edge.source)
    upstream.set(edge.target, list)
  }
  const ancestors = new Set<string>()
  const queue = [...(upstream.get(currentNodeId) ?? [])]
  while (queue.length > 0) {
    const id = queue.shift()!
    if (ancestors.has(id)) continue
    ancestors.add(id)
    queue.push(...(upstream.get(id) ?? []))
  }
  return ancestors
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
      return count(params.cases) > 0 ? `${count(params.cases)} cond` : ''
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
    case 'WebSearch': {
      const n = typeof params.max_results === 'number' ? params.max_results : 0
      return n > 0 ? `top ${n}` : ''
    }
    case 'QuestionClassifier':
      return count(params.classes) > 0 ? `${count(params.classes)} classes` : ''
    case 'ParameterExtractor':
      return count(params.parameters) > 0 ? `${count(params.parameters)} params` : ''
    case 'Iteration': {
      const items = typeof params.items === 'string' ? params.items : ''
      return items ? `${items.slice(0, 18)}` : ''
    }
    case 'Agent':
      return typeof params.model === 'string' && params.model
        ? params.model
        : count(params.kb_ids) > 0
          ? 'agent'
          : ''
    case 'MCPTool':
      return typeof params.tool === 'string' && params.tool ? params.tool : ''
    default:
      return ''
  }
}
