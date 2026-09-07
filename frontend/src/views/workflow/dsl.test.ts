import assert from 'node:assert/strict'
import test from 'node:test'

import { autoLayout, validateGraph } from './dsl.ts'
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
  // b, c, x, y all lack downstream edges → multiple terminals
  assert.ok(keys.includes('multipleTerminals'))
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
