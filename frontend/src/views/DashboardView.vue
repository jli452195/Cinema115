<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import MediaCard from '../components/MediaCard.vue'
import type { MediaCardItem } from '../types/media'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'

const props = defineProps<{ token: string; kind: 'tv' | 'movie' }>()
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
interface Genre { id: number; name: string }
const kind = computed(() => props.kind)
const genre = ref('')
const region = ref('')
const year = ref('')
const sort = ref('popular')
const route = useRoute()
const router = useRouter()
const query = computed(() => typeof route.query.q === 'string' ? route.query.q : '')
const page = ref(1)
const totalPages = ref(0)
const total = ref(0)
const genres = ref<Genre[]>([])
const items = ref<MediaCardItem[]>([])
const libraryMatches = useEmbyLibrary(() => props.token, items)
const loading = ref(true)
const error = ref('')
const genreError = ref('')
const regions = [{ id: '', name: '全部' }, { id: 'CN', name: '大陆' }, { id: 'HK', name: '香港' }, { id: 'TW', name: '台湾' }, { id: 'JP', name: '日本' }, { id: 'KR', name: '韩国' }, { id: 'western', name: '欧美' }]
const sorts = [{ id: 'popular', name: '热门' }, { id: 'latest', name: '最新' }, { id: 'rating', name: 'TMDB 评分' }]
const currentYear = new Date().getFullYear()
const years = Array.from({ length: 12 }, (_, i) => String(currentYear - i))
const olderYears = Array.from({ length: currentYear - 1911 }, (_, i) => String(currentYear - 12 - i))
const searching = computed(() => !!query.value)
let controller: AbortController | undefined
let genreController: AbortController | undefined
let mounted = true
function chip(active: boolean) { return active ? 'border-amber-400 bg-amber-50 text-amber-600' : 'border-transparent text-slate-500 hover:bg-slate-100 hover:text-slate-800' }
function clearSearch() { void router.replace({ path: route.path, query: {} }) }
async function request(path: string, signal: AbortSignal) {
  const response = await fetch(`/api/admin/tmdb/${path}`, { signal, headers: { Authorization: `Bearer ${props.token}` } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `查询失败（HTTP ${response.status}）`)
  return data
}
async function loadGenres() {
  genreController?.abort()
  const current = new AbortController(); genreController = current
  genres.value = []; genreError.value = ''
  try {
    const data = await request(`genres?kind=${kind.value}`, current.signal)
    if (!Array.isArray(data.genres)) throw new Error('类型数据格式不正确')
    if (mounted && !current.signal.aborted) genres.value = data.genres
  } catch (e) { if (mounted && !current.signal.aborted) genreError.value = e instanceof Error ? e.message : '类型加载失败' }
}
async function loadPage() {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; items.value = []; total.value = 0; totalPages.value = 0
  try {
    const params = new URLSearchParams({ kind: kind.value, page: String(page.value) })
    if (query.value) params.set('query', query.value)
    else { params.set('genre', genre.value); params.set('region', region.value); params.set('year', year.value); params.set('sort', sort.value) }
    const data = await request(`browse?${params}`, current.signal)
    if (!Array.isArray(data.items)) throw new Error('影视数据格式不正确')
    if (!mounted || current.signal.aborted) return
    items.value = data.items; totalPages.value = data.totalPages; total.value = data.total
  } catch (e) { if (mounted && !current.signal.aborted) error.value = e instanceof Error ? e.message : '看板加载失败' }
  finally { if (mounted && controller === current) loading.value = false }
}
watch(kind, () => { genre.value = ''; void loadGenres() })
watch([kind, genre, region, year, sort, query], () => { if (page.value !== 1) page.value = 1; else void loadPage() })
watch(page, () => { void loadPage() })
onMounted(() => { void loadGenres(); void loadPage() })
onBeforeUnmount(() => { mounted = false; controller?.abort(); genreController?.abort() })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 py-6 sm:px-8 lg:px-10 lg:py-8">
      <h1 class="sr-only">{{ kind === 'movie' ? '电影看板' : '剧集看板' }}</h1>
      <section aria-label="看板筛选" class="space-y-3 border-b border-slate-200 pb-6 text-sm">
        <p v-if="searching" class="rounded-lg bg-amber-50 p-3 text-xs leading-6 text-amber-700">正在搜索「{{ query }}」，结果按相关度排列。清空搜索后可按类型、地区、年份和评分筛选。<button class="ml-3 underline" @click="clearSearch">清空搜索</button></p>
        <fieldset :disabled="searching" class="space-y-3 disabled:opacity-40">
          <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800">类型</span><div class="flex flex-wrap gap-1"><button :aria-pressed="genre === ''" class="rounded-md border px-3 py-2" :class="chip(genre === '')" @click="genre = ''">全部</button><button v-for="item in genres" :key="item.id" :aria-pressed="genre === String(item.id)" class="rounded-md border px-3 py-2" :class="chip(genre === String(item.id))" @click="genre = String(item.id)">{{ item.name }}</button></div></div>
          <p v-if="genreError" class="ml-12 text-xs text-red-500">类型加载失败。<button class="underline" @click="loadGenres">重试</button></p>
          <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800">地区</span><div class="flex flex-wrap gap-1"><button v-for="item in regions" :key="item.id" :aria-pressed="region === item.id" class="rounded-md border px-3 py-2" :class="chip(region === item.id)" @click="region = item.id">{{ item.name }}</button></div></div>
          <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800">年份</span><div class="flex min-w-0 flex-wrap gap-1"><button :aria-pressed="year === ''" class="rounded-md border px-3 py-2" :class="chip(year === '')" @click="year = ''">全部</button><button v-for="item in years" :key="item" :aria-pressed="year === item" class="rounded-md border px-3 py-2" :class="chip(year === item)" @click="year = item">{{ item }}</button><select aria-label="更早年份" :value="olderYears.includes(year) ? year : ''" class="max-w-32 rounded-md border border-slate-200 bg-transparent px-2 text-xs text-slate-500" @change="year = ($event.target as HTMLSelectElement).value"><option value="">更早年份</option><option v-for="item in olderYears" :key="item" :value="item">{{ item }}</option></select></div></div>
          <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800">排序</span><div class="flex flex-wrap gap-1"><button v-for="item in sorts" :key="item.id" :aria-pressed="sort === item.id" class="rounded-md border px-3 py-2" :class="chip(sort === item.id)" @click="sort = item.id">{{ item.name }}</button></div></div>
        </fieldset>
      </section>
      <div class="my-5 flex flex-wrap justify-between gap-2 text-xs text-slate-400"><span>{{ loading ? '正在查询 TMDB…' : error ? '暂时无法加载' : `找到 ${total.toLocaleString()} 部${kind === 'tv' ? '剧集' : '电影'}` }}</span><span v-if="!searching && sort === 'rating'">评分排序至少需要 100 人评价</span></div>
      <div v-if="error" role="alert" class="rounded-xl border border-red-100 bg-red-50 p-6 text-sm text-red-600"><p>{{ error }}</p><button class="mt-4 rounded-lg border border-red-200 px-4 py-2" @click="loadGenres(); loadPage()">重新加载</button></div>
      <div v-else-if="loading" aria-label="正在加载影视" class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><div v-for="i in 10" :key="i" class="animate-pulse"><div class="aspect-[2/3] rounded-xl bg-slate-200"></div><div class="mt-4 h-4 w-3/4 rounded bg-slate-200"></div><div class="mt-2 h-3 w-10 rounded bg-slate-100"></div></div></div>
      <div v-else-if="!items.length" class="py-20 text-center text-sm text-slate-400">没有找到匹配的影视，试试其他关键词或筛选条件。</div>
      <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <MediaCard v-for="item in items" :key="`${item.kind}-${item.id}`" :item="item" :library="libraryMatches[`${item.kind}:${item.id}`]" />
      </div>
      <div v-if="!error && totalPages > 1" class="mt-9 flex items-center justify-center gap-5 text-sm"><button :disabled="loading || page <= 1" class="rounded-lg border border-slate-200 bg-white px-4 py-2 text-slate-600 disabled:opacity-40" @click="page--">上一页</button><span class="text-xs text-slate-400">{{ page }} / {{ totalPages }}</span><button :disabled="loading || page >= totalPages" class="rounded-lg border border-slate-200 bg-white px-4 py-2 text-slate-600 disabled:opacity-40" @click="page++">下一页</button></div>
      <footer class="mt-10 border-t border-slate-200 pt-5 text-xs leading-6 text-slate-400"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500">TMDB</a> · 评分为 TMDB 用户评分，地区按出品地筛选。点击海报查看影视详情。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
  </div>
</template>
