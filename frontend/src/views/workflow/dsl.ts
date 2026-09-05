import type { WFComponent, WFEdge, WFNode, WorkflowDSL, WorkflowNodeType } from '@/api/workflow'
import { WORKFLOW_NODE_TYPES } from '@/api/workflow'

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
      return {}
    case 'LLM':
      return { model: '', prompt: '', system_prompt: '', temperature: 0.7, max_tokens: 0 }
    case 'Retrieval':
      return { query: '', kb_ids: [] as string[], top_k: 10 }
    case 'Switch':
      return { value: '', cases: [] as Array<{ value: string; to: string }>, default: '' }
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
    default:
      return {}
  }
}

/**
 * Migrate params saved by older editor builds to the engine contract:
 * camelCase relics from the MVP forms (kbIds / topK / queryPlaceholder)
 * and the Retrieval `query` template that used to be missing. Mutates
 * nothing else — unknown extra keys pass through untouched (the engine
 * ignores params it does not read).
 */
export function migrateNodeParams(kind: WorkflowNodeType, params: Record<string, unknown>): Record<string, unknown> {
  const next = { ...params }
  if (kind === 'Start') {
    delete next.queryPlaceholder
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
