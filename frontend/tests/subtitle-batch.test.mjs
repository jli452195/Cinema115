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
  const module = { exports: {} }; cache.set(name, module.exports)
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('require', 'module', 'exports', compiled)(path => path.startsWith('./') ? load(path.slice(2)) : require(path), module, module.exports)
  return module.exports
}
const { subtitleBatchGroups, subtitleBatchVideos, previewSubtitleBatch } = load('subtitleBatch')
const { subtitleOutputConflicts, subtitleScopeKey } = load('subtitlePresentation')
const video = (episode, season = 1, id = `${season}:${episode}`) => ({ id, name: `Show.S${season}E${episode}.mkv`, season, episode })
const row = (key, name = `Subtitle ${key}.ass`, overrides = {}) => ({ key: String(key), file: { name }, videoId: '', language: '', version: '', selected: false, reason: '', ...overrides })
const settings = { mode: 'episode', season: '1', startVideoId: '1:1', language: '__keep', onlyUnknownLanguage: true, selectMatched: true }
const preview = (rows, videos, options = {}, all = rows) => previewSubtitleBatch(rows, all, videos, { ...settings, ...options })
const applyPreview = (rows, result) => {
  const changes = new Map(result.changes.map(change => [change.key, change]))
  return rows.map(item => ({ ...item, ...changes.get(item.key) }))
}
function freeze(value) {
  for (const child of Object.values(value)) if (child && typeof child === 'object') freeze(child)
  return Object.freeze(value)
}
function versionFixture() {
  const videos = Array.from({ length: 12 }, (_, i) => ({ ...video(i + 1), name: `[ReinForce] Nanatsu no Taizai ${String(i + 1).padStart(2, '0')} (DVDRip 852x480 x264 FLAC).mkv` }))
  // 同目录每集 HKG/KTXP 各一份；语言保持未知，文件名标记不承担语言事实。
  const rows = ['HKG', 'KTXP'].flatMap(hint => videos.map((item, i) => {
    const name = `Nanatsu/subs/[ReinForce] Nanatsu no Taizai ${String(i + 1).padStart(2, '0')} (DVDRip 852x480 x264 FLAC).${hint}.ass`
    return row(`${hint}:${i + 1}`, name, { file: { name, sourceKey: 'archive:nanatsu', sourceLabel: '七大罪字幕包' }, videoId: item.id, selected: hint === 'HKG' })
  }))
  return { rows, videos }
}

test('按来源、目录、语言及版本隔离字幕组，忽略显示名碰撞', () => {
  const rows = [row('a', 'dir/A.ass', { file: { name: 'dir/A.ass', sourceKey: 'archive:a' } }), row('b', 'dir/B.ass', { file: { name: 'dir/B.ass', sourceKey: 'archive:b' } }), row('c', 'dir/C.ass', { language: 'zh-CN' }), row('d', 'dir/D.ass', { version: 'v2' }), row('e', 'other/E.ass'), row('f', 'dir/F.ass', { versionId: 'v1' }), row('g', 'dir/G.ass', { versionId: 'v2' })]
  assert.equal(subtitleBatchGroups(rows).length, rows.length)
  assert.equal(subtitleBatchGroups([row(1), row(2)]).length, 1)
})

test('按集号不压缩缺集、不跨季，目标编辑编号优先且不改输入', () => {
  const rows = [row(1, 'S01E01.ass'), row(3, 'S01E03.ass'), row(9, 'S02E01.ass'), row(4, 'S01E04.ass')]
  const videos = [video(1), video(3), { ...video(4), episode: NaN }, video(1, 2)]
  const before = structuredClone(rows), result = preview(rows, videos)
  assert.deepEqual(result.errors, [])
  assert.deepEqual(result.changes.map(change => change.videoId), ['1:1', '1:3'])
  assert.equal(result.skipped, 2)
  assert.deepEqual(rows, before)
})

test('同集多个视频只留未匹配，不能顺序盲选版本', () => {
  const rows = [row(1, 'S01E01.ass')], videos = [video(1), video(1, 1, 'alternate')]
  assert.equal(preview(rows, videos).changes.length, 0)
  assert.match(preview(rows, videos, { mode: 'sequence' }).errors.join(''), /多个视频版本/)
})

test('顺序采用自然排序，保留已匹配位置与手动清空，提示未知缺集风险', () => {
  const rows = [row(10, 'Part 10 unknown.ass'), row(2, 'Part 2 unknown.ass', { videoId: '1:2' }), row(1, 'Part 1 unknown.ass'), row(3, 'Part 3 unknown.ass')]
  const result = preview(rows, [video(1), video(2), video(3), video(4)], { mode: 'sequence', protectedKeys: new Set(['3']) })
  assert.deepEqual(result.errors, [])
  assert.equal(result.changes.find(change => change.key === '1').videoId, '1:1')
  assert.equal(result.changes.find(change => change.key === '10').videoId, '1:4')
  assert(!result.changes.some(change => change.key === '3'))
  assert(result.warnings.length > 0)
})

test('顺序检测缺集、重复、跨季、范围和既有映射冲突', () => {
  for (const names of [['S01E01.ass', 'S01E03.ass'], ['S01E01.ass', 'S01E01.srt'], ['S01E01.ass', 'S02E02.ass'], ['Show - 01-02.ass'], ['S01E01.5.ass']]) {
    const result = preview(names.map((name, i) => row(i, name)), [video(1), video(2)], { mode: 'sequence' })
    assert(result.errors.length > 0, names.join(','))
  }
  assert.match(preview([row(1)], [video(1), video(3)], { mode: 'sequence' }).errors.join(''), /目标视频存在缺集/)
  assert.match(preview([row(1, 'A.ass', { videoId: '1:2' })], [video(1), video(2)], { mode: 'sequence' }).errors.join(''), /已有对应关系/)
  assert.match(preview([row(1)], [video(1)], { mode: 'sequence', startVideoId: '' }).errors.join(''), /起始视频/)
})

test('特典不占正片位置，数量不等提示且不越界配对', () => {
  const result = preview([row('op', 'NCOP.ass'), row('a', 'alpha.ass'), row('b', 'beta.ass')], [video(1)], { mode: 'sequence' })
  assert.deepEqual(result.changes.map(change => change.key), ['a'])
  assert.match(result.warnings.join(''), /数量不一致/)
  assert.equal(result.skipped, 2)
})

test('IDX/SUB 作为一个位置同步，跨组或缺少配对阻止应用', () => {
  const rows = [row('idx', 'alpha.idx', { pairKey: 'pair' }), row('sub', 'alpha.sub', { pairKey: 'pair' }), row('ass', 'beta.ass')]
  const result = preview(rows, [video(1), video(2)], { mode: 'sequence', language: 'zh-CN' })
  assert.deepEqual(result.errors, [])
  assert.deepEqual(result.changes.map(change => change.videoId), ['1:1', '1:1', '1:2'])
  assert.match(preview(rows.slice(0, 1), [video(1)], {}, rows).errors.join(''), /配对不完整/)
})

test('批量语言默认不覆盖指定值，重复输出跨组阻止、未涉及旧冲突不阻止', () => {
  const rows = [row('a', 'a.ass', { videoId: '1:1' }), row('b', 'b.ass', { videoId: '1:2', language: 'zh-TW' })]
  const result = preview(rows, [], { mode: 'language', language: 'zh-CN', selectMatched: false })
  assert.equal(result.changes.length, 1)
  assert.equal(result.changes[0].language, 'zh-CN')
  assert.equal(preview(rows, [], { mode: 'language', language: 'zh-CN', onlyUnknownLanguage: false, selectMatched: false }).changes.length, 2)
  const conflict = row('other', 'other.ass', { videoId: '1:1', language: 'zh-CN', selected: true })
  assert.match(preview(rows, [], { mode: 'language', language: 'zh-CN' }, [...rows, conflict]).errors.join(''), /重复的字幕目标/)
  const untouched = [row('x', 'x.srt', { videoId: '1:9' }), row('y', 'y.srt', { videoId: '1:9' })]
  assert.deepEqual(preview(rows, [], { mode: 'language', language: 'zh-CN' }, [...rows, ...untouched]).errors, [])
})

test('跨页大组一次生成全部变更，目标排序不受输入顺序影响', () => {
  const rows = Array.from({ length: 80 }, (_, i) => row(i, `S01E${i + 1}.ass`)), videos = Array.from({ length: 80 }, (_, i) => video(i + 1)).reverse()
  assert.equal(preview(rows, videos).changes.length, 80)
  assert.equal(subtitleBatchVideos(videos, '1')[0].episode, 1)
  assert.equal(subtitleBatchVideos(videos, '').length, 0)
})

test('批量按集号沿用源编号偏移与歧义保护，不另造目标编号解释', () => {
  const rows = [row('13', '13.ass')]
  const offset = { ...video(1), name: 'Show - 13.mkv' }
  assert.equal(preview(rows, [offset]).changes[0].videoId, offset.id)
  const ambiguous = preview(rows, [offset, { ...video(13), name: 'Show - 25.mkv' }])
  assert.equal(ambiguous.changes.length, 0)
  assert.equal(ambiguous.skipped, 1)
})

test('未勾选候选版本不阻止本组应用，单独 SUB 沿用现有导入支持', () => {
  const rows = [row('main', 'S01E01.ass')], alternate = row('alternate', 'Other.S01E01.ass', { videoId: '1:1' })
  assert.deepEqual(preview(rows, [video(1)], {}, [...rows, alternate]).errors, [])
  assert.match(preview(rows, [video(1)], {}, [...rows, { ...alternate, selected: true }]).errors.join(''), /重复的字幕目标/)
  const sub = row('sub', 'alpha.sub', { pairKey: 'alpha' })
  assert.deepEqual(preview([sub], [video(1)], { mode: 'sequence' }).errors, [])
})

test('24 份 HKG/KTXP 字幕按标记分为两个 12 集批量组，范围不随语言或版本漂移', () => {
  const { rows } = versionFixture()
  const groups = subtitleBatchGroups(rows)
  assert.equal(groups.length, 2)
  for (const hint of ['HKG', 'KTXP']) {
    const group = groups.find(item => item.rows[0].key.startsWith(`${hint}:`))
    assert.equal(group.rows.length, 12)
    assert(group.rows.every(item => item.file.name.endsWith(`.${hint}.ass`)))
    assert.match(group.label, new RegExp(hint))
  }
  const scopes = new Set(rows.map(subtitleScopeKey))
  assert.equal(scopes.size, 1)
  assert.deepEqual(new Set(rows.map(item => subtitleScopeKey({ ...item, language: 'zh-TW', version: '用户校时' }))), scopes)
})

test('版本推断填写 HKG/KTXP 并同时勾选两版，保留映射与语言且不产生重名', () => {
  const { rows, videos } = versionFixture()
  const before = structuredClone(rows)
  const result = preview(rows, videos, { mode: 'version', inferVersions: true, onlyEmptyVersion: true })
  assert.deepEqual(result.errors, [])
  assert.equal(result.changes.length, 24)
  const applied = applyPreview(rows, result)
  for (const item of applied) {
    const original = before.find(previous => previous.key === item.key)
    assert.equal(item.version, item.key.split(':')[0])
    assert.equal(item.videoId, original.videoId)
    assert.equal(item.language, '')
    assert.equal(item.selected, true)
  }
  assert.equal(applied.filter(item => item.version === 'HKG').length, 12)
  assert.equal(applied.filter(item => item.version === 'KTXP').length, 12)
  assert.deepEqual(subtitleOutputConflicts(applied), [])
  assert.deepEqual(rows, before)
})

test('版本推断即使关闭仅填空也保留自定义标记，并允许保持原勾选状态', () => {
  const { rows, videos } = versionFixture()
  rows[0].version = '用户校时'
  rows[0].language = 'zh-TW'
  rows[12].version = '备用字幕'
  const before = structuredClone(rows)
  const result = preview(rows, videos, { mode: 'version', inferVersions: true, onlyEmptyVersion: false, selectMatched: false })
  assert.deepEqual(result.errors, [])
  assert.equal(result.changes.length, 22)
  const applied = applyPreview(rows, result)
  assert.equal(applied[0].version, '用户校时')
  assert.equal(applied[0].language, 'zh-TW')
  assert.equal(applied[12].version, '备用字幕')
  for (let i = 0; i < applied.length; i++) assert.equal(applied[i].selected, before[i].selected)
  assert.deepEqual(rows, before)
})

test('手动版本可只填空、显式覆盖或清空，未关联行不因版本操作被勾选', () => {
  const rows = [row('custom', 'A.HKG.ass', { videoId: '1:1', version: '用户校时' }), row('empty', 'B.KTXP.ass', { videoId: '1:2' }), row('unmatched', 'C.HKG.ass')]
  const filled = preview(rows, [], { mode: 'version', version: '新版', onlyEmptyVersion: true, selectMatched: false })
  assert.deepEqual(filled.errors, [])
  assert.deepEqual(applyPreview(rows, filled).map(item => item.version), ['用户校时', '新版', '新版'])
  const replaced = preview(rows, [], { mode: 'version', version: '新版', onlyEmptyVersion: false })
  assert.deepEqual(replaced.errors, [])
  assert.deepEqual(applyPreview(rows, replaced).map(item => [item.version, item.selected]), [['新版', true], ['新版', true], ['新版', false]])
  const cleared = preview(rows, [], { mode: 'version', version: '', onlyEmptyVersion: false, selectMatched: false })
  assert.deepEqual(cleared.errors, [])
  assert.deepEqual(cleared.changes.map(change => [change.key, change.version]), [['custom', '']])
})

test('推断不把语言、技术或未知文件名写为版本，也不自动补语言', () => {
  const rows = ['chs', 'CHT', 'zh-CN', 'ENG', '1080p', 'x264', 'AAC', 'FLAC', 'v2', 'REV', '2024', 'DEADBEEF'].map((suffix, i) => row(i, `Nanatsu.01.${suffix}.ass`, { videoId: '1:1' }))
  rows.push(row('unknown', 'unknown.ass', { videoId: '1:2' }))
  const result = preview(rows, [], { mode: 'version', inferVersions: true, selectMatched: false })
  assert.deepEqual(result.errors, [])
  assert.deepEqual(result.changes, [])
  assert(rows.every(item => item.language === '' && item.version === '' && !item.selected))
})

test('将双版本写为相同标记会报告输出冲突，重新预览推断仍可保留两版', () => {
  const { rows, videos } = versionFixture()
  const before = structuredClone(rows)
  const conflict = preview(rows, videos, { mode: 'version', version: '同版', onlyEmptyVersion: false })
  assert.match(conflict.errors.join(''), /重复的字幕目标/)
  assert.equal(conflict.changes.length, 24)
  assert.equal(subtitleOutputConflicts(applyPreview(rows, conflict)).length, 12)
  assert.deepEqual(rows, before)
  const resolved = preview(rows, videos, { mode: 'version', inferVersions: true })
  assert.deepEqual(resolved.errors, [])
  assert.deepEqual(subtitleOutputConflicts(applyPreview(rows, resolved)), [])
  assert.deepEqual(rows, before)
})

test('版本变更检查其他来源的最终勾选输出，未勾选或未涉及的旧冲突不阻止', () => {
  const rows = [row('edit', 'A.HKG.ass', { videoId: '1:1', language: 'zh-CN' })]
  const other = row('other', 'B.ass', { videoId: '1:1', language: 'zh-CN', version: 'hkg', selected: true, versionId: 'other-library' })
  const options = { mode: 'version', version: 'HKG' }
  assert.match(preview(rows, [], options, [...rows, other]).errors.join(''), /重复的字幕目标/)
  assert.deepEqual(preview(rows, [], options, [...rows, { ...other, selected: false }]).errors, [])
  assert.deepEqual(preview(rows, [], { ...options, selectMatched: false }, [...rows, other]).errors, [])
  const old = [row('old-a', 'old-a.srt', { videoId: '1:9', selected: true }), row('old-b', 'old-b.SRT', { videoId: '1:9', selected: true })]
  assert.deepEqual(preview(rows, [], options, [...rows, ...old]).errors, [])
  const clearing = [row('clear', 'A.ass', { videoId: '1:1', version: 'HKG', selected: true }), row('existing-empty', 'B.ass', { videoId: '1:1', selected: true })]
  assert.match(preview(clearing.slice(0, 1), [], { mode: 'version', version: '', selectMatched: false }, clearing).errors.join(''), /重复的字幕目标/)
})

test('成功预览、冲突预览及取消均不修改输入，丢弃结果后重新预览一致', () => {
  const fixture = freeze(versionFixture())
  const options = freeze({ ...settings, mode: 'version', inferVersions: true, onlyEmptyVersion: true })
  const before = structuredClone(fixture), previousOptions = structuredClone(options)
  const first = previewSubtitleBatch(fixture.rows, fixture.rows, fixture.videos, options)
  assert.deepEqual(first.errors, [])
  const expected = structuredClone(first)
  // 只修改将被丢弃的预览结果，模拟取消；原始数据必须继续保持可再次预览。
  first.changes[0].version = '已取消'
  first.changes[0].selected = false
  first.changes.length = 0
  const conflict = previewSubtitleBatch(fixture.rows, fixture.rows, fixture.videos, { ...options, inferVersions: false, version: '同版' })
  assert.match(conflict.errors.join(''), /重复的字幕目标/)
  assert.deepEqual(fixture, before)
  assert.deepEqual(options, previousOptions)
  assert.deepEqual(previewSubtitleBatch(fixture.rows, fixture.rows, fixture.videos, options), expected)
})

test('版本推断同步完整 IDX/SUB 配对，扩展名不同无冲突，缺配对仍报错', () => {
  const rows = [row('idx', 'Nanatsu.01.HKG.IDX', { pairKey: 'pair', videoId: '1:1' }), row('sub', 'Nanatsu.01.HKG.SUB', { pairKey: 'pair', videoId: '1:1' })]
  const result = preview(rows, [], { mode: 'version', inferVersions: true })
  assert.deepEqual(result.errors, [])
  assert.deepEqual(result.changes.map(change => [change.key, change.version, change.selected]), [['idx', 'HKG', true], ['sub', 'HKG', true]])
  assert.deepEqual(subtitleOutputConflicts(applyPreview(rows, result)), [])
  assert.match(preview(rows.slice(0, 1), [], { mode: 'version', inferVersions: true }, rows).errors.join(''), /配对不完整/)
})

test('同一 IDX/SUB 配对的大写 HKG 与小写 hkg 共享分组并统一推断为 HKG', () => {
  const pair = freeze([row('idx', 'Show.HKG.idx', { pairKey: 'pair', videoId: '1:1' }), row('sub', 'Show.hkg.sub', { pairKey: 'pair', videoId: '1:1' })])
  const before = structuredClone(pair)
  for (const rows of [pair, [...pair].reverse()]) {
    const groups = subtitleBatchGroups(rows)
    assert.equal(groups.length, 1)
    assert.equal(groups[0].rows.length, 2)
    assert.match(groups[0].label, /HKG/)
    const result = preview(groups[0].rows, [], { mode: 'version', inferVersions: true }, rows)
    assert.deepEqual(result.errors, [])
    assert.equal(result.changes.length, 2)
    const applied = applyPreview(rows, result)
    assert(applied.every(item => item.version === 'HKG' && item.selected && item.videoId === '1:1' && item.language === ''))
    assert.deepEqual(subtitleOutputConflicts(applied), [])
  }
  assert.deepEqual(pair, before)
})

test('IDX/SUB 保留自定义版本后最终字段不一致时阻止应用，显式统一版本后允许应用', () => {
  for (const subVersion of ['另一校时', '']) {
    const rows = freeze([row('idx', 'Show.HKG.idx', { pairKey: 'pair', videoId: '1:1', version: '用户校时' }), row('sub', 'Show.hkg.sub', { pairKey: 'pair', videoId: '1:1', version: subVersion })])
    const before = structuredClone(rows)
    const result = preview(rows, [], { mode: 'version', inferVersions: true })
    assert.match(result.errors.join(''), /IDX\/SUB.*相同视频、语言和版本/)
    const applied = applyPreview(rows, result)
    assert.deepEqual(applied.map(item => [item.version, item.selected]), [['用户校时', true], [subVersion || 'HKG', true]])
    assert.deepEqual(subtitleOutputConflicts(applied), [])
    assert.deepEqual(rows, before)
    const resolved = preview(rows, [], { mode: 'version', version: '统一校时', onlyEmptyVersion: false })
    assert.deepEqual(resolved.errors, [])
    assert(applyPreview(rows, resolved).every(item => item.version === '统一校时' && item.selected))
    assert.deepEqual(rows, before)
  }
})

test('非法版本在预览中报错且不修改输入，30 个字符边界与空版本合法', () => {
  const rows = freeze([row('a', 'A.ass', { videoId: '1:1', version: '原版' })])
  const before = structuredClone(rows)
  const invalid = [' leading', 'trailing ', 'ending.', ...Array.from('\\/:*?"<>|').map(char => `bad${char}version`), 'bad\0version', 'bad\rversion', 'bad\nversion', 'bad\x1fversion', 'a'.repeat(31), '中'.repeat(31), '𠮷'.repeat(31)]
  for (const version of invalid) {
    const result = preview(rows, [], { mode: 'version', version })
    assert.match(result.errors.join(''), /版本最多 30 个字/, JSON.stringify(version))
    assert.deepEqual(rows, before)
  }
  for (const version of ['', '用户.校时', 'a'.repeat(30), '中'.repeat(30), '𠮷'.repeat(30)]) {
    const result = preview(rows, [], { mode: 'version', version })
    assert.deepEqual(result.errors, [], JSON.stringify(version))
    assert.equal(result.changes[0].version, version)
    assert.deepEqual(rows, before)
  }
})
