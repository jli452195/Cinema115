import { p115Request } from './p115-api'

interface CloudDownloadReceipt { accepted: boolean; removedOldTask?: boolean }

// 网页、观影和蜜柑共用提交预算及提示；旧任务处理由后端统一决定。
export async function submitCloudDownload(token: string, accountId: string, url: string, targetCID: string, signal: AbortSignal): Promise<CloudDownloadReceipt> {
  if (!accountId) throw new Error('请先在 115 助手中配置并登录主账号')
  try {
    const receipt = await p115Request<CloudDownloadReceipt>(token, `cloud-download/tasks?accountId=${encodeURIComponent(accountId)}`, {
      method: 'POST', body: JSON.stringify({ url, targetCID }),
      signal: AbortSignal.any([signal, AbortSignal.timeout(95000)]),
    })
    if (!receipt.accepted) throw Object.assign(new Error('115 未确认接收任务，请前往云下载页面核对'), { outcomeUncertain: true })
    return receipt
  } catch (reason) {
    const httpError = reason as { status?: number; responseData?: { error?: string } } | null
    const gatewayWithoutReceipt = [502, 503, 504].includes(httpError?.status || 0) && !httpError?.responseData?.error
    if (gatewayWithoutReceipt || reason instanceof TypeError || (reason instanceof Error && ['TimeoutError', 'AbortError'].includes(reason.name))) {
      throw Object.assign(new Error('处理结果尚未确认，请先到 115 云下载核对；不要立即重复提交'), { outcomeUncertain: true })
    }
    throw reason
  }
}

export function cloudDownloadNotice(receipt: CloudDownloadReceipt, targetPath: string): string {
  const operation = receipt.removedOldTask ? '已重新提交到 115 云下载' : '已提交到 115 云下载'
  const preserved = receipt.removedOldTask ? '；旧任务记录已移除，原有网盘文件已保留' : ''
  return `${operation}：${targetPath || '115 根目录'}${preserved}。提交成功不代表下载已完成。`
}
