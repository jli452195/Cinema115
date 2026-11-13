import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const code = ts.transpileModule(readFileSync(new URL('../src/utils/organizeHistory.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const module = { exports: {} }
new Function('module', 'exports', code)(module, module.exports)
const { historyTasks, isActiveTask, isCleanupOnly, taskCounts, taskStatus, taskTone, resultSummary, needsAttention, itemStatus } = module.exports
const task = (statuses, status = 'partial') => ({ key: 'organize:1', status, items: statuses.map((status, i) => ({ id: String(i), status })) })

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
  const expected = historyTasks([job])[0].items[0]
  assert.equal(expected.source, '')
  assert.equal(expected.target, '/out/作品 - S01E12')
  assert.equal(expected.titleOnly, true)
  Object.assign(job.items[0].file, { id: '901', sourcePath: '/staging/video.mkv', titleOnly: false })
  assert.equal(historyTasks([job])[0].items[0].source, '/staging/video.mkv')
})

test('skipped files never count as completed or cause a false failure warning', () => {
  const job = task(['done', 'done', 'skipped'], 'completed')
  assert.equal(resultSummary(job), '完成 2/3 · 跳过 1')
  assert.equal(taskCounts(job).attention, 0)
  assert.equal(taskStatus(job), '已结束（有跳过）')
  assert.equal(taskTone(job), 'success')
})

test('external subtitle failures remain on the original download card', () => {
  const job = { id: 'download', title: '作品', status: 'partial', createdAt: '2026-10-28T00:00:00Z', config: { accountId: 'main' }, items: [{ file: { id: '1', name: 'video.mkv', mediaType: 'video' }, status: 'done' }], download: { subtitles: [{ file: { name: '01.chs.ass', path: 'animation/show/01.chs.ass' }, targetPath: '/out/video.zh-CN.ass', status: 'uncertain', message: '上传回执丢失' }] } }
  const tasks = historyTasks([job])
  assert.equal(tasks.length, 1)
  assert.equal(tasks[0].items[1].target, '/out/video.zh-CN.ass')
  assert.equal(taskCounts(tasks[0]).attention, 1)
  assert.equal(taskCounts(tasks[0]).done, 1)
})

test('download task identities stay stable when records are sorted by creation time', () => {
  const first = { id: 'first', title: '閃靈二人組', status: 'completed', createdAt: '2026-10-15T00:00:00Z', config: { accountId: 'main', sourcePath: '/in', targetPath: '/out' }, items: [] }
  const second = { ...first, id: 'second', createdAt: '2026-10-16T00:00:00Z' }
  assert.deepEqual(historyTasks([first, second]).map(task => task.key), ['organize:second', 'organize:first'])
  assert.equal(historyTasks([second])[0].title, first.title)
})

const downloadJob = (id, status = 'completed') => ({
  id, title: `资源 ${id}`, status, createdAt: '2026-11-13T00:00:00Z', resumable: status === 'partial',
  config: { accountId: 'main', sourcePath: `/in/${id}`, targetPath: '/out' },
  items: [{ file: { id: 'same-file-id', name: `${id}.mkv`, mediaType: 'video' }, status: status === 'completed' ? 'done' : 'pending' }],
})
const pipeline = (overrides = {}) => ({
  id: 'batch', title: '一次提交', createdAt: '2026-11-13T00:00:00Z', jobIds: ['a', 'b'],
  shareSTRM: true, status: 'generating', finished: false, message: '',
  total: 2, ready: 2, organized: 2, failed: 0, videoCount: 2, written: 1, shares: [], ...overrides,
})

test('one submission groups its resources without hiding unrelated organize records', () => {
  const jobs = [downloadJob('a'), downloadJob('b'), downloadJob('ordinary')]
  const tasks = historyTasks(jobs, [pipeline({ jobIds: ['a', 'b', 'a'] })])
  assert.equal(tasks.length, 2)
  const batch = tasks.find(task => task.pipeline)
  assert.equal(batch.key, 'pipeline:batch')
  assert.deepEqual(batch.organizeJobs, jobs.slice(0, 2))
  assert.deepEqual(batch.items.map(item => item.jobId), ['a', 'b'])
  assert.equal(new Set(batch.items.map(item => item.id)).size, 2)
  assert.equal(tasks.find(task => !task.pipeline).organize, jobs[2])
})

test('organize completion keeps the batch active until STRM completes', () => {
  const [batch] = historyTasks([downloadJob('a'), downloadJob('b')], [pipeline()])
  assert.equal(taskCounts(batch).done, 2)
  assert.equal(isActiveTask(batch), true)
  assert.equal(taskStatus(batch), '生成 STRM 中')
  assert.equal(taskTone(batch), 'active')
  assert.equal(resultSummary(batch), '整理 2/2 个资源 · STRM 1 个')
  batch.pipeline.status = 'success'; batch.pipeline.finished = true
  assert.equal(taskTone(batch), 'success')
  assert.equal(taskStatus(batch), '已完成')
})

test('failed resources stay actionable while other resources in their batch are downloading', () => {
  const failed = downloadJob('a', 'partial'), waiting = downloadJob('b', 'downloading')
  const [batch] = historyTasks([failed, waiting], [pipeline({ status: 'downloading', ready: 0, organized: 0, failed: 1 })])
  assert.equal(taskCounts(batch).attention, 1)
  assert.equal(needsAttention(batch.items[0], batch), true)
  assert.equal(needsAttention(batch.items[1], batch), false)
  assert.equal(batch.organizeJobs.find(job => job.resumable), failed)
})

test('a failed STRM phase cannot appear successful because all resource files were organized', () => {
  const [batch] = historyTasks([downloadJob('a'), downloadJob('b')], [pipeline({ status: 'failed', finished: true })])
  assert.equal(taskCounts(batch).done, 2)
  assert.equal(taskTone(batch), 'warning')
  assert.equal(taskStatus(batch), '失败')
  assert.equal(isActiveTask(batch), false)
})

test('different resource directories cannot be presented as a shared batch destination', () => {
  const a = downloadJob('a'), b = downloadJob('b')
  b.config.targetPath = '/other-task-directory'
  const [batch] = historyTasks([a, b], [pipeline()])
  assert.equal(batch.target, undefined)
  assert.equal(batch.organizeJobs[1].config.targetPath, '/other-task-directory')
})

test('merged leader files and external subtitles count once and use the leader outcome', () => {
  const leader = downloadJob('a'), member = downloadJob('b')
  leader.items[0].file.id = 'first-file'
  member.items[0].file.id = 'second-file'
  leader.items.push({ ...member.items[0], status: 'uncertain', message: '主记录待核实' })
  const subtitle = { file: { name: '02.ass', path: 'show/02.ass' }, targetPath: '/out/02.ass', status: 'done' }
  leader.download = { share: { leaderId: 'a' }, subtitles: [{ ...subtitle, file: { name: '01.ass', path: 'show/01.ass' }, targetPath: '/out/01.ass' }, { ...subtitle, status: 'uncertain' }] }
  member.download = { share: { leaderId: 'a' }, subtitles: [subtitle] }
  const [batch] = historyTasks([member, leader], [pipeline({ jobIds: ['b', 'a'] })])
  assert.equal(batch.items.length, 4)
  assert.equal(taskCounts(batch).done, 2)
  assert.equal(taskCounts(batch).uncertain, 2)
  assert.equal(batch.items.find(item => item.name === 'b.mkv').message, '主记录待核实')
  assert.equal(batch.items.find(item => item.name === '02.ass').status, 'uncertain')
  assert.equal(batch.items.find(item => item.name === '02.ass').jobId, leader.id)
})

test('外部字幕明细使用原文件名，不显示内部附件编号或误标共享计划', () => {
  const job = downloadJob('a')
  job.download = { subtitles: [{ file: { name: '01.zh.ass', path: 'private-local-attachment-id' }, targetPath: '/out/01.zh.ass', status: 'done' }] }
  const subtitle = historyTasks([job])[0].items.find(item => item.mediaType === 'subtitle')
  assert.equal(subtitle.source, '外部字幕 / 01.zh.ass')
  assert.ok(!JSON.stringify(subtitle).includes('private-local-attachment-id'))
})

test('清理重试只适用于文件和字幕完成、分享已交接的原资源', () => {
  const job = downloadJob('a')
  job.download = { cleanupDirs: ['staging'] }
  assert.equal(isCleanupOnly(job), true)
  job.download.subtitles = [{ status: 'pending' }]
  assert.equal(isCleanupOnly(job), false)
  job.download.subtitles[0].status = 'done'
  job.download.share = { handedOff: false }
  assert.equal(isCleanupOnly(job), false)
  job.download.share.handedOff = true
  assert.equal(isCleanupOnly(job), true)
  job.download.cleanupDirs = []
  assert.equal(isCleanupOnly(job), false)
  assert.equal(isCleanupOnly(undefined), false)
})
