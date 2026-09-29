<script setup lang="ts">
import { catalogAPI } from '../composables/usePortalAuth'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import DashboardFilterDialog from '../components/DashboardFilterDialog.vue'
import MediaCard from '../components/MediaCard.vue'
import type { MediaCardItem } from '../types/media'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import { cacheGenres, cachedGenres, rememberDashboardPath, type DashboardGenre, type DashboardKind } from '../utils/dashboardPreferences'

const props = defineProps<{ token: string; kind: 'tv' | 'movie' }>()
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
const kind = computed(() => props.kind)
const route = useRoute(), router = useRouter()
const categories = computed(() => kind.value === 'tv'
  ? [{ id: '', name: '全部' }, { id: 'series', name: '剧集' }, { id: 'animation', name: '动漫' }, { id: 'documentary', name: '纪录' }, { id: 'variety', name: '综艺' }]
  : [{ id: '', name: '全部' }, { id: 'film', name: '电影' }, { id: 'animation', name: '动漫' }, { id: 'documentary', name: '纪录' }])
type FilterKey = 'category' | 'genre' | 'region' | 'year' | 'sort' | 'page'
function setFilters(values: Partial<Record<FilterKey, string>>) {
  const query = { ...route.query }
  for (const [key, value] of Object.entries(values) as [FilterKey, string][]) {
    if (!value || (key === 'page' && value === '1') || (key === 'sort' && value === 'popular')) delete query[key]
    else query[key] = value
  }
  if (Object.keys(values).some(key => key !== 'page')) delete query.page
  if ('category' in values) delete query.genre
  rememberDashboardPath(kind.value, router.resolve({ path: `/${kind.value}`, query }).fullPath)
  void router.push({ path: route.path, query })
}
function setFilter(key: FilterKey, value: string) { setFilters({ [key]: value }) }
function filter(key: FilterKey, fallback: string, valid: (value: string) => boolean) {
  return computed({
    get: () => { const value = route.query[key]; return typeof value === 'string' && valid(value) ? value : fallback },
    set: (value: string) => setFilter(key, value),
  })
}
const category = computed({ get: () => categories.value.some(item => item.id === route.query.category) ? String(route.query.category) : '', set: (value: string) => setFilter('category', value) })
const genreFilter = filter('genre', '', value => /^[1-9]\d{0,4}$/.test(value))
// 直接打开网址也排除与类别冲突或重复的类型。
function hiddenGenre(id: number) {
  if (category.value === 'film') return [16, 99].includes(id)
  return category.value === 'series' ? [16, 99, 10764, 10767, 10763].includes(id) : category.value === 'animation' ? id === 16 : category.value === 'documentary' ? id === 99 : category.value === 'variety' ? [10764, 10767].includes(id) : false
}
const genre = computed({ get: () => hiddenGenre(Number(genreFilter.value)) ? '' : genreFilter.value, set: (value: string) => { genreFilter.value = value } })
const region = filter('region', '', value => ['CN', 'HK', 'TW', 'JP', 'KR', 'TH', 'western'].includes(value))
const year = filter('year', '', value => /^\d{4}$/.test(value) && Number(value) >= 1900 && Number(value) <= new Date().getFullYear())
const sort = filter('sort', 'popular', value => ['popular', 'latest', 'rating'].includes(value))
const pageFilter = filter('page', '1', value => /^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 500)
const page = computed({ get: () => Number(pageFilter.value), set: (value: number) => { pageFilter.value = String(value) } })
const totalPages = ref(0)
const total = ref(0)
const genres = ref<DashboardGenre[]>(cachedGenres(kind.value).genres)
const visibleGenres = computed(() => genres.value.filter(item => !hiddenGenre(item.id)))
const items = ref<MediaCardItem[]>([])
const libraryMatches = useEmbyLibrary(() => props.token, items)
const loading = ref(true)
const error = ref('')
const genreError = ref('')
const regions = [{ id: '', name: '全部' }, { id: 'CN', name: '大陆' }, { id: 'HK', name: '香港' }, { id: 'TW', name: '台湾' }, { id: 'JP', name: '日本' }, { id: 'KR', name: '韩国' }, { id: 'TH', name: '泰国' }, { id: 'western', name: '欧美' }]
const sorts = [{ id: 'popular', name: '热门' }, { id: 'latest', name: '最新' }, { id: 'rating', name: 'TMDB 评分' }]
const currentYear = new Date().getFullYear()
const years = Array.from({ length: 12 }, (_, i) => String(currentYear - i))
const olderYears = Array.from({ length: currentYear - 1911 }, (_, i) => String(currentYear - 12 - i))
const mobileFiltersOpen = ref(false)
const activeMobileFilters = computed(() => {
  const values: string[] = []
  const genreName = visibleGenres.value.find(item => String(item.id) === genre.value)?.name
  const regionName = regions.find(item => item.id === region.value)?.name
  if (genreName) values.push(genreName)
  if (region.value && regionName) values.push(regionName)
  if (year.value) values.push(year.value)
  if (sort.value !== 'popular') values.push(sorts.find(item => item.id === sort.value)?.name ?? sort.value)
  return values
})
let controller: AbortController | undefined
const genreControllers: Partial<Record<DashboardKind, AbortController>> = {}
let mounted = true
function chip(active: boolean) { return active ? 'border-amber-400 bg-amber-50 text-amber-600 dark:border-amber-500/50 dark:bg-amber-500/10 dark:text-amber-400' : 'border-transparent text-slate-500 hover:bg-slate-100 hover:text-slate-800 dark:text-slate-400 dark:hover:bg-slate-800/50 dark:hover:text-slate-200' }
async function request(path: string, signal: AbortSignal) {
  const response = await fetch(catalogAPI(`tmdb/${path}`), { signal, headers: { Authorization: `Bearer ${props.token}` } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error ?? `查询失败（HTTP ${response.status}）`)
  return data
}
async function refreshGenres(targetKind: DashboardKind, force = false) {
  if (!force && cachedGenres(targetKind).fresh) return
  genreControllers[targetKind]?.abort()
  const current = new AbortController(); genreControllers[targetKind] = current
  try {
    const data = await request(`genres?kind=${targetKind}`, current.signal)
    if (!Array.isArray(data.genres)) throw new Error('类型数据格式不正确')
    const normalized = cacheGenres(targetKind, data.genres)
    if (mounted && !current.signal.aborted && kind.value === targetKind) genres.value = normalized
  } catch (e) {
    if (mounted && !current.signal.aborted && kind.value === targetKind && !genres.value.length) genreError.value = e instanceof Error ? e.message : '类型加载失败'
  }
}
function loadGenres(force = false) {
  const targetKind = kind.value
  genres.value = cachedGenres(targetKind).genres
  genreError.value = ''
  void refreshGenres(targetKind, force)
}
function applyMobileFilters(value: { genre: string; region: string; year: string; sort: string }) {
  mobileFiltersOpen.value = false
  setFilters(value)
}
function clearMobileFilters() {
  setFilters({ genre: '', region: '', year: '', sort: 'popular' })
}
async function loadPage() {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; items.value = []; total.value = 0; totalPages.value = 0
  try {
    const params = new URLSearchParams({ kind: kind.value, page: String(page.value) })
    if (category.value) params.set('category', category.value)
    params.set('genre', genre.value); params.set('region', region.value); params.set('year', year.value); params.set('sort', sort.value)
    const data = await request(`browse?${params}`, current.signal)
    if (!Array.isArray(data.items)) throw new Error('影视数据格式不正确')
    if (!mounted || current.signal.aborted) return
    items.value = data.items; totalPages.value = data.totalPages; total.value = data.total
  } catch (e) { if (mounted && !current.signal.aborted) error.value = e instanceof Error ? e.message : '看板加载失败' }
  finally { if (mounted && controller === current) loading.value = false }
}
watch(kind, () => { loadGenres() })
watch(() => route.fullPath, fullPath => {
  if (route.path === `/${kind.value}`) rememberDashboardPath(kind.value, fullPath)
}, { immediate: true })
watch([kind, category, genre, region, year, sort, page], () => { void loadPage() }, { immediate: true })
onMounted(() => {
  loadGenres()
  void refreshGenres(kind.value === 'movie' ? 'tv' : 'movie')
})
onBeforeUnmount(() => {
  mounted = false
  controller?.abort()
  Object.values(genreControllers).forEach(current => current?.abort())
})
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <h1 class="sr-only">{{ kind === 'movie' ? '电影看板' : '剧集看板' }}</h1>
      <section aria-label="看板筛选" class="space-y-3 border-b border-slate-200 dark:border-slate-800 pb-6 text-sm">
        <fieldset class="space-y-3">
          <div role="group" aria-label="影视类别" class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800 dark:text-slate-200">类别</span><div class="flex flex-wrap gap-1"><button v-for="item in categories" :key="item.id" :aria-pressed="category === item.id" class="rounded-md border px-3 py-2" :class="chip(category === item.id)" @click="category = item.id">{{ item.name }}</button></div></div>
          <div class="hidden space-y-3 sm:block">
            <div role="group" aria-label="影视类型" class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800 dark:text-slate-200">类型</span><div class="flex flex-wrap gap-1"><button :aria-pressed="genre === ''" class="rounded-md border px-3 py-2" :class="chip(genre === '')" @click="genre = ''">全部</button><button v-for="item in visibleGenres" :key="item.id" :aria-pressed="genre === String(item.id)" class="rounded-md border px-3 py-2" :class="chip(genre === String(item.id))" @click="genre = String(item.id)">{{ item.name }}</button></div></div>
            <p v-if="genreError" class="ml-12 text-xs text-red-500">类型加载失败。<button class="underline" @click="loadGenres(true)">重试</button></p>
            <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800 dark:text-slate-200">地区</span><div class="flex flex-wrap gap-1"><button v-for="item in regions" :key="item.id" :aria-pressed="region === item.id" class="rounded-md border px-3 py-2" :class="chip(region === item.id)" @click="region = item.id">{{ item.name }}</button></div></div>
            <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800 dark:text-slate-200">年份</span><div class="flex min-w-0 flex-wrap gap-1"><button :aria-pressed="year === ''" class="rounded-md border px-3 py-2" :class="chip(year === '')" @click="year = ''">全部</button><button v-for="item in years" :key="item" :aria-pressed="year === item" class="rounded-md border px-3 py-2" :class="chip(year === item)" @click="year = item">{{ item }}</button><select aria-label="更早年份" :value="olderYears.includes(year) ? year : ''" class="max-w-32 rounded-md border border-slate-200 dark:border-slate-800 bg-transparent px-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500" @change="year = ($event.target as HTMLSelectElement).value"><option value="">更早年份</option><option v-for="item in olderYears" :key="item" :value="item">{{ item }}</option></select></div></div>
            <div class="flex items-start gap-3"><span class="w-10 shrink-0 py-2 font-semibold text-slate-800 dark:text-slate-200">排序</span><div class="flex flex-wrap gap-1"><button v-for="item in sorts" :key="item.id" :aria-pressed="sort === item.id" class="rounded-md border px-3 py-2" :class="chip(sort === item.id)" @click="sort = item.id">{{ item.name }}</button></div></div>
          </div>
          <div class="flex min-w-0 items-center gap-3 pt-1 sm:hidden">
            <button type="button" class="flex h-10 shrink-0 items-center gap-2 rounded-md border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 px-3 font-medium text-slate-700 dark:text-slate-300" @click="mobileFiltersOpen = true"><span>筛选</span><span v-if="activeMobileFilters.length" class="min-w-5 rounded-full bg-amber-400 px-1.5 text-center text-xs leading-5 text-slate-950">{{ activeMobileFilters.length }}</span></button>
            <p class="min-w-0 flex-1 truncate text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ activeMobileFilters.length ? activeMobileFilters.join(' · ') : '全部类型 · 全部地区 · 全部年份' }}</p>
            <button v-if="activeMobileFilters.length" type="button" class="h-10 shrink-0 px-1 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500" @click="clearMobileFilters">清除</button>
          </div>
        </fieldset>
      </section>
      <div class="my-5 flex flex-wrap justify-between gap-2 text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500"><span>{{ loading ? '正在查询 TMDB…' : error ? '暂时无法加载' : `找到 ${total.toLocaleString()} 部${kind === 'tv' ? '剧集' : '电影'}` }}</span><span v-if="sort === 'rating'">评分排序至少需要 100 人评价</span></div>
      <div v-if="error" role="alert" class="rounded-xl border border-red-100 bg-red-50 p-6 text-sm text-red-600"><p>{{ error }}</p><button class="mt-4 rounded-lg border border-red-200 px-4 py-2" @click="loadGenres(); loadPage()">重新加载</button></div>
      <div v-else-if="loading" aria-label="正在加载影视" class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><div v-for="i in 10" :key="i" class="animate-pulse"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-4 h-4 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-2 h-3 w-10 rounded bg-slate-100 dark:bg-slate-800"></div></div></div>
      <div v-else-if="!items.length" class="py-20 text-center text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">没有找到匹配的影视，试试其他关键词或筛选条件。</div>
      <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <MediaCard v-for="item in items" :key="`${item.kind}-${item.id}`" :item="item" :library="libraryMatches[`${item.kind}:${item.id}`]" />
      </div>
      <div v-if="!error && totalPages > 1" class="mt-9 flex items-center justify-center gap-5 text-sm"><button :disabled="loading || page <= 1" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 text-slate-600 dark:text-slate-300 disabled:opacity-40" @click="page--">上一页</button><span class="text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ page }} / {{ totalPages }}</span><button :disabled="loading || page >= totalPages" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 text-slate-600 dark:text-slate-300 disabled:opacity-40" @click="page++">下一页</button></div>
      <footer class="mt-10 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">TMDB</a> · 评分为 TMDB 用户评分，地区按出品地筛选。点击海报查看影视详情。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
    <DashboardFilterDialog v-if="mobileFiltersOpen" :genres="visibleGenres" :regions="regions" :sorts="sorts" :genre="genre" :region="region" :year="year" :sort="sort" @close="mobileFiltersOpen = false" @apply="applyMobileFilters" />
  </div>
</template>
