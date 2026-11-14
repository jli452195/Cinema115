import { onBeforeUnmount, onMounted, readonly, ref, watch } from 'vue'
import { fetchFollowPendingCount } from '../lib/follow-counts'
import { isPortalAdmin, portalAuthError, portalToken } from './usePortalAuth'

const pending = ref<number>()
export const followPendingCount = readonly(pending)
let started = false, blocked = false, epoch = 0, lastAttempt = 0, queued = false
let timer: ReturnType<typeof setTimeout> | undefined
let request: Promise<void> | undefined
let controller: AbortController | undefined
const allowed = () => started && !!portalToken.value && isPortalAdmin.value && !portalAuthError.value && !blocked

function schedule(delay = 60000) {
  clearTimeout(timer)
  if (allowed() && !document.hidden) timer = setTimeout(() => { void refreshFollowPendingCount() }, delay)
}

function reset() {
  epoch++
  clearTimeout(timer)
  controller?.abort()
  controller = undefined; request = undefined; queued = false; lastAttempt = 0
  pending.value = undefined
}

export async function refreshFollowPendingCount(force = false): Promise<void> {
  if (!allowed() || document.hidden) return
  if (request) {
    // 操作完成时若有旧查询在途，再读一次，不能让操作前的结果盖住新状态。
    if (force) queued = true
    await request
    return
  }
  const elapsed = Date.now() - lastAttempt
  if (!force && lastAttempt && elapsed < 15000) {
    schedule(60000 - elapsed)
    return
  }
  clearTimeout(timer)
  const token = portalToken.value, generation = epoch, current = new AbortController()
  controller = current
  lastAttempt = Date.now()
  const operation = (async () => {
    try {
      const count = await fetchFollowPendingCount(token, AbortSignal.any([current.signal, AbortSignal.timeout(15000)]))
      if (generation === epoch && token === portalToken.value && allowed() && !current.signal.aborted && !queued) pending.value = count
    } catch (error) {
      if (generation !== epoch || token !== portalToken.value || current.signal.aborted) return
      const status = (error as { status?: number }).status
      if (status === 401 || status === 403) { blocked = true; pending.value = undefined }
      // 临时异常保留已有数量；首次读取失败保持未知，不弹出重复通知。
    } finally {
      if (generation === epoch) {
        request = undefined; controller = undefined
        if (queued && allowed() && !document.hidden) {
          queued = false
          await refreshFollowPendingCount(true)
        } else { queued = false; schedule() }
      }
    }
  })()
  request = operation
  await operation
}

// 仅由 App 挂载一次，桌面导航和手机抽屉都只读取共享数量。
export function useFollowPendingCountSync() {
  let stop: (() => void) | undefined
  const resume = () => {
    if (document.hidden) clearTimeout(timer)
    else void refreshFollowPendingCount()
  }
  onMounted(() => {
    started = true
    stop = watch([portalToken, isPortalAdmin, portalAuthError], () => {
      reset(); blocked = false
      void refreshFollowPendingCount(true)
    }, { immediate: true })
    window.addEventListener('focus', resume)
    document.addEventListener('visibilitychange', resume)
  })
  onBeforeUnmount(() => {
    started = false
    stop?.(); reset()
    window.removeEventListener('focus', resume)
    document.removeEventListener('visibilitychange', resume)
  })
}
