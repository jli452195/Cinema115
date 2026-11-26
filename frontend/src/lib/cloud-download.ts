import { p115Request } from './p115-api'

interface CloudDownloadReceipt { accepted: boolean }

export interface CloudDownloadTarget {
  id: string
  name: string
  accountId: string
  targetCID: string
  targetPath: string
  ready: boolean
}

export interface CloudDownloadProfilesResponse {
  accountId: string
  profiles: CloudDownloadTarget[]
}

export interface CloudDownloadTargetSnapshot {
  profileId: string
  accountId: string
  targetCID: string
  targetPath: string
}

// 仅用于整理下载暂存目录编辑与旧配置显式导入。
export function loadCloudDownloadConfig(token: string, signal: AbortSignal) {
  return p115Request<{ accountId: string; targetCID: string; targetPath: string }>(token, 'cloud-download/config', {
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
  })
}

export function loadCloudDownloadProfiles(token: string, signal: AbortSignal) {
  return p115Request<CloudDownloadProfilesResponse>(token, 'cloud-download/profiles', {
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
  })
}

export function isCloudTargetChangedError(reason: unknown): boolean {
  const error = reason as { status?: number; code?: string; responseData?: { code?: string } } | null
  if (!error) return false
  return error.code === 'cloud_target_changed' || (error.status === 409 && error.responseData?.code === 'cloud_target_changed')
}

export function isCloudDownloadUncertain(reason: unknown): boolean {
  const error = reason as { outcomeUncertain?: boolean; responseData?: { outcomeUncertain?: boolean } } | null
  return !!(error?.outcomeUncertain || error?.responseData?.outcomeUncertain)
}

// 网页、观影、蜜柑与 TG 共用提交预算、目标快照校验及回执处理。
export async function submitCloudDownload(token: string, target: CloudDownloadTargetSnapshot, url: string, signal: AbortSignal): Promise<void> {
  if (!target.profileId || !target.accountId || !target.targetCID) throw new Error('请先选择有效的云下载目录')
  try {
    const receipt = await p115Request<CloudDownloadReceipt>(token, 'cloud-download/tasks', {
      method: 'POST',
      body: JSON.stringify({ profileId: target.profileId, accountId: target.accountId, targetCID: target.targetCID, url }),
      signal: AbortSignal.any([signal, AbortSignal.timeout(95000)]),
    })
    if (receipt?.accepted !== true) throw Object.assign(new Error('115 未确认接收任务，请前往云下载页面核对'), { outcomeUncertain: true })
  } catch (reason) {
    if (isCloudTargetChangedError(reason)) throw reason
    const httpError = reason as { status?: number; responseData?: { error?: string } } | null
    const gatewayWithoutReceipt = [502, 503, 504].includes(httpError?.status || 0) && !httpError?.responseData?.error
    const unreadableReceipt = !!httpError?.status && httpError.status >= 200 && httpError.status < 300
    if (gatewayWithoutReceipt || unreadableReceipt || reason instanceof TypeError || (reason instanceof Error && ['TimeoutError', 'AbortError'].includes(reason.name))) {
      throw Object.assign(new Error('处理结果尚未确认，请先到 115 云下载核对；不要立即重复提交'), { outcomeUncertain: true })
    }
    throw reason
  }
}

export function cloudDownloadNotice(targetPath: string): string {
  return `已提交 1 条（${!targetPath || targetPath === '/' ? '115 根目录' : targetPath}）`
}
