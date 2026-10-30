<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import P115Icon from './P115Icon.vue'
import { historyDuration, historyTime, isActiveTask, itemStatus, needsAttention, resultSummary, taskCounts, taskStatus, taskTone, type HistoryTask } from '../utils/organizeHistory'

const props = defineProps<{ task: HistoryTask; accountName: string; blockedReason: string; historyError: string; unavailable: boolean; resuming: boolean; resumeError: string }>()
defineEmits<{ close: []; resume: [] }>()
// This component is keyed by task identity, not the polling snapshot.
const filter = ref(taskCounts(props.task).attention ? 'attention' : 'all')
const feedback = ref('')
const resumeButton = ref<HTMLButtonElement>(), closeButton = ref<HTMLButtonElement>(), resumeAlert = ref<HTMLElement>()
const counts = computed(() => taskCounts(props.task))
const items = computed(() => props.task.items.filter(item => filter.value !== 'attention' || needsAttention(item, props.task)))
const stats = computed(() => props.task.organize?.stats)
const resumable = computed(() => !!props.task.organize?.resumable)
const folders = computed(() => [{ label: '来源目录', path: props.task.source }, { label: '目标目录', path: props.task.target }].filter(row => row.path))
watch(() => props.resumeError, async message => {
  if (message) { await nextTick(); resumeAlert.value?.scrollIntoView({ block: 'nearest' }) }
})
watch(() => props.resuming, async (current, previous) => {
  if (current || !previous) return
  const restore = document.activeElement === resumeButton.value || document.activeElement === document.body
  await nextTick()
  if (restore) (resumable.value ? resumeButton.value : closeButton.value)?.focus({ preventScroll: true })
})
async function copy(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
      feedback.value = '已复制完整路径。'
      return
    }
  } catch { /* LAN HTTP and browser permissions may require the selection fallback. */ }
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const field = document.createElement('textarea')
  field.value = value
  field.readOnly = true
  field.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;pointer-events:none'
  // A modal dialog makes elements outside it inert, so keep the temporary selection inside it.
  const container = focused?.closest('dialog') || document.body
  container.append(field)
  try {
    field.select()
    feedback.value = document.execCommand('copy') ? '已复制完整路径。' : '复制失败，请选择路径文本后复制。'
  } catch { feedback.value = '复制失败，请选择路径文本后复制。' }
  finally { field.remove(); focused?.focus({ preventScroll: true }) }
}
</script>

<template>
  <AnimeFloatingDialog title="整理历史详情" close-label="关闭详情弹窗" size="organize" class="organize-task-dialog" @close="$emit('close')">
    <div class="task-detail-sections">
      <p v-if="resumeError" ref="resumeAlert" role="alert" class="rounded-xl bg-amber-50 p-3 text-xs leading-6 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">{{ resumeError }}</p>
      <p v-if="historyError || unavailable" role="alert" class="rounded-xl bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">{{ unavailable ? '此任务已不在最近记录中，当前显示打开时保留的快照。' : '记录刷新失败，当前结果可能已过期。请刷新记录后再执行操作。' }}</p>
      <section aria-label="任务摘要" class="detail-panel space-y-3 text-sm">
        <h3 class="break-words text-xl font-semibold leading-7 text-slate-900 dark:text-slate-100 [overflow-wrap:anywhere]">{{ task.title }}</h3>
        <div class="flex flex-wrap items-center gap-3"><span class="badge" :class="`badge-${taskTone(task)}`"><P115Icon v-if="taskTone(task) === 'success'" name="check" class="success-mark" />{{ taskStatus(task) }}</span><span class="text-xs text-slate-500 dark:text-slate-400">{{ resultSummary(task) }}</span></div>
        <p class="text-xs text-slate-500 dark:text-slate-400">共 {{ counts.total }} 个文件，含 {{ task.items.filter(item => item.mediaType === 'video').length }} 个视频、{{ task.items.filter(item => item.mediaType === 'subtitle').length }} 个字幕</p>
        <p v-if="task.items.some(item => item.titleOnly)" class="text-xs text-slate-500 dark:text-slate-400">单集按标题预设命名，实际文件、扩展名与关联字幕下载后确认。</p>
        <dl class="task-detail-meta"><div><dt>账号</dt><dd>{{ accountName }}</dd></div><div><dt>创建时间</dt><dd>{{ historyTime(task.createdAt, true) }}</dd></div></dl>
      </section>

      <div v-if="counts.attention || (!isActiveTask(task) && task.status !== 'completed')" class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs leading-6 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
        <p class="flex items-center gap-2 font-medium"><P115Icon name="warning" />{{ counts.uncertain ? '有文件结果待核实' : '任务有未完成的条目' }}</p>
        <p v-if="task.message" class="mt-1 break-all">{{ task.message }}</p>
        <p>{{ resumable ? '点击“检查并继续”核对剩余文件后继续原任务，保留已完成进度。' : '请核对原位置与目标位置的实际文件，再重新预览。待核实条目不会自动重复执行。' }}</p>
      </div>
      <p v-else-if="task.message" class="break-all text-xs leading-6 text-slate-600 dark:text-slate-300">{{ task.message }}</p>

      <RouterLink v-if="task.organize?.download?.share?.handedOff" to="/115/share/logs" class="text-xs text-blue-600 underline dark:text-blue-400">查看分享 STRM 日志与重试</RouterLink>
      <section v-if="folders.length" aria-label="路径信息" class="detail-panel space-y-3">
        <h3 class="text-sm font-semibold">路径信息</h3>
        <div v-for="folder in folders" :key="folder.label"><div class="mb-1 flex items-center justify-between gap-3 text-xs"><span class="text-slate-500 dark:text-slate-400">{{ folder.label }}</span><button class="path-copy" :aria-label="`复制${folder.label}`" @click="copy(folder.path!)">复制路径</button></div><p class="select-text break-all text-xs leading-5">{{ folder.path }}</p></div>
      </section>

      <details v-if="stats?.batchSize" class="detail-panel text-xs leading-6">
        <summary class="flex cursor-pointer list-none items-center gap-2 font-medium text-slate-700 dark:text-slate-200 [&::-webkit-details-marker]:hidden"><P115Icon name="chart" />耗时与请求详情<span class="ml-auto font-normal text-slate-500 dark:text-slate-400">累计 {{ historyDuration(stats.elapsedMs) }}</span></summary>
        <div class="mt-3 space-y-1 text-slate-500 dark:text-slate-400">
          <p>已完成 {{ stats.batchesCompleted }}/{{ stats.batchesTotal }} 批，每批最多 {{ stats.batchSize }} 个文件</p>
          <p>115 请求 {{ stats.httpRequests }} 次，接口响应 {{ historyDuration(stats.httpMs) }}，排队 / 限速等待 {{ historyDuration(stats.waitMs) }}</p>
          <p>目录读取 {{ stats.directoryRequests }} 次，批量改名 {{ stats.renameRequests }} 次，批量移动 {{ stats.moveRequests }} 次，新建目录 {{ stats.directoryCreates }} 次</p>
          <p>目录读取累计 {{ historyDuration(stats.directoryMs) }}，写操作累计 {{ historyDuration(stats.mutationMs) }}（均包含等待）</p>
          <p>记录保存 {{ stats.checkpoints }} 次，{{ historyDuration(stats.saveMs) }}；统计在执行阶段结束后更新。</p>
        </div>
      </details>

      <section aria-label="文件明细" class="detail-panel">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold">文件明细</h3><div class="flex gap-1" role="group" aria-label="明细筛选"><button class="filter" :aria-pressed="filter === 'all'" @click="filter = 'all'">全部 {{ counts.total }}</button><button class="filter" :aria-pressed="filter === 'attention'" @click="filter = 'attention'">需关注 {{ counts.attention }}</button></div></div>
        <p v-if="!items.length" class="rounded-lg bg-slate-50 px-3 py-6 text-center text-xs text-slate-500 dark:bg-slate-950 dark:text-slate-400">{{ filter === 'attention' ? '没有需要关注的条目，可切换查看全部。' : '此任务没有文件明细。' }}</p>
        <ul class="divide-y divide-slate-200 dark:divide-slate-700">
          <li v-for="item in items" :key="item.id" class="py-4 first:pt-0">
            <div class="mb-2 flex items-start justify-between gap-3"><p class="min-w-0 break-all text-sm font-medium">{{ item.name }}</p><span class="shrink-0 text-xs" :class="needsAttention(item, task) ? 'text-amber-700 dark:text-amber-300' : item.status === 'done' ? 'text-emerald-700 dark:text-emerald-300' : 'text-slate-500 dark:text-slate-400'">{{ itemStatus(item, task) }}</span></div>
            <div v-for="path in [{ label: '原路径', value: item.source }, { label: item.titleOnly ? '预计目标' : '目标路径', value: item.target }].filter(row => row.value)" :key="path.label" class="task-detail-path mt-2 rounded-lg p-2.5"><div class="mb-1 flex justify-between gap-3 text-xs text-slate-500 dark:text-slate-400"><span>{{ path.label }}</span><button class="path-copy" :aria-label="`复制${item.name}的${path.label}`" @click="copy(path.value!)">复制</button></div><p class="select-text break-all text-xs leading-5">{{ path.value }}</p></div>
            <p v-if="item.message" class="mt-2 break-all text-xs leading-5" :class="needsAttention(item, task) ? 'text-amber-700 dark:text-amber-300' : 'text-slate-500 dark:text-slate-400'">{{ item.message }}</p>
          </li>
        </ul>
      </section>
    </div>
    <template #footer><footer class="task-detail-footer"><p role="status" class="min-w-0 flex-1 text-xs text-slate-500 dark:text-slate-400">{{ resuming ? '正在恢复原任务…' : feedback || (resumable ? (task.organize?.download ? '不会重新提交离线' : '保留原任务与已完成进度') : '记录刷新不会关闭详情或改变当前筛选。') }}</p><button v-if="resumable || resuming" ref="resumeButton" class="resume" :disabled="resuming || !!blockedReason" :title="blockedReason" :aria-busy="resuming" @click="$emit('resume')"><P115Icon v-if="resuming" name="refresh" class="motion-safe:animate-spin" />{{ resuming ? '检查中…' : '检查并继续' }}</button><button ref="closeButton" class="action" @click="$emit('close')">关闭详情</button></footer></template>
  </AnimeFloatingDialog>
</template>

<style scoped>
:global(.organize-task-dialog) { max-width: none; }
:global(.organize-task-dialog .anime-floating-body) { padding-top: 10px; overflow-anchor: none; }
.task-detail-sections { display: grid; gap: 14px; }
.detail-panel { min-width: 0; padding: 16px 18px; border-radius: 12px; background: rgb(255 255 255 / .8); }
.dark .detail-panel { background: rgb(255 255 255 / .045); }
.task-detail-meta { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px 24px; font-size: 12px; line-height: 20px; }
.task-detail-meta > div { display: flex; gap: 12px; }
.task-detail-meta dt { flex: none; color: var(--floating-surface-muted); }
.task-detail-meta dd { min-width: 0; overflow-wrap: anywhere; }
.task-detail-path { background: rgb(148 163 184 / .08); }
.path-copy { display: inline-flex; min-height: 32px; align-items: center; margin-block: -6px; padding-inline: 4px; color: #1d4ed8; }
.path-copy:hover { text-decoration: underline; }
.dark .path-copy { color: #93c5fd; }
.task-detail-footer { display: flex; align-items: center; gap: 12px; }
@media (max-width: 639px) {
  .detail-panel { padding: 14px; }
  .task-detail-meta { grid-template-columns: minmax(0, 1fr); }
  .task-detail-footer { flex-wrap: wrap; gap: 10px; justify-content: flex-end; }
  .task-detail-footer > p { flex-basis: 100%; line-height: 18px; }
  .task-detail-footer .action, .task-detail-footer .resume { min-height: 44px; }
  .filter { min-height: 44px; }
}
@media (max-width: 639px), (pointer: coarse) { .path-copy { min-height: 44px; margin-block: 0; } }
.action { @apply min-h-9 shrink-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800; }
.resume { @apply inline-flex min-h-9 shrink-0 items-center justify-center gap-2 rounded-lg bg-blue-600 px-3 py-2 text-xs font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40 dark:bg-blue-500 dark:hover:bg-blue-600; }
.filter { @apply rounded-lg px-3 py-2 text-xs text-slate-500 hover:bg-slate-50 dark:text-slate-400 dark:hover:bg-slate-800; }
.filter[aria-pressed="true"] { @apply bg-blue-50 font-medium text-blue-700 dark:bg-blue-950/50 dark:text-blue-300; }
.badge { @apply inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium; }
.badge-active { @apply bg-blue-50 text-blue-700 dark:bg-blue-950/50 dark:text-blue-300; }
.badge-success { @apply px-0 text-slate-600 dark:text-slate-300; }
.success-mark { width: 14px; height: 14px; color: #15803d; }
.dark .success-mark { color: #4ade80; }
.badge-warning { @apply bg-amber-50 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300; }
button:focus-visible, summary:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
