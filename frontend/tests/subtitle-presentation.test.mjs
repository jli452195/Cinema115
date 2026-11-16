import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const cache = new Map()
function load(name) {
  if (cache.has(name)) return cache.get(name)
  const source = readFileSync(new URL(`../src/utils/${name}.ts`, import.meta.url), 'utf8')
  const module = { exports: {} }
  cache.set(name, module.exports)
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('require', 'module', 'exports', compiled)(path => path.startsWith('./') ? load(path.slice(2)) : require(path), module, module.exports)
  return module.exports
}
const { subtitleVariantHint, subtitleScopeKey, subtitleVariantGroups, subtitleOutputConflicts } = load('subtitlePresentation')
const { subtitleTargetName } = load('subtitleLibrary')
const video = (episode, season = 1, id = `${season}:${episode}`) => ({ id, name: `Yuyushiki.S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}.mkv`, season, episode })
const row = (key, name, overrides = {}) => ({ key: String(key), file: { name }, videoId: '', language: '', version: '', selected: false, reason: '', ...overrides })
const videos = Array.from({ length: 12 }, (_, i) => video(i + 1))
// 复现同一目录 12 集、每集 HKG/KTXP 两份字幕的实际文件布局，不连接网盘。
const variants = ['HKG', 'KTXP'].flatMap(hint => videos.map((item, i) => row(`${hint}:${i + 1}`, `Yuyushiki/[VCB-Studio] Yuyushiki [${String(i + 1).padStart(2, '0')}] [Ma10p_1080p] [x265_flac].${hint}.ass`, {
  file: { name: `Yuyushiki/[VCB-Studio] Yuyushiki [${String(i + 1).padStart(2, '0')}] [Ma10p_1080p] [x265_flac].${hint}.ass`, sourceKey: 'archive:fixture', sourceLabel: '字幕压缩包' },
  videoId: item.id, selected: true,
})))

test('扩展名前独立大写标记只作为文件名提示', () => {
  for (const hint of ['HKG', 'KTXP']) for (const extension of ['ass', 'SSA', 'srt', 'vtt', 'IDX', 'sub']) {
    assert.equal(subtitleVariantHint(`dir/Show.S01E01.${hint}.${extension}`), hint)
    assert.equal(subtitleVariantHint(`C:\\dir\\Show.chs.${hint}.${extension}`), hint)
  }
  assert.equal(subtitleVariantHint('Show.CUSTOM.ass'), 'CUSTOM')
  for (const name of ['Show.ass', 'unknown.srt', 'HKG.ass', 'ShowHKG.ass', 'Show_HKG.ass', 'Show.[HKG].ass', 'Show.Ktxp.ass', 'Show.hkg.ass', 'Show.HKG.ass.bak', 'HKG/Show.ass']) assert.equal(subtitleVariantHint(name), '', name)
})

test('语言、技术、修订、校验值与年份绝不作为版本提示', () => {
  for (const suffix of ['chs', 'cht', 'CHS', 'CHT', 'zh-CN', 'ZH-CN', 'zh-TW', 'ZH', 'EN', 'ENG', 'JA', 'JPN', 'FR', 'FRA', 'DE', 'DEU', 'ES', 'SPA', 'KO', 'KOR', 'NLD', 'DUT', 'FIL', 'YUE', 'SC', 'TC', 'GB', 'BIG5', 'SDH', 'CC', 'HANS', 'HANT', '1080p', '1080P', 'x264', 'X264', 'AAC', 'FLAC', 'HEVC', 'DTS', 'AVC', 'HDR', 'SDR', 'BD', 'WEB', 'UTF8', 'v2', 'V2', 'rev', 'REV', 'REV2', '2024', 'DEADBEEF', 'ABCDEF', 'A1B2C3D4', '4e9fabc123de', 'ASS', 'SRT', 'IDX', 'SUB']) {
    assert.equal(subtitleVariantHint(`Show.S01E01.${suffix}.ass`), '', suffix)
  }
  assert.equal(subtitleVariantHint('Show.HKG.chs.ass'), '')
  assert.equal(subtitleVariantHint('Show.HKG.AAC.ass'), '')
})

test('24 份 HKG/KTXP 字幕分成两组，各覆盖当前 12 个视频', () => {
  const before = structuredClone(variants)
  const groups = subtitleVariantGroups(variants, videos)
  assert.deepEqual(groups.map(group => [group.hint, group.rows.length, group.covered, group.total, group.missing]), [['HKG', 12, 12, 12, []], ['KTXP', 12, 12, 12, []]])
  assert.equal(groups[0].scopeKey, groups[1].scopeKey)
  assert.notEqual(groups[0].key, groups[1].key)
  assert.match(groups[0].label, /字幕压缩包.*文件名标记 HKG.*S01/)
  assert.doesNotMatch(groups[0].label, /Yuyushiki\/|作者|字幕组|简体|繁体/)
  assert.equal(groups[0].rows[0], variants[0])
  assert.deepEqual(variants, before)
})

test('来源和目录隔离，语言、输出版本与关联状态不改变范围', () => {
  const a = row('a', 'folder/a.HKG.ass', { file: { name: 'folder/a.HKG.ass', sourceKey: 'archive:a' } })
  const changed = { ...a, language: 'zh-TW', version: 'alternative', selected: true, videoId: '1:1' }
  assert.equal(subtitleScopeKey(a), subtitleScopeKey(changed))
  assert.equal(subtitleVariantGroups([a], videos)[0].key, subtitleVariantGroups([changed], videos)[0].key)
  const isolated = [a, { ...a, file: { ...a.file, sourceKey: 'archive:b' } }, { ...a, file: { ...a.file, name: 'other/b.HKG.ass' } }, { ...a, versionId: 'library:a' }, { ...a, versionId: 'library:b' }, { ...a, file: { ...a.file, sourceKey: 'library:library:a' } }]
  assert.equal(new Set(isolated.map(subtitleScopeKey)).size, isolated.length)
  assert.equal(subtitleVariantGroups(isolated, videos).length, isolated.length)
  assert.equal(subtitleScopeKey(a), subtitleScopeKey({ ...a, file: { ...a.file, name: 'folder\\b.HKG.ass' } }))
})

test('库路径决定目录，显示名碰撞不合并来源，短标签不含内部路径', () => {
  const a = row('a', 'A.HKG.ass', { versionId: 'repo:a', path: 'internal/season/A.HKG.ass', sourceLabel: '字幕库 · 作品名' })
  const b = { ...a, key: 'b', path: 'internal/season/B.HKG.ass', language: 'zh-CN', version: 'custom' }
  const c = { ...a, key: 'c', path: 'internal/other/C.HKG.ass' }
  const d = { ...a, key: 'd', versionId: 'repo:b' }
  const groups = subtitleVariantGroups([a, b, c, d], videos)
  assert.equal(groups.length, 3)
  assert.equal(groups[0].rows.length, 2)
  assert.equal(subtitleScopeKey(a), subtitleScopeKey({ ...a, path: 'internal\\season\\B.HKG.ass' }))
  assert.match(groups[0].label, /字幕库 · 作品名/)
  for (const group of groups) assert.doesNotMatch(group.label, /internal|repo:a|repo:b/)
  assert.match(subtitleVariantGroups([{ ...a, file: { ...a.file, sourceLabel: '资源包' } }], videos)[0].label, /^资源包/)
})

test('语言标记保留在未知提示组，手动修改语言和版本仍属于同组', () => {
  const rows = ['chs', 'CHT', 'zh-CN', 'zh-TW', 'ENG'].map((language, i) => row(i, `Show.${language}.ass`, { language: i % 2 ? 'zh-TW' : 'zh-CN', version: i % 2 ? 'other' : '' }))
  const groups = subtitleVariantGroups(rows, videos)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].hint, '')
  assert.equal(groups[0].covered, 0)
  assert.equal(groups[0].total, 12)
  assert.deepEqual(groups[0].missing, videos.map(item => item.name))
  assert.match(groups[0].label, /未识别标记.*未关联/)
})

test('不完整组仅按当前有效关联去重，未勾选关联仍计覆盖，失效 ID 不计入', () => {
  const rows = [variants[0], variants[2], { ...variants[2], key: 'duplicate', selected: false }, { ...variants[4], key: 'not-selected', selected: false }, { ...variants[6], key: 'removed', videoId: 'removed' }, { ...variants[7], key: 'unmatched', videoId: '' }]
  const group = subtitleVariantGroups(rows, [...videos, videos[0], { id: '', name: 'invalid.mkv' }])[0]
  assert.equal(group.rows.length, 6)
  assert.equal(group.covered, 3)
  assert.equal(group.total, 12)
  assert.deepEqual(group.missing, videos.filter(item => !['1:1', '1:3', '1:5'].includes(item.id)).map(item => item.name))
})

test('没有有效关联时不从字幕集号猜覆盖，无视频时总数与缺失均为空', () => {
  const rows = [row('a', 'Show.S02E01.HKG.ass', { videoId: 'old' }), row('b', 'Show.S02E02.HKG.ass')]
  const group = subtitleVariantGroups(rows, [video(1), video(1, 2)])[0]
  assert.equal(group.covered, 0)
  assert.equal(group.total, 2)
  assert.match(group.label, /未关联，按所选视频/)
  const empty = subtitleVariantGroups(rows, [])[0]
  assert.equal(empty.covered, 0)
  assert.equal(empty.total, 0)
  assert.deepEqual(empty.missing, [])
  assert.deepEqual(subtitleVariantGroups([], videos), [])
})

test('单季关联只统计所属季，并保留同集不同视频版本的缺失', () => {
  const targets = [video(1), video(2), video(2, 1, 'alternate'), video(1, 2), video(2, 2)]
  const rows = [row('a', 'A.HKG.ass', { videoId: '1:1' })]
  const group = subtitleVariantGroups(rows, targets)[0]
  assert.equal(group.covered, 1)
  assert.equal(group.total, 3)
  assert.deepEqual(group.missing, [targets[1].name, targets[2].name])
  assert.doesNotMatch(group.label, /跨季/)
})

test('跨季关联明确标记，分母为关联季的当前视频并集', () => {
  const targets = [video(1), video(2), video(1, 2), video(2, 2), video(1, 3)]
  const rows = [row('a', 'A.HKG.ass', { videoId: '1:1' }), row('b', 'B.HKG.ass', { videoId: '2:1' })]
  const groups = subtitleVariantGroups(rows, targets)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].covered, 2)
  assert.equal(groups[0].total, 4)
  assert.deepEqual(groups[0].missing, [targets[1].name, targets[3].name])
  assert.match(groups[0].label, /跨季 S01 \/ S02/)
})

test('关联季号不明时按所选视频保守统计并明确标记', () => {
  const targets = [video(1), video(1, 2), { id: 'unknown', name: 'Movie.mkv' }]
  const rows = [row('a', 'A.HKG.ass', { videoId: '1:1' }), row('b', 'B.HKG.ass', { videoId: 'unknown' })]
  const group = subtitleVariantGroups(rows, targets)[0]
  assert.equal(group.covered, 2)
  assert.equal(group.total, 3)
  assert.deepEqual(group.missing, [targets[1].name])
  assert.match(group.label, /季号不明，按所选视频/)
})

test('目标文件名季集复用已有解析，显式编辑的季号优先且非法值不回退', () => {
  const targets = [{ id: 'a', name: 'Show.S01E01.mkv' }, { id: 'b', name: 'Show.S01E02.mkv' }, { id: 'c', name: 'Show.S02E01.mkv' }]
  const rows = [row('a', 'A.HKG.ass', { videoId: 'a' })]
  assert.equal(subtitleVariantGroups(rows, targets)[0].total, 2)
  assert.equal(subtitleVariantGroups(rows, [{ ...targets[0], season: 2, episode: 1 }, ...targets.slice(1)])[0].total, 2)
  for (const fields of [{ season: NaN }, { season: -1 }, { season: 1.5 }, { episode: 1 }]) {
    const group = subtitleVariantGroups(rows, [{ ...targets[0], ...fields }, ...targets.slice(1)])[0]
    assert.equal(group.total, 3)
    assert.match(group.label, /季号不明/)
  }
})

test('冲突只包含最终勾选且关联非空的行，忽略来源并复用真实输出命名', () => {
  const rows = [row('a', 'a.ASS', { videoId: 'target', language: 'zh-CN', version: 'HKG', selected: true }), row('b', 'b.ass', { videoId: 'target', language: 'ZH-cn', version: 'hkg', selected: true, versionId: 'library:other' }), row('c', 'c.ass', { videoId: 'target', language: 'zh-CN', version: 'HKG', selected: false }), row('d', 'd.ass', { language: 'zh-CN', version: 'HKG', selected: true }), row('e', 'e.ass', { videoId: 'target', language: 'zh-TW', version: 'HKG', selected: true }), row('f', 'f.ass', { videoId: 'target', language: 'zh-CN', version: 'KTXP', selected: true }), row('g', 'g.ass', { videoId: 'other', language: 'zh-CN', version: 'HKG', selected: true })]
  const before = structuredClone(rows)
  const conflicts = subtitleOutputConflicts(rows)
  assert.equal(conflicts.length, 1)
  assert.deepEqual(conflicts[0].rowKeys, ['a', 'b'])
  assert.equal(conflicts[0].videoId, 'target')
  assert.equal(conflicts[0].name, subtitleTargetName('', rows[0].file.name, rows[0].language, rows[0].version))
  assert.equal(conflicts[0].key, subtitleOutputConflicts(rows.slice(0, 2).reverse())[0].key)
  assert.deepEqual(subtitleOutputConflicts(rows.map(item => ({ ...item, selected: false }))), [])
  assert.deepEqual(rows, before)
})

test('24 份双版本默认输出有 12 项冲突，填写输出版本后全部消除', () => {
  const conflicts = subtitleOutputConflicts(variants)
  assert.equal(conflicts.length, 12)
  assert(conflicts.every(conflict => conflict.rowKeys.length === 2))
  assert.deepEqual(subtitleOutputConflicts(variants.map(item => ({ ...item, version: subtitleVariantHint(item.file.name) }))), [])
  assert.deepEqual(subtitleOutputConflicts(variants.map(item => ({ ...item, selected: item.key.startsWith('HKG:') }))), [])
})

test('IDX/SUB 扩展名不同不冲突，覆盖作为同一视频去重，同扩展重复仍冲突', () => {
  const idx = row('idx', 'episode.HKG.IDX', { pairKey: 'pair', videoId: '1:1', language: 'zh-CN', selected: true })
  const sub = row('sub', 'episode.HKG.SUB', { pairKey: 'pair', videoId: '1:1', language: 'zh-CN', selected: true })
  assert.deepEqual(subtitleOutputConflicts([idx, sub]), [])
  const group = subtitleVariantGroups([idx, sub], videos)[0]
  assert.equal(group.rows.length, 2)
  assert.equal(group.covered, 1)
  assert.equal(group.total, 12)
  const conflicts = subtitleOutputConflicts([idx, sub, { ...idx, key: 'other-idx', file: { name: 'other.idx' } }])
  assert.equal(conflicts.length, 1)
  assert.deepEqual(conflicts[0].rowKeys, ['idx', 'other-idx'])
  assert.equal(conflicts[0].name, '.zh-CN.idx')
})

test('输出名大小写不敏感，视频 ID 保持原始身份，所有重复行集中列出', () => {
  const rows = ['a', 'b', 'c'].map(key => row(key, `${key}.ASS`, { videoId: 'Video', selected: true }))
  const conflicts = subtitleOutputConflicts([...rows, { ...rows[0], key: 'other-video', videoId: 'video' }, { ...rows[0], key: 'blank', videoId: ' ' }])
  assert.equal(conflicts.length, 1)
  assert.deepEqual(conflicts[0].rowKeys, ['a', 'b', 'c'])
  assert.deepEqual(subtitleOutputConflicts([]), [])
})
