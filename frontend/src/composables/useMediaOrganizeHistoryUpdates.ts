import { onBeforeUnmount, onMounted, watch } from 'vue'
import { mediaOrganizeHistoryEvents, MediaOrganizeRequestError } from '../lib/media-organize-api'

export function useMediaOrganizeHistoryUpdates(token: () => string, refresh: () => Promise<boolean>, available: () => boolean) {
  let mounted = false, connected = false, pending = false, reading = false, failed = false, denied = false, retryDelay = 1000
  let connection: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined, retry: ReturnType<typeof setTimeout> | undefined, fallback: ReturnType<typeof setInterval> | undefined

  // 固定窗口合并通知；查询在途或正在编辑操作时只保留一次待刷新。
  function schedule() {
    if (!mounted || document.hidden || !pending || reading || !available() || timer !== undefined) return
    timer = setTimeout(async () => {
      timer = undefined
      if (!mounted || document.hidden || reading || !available()) return
      pending = false; reading = true
      try { failed = !await refresh() } finally { reading = false; schedule() }
    }, 1000)
  }
  function changed() { pending = true; schedule() }
  async function connect() {
    if (!mounted || document.hidden || denied || connection && !connection.signal.aborted) return
    const session = new AbortController()
    connection = session
    try {
      await mediaOrganizeHistoryEvents(token(), session.signal, event => {
        if (session.signal.aborted) return
        if (event === 'ready') { connected = true; retryDelay = 1000 }
        changed()
      })
    } catch (reason) {
      if (!session.signal.aborted && connection === session && reason instanceof MediaOrganizeRequestError && [401, 403].includes(reason.status)) denied = true
    } finally {
      if (!session.signal.aborted && connection === session) {
        connection = undefined; connected = false
        if (!denied) { retry = setTimeout(connect, retryDelay); retryDelay = Math.min(retryDelay * 2, 30000) }
      }
    }
  }
  function stop() {
    connection?.abort(); connected = false
    clearTimeout(retry); clearTimeout(timer); timer = undefined
  }
  function visible() {
    if (document.hidden) stop()
    else { changed(); void connect() }
  }
  watch(available, schedule)
  watch(token, () => { stop(); denied = false; retryDelay = 1000; changed(); void connect() })
  onMounted(() => {
    mounted = true; changed(); void connect()
    fallback = setInterval(() => { if ((!connected || failed) && !denied) changed() }, 15000)
    document.addEventListener('visibilitychange', visible)
  })
  onBeforeUnmount(() => { mounted = false; stop(); clearInterval(fallback); document.removeEventListener('visibilitychange', visible) })
}
