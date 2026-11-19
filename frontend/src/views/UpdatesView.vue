<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconChevronDown, IconLoader2 } from '@tabler/icons-vue'
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
const compactDateHeading = computed(() => {
  const { date, weekday } = selectedDay.value
  return `${Number(date.slice(5, 7))}月${Number(date.slice(8))}日 · ${weekday}`
})
const hasSnapshot = computed(() => !!loadedDate.value)
const staleResults = computed(() => hasSnapshot.value && loadedDate.value !== selectedDate.value)
const dateError = computed(() => errorScope.value === 'date' ? error.value : '')
const paginationError = computed(() => errorScope.value === 'more' ? error.value : '')
const showEnd = computed(() => items.value.length > 0 && ready.value && !hasMore.value && !loading.value && !loadingMore.value && !staleResults.value && !error.value)
const timelineMessage = computed(() => timelineStatus.value === 'building' ? '最近更新时间线正在后台构建。' : timelineStatus.value === 'failed' ? '最近更新时间线暂时构建失败。' : '最近更新时间线尚未建立。')
const resultStatus = computed(() => {
  if (loading.value) return staleResults.value ? `正在加载 · 暂显 ${loadedDate.value}` : '正在加载…'
  if (dateError.value) return staleResults.value ? `${loadedDate.value} 结果 · 加载失败` : '加载失败'
  if (staleResults.value) return `${loadedDate.value} 暂存结果`
  if (!ready.value) return timelineStatus.value === 'failed' ? '时间线构建失败' : timelineStatus.value === 'waiting' ? '等待时间线' : '时间线准备中'
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
  ready.value = data.ready && data.status === 'ready'
  timelineStatus.value = data.status
  hasMore.value = data.hasMore
  nextCursor.value = data.nextCursor || ''
  loadedDate.value = date
  if (!ready.value) dayCache.delete(date)
}
function itemKey(item: RecentUpdateItem) { return `${item.date}:${item.kind}:${item.tmdbId}:${item.season ?? -1}` }
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
    ready.value = data.ready && data.status === 'ready'
    timelineStatus.value = data.status
    if (!ready.value) {
      hasMore.value = false
      nextCursor.value = ''
      dayCache.delete(loadedDate.value)
      return
    }
    const positions = new Map(items.value.map((item, index) => [itemKey(item), index]))
    for (const item of data.items) {
      const key = itemKey(item), index = positions.get(key)
      if (index === undefined) {
        positions.set(key, items.value.length)
        items.value.push(item)
      } else items.value[index] = item
    }
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
    <!-- 公共顶栏有多个根节点，手机端由页面自己的标题接管。 -->
    <div class="hidden sm:block">
      <WorkbenchHeader @logout="emit('logout')" />
    </div>
    <main class="updates-content catalog-filter-theme workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <header class="updates-heading">
        <h1>更新日历</h1><span>最近 7 天</span>
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
        <h2><span class="updates-desktop-date">{{ selectedDay.label }}</span><span class="updates-mobile-date">{{ compactDateHeading }}</span></h2>
        <p role="status"><span class="updates-desktop-date">{{ selectedDate }} · {{ selectedDay.weekday }} · </span>{{ resultStatus }}</p>
      </div>
      <div v-if="dateError" class="updates-error">
        <p role="alert">{{ dateError }}</p>
        <button type="button" class="catalog-chip" @click="load(true)">重新加载</button>
      </div>
      <section class="updates-results" aria-label="更新列表" :aria-busy="loading || loadingMore">
        <p v-if="hasSnapshot && !ready && items.length" role="status" class="updates-pending">{{ timelineMessage }}暂时保留 {{ loadedDate }} 已加载结果，刷新页面后可重新加载。</p>
        <div v-if="loading && hasSnapshot" class="updates-progress" aria-hidden="true"></div>
        <div v-if="loading && !hasSnapshot" aria-label="正在加载最近更新" class="catalog-card-grid updates-placeholder">
          <div v-for="index in 36" :key="index" class="animate-pulse motion-reduce:animate-none"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="updates-placeholder-title w-4/5 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-1 h-[18px] w-12 rounded bg-slate-100 dark:bg-slate-800"></div></div>
        </div>
        <div v-else-if="hasSnapshot && !ready && !items.length" class="updates-empty"><p>{{ timelineMessage }}</p><p class="mt-2 text-xs">{{ timelineStatus === 'building' ? '通常只需片刻，完成后刷新页面即可查看。' : timelineStatus === 'failed' ? '后台将在稍后自动重试。' : '请由管理员在 Emby 媒体库同步页面执行一次全量同步。' }}</p></div>
        <div v-else-if="hasSnapshot && !items.length" class="updates-empty">{{ staleResults ? loadedDate + ' 暂无更新。' : '这一天暂无更新，试试其他日期。' }}</div>
        <div v-else-if="items.length" class="catalog-card-grid updates-grid" :class="{ 'is-updating': loading || staleResults }">
          <UpdateCard v-for="item in items" :key="itemKey(item)" :item="item" />
        </div>
      </section>
      <footer class="updates-footer">
        <div v-if="items.length && hasMore" class="updates-more">
          <p v-if="paginationError" class="updates-more-error" role="alert">{{ paginationError }}</p>
          <button type="button" :disabled="loadingMore || loading || staleResults || !!dateError" class="catalog-chip" @click="loadMore">
            {{ loadingMore ? '正在加载更多…' : paginationError ? '重试加载更多' : '加载更多' }}
            <IconLoader2 v-if="loadingMore" class="animate-spin motion-reduce:animate-none" aria-hidden="true" />
            <IconChevronDown v-else-if="!paginationError" aria-hidden="true" />
          </button>
        </div>
        <p v-else-if="showEnd" class="updates-end">已显示当日全部更新</p>
        <div class="updates-attribution">
          <a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer">TMDB</a> · 海报及评分数据由 TMDB 提供。
          <p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
        </div>
      </footer>
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
.updates-mobile-date { display: none; }
.updates-error { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; padding: 12px 16px; border-radius: 16px; background: color-mix(in srgb, #b34a4a 7%, var(--filter-surface)); color: var(--filter-text); font-size: 13px; }
.updates-error p { margin: 0; overflow-wrap: anywhere; }
.updates-results { position: relative; }
.updates-pending { margin: 0 0 16px; color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.updates-grid { transition: opacity 160ms ease; }
.updates-grid.is-updating { opacity: 0.65; }
.updates-placeholder-title { height: 40px; margin-top: 10px; }
.updates-progress { position: absolute; top: -8px; left: 0; right: 0; height: 2px; overflow: hidden; border-radius: 2px; background: var(--filter-track); }
.updates-progress::after { content: ''; position: absolute; height: 100%; width: 35%; background: var(--filter-focus); animation: updates-fetching 1.1s ease-in-out infinite; }
.updates-empty { padding: 80px 16px; text-align: center; color: var(--filter-muted); font-size: 13px; line-height: 22px; }
.updates-more { display: grid; justify-items: center; gap: 8px; margin-top: 32px; }
.updates-more-error { max-width: 100%; margin: 0; font-size: 13px; line-height: 20px; color: rgb(var(--status-danger)); text-align: center; overflow-wrap: anywhere; }
.updates-more .catalog-chip { min-height: 44px; padding: 0 24px; background: var(--filter-tone); color: var(--filter-on-tone); }
.updates-more button:disabled { opacity: 0.45; cursor: default; }
.updates-end { margin: 24px 0 0; text-align: center; color: var(--filter-muted); font-size: 12px; line-height: 20px; }
.updates-attribution { margin-top: 48px; padding-top: 20px; border-top: 1px solid var(--filter-outline); color: var(--filter-muted); font-size: 12px; line-height: 24px; }
.updates-attribution a { color: var(--filter-text); font-weight: 600; text-decoration: underline; text-underline-offset: 3px; }
.updates-end + .updates-attribution { margin-top: 20px; }
@keyframes updates-fetching { from { transform: translateX(-110%); } to { transform: translateX(390%); } }
@media (max-width: 639px) {
  .updates-content { padding-top: calc(var(--app-safe-top) + 12px); }
  .updates-heading { display: block; margin-bottom: 12px; text-align: center; }
  .updates-heading h1 { font-size: 19px; line-height: 28px; }
  .updates-heading > span { display: none; }
  .updates-date-scroll { padding-bottom: 4px; }
  .updates-dates { width: 100%; }
  .updates-dates button { flex: 1 1 0; min-width: 0; min-height: 44px; padding: 4px 2px; }
  .updates-dates button[aria-pressed="true"] { color: var(--filter-on-tone); }
  .updates-dates .catalog-segment-indicator { background: var(--filter-tone); }
  .updates-date-number { font-size: 12px; line-height: 18px; }
  .updates-date-label { font-size: 11px; line-height: 16px; }
  .updates-summary { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; margin: 10px 0 12px; padding: 0; border: 0; text-align: left; }
  .updates-summary h2 { flex-shrink: 0; font-size: 14px; line-height: 22px; }
  .updates-summary p { margin: 0; text-align: right; overflow-wrap: anywhere; font-variant-numeric: tabular-nums; }
  .updates-desktop-date { display: none; }
  .updates-mobile-date { display: inline; }
  .updates-error { align-items: flex-start; padding: 12px; }
  .updates-grid, .updates-placeholder { row-gap: 18px; }
  .updates-placeholder-title { height: 20px; margin-top: 8px; }
  .updates-more { margin-top: 18px; }
  .updates-more .catalog-chip { width: 100%; max-width: 224px; border: 1px solid var(--filter-outline); background: transparent; color: var(--filter-on-tone); font-weight: 600; }
  .updates-more .catalog-chip:active:not(:disabled) { background: var(--filter-tone); }
  .updates-end { margin-top: 18px; }
  .updates-attribution, .updates-end + .updates-attribution { margin-top: 16px; padding-top: 12px; text-align: center; line-height: 19px; }
  .updates-attribution p { margin-top: 2px; }
}
@media (prefers-reduced-motion: reduce) {
  .updates-grid, .updates-date-content { transition: none; }
  .updates-dates button:active .updates-date-content { transform: none; }
  .updates-progress::after { animation: none; width: 100%; opacity: 0.5; }
}
</style>
