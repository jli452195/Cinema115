<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { IconChevronRight, IconRefresh, IconSearch, IconX } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import PlaybackDiagnostics from '../components/PlaybackDiagnostics.vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ openDashboard: []; home: []; openP115: []; openTelegram: []; logout: [] }>()
const levels = ['ALL', 'INFO', 'DEBUG', 'ERROR', 'WARNING'] as const
type Level = typeof levels[number]
const modules = ['ALL', 'STRM', 'Telegram', '订阅', '搜索', 'Emby', 'Emby 302'] as const
type Module = typeof modules[number]
const route = useRoute()
const router = useRouter()
const taskID = computed(() => typeof route.query.taskID === 'string' ? route.query.taskID : '')
function clearTaskFilter() {
  const { taskID: _, ...query } = route.query
  void router.replace({ query })
}
interface LogEntry { id: number; time: string; level: Exclude<Level, 'ALL'>; module: string; taskID: string; source: string; message: string; details?: { url?: string; requestHeaders?: string; responseHeaders?: string; lookupHeaders?: string; lookup?: string; steps?: string; method?: string; path?: string; durationMs?: number; status?: number } }
const entries = ref<LogEntry[]>([])
const level = ref<Level>('INFO')
const module = ref<Module>('ALL')
watch(() => [route.query.module, route.query.level], ([queryModule, queryLevel]) => {
  module.value = modules.includes(queryModule as Module) ? queryModule as Module : 'ALL'
  level.value = levels.includes(queryLevel as Level) ? queryLevel as Level : 'INFO'
}, { immediate: true })
const search = ref('')
const searchInputRef = ref<HTMLInputElement>()
function clearSearch() {
  search.value = ''
  searchInputRef.value?.focus()
}
const loading = ref(false)
const retryButtonRef = ref<HTMLButtonElement>()
const retryStatus = ref('')
const sessionExpired = ref(false)
const error = ref('')
const updated = ref('')
const visibleCount = ref(100)
const capacity = ref(2000)
const lifecycle = new AbortController()
let timer: number | undefined
let mounted = true
const levelNavRef = ref<HTMLElement>()
const { indicatorStyle } = useSlidingIndicator(levelNavRef, '[aria-pressed="true"]', [level])
const bgColors: Record<Level, string> = { ALL: 'bg-slate-800', INFO: 'bg-emerald-500', DEBUG: 'bg-sky-500', ERROR: 'bg-red-500', WARNING: 'bg-amber-400' }
const textColors: Record<Level, string> = { ALL: 'text-white', INFO: 'text-white', DEBUG: 'text-white', ERROR: 'text-white', WARNING: 'text-slate-900' }
const filtered = computed(() => {
  const query = search.value.trim().toLowerCase()
  return entries.value.filter(entry => (!taskID.value || entry.taskID === taskID.value) && (module.value === 'ALL' || entry.module === module.value) && (level.value === 'ALL' || entry.level === level.value) && (!query || `${entry.message} ${entry.module} ${entry.taskID} ${entry.source} ${entry.details ? JSON.stringify(entry.details) : ''}`.toLowerCase().includes(query)))
})
watch([level, module, search, taskID], () => { visibleCount.value = 100 })

function timestamp(value: string) {
  const date = new Date(value)
  return `${date.toLocaleString('zh-CN', { hour12: false })}.${String(date.getMilliseconds()).padStart(3, '0')}`
}

async function refresh() {
  if (loading.value || !mounted || sessionExpired.value) return
  window.clearTimeout(timer)
  const retrying = Boolean(error.value)
  loading.value = true
  if (retrying) retryStatus.value = '正在重试读取日志'
  try {
    const response = await fetch('/api/admin/logs?module=ALL&level=ALL', {
      headers: { Authorization: `Bearer ${props.token}` },
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(10000)]),
    })
    if (!mounted) return
    if (response.status === 401) { sessionExpired.value = true; throw new Error('管理员登录已过期，请重新登录') }
    const data = await response.json().catch(() => null)
    if (!mounted) return
    if (!response.ok || !Array.isArray(data?.entries)) throw new Error(data?.error ?? `日志读取失败（HTTP ${response.status}）`)
    entries.value = data.entries
    capacity.value = data.capacity
    updated.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
    if (retrying && document.activeElement === retryButtonRef.value) searchInputRef.value?.focus()
    error.value = ''
    if (retrying) retryStatus.value = '日志已更新'
  } catch (e) {
    if (mounted) {
      error.value = e instanceof Error ? e.message : '日志读取失败'
      retryStatus.value = ''
    }
  } finally {
    loading.value = false
    // Pause after a failed read; a successful retry resumes the single polling loop.
    if (mounted && !error.value && !sessionExpired.value) timer = window.setTimeout(() => { void refresh() }, 2000)
  }
}

onMounted(() => { void refresh() })
onBeforeUnmount(() => { mounted = false; window.clearTimeout(timer); lifecycle.abort() })
</script>

<template>
  <ManagementLayout title="实时日志" description="查看 STRM、Telegram、订阅、搜索与 Emby 日志；搜索来源显示在每条记录中，排查问题请选择全部等级。" @logout="emit('logout')">
      <section class="logs-panel" aria-label="日志列表与筛选">
        <div class="logs-toolbar">
          <div class="logs-controls">
            <div class="logs-search">
              <IconSearch :size="18" stroke="1.75" aria-hidden="true" focusable="false" />
              <input ref="searchInputRef" v-model="search" type="search" aria-label="搜索日志" placeholder="搜索日志" title="可搜索内容、任务编号、来源或详情" autocomplete="off" :spellcheck="false" />
              <button v-if="search" type="button" class="logs-clear" aria-label="清空搜索" @click="clearSearch"><IconX :size="16" stroke="1.75" aria-hidden="true" focusable="false" /></button>
            </div>
            <select v-model="module" aria-label="日志模块" class="logs-module"><option v-for="item in modules" :key="item" :value="item">{{ item === 'ALL' ? '全部模块' : item }}</option></select>
          </div>
          <div class="logs-filter-row">
            <nav ref="levelNavRef" class="logs-levels relative flex max-w-full overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700 p-1" role="group" aria-label="日志等级">
              <div class="absolute z-0 rounded-md transition-all duration-300 ease-out" :class="bgColors[level]" :style="indicatorStyle"></div>
              <button v-for="item in levels" :key="item" class="relative z-10 shrink-0 rounded-md px-3 py-2 text-xs font-medium transition-colors" :class="level === item ? textColors[item] : 'text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800'" :aria-pressed="level === item" @click="level = item">{{ item }}</button>
            </nav>
            <div class="logs-status">
              <span v-if="updated" class="logs-updated">更新于 {{ updated }}</span>
              <span class="logs-count">匹配 {{ filtered.length }} 条 / 已缓存 {{ entries.length }} 条</span>
            </div>
          </div>
          <div v-if="taskID" class="logs-task-filter">
            <span>仅查看任务：{{ taskID }}</span>
            <button type="button" @click="clearTaskFilter">取消任务筛选</button>
          </div>
          <div v-if="error" class="logs-error">
            <p role="alert">{{ error }}。{{ entries.length ? '当前保留上次读取的日志。' : '' }}</p>
            <RouterLink v-if="sessionExpired" :to="{ path: '/admin/login', query: { redirect: route.fullPath } }" class="logs-retry">重新登录</RouterLink>
            <button v-else ref="retryButtonRef" type="button" class="logs-retry" :aria-busy="loading" :aria-disabled="loading" @click="refresh">
              <IconRefresh :size="16" stroke="1.75" :class="{ 'is-refreshing': loading }" aria-hidden="true" focusable="false" /><span>重试</span>
            </button>
          </div>
          <span class="sr-only" role="status">{{ retryStatus }}</span>
        </div>
        <div v-if="!filtered.length" class="logs-empty">{{ loading && !updated ? '正在读取日志…' : error && !entries.length ? sessionExpired ? '重新登录后即可继续查看日志。' : '暂时无法读取日志，请重试。' : entries.length ? '没有符合筛选条件的日志。' : '暂无日志，执行搜索、STRM、Telegram、订阅或 Emby 操作后会显示在这里。' }}</div>
        <ol v-else class="logs-list">
          <li v-for="entry in filtered.slice(0, visibleCount)" :key="entry.id" class="logs-entry">
            <div class="logs-entry-meta">
              <span class="logs-level-badge" :data-level="entry.level" :class="[bgColors[entry.level], entry.level === 'WARNING' ? 'text-slate-900' : 'text-white']">{{ entry.level }}</span>
              <time :datetime="entry.time">{{ timestamp(entry.time) }}</time>
              <span class="logs-entry-module">{{ entry.module }}</span>
              <span v-if="entry.source" class="logs-entry-source">{{ entry.source }}</span>
              <span v-if="entry.taskID" class="logs-entry-task">{{ entry.taskID }}</span>
            </div>
            <p class="logs-message">{{ entry.message }}</p>
            <details v-if="entry.details && Object.values(entry.details).some(Boolean)" class="logs-details">
              <summary><IconChevronRight :size="14" stroke="1.75" aria-hidden="true" focusable="false" />请求详情与解析步骤</summary>
              <PlaybackDiagnostics v-bind="entry.details" />
            </details>
          </li>
        </ol>
        <div v-if="filtered.length > visibleCount" class="logs-more"><button type="button" @click="visibleCount += 100">加载更多（还剩 {{ filtered.length - visibleCount }} 条）</button></div>
      </section>
      <p class="logs-footnote">全部模块合计保留本次后端运行期间最近 {{ capacity.toLocaleString() }} 条日志，重启后清空。分享任务记录独立保留；此页仅显示新产生的日志。</p>
  </ManagementLayout>
</template>

<style scoped>
.logs-panel {
  --logs-soft: rgb(var(--color-slate-100) / 80%);
  --logs-text: rgb(var(--color-slate-700));
  --logs-muted: rgb(var(--color-slate-500));
  --logs-action: #e8eff9;
  --logs-action-hover: #dae6f5;
  --logs-action-text: #315e99;
  --logs-focus: #739bc9;
  overflow: hidden; min-width: 0; border-radius: 16px; background: #fff;
  box-shadow: 0 1px 4px rgb(15 23 42 / 6%); color: var(--logs-text);
}
.logs-toolbar { display: grid; gap: 14px; padding: 20px 24px; background: rgb(var(--color-slate-50) / 45%); }
.logs-controls { display: grid; grid-template-columns: minmax(0, 1fr) 136px; gap: 12px; align-items: center; }
.logs-search { display: grid; grid-template-columns: 18px minmax(0, 1fr) 32px; align-items: center; gap: 8px; min-width: 0; min-height: 44px; padding-left: 12px; border-radius: 10px; background: var(--logs-soft); color: var(--logs-muted); }
.logs-search input { width: 100%; min-width: 0; height: 44px; padding: 0; border: 0; outline: none; background: transparent; color: var(--logs-text); font-size: 13px; }
.logs-search input::placeholder { color: var(--logs-muted); }
.logs-search input::-webkit-search-cancel-button { -webkit-appearance: none; }
.logs-search:has(input:focus-visible) { outline: 2px solid var(--logs-focus); outline-offset: 2px; }
.logs-clear { display: grid; place-items: center; width: 32px; min-height: 44px; border-radius: 8px; cursor: pointer; }
.logs-module { width: 100%; min-width: 0; min-height: 44px; padding: 0 12px; border: 0; border-radius: 10px; background: var(--logs-soft); color: var(--logs-text); font-size: 13px; cursor: pointer; }
.logs-retry { display: inline-flex; min-width: 88px; min-height: 44px; margin-left: auto; padding: 0 12px; align-items: center; justify-content: center; gap: 7px; flex-shrink: 0; border: 0; border-radius: 10px; background: var(--logs-action); color: var(--logs-action-text); font-size: 13px; font-weight: 500; cursor: pointer; transition: background-color 180ms ease-out, box-shadow 180ms ease-out; }
.logs-retry[aria-disabled="true"] { cursor: wait; }
.logs-retry svg { flex-shrink: 0; }
.logs-retry .is-refreshing { animation: logs-refresh-spin 800ms linear infinite; }
.logs-filter-row { display: flex; min-width: 0; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 16px; }
.logs-levels { flex-shrink: 0; }
.logs-status { display: flex; min-width: 0; flex-wrap: wrap; gap: 4px 14px; margin-left: auto; color: var(--logs-muted); font-size: 12px; line-height: 20px; }
.logs-updated, .logs-count { font-variant-numeric: tabular-nums; }
.logs-count { white-space: nowrap; }
.logs-task-filter { display: flex; min-width: 0; flex-wrap: wrap; align-items: center; gap: 4px 12px; padding: 4px 12px; border-radius: 10px; background: var(--logs-action); color: var(--logs-action-text); font-size: 12px; }
.logs-task-filter > span { min-width: 0; overflow-wrap: anywhere; }
.logs-task-filter button { min-height: 44px; margin-left: auto; padding: 0 8px; flex-shrink: 0; border-radius: 8px; cursor: pointer; }
.logs-error { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; padding: 12px; border-radius: 10px; background: #fcebed; color: #a23549; font-size: 13px; line-height: 22px; overflow-wrap: anywhere; }
.logs-error > p { min-width: 0; flex: 1 1 240px; }
.logs-empty { padding: 64px 24px; color: var(--logs-muted); text-align: center; font-size: 13px; line-height: 24px; }
.logs-list { padding: 4px 0 8px; }
.logs-entry { min-width: 0; padding: 16px 24px; transition: background-color 150ms ease-out; }
.logs-entry:nth-child(even) { background: rgb(var(--color-slate-50) / 60%); }
.logs-entry-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; margin-bottom: 7px; color: var(--logs-muted); font-size: 12px; line-height: 20px; }
.logs-level-badge { min-width: 64px; padding: 2px 8px; border-radius: 7px; text-align: center; font-size: 11px; font-weight: 600; }
.logs-entry-meta time { font-variant-numeric: tabular-nums; }
.logs-entry-module { color: var(--logs-text); font-weight: 500; }
.logs-entry-source, .logs-entry-task { min-width: 0; overflow-wrap: anywhere; }
.logs-message { color: var(--logs-text); font-size: 14px; line-height: 24px; white-space: pre-wrap; overflow-wrap: anywhere; }
.logs-details { min-width: 0; margin-top: 4px; overflow-wrap: anywhere; }
.logs-details summary { display: flex; width: fit-content; max-width: 100%; min-height: 32px; align-items: center; gap: 4px; border-radius: 6px; color: var(--logs-action-text); font-size: 12px; cursor: pointer; list-style: none; }
.logs-details summary::-webkit-details-marker { display: none; }
.logs-details summary > svg { flex-shrink: 0; transition: transform 150ms ease-out; }
.logs-details[open] summary > svg { transform: rotate(90deg); }
.logs-details :deep(button) { min-height: 36px; padding: 0 8px; border-radius: 8px; color: var(--logs-action-text); }
.logs-more { padding: 12px 24px 20px; text-align: center; }
.logs-more button { min-height: 44px; padding: 0 20px; border-radius: 10px; background: var(--logs-soft); color: var(--logs-action-text); font-size: 13px; cursor: pointer; }
.logs-module:focus-visible, .logs-clear:focus-visible, .logs-retry:focus-visible, .logs-task-filter button:focus-visible, .logs-levels button:focus-visible, .logs-details summary:focus-visible, .logs-details :deep(button:focus-visible), .logs-more button:focus-visible { outline: 2px solid var(--logs-focus); outline-offset: -2px; }
.logs-footnote { margin-top: 16px; color: rgb(var(--color-slate-500)); font-size: 12px; line-height: 24px; }
.dark .logs-panel { --logs-soft: rgb(var(--color-slate-950) / 60%); --logs-text: rgb(var(--color-slate-300)); --logs-muted: rgb(var(--color-slate-400)); --logs-action: #29394d; --logs-action-hover: #334961; --logs-action-text: #bad2f4; --logs-focus: #94b8e5; background: rgb(var(--color-slate-900)); box-shadow: 0 1px 4px rgb(0 0 0 / 15%); }
.dark .logs-toolbar { background: rgb(var(--color-slate-950) / 20%); }
.dark .logs-entry:nth-child(even) { background: rgb(var(--color-slate-950) / 22%); }
.dark .logs-error { background: #45272e; color: #ffb4bf; }
.dark .logs-footnote { color: rgb(var(--color-slate-500)); }
@media (hover: hover) and (pointer: fine) {
  .logs-retry:not([aria-disabled="true"]):hover { background: var(--logs-action-hover); box-shadow: 0 2px 6px rgb(15 23 42 / 6%); }
  .logs-clear:hover, .logs-task-filter button:hover, .logs-details :deep(button:hover), .logs-more button:hover { background: var(--logs-action); }
  .logs-entry:hover { background: #f5f8fc; }.dark .logs-entry:hover { background: #272c32; }
}
@keyframes logs-refresh-spin { to { transform: rotate(360deg); } }
@media (max-width: 639px) {
  .logs-toolbar { padding: 16px; }
  .logs-controls { grid-template-columns: minmax(0, 1fr) 112px; gap: 10px; }
  .logs-search { grid-template-columns: 18px minmax(0, 1fr) 44px; }
  .logs-clear { width: 44px; }
  .logs-levels button { min-height: 44px; }
  .logs-status { width: 100%; margin-left: 0; }
  .logs-entry { padding: 14px 16px; }.logs-entry-meta { gap: 6px 10px; }
  .logs-details summary, .logs-details :deep(button) { min-height: 44px; }
}
@media (max-width: 379px) {
  .logs-controls { grid-template-columns: minmax(0, 1fr); }
}
@media (prefers-reduced-motion: reduce) {
  .logs-retry, .logs-entry, .logs-details summary > svg { transition: none; }
  .logs-retry .is-refreshing { animation: none; }
}
</style>
