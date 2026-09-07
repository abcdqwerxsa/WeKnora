import type { WFComponent, WFEdge, WFNode, WFPosition, WorkflowDSL, WorkflowNodeType } from '@/api/workflow'
export type { WFNode } from '@/api/workflow'
// Runtime value from the zero-dependency contract module (relative path:
// keeps this file importable from node tests without the request chain).
import { WORKFLOW_NODE_TYPES } from '../../api/workflowContract'

/**
 * DSL dual-view helpers.
 *
 * `graph` (canvas layout) and `components` (execution topology) are two
 * synchronized views of the same workflow. Whichever side is missing is
 * rebuilt from the other; the editor always edits the graph view and
 * regenerates `components` on save.
 */
/**
 * Default params per node kind. Keys MUST match the engine registry
 * verbatim (snake_case) — see api/workflow.ts. Only fields the engine
 * actually reads are seeded; optional numeric knobs stay absent until
 * the user sets them (the engine applies its own defaults).
 */
export function defaultParams(kind: WorkflowNodeType): Record<string, unknown> {
  switch (kind) {
    case 'Start':
      return { fields: [] as unknown[] }
    case 'LLM':
      return { model: '', prompt: '', system_prompt: '', temperature: 0.7, max_tokens: 0 }
    case 'Retrieval':
      return { query: '', kb_ids: [] as string[], top_k: 10 }
    case 'Switch':
      return { cases: [] as unknown[], default: '' }
    case 'Answer':
      return { template: '' }
    case 'Template':
      return { template: '', ops: [] as unknown[] }
    case 'VariableAggregator':
      return { variables: [] as Array<{ name: string; ref: string }> }
    case 'HTTP':
      return { method: 'GET', url: '', headers: {}, body_template: '', timeout_seconds: 30 }
    case 'DataOps':
      return { sql: '', variables: [] as Array<{ name: string; ref: string }> }
    case 'WebSearch':
      return { query: '', provider_id: '', max_results: 5 }
    case 'QuestionClassifier':
      return { query: '', classes: [] as unknown[], default: '' }
    case 'ParameterExtractor':
      return { query: '', parameters: [] as unknown[] }
    default:
      return {}
  }
}

/**
 * Migrate params saved by older editor builds to the engine contract.
 * Switch legacy shape ({value, to} cases + node-level `value` template,
 * pure equality) migrates to condition groups (eq against the node-level
 * template) so the property form always edits the new shape; the engine
 * applies the same migration for DSLs from other clients. Mutates nothing
 * else — unknown extra keys pass through untouched.
 */
export function migrateNodeParams(kind: WorkflowNodeType, params: Record<string, unknown>): Record<string, unknown> {
  const next = { ...params }
  if (kind === 'Start') {
    delete next.queryPlaceholder
    if (!Array.isArray(next.fields)) next.fields = []
  }
  if (kind === 'Retrieval') {
    if (!Array.isArray(next.kb_ids) && Array.isArray(next.kbIds)) {
      next.kb_ids = next.kbIds
    }
    delete next.kbIds
    if (next.top_k === undefined && typeof next.topK === 'number') {
      next.top_k = next.topK
    }
    delete next.topK
  }
  if (kind === 'Switch' && Array.isArray(next.cases)) {
    const nodeValue = typeof next.value === 'string' ? next.value : ''
    next.cases = (next.cases as Array<Record<string, unknown>>).map((entry) => {
      if (Array.isArray(entry.conditions)) return entry
      const legacyValue = typeof entry.value === 'string' ? entry.value : ''
      return {
        conditions: [{ ref: nodeValue, op: 'eq', value: legacyValue }],
        logic: 'and',
        to: typeof entry.to === 'string' ? entry.to : '',
      }
    })
    delete next.value
  }
  return next
}

/** Canvas node id that is unique enough for a single editing session. */
export function makeNodeId(kind: WorkflowNodeType): string {
  const rand = Math.random().toString(36).slice(2, 8)
  return `${kind.toLowerCase()}-${rand}`
}

/** A minimal valid draft DSL: Start → Answer. */
function defaultDsl(): WorkflowDSL {
  const startId = `start-${Math.random().toString(36).slice(2, 8)}`
  const answerId = `answer-${Math.random().toString(36).slice(2, 8)}`
  return {
    version: 1,
    graph: {
      nodes: [
        { id: startId, type: 'Start', position: { x: 80, y: 160 } },
        { id: answerId, type: 'Answer', position: { x: 480, y: 160 } },
      ],
      edges: [{ id: `e-${startId}-${answerId}`, source: startId, target: answerId }],
    },
    components: {},
  }
}

function isNodeType(value: unknown): value is WorkflowNodeType {
  return typeof value === 'string' && (WORKFLOW_NODE_TYPES as string[]).includes(value)
}

/** Deterministic layout when only `components` exists: BFS columns, 200px apart. */
export function layoutComponents(components: Record<string, WFComponent>): { nodes: WFNode[]; edges: WFEdge[] } {
  const depth = new Map<string, number>()
  const resolve = (id: string, guard: Set<string>): number => {
    if (depth.has(id)) return depth.get(id)!
    if (guard.has(id)) return 0
    guard.add(id)
    const comp = components[id]
    const parents = comp ? comp.upstream.filter((u) => components[u]) : []
    const d = parents.length === 0 ? 0 : Math.max(...parents.map((p) => resolve(p, guard))) + 1
    depth.set(id, d)
    return d
  }
  Object.keys(components).forEach((id) => resolve(id, new Set()))

  const columns = new Map<number, WFNode[]>()
  const nodes: WFNode[] = []
  for (const [id, comp] of Object.entries(components)) {
    const kind = comp.obj.component_name as WorkflowNodeType
    const d = depth.get(id) ?? 0
    const col = columns.get(d) ?? []
    const node: WFNode = {
      id,
      type: isNodeType(kind) ? kind : 'Answer',
      position: { x: 80 + d * 200, y: 80 + col.length * 140 },
      data: { params: migrateNodeParams(isNodeType(kind) ? kind : 'Answer', (comp.obj.params as Record<string, unknown>) ?? defaultParams(isNodeType(kind) ? kind : 'Answer')) },
    }
    col.push(node)
    columns.set(d, col)
    nodes.push(node)
  }

  const edges: WFEdge[] = []
  for (const [id, comp] of Object.entries(components)) {
    for (const target of comp.downstream) {
      if (components[target]) {
        edges.push({ id: `e-${id}-${target}`, source: id, target })
      }
    }
  }
  return { nodes, edges }
}

/** Derive `components` from the graph view (edges → upstream/downstream). */
export function componentsFromGraph(nodes: WFNode[], edges: WFEdge[]): Record<string, WFComponent> {
  const components: Record<string, WFComponent> = {}
  for (const node of nodes) {
    const kind = isNodeType(node.type) ? node.type : 'Answer'
    const params = (node.data?.params as Record<string, unknown>) ?? defaultParams(kind)
    components[node.id] = { obj: { component_name: kind, params }, upstream: [], downstream: [] }
  }
  for (const edge of edges) {
    const source = components[edge.source]
    const target = components[edge.target]
    if (!source || !target) continue
    if (!source.downstream.includes(edge.target)) source.downstream.push(edge.target)
    if (!target.upstream.includes(edge.source)) target.upstream.push(edge.source)
  }
  return components
}

/**
 * Normalize a (possibly one-sided) DSL into a full dual-view DSL.
 * Mutates nothing: returns a fresh object.
 */
export function normalizeDsl(input: unknown): WorkflowDSL {
  const dsl = (input ?? {}) as Partial<WorkflowDSL>
  const graphNodes = Array.isArray(dsl.graph?.nodes) ? dsl.graph!.nodes : []
  const graphEdges = Array.isArray(dsl.graph?.edges) ? dsl.graph!.edges : []
  const components = dsl.components && typeof dsl.components === 'object' ? dsl.components : {}

  const graphUsable = graphNodes.length > 0
  const componentsUsable = Object.keys(components).length > 0

  if (graphUsable) {
    const nodes = graphNodes
      .filter((n) => n && typeof n.id === 'string' && isNodeType(n.type))
      .map((n) => ({
        id: n.id,
        type: n.type,
        position: { x: Number(n.position?.x) || 0, y: Number(n.position?.y) || 0 },
        data: { params: migrateNodeParams(n.type, (n.data?.params as Record<string, unknown>) ?? defaultParams(n.type)) },
      }))
    const nodeIds = new Set(nodes.map((n) => n.id))
    const edges = graphEdges
      .filter((e) => e && typeof e.source === 'string' && typeof e.target === 'string' && nodeIds.has(e.source) && nodeIds.has(e.target))
      .map((e) => ({ id: e.id || `e-${e.source}-${e.target}`, source: e.source, target: e.target }))
    return {
      version: 1,
      graph: { nodes, edges },
      components: componentsFromGraph(nodes, edges),
      variables: dsl.variables && typeof dsl.variables === 'object' ? dsl.variables : {},
    }
  }

  if (componentsUsable) {
    const laid = layoutComponents(components)
    return {
      version: 1,
      graph: laid,
      components: componentsFromGraph(laid.nodes, laid.edges),
      variables: dsl.variables && typeof dsl.variables === 'object' ? dsl.variables : {},
    }
  }

  return defaultDsl()
}

/** Build the DSL to persist from the current canvas state. */
export function buildDsl(
  nodes: WFNode[],
  edges: WFEdge[],
  variables?: Record<string, unknown>,
): WorkflowDSL {
  return {
    version: 1,
    graph: { nodes, edges },
    components: componentsFromGraph(nodes, edges),
    variables: variables ?? {},
  }
}

// ---------------------------------------------------------------------------
// Auto layout (port of the engine's defaultLayout: BFS depth columns).
// ---------------------------------------------------------------------------

/**
 * Layered auto layout: depth = longest distance from an entry node
 * (mirrors internal/agent/workflow/dsl.go defaultLayout). Returns new
 * positions per node id; callers assign them onto the canvas.
 */
export function autoLayout(nodes: WFNode[], edges: WFEdge[]): Record<string, WFPosition> {
  const downstream = new Map<string, string[]>()
  const upstream = new Map<string, string[]>()
  for (const id of nodes.map((n) => n.id)) {
    downstream.set(id, [])
    upstream.set(id, [])
  }
  for (const edge of edges) {
    downstream.get(edge.source)?.push(edge.target)
    upstream.get(edge.target)?.push(edge.source)
  }

  const depth = new Map<string, number>()
  const resolve = (id: string, guard: Set<string>): number => {
    const known = depth.get(id)
    if (known !== undefined) return known
    if (guard.has(id)) return 0 // cycle: pin to current guard depth
    guard.add(id)
    const parents = (upstream.get(id) ?? []).filter((u) => downstream.has(u))
    const d = parents.length === 0 ? 0 : Math.max(...parents.map((p) => resolve(p, guard))) + 1
    depth.set(id, d)
    return d
  }
  for (const node of nodes) resolve(node.id, new Set())

  const columnCount = new Map<number, number>()
  const positions: Record<string, WFPosition> = {}
  for (const node of nodes) {
    const d = depth.get(node.id) ?? 0
    const row = columnCount.get(d) ?? 0
    columnCount.set(d, row + 1)
    positions[node.id] = { x: 80 + d * 260, y: 80 + row * 160 }
  }
  return positions
}

// ---------------------------------------------------------------------------
// Pre-save validation (mirrors the engine's compile constraints + adds
// editor-level warnings the engine deliberately ignores).
// ---------------------------------------------------------------------------

export interface GraphIssue {
  level: 'error' | 'warning'
  /** i18n key under workflow.editor.issues.* */
  key: string
  /** Interpolation values for the i18n message. */
  values?: Record<string, string | number>
  nodeId?: string
}

/** `{nodeId@param}` reference pattern used inside prompt/template params. */
export const NODE_REF_PATTERN = /\{([a-zA-Z0-9_-]+)@([a-zA-Z0-9_.-]+)\}/g

/** Collect every {node@param} ref reachable from a params object. */
function collectRefs(params: Record<string, unknown>): Array<{ node: string; param: string }> {
  const refs: Array<{ node: string; param: string }> = []
  const walk = (value: unknown): void => {
    if (typeof value === 'string') {
      for (const match of value.matchAll(NODE_REF_PATTERN)) {
        refs.push({ node: match[1], param: match[2] })
      }
      return
    }
    if (Array.isArray(value)) {
      value.forEach(walk)
      return
    }
    if (value && typeof value === 'object') {
      Object.values(value).forEach(walk)
    }
  }
  // Every string anywhere inside params may carry refs (documented keys
  // like prompt/template plus nested headers/ops/cases).
  for (const value of Object.values(params)) walk(value)
  return refs
}

/**
 * Validate the canvas before save/run. Errors mirror engine compile
 * constraints (the save is rejected server-side anyway); warnings cover
 * editor-observable rot the engine ignores (unreachable nodes, stale
 * {node@param} references).
 */
export function validateGraph(nodes: WFNode[], edges: WFEdge[]): GraphIssue[] {
  const issues: GraphIssue[] = []
  const ids = new Set(nodes.map((n) => n.id))

  const targeted = new Set(edges.map((e) => e.target))
  const entries = nodes.filter((n) => !targeted.has(n.id))
  const terminals = nodes.filter((n) => !edges.some((e) => e.source === n.id))

  if (entries.length === 0) issues.push({ level: 'error', key: 'noEntry' })
  if (entries.length > 1) {
    issues.push({ level: 'error', key: 'multipleEntries', values: { count: entries.length, names: entries.map((n) => n.id).join(', ') } })
  }
  if (terminals.length === 0) issues.push({ level: 'error', key: 'noTerminal' })
  if (terminals.length > 1) {
    issues.push({ level: 'error', key: 'multipleTerminals', values: { count: terminals.length, names: terminals.map((n) => n.id).join(', ') } })
  }

  // Reachability from the entry set (BFS over edges).
  if (entries.length > 0) {
    const reachable = new Set<string>()
    const queue = entries.map((n) => n.id)
    while (queue.length > 0) {
      const id = queue.shift()!
      if (reachable.has(id)) continue
      reachable.add(id)
      for (const edge of edges) {
        if (edge.source === id && !reachable.has(edge.target)) queue.push(edge.target)
      }
    }
    for (const node of nodes) {
      if (!reachable.has(node.id)) {
        issues.push({ level: 'warning', key: 'unreachable', values: { name: node.id }, nodeId: node.id })
      }
    }
  }

  // Stale {node@param} references in template params.
  for (const node of nodes) {
    const params = (node.data?.params as Record<string, unknown>) ?? {}
    for (const ref of collectRefs(params)) {
      if (!ids.has(ref.node)) {
        issues.push({ level: 'warning', key: 'staleRef', values: { ref: `${ref.node}@${ref.param}`, name: node.id }, nodeId: node.id })
      }
    }
  }

  return issues
}
