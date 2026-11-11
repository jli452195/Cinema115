import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/utils/animeSubscriptionPresentation.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { sourceProgressView } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('明确终点使用目标范围，缺集与齐集分别展示', () => {
  const progress = { state: 'missing', firstEpisode: 49, publishedThrough: 58, organizedCount: 8, missingCount: 2, targetCount: 12, missing: [{ first: 53, last: 54 }] }
  const view = sourceProgressView(progress)
  assert.equal(view.label, '8 / 12 集')
  assert.equal(view.status, '待整理 2 集')
  assert.match(view.hint, /待整理：53–54/)
  assert.equal(sourceProgressView({ ...progress, state: 'complete', publishedThrough: 60, organizedCount: 12, missingCount: 0, missing: [] }).status, '已齐集')
  assert.equal(sourceProgressView(progress, true).total, 0)
  assert.equal(sourceProgressView(progress, true).label, '保存后更新进度')
})

test('长篇从指定集号计算当前范围，跟上更新保持进行中', () => {
  const progress = { state: 'ongoing', firstEpisode: 1100, publishedThrough: 1120, organizedCount: 21, missingCount: 0, targetCount: 0 }
  const view = sourceProgressView(progress)
  assert.equal(view.label, '21 / 21 集 · 当前更新')
  assert.equal(view.status, '')
  assert.equal(view.state, 'ongoing')
  assert.match(view.detail, /从第 1100 集起.*第 1120 集.*当前更新范围/)
  assert.equal(sourceProgressView({ ...progress, state: 'missing', publishedThrough: 1121, missingCount: 1, missing: [{ first: 1121, last: 1121 }] }).total, 22)
  assert.equal(sourceProgressView({ ...progress, firstEpisode: 0, publishedThrough: 0, organizedCount: 1 }).total, 1)
  assert.equal(sourceProgressView({ ...progress, publishedThrough: 1099, organizedCount: 0 }).total, 0)
  assert.equal(sourceProgressView({ ...progress, state: 'unknown' }).label, '等待检查')
})

test('缺第 5 集时按集号保留蓝、红、蓝、灰的位置', () => {
  const progress = { state: 'missing', firstEpisode: 1, publishedThrough: 6, organizedCount: 5, missingCount: 1, targetCount: 12, missing: [{ first: 5, last: 5 }] }
  const view = sourceProgressView(progress)
  assert.deepEqual(view.segments, [
    { first: 1, last: 4, state: 'organized' },
    { first: 5, last: 5, state: 'missing' },
    { first: 6, last: 6, state: 'organized' },
    { first: 7, last: 12, state: 'unchecked' },
  ])
  assert.equal(view.status, '待整理 1 集')
  assert.equal(view.coverage, '已整理：1–4、6 · 待整理：5')
  assert.deepEqual(sourceProgressView(progress, true).segments, [])
})

test('首尾和连续缺集保持区间，长篇只生成必要的连续段', () => {
  const view = sourceProgressView({ state: 'missing', firstEpisode: 0, publishedThrough: 8, organizedCount: 5, missingCount: 4, targetCount: 12, missing: [{ first: 0, last: 0 }, { first: 3, last: 4 }, { first: 8, last: 8 }] })
  assert.deepEqual(view.segments.map(({ first, last, state }) => [first, last, state]), [
    [0, 0, 'missing'], [1, 2, 'organized'], [3, 4, 'missing'], [5, 7, 'organized'], [8, 8, 'missing'], [9, 11, 'unchecked'],
  ])
  assert.equal(view.coverage, '已整理：1–2、5–7 · 待整理：0、3–4、8')
  const long = sourceProgressView({ state: 'missing', firstEpisode: 1000, publishedThrough: 9999, organizedCount: 8999, missingCount: 1, targetCount: 0, missing: [{ first: 5000, last: 5000 }] })
  assert.equal(long.total, 9000)
  assert.equal(long.segments.length, 3)
  assert.equal(long.segments.reduce((total, segment) => total + segment.last - segment.first + 1, 0), 9000)
})
