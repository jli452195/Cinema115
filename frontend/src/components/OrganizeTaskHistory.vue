<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import P115Icon from './P115Icon.vue'
import OrganizeTaskDetails from './OrganizeTaskDetails.vue'
import type { OrganizeCleanup, OrganizeJob } from '../types/manualOrganize'
import type { SubtitleImport } from '../types/subtitleImport'
import type { P115Account } from '../types/p115Accounts'
import { activePhase, historyLabels, historyTasks, historyTime, isActiveTask, resultSummary, taskCounts, taskStatus, taskTone, type HistoryKind, type HistoryTask } from '../utils/organizeHistory'

const props = defineProps<{
  jobs: OrganizeJob[]; cleanupJobs: OrganizeCleanup[]; subtitleJobs: SubtitleImport[]; accounts: P115Account[]
  tab: HistoryKind; ready: boolean; error: string; refreshing: boolean; refreshedAt: string; busy: boolean
  resuming: string; resumeError?: { id: string; message: string }
}>()
const emit = defineEmits<{ 'update:tab': [value: HistoryKind]; refresh: []; source: [job: OrganizeJob]; subtitle: [job: OrganizeJob]; resume: [job: OrganizeJob] }>()
const limits = ref<Record<HistoryKind, number>>({ organize: 8, cleanup: 8, subtitle: 8 })
const selected = ref<{ key: string; snapshot: HistoryTask }>()
const completedElsewhere = ref<HistoryTask>()
const tasks = computed(() => historyTasks(props.jobs, props.cleanupJobs, props.subtitleJobs))
const activeTasks = computed(() => tasks.value.filter(isActiveTask))
const history = computed(() => tasks.value.filter(task => task.kind === props.tab && !isActiveTask(task)))
const visible = computed(() => history.value.slice(0, limits.value[props.tab]))
const selectedTask = computed(() => tasks.value.find(task => task.key === selected.value?.key) || selected.value?.snapshot)
const activeOtherKinds = computed(() => [...new Set(activeTasks.value.filter(task => task.kind !== props.tab).map(task => historyLabels[task.kind]))])
watch(tasks, (current, previous) => {
  const ended = current.find(task => !isActiveTask(task) && task.kind !== props.tab && previous.some(old => old.key === task.key && isActiveTask(old)))
  if (ended) completedElsewhere.value = ended
})
watch(() => props.tab, tab => { if (tab === completedElsewhere.value?.kind) completedElsewhere.value = undefined })
const unavailable = computed(() => !!selected.value && !tasks.value.some(task => task.key === selected.value?.key))
const accountName = computed(() => {
  const account = props.accounts.find(account => account.id === selectedTask.value?.accountId)
  return account?.primary ? '主账号' : account?.name || selectedTask.value?.accountId || ''
})
const blockedReason = computed(() => {
  if (unavailable.value) return '此任务已不在最近记录中，请重新选择有效记录。'
  if (!props.ready || props.error) return '请刷新任务记录，确认最新状态后再执行操作。'
  if (props.busy || activeTasks.value.some(task => task.status !== 'downloading')) return '请等待当前操作完成。'
  if (!props.accounts.some(account => account.id === selectedTask.value?.accountId && account.configured)) return '此任务的账号已删除或未登录，请先配置凭据。'
  return ''
})
const canSubtitle = computed(() => !!selectedTask.value?.organize?.items.some(item => item.status === 'done' && item.file.mediaType === 'video'))
function openDetails(task: HistoryTask) { selected.value = { key: task.key, snapshot: task } }
function resume() {
  const job = selectedTask.value?.organize
  if (job?.resumable && !blockedReason.value && !props.resuming) emit('resume', job)
}
async function act(action: 'source' | 'subtitle') {
  const job = selectedTask.value?.organize
  if (!job || blockedReason.value || isActiveTask(selectedTask.value!) || (action === 'subtitle' && !canSubtitle.value)) return
  selected.value = undefined
  // Let the drawer restore focus and release its scroll lock before opening another dialog.
  await nextTick()
  if (action === 'source') emit('source', job)
  else emit('subtitle', job)
}
</script>

<template>
  <section class="min-w-0 rounded-2xl border border-slate-200 bg-white p-5 text-slate-700 shadow-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200" aria-label="任务记录">
    <div class="flex items-center justify-between gap-3"><h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">任务记录</h2><button class="action inline-flex items-center gap-2" :disabled="refreshing" @click="$emit('refresh')"><P115Icon name="refresh" :class="refreshing ? 'motion-safe:animate-spin' : ''" />{{ refreshing ? '刷新中' : '刷新记录' }}</button></div>
    <p v-if="error" role="alert" class="mt-4 rounded-xl bg-amber-50 p-3 text-xs leading-6 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">{{ ready ? '记录刷新失败，当前显示上次获取的数据，结果可能已过期。' : '任务记录加载失败。' }}{{ error }}<span v-if="refreshedAt" class="block">上次更新：{{ historyTime(refreshedAt, true) }}</span><span class="block">请刷新记录后再执行操作。</span></p>
    <p v-if="!ready && !error" role="status" class="py-8 text-center text-sm text-slate-500 dark:text-slate-400">正在读取任务记录…</p>

    <div v-if="activeTasks.length" class="mt-4 space-y-3" aria-label="正在执行的任务">
      <article v-for="task in activeTasks" :key="task.key" class="rounded-xl border border-blue-100 bg-blue-50/70 p-4 dark:border-blue-900 dark:bg-blue-950/30">
        <div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="mb-1 text-xs text-blue-700 dark:text-blue-300">{{ historyLabels[task.kind] }}任务 · {{ taskStatus(task) }}</p><h3 class="truncate text-sm font-semibold" :title="task.title">{{ task.title }}</h3></div><button class="detail" :aria-label="`查看${task.title}的详情`" @click="openDetails(task)">详情</button></div>
        <div class="mt-3 flex flex-wrap justify-between gap-2 text-xs text-blue-800 dark:text-blue-200"><span>{{ resultSummary(task) }}</span><span>{{ activePhase(task) }}</span></div>
        <div v-if="task.items.length" role="progressbar" :aria-label="`${task.title}已完成文件进度`" :aria-valuemin="0" :aria-valuemax="task.items.length" :aria-valuenow="taskCounts(task).done" :aria-valuetext="`${resultSummary(task)}，${activePhase(task)}`" class="mt-2 h-1.5 overflow-hidden rounded-full bg-blue-100 dark:bg-blue-950"><div class="h-full rounded-full bg-blue-600 transition-[width] motion-reduce:transition-none dark:bg-blue-400" :style="{ width: `${taskCounts(task).done / task.items.length * 100}%` }" /></div>
      </article>
    </div>

    <p v-if="completedElsewhere" role="status" class="mt-4 flex flex-wrap items-center gap-2 text-xs text-slate-600 dark:text-slate-300">{{ historyLabels[completedElsewhere.kind] }}任务{{ taskStatus(completedElsewhere) }}，记录已归入对应分类。<button class="detail" @click="$emit('update:tab', completedElsewhere.kind)">查看{{ historyLabels[completedElsewhere.kind] }}记录</button></p>
    <div class="my-4 flex flex-wrap gap-1" role="group" aria-label="记录类型"><button v-for="kind in (['organize', 'cleanup', 'subtitle'] as const)" :key="kind" class="filter" :aria-pressed="tab === kind" @click="$emit('update:tab', kind)">{{ historyLabels[kind] }} <span class="ml-1 tabular-nums">{{ tasks.filter(task => task.kind === kind).length }}</span></button></div>
    <div v-if="ready" :aria-label="`${historyLabels[tab]}历史记录`">
      <div v-if="visible.length" class="hidden grid-cols-[minmax(0,1fr)_8rem_minmax(10rem,12rem)_7.5rem_3.5rem] gap-3 border-b border-slate-200 px-2 pb-2 text-xs text-slate-500 lg:grid dark:border-slate-700 dark:text-slate-400"><span>作品 / 任务</span><span>状态</span><span>{{ tab === 'cleanup' ? '条目结果' : '文件结果' }}</span><span>创建时间</span><span class="text-right">操作</span></div>
      <ul class="divide-y divide-slate-100 dark:divide-slate-800">
        <li v-for="task in visible" :key="task.key" class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-2 py-3 lg:grid-cols-[minmax(0,1fr)_8rem_minmax(10rem,12rem)_7.5rem_3.5rem]">
          <p class="col-start-1 row-start-1 truncate text-sm font-medium text-slate-800 dark:text-slate-100" :title="task.title">{{ task.title }}</p>
          <span class="col-start-2 row-start-1 justify-self-end rounded-md px-2 py-1 text-xs font-medium lg:justify-self-start" :class="`badge-${taskTone(task)}`">{{ taskStatus(task) }}</span>
          <div class="col-start-1 row-start-2 min-w-0 text-xs leading-5 text-slate-500 lg:col-start-3 lg:row-start-1 dark:text-slate-400"><p>{{ resultSummary(task) }}</p><time class="block text-slate-400 lg:hidden dark:text-slate-500" :datetime="task.createdAt" :title="historyTime(task.createdAt, true)">{{ historyTime(task.createdAt) }}</time></div>
          <time class="hidden text-xs tabular-nums text-slate-500 lg:col-start-4 lg:row-start-1 lg:block dark:text-slate-400" :datetime="task.createdAt" :title="historyTime(task.createdAt, true)">{{ historyTime(task.createdAt) }}</time>
          <button class="detail col-start-2 row-start-2 justify-self-end lg:col-start-5 lg:row-start-1" :aria-label="`查看${task.title}的详情`" @click="openDetails(task)">详情</button>
        </li>
      </ul>
      <p v-if="!history.length" class="py-8 text-center text-sm text-slate-500 dark:text-slate-400">{{ activeTasks.some(task => task.kind === tab) ? '当前任务的进度显示在上方，结束后会归入记录。' : activeOtherKinds.length ? `当前没有${historyLabels[tab]}历史记录；${activeOtherKinds.join('、')}任务正在执行，请在对应标签查看。` : `暂无${historyLabels[tab]}记录，任务执行后会在这里显示结果。` }}</p>
      <div v-if="history.length" class="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-slate-100 pt-3 text-xs text-slate-500 dark:border-slate-800 dark:text-slate-400"><p>已显示 {{ visible.length }}/{{ history.length }} 条，最多保留最近 {{ tab === 'organize' ? 30 : 10 }} 条{{ historyLabels[tab] }}记录<span v-if="tab === 'organize'" class="block mt-1">文件数包含视频及字幕</span><span v-else-if="tab === 'cleanup'" class="block mt-1">按文件或目录条目计数</span></p><button v-if="visible.length < history.length" class="action" @click="limits[tab] += 8">显示更多</button></div>
    </div>
    <OrganizeTaskDetails v-if="selectedTask" :key="selectedTask.key" :task="selectedTask" :account-name="accountName" :blocked-reason="blockedReason" :can-subtitle="canSubtitle" :history-error="error" :unavailable="unavailable" :resuming="resuming === selectedTask.organize?.id" :resume-error="resumeError && resumeError.id === selectedTask.organize?.id ? resumeError.message : ''" @close="selected = undefined" @source="act('source')" @subtitle="act('subtitle')" @resume="resume" />
  </section>
</template>

<style scoped>
.action { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-600 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800; }
.detail { @apply min-h-9 rounded-lg px-2 py-1 text-xs font-medium text-blue-700 hover:bg-blue-50 dark:text-blue-300 dark:hover:bg-blue-950/50; }
.filter { @apply rounded-lg px-3 py-2 text-xs text-slate-500 hover:bg-slate-50 dark:text-slate-400 dark:hover:bg-slate-800; }
.filter[aria-pressed="true"] { @apply bg-blue-50 font-medium text-blue-700 dark:bg-blue-950/40 dark:text-blue-300; }
.badge-active { @apply bg-blue-50 text-blue-700 dark:bg-blue-950/50 dark:text-blue-300; }
.badge-success { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300; }
.badge-warning { @apply bg-amber-50 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300; }
button:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
