import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/types/tvNumbering.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { numberingChanges } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const episode = (id, number) => ({ id, number, sourceSeason: 1, sourceNumber: number, name: `分集 ${id}`, date: '2018-07-06', runtime: 24, overview: '', stillUrl: '', rating: 0, votes: 0 })
const group = episodes => ({ id: 'g', name: 'Season', type: 6, seasons: [{ number: 1, episodes }] })

test('根据分集身份识别重排、新增和移除，不按当前位置比较', () => {
  const before = group([episode(1, 1), episode(2, 2), episode(3, 3)])
  const after = group([episode(2, 1), episode(1, 2), episode(4, 3)])
  const changes = numberingChanges(before, after)
  assert.equal(changes.added.length, 1)
  assert.equal(changes.removed.length, 1)
  assert.equal(changes.changed.length, 2)
  assert.match(changes.changed[0], /S01E02 分集 2 改为 S01E01 分集 2/)
})

test('播出日期和时长变化可供审核，评分变化不计为编排调整', () => {
  const before = group([episode(1, 1)])
  const after = structuredClone(before)
  after.seasons[0].episodes[0].rating = 9
  after.seasons[0].episodes[0].votes = 999
  assert.equal(numberingChanges(before, after).changed.length, 0)
  after.seasons[0].episodes[0].date = ''
  after.seasons[0].episodes[0].runtime = 12
  const changes = numberingChanges(before, after)
  assert.equal(changes.changed.length, 1)
  assert.match(changes.changed[0], /播出日期 2018-07-06 改为 未知/)
  assert.match(changes.changed[0], /时长 24 改为 12/)
})

test('空日期和缺失日期均显示未知，不能报告未知改为未知', () => {
  const before = group([{ ...episode(1, 1), date: '', runtime: 0 }])
  const after = structuredClone(before)
  const next = after.seasons[0].episodes[0]
  next.date = null
  delete next.runtime
  delete next.overview
  delete next.stillUrl
  assert.equal(numberingChanges(before, after).changed.length, 0)
})
