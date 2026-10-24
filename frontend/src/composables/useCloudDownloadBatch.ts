import { computed, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice } from '../lib/cloud-download'
import { CLOUD_BATCH_LIMIT, magnetKey, readCloudBatch, type CloudBatchEvent } from '../lib/cloud-download-batch'

export interface BatchResource { id: string; title: string; magnet: string }
export interface BatchStatus { busy?: boolean; success?: boolean; uncertain?: boolean; message?: string; needsConfig?: boolean }

export function useCloudDownloadBatch(token: string, lifecycle: AbortSignal) {
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
    if (count.value >= CLOUD_BATCH_LIMIT) { notice.value = '每批最多选择 100 项，请分批提交。'; return }
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
      if (count.value >= CLOUD_BATCH_LIMIT) { notice.value = '每批最多选择 100 项，已保留当前选择。'; break }
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
    running.value = true; notice.value = ''; needsConfig.value = false
    total.value = rows.length; processed.value = 0; accepted.value = 0; failed.value = 0; uncertain.value = 0
    controller = new AbortController()
    const signal = AbortSignal.any([lifecycle, controller.signal])
    let dispatched = false, activeIndex: number | undefined
    const completed = new Set<number>()
    let receivedDone = false
    try {
      const config = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(token, 'cloud-download/config', { signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]) })
      if (!config.accountId || !config.targetCID) throw Object.assign(new Error('请先在 115 云下载配置主账号和下载目录'), { beforeStart: true, needsConfig: true })
      const onEvent = (event: CloudBatchEvent) => {
        if (event.total !== rows.length) throw new Error('批量提交回执数量异常')
        if (event.type === 'ready') return
        if (event.type === 'done') {
          if (activeIndex !== undefined || (!event.stopped && completed.size !== rows.length)) throw new Error('批量提交缺少资源回执')
          receivedDone = true; return
        }
        const item = rows[event.index]
        if (!item || completed.has(event.index)) throw new Error('批量提交回执编号异常')
        const key = magnetKey(item.magnet)
        if (event.type === 'start') {
          if (activeIndex !== undefined) throw new Error('批量提交回执顺序异常')
          activeIndex = event.index; statuses.value[key] = { busy: true }; return
        }
        if (activeIndex !== event.index) throw new Error('批量提交回执顺序异常')
        completed.add(event.index); activeIndex = undefined; processed.value++
        if (event.accepted) {
          statuses.value[key] = { success: true, message: cloudDownloadNotice({ accepted: true, removedOldTask: event.removedOldTask }, config.targetPath) }
          selected.value.delete(key); accepted.value++
        } else {
          statuses.value[key] = { uncertain: event.outcomeUncertain, message: event.error || '提交失败' }
          if (event.outcomeUncertain) { selected.value.delete(key); uncertain.value++ } else failed.value++
        }
      }
      dispatched = true
      const response = await fetch(`/api/admin/p115/cloud-download/tasks/batch?accountId=${encodeURIComponent(config.accountId)}`, {
        method: 'POST', cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ urls: rows.map(item => item.magnet), targetCID: config.targetCID }), signal,
      })
      await readCloudBatch(response, onEvent)
      if (processed.value === total.value) notice.value = `已提交 ${accepted.value}，失败 ${failed.value}，待核对 ${uncertain.value}。下载进度请到 115 查看。`
      else notice.value = `已停止后续提交：成功 ${accepted.value}，失败 ${failed.value}，待核对 ${uncertain.value}；其余 ${total.value - processed.value} 项保留选择。`
    } catch (reason) {
      const error = reason as Error & { beforeStart?: boolean; needsConfig?: boolean }
      // Without a start receipt the stream may have been buffered: every
      // unacknowledged row could have reached the server, so none is auto-retried.
      if (dispatched && !error.beforeStart && !receivedDone) {
        const unknown = rows.map((_, index) => index).filter(index => !completed.has(index))
        for (const index of unknown) {
          const key = magnetKey(rows[index].magnet)
          statuses.value[key] = { uncertain: true, message: '提交结果尚未确认，请先到 115 云下载核对。' }
          selected.value.delete(key); uncertain.value++
        }
      }
      needsConfig.value = !!error.needsConfig || /配置|凭据|登录/.test(error.message)
      notice.value = signal.aborted ? dispatched ? `已停止提交。成功 ${accepted.value}，其余 ${uncertain.value} 项接收情况需先核对。` : '已停止，资源尚未提交，已保留选择。' : error.message || '批量提交失败'
    } finally {
      running.value = false; controller = undefined
    }
  }
  return { mode, running, selected, statuses, notice, needsConfig, total, processed, accepted, failed, uncertain, uncheckedCount, count, eligible, toggle, selectAll, clear, exit, stop, allowReselectAfterCheck, submit }
}
