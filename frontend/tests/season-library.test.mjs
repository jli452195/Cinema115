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
const today = '2026-09-20'
const episodes = (count, date = '') => Array.from({ length: count }, (_, i) => ({ number: i + 1, date }))

test('小猪佩奇：63 集无日期，实际入库 1–21 集，不误报缺集或追平', () => {
  const progress = seasonLibraryProgress(episodes(63), new Set(Array.from({ length: 21 }, (_, i) => i + 1)), 63, today)
  assert.equal(progress.ownedCount, 21)
  assert.equal(progress.ownedLabel, '1-21')
  assert.equal(progress.collected, 0)
  assert.equal(progress.aired.length, 0)
  assert.deepEqual(progress.missing, [])
  assert.deepEqual(progress.segments, [])
  assert.equal(progress.complete, false)
  assert.equal(progress.caughtUp, false)
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

const source = await readFile(new URL('../src/components/SeasonLibraryProgress.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = compile(compileScript(descriptor, { id: 'season-progress-test', inlineTemplate: true }).content)
  .replaceAll(/from ["']vue["']/g, `from '${pathToFileURL(require.resolve('vue/dist/vue.runtime.esm-bundler.js')).href}'`)
  .replaceAll(/from ["']\.\.\/types\/seasonLibrary["']/g, `from '${libraryURL}'`)
const { default: SeasonLibraryProgress } = await import(dataModule(compiled))

test('季卡片展示实际入库集数，无日期不渲染进度条', async () => {
  const progress = seasonLibraryProgress(episodes(63), new Set(Array.from({ length: 21 }, (_, i) => i + 1)), 63, today)
  const html = await renderToString(createSSRApp(SeasonLibraryProgress, { state: 'present', progress, loading: false, error: '', hasUndated: true }))
  assert.match(html, /已入库 21 集/)
  assert.match(html, /入库集数：1-21/)
  assert.match(html, /TMDB 播出日期未提供/)
  assert.doesNotMatch(html, /role="progressbar"|已追平|整季已入库/)
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
