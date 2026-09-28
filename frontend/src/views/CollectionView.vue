<script setup lang="ts">
import { catalogAPI } from '../composables/useWebsiteAuth'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import MediaCard from '../components/MediaCard.vue'
import HistoryBackButton from '../components/HistoryBackButton.vue'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import type { MediaCardItem } from '../types/media'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ token: string; tmdbId: string }>()
defineEmits<{ logout: [] }>()
interface Collection { id: number; name: string; overview: string; posterUrl: string; backdropUrl: string; items: MediaCardItem[] }
const collection = ref<Collection>()
const loading = ref(true)
const error = ref('')
const failedImages = ref(new Set<string>())
const limit = ref(30)
const visibleItems = computed(() => collection.value?.items.slice(0, limit.value) ?? [])
const matches = useEmbyLibrary(() => props.token, visibleItems)
let controller: AbortController | undefined
const previousTitle = document.title
function hasImage(url: string) { return !!url && !failedImages.value.has(url) }
async function load() {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; collection.value = undefined; limit.value = 30; failedImages.value = new Set()
  document.title = '电影系列 · Cinema115'
  try {
    if (!/^[1-9]\d*$/.test(props.tmdbId) || !Number.isSafeInteger(Number(props.tmdbId))) throw new Error('TMDB 系列 ID 无效')
    const response = await fetch(catalogAPI(`tmdb/collection/${props.tmdbId}`), { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([current.signal, AbortSignal.timeout(25000)]) })
    const data = await response.json().catch(() => null)
    if (!response.ok) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : response.status === 404 ? '没有找到这个电影系列。' : data?.error || '系列加载失败，请稍后重试。')
    if (!data || !Array.isArray(data.items)) throw new Error('系列数据格式不正确')
    if (current.signal.aborted) return
    collection.value = data
    document.title = `${data.name || '电影系列'} · Cinema115`
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '系列加载失败' }
  finally { if (controller === current) loading.value = false }
}
watch(() => [props.tmdbId, props.token], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); document.title = previousTitle })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <main class="relative min-w-0 flex-1 overflow-hidden">
      <div v-if="collection && hasImage(collection.backdropUrl)" class="pointer-events-none absolute inset-x-0 top-0 h-[560px]" aria-hidden="true"><img :src="tmdbImageURL(collection.backdropUrl)" alt="" referrerpolicy="no-referrer" class="h-full w-full object-cover opacity-[0.15]" @error="failedImages.add(collection.backdropUrl)"/><div class="absolute inset-0 bg-gradient-to-b from-slate-50/20 to-slate-50"></div></div>
      <div class="relative mx-auto max-w-[1440px] px-4 py-6 sm:px-8 lg:px-10">
        <nav aria-label="系列导航" class="mb-8 flex items-center justify-between text-xs text-slate-400 dark:text-slate-500"><HistoryBackButton fallback="/movie" class="text-sm" /><span>电影系列</span></nav>
        <div v-if="loading" role="status" aria-label="正在加载电影系列" class="grid gap-8 motion-safe:animate-pulse sm:grid-cols-[200px_1fr]"><div class="aspect-[2/3] w-48 rounded-2xl bg-slate-200 dark:bg-slate-700"></div><div class="space-y-6 py-5"><div class="h-10 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div><div class="h-24 rounded bg-slate-200 dark:bg-slate-700/60"></div></div></div>
        <section v-else-if="error" role="alert" class="rounded-2xl border border-red-100 bg-white dark:bg-slate-900 p-8 text-center"><h1 class="text-xl font-semibold">暂时无法加载系列</h1><p class="mt-3 text-sm text-red-600">{{ error }}</p><button class="mt-5 rounded-lg bg-slate-900 px-5 py-2.5 text-sm text-white" @click="load">重新加载</button></section>
        <template v-else-if="collection">
          <section aria-label="系列信息" class="grid items-start gap-7 sm:grid-cols-[200px_minmax(0,1fr)] lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-10">
            <div class="mx-auto aspect-[2/3] w-44 overflow-hidden rounded-2xl bg-slate-200 dark:bg-slate-700 shadow-xl shadow-slate-900/10 sm:w-full"><img v-if="hasImage(collection.posterUrl)" :src="tmdbImageURL(collection.posterUrl)" :alt="collection.name" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(collection.posterUrl)"/><div v-else class="flex h-full items-center justify-center text-sm text-slate-400 dark:text-slate-500">暂无系列海报</div></div>
            <div class="min-w-0 py-2"><span class="rounded-full bg-amber-100 px-3 py-1 text-xs font-medium text-amber-700">电影系列</span><h1 class="mt-5 break-words text-3xl font-bold leading-tight tracking-tight text-slate-950 lg:text-4xl">{{ collection.name || '未命名系列' }}</h1><p class="mt-4 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">共 {{ collection.items.length }} 部电影 · 按上映时间排列</p><h2 class="mt-7 text-sm font-semibold text-slate-700 dark:text-slate-300">系列简介</h2><p class="mt-3 max-w-4xl whitespace-pre-line text-sm leading-7 text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ collection.overview || 'TMDB 暂未提供该系列的中文简介。' }}</p></div>
          </section>
          <section aria-label="系列电影" class="mt-12"><h2 class="mb-6 text-xl font-semibold text-slate-900 dark:text-slate-100">系列电影 <span class="ml-2 text-sm font-normal text-slate-400 dark:text-slate-500">{{ collection.items.length }}</span></h2>
            <p v-if="!collection.items.length" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-10 text-center text-sm text-slate-400 dark:text-slate-500">该系列暂无电影条目。</p>
            <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><MediaCard v-for="item in visibleItems" :key="item.id" :item="item" :library="matches[`movie:${item.id}`]" /></div>
            <div v-if="limit < collection.items.length" class="mt-8 text-center"><button class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-5 py-2.5 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500" @click="limit += 30">加载更多（剩余 {{ collection.items.length - limit }} 部）</button></div>
          </section>
          <footer class="mt-10 border-t border-slate-200 dark:border-slate-700 py-5 text-xs leading-6 text-slate-400 dark:text-slate-500">系列信息来自 <a :href="`https://www.themoviedb.org/collection/${collection.id}?language=zh-CN`" target="_blank" rel="noopener noreferrer" class="text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:underline">TMDB</a>，点击海报查看电影详情。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
        </template>
      </div>
    </main>
  </div>
</template>
