import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { namingRequest } from '../lib/naming-api'
import type { NamingKind, NamingRecord, NamingResult } from '../types/naming'

interface MediaNamingInput {
  kind: NamingKind; tmdbId: number
  files: { id: string; name: string; season: number; episode: number; titleOnly?: boolean; namingValues?: Record<string, string> }[]
  subtitles: { file: { id: string; name: string }; videoId?: string; tail?: string }[]
}
interface MediaNamingPreview { naming: NamingRecord; items: { id: string; result?: NamingResult; error?: string }[] }

// One request for the full displayed list; selecting/filtering rows doesn't
// rerender names. Preview has no drive reads, torrent parsing or file mutations.
export function useMediaNamingPreview(token: string, source: () => MediaNamingInput | undefined, lifecycle: AbortSignal) {
  const value = ref<MediaNamingPreview>(), pending = ref(false), error = ref(''), loadedKey = ref('')
  const key = computed(() => JSON.stringify(source()) || '')
  let sequence = 0, controller: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
  function refresh() {
    const request = ++sequence, input = key.value
    clearTimeout(timer); controller?.abort(); error.value = ''; loadedKey.value = ''
    pending.value = !!input
    if (!input) { value.value = undefined; return }
    controller = new AbortController()
    const signal = AbortSignal.any([lifecycle, controller.signal, AbortSignal.timeout(20000)])
    timer = setTimeout(async () => {
      try {
        const result = await namingRequest<MediaNamingPreview>(token, '/media-preview', { method: 'POST', body: input, signal })
        if (request !== sequence || lifecycle.aborted) return
        const expected: MediaNamingInput = JSON.parse(input)
        const known = new Set([...expected.files.map(file => file.id), ...expected.subtitles.filter(sub => sub.videoId).map(sub => sub.file.id)])
        const seen = new Set<string>()
        if (!Number.isSafeInteger(result.naming?.revision) || result.naming.revision < 0 || result.naming.template?.kind !== expected.kind || !Array.isArray(result.items)) throw new Error('命名预览结果无效，请重试')
        for (const item of result.items) {
          if (!known.has(item.id) || seen.has(item.id) || !!item.result === !!item.error || item.result && (!item.result.name || !item.result.stem || typeof item.result.directory !== 'string' || typeof item.result.relativePath !== 'string')) throw new Error('命名预览结果不完整，请重试')
          seen.add(item.id)
        }
        if (expected.files.some(file => !seen.has(file.id))) throw new Error('命名预览缺少文件，请重试')
        const renderedVideos = new Set(result.items.filter(item => item.result).map(item => item.id))
        if (expected.subtitles.some(sub => sub.videoId && renderedVideos.has(sub.videoId) && !seen.has(sub.file.id))) throw new Error('命名预览缺少关联字幕，请重试')
        value.value = result; loadedKey.value = input
      } catch (reason) {
        if (request === sequence && !lifecycle.aborted) error.value = reason instanceof Error ? reason.message : '命名预览失败，请重试'
      } finally { if (request === sequence && !lifecycle.aborted) pending.value = false }
    }, 300)
  }
  const results = computed(() => Object.fromEntries((value.value?.items || []).filter(item => item.result).map(item => [item.id, item.result!])))
  const errors = computed(() => Object.fromEntries((value.value?.items || []).filter(item => item.error).map(item => [item.id, item.error!])))
  const ready = computed(() => !!key.value && loadedKey.value === key.value && !pending.value && !error.value)
  watch(key, refresh, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => { sequence++; clearTimeout(timer); controller?.abort() })
  return { value, pending, error, results, errors, ready, refresh }
}
