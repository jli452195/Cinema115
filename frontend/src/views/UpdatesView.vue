<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
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
const period = ref({ from: '', to: '' })
let controller: AbortController | undefined
let mounted = true

const ranges = [
  { id: 'today', label: '今天' },
  { id: 'yesterday', label: '昨天' },
  { id: '7d', label: '最近 7 天' },
] as const
const selectedDate = computed(() => typeof route.query.date === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(route.query.date) ? route.query.date : '')
const selectedRange = computed(() => selectedDate.value ? '' : ['today', 'yesterday', '7d'].includes(String(route.query.range)) ? String(route.query.range) : 'today')
const today = shanghaiDate(new Date())
const groups = computed(() => {
  const result: { date: string; items: RecentUpdateItem[] }[] = []
  for (const item of items.value) {
    let group = result[result.length - 1]
    if (!group || group.date !== item.date) {
      group = { date: item.date, items: [] }
      result.push(group)
    }
    group.items.push(item)
  }
  return result
})

function shanghaiDate(value: Date) {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(value)
  const find = (type: Intl.DateTimeFormatPartTypes) => parts.find(part => part.type === type)?.value || ''
  return `${find('year')}-${find('month')}-${find('day')}`
}
function dateLabel(date: string) {
  if (date === today) return '今天'
  const yesterday = new Date(new Date(`${today}T00:00:00+08:00`).getTime() - 24 * 60 * 60 * 1000)
  if (date === shanghaiDate(yesterday)) return '昨天'
  const [year, month, day] = date.split('-')
  return `${year}年${Number(month)}月${Number(day)}日`
}
function selectRange(range: string) {
  void router.push({ path: '/updates', query: range === 'today' ? {} : { range } })
}
function selectDate(event: Event) {
  const value = (event.target as HTMLInputElement).value
  if (value) void router.push({ path: '/updates', query: { date: value } })
}
async function request(signal: AbortSignal, cursor = '') {
  const params = new URLSearchParams()
  if (selectedDate.value) params.set('date', selectedDate.value)
  else params.set('range', selectedRange.value)
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
  try {
    const data = await request(current.signal)
    if (!mounted || current.signal.aborted || controller !== current) return
    items.value = data.items
    ready.value = data.ready
    timelineStatus.value = data.status
    hasMore.value = data.hasMore
    nextCursor.value = data.nextCursor || ''
    period.value = { from: data.from, to: data.to }
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
watch(() => [route.query.range, route.query.date], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { mounted = false; controller?.abort() })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 py-6 sm:px-8 lg:px-10 lg:py-8">
      <div class="flex flex-col gap-4 border-b border-slate-200 dark:border-slate-800 pb-6 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-xl font-bold text-slate-900 dark:text-slate-100">最近更新</h1>
          <p v-if="period.from" class="mt-1 text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ period.from === period.to ? period.from : `${period.from} 至 ${period.to}` }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <div role="group" aria-label="更新时间范围" class="inline-flex h-10 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-1">
            <button v-for="range in ranges" :key="range.id" type="button" :aria-pressed="selectedRange === range.id" class="rounded-md px-3 text-sm transition" :class="selectedRange === range.id ? 'bg-slate-900 text-white shadow-sm dark:shadow-none' : 'text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 dark:hover:bg-slate-800 dark:bg-slate-800'" @click="selectRange(range.id)">{{ range.label }}</button>
          </div>
          <input type="date" aria-label="选择更新日期" :value="selectedDate" :max="today" class="h-10 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 text-sm text-slate-600 dark:text-slate-300" @change="selectDate" />
        </div>
      </div>

      <div v-if="error && !items.length" role="alert" class="mt-6 rounded-lg border border-red-100 bg-red-50 p-6 text-sm text-red-600"><p>{{ error }}</p><button class="mt-4 rounded-lg border border-red-200 px-4 py-2" @click="load">重新加载</button></div>
      <div v-else-if="loading" aria-label="正在加载最近更新" class="mt-7 grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <div v-for="index in 12" :key="index" class="animate-pulse"><div class="aspect-[2/3] rounded-lg bg-slate-200 dark:bg-slate-700"></div><div class="mt-3 h-4 w-4/5 rounded bg-slate-200 dark:bg-slate-700"></div><div class="mt-2 h-3 w-12 rounded bg-slate-100 dark:bg-slate-800"></div></div>
      </div>
      <div v-else-if="!ready" class="py-24 text-center text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500"><p>{{ timelineStatus === 'building' ? '最近更新时间线正在后台构建。' : timelineStatus === 'failed' ? '最近更新时间线暂时构建失败。' : '最近更新时间线尚未建立。' }}</p><p class="mt-2 text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ timelineStatus === 'building' ? '通常只需片刻，完成后刷新页面即可查看。' : timelineStatus === 'failed' ? '后台将在稍后自动重试。' : '请由管理员在 Emby 媒体库同步页面执行一次全量同步。' }}</p></div>
      <div v-else-if="!items.length" class="py-24 text-center text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">这个时间范围内没有新增影视。</div>
      <div v-else class="space-y-10 pt-7">
        <section v-for="group in groups" :key="group.date" :aria-labelledby="`updates-${group.date}`">
          <div class="mb-5 flex items-baseline gap-3 border-b border-slate-200 dark:border-slate-800 pb-3"><h2 :id="`updates-${group.date}`" class="text-base font-semibold text-slate-800 dark:text-slate-200">{{ dateLabel(group.date) }}</h2><span class="text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ group.date }} · {{ group.items.length }} 项</span></div>
          <div class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><UpdateCard v-for="item in group.items" :key="`${item.date}:${item.kind}:${item.tmdbId}:${item.season ?? -1}`" :item="item" /></div>
        </section>
      </div>
      <div v-if="items.length && hasMore" class="mt-10 flex justify-center"><button type="button" :disabled="loadingMore" class="h-10 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 dark:hover:bg-slate-800 dark:bg-slate-800 disabled:opacity-50" @click="loadMore">{{ loadingMore ? '正在加载…' : '加载更多' }}</button></div>
      <p v-if="error && items.length" role="alert" class="mt-5 text-center text-sm text-red-500">{{ error }}</p>
      <footer class="mt-12 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500"><a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="font-semibold text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">TMDB</a> · 海报及评分数据由 TMDB 提供。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
  </div>
</template>
