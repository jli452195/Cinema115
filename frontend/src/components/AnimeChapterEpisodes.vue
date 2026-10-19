<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeChapterEpisode, AnimeChapterEpisodes } from '../types/anime'
import type { AnimeNamingTarget } from '../types/animeSubscription'
import { chapterEpisodeReferences, seasonEpisodeLabel, type ChapterEpisodeReference } from '../utils/animeChapterEpisodes'

const props = defineProps<{ token: string; bangumiId: string; target?: AnimeNamingTarget; targetBusy: boolean }>()
const items = ref<AnimeChapterEpisode[]>([]), total = ref(0), limit = ref(100), maxItems = ref(2000)
const loading = ref(false), loaded = ref(false), error = ref(''), expanded = ref(false), stale = ref(false)
const listID = useId()
let generation = 0, controller = new AbortController()
const rows = computed(() => chapterEpisodeReferences(items.value, props.targetBusy ? undefined : props.target))
const visible = computed(() => expanded.value ? rows.value : rows.value.slice(0, 12))
const matched = computed(() => rows.value.filter(row => row.episode).length)
const more = computed(() => items.value.length < Math.min(total.value, maxItems.value))
const heading = computed(() => props.target && !props.targetBusy ? 'TMDB 对应集数' : 'Bangumi 原集数')
const note = computed(() => {
  if (props.targetBusy) return '正在读取 TMDB 编排…'
  if (!props.target) return '关联 TMDB 后显示对应季集'
  if (!items.value.length) return ''
  const prefix = props.target.groupId ? `${props.target.groupName} · ` : ''
  return `${prefix}${matched.value} 集已对应${matched.value < items.value.length ? ` · ${items.value.length - matched.value} 集待核对` : ''}`
})
function description(row: ChapterEpisodeReference) {
  const original = `Bangumi 原编号 ${row.chapter.number}${row.chapter.chapterNumber != null ? `（篇章内第 ${row.chapter.chapterNumber} 集）` : ''}`
  const title = row.chapter.title || row.chapter.originalTitle
  if (!row.episode) return `${original}${title ? ` · ${title}` : ''}${props.target ? '；当前 TMDB 季未确认对应关系' : ''}`
  const evidence = row.evidence === 'both' ? '标题与首播日期一致' : row.evidence === 'title' ? '标题唯一匹配' : '首播日期唯一匹配'
  return `${seasonEpisodeLabel(row.season!, row.episode.number)} · ${row.episode.name}；${original}；${evidence}`
}
async function read(append = false) {
  if (!props.bangumiId || loading.value) return
  const current = generation, offset = append ? items.value.length : 0
  loading.value = true; error.value = ''
  try {
    const data = await animeConfigRequest<AnimeChapterEpisodes>(props.token, `discovery/subjects/${encodeURIComponent(props.bangumiId)}/episodes?offset=${offset}`, { signal: controller.signal })
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
  items.value = []; total.value = 0; loaded.value = false; expanded.value = false; stale.value = false; error.value = ''; loading.value = false
  void read()
}, { immediate: true })
onBeforeUnmount(() => { generation++; controller.abort() })
</script>

<template>
  <section class="ae-chapter-episodes" aria-label="篇章对应集数" :aria-busy="loading || targetBusy">
    <div class="ae-episode-heading"><span>{{ heading }}</span><span v-if="loaded && total">共 {{ total }} 集</span></div>
    <p v-if="loading && !loaded" class="ae-episode-note" role="status">正在读取篇章集数…</p>
    <template v-else-if="loaded && items.length">
      <div :id="listID" class="ae-episode-chips" :class="{ expanded }" role="list" aria-label="集数编号一览" :tabindex="expanded ? 0 : undefined">
        <span v-for="row in visible" :key="row.chapter.id" class="ae-episode-chip" :class="{ mapped: row.episode }" role="listitem" :title="description(row)" :aria-label="description(row)">{{ row.episode ? seasonEpisodeLabel(row.season!, row.episode.number) : `BGM ${row.chapter.number}` }}</span>
      </div>
      <p class="ae-episode-note">{{ note }}<span v-if="stale"> · 缓存资料</span></p>
      <div v-if="items.length > 12 || more" class="ae-episode-actions">
        <button type="button" class="ae-link" :aria-expanded="expanded" :aria-controls="listID" @click="expanded = !expanded">{{ expanded ? '收起集数' : `展开集数（已读 ${items.length} 集）` }}</button>
        <button v-if="expanded && more" type="button" class="ae-link" :disabled="loading" @click="read(true)">{{ loading ? '读取中…' : `继续读取 ${Math.min(limit, total - items.length, maxItems - items.length)} 集` }}</button>
        <span v-if="expanded && total > maxItems && !more" class="ae-episode-note">仅提供前 {{ maxItems }} 集参考</span>
      </div>
    </template>
    <p v-else-if="loaded" class="ae-episode-note">Bangumi 暂未收录正片集数</p>
    <p v-if="error" class="ae-episode-note" role="status">{{ error }}<button type="button" class="ae-link" :disabled="loading" @click="read()">重新读取集数</button></p>
  </section>
</template>
