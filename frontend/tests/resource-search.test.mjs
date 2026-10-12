import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const module = { exports: {} }
const code = ts.transpileModule(readFileSync(new URL('../src/types/resourceSearch.ts', import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
new Function('module', 'exports', code)(module, module.exports)
const { mergeResourcePage } = module.exports
const item = (id, status = 'ready') => ({ id, title: id, links: status === 'ready' ? [{ url: 'https://115.com/s/' + id }] : [], detailStatus: status })
const first = () => ({ query: 'film', matchedBy: 'title', page: 1, nextPage: 2, hasMore: true, items: [item('a'), item('b')], attempts: [{ method: 'title', query: 'film', total: 2, usable: 2 }], cached: false })
test('pagination preserves stable site IDs, replaces successful duplicates and retains good details', () => {
  const previous = first()
  const next = { ...first(), page: 2, nextPage: 3, items: [item('b', 'failed'), item('c')], partial: true, failedDetails: 1 }
  const merged = mergeResourcePage(previous, next)
  assert.deepEqual(merged.items.map(item => item.id), ['a', 'b', 'c'])
  assert.equal(merged.items[1].links.length, 1)
  assert.equal(merged.partial, true)
  assert.equal(merged.nextPage, 3)
  assert.equal(previous.attempts[0].total, 2)
})
test('pagination rejects changed query, changed method and nonconsecutive pages', () => {
  for (const change of [{ query: 'other' }, { matchedBy: 'original' }, { page: 3 }]) assert.throws(() => mergeResourcePage(first(), { ...first(), page: 2, ...change }), /分页/)
})
