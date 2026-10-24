import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
const source = await readFile(new URL('../src/utils/organizeNaming.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { applyOrganizeMapping } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
async function importUtility(name) {
  const source = await readFile(new URL(`../src/utils/${name}.ts`, import.meta.url), 'utf8')
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  return import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
}
const { matchesResourceSpec, resourceSpecOptions, resourceChineseLanguage } = await importUtility('resourceSpecFilters')
const row = (id, episode, endEpisode = '') => ({ id, name: `file${id}.mkv`, selected: true, season: 1, episode, endEpisode, suffix: 'Group' })

test('specification filters partition Chinese, combine groups and retain unknown rows when no filter is active', () => {
  const empty = { resolution: [], language: [], external: false }
  const spec = { resolution: '1080p', languages: ['ja', 'zh-Hans', 'zh-Hant'], subtitleMode: 'external' }
  assert.equal(resourceChineseLanguage(spec), 'both')
  assert.equal(resourceChineseLanguage({ languages: ['ja', 'zh-Hans'] }), 'simplified')
  assert.equal(resourceChineseLanguage({ languages: ['ja', 'zh-Hant'] }), 'traditional')
  assert.equal(resourceChineseLanguage({ languages: ['ja'] }), '')
  assert.ok(matchesResourceSpec(spec, empty))
  assert.ok(matchesResourceSpec(undefined, empty))
  assert.ok(matchesResourceSpec(spec, { resolution: ['720p', '1080p'], language: ['simplified', 'both'], external: true }))
  assert.equal(matchesResourceSpec(spec, { ...empty, language: ['simplified'] }), false)
  assert.equal(matchesResourceSpec(spec, { ...empty, resolution: ['720p'], external: true }), false)
  assert.equal(matchesResourceSpec(undefined, { ...empty, external: true }), false)
  assert.equal(matchesResourceSpec({ subtitleMode: 'soft' }, { ...empty, external: true }), false)
  assert.deepEqual(resourceSpecOptions([undefined, {}]), [])
  assert.deepEqual(resourceSpecOptions([spec, spec]).map(option => option.label), ['1080P', '简繁', '外挂字幕'])
})

test('offset preserves publisher gaps and joined ranges; explicit sequential mapping preserves width', () => {
  const rows = [row('a', 27, 28), row('b', 30), row('unknown', 0)]
  const sources = { a: { episode: 27, endEpisode: 28 }, b: { episode: 30, endEpisode: 0 }, unknown: { episode: 0, endEpisode: 0 } }
  const undo = applyOrganizeMapping(rows, sources, 2, 27, 1)
  assert.deepEqual(rows.map(r => [r.season, r.episode, r.endEpisode]), [[2, 1, 2], [2, 4, ''], [1, 0, '']])
  assert.deepEqual(undo.map(r => r.episode), [27, 30])
  rows[1].selected = false
  applyOrganizeMapping(rows, sources, 1, 1, 5, true)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[5, 6], [4, ''], [7, '']])
})

test('invalid bulk action changes no rows, repeated offset uses source numbers and manual edits outside selection survive', () => {
  const rows = [row('a', 1), row('b', 3)]
  const sources = { a: { episode: 1, endEpisode: 0 }, b: { episode: 3, endEpisode: 0 } }
  const before = structuredClone(rows)
  assert.throws(() => applyOrganizeMapping(rows, sources, 2, 3, 1))
  assert.deepEqual(rows, before)
  rows[1].selected = false; rows[1].episode = 99
  applyOrganizeMapping(rows, sources, 2, 1, 10); applyOrganizeMapping(rows, sources, 2, 1, 10)
  assert.equal(rows[0].episode, 10); assert.equal(rows[1].episode, 99)
})

test('joined files retain starts 01 and 03, and clearing the end names only the starting episode', () => {
  const rows = [row('a', 1, 2), row('b', 3, 4)]
  const sources = { a: { episode: 1, endEpisode: 2 }, b: { episode: 3, endEpisode: 4 } }
  applyOrganizeMapping(rows, sources, 1, 1, 1, true)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[1, 2], [3, 4]])
  rows[0].endEpisode = ''; rows[1].endEpisode = ''
  applyOrganizeMapping(rows, sources, 1, 1, 1, true)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[1, ''], [2, '']])
  applyOrganizeMapping(rows, sources, 1, 1, 1)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[1, 2], [3, 4]])
})

test('inverted, fractional or overflowing joined spans fail without modifying selected rows', () => {
  for (const [episode, endEpisode] of [[3, 2], [1, 2.5], [1, 10001], [0, 2]]) {
    const rows = [row('valid', 1, 2), row('bad', episode, endEpisode)]
    const before = structuredClone(rows)
    assert.throws(() => applyOrganizeMapping(rows, {}, 1, 1, 1, true))
    assert.deepEqual(rows, before)
  }
})
