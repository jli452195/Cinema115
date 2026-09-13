import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { embyRequest, type EmbyMatch } from '../types/emby'

export function useEmbyMedia(token: () => string, kind: () => 'tv' | 'movie', id: () => string) {
  const media = ref<EmbyMatch>()
  const baseUrl = ref('')
  const serverId = ref('')
  const error = ref('')
  const loading = ref(false)
  let controller: AbortController | undefined
  async function refresh() {
    controller?.abort(); const current = new AbortController(); controller = current
    error.value = ''; loading.value = true
    try {
      const data = await embyRequest(token(), `media/${kind()}/${id()}`, current.signal)
      if (!current.signal.aborted) { media.value = data.media; baseUrl.value = data.baseUrl; serverId.value = data.serverId }
    } catch (e) {
      if (!current.signal.aborted) {
        error.value = e instanceof Error ? e.message : '入库状态查询失败'
        if (media.value) media.value = { ...media.value, state: 'stale' }
      }
    }
    finally { if (!current.signal.aborted) loading.value = false }
  }
  const episodes = computed(() => {
    const result: Record<number, Set<number>> = {}
    for (const item of media.value?.items || []) {
      if (item.type !== 'Episode' || item.season < 0 || item.episode < 0) continue
      const numbers = result[item.season] ||= new Set<number>()
      const end = item.endEpisode >= item.episode && item.endEpisode - item.episode <= 100 ? item.endEpisode : item.episode
      for (let n = item.episode; n <= end; n++) numbers.add(n)
    }
    return result
  })
  watch(() => [token(), kind(), id()], () => { media.value = undefined; baseUrl.value = ''; serverId.value = ''; void refresh() }, { immediate: true })
  // 只轮询本地索引，Webhook 增删后页面自动跟进；隐藏页面不发请求。
  const timer = window.setInterval(() => { if (!document.hidden && !loading.value) void refresh() }, 30000)
  onBeforeUnmount(() => { controller?.abort(); window.clearInterval(timer) })
  return { media, baseUrl, serverId, error, loading, episodes, refresh }
}
