import { computed, ref } from 'vue'
import { useCloudDownloadAction } from './useCloudDownloadAction'
import { cloudDownloadNotice, loadCloudDownloadConfig } from '../lib/cloud-download'
import { submitCloudBatch, trackCloudBatch, type CloudBatchEvent } from '../lib/cloud-download-batch'
import { createED2KSTRMAPI } from '../lib/ed2k-strm-api'
import { ed2kRequestID } from '../utils/ed2kSTRM'
import type { TelegramFile } from '../utils/telegramFiles'
import type { ED2KSTRMState } from '../types/ed2kSTRM'

export interface TelegramFileActionStatus { busy?: boolean; success?: boolean; uncertain?: boolean; message?: string; needsConfig?: boolean }
interface PendingSTRM { requestId: string; targetId: string; accountId: string; outputDir: string; baseUrl: string; files: TelegramFile[]; keys: string[] }

export function useTelegramFileActions(token: string, lifecycle: AbortSignal) {
  const cloud = useCloudDownloadAction(token, lifecycle)
  const api = createED2KSTRMAPI(() => token, lifecycle)
  const state = ref<ED2KSTRMState>(), targetID = ref(''), loading = ref(false)
  const setupError = ref(''), notice = ref(''), running = ref(false), cloudBatchRunning = ref(false)
  const strmStatuses = ref<Record<string, TelegramFileActionStatus>>({})
  const pendingSTRM = ref<PendingSTRM>()
  const attempts = new Map<string, string>()
  let controller: AbortController | undefined
  const busy = computed(() => running.value || cloud.busy.value || loading.value)
  const targets = computed(() => state.value?.targets.map(target => ({ value: target.id, label: target.name })) || [])
  const strmKey = (file: TelegramFile) => `${state.value?.primaryAccountId || ''}/${targetID.value}/${file.id}`
  const strmStatus = (file: TelegramFile) => strmStatuses.value[strmKey(file)]
  const cloudStatus = (file: TelegramFile) => cloud.statuses.value[file.id]
  const cloudEligible = (file: TelegramFile) => !cloudStatus(file)?.busy && !cloudStatus(file)?.success && !cloudStatus(file)?.uncertain
  const strmEligible = (file: TelegramFile) => !strmStatus(file)?.busy && !strmStatus(file)?.success && !strmStatus(file)?.uncertain

  async function loadTargets() {
    if (loading.value || running.value || cloud.busy.value || lifecycle.aborted) return
    loading.value = true; setupError.value = ''
    try {
      const value = await api.state()
      if (lifecycle.aborted) return
      state.value = value
      if (value.targets.length === 1) targetID.value = value.targets[0]!.id
      else if (!value.targets.some(target => target.id === targetID.value)) targetID.value = ''
    } catch (reason) {
      if (!lifecycle.aborted) setupError.value = reason instanceof Error ? reason.message : '读取 STRM 输出目录失败'
    } finally { loading.value = false }
  }

  async function download(files: TelegramFile[]) {
    const rows = files.filter(cloudEligible)
    if (busy.value || !rows.length || lifecycle.aborted) return
    notice.value = ''
    if (rows.length === 1) {
      const status = await cloud.submit(rows[0]!.url, rows[0]!.id)
      if (!lifecycle.aborted && status?.message) notice.value = status.message
      return
    }
    running.value = true; cloudBatchRunning.value = true
    controller = new AbortController()
    const signal = AbortSignal.any([lifecycle, controller.signal])
    const receipts = trackCloudBatch(rows.length)
    let dispatched = false, accepted = 0, failed = 0, uncertain = 0
    try {
      const config = await loadCloudDownloadConfig(token, signal)
      if (!config.accountId || !config.targetCID) throw Object.assign(new Error('请先在 115 云下载保存主账号和下载目录'), { beforeStart: true })
      // 整批交给现有批量接口，在后端固定账号与目录，不逐文件重新读取配置。
      const onEvent = (event: CloudBatchEvent) => {
        receipts.accept(event)
        if (event.type === 'ready' || event.type === 'done') return
        const file = rows[event.index]!
        if (event.type === 'start') {
          cloud.statuses.value[file.id] = { busy: true }
          return
        }
        if (event.accepted) {
          accepted++
          cloud.statuses.value[file.id] = { success: true, message: cloudDownloadNotice(config.targetPath) }
        } else {
          if (event.outcomeUncertain) uncertain++
          else failed++
          cloud.statuses.value[file.id] = { uncertain: event.outcomeUncertain, message: event.error || '提交失败' }
        }
      }
      dispatched = true
      await submitCloudBatch(token, config.accountId, config.targetCID, rows.map(file => file.url), signal, onEvent)
      const counts = [`已提交 ${accepted} 条`]
      if (failed) counts.push(`失败 ${failed} 条`)
      if (uncertain) counts.push(`待核对 ${uncertain} 条`)
      const remaining = rows.length - receipts.completed.size
      if (remaining) counts.push(`已停止后续提交，其余 ${remaining} 条未提交`)
      notice.value = counts.join('，')
    } catch (reason) {
      const error = reason as Error & { beforeStart?: boolean }
      if (dispatched && !receipts.done && !error.beforeStart) {
        for (let i = 0; i < rows.length; i++) if (!receipts.completed.has(i)) {
          cloud.statuses.value[rows[i]!.id] = { uncertain: true, message: '接收结果尚未确认，请先到 115 云下载核对。' }
        }
      }
      if (!lifecycle.aborted) {
        const detail = signal.aborted ? '已停止后续提交，未取得回执的文件请先核对。' : error.message || '批量云下载失败'
        notice.value = `已提交 ${accepted} 条。${detail}`
      }
    } finally { running.value = false; cloudBatchRunning.value = false; controller = undefined }
  }

  async function generate(files: TelegramFile[]) {
    if (busy.value || !files.length || lifecycle.aborted) return
    if (pendingSTRM.value) { notice.value = '上一批 STRM 接收情况尚未确认，请重试原批次或先查看 STRM 任务。'; return }
    running.value = true; notice.value = ''
    const selectedTarget = targetID.value
    let rows: TelegramFile[] = [], keys: string[] = [], packet: PendingSTRM | undefined
    try {
      const value = await api.state()
      if (lifecycle.aborted) return
      state.value = value
      if (!value.accountReady) throw new Error('请先在 115 助手登录主账号')
      if (!value.settingsSaved) throw new Error('请先保存 ED2K STRM 播放地址')
      if (!value.targets.length) { targetID.value = ''; throw new Error('请先添加 ED2K STRM 输出目录') }
      const target = value.targets.length === 1 ? value.targets[0]!.id : selectedTarget
      if (!target || !value.targets.some(entry => entry.id === target)) {
        targetID.value = ''; throw new Error('请选择上方的 STRM 输出目录，然后提交。')
      }
      targetID.value = target
      rows = files.filter(strmEligible).sort((a, b) => a.id.localeCompare(b.id))
      if (!rows.length) return
      keys = rows.map(strmKey)
      const fingerprint = keys.map((key, index) => `${key}/${rows[index]!.url}`).join('|')
      const requestId = attempts.get(fingerprint) || ed2kRequestID()
      attempts.set(fingerprint, requestId)
      for (const key of keys) strmStatuses.value[key] = { busy: true }
      packet = { requestId, targetId: target, accountId: value.primaryAccountId, outputDir: value.targets.find(entry => entry.id === target)!.outputDir, baseUrl: value.settings.baseUrl, files: [...rows], keys: [...keys] }
      await api.imports({ targetId: target, requestId, links: rows.map(file => file.url) })
      attempts.delete(fingerprint)
      for (const key of keys) strmStatuses.value[key] = { success: true, message: '已接收生成任务，文件资料就绪后写入所选目录。' }
      notice.value = `已接收 ${rows.length} 个文件的 STRM 生成任务。`
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : '提交 ED2K STRM 失败'
      const rejected = reason as { status?: number; responseData?: { error?: string } }
      const definiteRejection = [400, 401, 403, 404, 409, 413, 422].includes(rejected?.status || 0) && !!rejected?.responseData?.error
      const uncertain = !!packet && !definiteRejection
      if (uncertain) pendingSTRM.value = packet
      for (const key of keys) strmStatuses.value[key] = { message, uncertain }
      if (!lifecycle.aborted) notice.value = uncertain ? 'STRM 接收情况尚未确认。可重试原批次，或先到 STRM 任务核对。' : message
    } finally { running.value = false }
  }
  async function retrySTRM() {
    const packet = pendingSTRM.value
    if (!packet || busy.value || lifecycle.aborted) return
    running.value = true
    try {
      const current = await api.state()
      if (current.primaryAccountId !== packet.accountId || !current.targets.some(target => target.id === packet.targetId && target.outputDir === packet.outputDir)) throw new Error('原批次的主账号或输出目录已变化，请先查看 STRM 任务核对结果。')
      if (current.settings.baseUrl !== packet.baseUrl) throw new Error('原批次的播放地址已变化，请先查看 STRM 任务核对结果。')
      // 回执丢失后只重放原始请求编号和完整文件集合，禁止拆分成新输出请求。
      for (const key of packet.keys) strmStatuses.value[key] = { busy: true }
      await api.imports({ targetId: packet.targetId, requestId: packet.requestId, links: packet.files.map(file => file.url) })
      for (const key of packet.keys) strmStatuses.value[key] = { success: true, message: '已确认接收原 STRM 批次。' }
      pendingSTRM.value = undefined
      notice.value = `已确认接收 ${packet.files.length} 个文件的原 STRM 批次。`
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : '原 STRM 批次接收情况尚未确认'
      for (const key of packet.keys) strmStatuses.value[key] = { uncertain: true, message }
      if (!lifecycle.aborted) notice.value = message
    } finally { running.value = false }
  }
  return { state, targetID, targets, loading, setupError, notice, busy, running, cloudBatchRunning, pendingSTRM, retrySTRM, cloudStatus, strmStatus, cloudEligible, strmEligible, loadTargets, download, generate, stop: () => controller?.abort() }
}
