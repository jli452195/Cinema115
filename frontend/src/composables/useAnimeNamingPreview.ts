import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeNamingTarget, AnimeSourceMapping } from '../types/animeSubscription'
import type { NamingRecord, NamingResult } from '../types/naming'

interface PreviewInput {
  target: AnimeNamingTarget; mapping: AnimeSourceMapping; group: string
  items: { id: string; title: string; parse: { kind: string; episode: number | null; rawEpisode: string } }[]
}
interface PreviewItem {
  id: string; status: 'mapped' | 'unmapped' | 'ignored'; episodeName?: string
  result?: NamingResult; error?: string
}
interface Preview { naming: NamingRecord; items: PreviewItem[] }

// All loaded releases share one local-only request. Filtering does not change
// its input, and old mappings are never shown while a new request is pending.
export function useAnimeNamingPreview(token: () => string, source: () => PreviewInput | undefined) {
  const value = ref<Preview>(), pending = ref(false), error = ref(''), loadedKey = ref('')
  const key = computed(() => JSON.stringify(source()) || '')
  let sequence = 0, controller: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
  function refresh() {
    const request = ++sequence, input = key.value
    clearTimeout(timer); controller?.abort(); loadedKey.value = ''; error.value = ''; value.value = undefined
    pending.value = !!input
    if (!input) return
    controller = new AbortController()
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(20000)])
    timer = setTimeout(async () => {
      try {
        const result = await animeConfigRequest<Preview>(token(), 'naming/preview', { method: 'POST', body: JSON.parse(input), signal })
        if (request !== sequence) return
        const expected: PreviewInput = JSON.parse(input), known = new Set(expected.items.map(item => item.id)), seen = new Set<string>()
        if (!Number.isSafeInteger(result.naming?.revision) || result.naming.revision < 0 || result.naming.template?.kind !== 'tv' || !Array.isArray(result.items)) throw new Error('命名预览结果无效，请重试')
        for (const item of result.items) {
          if (!known.has(item.id) || seen.has(item.id) || !['mapped', 'unmapped', 'ignored'].includes(item.status) ||
            (item.status === 'mapped' ? !!item.result === !!item.error : !!item.result || !!item.error) ||
            item.result && (!item.result.name || !item.result.stem || item.result.extension !== '' || typeof item.result.directory !== 'string' || typeof item.result.relativePath !== 'string')) throw new Error('命名预览结果不完整，请重试')
          seen.add(item.id)
        }
        if (seen.size !== known.size) throw new Error('命名预览缺少发布，请重试')
        value.value = result; loadedKey.value = input
      } catch (reason) {
        if (request === sequence) error.value = reason instanceof Error ? reason.message : '命名预览失败，请重试'
      } finally { if (request === sequence) pending.value = false }
    }, 300)
  }
  const items = computed(() => loadedKey.value === key.value ? Object.fromEntries((value.value?.items || []).map(item => [item.id, item])) : {})
  watch([key, token], refresh, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => { sequence++; clearTimeout(timer); controller?.abort() })
  return { items, pending, error, refresh }
}
