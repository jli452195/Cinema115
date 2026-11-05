import { computed, onBeforeUnmount, ref } from 'vue'
import { gyingRequest } from '../types/gying'
import { woniuRequest, WoniuError } from '../types/woniu'
import { mergeResourcePage, type ResourceSearchConfig, type ResourceSearchResult } from '../types/resourceSearch'
import { requestErrorMessage } from '../utils/requestErrorMessage'

interface MediaResourceSearchOptions {
  source: 'gying' | 'woniu'
  token: string
  kind: 'movie' | 'tv'
  tmdbId: string
}

type LoadPhase = '' | 'config' | 'search' | 'more'

export function useMediaResourceSearch(options: MediaResourceSearchOptions, signal: AbortSignal) {
  const sourceLabel = options.source === 'woniu' ? '蜗牛' : '观影'
  const request = options.source === 'woniu' ? woniuRequest : gyingRequest
  const config = ref<ResourceSearchConfig>()
  const result = ref<ResourceSearchResult>()
  const searchEnabled = computed(() => Boolean(config.value?.configured))
  const phase = ref<LoadPhase>('')
  const elapsedSeconds = ref(0)
  const error = ref(''), errorCode = ref('')
  const moreError = ref(''), moreErrorCode = ref('')
  let progressTimer: ReturnType<typeof setInterval> | undefined

  function blocked() { return signal.aborted || phase.value !== '' }

  function finishPhase(expected?: Exclude<LoadPhase, ''>) {
    if (expected && phase.value !== expected) return
    clearInterval(progressTimer)
    progressTimer = undefined
    phase.value = ''
  }

  function beginPhase(next: Exclude<LoadPhase, ''>) {
    finishPhase()
    phase.value = next
    elapsedSeconds.value = 0
    const started = Date.now()
    progressTimer = setInterval(() => { elapsedSeconds.value = Math.floor((Date.now() - started) / 1000) }, 1000)
  }

  async function loadConfig() {
    if (blocked()) return false
    beginPhase('config'); error.value = ''
    try {
      const value = await request<ResourceSearchConfig>(options.token, 'config', signal)
      if (signal.aborted) return false
      config.value = value
      return searchEnabled.value
    } catch (reason) {
      if (!signal.aborted) error.value = requestErrorMessage(reason, '读取' + sourceLabel + '配置')
      return false
    } finally { finishPhase('config') }
  }

  async function search(refresh = false) {
    if (blocked()) return false
    beginPhase('search'); error.value = ''; errorCode.value = ''; moreError.value = ''; moreErrorCode.value = ''
    try {
      const value = await request<ResourceSearchResult>(options.token, 'search', signal, 'POST', { kind: options.kind, tmdbId: options.tmdbId, refresh })
      if (signal.aborted) return false
      result.value = value
      return true
    } catch (reason) {
      if (!signal.aborted) {
        error.value = requestErrorMessage(reason, sourceLabel + '搜索')
        errorCode.value = reason instanceof WoniuError ? reason.code : ''
      }
      return false
    } finally { finishPhase('search') }
  }

  async function loadNextPage() {
    const previous = result.value
    if (blocked() || !previous?.hasMore || !previous.nextPage) return false
    beginPhase('more'); moreError.value = ''; moreErrorCode.value = ''
    try {
      const next = await request<ResourceSearchResult>(options.token, 'search', signal, 'POST', { kind: options.kind, tmdbId: options.tmdbId, page: previous.nextPage, method: previous.matchedBy })
      if (signal.aborted) return false
      result.value = mergeResourcePage(previous, next)
      return true
    } catch (reason) {
      if (!signal.aborted) {
        moreError.value = requestErrorMessage(reason, '加载更多')
        moreErrorCode.value = reason instanceof WoniuError ? reason.code : ''
      }
      return false
    } finally { finishPhase('more') }
  }

  onBeforeUnmount(finishPhase)
  return { config, result, searchEnabled, phase, elapsedSeconds, error, errorCode, moreError, moreErrorCode, loadConfig, search, loadNextPage }
}
