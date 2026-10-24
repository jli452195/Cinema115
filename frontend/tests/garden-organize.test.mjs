import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
const source = await readFile(new URL('../src/utils/organizeNaming.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { applyOrganizeMapping, organizeTargetName, organizeSubtitleName } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const row = (id, episode, endEpisode = '') => ({ id, name: `file${id}.mkv`, selected: true, season: 1, episode, endEpisode, suffix: 'Group' })

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

test('target preview preserves range, file extension and group, unknown remains unconfirmed', () => {
  assert.equal(organizeTargetName(row('a', 1, 2), '测试作品', '2024-01-01', 'tv'), '测试作品 - S01E01-E02 - Group.mkv')
  assert.equal(organizeTargetName(row('a', 0), '测试作品', '', 'tv'), '请填写目标季集')
  assert.equal(organizeTargetName(row('a', 1), '电影:名', '2024-01-01', 'movie'), '电影_名 (2024) - Group.mkv')
})

test('companion naming follows edited video ranges and keeps complete language/default tags', () => {
  const video = row('a', 13, 14)
  const sub = { file: { name: 'original.zh-TW.default.ASS' }, tail: '.zh-TW.default' }
  assert.equal(organizeSubtitleName(organizeTargetName(video, '测试作品', '', 'tv'), sub), '测试作品 - S01E13-E14 - Group.zh-TW.default.ass')
  Object.assign(video, { season: 2, episode: 1, endEpisode: '', suffix: 'BD' })
  assert.equal(organizeSubtitleName(organizeTargetName(video, '测试作品', '', 'tv'), sub), '测试作品 - S02E01 - BD.zh-TW.default.ass')
  assert.equal(organizeSubtitleName('电影.名字 (2026).mkv', { file: { name: 'original.srt' } }), '电影.名字 (2026).srt')
  assert.equal(organizeSubtitleName('请填写目标季集', sub), '请填写目标季集')
})

test('joined files retain starts 01 and 03, and clearing the end names only the starting episode', () => {
  const rows = [row('a', 1, 2), row('b', 3, 4)]
  const sources = { a: { episode: 1, endEpisode: 2 }, b: { episode: 3, endEpisode: 4 } }
  applyOrganizeMapping(rows, sources, 1, 1, 1, true)
  assert.deepEqual(rows.map(r => [r.episode, r.endEpisode]), [[1, 2], [3, 4]])
  assert.equal(organizeTargetName(rows[1], '作品', '', 'tv'), '作品 - S01E03-E04 - Group.mkv')
  rows[0].endEpisode = ''; rows[1].endEpisode = ''
  assert.equal(organizeTargetName(rows[0], '作品', '', 'tv'), '作品 - S01E01 - Group.mkv')
  assert.equal(organizeTargetName(rows[1], '作品', '', 'tv'), '作品 - S01E03 - Group.mkv')
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
