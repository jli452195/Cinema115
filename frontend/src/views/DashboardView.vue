<script setup lang="ts">
import { catalogAPI } from '../composables/usePortalAuth'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter, type NavigationGuard } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import DashboardFilterDialog from '../components/DashboardFilterDialog.vue'
import DashboardYearMenu from '../components/DashboardYearMenu.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import MediaCard from '../components/MediaCard.vue'
import PaginationControl from '../components/PaginationControl.vue'
import type { MediaCardItem } from '../types/media'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import { useScrollBoundary } from '../composables/useScrollBoundary'
import { cacheGenres, cachedGenres, rememberDashboardPath, rememberedDashboardPaths, type DashboardKind } from '../utils/dashboardPreferences'
import { dashboardCategoryOptions, dashboardRegions as regions, dashboardSorts as sorts, defaultDashboardRefinements, isDashboardGenreCompatible, readDashboardFilters, type DashboardFilters } from '../utils/dashboardFilters'
import { cacheDashboard, cachedDashboard, dashboardBrowseScope, dashboardCacheLifetime, dashboardLayoutReady, isDashboardPath, rememberDashboardScroll } from '../utils/dashboardBrowseState'

const props = defineProps<{ token: string; kind: 'tv' | 'movie' }>()
const emit = defineEmits<{ logout: [] }>()
const kind = computed(() => props.kind)
const route = useRoute(), router = useRouter()
const workbenchHeader = ref<InstanceType<typeof WorkbenchHeader>>()
const categoryAnchor = ref<HTMLElement>()
const categoriesStuck = useScrollBoundary(categoryAnchor, () => workbenchHeader.value?.element, true)
type FilterKey = keyof DashboardFilters | 'page'
function setFilters(values: Partial<Record<FilterKey, string>>, targetKind = kind.value) {
  const query = targetKind === kind.value ? { ...route.query } : {}
  for (const [key, value] of Object.entries(values) as [FilterKey, string][]) {
    if (!value || (key === 'page' && value === '1') || (key === 'sort' && value === defaultDashboardRefinements.sort)) delete query[key]
    else query[key] = value
  }
  if (Object.keys(values).some(key => key !== 'page')) delete query.page
  const target = router.resolve({ path: `/${targetKind}`, query })
  if (target.fullPath === route.fullPath) return
  rememberDashboardScroll(target.fullPath, { left: 0, top: 0 })
  void router.push(target.fullPath)
}
const activeFilters = computed(() => readDashboardFilters(kind.value, route.query))
function filterModel(key: keyof DashboardFilters) {
  return computed({
    get: () => activeFilters.value[key],
    // 保留首页分类快切的行为；Sheet 会提交整组已校验的筛选条件。
    set: (value: string) => setFilters(key === 'category' ? { category: value, genre: '' } : { [key]: value }),
  })
}
const category = filterModel('category')
const genre = filterModel('genre')
const region = filterModel('region')
const year = filterModel('year')
const sort = filterModel('sort')
const page = computed({
  get: () => {
    const value = route.query.page
    return typeof value === 'string' && /^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 500 ? Number(value) : 1
  },
  set: (value: number) => setFilters({ page: String(value) }),
})
const categoryOptions = computed(() => dashboardCategoryOptions[kind.value])
const totalPages = ref(0)
const pageCount = computed(() => Math.min(500, totalPages.value))
function changePage(value: number) {
  if (!loading.value && !error.value && value >= 1 && value <= pageCount.value) page.value = value
}
const total = ref(0)
const genresByKind = ref({ movie: cachedGenres('movie').genres, tv: cachedGenres('tv').genres })
const genreErrors = ref({ movie: false, tv: false })
const genres = computed(() => genresByKind.value[kind.value])
const visibleGenres = computed(() => genres.value.filter(item => isDashboardGenreCompatible(category.value, item.id)))
const items = ref<MediaCardItem[]>([])
const libraryMatches = useEmbyLibrary(() => props.token, items)
const loading = ref(true)
const error = ref('')
const hasLoaded = ref(false)
const loadedPage = ref(1)
const loadedSummary = ref('')
const sortOptions = sorts.map(({ id, name }) => ({ value: id, label: name }))
const currentYear = new Date().getFullYear()
const years = Array.from({ length: 8 }, (_, i) => String(currentYear - i))
const mobileFiltersOpen = ref(false)
const sheetFilters = computed<Record<DashboardKind, DashboardFilters>>(() => {
  const remembered = (target: DashboardKind) => readDashboardFilters(target, router.resolve(rememberedDashboardPaths[target]).query)
  return {
    movie: kind.value === 'movie' ? activeFilters.value : remembered('movie'),
    tv: kind.value === 'tv' ? activeFilters.value : remembered('tv'),
  }
})
const recentFilterYears = years.slice(0, 5)
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
let displayedKind = kind.value
const resultSummary = computed(() => {
  const labels = [...activeMobileFilters.value]
  if (category.value) labels.unshift(categoryOptions.value.find(item => item.value === category.value)?.label || '')
  return labels.filter(Boolean).join(' · ') || '全部条件 · 热门'
})
async function request(path: string, signal: AbortSignal) {
  // 总超时覆盖连接和响应体读取，导航取消仍由调用方单独识别。
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(25000)])
  try {
    const response = await fetch(catalogAPI(`tmdb/${path}`), { signal: requestSignal, headers: { Authorization: `Bearer ${props.token}` } })
    const data = await response.json().catch(reason => {
      if (requestSignal.aborted) throw reason
      return null
    })
    if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error ?? `查询失败（HTTP ${response.status}）`)
    return data
  } catch (reason) {
    if (!signal.aborted && requestSignal.aborted && requestSignal.reason?.name === 'TimeoutError') throw new Error('TMDB 查询超时，请重新加载')
    throw reason
  }
}
async function refreshGenres(targetKind: DashboardKind, force = false) {
  if (!force && (genreControllers[targetKind] || cachedGenres(targetKind).fresh)) return
  genreControllers[targetKind]?.abort()
  const current = new AbortController(); genreControllers[targetKind] = current
  genreErrors.value[targetKind] = false
  try {
    const data = await request(`genres?kind=${targetKind}`, current.signal)
    if (!Array.isArray(data.genres)) throw new Error('类型数据格式不正确')
    if (mounted && !current.signal.aborted) genresByKind.value[targetKind] = cacheGenres(targetKind, data.genres)
  } catch {
    if (mounted && !current.signal.aborted) genreErrors.value[targetKind] = true
  } finally {
    if (genreControllers[targetKind] === current) delete genreControllers[targetKind]
  }
}
function applyMobileFilters({ kind: targetKind, ...filters }: DashboardFilters & { kind: DashboardKind }) {
  mobileFiltersOpen.value = false
  setFilters(filters, targetKind)
}
function clearMobileFilters() {
  setFilters(defaultDashboardRefinements)
}
async function loadPage() {
  if (route.path !== `/${kind.value}`) return
  controller?.abort()
  const current = new AbortController(); controller = current
  if (displayedKind !== kind.value) {
    displayedKind = kind.value
    items.value = []; total.value = 0; totalPages.value = 0; hasLoaded.value = false
    void refreshGenres(kind.value)
  }
  const requestedPath = route.fullPath, requestedScope = dashboardBrowseScope()
  loading.value = true; error.value = ''
  const requestedPage = page.value, requestedSummary = resultSummary.value
  const ready = async () => {
    await nextTick()
    if (mounted && controller === current && !current.signal.aborted && dashboardBrowseScope() === requestedScope) dashboardLayoutReady(requestedPath)
  }
  const cached = cachedDashboard(requestedPath)
  if (cached) {
    items.value = cached.items; totalPages.value = cached.totalPages; total.value = cached.total
    loadedPage.value = requestedPage; loadedSummary.value = cached.summary; hasLoaded.value = true
    if (Date.now() - cached.cachedAt < dashboardCacheLifetime) { loading.value = false; await ready(); return }
    void ready()
  }
  try {
    const params = new URLSearchParams({ kind: kind.value, page: String(page.value) })
    if (category.value) params.set('category', category.value)
    params.set('genre', genre.value); params.set('region', region.value); params.set('year', year.value); params.set('sort', sort.value)
    const data = await request(`browse?${params}`, current.signal)
    if (!Array.isArray(data.items)) throw new Error('影视数据格式不正确')
    if (!mounted || current.signal.aborted || dashboardBrowseScope() !== requestedScope) return
    items.value = data.items; totalPages.value = data.totalPages; total.value = data.total
    loadedPage.value = requestedPage; loadedSummary.value = requestedSummary; hasLoaded.value = true
    cacheDashboard(requestedPath, { items: data.items, totalPages: data.totalPages, total: data.total, summary: requestedSummary, cachedAt: Date.now() })
  } catch (e) { if (mounted && !current.signal.aborted) error.value = e instanceof Error ? e.message : '看板加载失败' }
  finally { if (mounted && controller === current) { loading.value = false; await ready() } }
}
watch(() => [dashboardBrowseScope(), kind.value, route.fullPath], () => {
  if (route.path !== `/${kind.value}`) return
  rememberDashboardPath(kind.value, route.fullPath)
  void loadPage()
}, { immediate: true, flush: 'post' })
function saveScroll(path = route.fullPath) {
  if (isDashboardPath(path.split('?')[0]!.split('#')[0]!) && hasLoaded.value) rememberDashboardScroll(path, { left: window.scrollX, top: window.scrollY })
}
const beforeDashboardNavigation: NavigationGuard = (_to, from) => {
  mobileFiltersOpen.value = false
  saveScroll(from.fullPath)
}
onBeforeRouteLeave(beforeDashboardNavigation)
onBeforeRouteUpdate(beforeDashboardNavigation)
function pageHidden() { saveScroll() }
onMounted(() => {
  window.addEventListener('pagehide', pageHidden)
  void refreshGenres('movie')
  void refreshGenres('tv')
})
onBeforeUnmount(() => {
  window.removeEventListener('pagehide', pageHidden)
  mounted = false
  controller?.abort()
  Object.values(genreControllers).forEach(current => current?.abort())
})
</script>

<template>
  <div class="catalog-home flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader ref="workbenchHeader" @logout="emit('logout')" />
    <main class="catalog-dashboard catalog-filter-theme workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <h1 class="sr-only">{{ kind === 'movie' ? '电影看板' : '剧集看板' }}</h1>
      <div ref="categoryAnchor" class="catalog-sticky-anchor" aria-hidden="true"></div>
      <section aria-label="看板筛选" class="catalog-filters">
        <div class="catalog-primary-tools">
          <div class="catalog-category-pill glass-floating-pill" :data-stuck="categoriesStuck || undefined">
            <SegmentedControl v-model="category" :options="categoryOptions" label="影视类别" class="catalog-categories" density="compact" appearance="pwa-pill" />
          </div>
          <button type="button" class="catalog-filter-trigger glass-floating-pill" :class="{ 'has-filters': activeMobileFilters.length }" :data-stuck="categoriesStuck || undefined" aria-haspopup="dialog" :aria-label="activeMobileFilters.length ? `筛选，已选 ${activeMobileFilters.length} 项` : '筛选'" :aria-expanded="mobileFiltersOpen" @click="mobileFiltersOpen = true">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 5h17l-6.6 7.8v5.4l-3.8 1.5v-6.9L3.5 5z" /></svg>
            <span v-if="activeMobileFilters.length" class="catalog-filter-dot" aria-hidden="true"></span>
          </button>
        </div>
        <div class="catalog-desktop-filters">
          <div role="group" aria-label="影视题材" class="catalog-filter-row"><span class="catalog-row-label">题材</span><div class="catalog-options">
            <button type="button" class="catalog-chip" :aria-pressed="genre === ''" @click="genre = ''">全部</button><button v-for="item in visibleGenres" :key="item.id" type="button" class="catalog-chip" :aria-pressed="genre === String(item.id)" @click="genre = String(item.id)">{{ item.name }}</button>
          </div></div>
          <p v-if="genreErrors[kind] && !genres.length" class="catalog-genre-error">题材加载失败。<button type="button" @click="refreshGenres(kind, true)">重试</button></p>
          <div role="group" aria-label="影视地区" class="catalog-filter-row"><span class="catalog-row-label">地区</span><div class="catalog-options"><button v-for="item in regions" :key="item.id" type="button" class="catalog-chip" :aria-pressed="region === item.id" @click="region = item.id">{{ item.name }}</button></div></div>
          <div role="group" aria-label="影视年份" class="catalog-filter-row"><span class="catalog-row-label">年份</span><div class="catalog-options">
            <button type="button" class="catalog-chip" :aria-pressed="year === ''" @click="year = ''">全部</button><button v-for="value in years" :key="value" type="button" class="catalog-chip" :aria-pressed="year === value" @click="year = value">{{ value }}</button><DashboardYearMenu v-model="year" :recent-years="years" />
          </div></div>
        </div>
      </section>
      <div class="catalog-result-toolbar">
        <div class="catalog-result-heading">
          <span role="status"><template v-if="loading || error">{{ loading ? hasLoaded ? '正在更新结果…' : '正在查询 TMDB…' : hasLoaded ? '更新失败，保留上次结果' : '暂时无法加载' }}</template><template v-else>找到 <strong class="catalog-result-total">{{ total.toLocaleString() }}</strong> 部{{ kind === 'tv' ? '剧集' : '电影' }}</template></span>
          <template v-if="activeMobileFilters.length"><span class="catalog-filter-summary" :title="activeMobileFilters.join(' · ')">{{ activeMobileFilters.join(' · ') }}</span><button type="button" class="catalog-text-button catalog-filter-reset" @click="clearMobileFilters">清除筛选</button></template>
        </div>
        <div class="catalog-sort-tools"><SegmentedControl v-model="sort" :options="sortOptions" label="结果排序" class="catalog-sort" density="compact" /><p v-if="sort === 'rating'">评分排序至少需要 100 人评价</p></div>
      </div>
      <div v-if="error" role="alert" class="catalog-load-error"><div><p>{{ error }}</p><p v-if="hasLoaded" class="catalog-error-context">当前保留上次结果：{{ loadedSummary }}，第 {{ loadedPage }} 页。</p></div><button type="button" class="catalog-chip" @click="refreshGenres(kind); loadPage()">重新加载</button></div>
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
    <DashboardFilterDialog v-if="mobileFiltersOpen"
      :kind="kind" :initial-filters="sheetFilters" :recent-years="recentFilterYears"
      :genre-errors="genreErrors" :genres-by-kind="genresByKind"
      @close="mobileFiltersOpen = false" @apply="applyMobileFilters" @retry-genres="refreshGenres($event, true)" />
  </div>
</template>

<style scoped>
.catalog-filters { padding: 2px 0 10px; border-bottom: 1px solid var(--filter-outline); }
.catalog-sticky-anchor { height: 1px; margin-bottom: -1px; pointer-events: none; }
.catalog-primary-tools { margin-bottom: 10px; }
.catalog-category-pill { min-width: 0; }
.catalog-categories { vertical-align: top; }
.catalog-desktop-filters { display: flex; flex-direction: column; gap: 6px; }
.catalog-filter-row { display: flex; align-items: flex-start; gap: 12px; min-width: 0; }
.catalog-row-label { flex-shrink: 0; width: 32px; padding-top: 8px; color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.catalog-options { display: flex; min-width: 0; flex-wrap: wrap; gap: 3px; }
.catalog-genre-error { margin: 0 0 4px 44px; color: #b34a4a; font-size: 12px; }
.catalog-genre-error button { margin-left: 8px; text-decoration: underline; }
.catalog-filter-trigger { display: none; }
.catalog-result-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 20px; min-height: 58px; padding: 8px 0; }
.catalog-result-heading { display: flex; align-items: center; min-width: 0; flex-wrap: wrap; gap: 8px 12px; color: var(--filter-muted); font-size: 12px; }
.catalog-filter-summary { max-width: 350px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.catalog-filter-reset { font-size: 12px; min-height: 32px; padding: 0 8px; }
.catalog-sort-tools { display: flex; flex-shrink: 0; flex-direction: column; align-items: flex-end; gap: 5px; }
.catalog-sort-tools > p { margin: 0; font-size: 11px; color: var(--filter-muted); }
.catalog-sort { padding: 3px; border-radius: 14px; }
.catalog-sort :deep(.segmented-control-option) { min-width: 0; min-height: 32px; padding: 0 13px; font-size: 12px; }
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
  .catalog-home { --workbench-toolbar-height: 56px; --workbench-header-height: calc(var(--workbench-toolbar-height) + var(--app-safe-top)); }
  .catalog-dashboard { padding-top: calc(var(--workbench-header-height) + 4px); padding-inline: max(16px, var(--app-safe-left)) max(16px, var(--app-safe-right)); }
  /* 外层吸顶行完全透明，海报从两个独立玻璃控件之间经过。 */
  .catalog-filters { position: sticky; top: var(--workbench-header-height); z-index: 30; padding: 6px 0; border-bottom: 0; background: transparent; pointer-events: none; }
  .catalog-primary-tools { display: flex; align-items: center; gap: 8px; margin-bottom: 0; }
  .catalog-category-pill { flex: 1 1 auto; height: 44px; border-radius: 999px; pointer-events: auto; }
  .catalog-categories { display: flex; width: 100%; }
  .catalog-category-pill :deep(.segmented-control-pwa-pill) { background: transparent; box-shadow: none; }
  .catalog-category-pill :deep(.segmented-control-pwa-pill .segmented-control-option) { min-height: 42px; }
  .catalog-desktop-filters, .catalog-sort-tools, .catalog-filter-summary, .catalog-filter-reset { display: none; }
  .catalog-result-toolbar { min-height: 36px; gap: 8px; padding: 0 0 8px; }
  .catalog-result-heading { font-size: 13px; }
  .catalog-result-total { color: var(--filter-text); font-size: 14px; font-weight: 700; font-variant-numeric: tabular-nums; }
  .catalog-filter-trigger { position: relative; display: inline-flex; flex: 0 0 44px; width: 44px; height: 44px; align-items: center; justify-content: center; padding: 0; border-radius: 50%; color: var(--filter-text); pointer-events: auto; -webkit-tap-highlight-color: transparent; }
  .catalog-filter-trigger.has-filters { color: rgb(var(--selection-content)); }
  .catalog-filter-trigger:active { opacity: .8; }
  .catalog-filter-dot { position: absolute; top: 8px; right: 8px; width: 6px; height: 6px; border-radius: 50%; background: rgb(var(--selection-content)); }
  .catalog-load-error { align-items: flex-start; padding: 12px; }
}
@media (pointer: coarse) { .catalog-row-label { padding-top: 12px; } .catalog-sort :deep(.segmented-control-option) { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .catalog-media-grid { transition: none; } .catalog-progress::after { animation: none; width: 100%; opacity: 0.5; } }
</style>
