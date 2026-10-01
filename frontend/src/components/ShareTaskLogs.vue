<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ token: string }>()
interface Task { id: string; source: string; sourceUrl: string; shareUrl: string; mode: string; status: string; written: number; total: number; error: string; note: string; retries: number; manualRetries: number; canRetry: boolean; retryReason?: string; maxRetries: number; nextRetryAt: string | null; createdAt: string; updatedAt: string; elapsed: number }
const statuses: Record<string, string> = { success: '成功', failed: '失败', queued: '等待生成', running: '运行中', retry_queued: '等待手动重试', review: '审核中' }
// 展示状态合并，内部状态仍用于判断任务能否清理及是否继续重试。
function statusGroup(value: string) {
  if (value === 'interrupted' || value === 'cancelled') return 'failed'
  return value === 'waiting_review' ? 'review' : value
}
const entries = ref<Task[]>([])
const status = ref('')
const mode = ref('')
const search = ref('')
const loading = ref(false)
const error = ref('')
const notice = ref('')
const selected = ref<string[]>([])
const selecting = ref(false)
const confirmClear = ref<'all' | 'selected' | ''>('')
const clearing = ref(false)
const retrying = ref(false)
const selectionMode = ref<'retry' | 'clear'>('retry')
const limit = ref(50)
const offset = ref(0)
const hasMore = ref(false)
let timer: number | undefined
let debounce: number | undefined
let controller: AbortController | undefined
let mounted = true
const clearable = (task: Task) => !['queued', 'running', 'waiting_review', 'retry_queued'].includes(task.status)
const selectable = computed(() => entries.value.filter(task => selectionMode.value === 'retry' ? task.canRetry : clearable(task)).map(task => task.id))
const allSelected = computed(() => selectable.value.length > 0 && selectable.value.every(id => selected.value.includes(id)))
function toggleAll() { selected.value = allSelected.value ? [] : [...selectable.value] }
function date(value: string) { return new Date(value).toLocaleString('zh-CN', { hour12: false }) }
function color(value: string) { const group = statusGroup(value); return group === 'success' ? 'bg-emerald-100 text-emerald-700' : ['queued', 'running', 'retry_queued'].includes(group) ? 'bg-blue-100 text-blue-700' : group === 'review' ? 'bg-amber-100 text-amber-700' : 'bg-red-100 text-red-600' }
async function refresh() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const query = new URLSearchParams({ status: status.value, mode: mode.value, search: search.value.trim(), limit: String(limit.value), offset: String(offset.value) })
    const response = await fetch(`/api/admin/p115/share/tasks?${query}`, { signal: current.signal, headers: { Authorization: `Bearer ${props.token}` } })
    const data = await response.json()
    if (!response.ok || !Array.isArray(data.entries)) throw new Error(data.error ?? '读取任务日志失败')
    if (!mounted || current.signal.aborted) return
    entries.value = data.entries
    hasMore.value = data.entries.length === limit.value
    selected.value = selected.value.filter(id => selectable.value.includes(id))
    error.value = ''
  } catch (e) { if (mounted && !current.signal.aborted) error.value = e instanceof Error ? e.message : '读取任务日志失败' }
  finally { if (mounted && controller === current) loading.value = false }
}
async function clear() {
  if (clearing.value || !confirmClear.value) return
  clearing.value = true
  const ids = confirmClear.value === 'selected' ? entries.value.filter(task => selected.value.includes(task.id) && clearable(task)).map(task => task.id) : []
  if (confirmClear.value === 'selected' && !ids.length) { clearing.value = false; confirmClear.value = ''; return }
  try {
    const response = await fetch('/api/admin/p115/share/tasks/clear', { method: 'POST', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }) })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error ?? '清理失败')
    if (!mounted) return
    notice.value = `已清理 ${data.deleted} 条记录。执行中、等待重试及未完成目录批次关联的任务继续保留。`
    confirmClear.value = ''; selected.value = []; offset.value = 0
    await refresh()
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : '清理失败' }
  finally { if (mounted) clearing.value = false }
}
async function retry(ids: string[]) {
  if (retrying.value || !ids.length) return
  retrying.value = true
  try {
    const response = await fetch('/api/admin/p115/share/tasks/retry', { method: 'POST', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }) })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error ?? '提交重试失败')
    if (!mounted) return
    const results = data.results as { id: string; accepted: boolean; reason?: string }[]
    const accepted = results.filter(item => item.accepted).length
    notice.value = `已加入重试队列 ${accepted} 条。${results.filter(item => !item.accepted).map(item => item.reason).join('；')}`
    selected.value = []
    await refresh()
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : '提交重试失败' }
  finally { if (mounted) retrying.value = false }
}
watch(selectionMode, () => { selected.value = []; confirmClear.value = '' })
watch([status, mode, search], () => { window.clearTimeout(debounce); offset.value = 0; debounce = window.setTimeout(refresh, 250) })
onMounted(() => { void refresh(); timer = window.setInterval(() => { if (!loading.value && !clearing.value && !retrying.value) void refresh() }, 3000) })
onBeforeUnmount(() => { mounted = false; controller?.abort(); window.clearInterval(timer); window.clearTimeout(debounce) })
</script>

<template>
  <section aria-label="分享任务日志" class="space-y-4">
    <div class="flex flex-wrap items-center gap-3 rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-4">
      <select v-model="status" aria-label="状态" class="min-w-36 flex-1 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-3 py-2.5 text-sm text-slate-700 dark:text-slate-300"><option value="">全部状态</option><option v-for="(label, value) in statuses" :key="value" :value="value">{{ label }}</option></select>
      <select v-model="mode" aria-label="模式" class="min-w-36 flex-1 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-3 py-2.5 text-sm text-slate-700 dark:text-slate-300"><option value="">全部模式</option><option value="direct">普通分享</option><option value="reshare">转存再分享</option></select>
      <input v-model="search" aria-label="搜索链接" placeholder="搜索链接" maxlength="1000" class="min-w-40 flex-1 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-3 py-2.5 text-sm outline-none focus:border-blue-500"/>
      <button type="button" :disabled="clearing" class="rounded-lg border border-red-200 px-3 py-2 text-xs text-red-500 hover:bg-red-50 disabled:opacity-50" @click="confirmClear = 'all'">清空全部</button>
      <button type="button" :aria-pressed="selecting" class="rounded-lg border border-slate-200 dark:border-slate-700 px-3 py-2 text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500" @click="selecting = !selecting">{{ selecting ? '取消选择' : '批量选择' }}</button>
    </div>
    <div v-if="selecting" class="flex flex-wrap items-center gap-4 px-2 text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500">
      <select v-model="selectionMode" aria-label="批量操作" class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2"><option value="retry">选择可重试任务</option><option value="clear">选择可清理任务</option></select>
      <label class="flex items-center gap-2"><input type="checkbox" :checked="allSelected" @change="toggleAll"/>全选当前页</label>
      <button v-if="selectionMode === 'retry'" :disabled="!selected.length || retrying" class="text-blue-600 disabled:opacity-40" @click="retry([...selected])">重试所选（{{ selected.length }}）</button>
      <button v-else :disabled="!selected.length" class="text-red-500 disabled:opacity-40" @click="confirmClear = 'selected'">清理所选（{{ selected.length }}）</button>
    </div>
    <div v-if="confirmClear" class="flex flex-wrap items-center gap-3 rounded-lg bg-red-50 p-4 text-sm text-red-700"><span class="flex-1">{{ confirmClear === 'all' ? '清空所有已结束的任务日志？' : `清理选中的 ${selected.length} 条日志？` }}STRM 文件、网盘文件和转存检查点会保留。</span><button :disabled="clearing" @click="confirmClear = ''">取消</button><button :disabled="clearing" class="rounded-md bg-red-600 px-3 py-2 text-white" @click="clear">{{ clearing ? '清理中…' : '确认清理' }}</button></div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-600">{{ error }}</p>
    <p v-if="notice" role="status" class="px-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ notice }}</p>
    <div v-if="!entries.length" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-6 py-16 text-center text-sm text-slate-400 dark:text-slate-500">{{ loading ? '正在读取日志…' : '暂无匹配的任务。生成 STRM 后将在这里显示结果。' }}</div>
    <article v-for="task in entries" :key="task.id" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5">
      <div class="flex items-start gap-3"><input v-if="selecting" v-model="selected" type="checkbox" :value="task.id" :disabled="!selectable.includes(task.id)" :aria-label="'选择任务 ' + task.id" class="mt-1 accent-blue-600"/><div class="min-w-0 flex-1">
        <a v-if="task.sourceUrl" :href="task.sourceUrl" target="_blank" rel="noopener noreferrer" class="break-all text-sm text-slate-700 dark:text-slate-300 hover:text-blue-600">{{ task.sourceUrl }}</a><p v-else class="text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">无效分享链接</p>
        <a v-if="task.shareUrl" :href="task.shareUrl" target="_blank" rel="noopener noreferrer" class="mt-2 block break-all text-xs text-violet-600">新：{{ task.shareUrl }}</a>
        <div class="mt-3 flex flex-wrap items-center gap-2 text-xs"><span class="rounded-full px-2 py-1" :class="color(task.status)">{{ statuses[statusGroup(task.status)] ?? task.status }}</span><span class="rounded-full px-2 py-1" :class="task.mode === 'reshare' ? 'bg-amber-50 text-amber-600' : 'bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400 dark:text-slate-500'">{{ task.mode === 'reshare' ? '转存再分享' : '普通分享' }}</span><span class="ml-1 text-slate-500 dark:text-slate-400 dark:text-slate-500">STRM：{{ task.written }}/{{ task.total }}</span><span class="text-slate-400 dark:text-slate-500">{{ task.source }} · {{ task.elapsed.toFixed(2) }} 秒</span></div>
        <p class="mt-3 text-xs text-slate-400 dark:text-slate-500">{{ date(task.createdAt) }}<span v-if="task.retries"> · 自动重试 {{ task.retries }}/{{ task.maxRetries }} 次</span><span v-if="task.manualRetries"> · 手动重试 {{ task.manualRetries }} 次</span></p>
        <button v-if="task.canRetry" type="button" :disabled="retrying" class="mt-3 rounded-lg border border-blue-200 px-3 py-2 text-xs text-blue-600 hover:bg-blue-50 disabled:opacity-40" @click="retry([task.id])">{{ retrying ? '提交中…' : '立即重试' }}</button>
        <p v-else-if="task.retryReason && ['failed', 'review', 'waiting_review', 'cancelled', 'interrupted'].includes(task.status)" class="mt-3 text-xs text-slate-400 dark:text-slate-500">不可重试：{{ task.retryReason }}</p>
        <p v-if="task.nextRetryAt" class="mt-3 text-xs text-blue-600">下次审核重试：{{ date(task.nextRetryAt) }} · {{ task.retries }}/{{ task.maxRetries }}</p>
        <p v-if="task.error" class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-red-500">{{ task.error }}</p>
        <p v-if="task.note" class="mt-2 text-xs leading-6 text-amber-700">{{ task.note }}</p>
      </div></div>
    </article>
    <div v-if="hasMore || offset > 0" class="flex items-center justify-between px-2 text-sm text-blue-600"><button :disabled="offset === 0 || loading" class="disabled:opacity-40" @click="offset = Math.max(0, offset - limit); refresh()">上一页</button><span class="text-xs text-slate-400 dark:text-slate-500">第 {{ Math.floor(offset / limit) + 1 }} 页</span><button :disabled="!hasMore || loading" class="disabled:opacity-40" @click="offset += limit; refresh()">下一页</button></div>
    <p class="px-2 text-xs leading-6 text-slate-400 dark:text-slate-500">每 3 秒更新，按最近更新排序。日志保存在服务器，重启后保留；清理日志不会中止正在执行或等待重试的任务。</p>
    <p class="px-2 text-xs leading-6 text-slate-400 dark:text-slate-500">手动重试不受下次审核时间、自动重试开关和次数限制，也不消耗自动次数。提交后进入后台重试队列，按序执行；仍在审核中的分享不保证立即成功。</p>
  </section>
</template>
