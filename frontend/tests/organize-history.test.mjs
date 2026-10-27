import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const code = ts.transpileModule(readFileSync(new URL('../src/utils/organizeHistory.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const module = { exports: {} }
new Function('module', 'exports', code)(module, module.exports)
const { historyTasks, isActiveTask, taskCounts, taskStatus, taskTone, resultSummary, needsAttention, itemStatus } = module.exports
const task = (statuses, status = 'partial', kind = 'organize') => ({ key: 'organize:1', kind, status, items: statuses.map((status, i) => ({ id: String(i), status })) })

test('partial operations preserve renamed, unprocessed, uncertain and skipped outcomes', () => {
  const job = task(['done', 'uncertain', 'renamed', 'pending', 'blocked', 'skipped'])
  assert.deepEqual(taskCounts(job), { done: 1, uncertain: 1, unfinished: 3, skipped: 1, processing: 0, attention: 4, total: 6 })
  assert.equal(resultSummary(job), '完成 1/6 · 待核实 1 · 跳过 1 · 未完成 3')
  assert.equal(itemStatus(job.items[2], job), '已改名，尚未移动')
  assert.equal(needsAttention(job.items[5], job), false)
  assert.equal(taskTone(job), 'warning')
})

test('active intermediate stages are processing, not failed or completed', () => {
  const job = task(['done', 'renamed', 'moving', 'preparing', 'pending'], 'running')
  assert.equal(isActiveTask(job), true)
  assert.equal(taskCounts(job).processing, 3)
  assert.equal(taskCounts(job).done, 1)
  assert.equal(taskCounts(job).attention, 0)
  job.status = 'interrupted'
  assert.equal(taskCounts(job).attention, 4)
  assert.equal(itemStatus(job.items[2], job), '操作中断，需核实结果')
})

test('title-only history keeps the expected target without exposing an invented source path', () => {
  const job = { id: 'single', title: '作品', status: 'downloading', createdAt: '2026-10-24T00:00:00Z', config: { accountId: 'main', sourcePath: '/staging', targetPath: '/out' }, items: [{ file: { id: 'title', name: '作品 [12 END]', sourcePath: '/title', mediaType: 'video', titleOnly: true }, targetPath: '/out/作品 - S01E12', status: 'pending' }] }
  const expected = historyTasks([job], [], [])[0].items[0]
  assert.equal(expected.source, '')
  assert.equal(expected.target, '/out/作品 - S01E12')
  assert.equal(expected.titleOnly, true)
  Object.assign(job.items[0].file, { id: '901', sourcePath: '/staging/video.mkv', titleOnly: false })
  assert.equal(historyTasks([job], [], [])[0].items[0].source, '/staging/video.mkv')
})

test('skipped subtitles never count as uploaded or cause a false failure warning', () => {
  const job = task(['done', 'done', 'skipped'], 'completed', 'subtitle')
  assert.equal(resultSummary(job), '已上传 2/3 · 跳过 1')
  assert.equal(taskCounts(job).attention, 0)
  assert.equal(taskStatus(job), '已结束（有跳过）')
  assert.equal(taskTone(job), 'success')
})

test('external subtitle failures remain on the original download card', () => {
  const job = { id: 'download', title: '作品', status: 'partial', createdAt: '2026-10-28T00:00:00Z', config: { accountId: 'main' }, items: [{ file: { id: '1', name: 'video.mkv', mediaType: 'video' }, status: 'done' }], download: { subtitles: [{ file: { name: '01.chs.ass', path: 'animation/show/01.chs.ass' }, targetPath: '/out/video.zh-CN.ass', status: 'uncertain', message: '上传回执丢失' }] } }
  const tasks = historyTasks([job], [], [])
  assert.equal(tasks.length, 1)
  assert.equal(tasks[0].items[1].target, '/out/video.zh-CN.ass')
  assert.equal(taskCounts(tasks[0]).attention, 1)
  assert.equal(taskCounts(tasks[0]).done, 1)
})

test('task identity includes kind; subtitles resolve the parent title without depending on retention', () => {
  const jobs = [{ id: 'same', title: '閃靈二人組', status: 'completed', createdAt: '2026-10-15T00:00:00Z', config: { accountId: 'main', sourcePath: '/in', targetPath: '/out' }, items: [] }]
  const cleanup = [{ id: 'same', accountId: 'main', mode: 'empty', parentPath: '/in/目录', createdAt: '2026-10-14T00:00:00Z', status: 'completed', items: [] }]
  const subtitles = [{ id: 'same', accountId: 'main', jobId: 'same', status: 'completed', createdAt: '2026-10-13T00:00:00Z', items: [{ localId: '0', originalName: 'local.srt', video: { name: 'fallback.mkv' }, targetPath: '/out/local.srt', status: 'skipped' }] }]
  const all = historyTasks(jobs, cleanup, subtitles)
  assert.equal(new Set(all.map(job => job.key)).size, 3)
  assert.equal(all[1].title, '清理空目录 · /in/目录')
  assert.equal(all[2].title, '閃靈二人組 · 字幕导入')
  assert.equal(historyTasks([], [], subtitles)[0].title, 'fallback.mkv · 字幕导入')
})
