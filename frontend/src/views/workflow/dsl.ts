import type { WFComponent, WFEdge, WFNode, WFPosition, WorkflowDSL, WorkflowNodeType } from '@/api/workflow'
export type { WFNode } from '@/api/workflow'
// Runtime value from the zero-dependency contract module (relative path:
// keeps this file importable from node tests without the request chain).
import { WORKFLOW_NODE_TYPES } from '../../api/workflowContract'

/*
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
/** Canvas annotation nodes (Dify-style sticky notes): purely presentational,
 * excluded from `components` — the backend keeps them in the graph view. */
export const NOTE_NODE_TYPE = 'wf-note'

export function isNoteNode(node: { type?: string }): boolean {
  return node.type === NOTE_NODE_TYPE
}

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
    case 'Code':
      return { language: 'python3', code: '', variables: [] as Array<{ name: string; ref: string }>, timeout_seconds: 30 }
    case 'Iteration':
      return { items: '', item_var: 'item', index_var: 'index', output_ref: '', output_var: 'results' }
    case 'Agent':
      return { prompt: '', system_prompt: '', model: '', kb_ids: [] as string[], temperature: 0.4, agent_id: '' }
    case 'MCPTool':
      return { service_id: '', tool: '', args: '', timeout_seconds: 30 }
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
    const safeKind = isNodeType(kind) ? kind : 'Answer'
    const d = depth.get(id) ?? 0
    const col = columns.get(d) ?? []
    const node: WFNode = {
      id,
      type: safeKind,
      position: { x: 80 + d * 200, y: 80 + col.length * 140 },
      data: comp.parent
        ? { params: migrateNodeParams(safeKind, (comp.obj.params as Record<string, unknown>) ?? defaultParams(safeKind)), parent: comp.parent }
        : { params: migrateNodeParams(safeKind, (comp.obj.params as Record<string, unknown>) ?? defaultParams(safeKind)) },
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
  const realNodes = nodes.filter((n) => !isNoteNode(n))
  const noteIds = new Set(nodes.filter(isNoteNode).map((n) => n.id))
  for (const node of realNodes) {
    const kind = isNodeType(node.type) ? node.type : 'Answer'
    const params = (node.data?.params as Record<string, unknown>) ?? defaultParams(kind)
    const parent = typeof node.data?.parent === 'string' ? (node.data.parent as string) : ''
    components[node.id] = { obj: { component_name: kind, params }, upstream: [], downstream: [], parent }
  }
  for (const edge of edges) {
    if (noteIds.has(edge.source) || noteIds.has(edge.target)) continue
    const source = components[edge.source]
    const target = components[edge.target]
    if (!source || !target) continue
    if (!source.downstream.includes(edge.target)) source.downstream.push(edge.target)
    if (!target.upstream.includes(edge.source)) target.upstream.push(edge.source)
  }
  // Floating (unwired) nodes must NOT reach the execution view: the engine
  // compiles components and every node with empty upstream counts as an
  // entry, so a stray node would fail the run with "multiple entries".
  // Isolated = no incoming AND no outgoing edges AND not an iteration body.
  // If that exclusion would empty the view, keep everything (still saves).
  const isolated = Object.keys(components).filter((id) => {
    const comp = components[id]
    return comp.upstream.length === 0 && comp.downstream.length === 0 && !comp.parent
  })
  if (isolated.length > 0 && isolated.length < Object.keys(components).length) {
    for (const id of isolated) delete components[id]
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
  const variables = dsl.variables && typeof dsl.variables === 'object' ? dsl.variables : {}

  if (graphUsable) {
    const realNodes = graphNodes
      .filter((n) => n && typeof n.id === 'string' && isNodeType(n.type))
      .map((n) => ({
        id: n.id,
        type: n.type,
        position: { x: Number(n.position?.x) || 0, y: Number(n.position?.y) || 0 },
        data: {
          params: migrateNodeParams(n.type, (n.data?.params as Record<string, unknown>) ?? defaultParams(n.type)),
          ...(typeof (n.data as Record<string, unknown> | undefined)?.parent === 'string' ? { parent: (n.data as Record<string, unknown>).parent } : {}),
        },
      }))
    // Annotations ride along in the graph view only (no component twins).
    const noteNodes = graphNodes
      .filter((n) => n && typeof n.id === 'string' && isNoteNode(n))
      .map((n) => ({
        id: n.id,
        type: NOTE_NODE_TYPE as WorkflowNodeType,
        position: { x: Number(n.position?.x) || 0, y: Number(n.position?.y) || 0 },
        data: { text: typeof (n.data as Record<string, unknown> | undefined)?.text === 'string' ? (n.data as Record<string, unknown>).text : '' },
      }))
    const nodes = [...realNodes, ...noteNodes]
    const nodeIds = new Set(nodes.map((n) => n.id))
    const edges = graphEdges
      .filter((e) => e && typeof e.source === 'string' && typeof e.target === 'string' && nodeIds.has(e.source) && nodeIds.has(e.target))
      .map((e) => ({
        id: e.id || `e-${e.source}-${e.target}`,
        source: e.source,
        target: e.target,
        // Branch identity (routing nodes) rides on the edge — the canvas
        // renders branch handles from it; the engine ignores it.
        ...(e.sourceHandle ? { sourceHandle: e.sourceHandle } : {}),
      }))
    return {
      version: 1,
      graph: { nodes, edges },
      components: componentsFromGraph(nodes, edges),
      variables,
    }
  }

  if (componentsUsable) {
    const laid = layoutComponents(components)
    return {
      version: 1,
      graph: laid,
      components: componentsFromGraph(laid.nodes, laid.edges),
      variables,
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
  // An entry is an untargeted node that actually STARTS a flow (has an
  // outgoing edge). A floating node (no edges at all) is not an entry —
  // it is already covered by the `unreachable` warning below.
  const entries = nodes.filter((n) => !targeted.has(n.id) && edges.some((e) => e.source === n.id))
  const terminals = nodes.filter((n) => !edges.some((e) => e.source === n.id))

  if (entries.length === 0) issues.push({ level: 'error', key: 'noEntry' })
  if (entries.length > 1) {
    issues.push({ level: 'error', key: 'multipleEntries', values: { count: entries.length, names: entries.map((n) => n.id).join(', ') } })
  }
  if (terminals.length === 0) issues.push({ level: 'error', key: 'noTerminal' })
  // Multiple terminals are legal (parallel branches need not converge).
  // One terminal answer still wins the run result — first to complete.

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

  // Iteration body membership: parents must target Iteration nodes; each
  // body needs exactly one entry (a body node not targeted by any edge).
  const parentOf = (node: WFNode): string =>
    typeof (node.data as Record<string, unknown> | undefined)?.parent === 'string'
      ? ((node.data as Record<string, unknown>).parent as string)
      : ''
  const iterationIds = new Set(nodes.filter((n) => n.type === 'Iteration').map((n) => n.id))
  const bodies = new Map<string, WFNode[]>()
  for (const node of nodes) {
    const parent = parentOf(node)
    if (!parent) continue
    if (!iterationIds.has(parent)) {
      issues.push({ level: 'error', key: 'badParent', values: { name: node.id, parent }, nodeId: node.id })
      continue
    }
    const body = bodies.get(parent) ?? []
    body.push(node)
    bodies.set(parent, body)
  }
  for (const [iterID, body] of bodies) {
    const bodyIds = new Set(body.map((n) => n.id))
    const bodyTargeted = new Set(edges.filter((e) => bodyIds.has(e.source) && bodyIds.has(e.target)).map((e) => e.target))
    const entries = body.filter((n) => !bodyTargeted.has(n.id))
    if (entries.length !== 1) {
      issues.push({ level: 'error', key: 'bodyEntries', values: { name: iterID, count: entries.length } })
    }
  }
  for (const iterID of iterationIds) {
    if (!bodies.has(iterID)) {
      issues.push({ level: 'error', key: 'emptyBody', values: { name: iterID }, nodeId: iterID })
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
