<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { animeConfigMessage } from '../lib/anime-config-api'
import { cachedAnime, requestAnime } from '../composables/useAnimeDiscovery'
import type { AnimeChapterEpisode, AnimeChapterEpisodes } from '../types/anime'

const props = defineProps<{ token: string; bangumiId: string; active?: boolean }>()
const items = ref<AnimeChapterEpisode[]>([]), total = ref(0), limit = ref(100), maxItems = ref(2000)
const loading = ref(false), loaded = ref(false), error = ref(''), stale = ref(false)
const listID = useId()
let generation = 0, controller = new AbortController()
const more = computed(() => items.value.length < Math.min(total.value, maxItems.value))
async function read(append = false) {
  if (!props.bangumiId || loading.value) return
  const current = generation, offset = append ? items.value.length : 0
  const cacheKey = 'chapter-episodes:' + props.bangumiId + ':' + offset
  const cached = cachedAnime<AnimeChapterEpisodes>(cacheKey)
  if (cached) {
    const data = cached.data
    const valid = Array.isArray(data.items) && data.offset === offset && data.limit === 100 && Number.isInteger(data.total) && data.total >= offset + data.items.length && data.maxItems === 2000
    const duplicate = append && data.items.some(item => items.value.some(old => old.id === item.id))
    if (valid && !duplicate && (!append || cached.fresh && data.total === total.value)) {
      items.value = append ? [...items.value, ...data.items] : data.items
      total.value = data.total; limit.value = data.limit; maxItems.value = data.maxItems
      stale.value = (append && stale.value) || !cached.fresh || data.stale
      loaded.value = true
      if (cached.fresh) { error.value = ''; return }
    }
  }
  loading.value = true; error.value = ''
  try {
    const data = await requestAnime<AnimeChapterEpisodes>(cacheKey, 'subjects/' + encodeURIComponent(props.bangumiId) + '/episodes?offset=' + offset, props.token, controller.signal)
    if (current !== generation || controller.signal.aborted) return
    if (!Array.isArray(data.items) || data.offset !== offset || data.limit !== 100 || !Number.isInteger(data.total) || data.total < offset + data.items.length || data.maxItems !== 2000) throw new Error('集数资料不完整，请重新读取')
    if (append && (data.total !== total.value || data.items.some(item => items.value.some(old => old.id === item.id)))) throw new Error('集数列表已变化，请重新读取')
    items.value = append ? [...items.value, ...data.items] : data.items
    total.value = data.total; limit.value = data.limit; maxItems.value = data.maxItems; stale.value = (append && stale.value) || data.stale
    loaded.value = true
  } catch (reason) { if (current === generation && !controller.signal.aborted) error.value = animeConfigMessage(reason) }
  finally { if (current === generation) loading.value = false }
}
watch(() => [props.token, props.bangumiId], () => {
  generation++; controller.abort(); controller = new AbortController()
  items.value = []; total.value = 0; loaded.value = false; stale.value = false; error.value = ''; loading.value = false
  if (props.active !== false) void read()
}, { immediate: true })
watch(() => props.active, active => {
  if (active && (!loaded.value || !cachedAnime('chapter-episodes:' + props.bangumiId + ':0')?.fresh)) void read()
})
onBeforeUnmount(() => { generation++; controller.abort() })
</script>

<template>
  <section class="ae-chapter-episodes" aria-label="Bangumi 正片集数" :aria-busy="loading">
    <div class="ae-episode-heading"><span>Bangumi 原编号</span><span v-if="loaded && total">共 {{ total }} 集</span></div>
    <p v-if="loading && !loaded" class="ae-episode-note" role="status">正在读取 Bangumi 集数…</p>
    <template v-else-if="loaded && items.length">
      <div :id="listID" class="ae-episode-chips" role="list" aria-label="Bangumi 逐集信息">
        <span v-for="item in items" :key="item.id" class="ae-episode-chip" role="listitem" :title="[item.title || item.originalTitle, item.date].filter(Boolean).join(' · ')">{{ item.number }}</span>
      </div>
      <p v-if="stale" class="ae-episode-note">缓存资料</p>
      <button v-if="more" type="button" class="ae-link" :disabled="loading" :aria-controls="listID" @click="read(true)">{{ loading ? '读取中…' : `继续读取 ${Math.min(limit, total - items.length, maxItems - items.length)} 集` }}</button>
      <p v-if="total > maxItems && !more" class="ae-episode-note">仅提供前 {{ maxItems }} 集参考</p>
    </template>
    <p v-else-if="loaded" class="ae-episode-note">Bangumi 暂未收录正片集数</p>
    <p v-if="error" class="ae-episode-note" role="status">{{ error }}<button type="button" class="ae-link" :disabled="loading" @click="read()">重新读取集数</button></p>
  </section>
</template>

<style scoped>
.ae-episode-heading { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 6px; color: var(--ae-muted); font-size: 11px; }
.ae-episode-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.ae-episode-chip { display: inline-flex; align-items: center; justify-content: center; min-width: 32px; min-height: 28px; padding: 3px 7px; border-radius: 6px; color: var(--ae-ink); background: var(--ae-inset); font-size: 12px; font-variant-numeric: tabular-nums; cursor: default; }
.ae-episode-note { margin-top: 6px; color: var(--ae-muted); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.ae-episode-note .ae-link { margin-left: 8px; }
</style>
