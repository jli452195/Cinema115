import { computed, ref, watch } from 'vue'
import { telegramUserRequest, TelegramSearchError, type TelegramChannelResource, type TelegramChannelSourceResult } from '../types/telegramUser'
import { telegramFileSource, type TelegramFileResource } from '../utils/telegramFiles'
import { canonicalTelegramSourceURL, telegramSourceCandidates, type TelegramSourceCandidate } from '../utils/telegramSources'

export interface TelegramSourceStatus { busy?: boolean; error?: string }
export interface TelegramChannelSourcesOptions {
  token: string
  signal: AbortSignal
  kind: 'movie' | 'tv'
  tmdbId: string
  resources: () => TelegramChannelResource[]
  enabled: () => boolean
  onExpired?: (error: unknown) => void
}
interface SourceTask { url: string; generation: number; promise: Promise<void>; finish: () => void }

export function useTelegramChannelSources(options: TelegramChannelSourcesOptions) {
  const candidates = computed(() => telegramSourceCandidates(options.resources()))
  const pages = ref<Record<string, TelegramChannelSourceResult>>({})
  const statuses = ref<Record<string, TelegramSourceStatus>>({})
  const paused = ref(false)
  let generation = 0, pumpScheduled = false
  let controller = new AbortController()
  const active = new Map<string, SourceTask>(), queued = new Map<string, SourceTask>()
  let queue: SourceTask[] = []

  const resolvedItems = computed<TelegramFileResource[]>(() => candidates.value.flatMap(candidate => {
    const page = pages.value[candidate.url]
    if (!page) return []
    const seen = new Set<string>()
    const fileSources = candidate.references.map(reference => telegramFileSource(reference.item)).filter(source => {
      const identity = JSON.stringify([source.id, source.url])
      if (seen.has(identity)) return false
      seen.add(identity); return true
    })
    // 每个版本只展开一次文件集合，来源另行聚合，避免跨帖重复解析大型清单。
    return page.groups.map(group => ({
      id: `${candidate.url}/${group.id}`, title: page.pageTitle, version: group.title, text: '',
      channel: candidate.item.channel, sourceUrl: candidate.item.sourceUrl, postedAt: candidate.item.postedAt,
      links: group.links, fileSources,
    }))
  }))
  const progress = computed(() => {
    let loaded = 0, failed = 0, loading = 0, references = 0
    for (const candidate of candidates.value) {
      references += candidate.references.length
      if (pages.value[candidate.url]) loaded++
      else if (statuses.value[candidate.url]?.busy) loading++
      else if (statuses.value[candidate.url]?.error) failed++
    }
    const total = candidates.value.length
    return { total, loaded, failed, loading, pending: total - loaded - failed - loading, references }
  })

  function task(url: string): SourceTask {
    let finish!: () => void
    const promise = new Promise<void>(resolve => { finish = resolve })
    return { url, generation, promise, finish }
  }
  function schedule() {
    if (pumpScheduled || options.signal.aborted || paused.value || !options.enabled()) return
    pumpScheduled = true
    queueMicrotask(() => { pumpScheduled = false; pump() })
  }
  function owns(request: SourceTask) { return !options.signal.aborted && request.generation === generation }

  async function read(request: SourceTask, candidate: TelegramSourceCandidate) {
    const signal = AbortSignal.any([options.signal, controller.signal])
    active.set(request.url, request)
    statuses.value[request.url] = { busy: true }
    try {
      const value = await telegramUserRequest<TelegramChannelSourceResult>(options.token, 'channels/resolve', signal, {
        kind: options.kind, tmdbId: options.tmdbId, resourceId: candidate.item.id, sourceId: candidate.source.id,
      })
      if (!owns(request)) return
      pages.value[request.url] = value
      statuses.value[request.url] = {}
    } catch (reason) {
      if (!owns(request)) return
      statuses.value[request.url] = { error: reason instanceof Error && reason.message ? reason.message : '读取资源清单失败' }
      if (reason instanceof TelegramSearchError && reason.code === 'channel_search_expired') options.onExpired?.(reason)
    } finally {
      if (active.get(request.url) === request) active.delete(request.url)
      request.finish()
      if (owns(request)) schedule()
    }
  }

  function pump() {
    if (options.signal.aborted || paused.value || !options.enabled()) return
    while (active.size < 2) {
      let request = queue.shift()
      if (request) queued.delete(request.url)
      if (!request && !paused.value && options.enabled()) {
        const candidate = candidates.value.find(candidate => !pages.value[candidate.url] && !statuses.value[candidate.url]?.error && !active.has(candidate.url) && !queued.has(candidate.url))
        if (candidate) request = task(candidate.url)
      }
      if (!request) return
      const candidate = candidates.value.find(candidate => candidate.url === request.url)
      if (!owns(request) || !candidate || pages.value[request.url]) { request.finish(); continue }
      void read(request, candidate)
    }
  }

  function resolve(candidate: TelegramSourceCandidate): Promise<void> {
    const url = canonicalTelegramSourceURL(candidate.url)
    if (!url || options.signal.aborted || !candidates.value.some(entry => entry.url === url) || pages.value[url]) return Promise.resolve()
    const existing = active.get(url) || queued.get(url)
    if (existing) return existing.promise
    const request = task(url)
    queued.set(url, request); queue.push(request)
    statuses.value[url] = {}
    schedule()
    return request.promise
  }
  function pause() { paused.value = true }
  function resume() { paused.value = false; schedule() }
  async function retryFailed() {
    await Promise.all(candidates.value.filter(candidate => statuses.value[candidate.url]?.error).map(resolve))
  }
  function cancel() {
    generation++
    controller.abort()
    for (const request of [...active.values(), ...queue]) request.finish()
    active.clear(); queued.clear(); queue = []
  }
  function reset() {
    cancel(); controller = new AbortController()
    pages.value = {}; statuses.value = {}; paused.value = false
    schedule()
  }
  function cancelPending() {
    cancel(); controller = new AbortController()
    // 搜索期间隔离旧请求回执，已经取得的页面及明确失败信息继续保留。
    for (const [url, status] of Object.entries(statuses.value)) if (status.busy) statuses.value[url] = {}
    schedule()
  }
  const stop = watch([candidates, options.enabled, paused], schedule, { immediate: true, flush: 'sync' })
  options.signal.addEventListener('abort', () => { cancel(); stop() }, { once: true })
  if (options.signal.aborted) stop()

  return { candidates, pages, statuses, resolvedItems, progress, paused, resolve, pause, resume, retryFailed, reset, cancelPending }
}
