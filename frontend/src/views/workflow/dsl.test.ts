import assert from 'node:assert/strict'
import test from 'node:test'

import { autoLayout, validateGraph, normalizeDsl, buildDsl } from './dsl.ts'
import type { WFNode } from './dsl.ts'

function node(id: string, kind: WFNode['type'], params?: Record<string, unknown>): WFNode {
  return { id, type: kind, position: { x: 0, y: 0 }, data: params ? { params } : undefined }
}

test('validateGraph accepts a clean linear graph', () => {
  const issues = validateGraph(
    [node('start', 'Start'), node('llm', 'LLM', { prompt: '{start@query}' }), node('ans', 'Answer')],
    [
      { id: 'e1', source: 'start', target: 'llm' },
      { id: 'e2', source: 'llm', target: 'ans' },
    ],
  )
  assert.deepEqual(issues, [])
})

test('validateGraph flags multiple entries, multiple terminals, stale refs and unreachable cycles', () => {
  // x↔y is a disconnected cycle: every node in it is targeted (so not an
  // "entry") yet unreachable from the real entries a/b.
  const issues = validateGraph(
    [
      node('a', 'Start'),
      node('b', 'Start'),
      node('c', 'LLM', { prompt: '{ghost@content}' }),
      node('x', 'LLM'),
      node('y', 'Template'),
    ],
    [
      { id: 'e1', source: 'a', target: 'c' },
      { id: 'e2', source: 'x', target: 'y' },
      { id: 'e3', source: 'y', target: 'x' },
    ],
  )
  const keys = issues.map((i) => i.key)
  assert.ok(keys.includes('multipleEntries'))
  // Multiple terminals are legal now (parallel fan-out) — must NOT be flagged.
  assert.ok(!keys.includes('multipleTerminals'))
  // b, c, x, y all lack downstream edges → no noTerminal (each is a terminal)
  assert.ok(!keys.includes('noTerminal'))
  assert.deepEqual(
    issues.filter((i) => i.key === 'unreachable').map((i) => i.nodeId),
    ['x', 'y'],
  )
  assert.ok(keys.includes('staleRef'))
})

test('validateGraph reports no entry and no terminal on a pure cycle', () => {
  // Every node in a↔b is targeted (no entry) and has downstream (no
  // terminal) — exactly the engine-rejected topology.
  const issues = validateGraph(
    [node('a', 'LLM'), node('b', 'LLM')],
    [
      { id: 'e1', source: 'a', target: 'b' },
      { id: 'e2', source: 'b', target: 'a' },
    ],
  )
  const keys = issues.map((i) => i.key)
  assert.ok(keys.includes('noEntry'))
  assert.ok(keys.includes('noTerminal'))
})

test('autoLayout layers by longest distance from the entry', () => {
  const nodes = [node('start', 'Start'), node('mid', 'LLM'), node('end', 'Answer'), node('end2', 'Template')]
  const edges = [
    { id: 'e1', source: 'start', target: 'mid' },
    { id: 'e2', source: 'mid', target: 'end' },
    { id: 'e3', source: 'start', target: 'end2' },
  ]
  const pos = autoLayout(nodes, edges)
  assert.equal(pos.start.x, 80)
  assert.equal(pos.mid.x, 340) // start + 1 column
  assert.equal(pos.end2.x, 340) // direct edge from start → same column as mid
  assert.equal(pos.end.x, 600) // longest path start→mid→end → deepest column
  assert.equal(pos.start.y, 80) // first row
  assert.ok(pos.end2.y > pos.start.y) // column 1 stacks mid then end2 below start's row
})

test('autoLayout tolerates cycles without hanging', () => {
  const nodes = [node('a', 'LLM'), node('b', 'LLM')]
  const edges = [
    { id: 'e1', source: 'a', target: 'b' },
    { id: 'e2', source: 'b', target: 'a' },
  ]
  const pos = autoLayout(nodes, edges)
  assert.ok(Number.isFinite(pos.a.x) && Number.isFinite(pos.b.x))
})

test('migrateNodeParams converts legacy Switch equality cases to condition groups', async () => {
  const { migrateNodeParams } = await import('./dsl.ts')
  const migrated = migrateNodeParams('Switch', {
    value: '{sys.lang}',
    cases: [
      { value: 'go', to: 'a' },
      { value: 'py', to: 'b' },
    ],
    default: 'b',
  })
  assert.equal((migrated as Record<string, unknown>).value, undefined)
  const cases = migrated.cases as Array<{ conditions: Array<{ ref: string; op: string; value: string }>; logic: string; to: string }>
  assert.equal(cases.length, 2)
  assert.deepEqual(cases[0].conditions, [{ ref: '{sys.lang}', op: 'eq', value: 'go' }])
  assert.equal(cases[0].logic, 'and')
  assert.equal(cases[0].to, 'a')
  // New-style cases pass through untouched.
  const kept = migrateNodeParams('Switch', {
    cases: [{ conditions: [{ ref: '{x@y}', op: 'contains', value: 'z' }], logic: 'or', to: 'c' }],
  })
  assert.deepEqual((kept.cases as unknown[])[0], {
    conditions: [{ ref: '{x@y}', op: 'contains', value: 'z' }],
    logic: 'or',
    to: 'c',
  })
})

test('normalizeDsl preserves iteration body membership (parent) round-trip', async () => {
  const { normalizeDsl } = await import('./dsl.ts')
  const dsl = normalizeDsl({
    version: 1,
    graph: {
      nodes: [
        { id: 'start', type: 'Start', position: { x: 0, y: 0 } },
        { id: 'iter1', type: 'Iteration', position: { x: 1, y: 1 }, data: { params: { items: '[1]' } } },
        { id: 'body1', type: 'Template', position: { x: 2, y: 2 }, data: { params: { template: '{iter1@item}' }, parent: 'iter1' } },
      ],
      edges: [
        { id: 'e1', source: 'start', target: 'iter1' },
      ],
    },
    components: {},
  })
  assert.equal(dsl.components.body1.parent, 'iter1')
  // Round-trip: normalize again from the built graph view
  const again = normalizeDsl(dsl)
  assert.equal(again.components.body1.parent, 'iter1')
})

test('validateGraph checks iteration body membership', async () => {
  const { validateGraph } = await import('./dsl.ts')
  const iter = (tpl: { id: string; parent?: string }) => ({
    id: tpl.id,
    type: 'Template' as const,
    position: { x: 0, y: 0 },
    data: tpl.parent ? { params: { template: 'x' }, parent: tpl.parent } : { params: { template: 'x' } },
  })

  // 1. Iteration without body → emptyBody error.
  let issues = validateGraph(
    [node('start', 'Start'), node('iter1', 'Iteration', { items: '[1]', output_ref: '{tpl@text}' }), iter({ id: 'tpl' })],
    [
      { id: 'e1', source: 'start', target: 'iter1' },
      { id: 'e2', source: 'iter1', target: 'tpl' },
    ],
  )
  let keys = issues.map((i) => `${i.level}:${i.key}`)
  assert.ok(keys.includes('error:emptyBody'), keys.join(','))

  // 2. tpl joins iter1's body → single body entry, no body errors.
  issues = validateGraph(
    [node('start', 'Start'), node('iter1', 'Iteration', { items: '[1]', output_ref: '{tpl@text}' }), iter({ id: 'tpl', parent: 'iter1' })],
    [
      { id: 'e1', source: 'start', target: 'iter1' },
      { id: 'e2', source: 'iter1', target: 'tpl' },
    ],
  )
  keys = issues.map((i) => `${i.level}:${i.key}`)
  assert.ok(!keys.includes('error:bodyEntries'), keys.join(','))
  assert.ok(!keys.includes('error:emptyBody'), keys.join(','))

  // 3. Bad parent → error.
  issues = validateGraph(
    [node('start', 'Start'), node('iter1', 'Iteration', { items: '[1]', output_ref: '{tpl@text}' }), iter({ id: 'tpl', parent: 'nope' })],
    [
      { id: 'e1', source: 'start', target: 'iter1' },
      { id: 'e2', source: 'iter1', target: 'tpl' },
    ],
  )
  keys = issues.map((i) => `${i.level}:${i.key}`)
  assert.ok(keys.includes('error:badParent'), keys.join(','))

  // 4. Two body nodes chained → single entry ok; two disconnected → bodyEntries error.
  issues = validateGraph(
    [
      node('start', 'Start'),
      node('iter1', 'Iteration', { items: '[1]', output_ref: '{b2@text}' }),
      iter({ id: 'b1', parent: 'iter1' }),
      iter({ id: 'b2', parent: 'iter1' }),
    ],
    [
      { id: 'e1', source: 'start', target: 'iter1' },
      { id: 'e2', source: 'iter1', target: 'b1' },
      { id: 'e3', source: 'b1', target: 'b2' },
    ],
  )
  keys = issues.map((i) => `${i.level}:${i.key}`)
  assert.ok(!keys.includes('error:bodyEntries'), keys.join(','))
  issues = validateGraph(
    [
      node('start', 'Start'),
      node('iter1', 'Iteration', { items: '[1]', output_ref: '{b2@text}' }),
      iter({ id: 'b1', parent: 'iter1' }),
      iter({ id: 'b2', parent: 'iter1' }),
    ],
    [{ id: 'e1', source: 'start', target: 'iter1' }],
  )
  keys = issues.map((i) => `${i.level}:${i.key}`)
  assert.ok(keys.includes('error:bodyEntries'), keys.join(','))
})

test('annotations ride in the graph view and never become components', () => {
  const input = {
    version: 1,
    graph: {
      nodes: [
        node('start', 'Start'),
        node('ans', 'Answer'),
        { id: 'note-1', type: 'wf-note', position: { x: 10, y: 10 }, data: { text: 'hello' } },
      ],
      edges: [],
    },
    components: {
      start: { obj: { component_name: 'Start', params: { fields: [] } }, upstream: [], downstream: ['ans'] },
      ans: { obj: { component_name: 'Answer', params: { template: '{start@query}' } }, upstream: ['start'], downstream: [] },
    },
  }
  const out = normalizeDsl(input)
  const note = out.graph?.nodes.find((n) => n.id === 'note-1')
  assert.ok(note, 'note survives normalizeDsl')
  assert.strictEqual((note!.data as Record<string, unknown>).text, 'hello')
  const built = buildDsl(out.graph!.nodes, out.graph!.edges)
  assert.strictEqual(built.components['note-1'], undefined, 'notes must not become components')
  assert.ok(built.graph?.nodes.some((n) => n.id === 'note-1'))
})
