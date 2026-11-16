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
const video = (episode, season = 1, id = `${season}:${episode}`) => ({ id, name: `Show.S${season}E${episode}.mkv`, season, episode })
const row = (key, name = `Subtitle ${key}.ass`, overrides = {}) => ({ key: String(key), file: { name }, videoId: '', language: '', version: '', selected: false, reason: '', ...overrides })
const settings = { mode: 'episode', season: '1', startVideoId: '1:1', language: '__keep', onlyUnknownLanguage: true, selectMatched: true }
const preview = (rows, videos, options = {}, all = rows) => previewSubtitleBatch(rows, all, videos, { ...settings, ...options })

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
