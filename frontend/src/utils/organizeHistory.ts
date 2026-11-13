import type { DownloadPipeline, OrganizeJob } from '../types/organize'

export interface HistoryItem {
  titleOnly?: boolean
  id: string
  name: string
  source: string
  target?: string
  status: string
  message?: string
  mediaType?: string
  jobId?: string
}
export interface HistoryTask {
  key: string
  title: string
  accountId: string
  source?: string
  target?: string
  status: string
  createdAt: string
  message?: string
  items: HistoryItem[]
  organize?: OrganizeJob
  organizeJobs?: OrganizeJob[]
  pipeline?: DownloadPipeline
}

const itemLabels: Record<string, string> = {
  pending: '未处理', preparing: '检查目录', renaming: '重命名中', renamed: '已改名，尚未移动',
  moving: '移动中', done: '已完成', blocked: '已停止', uncertain: '结果待核实', skipped: '已跳过',
}
export function isActiveTask(task: HistoryTask) { return task.pipeline ? !task.pipeline.finished : ['queued', 'submitting', 'downloading', 'running'].includes(task.status) }
function isActiveItem(item: HistoryItem, task: HistoryTask) {
  const job = task.organizeJobs?.find(job => job.id === item.jobId)
  return job ? ['queued', 'submitting', 'downloading', 'running'].includes(job.status) : isActiveTask(task)
}
export function needsAttention(item: HistoryItem, task: HistoryTask) {
  return item.status === 'uncertain' || item.status === 'blocked' || (!isActiveItem(item, task) && item.status !== 'done' && item.status !== 'skipped')
}
export function taskCounts(task: HistoryTask) {
  const counts = { done: 0, skipped: 0, uncertain: 0, unfinished: 0, processing: 0, attention: 0, total: task.items.length }
  for (const item of task.items) {
    if (item.status === 'done') counts.done++
    else if (item.status === 'skipped') counts.skipped++
    else if (item.status === 'uncertain') counts.uncertain++
    else if (isActiveItem(item, task) && ['preparing', 'renaming', 'renamed', 'moving'].includes(item.status)) counts.processing++
    else counts.unfinished++
    if (needsAttention(item, task)) counts.attention++
  }
  return counts
}
export function taskStatus(task: HistoryTask) {
  if (task.pipeline) return { queued: '排队中', downloading: '下载中', organizing: '整理中', generating: '生成 STRM 中', review: '等待分享审核', success: '已完成', partial: '部分完成', failed: '失败', interrupted: '已中断' }[task.pipeline.status]
  if (task.status === 'submitting') return '提交离线中'
  if (task.status === 'downloading') return '离线中，完成后整理'
  if (task.status === 'queued') return '排队中'
  if (task.status === 'running') return '整理中'
  if (task.status === 'completed') return taskCounts(task).skipped ? '已结束（有跳过）' : '已完成'
  if (task.status === 'partial') return '部分完成，已停止'
  if (task.status === 'interrupted') return '已中断'
  return { uncertain: '结果待核实', blocked: '已停止', preview: '待确认' }[task.status] || task.status
}
export function taskTone(task: HistoryTask) {
  if (isActiveTask(task)) return 'active'
  return (task.pipeline ? task.pipeline.status === 'success' : task.status === 'completed' && !taskCounts(task).attention) ? 'success' : 'warning'
}
export function itemStatus(item: HistoryItem, task: HistoryTask) {
  if (item.status === 'done') return '已完成'
  if (!isActiveItem(item, task) && ['renaming', 'moving'].includes(item.status)) return '操作中断，需核实结果'
  return itemLabels[item.status] || item.status
}
export function resultSummary(task: HistoryTask) {
  if (task.pipeline) {
    const pipeline = task.pipeline
    return [`整理 ${pipeline.organized}/${pipeline.total} 个资源`, pipeline.shareSTRM && `STRM ${pipeline.written} 个`, pipeline.failed > 0 && `失败 ${pipeline.failed} 个资源`].filter(Boolean).join(' · ')
  }
  const c = taskCounts(task)
  const parts = [`完成 ${c.done}/${c.total}`]
  if (c.uncertain) parts.push(`待核实 ${c.uncertain}`)
  if (c.skipped) parts.push(`跳过 ${c.skipped}`)
  if (c.processing) parts.push(`处理中 ${c.processing}`)
  if (c.unfinished) parts.push(`未完成 ${c.unfinished}`)
  return parts.join(' · ')
}
export function activePhase(task: HistoryTask) {
  if (task.pipeline) return taskStatus(task)
  if (task.status === 'queued') return '等待执行'
  const current = task.items.find(item => ['moving', 'renaming', 'preparing'].includes(item.status))
  return current ? itemStatus(current, task) : taskStatus(task)
}
export function historyTasks(jobs: OrganizeJob[], pipelines: DownloadPipeline[] = []): HistoryTask[] {
  const tasks: HistoryTask[] = jobs.map(job => ({
    key: `organize:${job.id}`, title: job.title, accountId: job.config.accountId,
    source: job.config.sourcePath, target: job.config.targetPath, status: job.status, createdAt: job.createdAt,
    message: job.message, organize: job,
    items: [...job.items.map(item => ({ id: item.file.id, name: item.file.name, source: item.file.titleOnly ? '' : item.file.sourcePath, target: item.targetPath, status: item.status, message: item.message, mediaType: item.file.mediaType, titleOnly: item.file.titleOnly })), ...(job.download?.subtitles || []).map((sub, index) => ({ id: `external-subtitle:${index}`, name: sub.file.name, source: `字幕共享计划 / ${sub.file.path}`, target: sub.targetPath, status: sub.status, message: sub.message, mediaType: 'subtitle' }))],
  }))
  const grouped = new Set(pipelines.flatMap(pipeline => pipeline.jobIds))
  const byId = new Map(tasks.map(task => [task.organize!.id, task]))
  const batches: HistoryTask[] = pipelines.map(pipeline => {
    const members = [...new Set(pipeline.jobIds)].map(id => byId.get(id)).filter((task): task is HistoryTask => !!task)
    const first = members[0]
    const items = new Map<string, HistoryItem>()
    for (const task of members) {
      const job = task.organize!, leaderId = job.download?.share?.leaderId || job.id
      for (const item of task.items) {
        // 合并任务的主记录包含成员文件，优先使用主记录的最新结果，字幕按实际来源与目标去重。
        const identity = item.id.startsWith('external-subtitle:') ? JSON.stringify([item.source, item.target]) : item.id
        const id = `${leaderId}:${identity}`
        if (!items.has(id) || job.id === leaderId) items.set(id, { ...item, id, jobId: job.id })
      }
    }
    return {
      key: `pipeline:${pipeline.id}`, title: pipeline.title, createdAt: pipeline.createdAt,
      accountId: first?.accountId || '', target: members.every(task => task.target === first?.target) ? first?.target : undefined, status: pipeline.status,
      message: pipeline.message, pipeline, organizeJobs: members.map(task => task.organize!),
      items: [...items.values()],
    }
  })
  return [...tasks.filter(task => !grouped.has(task.organize!.id)), ...batches].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
}
export function shareTaskStatus(status: string) {
  return ({ queued: '等待生成', running: '生成中', retry_queued: '等待重试', review: '审核中', waiting_review: '等待审核重试', success: '已完成', failed: '失败', interrupted: '已中断', cancelled: '已取消' } as Record<string, string>)[status] || status
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
