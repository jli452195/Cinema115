<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { embyRequest } from '../types/emby'
import type { EmbySource, EmbyVersion, EmbyVersionPage } from '../types/embyVersion'
import { queueEmbyRequest } from '../composables/embyRequestQueue'
import EmbyVersionAccordion from './EmbyVersionAccordion.vue'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; season?: number; episode?: number; showTitle?: boolean }>()
const root = ref<HTMLElement>()
const versions = ref<EmbyVersion[]>([])
const loaded = ref(false)
const loading = ref(false)
const error = ref('')
const hasMore = ref(false)
const offset = ref(0)
const openKey = ref('')
let controller: AbortController | undefined
let observer: IntersectionObserver | undefined
let visible = false
interface VersionRow { key: string; version: EmbyVersion; source?: EmbySource }
const rows = computed(() => versions.value.flatMap<VersionRow>(version => version.sources?.length
  ? version.sources.map((source, index) => ({ key: `${version.itemId}-${source.Id || index}`, version, source }))
  : [{ key: version.itemId, version, source: undefined }]))
const idPrefix = computed(() => `emby-${props.kind}-${props.tmdbId}-${props.season ?? 'movie'}-${props.episode ?? 'all'}`)
async function load(more = false) {
  if (loading.value || (loaded.value && !more)) return
  const current = new AbortController(); controller = current; loading.value = true; error.value = ''
  try {
    const params = new URLSearchParams({ offset: String(more ? offset.value : 0) })
    if (props.kind === 'tv') { params.set('season', String(props.season)); params.set('episode', String(props.episode)) }
    const data: EmbyVersionPage = await queueEmbyRequest(current.signal, () => embyRequest(props.token, `versions/${props.kind}/${props.tmdbId}?${params}`, current.signal))
    if (current.signal.aborted) return
    versions.value = more ? [...versions.value, ...data.versions] : data.versions
    loaded.value = true; hasMore.value = !!data.hasMore; offset.value = data.nextOffset
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '媒体版本读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
watch(() => [props.kind, props.tmdbId, props.season, props.episode, props.token], () => {
  controller?.abort(); loading.value = false; loaded.value = false; versions.value = []; openKey.value = ''; error.value = ''; hasMore.value = false; offset.value = 0
  if (visible || props.kind === 'movie') void load()
})
onMounted(() => {
  if (props.kind === 'movie' || !('IntersectionObserver' in window)) { visible = true; void load(); return }
  observer = new IntersectionObserver(entries => { visible = entries.some(entry => entry.isIntersecting); if (visible) void load() }, { rootMargin: '120px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { controller?.abort(); observer?.disconnect() })
</script>

<template>
  <section ref="root" aria-label="Emby 媒体信息" class="min-w-0" :class="{ 'mt-5': showTitle !== false }">
    <h3 v-if="showTitle !== false" class="mb-3 font-semibold text-slate-700 dark:text-slate-300" :class="kind === 'movie' ? 'text-base' : 'text-xs'">Emby 媒体信息</h3>
    <div v-if="!loaded && !error" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4 text-xs text-slate-400 dark:text-slate-500" role="status">{{ loading ? '正在读取媒体版本…' : '滚动至此处加载媒体版本' }}</div>
    <div class="space-y-3"><EmbyVersionAccordion v-for="(row, index) in rows" :key="row.key" :version="row.version" :source="row.source" :number="index + 1" :panel-id="`${idPrefix}-${index}`" :expanded="openKey === row.key" @toggle="openKey = openKey === row.key ? '' : row.key" /></div>
    <p v-if="loaded && !versions.length" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">Emby 暂未返回可用媒体版本，可刷新入库状态后重试。</p>
    <p v-if="error" role="alert" class="mt-3 text-xs leading-6 text-red-600">{{ error }}<button class="ml-3 font-medium underline" @click="load(loaded)">重新加载</button></p>
    <button v-if="hasMore && !error" :disabled="loading" class="mt-3 rounded-lg border border-slate-200 dark:border-slate-700 px-4 py-2 text-xs text-blue-600 disabled:opacity-40" @click="load(true)">{{ loading ? '正在读取…' : '加载更多版本' }}</button>
  </section>
</template>
