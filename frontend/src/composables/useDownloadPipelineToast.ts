import { toast } from 'vue-sonner'
import { fetchDownloadPipelines } from '../lib/download-pipeline'
import type { DownloadPipeline } from '../types/organize'

interface TrackedPipeline { token: string; id: string; summary: string; errors: number; last?: string }
const tracked = new Map<string, TrackedPipeline>()
let polling = false
let timer: ReturnType<typeof setTimeout> | undefined

const stages: Record<DownloadPipeline['status'], string> = {
  queued: '等待下载', downloading: '正在下载', organizing: '正在整理', generating: '正在生成 STRM',
  review: '分享等待审核', success: '任务完成', partial: '任务部分完成', failed: '任务失败', interrupted: '任务待核对',
}

function update(task: TrackedPipeline, pipeline: DownloadPipeline) {
  const title = stages[pipeline.status]
  const description = [task.summary, pipeline.message].filter(Boolean).join(' · ')
  const version = JSON.stringify([pipeline.status, description, pipeline.finished])
  if (version !== task.last) {
    const options = { id: `download-pipeline:${task.id}`, description, classes: { description: 'task-resource-summary' }, richColors: true,
      duration: 6000 }
    if (pipeline.status === 'success' && pipeline.finished) toast.success(title, options)
    else if (pipeline.status === 'failed' && pipeline.finished) toast.error(title, options)
    else if (pipeline.status === 'review' || pipeline.finished) toast.warning(title, options)
    else toast.loading(title, options)
    task.last = version
  }
}

async function poll() {
  if (polling || !tracked.size) return
  clearTimeout(timer)
  polling = true
  try {
    const groups = new Map<string, TrackedPipeline[]>()
    for (const task of tracked.values()) groups.set(task.token, [...(groups.get(task.token) || []), task])
    await Promise.all([...groups].map(async ([token, tasks]) => {
      try {
        const pipelines = await fetchDownloadPipelines(token, tasks.map(task => task.id), AbortSignal.timeout(15000))
        for (const task of tasks) {
          if (tracked.get(task.id) !== task) continue
          const pipeline = pipelines.find(value => value.id === task.id || value.jobIds.includes(task.id))
          if (!pipeline) { unavailable(task); continue }
          task.errors = 0
          update(task, pipeline)
          if (pipeline.finished) tracked.delete(task.id)
        }
      } catch (error) {
        for (const task of tasks) unavailable(task, (error as { status?: number }).status === 401)
      }
    }))
  } finally {
    polling = false
    if (tracked.size) timer = setTimeout(() => void poll(), 2500)
  }
}

function unavailable(task: TrackedPipeline, unauthorized = false) {
  if (tracked.get(task.id) !== task) return
  if (++task.errors < 5 && !unauthorized) return
  toast.warning('任务进度暂不可用', {
    id: `download-pipeline:${task.id}`, description: `${task.summary} · ${unauthorized ? '请重新登录后查看下载整理记录' : '后台任务继续执行，请在下载整理记录中核对结果'}`,
    classes: { description: 'task-resource-summary' }, richColors: true, duration: 6000,
  })
  tracked.delete(task.id)
}

// 跟踪属于已提交的后台批次，关闭搜索或整理弹窗不会中止。
export function trackDownloadPipeline(token: string, id: string, summary: string, restart = false) {
  if (tracked.has(id) && !restart) return
  const task: TrackedPipeline = { token, id, summary, errors: 0 }
  tracked.set(id, task)
  // loading 类型本身暂停倒计时，避免 Infinity 被同 ID 的最终通知沿用。
  toast.loading('正在更新任务进度', {
    id: `download-pipeline:${id}`, description: summary, duration: 6000, classes: { description: 'task-resource-summary' },
  })
  void poll()
}
