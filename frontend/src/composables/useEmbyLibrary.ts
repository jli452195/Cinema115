import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import type { MediaCardItem } from '../types/media'
import { embyRequest, type EmbyMatch } from '../types/emby'

// 同一批卡片只查一次本地索引，不逐张访问 Emby。
export function useEmbyLibrary(token: () => string, items: Ref<MediaCardItem[]>) {
  const matches = ref<Record<string, EmbyMatch>>({})
  let controller: AbortController | undefined
  watch(() => [token(), items.value.map(item => `${item.kind}:${item.id}`).join(',')], async () => {
    controller?.abort(); matches.value = {}
    if (!items.value.length) return
    const current = new AbortController(); controller = current
    try {
      const batch = items.value.map(item => ({ kind: item.kind, tmdbId: String(item.id) }))
      // 长系列按接口上限分批，避免超过一百条后整个入库检查失败。
      for (let offset = 0; offset < batch.length; offset += 100) {
        if (current.signal.aborted) return
        const data = await embyRequest(token(), 'library-check', current.signal, { items: batch.slice(offset, offset + 100) })
        if (!current.signal.aborted && Array.isArray(data.items)) matches.value = { ...matches.value, ...Object.fromEntries(data.items.map((item: EmbyMatch) => [`${item.kind}:${item.tmdbId}`, item])) }
      }
    } catch { /* 入库检查失败不影响 TMDB 浏览，也不显示未入库。 */ }
  }, { immediate: true })
  onBeforeUnmount(() => controller?.abort())
  return matches
}
