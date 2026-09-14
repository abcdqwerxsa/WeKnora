import assert from 'node:assert/strict'
import test from 'node:test'

import { outputDeclsOf, outputParamsOf } from './nodeMeta.ts'

// The declaration registry must mirror the engine's Invoke return shapes
// (regression lock for the two mismatches a review caught: VariableAggregator
// returns {values, collected}, Code's outputs are script-defined).
test('outputDeclsOf mirrors engine output shapes', () => {
  assert.deepEqual(outputParamsOf('VariableAggregator'), ['values', 'collected'])
  const agg = outputDeclsOf('VariableAggregator')
  assert.equal(agg[0].type, 'object')
  assert.match(agg[0].desc ?? '', /values\./)

  assert.deepEqual(outputDeclsOf('Code'), [])
  assert.deepEqual(outputParamsOf('Retrieval'), ['chunks', 'doc_aggs'])
  assert.deepEqual(outputParamsOf('Agent'), ['answer'])

  // Start: query + files + declared fields (typed).
  const start = outputDeclsOf('Start', {
    fields: [
      { name: 'topic', type: 'text', default: '' },
      { name: 'limit', type: 'number', default: '' },
      { name: '', type: 'text', default: '' },
    ],
  })
  assert.deepEqual(start.map((d) => d.name), ['query', 'files', 'topic', 'limit'])
  assert.equal(start[3].type, 'number')

  // ParameterExtractor: declared names, order preserved.
  assert.deepEqual(
    outputParamsOf('ParameterExtractor', { parameters: [{ name: 'city' }, { name: 'days' }] }),
    ['city', 'days'],
  )
})
