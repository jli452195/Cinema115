<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import UpdateCard from '../components/UpdateCard.vue'
import { catalogAPI } from '../composables/usePortalAuth'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import type { RecentUpdateItem, RecentUpdatesPage } from '../types/updates'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const router = useRouter()
const items = ref<RecentUpdateItem[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const error = ref('')
const errorScope = ref<'date' | 'more'>('date')
const loadedDate = ref('')
const ready = ref(true)
const timelineStatus = ref<RecentUpdatesPage['status']>('ready')
const hasMore = ref(false)
const nextCursor = ref('')
const scrollContainer = ref<HTMLElement>()
const dateStrip = ref<HTMLElement>()
const today = ref(shanghaiDate(new Date()))
type DaySnapshot = Pick<RecentUpdatesPage, 'items' | 'ready' | 'status' | 'hasMore' | 'nextCursor'> & { cachedAt: number }
const dayCache = new Map<string, DaySnapshot>()
const cacheLifetimeMs = 60000
let dayTimer: ReturnType<typeof setInterval> | undefined
let controller: AbortController | undefined
let mounted = true

const days = computed(() => Array.from({ length: 7 }, (_, index) => {
  const value = new Date(new Date(`${today.value}T00:00:00+08:00`).getTime() - index * 86400000)
  const date = shanghaiDate(value)
  const weekday = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'short' }).format(value)
  return { date, weekday, shortDate: `${Number(date.slice(5, 7))}/${Number(date.slice(8))}`, label: index === 0 ? '今天' : index === 1 ? '昨天' : weekday }
}))
const selectedDay = computed(() => days.value.find(day => day.date === route.query.date) || days.value[0]!)
const selectedDate = computed(() => selectedDay.value.date)
const hasSnapshot = computed(() => !!loadedDate.value)
const staleResults = computed(() => hasSnapshot.value && loadedDate.value !== selectedDate.value)
const resultStatus = computed(() => {
  if (loading.value) return staleResults.value ? `正在加载 · 暂显 ${loadedDate.value}` : '正在加载…'
  if (error.value && errorScope.value === 'date') return staleResults.value ? `${loadedDate.value} 结果 · 加载失败` : '加载失败'
  if (staleResults.value) return `${loadedDate.value} 暂存结果`
  if (!ready.value) return '时间线准备中'
  return `${hasMore.value ? '已加载' : '更新'} ${items.value.length} 项`
})

const { indicatorStyle } = useSlidingIndicator(dateStrip, '[aria-pressed="true"]', [selectedDate, days], {
  durationMs: 240,
  easing: 'cubic-bezier(0.2, 0, 0, 1)',
})

function shanghaiDate(value: Date) {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(value)
  const find = (type: Intl.DateTimeFormatPartTypes) => parts.find(part => part.type === type)?.value || ''
  return `${find('year')}-${find('month')}-${find('day')}`
}
function selectDate(date: string) {
  if (date === selectedDate.value) return
  void router.push({ path: '/updates', query: date === today.value ? {} : { date } })
}
function applySnapshot(date: string, data: DaySnapshot | RecentUpdatesPage) {
  items.value = [...data.items]
  ready.value = data.ready
  timelineStatus.value = data.status
  hasMore.value = data.hasMore
  nextCursor.value = data.nextCursor || ''
  loadedDate.value = date
}
function pruneCache() {
  const validDates = new Set(days.value.map(day => day.date))
  for (const date of dayCache.keys()) if (!validDates.has(date)) dayCache.delete(date)
}
function rememberSnapshot(cachedAt = Date.now()) {
  if (!ready.value || timelineStatus.value !== 'ready') return
  dayCache.set(loadedDate.value, {
    items: [...items.value], ready: ready.value, status: timelineStatus.value,
    hasMore: hasMore.value, nextCursor: nextCursor.value, cachedAt,
  })
  pruneCache()
}
async function revealSelectedDay() {
  await nextTick()
  const wrapper = scrollContainer.value
  const button = dateStrip.value?.querySelector<HTMLElement>('[aria-pressed="true"]')
  if (!wrapper || !button) return
  const bounds = wrapper.getBoundingClientRect()
  const selected = button.getBoundingClientRect()
  if (selected.left < bounds.left || selected.right > bounds.right) {
    wrapper.scrollTo({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', left: wrapper.scrollLeft + selected.left - bounds.left - (wrapper.clientWidth - selected.width) / 2 })
  }
}
async function request(date: string, signal: AbortSignal, cursor = '') {
  const params = new URLSearchParams()
  params.set('date', date)
  if (cursor) params.set('cursor', cursor)
  const response = await fetch(catalogAPI(`updates?${params}`), {
    headers: { Authorization: `Bearer ${props.token}` },
    signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
  })
  const data = await response.json().catch(() => null) as RecentUpdatesPage | { error?: string } | null
  if (!response.ok || !data || !('items' in data) || !Array.isArray(data.items)) throw new Error((data && 'error' in data ? data.error : '') || `查询失败（HTTP ${response.status}）`)
  return data
}
async function load(force = false) {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loadingMore.value = false
  error.value = ''
  errorScope.value = 'date'
  const requestedDate = selectedDate.value
  const cached = dayCache.get(requestedDate)
  if (!force && cached && Date.now() - cached.cachedAt < cacheLifetimeMs) {
    applySnapshot(requestedDate, cached)
    loading.value = false
    return
  }
  loading.value = true
  try {
    const data = await request(requestedDate, current.signal)
    if (!mounted || current.signal.aborted || controller !== current) return
    applySnapshot(requestedDate, data)
    rememberSnapshot()
  } catch (value) {
    if (mounted && !current.signal.aborted && controller === current) error.value = value instanceof Error ? value.message : '最近更新加载失败'
  } finally {
    if (mounted && !current.signal.aborted && controller === current) loading.value = false
  }
}
async function loadMore() {
  if (!nextCursor.value || loadingMore.value || loading.value || staleResults.value || !hasMore.value || !ready.value) return
  const current = controller
  if (!current) return
  loadingMore.value = true
  error.value = ''
  errorScope.value = 'more'
  try {
    const data = await request(loadedDate.value, current.signal, nextCursor.value)
    if (!mounted || current.signal.aborted || controller !== current) return
    items.value.push(...data.items)
    hasMore.value = data.hasMore
    nextCursor.value = data.nextCursor || ''
    rememberSnapshot(dayCache.get(loadedDate.value)?.cachedAt)
  } catch (value) {
    if (mounted && !current.signal.aborted && controller === current) error.value = value instanceof Error ? value.message : '继续加载失败'
  } finally {
    if (mounted && controller === current) loadingMore.value = false
  }
}
watch(selectedDate, () => { void load(); void revealSelectedDay() }, { immediate: true })
watch(days, () => { pruneCache(); void revealSelectedDay() })
onMounted(() => {
  dayTimer = setInterval(() => { today.value = shanghaiDate(new Date()) }, 60000)
})
onBeforeUnmount(() => { mounted = false; controller?.abort(); clearInterval(dayTimer) })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="catalog-filter-theme workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <header class="updates-heading">
        <h1>每日更新</h1><span>最近 7 天</span>
      </header>
      <div ref="scrollContainer" class="updates-date-scroll">
        <div ref="dateStrip" role="group" aria-label="选择更新日期" class="catalog-segments updates-dates">
          <div class="catalog-segment-indicator" :style="indicatorStyle" aria-hidden="true"></div>
          <button v-for="day in days" :key="day.date" type="button" :aria-label="day.date + ' ' + day.label" :aria-pressed="selectedDate === day.date"
            @click="selectDate(day.date)">
            <span class="updates-date-content"><span class="updates-date-number">{{ day.shortDate }}</span><span class="updates-date-label">{{ day.label }}</span></span>
          </button>
        </div>
      </div>
      <div class="updates-summary">
        <h2>{{ selectedDay.label }}</h2>
        <p role="status">{{ selectedDate }} · {{ selectedDay.weekday }}<span> · {{ resultStatus }}</span></p>
      </div>
      <div v-if="error" role="alert" class="updates-error"><p>{{ error }}</p><button type="button" class="catalog-chip" @click="errorScope === 'more' ? loadMore() : load(true)">{{ errorScope === 'more' ? '重试加载更多' : '重新加载' }}</button></div>
      <section class="updates-results" aria-label="更新列表" :aria-busy="loading || loadingMore">
        <div v-if="loading && hasSnapshot" class="updates-progress" aria-hidden="true"></div>
        <div v-if="loading && !hasSnapshot" aria-label="正在加载最近更新" class="catalog-card-grid">
          <div v-for="index in 36" :key="index" class="animate-pulse"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-2.5 h-10 w-4/5 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-1 h-[18px] w-12 rounded bg-slate-100 dark:bg-slate-800"></div></div>
        </div>
        <div v-else-if="hasSnapshot && !ready" class="updates-empty"><p>{{ timelineStatus === 'building' ? '最近更新时间线正在后台构建。' : timelineStatus === 'failed' ? '最近更新时间线暂时构建失败。' : '最近更新时间线尚未建立。' }}</p><p class="mt-2 text-xs">{{ timelineStatus === 'building' ? '通常只需片刻，完成后刷新页面即可查看。' : timelineStatus === 'failed' ? '后台将在稍后自动重试。' : '请由管理员在 Emby 媒体库同步页面执行一次全量同步。' }}</p></div>
        <div v-else-if="hasSnapshot && !items.length" class="updates-empty">{{ staleResults ? loadedDate + ' 暂无更新。' : '这一天暂无更新，试试其他日期。' }}</div>
        <div v-else-if="items.length" class="catalog-card-grid updates-grid" :class="{ 'is-updating': loading || staleResults }">
          <UpdateCard v-for="item in items" :key="item.date + ':' + item.kind + ':' + item.tmdbId + ':' + (item.season ?? -1)" :item="item" />
        </div>
      </section>
      <div v-if="items.length && hasMore" class="updates-more">
        <button type="button" :disabled="loadingMore || loading || staleResults || (!!error && errorScope === 'date')" class="catalog-chip" @click="loadMore">{{ loadingMore ? '正在加载…' : '加载更多' }}<svg v-if="!loadingMore" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m7 10 5 5 5-5" /></svg></button>
      </div>
      <footer class="mt-12 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-400"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500 dark:text-slate-400">TMDB</a> · 海报及评分数据由 TMDB 提供。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
  </div>
</template>

<style scoped>
.updates-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 12px 24px; margin-bottom: 16px; }
.updates-heading h1 { margin: 0; color: var(--filter-text); font-size: 20px; line-height: 28px; font-weight: 700; }
.updates-heading > span { color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.updates-date-scroll { display: flex; justify-content: center; overflow-x: auto; padding: 4px 4px 6px; margin: -4px -4px 0; scrollbar-width: thin; }
.updates-dates { max-width: none; flex-shrink: 0; gap: 2px; }
.updates-dates .catalog-segment-indicator { box-shadow: 0 2px 5px rgb(0 0 0 / 8%); }
.dark .updates-dates .catalog-segment-indicator { background: color-mix(in srgb, #fff 6%, var(--filter-tone)); box-shadow: 0 1px 3px rgb(0 0 0 / 20%); }
.updates-dates button { display: flex; min-width: 64px; min-height: 48px; flex-shrink: 0; flex-direction: column; justify-content: center; padding: 4px 10px; transition: color 160ms ease; }
.updates-date-content { display: flex; flex-direction: column; align-items: center; gap: 1px; transition: transform 80ms ease; }
.updates-dates button:active .updates-date-content { transform: scale(0.97); }
.updates-date-number { font-size: 13px; font-weight: 600; font-variant-numeric: tabular-nums; line-height: 18px; }
.updates-date-label { font-size: 11px; font-weight: 400; line-height: 16px; }
.updates-summary { margin: 16px 0 20px; padding-bottom: 16px; border-bottom: 1px solid var(--filter-outline); text-align: center; }
.updates-summary h2 { margin: 0; color: var(--filter-text); font-size: 20px; font-weight: 600; line-height: 28px; }
.updates-summary p { margin: 6px 0 0; color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.updates-error { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; padding: 12px 16px; border-radius: 16px; background: color-mix(in srgb, #b34a4a 7%, var(--filter-surface)); color: var(--filter-text); font-size: 13px; }
.updates-error p { margin: 0; overflow-wrap: anywhere; }
.updates-results { position: relative; }
.updates-grid { transition: opacity 160ms ease; }
.updates-grid.is-updating { opacity: 0.65; }
.updates-progress { position: absolute; top: -8px; left: 0; right: 0; height: 2px; overflow: hidden; border-radius: 2px; background: var(--filter-track); }
.updates-progress::after { content: ''; position: absolute; height: 100%; width: 35%; background: var(--filter-focus); animation: updates-fetching 1.1s ease-in-out infinite; }
.updates-empty { padding: 80px 16px; text-align: center; color: var(--filter-muted); font-size: 13px; line-height: 22px; }
.updates-more { display: flex; justify-content: center; margin-top: 32px; }
.updates-more .catalog-chip { min-height: 44px; padding: 0 24px; background: var(--filter-tone); color: var(--filter-on-tone); }
.updates-more button:disabled { opacity: 0.45; cursor: default; }
@keyframes updates-fetching { from { transform: translateX(-110%); } to { transform: translateX(390%); } }
@media (max-width: 639px) {
  .updates-heading { margin-bottom: 12px; }
  .updates-heading h1 { font-size: 18px; line-height: 26px; }
  .updates-dates { width: 100%; }
  .updates-dates button { flex: 1 1 0; min-width: 0; min-height: 44px; padding: 4px 2px; }
  .updates-date-number { font-size: 12px; line-height: 18px; }
  .updates-date-label { font-size: 10px; line-height: 16px; }
  .updates-summary { margin: 12px 0 16px; padding-bottom: 12px; }
  .updates-summary p { height: 40px; }
  .updates-summary p > span { display: block; }
  .updates-error { align-items: flex-start; padding: 12px; }
}
@media (prefers-reduced-motion: reduce) {
  .updates-grid, .updates-date-content { transition: none; }
  .updates-dates button:active .updates-date-content { transform: none; }
  .updates-progress::after { animation: none; width: 100%; opacity: 0.5; }
}
</style>
