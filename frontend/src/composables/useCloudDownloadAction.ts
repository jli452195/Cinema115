import { computed, ref, type Ref } from 'vue'
import { cloudDownloadNotice, isCloudDownloadUncertain, isCloudTargetChangedError, submitCloudDownload } from '../lib/cloud-download'
import { magnetKey } from '../lib/cloud-download-batch'
import type { CloudDownloadTargetController } from './useCloudDownloadTarget'

export interface CloudDownloadActionStatus {
  busy?: boolean
  success?: boolean
  message?: string
  uncertain?: boolean
  needsConfig?: boolean
}

export function useCloudDownloadAction(
  token: string,
  signal: AbortSignal,
  statuses: Ref<Record<string, CloudDownloadActionStatus>> = ref({}),
  targetController?: CloudDownloadTargetController,
) {
  const pending = computed(() => Object.fromEntries(Object.entries(statuses.value).filter(([, status]) => status.busy).map(([key]) => [key, true])))
  const busy = computed(() => Object.keys(pending.value).length > 0)

  async function submit(url: string, key = magnetKey(url) || url): Promise<CloudDownloadActionStatus | undefined> {
    const current = statuses.value[key]
    if (signal.aborted || current?.busy || current?.success || current?.uncertain) return
    const snapshot = targetController?.snapshot()
    if (!snapshot) {
      const message = targetController?.missingTargetMessage() || '请先在“115 云转存”新增磁力/ED2K 转存目录'
      const needsConfig = !targetController?.targets.value.length && !targetController?.reconfirmMessage.value
      return statuses.value[key] = { message, needsConfig }
    }
    statuses.value[key] = { busy: true }
    try {
      // 提交时固定当前选中的目录快照，切换目录不改写已发出请求的账号、目录与回执。
      await submitCloudDownload(token, snapshot, url, signal)
      if (signal.aborted) return
      return statuses.value[key] = { success: true, message: cloudDownloadNotice(snapshot.targetPath) }
    } catch (reason) {
      if (signal.aborted) return
      const message = reason instanceof Error ? reason.message : '提交 115 云下载失败'
      if (isCloudTargetChangedError(reason)) {
        // 过期目录明确未提交，清空选择并刷新列表等待重新确认，不标记为结果未知。
        void targetController?.handleTargetChanged(snapshot, message)
        return statuses.value[key] = { message, uncertain: false, needsConfig: false }
      }
      const error = reason as { needsConfig?: boolean } | null
      return statuses.value[key] = {
        message,
        uncertain: isCloudDownloadUncertain(reason),
        needsConfig: !!error?.needsConfig || /请先.*(?:新增|配置)|尚未登录|账号.*不存在|凭据/.test(message),
      }
    } finally {
      if (signal.aborted && statuses.value[key]?.busy) delete statuses.value[key]
    }
  }

  return { statuses, pending, busy, submit }
}
