<script setup lang="ts">
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { embyRequest } from '../types/emby'
import type { EmbySource, EmbyVersion, EmbyVersionPage } from '../types/embyVersion'
import { queueEmbyRequest } from '../composables/embyRequestQueue'
import { embyVersionCacheKey } from '../composables/useEmbyVersionCache'
import EmbyVersionAccordion from './EmbyVersionAccordion.vue'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; season?: number; episode?: number; showTitle?: boolean; eager?: boolean; compact?: boolean }>()
const cache = inject(embyVersionCacheKey, undefined)
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
async function toggleVersion(key: string, index: number) {
  const button = document.getElementById(`${idPrefix.value}-${index}-button`)
  const previousTop = button?.getBoundingClientRect().top ?? 0
  openKey.value = openKey.value === key ? '' : key
  if (props.kind !== 'movie') return
  await nextTick()
  if (!button?.isConnected) return
  button.focus({ preventScroll: true })
  const headerBottom = document.querySelector('.workbench-header')?.getBoundingClientRect().bottom || 0
  const targetTop = Math.max(headerBottom + 12, Math.min(previousTop, window.innerHeight - button.offsetHeight - 12))
  window.scrollBy({ top: button.getBoundingClientRect().top - targetTop, behavior: 'instant' })
}
async function load(more = false) {
  if (loading.value || (loaded.value && !more)) return
  const key = idPrefix.value
  if (!more) {
    const cached = cache?.read(key)
    if (cached) {
      versions.value = cached.versions; loaded.value = true; hasMore.value = cached.hasMore; offset.value = cached.nextOffset
      return
    }
  }
  const generation = cache?.generation.value
  const current = new AbortController(); controller = current; loading.value = true; error.value = ''
  try {
    const params = new URLSearchParams({ offset: String(more ? offset.value : 0) })
    if (props.kind === 'tv') { params.set('season', String(props.season)); params.set('episode', String(props.episode)) }
    const data: EmbyVersionPage = await queueEmbyRequest(current.signal, () => embyRequest(props.token, `versions/${props.kind}/${props.tmdbId}?${params}`, current.signal))
    if (current.signal.aborted || generation !== cache?.generation.value) return
    versions.value = more ? [...versions.value, ...data.versions] : data.versions
    loaded.value = true; hasMore.value = !!data.hasMore; offset.value = data.nextOffset
    if (generation !== undefined) cache?.write(key, { ...data, versions: versions.value }, generation)
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '媒体版本读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
watch(() => [props.kind, props.tmdbId, props.season, props.episode, props.token, cache?.generation.value], () => {
  controller?.abort(); loading.value = false; loaded.value = false; versions.value = []; openKey.value = ''; error.value = ''; hasMore.value = false; offset.value = 0
  if (visible || props.eager || props.kind === 'movie') void load()
})
onMounted(() => {
  if (props.eager || props.kind === 'movie' || !('IntersectionObserver' in window)) { visible = true; void load(); return }
  observer = new IntersectionObserver(entries => { visible = entries.some(entry => entry.isIntersecting); if (visible) void load() }, { rootMargin: '120px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { controller?.abort(); observer?.disconnect() })
</script>

<template>
  <section ref="root" aria-label="Emby 媒体信息" class="min-w-0" :class="{ 'mt-5': showTitle !== false }">
    <h3 v-if="showTitle !== false" class="mb-3 font-semibold text-slate-700 dark:text-slate-300" :class="kind === 'movie' ? 'text-base' : 'text-xs'">Emby 媒体信息</h3>
    <div v-if="!loaded && !error" :class="kind === 'movie' ? 'movie-version-status' : 'rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4 text-xs text-slate-400 dark:text-slate-500'" role="status">{{ loading ? '正在读取媒体版本…' : '滚动至此处加载媒体版本' }}</div>
    <div :class="compact ? 'space-y-1' : 'space-y-2'"><EmbyVersionAccordion v-for="(row, index) in rows" :key="row.key" :version="row.version" :source="row.source" :number="index + 1" :panel-id="`${idPrefix}-${index}`" :expanded="openKey === row.key" :compact="compact" @toggle="toggleVersion(row.key, index)" /></div>
    <p v-if="loaded && !versions.length" :class="kind === 'movie' ? 'movie-version-status' : 'rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4 text-xs leading-6 text-slate-500 dark:text-slate-400'">Emby 暂未返回可用媒体版本，可刷新入库状态后重试。</p>
    <p v-if="error" role="alert" class="mt-3 text-xs leading-6 text-red-600">{{ error }}<button class="ml-3 font-medium underline" @click="load(loaded)">重新加载</button></p>
    <button v-if="hasMore && !error" :disabled="loading" class="mt-3 rounded-lg border border-slate-200 dark:border-slate-700 px-4 py-2 text-xs text-blue-600 disabled:opacity-40" @click="load(true)">{{ loading ? '正在读取…' : '加载更多版本' }}</button>
  </section>
</template>

<style scoped>
.movie-version-status { padding: 16px 20px; border-radius: 12px; background: var(--detail-media-surface); color: var(--detail-media-muted); font-size: 12px; line-height: 24px; }
</style>
