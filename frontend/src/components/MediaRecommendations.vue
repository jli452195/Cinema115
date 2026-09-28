<script setup lang="ts">
import { catalogAPI } from '../composables/useWebsiteAuth'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import MediaCard from './MediaCard.vue'
import type { MediaCardItem } from '../types/media'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const items = ref<MediaCardItem[]>([])
const libraryMatches = useEmbyLibrary(() => props.token, items)
const loading = ref(true)
const error = ref('')
const rail = ref<HTMLUListElement>()
const canPrevious = ref(false)
const canNext = ref(false)
let controller: AbortController | undefined
let observer: ResizeObserver | undefined

function updateEdges() {
  const element = rail.value
  canPrevious.value = !!element && element.scrollLeft > 2
  canNext.value = !!element && element.scrollLeft + element.clientWidth < element.scrollWidth - 2
}

function scroll(direction: number) {
  const element = rail.value
  if (!element) return
  element.scrollBy({ left: direction * element.clientWidth * 0.85, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })
}

async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  items.value = []; loading.value = true; error.value = ''
  canPrevious.value = false; canNext.value = false
  let timedOut = false
  const timeout = window.setTimeout(() => { timedOut = true; current.abort() }, 20000)
  try {
    if (!/^[1-9]\d*$/.test(props.tmdbId) || !Number.isSafeInteger(Number(props.tmdbId))) throw new Error('TMDB ID 无效')
    const response = await fetch(catalogAPI(`tmdb/recommendations/${props.kind}/${props.tmdbId}`), {
      signal: current.signal, headers: { Authorization: `Bearer ${props.token}` },
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error ?? '推荐加载失败，请稍后重试')
    if (!Array.isArray(data.items)) throw new Error('推荐数据格式不正确')
    if (current.signal.aborted) return
    items.value = data.items
  } catch (e) {
    if (controller === current && (!current.signal.aborted || timedOut)) error.value = timedOut ? '推荐加载超时，请稍后重试' : e instanceof Error ? e.message : '推荐加载失败'
  } finally {
    window.clearTimeout(timeout)
    // 路由切换或卸载后，旧请求不能更新新影片的推荐。
    if (controller === current && (!current.signal.aborted || timedOut)) {
      loading.value = false
      await nextTick()
      if (controller === current && rail.value) {
        rail.value.scrollLeft = 0
        updateEdges()
      }
    }
  }
}

watch(() => [props.kind, props.tmdbId, props.token], () => { void load() }, { immediate: true })
onMounted(() => {
  observer = new ResizeObserver(updateEdges)
  if (rail.value) observer.observe(rail.value)
})
onBeforeUnmount(() => { controller?.abort(); controller = undefined; observer?.disconnect() })
</script>

<template>
  <section class="mt-12 min-w-0" aria-label="相似影视推荐" :aria-busy="loading">
    <div class="mb-5 flex items-center justify-between gap-4">
      <h2 class="text-lg font-bold text-slate-900 dark:text-slate-100">相似影视推荐</h2>
      <div v-if="items.length" class="flex gap-2">
        <button type="button" aria-label="上一组推荐" :disabled="!canPrevious" class="flex h-8 w-8 items-center justify-center rounded-full border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-400 dark:text-slate-500 transition hover:border-amber-400 hover:text-amber-600 disabled:cursor-default disabled:opacity-30" @click="scroll(-1)"><span aria-hidden="true">←</span></button>
        <button type="button" aria-label="下一组推荐" :disabled="!canNext" class="flex h-8 w-8 items-center justify-center rounded-full border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-400 dark:text-slate-500 transition hover:border-amber-400 hover:text-amber-600 disabled:cursor-default disabled:opacity-30" @click="scroll(1)"><span aria-hidden="true">→</span></button>
      </div>
    </div>
    <div v-if="loading" role="status" aria-label="正在加载推荐" class="flex gap-5 overflow-hidden pb-4 motion-safe:animate-pulse">
      <div v-for="i in 6" :key="i" class="w-[136px] shrink-0 sm:w-[170px]"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-3 h-4 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-2 h-3 w-10 rounded bg-slate-200 dark:bg-slate-700"></div></div>
    </div>
    <div v-else-if="error" role="status" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900/70 p-5 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ error }}<button type="button" class="ml-3 font-medium text-amber-600 hover:underline" @click="load">重新加载推荐</button></div>
    <p v-else-if="!items.length" class="py-4 text-sm text-slate-400 dark:text-slate-500">暂时没有相似影视推荐。</p>
    <ul v-show="!loading && !error && items.length" ref="rail" tabindex="0" aria-label="推荐影视列表" class="recommendation-rail flex snap-x snap-proximity gap-5 overflow-x-auto overscroll-x-contain px-1 pb-5 pt-1 focus-visible:outline focus-visible:outline-2 focus-visible:outline-amber-400" @scroll.passive="updateEdges" @keydown.left.prevent="scroll(-1)" @keydown.right.prevent="scroll(1)">
      <li v-for="item in items" :key="`${item.kind}-${item.id}`" class="w-[136px] shrink-0 snap-start sm:w-[170px]"><MediaCard :item="item" :library="libraryMatches[`${item.kind}:${item.id}`]" /></li>
    </ul>
  </section>
</template>

<style scoped>
.recommendation-rail { scrollbar-width: thin; scrollbar-color: #cbd5e1 transparent; }
</style>
