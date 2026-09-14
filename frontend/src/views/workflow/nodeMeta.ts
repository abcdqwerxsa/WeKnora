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

/** One declared output of a node kind: the single source of truth for
 * what a node emits (mirrors the engine's Invoke return shape). Type is
 * informational — the engine's runtime values always win. */
export interface OutputDecl {
  name: string
  type: 'string' | 'number' | 'boolean' | 'object' | 'array' | 'any'
  desc?: string
}

const DECL = (name: string, type: OutputDecl['type'], desc?: string): OutputDecl => ({ name, type, desc })

/** Declared outputs per node kind (params resolves dynamic names/types). */
export function outputDeclsOf(kind: WorkflowNodeType, params?: Record<string, unknown>): OutputDecl[] {
  switch (kind) {
    case 'Start': {
      // Declared form fields are materialised into Start outputs by name.
      const fields = params?.fields
      const dynamic = Array.isArray(fields)
        ? (fields as Array<{ name?: unknown; type?: unknown }>).filter((f) => String(f?.name ?? '')).map((f) => {
            const t = typeof f?.type === 'string' ? f.type : 'text'
            const declType: OutputDecl['type'] = t === 'number' ? 'number' : 'string'
            return DECL(String(f.name), declType)
          })
        : []
      return [DECL('query', 'string'), DECL('files', 'array', 'run attachment ids'), ...dynamic]
    }
    case 'LLM':
      return [DECL('content', 'string', 'generated text')]
    case 'Retrieval':
      return [DECL('chunks', 'array', 'retrieved chunk objects (index with .0, .1, …)'), DECL('doc_aggs', 'array', 'per-document aggregates')]
    case 'Template':
      return [DECL('text', 'string')]
    case 'Answer':
      return [DECL('answer', 'string')]
    case 'HTTP':
      return [DECL('status_code', 'number'), DECL('body', 'string'), DECL('headers', 'object')]
    case 'DataOps':
      return [DECL('columns', 'array', 'column names'), DECL('rows', 'array', 'row objects'), DECL('row_count', 'number')]
    case 'Code':
      // Script-defined: the keys of the last printed JSON object become
      // outputs verbatim — nothing static to declare.
      return []
    case 'WebSearch':
      return [DECL('results', 'array', 'search result objects'), DECL('result_count', 'number')]
    case 'QuestionClassifier':
      return [DECL('class', 'string', 'matched class name')]
    case 'ParameterExtractor': {
      const decls = params?.parameters
      return Array.isArray(decls)
        ? (decls as Array<{ name?: unknown }>).filter((p) => String(p?.name ?? '')).map((p) => DECL(String(p.name), 'any'))
        : []
    }
    case 'Iteration': {
      const vars = params
      const out = typeof vars?.output_var === 'string' && vars.output_var ? vars.output_var : 'results'
      return [DECL(out, 'array', 'collected per-item outputs'), DECL('count', 'number')]
    }
    case 'Agent':
      return [DECL('answer', 'string')]
    case 'MCPTool':
      return [DECL('result', 'any', 'raw MCP content'), DECL('result_text', 'string', 'textual rendering')]
    case 'VariableAggregator': {
      // The engine returns {values: map, collected: n} — members are
      // addressed as values.<name> (see template_test / e2e notes).
      return [DECL('values', 'object', 'address members as values.<name>'), DECL('collected', 'number')]
    }
    default:
      return []
  }
}

/** Upstream output params a reference picker may offer for a node kind. */
export function outputParamsOf(kind: WorkflowNodeType, params?: Record<string, unknown>): string[] {
  return outputDeclsOf(kind, params).map((d) => d.name)
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
