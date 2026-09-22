import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

const require = createRequire(import.meta.url)
const dataModule = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const libraryURL = dataModule(compile(await readFile(new URL('../src/types/seasonLibrary.ts', import.meta.url), 'utf8')))
const { seasonLibraryProgress } = await import(libraryURL)
const namingURL = dataModule(compile(await readFile(new URL('../src/utils/mediaNaming.ts', import.meta.url), 'utf8')))
const { formatScrapeName } = await import(namingURL)
const today = '2026-09-20'
const episodes = (count, date = '') => Array.from({ length: count }, (_, i) => ({ number: i + 1, date }))

test('详情页标题使用 TMDB 刮削命名格式', () => {
  assert.equal(formatScrapeName('暗杀教室', '2015-01-09', '62110'), '暗杀教室 (2015) {tmdb-62110}')
  assert.equal(formatScrapeName('无日期剧集', '', 42), '无日期剧集 {tmdb-42}')
})

test('小猪佩奇：63 集无日期，实际入库 1–21 集，不误报缺集或追平', () => {
  const progress = seasonLibraryProgress(episodes(63), new Set(Array.from({ length: 21 }, (_, i) => i + 1)), 63, today)
  assert.equal(progress.ownedCount, 21)
  assert.equal(progress.ownedLabel, '1-21')
  assert.equal(progress.collected, 0)
  assert.equal(progress.aired.length, 0)
  assert.deepEqual(progress.missing, [])
  assert.deepEqual(progress.segments, [])
  assert.equal(progress.numberingMode, true)
  assert.equal(progress.displayTotal, 63)
  assert.equal(progress.displayCollected, 21)
  assert.deepEqual(progress.displayMissing, [])
  assert.deepEqual(progress.displaySegments, [{ state: 'owned', count: 21 }, { state: 'unknown', count: 42 }])
  assert.equal(progress.complete, false)
  assert.equal(progress.caughtUp, false)
})

test('无日期时只标记已入库范围内部的断档', () => {
  const owned = new Set([...Array.from({ length: 21 }, (_, i) => i + 1), ...Array.from({ length: 23 }, (_, i) => i + 34)])
  const progress = seasonLibraryProgress(episodes(63), owned, 63, today)
  assert.equal(progress.displayCollected, 44)
  assert.equal(progress.displayMissingLabel, '22-33')
  assert.deepEqual(progress.displayMissing, Array.from({ length: 12 }, (_, i) => i + 22))
  assert.deepEqual(progress.displaySegments, [
    { state: 'owned', count: 21 }, { state: 'missing', count: 12 },
    { state: 'owned', count: 23 }, { state: 'unknown', count: 7 },
  ])
})

test('日期混合时仅统计已播缺集，未来和无日期入库单独计数', () => {
  const progress = seasonLibraryProgress([
    { number: 1, date: '2026-01-01' }, { number: 2, date: '2026-01-02' },
    { number: 3, date: '' }, { number: 4, date: '2027-01-01' }, { number: 5, date: '' },
  ], new Set([1, 3, 4]), 5, today)
  assert.equal(progress.ownedCount, 3)
  assert.equal(progress.collected, 1)
  assert.equal(progress.extraOwned, 2)
  assert.deepEqual(progress.missing, [2])
  assert.deepEqual(progress.segments, [{ owned: true, count: 1 }, { owned: false, count: 1 }])
})

test('已知播出全收齐但仍有日期未知时不宣称追平', () => {
  const progress = seasonLibraryProgress([{ number: 1, date: '2026-01-01' }, { number: 2, date: '' }], new Set([1]), 2, today)
  assert.equal(progress.caughtUp, false)
  assert.equal(progress.complete, false)
  assert.deepEqual(progress.missing, [])
})

test('正常已播完和未播完的进度不变，特别篇第 0 集和额外入库保留', () => {
  const complete = seasonLibraryProgress(episodes(2, '2026-01-01'), new Set([1, 2]), 2, today)
  assert.equal(complete.complete, true)
  const ongoing = seasonLibraryProgress([{ number: 1, date: '2026-01-01' }, { number: 2, date: '2027-01-01' }], new Set([1]), 2, today)
  assert.equal(ongoing.caughtUp, true)
  const specials = seasonLibraryProgress([{ number: 0, date: '' }], new Set([0, 1, 3]), 1, today)
  assert.equal(specials.ownedCount, 3)
  assert.equal(specials.ownedLabel, '0-1、3')
})

test('TMDB 未收录的入库集不扩展本季范围', () => {
  const progress = seasonLibraryProgress(episodes(23, '2026-01-01'), new Set([...Array.from({ length: 23 }, (_, i) => i + 1), 24]), 23, today)
  assert.deepEqual(progress.unlistedOwned, [24])
  assert.equal(progress.complete, true)
})

const source = await readFile(new URL('../src/components/SeasonLibraryProgress.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = compile(compileScript(descriptor, { id: 'season-progress-test', inlineTemplate: true }).content)
  .replaceAll(/from ["']vue["']/g, `from '${pathToFileURL(require.resolve('vue/dist/vue.runtime.esm-bundler.js')).href}'`)
  .replaceAll(/from ["']\.\.\/types\/seasonLibrary["']/g, `from '${libraryURL}'`)
const { default: SeasonLibraryProgress } = await import(dataModule(compiled))

test('季卡片在无日期时只展示紧凑的集号进度条', async () => {
  const progress = seasonLibraryProgress(episodes(63), new Set(Array.from({ length: 21 }, (_, i) => i + 1)), 63, today)
  const html = await renderToString(createSSRApp(SeasonLibraryProgress, { state: 'present', progress, loading: false, error: '', hasUndated: true }))
  assert.match(html, /已入库 1-21/)
  assert.match(html, /role="progressbar"/)
  assert.match(html, /aria-valuemax="63"/)
  assert.match(html, /其余集数尚未确认/)
  assert.doesNotMatch(html, /TMDB 播出日期未提供|入库集数：1-21/)
})

test('季卡片用缺集色标记无日期范围内的中间断档', async () => {
  const owned = new Set([...Array.from({ length: 21 }, (_, i) => i + 1), ...Array.from({ length: 23 }, (_, i) => i + 34)])
  const progress = seasonLibraryProgress(episodes(63), owned, 63, today)
  const html = await renderToString(createSSRApp(SeasonLibraryProgress, { state: 'present', progress, loading: false, error: '', hasUndated: true }))
  assert.match(html, /缺集 22-33/)
  assert.match(html, /bg-amber-400/)
})

test('TMDB 加载失败不隐藏已入库事实，过期索引仍明确标注', async () => {
  const props = { state: 'present', ownedEpisodes: [1, 2], loading: false, error: '失败', hasUndated: false }
  const html = await renderToString(createSSRApp(SeasonLibraryProgress, props))
  assert.match(html, /已入库 2 集/)
  assert.match(html, /播出信息加载失败/)
  const stale = await renderToString(createSSRApp(SeasonLibraryProgress, { ...props, state: 'stale' }))
  assert.match(stale, /索引待更新/)
  assert.doesNotMatch(stale, /已入库 2 集/)
})
