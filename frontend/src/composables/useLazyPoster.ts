import { computed, inject, onBeforeUnmount, onMounted, provide, ref, watch, type InjectionKey, type Ref } from 'vue'
import { tmdbImageURL } from '../utils/tmdbImage'

interface PosterEntry { url: string; users: number }
interface PosterLease { url: string; release: () => void }
interface PosterTask { reader: AbortController; users: number; promise: Promise<PosterEntry | undefined> }
const posterCacheKey: InjectionKey<ReturnType<typeof createPosterCache>> = Symbol('missing-posters')

function thumbnailURL(source: string) {
  return tmdbImageURL(source).replace(/^\/api\/tmdb\/image\/(?:w185|w300|w342|w500|w1280)\//, '/api/tmdb/image/w185/')
}

// Current cards pin their URLs; the owner determines the cache lifetime.
export function createPosterCache(token: () => string) {
  const ready = new Map<string, PosterEntry>(), pending = new Map<string, PosterTask>(), missing = new Map<string, number>()
  function trim() {
    for (const [address, entry] of ready) {
      if (ready.size <= 48) break
      if (entry.users) continue
      ready.delete(address); URL.revokeObjectURL(entry.url)
    }
  }
  function touch(address: string, entry: PosterEntry) { ready.delete(address); ready.set(address, entry) }
  function retain(address: string, entry: PosterEntry): PosterLease {
    touch(address, entry); entry.users++
    let released = false
    return { url: entry.url, release: () => { if (!released) { released = true; entry.users--; trim() } } }
  }
  function cached(source: string) {
    const address = thumbnailURL(source), entry = ready.get(address)
    return entry ? retain(address, entry) : undefined
  }
  function cancelPending() {
    for (const task of pending.values()) task.reader.abort()
    pending.clear()
  }
  function clear() {
    cancelPending()
    for (const entry of ready.values()) URL.revokeObjectURL(entry.url)
    ready.clear(); missing.clear()
  }
  async function ensure(source: string, signal: AbortSignal): Promise<PosterEntry | undefined> {
    const address = thumbnailURL(source)
    if (signal.aborted || !address) return
    const entry = ready.get(address)
    if (entry) { touch(address, entry); return entry }
    if ((missing.get(address) || 0) > Date.now()) return
    missing.delete(address)
    const authenticated = address.startsWith('/api/admin/emby/posters/'), credential = token()
    if (authenticated && !credential) return
    let task = pending.get(address)
    if (!task) {
      const reader = new AbortController()
      const next: PosterTask = { reader, users: 0, promise: Promise.resolve(undefined) }
      pending.set(address, next)
      next.promise = (async () => {
        let objectURL = ''
        try {
          const response = await fetch(address, {
            headers: authenticated ? { Authorization: `Bearer ${credential}` } : undefined,
            signal: AbortSignal.any([reader.signal, AbortSignal.timeout(20000)]),
            referrerPolicy: 'no-referrer',
          })
          if (reader.signal.aborted) return
          if (response.status === 404) {
            if (missing.size >= 512) missing.delete(missing.keys().next().value!)
            missing.set(address, Date.now() + 5 * 60 * 1000)
          }
          if (!response.ok) return
          const blob = await response.blob()
          if (reader.signal.aborted || !blob.type.startsWith('image/')) return
          objectURL = URL.createObjectURL(blob)
          const image = new Image(); image.src = objectURL
          await image.decode()
          if (reader.signal.aborted) return
          const decoded = { url: objectURL, users: 0 }
          ready.set(address, decoded); objectURL = ''; trim()
          return decoded
        } catch {
          // Optional images never prevent reading detection results.
        } finally {
          if (objectURL) URL.revokeObjectURL(objectURL)
          if (pending.get(address) === next) pending.delete(address)
        }
      })()
      task = next
    }
    task.users++
    const current = task
    return new Promise<PosterEntry | undefined>(resolve => {
      let settled = false
      function finish(value?: PosterEntry) {
        if (settled) return
        settled = true; signal.removeEventListener('abort', abort); current.users--
        if (!current.users && pending.get(address) === current) { pending.delete(address); current.reader.abort() }
        resolve(value)
      }
      function abort() { finish() }
      signal.addEventListener('abort', abort, { once: true })
      void current.promise.then(value => finish(signal.aborted ? undefined : value))
      if (signal.aborted) finish()
    })
  }
  async function acquire(source: string, signal: AbortSignal) {
    const entry = await ensure(source, signal)
    const address = thumbnailURL(source)
    return entry && !signal.aborted && ready.get(address) === entry ? retain(address, entry) : undefined
  }
  async function prepare(sources: string[], signal: AbortSignal) {
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      // A slow image must not hold the page indefinitely. Keep its shared request
      // alive for the incoming card; navigation/unmount still cancels its owner.
      await Promise.race([
        Promise.all(sources.map(source => ensure(source, signal))),
        new Promise<void>(resolve => { timer = setTimeout(resolve, 1500) }),
      ])
    } finally { clearTimeout(timer) }
  }
  return { cached, acquire, prepare, cancelPending, clear }
}

export function providePosterCache(token: () => string, retained?: ReturnType<typeof createPosterCache>) {
  const cache = retained || createPosterCache(token)
  provide(posterCacheKey, cache)
  watch(token, cache.clear, { flush: 'sync' })
  onBeforeUnmount(() => { if (!retained) cache.clear() })
  return cache
}

// The poster and ambient background always share a single decoded object URL.
export function useLazyPoster(element: Ref<HTMLElement | undefined>, source: () => string, token: () => string) {
  const shared = inject(posterCacheKey, undefined), cache = shared || createPosterCache(token)
  if (!shared) watch(token, cache.clear, { flush: 'sync' })
  const visible = ref(false), imageURL = ref('')
  const thumbnail = computed(() => thumbnailURL(source()))
  let observer: IntersectionObserver | undefined
  onMounted(() => {
    if (!element.value) return
    if (typeof IntersectionObserver === 'undefined') { visible.value = true; return }
    observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) { visible.value = true; observer?.disconnect() }
    }, { rootMargin: '100px' })
    observer.observe(element.value)
  })
  onBeforeUnmount(() => { observer?.disconnect(); if (!shared) cache.clear() })
  watch([visible, thumbnail, token], async ([show, address], _previous, onCleanup) => {
    const reader = new AbortController()
    let lease: PosterLease | undefined
    onCleanup(() => { reader.abort(); lease?.release() })
    imageURL.value = ''
    lease = cache.cached(address)
    if (lease) { imageURL.value = lease.url; return }
    if (!show) return
    lease = await cache.acquire(address, reader.signal)
    if (reader.signal.aborted) { lease?.release(); return }
    imageURL.value = lease?.url || ''
  }, { immediate: true })
  return imageURL
}
