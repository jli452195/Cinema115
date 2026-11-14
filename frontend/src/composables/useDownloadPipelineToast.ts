import { fetchDownloadPipelines } from '../lib/download-pipeline'
import type { DownloadPipeline } from '../types/organize'
import { createTaskToast, type TaskToastHandle } from './useTaskToast'

interface TrackedPipeline {
  token: string
  id: string
  summary: string
  errors: number
  last?: string
  toast: TaskToastHandle
}

const tracked = new Map<string, TrackedPipeline>()
let polling = false
let timer: ReturnType<typeof setTimeout> | undefined

const stages: Record<DownloadPipeline['status'], string> = {
  queued: '等待下载',
  downloading: '正在下载',
  organizing: '正在整理',
  generating: '正在生成 STRM',
  review: '分享等待审核',
  success: '下载整理完成',
  partial: '下载整理部分完成',
  failed: '下载整理失败',
  interrupted: '任务待核对',
}

/** 仅使用作品标题与资源/视频数量构建两行摘要，不拼接冗长阶段日志 */
function formatPipelineSummary(fallback: string, pipeline: DownloadPipeline): string {
  const title = pipeline.title?.trim()
  if (!title) return fallback

  const total = pipeline.total ?? 0
  const videoCount = pipeline.videoCount ?? 0
  const countText = total > 1
    ? `等 ${total} 项资源`
    : videoCount > 0
      ? `${videoCount} 个视频`
      : ''

  return [title, countText].filter(Boolean).join(' · ')
}

function getFinishTitle(pipeline: DownloadPipeline): string {
  if (pipeline.status === 'success') {
    return pipeline.shareSTRM ? '下载整理并生成 STRM 完成' : '下载整理完成'
  }
  if (pipeline.status === 'partial') {
    return pipeline.shareSTRM ? '整理与 STRM 部分完成' : '下载整理部分完成'
  }
  return stages[pipeline.status] || '任务待核对'
}

function update(task: TrackedPipeline, pipeline: DownloadPipeline) {
  const summary = formatPipelineSummary(task.summary, pipeline)
  task.summary = summary

  const version = JSON.stringify([pipeline.status, summary, pipeline.finished, pipeline.shareSTRM])
  if (version === task.last) return
  task.last = version

  if (pipeline.finished) {
    const finishTitle = getFinishTitle(pipeline)
    if (pipeline.status === 'success') {
      task.toast.finish('success', finishTitle, undefined, summary)
    } else if (pipeline.status === 'failed') {
      const detail = [pipeline.message?.trim(), '请在下载整理记录中核对'].filter(Boolean).join(' · ')
      task.toast.finish('error', finishTitle, detail, summary)
    } else {
      const detail = [pipeline.message?.trim(), '请在下载整理记录中核对结果'].filter(Boolean).join(' · ')
      task.toast.finish('warning', finishTitle, detail, summary)
    }
    return
  }

  task.toast.update(stages[pipeline.status] || '正在处理后台任务', summary)
}

async function poll() {
  if (polling || !tracked.size) return
  clearTimeout(timer)
  polling = true
  try {
    const groups = new Map<string, TrackedPipeline[]>()
    for (const task of tracked.values()) {
      groups.set(task.token, [...(groups.get(task.token) || []), task])
    }
    await Promise.all([...groups].map(async ([token, tasks]) => {
      try {
        const pipelines = await fetchDownloadPipelines(token, tasks.map(task => task.id), AbortSignal.timeout(15000))
        for (const task of tasks) {
          if (tracked.get(task.id) !== task) continue
          const pipeline = pipelines.find(value => value.id === task.id || value.jobIds.includes(task.id))
          if (!pipeline) {
            unavailable(task)
            continue
          }
          task.errors = 0
          update(task, pipeline)
          // 仅移除已完成项，不影响其他仍在跟踪的批次轮询
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
  task.toast.finish(
    'warning',
    '任务进度暂不可用',
    unauthorized ? '请重新登录后查看下载整理记录' : '后台任务继续执行，请在下载整理记录中核对结果',
    task.summary,
  )
  tracked.delete(task.id)
}

// 跟踪属于已提交的后台批次，关闭搜索或整理弹窗不会中止。
export function trackDownloadPipeline(token: string, id: string, summary: string, restart = false) {
  const existing = tracked.get(id)
  if (existing && !restart) return
  existing?.toast.dismiss()

  const task: TrackedPipeline = {
    token,
    id,
    summary,
    errors: 0,
    toast: createTaskToast('已加入后台下载整理', summary),
  }
  tracked.set(id, task)
  void poll()
}
