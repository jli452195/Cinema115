import { onBeforeUnmount, onMounted } from 'vue'

// Poll only our local API while visible. A single timeout avoids overlapping
// reads, and the owner cancels its own in-flight read when hidden/unmounted.
export function useAnimeLocalPolling(read: () => Promise<unknown>, stop: () => void, enabled = () => true) {
  let timer: ReturnType<typeof setTimeout> | undefined, disposed = false
  function cancel() { clearTimeout(timer); timer = undefined }
  function schedule() {
    cancel()
    if (!disposed && !document.hidden) timer = setTimeout(async () => {
      if (enabled()) await read()
      schedule()
    }, 30000)
  }
  function visibility() {
    cancel()
    if (document.hidden) { stop(); return }
    if (enabled()) void read().finally(schedule)
    else schedule()
  }
  onMounted(() => { document.addEventListener('visibilitychange', visibility); schedule() })
  onBeforeUnmount(() => { disposed = true; cancel(); document.removeEventListener('visibilitychange', visibility) })
}
