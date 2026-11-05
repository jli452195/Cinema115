import { computed, ref, type Ref } from 'vue'
import { cloudDownloadNotice, loadCloudDownloadConfig, submitCloudDownload } from '../lib/cloud-download'
import { magnetKey } from '../lib/cloud-download-batch'

export interface CloudDownloadActionStatus {
  busy?: boolean
  success?: boolean
  message?: string
  uncertain?: boolean
  needsConfig?: boolean
}

export function useCloudDownloadAction(token: string, signal: AbortSignal, statuses: Ref<Record<string, CloudDownloadActionStatus>> = ref({})) {
  const pending = computed(() => Object.fromEntries(Object.entries(statuses.value).filter(([, status]) => status.busy).map(([key]) => [key, true])))
  const busy = computed(() => Object.keys(pending.value).length > 0)

  async function submit(url: string, key = magnetKey(url) || url): Promise<CloudDownloadActionStatus | undefined> {
    const current = statuses.value[key]
    if (signal.aborted || current?.busy || current?.success || current?.uncertain) return
    statuses.value[key] = { busy: true }
    try {
      // 每次提交只读取一次已保存的目标，配置变化不改写本次账号、目录与回执提示。
      const { accountId, targetCID, targetPath } = await loadCloudDownloadConfig(token, signal)
      if (signal.aborted) return
      if (!accountId || !targetCID) throw Object.assign(new Error('请先配置主账号，并在“115 云下载”保存下载目录'), { needsConfig: true })
      const receipt = await submitCloudDownload(token, accountId, url, targetCID, signal)
      if (signal.aborted) return
      return statuses.value[key] = { success: true, message: cloudDownloadNotice(receipt, targetPath) }
    } catch (reason) {
      if (signal.aborted) return
      const error = reason as { outcomeUncertain?: boolean; needsConfig?: boolean; responseData?: { outcomeUncertain?: boolean } } | null
      const message = reason instanceof Error ? reason.message : '提交 115 云下载失败'
      return statuses.value[key] = {
        message,
        uncertain: !!(error?.outcomeUncertain || error?.responseData?.outcomeUncertain),
        needsConfig: !!error?.needsConfig || /请先.*(?:选择|配置)|尚未登录|账号.*不存在|配置|凭据/.test(message),
      }
    } finally {
      if (signal.aborted && statuses.value[key]?.busy) delete statuses.value[key]
    }
  }

  return { statuses, pending, busy, submit }
}
