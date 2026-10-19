import type { AnimeDownloadJob } from '../types/animeSubscription'

export function animeDownloadState(job: AnimeDownloadJob) {
  if (job.workflowState === 'organized') return '已整理'
  if (job.blockCode === 'DOWNLOAD_PAUSED') return '等待恢复订阅'
  if (job.organizationPlanned && job.fileState === 'verified') {
    if (job.blockCode && job.organization?.state !== 'needs_review' && job.organization?.state !== 'unknown') return '整理暂时受阻'
    return animeOrganizationState(job.organization?.state || 'pending')
  }
  if (job.fileState === 'verified') return '文件清单已保存'
  if (job.fileState === 'needs_review') return '需要处理'
  return ({ queued: '待提交', submitting: '正在提交', unknown: '结果待核对', accepted: '115 已接收', downloading: '115 下载中', completed: '网盘已完成', failed: '下载失败', blocked: '任务受阻', cancelled: '已取消' } as Record<string, string>)[job.downloadState] || '状态待核对'
}
export function animeOrganizationState(state: string) {
  return ({ pending: '待整理', working: '正在整理', unknown: '整理结果待核对', needs_review: '整理需要处理', done: '已整理' } as Record<string, string>)[state] || '整理状态待核对'
}
const reasons: Record<string, string> = {
  ORGANIZE_MAPPING_REQUIRED: '文件尚未全部匹配到明确季集，保留原文件，请核对命名计划。',
  ORGANIZE_TARGET_CONFLICT: '来源或目标目录存在同名冲突，整理已停止，不覆盖文件。',
  ORGANIZE_UNCERTAIN: '改名或移动结果未知，只核对已记录的对象，不重复发送原指令。',
  ORGANIZE_READ_FAILED: '读取整理目录失败，保留已完成步骤，稍后继续读取。',
  ORGANIZE_RESULT_UNRESOLVED: '部分结果无法确认，已保存确认成功的步骤，请人工核对；不会重放未确认的指令。',
  DOWNLOAD_PAUSED: '订阅已暂停，保留原任务，恢复后继续。', SUBMIT_UNCERTAIN: '提交结果尚未确认，只核对原任务，不重复提交。', TASK_MISSING: '未找到原离线任务，不自动重新下载。', TASK_READ_FAILED: '读取离线结果失败，保留上次成功记录。', TASK_STATUS_UNKNOWN: '115 返回的状态暂时无法判断。', TASK_RATE_LIMITED: '115 正在限流，核对需等待冷却结束。', TASK_RESULT_PENDING: '网盘已完成，等待取得明确的结果位置。', TASK_ACCESS_DENIED: '当前凭据不能读取，请先检查主账号。', TASK_TARGET_CONFLICT: '发现同一资源的目录冲突，请人工核对。', TASK_RESULT_CHANGED: '结果对象变化，请人工核对。', TASK_STATUS_CONFLICT: '状态与此前完成记录冲突，请人工核对。', DOWNLOAD_ACCOUNT_CHANGED: '任务固定的主账号已变化，请人工处理原账号上的任务。', DOWNLOAD_DIRECTORY_REQUIRED: '下载目录不可用，请检查目录设置。', DIRECTORY_CREATE_UNCERTAIN: '资源目录创建结果未知，将按原目录核对。', DIRECTORY_READ_FAILED: '目录读取失败，保留原步骤。', FILES_READ_FAILED: '读取文件清单失败，可重试读取。', FILES_INCOMPLETE: '文件清单为空或尚未读完整，可重试读取。', FILES_OUTSIDE_TASK: '结果超出本任务目录，请人工核对。', SUBMIT_REJECTED: '115 拒绝提交，请检查网盘配额与账号状态。', STALE_GENERATION: '收到旧执行结果，请人工核对。',
}
export function animeDownloadReason(code: string) { return reasons[code] || '此步骤暂时受阻，请保留原任务并核对。' }
