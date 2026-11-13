import { onBeforeUnmount, onMounted, watch } from 'vue'

// 仅在页面可见时轮询本地接口；请求结束后再计时，由调用方取消进行中的请求。
export function useAnimeLocalPolling(read: () => Promise<unknown>, stop: () => void, enabled = () => true, interval = () => 30000) {
  let timer: ReturnType<typeof setTimeout> | undefined, disposed = false
  function cancel() { clearTimeout(timer); timer = undefined }
  function schedule() {
    cancel()
    if (!disposed && !document.hidden) timer = setTimeout(async () => {
      try { if (enabled()) await read() } finally { schedule() }
    }, interval())
  }
  function visibility() {
    cancel()
    if (document.hidden) { stop(); return }
    if (enabled()) void read().finally(schedule)
    else schedule()
  }
  watch(interval, schedule)
  onMounted(() => { document.addEventListener('visibilitychange', visibility); schedule() })
  onBeforeUnmount(() => { disposed = true; cancel(); document.removeEventListener('visibilitychange', visibility) })
}
