<script setup lang="ts">
import { computed, ref } from 'vue'
import P115Icon from './P115Icon.vue'
import OrganizeTaskDetails from './OrganizeTaskDetails.vue'
import OrganizeTaskCard from './OrganizeTaskCard.vue'
import type { OrganizeJob } from '../types/organize'
import type { P115Account } from '../types/p115Accounts'
import { historyTasks, historyTime, isActiveTask, type HistoryTask } from '../utils/organizeHistory'

const props = defineProps<{
  jobs: OrganizeJob[]; accounts: P115Account[]
  ready: boolean; error: string; refreshing: boolean; refreshedAt: string
  resuming: string; resumeError?: { id: string; message: string }
}>()
const emit = defineEmits<{ refresh: []; resume: [job: OrganizeJob] }>()
const limit = ref(12)
const selected = ref<{ key: string; snapshot: HistoryTask }>()
const tasks = computed(() => historyTasks(props.jobs))
const activeTasks = computed(() => tasks.value.filter(isActiveTask))
const history = computed(() => tasks.value.filter(task => !isActiveTask(task)))
const visible = computed(() => history.value.slice(0, limit.value))
const selectedTask = computed(() => tasks.value.find(task => task.key === selected.value?.key) || selected.value?.snapshot)
const unavailable = computed(() => !!selected.value && !tasks.value.some(task => task.key === selected.value?.key))
const accountName = computed(() => {
  const account = props.accounts.find(account => account.id === selectedTask.value?.accountId)
  return account?.primary ? '主账号' : account?.name || selectedTask.value?.accountId || ''
})
const blockedReason = computed(() => {
  if (unavailable.value) return '此任务已不在最近记录中，请重新选择有效记录。'
  if (!props.ready || props.error) return '请刷新任务记录，确认最新状态后再执行操作。'
  if (!props.accounts.some(account => account.id === selectedTask.value?.accountId && account.configured)) return '此任务的账号已删除或未登录，请先配置凭据。'
  return ''
})
function openDetails(task: HistoryTask) { selected.value = { key: task.key, snapshot: task } }
function resume() {
  const job = selectedTask.value?.organize
  if (job?.resumable && !blockedReason.value && !props.resuming) emit('resume', job)
}
</script>

<template>
  <section class="history-standalone min-w-0 text-slate-700 dark:text-slate-200" aria-label="任务记录">
    <div class="flex items-center justify-between gap-3"><h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">任务记录</h2><button class="action inline-flex items-center gap-2" :disabled="refreshing" @click="$emit('refresh')"><P115Icon name="refresh" :class="refreshing ? 'motion-safe:animate-spin' : ''" />{{ refreshing ? '刷新中' : '刷新记录' }}</button></div>
    <p v-if="error" role="alert" class="mt-4 rounded-xl bg-amber-50 p-3 text-xs leading-6 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">{{ ready ? '记录刷新失败，当前显示上次获取的数据，结果可能已过期。' : '任务记录加载失败。' }}{{ error }}<span v-if="refreshedAt" class="block">上次更新：{{ historyTime(refreshedAt, true) }}</span><span class="block">请刷新记录后再执行操作。</span></p>
    <p v-if="!ready && !error" role="status" class="py-8 text-center text-sm text-slate-500 dark:text-slate-400">正在读取任务记录…</p>

    <div v-if="activeTasks.length" class="history-grid mt-4" aria-label="正在执行的任务"><OrganizeTaskCard v-for="task in activeTasks" :key="task.key" :task="task" @details="openDetails(task)" /></div>

    <div v-if="ready" class="mt-4" aria-label="整理历史记录">
      <ul class="history-grid"><li v-for="task in visible" :key="task.key" class="min-w-0"><OrganizeTaskCard :task="task" @details="openDetails(task)" /></li></ul>
      <p v-if="!history.length" class="py-8 text-center text-sm text-slate-500 dark:text-slate-400">{{ activeTasks.length ? '当前任务的进度显示在上方，结束后会归入记录。' : '暂无整理记录，任务执行后会在这里显示结果。' }}</p>
      <div v-if="history.length" class="history-footer mt-3 flex flex-wrap items-center justify-between gap-3 pt-3 text-xs text-slate-500 dark:text-slate-400"><p>已显示 {{ visible.length }}/{{ history.length }} 条，最多保留最近 30 条整理记录<span class="block mt-1">文件数包含视频及字幕</span></p><button v-if="visible.length < history.length" class="action" @click="limit += 8">显示更多</button></div>
    </div>
    <OrganizeTaskDetails v-if="selectedTask" :key="selectedTask.key" :task="selectedTask" :account-name="accountName" :blocked-reason="blockedReason" :history-error="error" :unavailable="unavailable" :resuming="resuming === selectedTask.organize?.id" :resume-error="resumeError && resumeError.id === selectedTask.organize?.id ? resumeError.message : ''" @close="selected = undefined" @resume="resume" />
  </section>
</template>

<style scoped>
.action { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-600 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800; }
.history-grid { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
@media (min-width: 640px) { .history-grid { grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr)); } }
button:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
