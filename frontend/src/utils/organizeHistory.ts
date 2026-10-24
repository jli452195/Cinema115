import type { OrganizeCleanup, OrganizeJob } from '../types/manualOrganize'
import type { SubtitleImport } from '../types/subtitleImport'

export type HistoryKind = 'organize' | 'cleanup' | 'subtitle'
export interface HistoryItem {
  titleOnly?: boolean
  id: string
  name: string
  source: string
  target?: string
  status: string
  message?: string
  mediaType?: string
}
export interface HistoryTask {
  key: string
  kind: HistoryKind
  title: string
  accountId: string
  source?: string
  target?: string
  status: string
  createdAt: string
  message?: string
  items: HistoryItem[]
  organize?: OrganizeJob
}

export const historyLabels: Record<HistoryKind, string> = { organize: '整理', cleanup: '清理', subtitle: '字幕' }
const itemLabels: Record<string, string> = {
  pending: '未处理', preparing: '检查目录', renaming: '重命名中', renamed: '已改名，尚未移动',
  moving: '移动中', done: '已完成', blocked: '已停止', uncertain: '结果待核实', skipped: '已跳过',
}
export function isActiveTask(task: HistoryTask) { return ['queued', 'submitting', 'downloading', 'running'].includes(task.status) }
export function needsAttention(item: HistoryItem, task: HistoryTask) {
  return item.status === 'uncertain' || item.status === 'blocked' || (!isActiveTask(task) && item.status !== 'done' && item.status !== 'skipped')
}
export function taskCounts(task: HistoryTask) {
  const counts = { done: 0, skipped: 0, uncertain: 0, unfinished: 0, processing: 0, attention: 0, total: task.items.length }
  for (const item of task.items) {
    if (item.status === 'done') counts.done++
    else if (item.status === 'skipped') counts.skipped++
    else if (item.status === 'uncertain') counts.uncertain++
    else if (isActiveTask(task) && ['preparing', 'renaming', 'renamed', 'moving'].includes(item.status)) counts.processing++
    else counts.unfinished++
    if (needsAttention(item, task)) counts.attention++
  }
  return counts
}
export function taskStatus(task: HistoryTask) {
  if (task.status === 'submitting') return '提交离线中'
  if (task.status === 'downloading') return '离线中，完成后整理'
  if (task.status === 'queued') return '排队中'
  if (task.status === 'running') return { organize: '整理中', cleanup: '清理中', subtitle: '上传中' }[task.kind]
  if (task.status === 'completed') return taskCounts(task).skipped ? '已结束（有跳过）' : '已完成'
  if (task.status === 'partial') return '部分完成，已停止'
  if (task.status === 'interrupted') return '已中断'
  return { uncertain: '结果待核实', blocked: '已停止', preview: '待确认' }[task.status] || task.status
}
export function taskTone(task: HistoryTask) {
  if (isActiveTask(task)) return 'active'
  return task.status === 'completed' && !taskCounts(task).attention ? 'success' : 'warning'
}
export function itemStatus(item: HistoryItem, task: HistoryTask) {
  if (item.status === 'done') return { organize: '已完成', cleanup: '已移入回收站', subtitle: '已上传' }[task.kind]
  if (!isActiveTask(task) && ['renaming', 'moving'].includes(item.status)) return '操作中断，需核实结果'
  return itemLabels[item.status] || item.status
}
export function resultSummary(task: HistoryTask) {
  const c = taskCounts(task)
  const verb = { organize: '完成', cleanup: '已移入回收站', subtitle: '已上传' }[task.kind]
  const parts = [`${verb} ${c.done}/${c.total}`]
  if (c.uncertain) parts.push(`待核实 ${c.uncertain}`)
  if (c.skipped) parts.push(`跳过 ${c.skipped}`)
  if (c.processing) parts.push(`处理中 ${c.processing}`)
  if (c.unfinished) parts.push(`未完成 ${c.unfinished}`)
  return parts.join(' · ')
}
export function activePhase(task: HistoryTask) {
  if (task.status === 'queued') return '等待执行'
  const current = task.items.find(item => ['moving', 'renaming', 'preparing'].includes(item.status))
  return current ? itemStatus(current, task) : taskStatus(task)
}
export function historyTasks(jobs: OrganizeJob[], cleanup: OrganizeCleanup[], subtitles: SubtitleImport[]): HistoryTask[] {
  const tasks: HistoryTask[] = jobs.map(job => ({
    key: `organize:${job.id}`, kind: 'organize', title: job.title, accountId: job.config.accountId,
    source: job.config.sourcePath, target: job.config.targetPath, status: job.status, createdAt: job.createdAt,
    message: job.message, organize: job,
    items: job.items.map(item => ({ id: item.file.id, name: item.file.name, source: item.file.titleOnly ? '' : item.file.sourcePath, target: item.targetPath, status: item.status, message: item.message, mediaType: item.file.mediaType, titleOnly: item.file.titleOnly })),
  }))
  for (const job of cleanup) tasks.push({
    key: `cleanup:${job.id}`, kind: 'cleanup', title: `${job.mode === 'empty' ? '清理空目录' : '删除所选条目'} · ${job.parentPath || '未记录目录'}`,
    accountId: job.accountId, source: job.parentPath, status: job.status, createdAt: job.createdAt, message: job.message,
    items: job.items.map(item => ({ id: item.id, name: item.name, source: item.path, status: item.status, message: item.message })),
  })
  for (const job of subtitles) tasks.push({
    key: `subtitle:${job.id}`, kind: 'subtitle', title: `${jobs.find(parent => parent.id === job.jobId)?.title || job.items[0]?.video.name || '字幕'} · 字幕导入`,
    accountId: job.accountId, status: job.status, createdAt: job.createdAt, message: job.message,
    items: job.items.map(item => ({ id: item.localId, name: item.originalName, source: item.originalName, target: item.targetPath, status: item.status, message: item.message, mediaType: 'subtitle' })),
  })
  return tasks.sort((a, b) => b.createdAt.localeCompare(a.createdAt))
}
export function historyTime(value: string, full = false) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '时间未记录'
  return date.toLocaleString('zh-CN', full
    ? { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }
    : { ...(date.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' as const } : {}), month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
export function historyDuration(ms: number) {
  const seconds = Math.max(0, Math.round(ms / 1000))
  return seconds < 60 ? `${(Math.max(0, ms) / 1000).toFixed(1)} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
}
