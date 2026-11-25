import { p115Request } from './p115-api'

interface CloudDownloadReceipt { accepted: boolean }

export function loadCloudDownloadConfig(token: string, signal: AbortSignal) {
  return p115Request<{ accountId: string; targetCID: string; targetPath: string }>(token, 'cloud-download/config', {
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
  })
}

// 网页、观影和蜜柑共用提交预算及回执校验。
export async function submitCloudDownload(token: string, accountId: string, url: string, targetCID: string, signal: AbortSignal): Promise<void> {
  if (!accountId) throw new Error('请先在 115 助手中配置并登录主账号')
  try {
    const receipt = await p115Request<CloudDownloadReceipt>(token, `cloud-download/tasks?accountId=${encodeURIComponent(accountId)}`, {
      method: 'POST', body: JSON.stringify({ url, targetCID }),
      signal: AbortSignal.any([signal, AbortSignal.timeout(95000)]),
    })
    if (receipt?.accepted !== true) throw Object.assign(new Error('115 未确认接收任务，请前往云下载页面核对'), { outcomeUncertain: true })
  } catch (reason) {
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
