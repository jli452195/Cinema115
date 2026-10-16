<script setup lang="ts">
import { catalogAPI } from '../composables/usePortalAuth'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import DashboardFilterDialog from '../components/DashboardFilterDialog.vue'
import DashboardYearMenu from '../components/DashboardYearMenu.vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import MediaCard from '../components/MediaCard.vue'
import PaginationControl from '../components/PaginationControl.vue'
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
const categoryStrip = ref<HTMLElement>()
const sortStrip = ref<HTMLElement>()
const { indicatorStyle: categoryIndicator } = useSlidingIndicator(categoryStrip, '[aria-pressed="true"]', [category, categories])
const { indicatorStyle: sortIndicator } = useSlidingIndicator(sortStrip, '[aria-pressed="true"]', [sort])
const totalPages = ref(0)
const pageCount = computed(() => Math.min(500, totalPages.value))
function changePage(value: number) {
  if (!loading.value && !error.value && value >= 1 && value <= pageCount.value) page.value = value
}
const total = ref(0)
const genres = ref<DashboardGenre[]>(cachedGenres(kind.value).genres)
const visibleGenres = computed(() => genres.value.filter(item => !hiddenGenre(item.id)))
const items = ref<MediaCardItem[]>([])
const libraryMatches = useEmbyLibrary(() => props.token, items)
const loading = ref(true)
const error = ref('')
const genreError = ref('')
const hasLoaded = ref(false)
const loadedPage = ref(1)
const loadedSummary = ref('')
const regions = [{ id: '', name: '全部' }, { id: 'CN', name: '大陆' }, { id: 'HK', name: '香港' }, { id: 'TW', name: '台湾' }, { id: 'JP', name: '日本' }, { id: 'KR', name: '韩国' }, { id: 'TH', name: '泰国' }, { id: 'western', name: '欧美' }]
const sorts = [{ id: 'popular', name: '热门' }, { id: 'latest', name: '最新' }, { id: 'rating', name: 'TMDB 评分' }]
const currentYear = new Date().getFullYear()
const years = Array.from({ length: 8 }, (_, i) => String(currentYear - i))
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
const resultSummary = computed(() => {
  const labels = [...activeMobileFilters.value]
  if (category.value) labels.unshift(categories.value.find(item => item.id === category.value)?.name || '')
  return labels.filter(Boolean).join(' · ') || '全部条件 · 热门'
})
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
  loading.value = true; error.value = ''
  const requestedPage = page.value, requestedSummary = resultSummary.value
  try {
    const params = new URLSearchParams({ kind: kind.value, page: String(page.value) })
    if (category.value) params.set('category', category.value)
    params.set('genre', genre.value); params.set('region', region.value); params.set('year', year.value); params.set('sort', sort.value)
    const data = await request(`browse?${params}`, current.signal)
    if (!Array.isArray(data.items)) throw new Error('影视数据格式不正确')
    if (!mounted || current.signal.aborted) return
    items.value = data.items; totalPages.value = data.totalPages; total.value = data.total
    loadedPage.value = requestedPage; loadedSummary.value = requestedSummary; hasLoaded.value = true
  } catch (e) { if (mounted && !current.signal.aborted) error.value = e instanceof Error ? e.message : '看板加载失败' }
  finally { if (mounted && controller === current) loading.value = false }
}
watch(kind, () => { items.value = []; total.value = 0; totalPages.value = 0; hasLoaded.value = false; loadGenres() })
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
    <main class="catalog-filter-theme workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <h1 class="sr-only">{{ kind === 'movie' ? '电影看板' : '剧集看板' }}</h1>
      <section aria-label="看板筛选" class="catalog-filters">
        <div ref="categoryStrip" role="group" aria-label="影视类别" class="catalog-segments catalog-categories">
          <div class="catalog-segment-indicator" :style="categoryIndicator" aria-hidden="true"></div>
          <button v-for="item in categories" :key="item.id" type="button" :aria-pressed="category === item.id" @click="category = item.id">{{ item.name }}</button>
        </div>
        <div class="catalog-desktop-filters">
          <div role="group" aria-label="影视题材" class="catalog-filter-row"><span class="catalog-row-label">题材</span><div class="catalog-options">
            <button type="button" class="catalog-chip" :aria-pressed="genre === ''" @click="genre = ''">全部</button><button v-for="item in visibleGenres" :key="item.id" type="button" class="catalog-chip" :aria-pressed="genre === String(item.id)" @click="genre = String(item.id)">{{ item.name }}</button>
          </div></div>
          <p v-if="genreError" class="catalog-genre-error">题材加载失败。<button type="button" @click="loadGenres(true)">重试</button></p>
          <div role="group" aria-label="影视地区" class="catalog-filter-row"><span class="catalog-row-label">地区</span><div class="catalog-options"><button v-for="item in regions" :key="item.id" type="button" class="catalog-chip" :aria-pressed="region === item.id" @click="region = item.id">{{ item.name }}</button></div></div>
          <div role="group" aria-label="影视年份" class="catalog-filter-row"><span class="catalog-row-label">年份</span><div class="catalog-options">
            <button type="button" class="catalog-chip" :aria-pressed="year === ''" @click="year = ''">全部</button><button v-for="value in years" :key="value" type="button" class="catalog-chip" :aria-pressed="year === value" @click="year = value">{{ value }}</button><DashboardYearMenu v-model="year" :recent-years="years" />
          </div></div>
        </div>
        <div class="catalog-mobile-tools">
          <button type="button" class="catalog-chip catalog-filter-trigger" aria-haspopup="dialog" @click="mobileFiltersOpen = true"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="3" fill="var(--filter-tone)" /><circle cx="15" cy="17" r="3" fill="var(--filter-tone)" /></svg>筛选<span v-if="activeMobileFilters.length" class="catalog-filter-count">{{ activeMobileFilters.length }}</span></button>
          <p :title="activeMobileFilters.join(' · ')">{{ activeMobileFilters.length ? activeMobileFilters.join(' · ') : '全部题材 · 全部地区 · 全部年份' }}</p>
          <button v-if="activeMobileFilters.length" type="button" class="catalog-text-button" @click="clearMobileFilters">清除</button>
        </div>
      </section>
      <div class="catalog-result-toolbar">
        <div class="catalog-result-heading"><span role="status">{{ loading ? hasLoaded ? '正在更新结果…' : '正在查询 TMDB…' : error ? hasLoaded ? '更新失败，保留上次结果' : '暂时无法加载' : `找到 ${total.toLocaleString()} 部${kind === 'tv' ? '剧集' : '电影'}` }}</span><template v-if="activeMobileFilters.length"><span class="catalog-desktop-summary">{{ activeMobileFilters.join(' · ') }}</span><button type="button" class="catalog-text-button catalog-desktop-reset" @click="clearMobileFilters">清除筛选</button></template></div>
        <div class="catalog-sort-tools"><div ref="sortStrip" role="group" aria-label="结果排序" class="catalog-segments catalog-sort"><div class="catalog-segment-indicator" :style="sortIndicator" aria-hidden="true"></div><button v-for="item in sorts" :key="item.id" type="button" :aria-pressed="sort === item.id" @click="sort = item.id">{{ item.name }}</button></div><p v-if="sort === 'rating'">评分排序至少需要 100 人评价</p></div>
      </div>
      <div v-if="error" role="alert" class="catalog-load-error"><div><p>{{ error }}</p><p v-if="hasLoaded" class="catalog-error-context">当前保留上次结果：{{ loadedSummary }}，第 {{ loadedPage }} 页。</p></div><button type="button" class="catalog-chip" @click="loadGenres(); loadPage()">重新加载</button></div>
      <section aria-label="影视列表" :aria-busy="loading" class="catalog-results">
        <div v-if="loading && hasLoaded" class="catalog-progress" aria-hidden="true"></div>
        <div v-if="loading && !hasLoaded" aria-label="正在加载影视" class="catalog-card-grid"><div v-for="i in 20" :key="i" class="animate-pulse"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-2.5 h-10 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-0.5 h-4 w-10 rounded bg-slate-100 dark:bg-slate-800"></div></div></div>
        <div v-else-if="!items.length && hasLoaded" class="catalog-empty">{{ loading || error ? '上次查询没有匹配的影视。' : '没有找到匹配的影视，试试其他筛选条件。' }}</div>
        <div v-else-if="items.length" class="catalog-card-grid catalog-media-grid" :class="{ 'catalog-updating': loading }">
          <MediaCard v-for="item in items" :key="`${item.kind}-${item.id}`" :item="item" :library="libraryMatches[`${item.kind}:${item.id}`]" :show-kind-badge="false" :title-lines="2" />
        </div>
      </section>
      <PaginationControl v-if="hasLoaded && pageCount > 1" class="mt-9" :page="loading || error ? loadedPage : page" :pages="pageCount" :disabled="loading || !!error" :label="kind === 'movie' ? '电影分页' : '剧集分页'" @change="changePage" />
      <footer class="mt-10 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">TMDB</a> · 评分为 TMDB 用户评分，地区按出品地筛选。点击海报查看影视详情。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
    <DashboardFilterDialog v-if="mobileFiltersOpen" :genres="visibleGenres" :regions="regions" :sorts="sorts" :genre="genre" :region="region" :year="year" :sort="sort" @close="mobileFiltersOpen = false" @apply="applyMobileFilters" />
  </div>
</template>

<style scoped>
.catalog-filters { padding: 2px 0 10px; border-bottom: 1px solid var(--filter-outline); }
.catalog-categories { margin-bottom: 10px; }
.catalog-desktop-filters { display: flex; flex-direction: column; gap: 6px; }
.catalog-filter-row { display: flex; align-items: flex-start; gap: 12px; min-width: 0; }
.catalog-row-label { flex-shrink: 0; width: 32px; padding-top: 8px; color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.catalog-options { display: flex; min-width: 0; flex-wrap: wrap; gap: 3px; }
.catalog-genre-error { margin: 0 0 4px 44px; color: #b34a4a; font-size: 12px; }
.catalog-genre-error button { margin-left: 8px; text-decoration: underline; }
.catalog-mobile-tools { display: none; }
.catalog-result-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 20px; min-height: 58px; padding: 8px 0; }
.catalog-result-heading { display: flex; align-items: center; min-width: 0; flex-wrap: wrap; gap: 8px 12px; color: var(--filter-muted); font-size: 12px; }
.catalog-desktop-summary { max-width: 350px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.catalog-desktop-reset { font-size: 12px; min-height: 32px; padding: 0 8px; }
.catalog-sort-tools { display: flex; flex-shrink: 0; flex-direction: column; align-items: flex-end; gap: 5px; }
.catalog-sort-tools > p { margin: 0; font-size: 11px; color: var(--filter-muted); }
.catalog-sort { padding: 3px; border-radius: 14px; }
.catalog-sort button { min-height: 32px; padding: 0 13px; font-size: 12px; }
.catalog-load-error { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 18px; padding: 14px 18px; border-radius: 16px; background: color-mix(in srgb, #b34a4a 7%, var(--filter-surface)); color: var(--filter-text); font-size: 13px; }
.catalog-load-error p { margin: 0; }
.catalog-load-error .catalog-error-context { margin-top: 5px; color: var(--filter-muted); font-size: 12px; }
.catalog-results { position: relative; }
.catalog-media-grid { transition: opacity 160ms ease; }
.catalog-updating { opacity: 0.65; }
.catalog-progress { position: absolute; top: -6px; left: 0; right: 0; height: 2px; overflow: hidden; border-radius: 2px; background: var(--filter-track); }
.catalog-progress::after { content: ''; position: absolute; height: 100%; width: 35%; background: var(--filter-focus); animation: catalog-fetching 1.1s ease-in-out infinite; }
.catalog-empty { padding: 72px 20px; text-align: center; font-size: 13px; color: var(--filter-muted); }
@keyframes catalog-fetching { from { transform: translateX(-110%); } to { transform: translateX(390%); } }
@media (max-width: 639px) {
  .catalog-filters { padding-bottom: 12px; }
  .catalog-categories { display: flex; width: 100%; margin-bottom: 10px; gap: 0; padding: 3px; }
  .catalog-categories button { flex: 1; min-width: 0; min-height: 44px; padding: 0 8px; font-size: 12px; }
  .catalog-desktop-filters, .catalog-sort-tools, .catalog-desktop-summary, .catalog-desktop-reset { display: none; }
  .catalog-mobile-tools { display: flex; align-items: center; gap: 8px; min-width: 0; }
  .catalog-filter-trigger { background: var(--filter-tone); color: var(--filter-on-tone); padding: 0 12px; }
  .catalog-filter-count { min-width: 18px; padding: 0 4px; border-radius: 6px; background: color-mix(in srgb, var(--filter-on-tone) 10%, transparent); font-size: 11px; line-height: 18px; }
  .catalog-mobile-tools p { flex: 1; min-width: 0; margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--filter-muted); font-size: 11px; }
  .catalog-mobile-tools .catalog-text-button { padding: 0 5px; font-size: 12px; }
  .catalog-result-toolbar { min-height: 52px; padding: 12px 0; }
  .catalog-load-error { align-items: flex-start; padding: 12px; }
}
@media (pointer: coarse) { .catalog-row-label { padding-top: 12px; } .catalog-sort button { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .catalog-media-grid { transition: none; } .catalog-progress::after { animation: none; width: 100%; opacity: 0.5; } }
</style>
