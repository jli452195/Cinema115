import { computed, ref } from 'vue'
import { cloudDownloadNotice, isCloudTargetChangedError } from '../lib/cloud-download'
import { magnetKey, submitCloudBatch, trackCloudBatch, type CloudBatchEvent } from '../lib/cloud-download-batch'
import type { CloudDownloadTargetController } from './useCloudDownloadTarget'

export interface BatchResource { id: string; title: string; magnet: string }
export interface BatchStatus { busy?: boolean; success?: boolean; uncertain?: boolean; message?: string; needsConfig?: boolean }

export function useCloudDownloadBatch(token: string, lifecycle: AbortSignal, targetController?: CloudDownloadTargetController) {
  const mode = ref(false), running = ref(false)
  const selected = ref(new Map<string, BatchResource>())
  const statuses = ref<Record<string, BatchStatus>>({})
  const notice = ref(''), needsConfig = ref(false)
  const total = ref(0), processed = ref(0), accepted = ref(0), failed = ref(0), uncertain = ref(0)
  let controller: AbortController | undefined
  const count = computed(() => selected.value.size)
  const uncheckedCount = computed(() => Object.values(statuses.value).filter(state => state.uncertain).length)
  const eligible = (item: BatchResource) => {
    const key = magnetKey(item.magnet), state = statuses.value[key]
    return !!key && !state?.busy && !state?.success && !state?.uncertain
  }
  function toggle(item: BatchResource) {
    if (running.value) return
    notice.value = ''
    const key = magnetKey(item.magnet)
    if (selected.value.has(key)) { selected.value.delete(key); return }
    if (!eligible(item)) return
    selected.value.set(key, { ...item })
  }
  function selectAll(items: BatchResource[]) {
    if (running.value) return
    notice.value = ''
    const available = [...new Map(items.filter(eligible).map(item => [magnetKey(item.magnet), item])).values()]
    if (available.length && available.every(item => selected.value.has(magnetKey(item.magnet)))) {
      for (const item of available) selected.value.delete(magnetKey(item.magnet))
      return
    }
    for (const item of available) {
      const key = magnetKey(item.magnet)
      if (selected.value.has(key)) continue
      selected.value.set(key, { ...item })
    }
  }
  function clear() { if (!running.value) { selected.value.clear(); notice.value = '' } }
  function exit() { if (!running.value) { clear(); mode.value = false; notice.value = '' } }
  function stop() { controller?.abort() }
  function allowReselectAfterCheck() {
    if (running.value) return
    for (const [key, state] of Object.entries(statuses.value)) {
      if (state.uncertain) delete statuses.value[key]
    }
    notice.value = '已解除待核对标记，可重新选择尚未接收的资源。'
  }
  async function submit() {
    if (running.value || !count.value) return
    const rows = [...selected.value.values()].filter(eligible)
    if (!rows.length) return
    const snapshot = targetController?.snapshot()
    if (!snapshot) {
      notice.value = targetController?.missingTargetMessage() || '请先在“115 云转存”新增磁力/ED2K 转存目录'
      needsConfig.value = !targetController?.targets.value.length && !targetController?.reconfirmMessage.value
      return
    }
    running.value = true; notice.value = ''; needsConfig.value = false
    total.value = rows.length; processed.value = 0; accepted.value = 0; failed.value = 0; uncertain.value = 0
    controller = new AbortController()
    const signal = AbortSignal.any([lifecycle, controller.signal])
    let dispatched = false
    const receipts = trackCloudBatch(rows.length)
    try {
      const onEvent = (event: CloudBatchEvent) => {
        receipts.accept(event)
        if (event.type === 'ready' || event.type === 'done') return
        const key = magnetKey(rows[event.index]!.magnet)
        if (event.type === 'start') {
          statuses.value[key] = { busy: true }
          return
        }
        processed.value++
        if (event.accepted) {
          statuses.value[key] = { success: true, message: cloudDownloadNotice(snapshot.targetPath) }
          selected.value.delete(key); accepted.value++
        } else {
          statuses.value[key] = { uncertain: event.outcomeUncertain, message: event.error || '提交失败' }
          if (event.outcomeUncertain) { selected.value.delete(key); uncertain.value++ } else failed.value++
        }
      }
      dispatched = true
      await submitCloudBatch(token, snapshot, rows.map(item => item.magnet), signal, onEvent)
      const counts = [`已提交 ${accepted.value} 条`]
      if (failed.value) counts.push(`失败 ${failed.value} 条`)
      if (uncertain.value) counts.push(`待核对 ${uncertain.value} 条`)
      if (processed.value < total.value) counts.push(`已停止后续提交，其余 ${total.value - processed.value} 条保留选择`)
      notice.value = counts.join('，')
    } catch (reason) {
      const error = reason as Error & { beforeStart?: boolean; needsConfig?: boolean }
      if (isCloudTargetChangedError(reason)) {
        if (!lifecycle.aborted) void targetController?.handleTargetChanged(snapshot, error.message)
        notice.value = error.message || '所选下载目录已变更或删除，本次未提交，请重新确认目录。'
        return
      }
      // 回执可能被缓冲，连接中断后所有未确认的资源都需核对。
      if (dispatched && !error.beforeStart && !receipts.done) {
        const unknown = rows.map((_, index) => index).filter(index => !receipts.completed.has(index))
        for (const index of unknown) {
          const key = magnetKey(rows[index].magnet)
          statuses.value[key] = { uncertain: true, message: '提交结果尚未确认，请先到 115 云下载核对。' }
          selected.value.delete(key); uncertain.value++
        }
      }
      needsConfig.value = !!error.needsConfig || /配置|凭据|登录/.test(error.message)
      const detail = signal.aborted
        ? dispatched ? `已停止提交，待核对 ${uncertain.value} 条` : '已停止，已保留选择'
        : error.message || '批量提交失败'
      notice.value = `已提交 ${accepted.value} 条。${detail}`
    } finally {
      running.value = false; controller = undefined
    }
  }
  return { mode, running, selected, statuses, notice, needsConfig, total, processed, accepted, failed, uncertain, uncheckedCount, count, eligible, toggle, selectAll, clear, exit, stop, allowReselectAfterCheck, submit }
}
