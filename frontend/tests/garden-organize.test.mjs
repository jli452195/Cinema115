import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
const source = await readFile(new URL('../src/utils/gardenOrganize.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { applyGardenMapping, gardenTargetName } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const row = (id, episode, endEpisode = '') => ({ id, name: `file${id}.mkv`, selected: true, season: 1, episode, endEpisode, suffix: 'Group' })

test('offset preserves publisher gaps and joined ranges; explicit sequential mapping preserves width', () => {
  const rows = [row('a', 27, 28), row('b', 30), row('unknown', 0)]
  const sources = { a: { episode: 27, endEpisode: 28 }, b: { episode: 30, endEpisode: 0 }, unknown: { episode: 0, endEpisode: 0 } }
  const undo = applyGardenMapping(rows, sources, 2, 27, 1)
  assert.deepEqual(rows.map(r => [r.season, r.episode, r.endEpisode]), [[2, 1, 2], [2, 4, ''], [1, 0, '']])
  assert.deepEqual(undo.map(r => r.episode), [27, 30])
  rows[1].selected = false
  applyGardenMapping(rows, sources, 1, 1, 5, true)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[5, 6], [4, ''], [7, '']])
})

test('invalid bulk action changes no rows, repeated offset uses source numbers and manual edits outside selection survive', () => {
  const rows = [row('a', 1), row('b', 3)]
  const sources = { a: { episode: 1, endEpisode: 0 }, b: { episode: 3, endEpisode: 0 } }
  const before = structuredClone(rows)
  assert.throws(() => applyGardenMapping(rows, sources, 2, 3, 1))
  assert.deepEqual(rows, before)
  rows[1].selected = false; rows[1].episode = 99
  applyGardenMapping(rows, sources, 2, 1, 10); applyGardenMapping(rows, sources, 2, 1, 10)
  assert.equal(rows[0].episode, 10); assert.equal(rows[1].episode, 99)
})

test('target preview preserves range, file extension and group, unknown remains unconfirmed', () => {
  assert.equal(gardenTargetName(row('a', 1, 2), '测试作品', '2024-01-01', 'tv'), '测试作品 - S01E01-E02 - Group.mkv')
  assert.equal(gardenTargetName(row('a', 0), '测试作品', '', 'tv'), '请填写目标季集')
  assert.equal(gardenTargetName(row('a', 1), '电影:名', '2024-01-01', 'movie'), '电影_名 (2024) - Group.mkv')
})
