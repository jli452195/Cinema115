<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import UpdateCard from '../components/UpdateCard.vue'
import { catalogAPI } from '../composables/useWebsiteAuth'
import type { RecentUpdateItem, RecentUpdatesPage } from '../types/updates'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const router = useRouter()
const items = ref<RecentUpdateItem[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const error = ref('')
const ready = ref(true)
const timelineStatus = ref<RecentUpdatesPage['status']>('ready')
const hasMore = ref(false)
const nextCursor = ref('')
const dateStrip = ref<HTMLElement>()
const today = ref(shanghaiDate(new Date()))
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

function shanghaiDate(value: Date) {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(value)
  const find = (type: Intl.DateTimeFormatPartTypes) => parts.find(part => part.type === type)?.value || ''
  return `${find('year')}-${find('month')}-${find('day')}`
}
function selectDate(date: string) {
  if (date === selectedDate.value) return
  void router.push({ path: '/updates', query: date === today.value ? {} : { date } })
}
async function revealSelectedDay() {
  await nextTick()
  const strip = dateStrip.value
  const button = strip?.querySelector<HTMLElement>('[aria-pressed="true"]')
  if (!strip || !button) return
  const bounds = strip.getBoundingClientRect()
  const selected = button.getBoundingClientRect()
  if (selected.left < bounds.left || selected.right > bounds.right) {
    strip.scrollTo({ left: strip.scrollLeft + selected.left - bounds.left - (strip.clientWidth - selected.width) / 2 })
  }
}
async function request(signal: AbortSignal, cursor = '') {
  const params = new URLSearchParams()
  params.set('date', selectedDate.value)
  if (cursor) params.set('cursor', cursor)
  const response = await fetch(catalogAPI(`updates?${params}`), {
    headers: { Authorization: `Bearer ${props.token}` },
    signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
  })
  const data = await response.json().catch(() => null) as RecentUpdatesPage | { error?: string } | null
  if (!response.ok || !data || !('items' in data) || !Array.isArray(data.items)) throw new Error(data && 'error' in data ? data.error : `查询失败（HTTP ${response.status}）`)
  return data
}
async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  loadingMore.value = false
  error.value = ''
  items.value = []
  hasMore.value = false
  nextCursor.value = ''
  try {
    const data = await request(current.signal)
    if (!mounted || current.signal.aborted || controller !== current) return
    items.value = data.items
    ready.value = data.ready
    timelineStatus.value = data.status
    hasMore.value = data.hasMore
    nextCursor.value = data.nextCursor || ''
  } catch (value) {
    if (mounted && !current.signal.aborted && controller === current) error.value = value instanceof Error ? value.message : '最近更新加载失败'
  } finally {
    if (mounted && !current.signal.aborted && controller === current) loading.value = false
  }
}
async function loadMore() {
  if (!nextCursor.value || loadingMore.value) return
  const current = controller
  if (!current) return
  loadingMore.value = true
  error.value = ''
  try {
    const data = await request(current.signal, nextCursor.value)
    if (!mounted || current.signal.aborted || controller !== current) return
    items.value.push(...data.items)
    hasMore.value = data.hasMore
    nextCursor.value = data.nextCursor || ''
  } catch (value) {
    if (mounted && !current.signal.aborted && controller === current) error.value = value instanceof Error ? value.message : '继续加载失败'
  } finally {
    if (mounted && controller === current) loadingMore.value = false
  }
}
watch(selectedDate, () => { void load(); void revealSelectedDay() }, { immediate: true })
onMounted(() => {
  dayTimer = setInterval(() => { today.value = shanghaiDate(new Date()) }, 60000)
})
onBeforeUnmount(() => { mounted = false; controller?.abort(); clearInterval(dayTimer) })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <header class="mb-5 flex items-baseline justify-between gap-3">
        <h1 class="text-xl font-bold text-slate-900 dark:text-slate-100">每日更新</h1>
        <span class="text-xs text-slate-500 dark:text-slate-400">最近 7 天</span>
      </header>
      <div ref="dateStrip" role="group" aria-label="选择更新日期" class="flex gap-2 overflow-x-auto pb-2 sm:gap-3">
        <button v-for="day in days" :key="day.date" type="button" :aria-label="day.date + ' ' + day.label" :aria-pressed="selectedDate === day.date"
          class="flex min-w-[88px] flex-1 flex-col items-center gap-1.5 rounded-xl border px-4 py-3 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-400 focus-visible:ring-inset sm:min-w-0"
          :class="selectedDate === day.date ? 'border-amber-400 bg-amber-100 text-amber-950 dark:border-amber-400/40 dark:bg-amber-400/15 dark:text-amber-200' : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:border-slate-600 dark:hover:bg-slate-800'"
          @click="selectDate(day.date)">
          <span class="text-sm font-semibold tabular-nums">{{ day.shortDate }}</span>
          <span class="text-xs">{{ day.label }}</span>
        </button>
      </div>
      <div class="mb-5 mt-5 border-b border-slate-200 pb-4 text-center dark:border-slate-800">
        <h2 class="text-xl font-semibold text-slate-900 dark:text-slate-100">{{ selectedDay.label }}</h2>
        <p class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">{{ selectedDate }} · {{ selectedDay.weekday }}<span v-if="!loading && ready && (!error || items.length)"> · {{ hasMore ? '已加载' : '更新' }} {{ items.length }} 项</span></p>
      </div>

      <div v-if="error && !items.length" role="alert" class="mt-6 rounded-lg border border-red-100 bg-red-50 p-6 text-sm text-red-600 dark:border-red-400/20 dark:bg-red-400/10 dark:text-red-300"><p>{{ error }}</p><button class="mt-4 rounded-lg border border-red-200 px-4 py-2" @click="load">重新加载</button></div>
      <div v-else-if="loading" aria-label="正在加载最近更新" class="mt-7 grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <div v-for="index in 12" :key="index" class="animate-pulse"><div class="aspect-[2/3] rounded-lg bg-slate-200 dark:bg-slate-700"></div><div class="mt-3 h-4 w-4/5 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-2 h-3 w-12 rounded bg-slate-100 dark:bg-slate-800"></div></div>
      </div>
      <div v-else-if="!ready" class="py-24 text-center text-sm text-slate-500 dark:text-slate-400"><p>{{ timelineStatus === 'building' ? '最近更新时间线正在后台构建。' : timelineStatus === 'failed' ? '最近更新时间线暂时构建失败。' : '最近更新时间线尚未建立。' }}</p><p class="mt-2 text-xs text-slate-400 dark:text-slate-400">{{ timelineStatus === 'building' ? '通常只需片刻，完成后刷新页面即可查看。' : timelineStatus === 'failed' ? '后台将在稍后自动重试。' : '请由管理员在 Emby 媒体库同步页面执行一次全量同步。' }}</p></div>
      <div v-else-if="!items.length" class="py-24 text-center text-sm text-slate-500 dark:text-slate-400">这一天暂无更新，试试其他日期。</div>
      <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <UpdateCard v-for="item in items" :key="item.date + ':' + item.kind + ':' + item.tmdbId + ':' + (item.season ?? -1)" :item="item" />
      </div>
      <div v-if="items.length && hasMore" class="mt-10 flex justify-center"><button type="button" :disabled="loadingMore" class="h-10 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50" @click="loadMore">{{ loadingMore ? '正在加载…' : '加载更多' }}</button></div>
      <p v-if="error && items.length" role="alert" class="mt-5 text-center text-sm text-red-500">{{ error }}</p>
      <footer class="mt-12 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-400"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500 dark:text-slate-400">TMDB</a> · 海报及评分数据由 TMDB 提供。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
  </div>
</template>
