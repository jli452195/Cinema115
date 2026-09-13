<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import WorkbenchSidebar from '../components/WorkbenchSidebar.vue'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ openDashboard: []; home: []; openP115: []; openTelegram: []; logout: [] }>()
const levels = ['ALL', 'INFO', 'DEBUG', 'ERROR', 'WARNING'] as const
type Level = typeof levels[number]
interface LogEntry { id: number; time: string; level: Exclude<Level, 'ALL'>; module: string; taskID: string; source: string; message: string }
const entries = ref<LogEntry[]>([])
const level = ref<Level>('INFO')
const search = ref('')
const automatic = ref(true)
const loading = ref(false)
const error = ref('')
const updated = ref('')
const visibleCount = ref(100)
const capacity = ref(2000)
const lifecycle = new AbortController()
let timer: number | undefined
let mounted = true
const colors: Record<Level, string> = { ALL: 'bg-slate-800 text-white', INFO: 'bg-emerald-500 text-white', DEBUG: 'bg-sky-500 text-white', ERROR: 'bg-red-500 text-white', WARNING: 'bg-amber-400 text-slate-900' }
const filtered = computed(() => {
  const query = search.value.trim().toLowerCase()
  return entries.value.filter(entry => (level.value === 'ALL' || entry.level === level.value) && (!query || `${entry.message} ${entry.taskID} ${entry.source}`.toLowerCase().includes(query)))
})
watch([level, search], () => { visibleCount.value = 100 })

function timestamp(value: string) {
  const date = new Date(value)
  return `${date.toLocaleString('zh-CN', { hour12: false })}.${String(date.getMilliseconds()).padStart(3, '0')}`
}

async function refresh() {
  if (loading.value || !mounted) return
  window.clearTimeout(timer)
  loading.value = true
  try {
    const response = await fetch('/api/admin/logs?module=STRM&level=ALL', {
      headers: { Authorization: `Bearer ${props.token}` },
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(10000)]),
    })
    if (response.status === 401) { automatic.value = false; throw new Error('管理员登录已过期，请退出后重新登录') }
    const data = await response.json().catch(() => null)
    if (!response.ok || !Array.isArray(data?.entries)) throw new Error(data?.error ?? `日志读取失败（HTTP ${response.status}）`)
    entries.value = data.entries
    capacity.value = data.capacity
    updated.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
    error.value = ''
  } catch (e) {
    if (mounted) error.value = e instanceof Error ? e.message : '日志读取失败'
  } finally {
    loading.value = false
    if (mounted && automatic.value) timer = window.setTimeout(refresh, 2000)
  }
}

function toggleAutomatic() {
  automatic.value = !automatic.value
  if (automatic.value) void refresh()
  else window.clearTimeout(timer)
}
onMounted(refresh)
onBeforeUnmount(() => { mounted = false; window.clearTimeout(timer); lifecycle.abort() })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-100 md:flex-row">
    <WorkbenchSidebar @open-dashboard="emit('openDashboard')" active="logs" @home="emit('home')" @open-p115="emit('openP115')" @open-telegram="emit('openTelegram')" @logout="emit('logout')" />
    <main class="min-w-0 flex-1 p-4 md:p-10">
      <header class="mb-7"><p class="text-sm text-slate-500">控制台 / 查看日志</p><h1 class="mt-2 text-3xl font-semibold text-slate-900">实时日志</h1><p class="mt-3 text-sm leading-6 text-slate-500">INFO 显示分享链接、消息批次、输出目录、生成进度、成功与失败数量及耗时，最新记录显示在顶部。逐文件路径和详细处理阶段请切换 DEBUG 查看。</p></header>
      <section class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <div class="space-y-4 border-b border-slate-100 p-4 md:p-6">
          <div class="flex flex-wrap items-center gap-3">
            <div class="flex max-w-full overflow-x-auto rounded-lg border border-slate-200 p-1" role="group" aria-label="日志等级">
              <button v-for="item in levels" :key="item" class="shrink-0 rounded-md px-3 py-2 text-xs font-medium transition" :class="level === item ? colors[item] : 'text-slate-500 hover:bg-slate-100'" :aria-pressed="level === item" @click="level = item">{{ item }}</button>
            </div>
            <select aria-label="日志模块" class="rounded-lg border border-slate-200 bg-slate-50 px-4 py-2 text-sm text-slate-700"><option>STRM</option></select>
            <div class="flex gap-2 md:ml-auto">
              <button class="rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 hover:bg-slate-50" :aria-pressed="automatic" @click="toggleAutomatic">{{ automatic ? '暂停更新' : '继续更新' }}</button>
              <button class="rounded-lg bg-blue-600 px-3 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-50" :disabled="loading" @click="refresh">{{ loading ? '读取中…' : '刷新' }}</button>
            </div>
          </div>
          <input v-model="search" aria-label="搜索日志" placeholder="搜索日志内容、任务编号或来源" class="w-full rounded-lg border border-slate-200 bg-slate-50 px-3 py-2.5 text-sm outline-none focus:border-blue-500" />
          <div class="flex flex-wrap items-center justify-between gap-2 text-xs leading-5 text-slate-400"><span>{{ automatic ? '每 2 秒自动更新' : '已暂停自动更新' }}<span v-if="updated"> · 更新于 {{ updated }}</span></span><span>匹配 {{ filtered.length }} 条 / 已缓存 {{ entries.length }} 条</span></div>
          <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}。{{ entries.length ? '当前保留上次读取的日志。' : '' }}</p>
        </div>
        <div v-if="!filtered.length" class="px-6 py-20 text-center text-sm text-slate-500">{{ loading && !updated ? '正在读取日志…' : error && !entries.length ? '暂时无法读取日志，请稍后刷新。' : entries.length ? '没有符合筛选条件的日志。' : '暂无 STRM 日志，发起生成任务后会显示在这里。' }}</div>
        <ol v-else class="divide-y divide-slate-100">
          <li v-for="entry in filtered.slice(0, visibleCount)" :key="entry.id" class="px-4 py-4 hover:bg-slate-50/70 md:px-6">
            <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-2 text-xs">
              <span class="min-w-16 rounded-full px-2.5 py-1 text-center font-medium" :class="colors[entry.level]">{{ entry.level }}</span>
              <time :datetime="entry.time" class="tabular-nums text-slate-500">{{ timestamp(entry.time) }}</time>
              <span class="rounded-full bg-slate-100 px-2.5 py-1 text-slate-600">{{ entry.module }}</span>
              <span class="text-slate-400">{{ entry.source }} · {{ entry.taskID }}</span>
            </div>
            <p class="whitespace-pre-wrap break-words text-sm leading-6 text-slate-700 [overflow-wrap:anywhere]">{{ entry.message }}</p>
          </li>
        </ol>
        <div v-if="filtered.length > visibleCount" class="border-t border-slate-100 p-4 text-center"><button class="text-sm text-blue-600" @click="visibleCount += 100">加载更多（还剩 {{ filtered.length - visibleCount }} 条）</button></div>
      </section>
      <p class="mt-4 text-xs leading-6 text-slate-400">保留本次后端运行期间最近 {{ capacity.toLocaleString() }} 条日志，重启后清空。转存任务记录独立保留；此页仅显示新产生的日志。</p>
    </main>
  </div>
</template>
