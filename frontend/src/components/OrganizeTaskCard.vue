<script setup lang="ts">
import { computed } from 'vue'
import { IconCircleCheck, IconDeviceTv, IconEye, IconFolders, IconMovie } from '@tabler/icons-vue'
import { activePhase, historyTime, isActiveTask, resultSummary, taskCounts, taskStatus, taskTone, type HistoryTask } from '../utils/organizeHistory'

const props = defineProps<{ task: HistoryTask }>()
const emit = defineEmits<{ details: [] }>()
const active = computed(() => isActiveTask(props.task))
const counts = computed(() => taskCounts(props.task))
const typeIcon = computed(() => props.task.organize?.naming?.template.kind === 'movie' ? IconMovie : props.task.organize?.naming?.template.kind === 'tv' ? IconDeviceTv : IconFolders)
const episode = computed(() => {
  if (props.task.organize?.naming?.template.kind !== 'tv') return ''
  const videos = props.task.organize.items.filter(item => item.file.mediaType === 'video')
  if (videos.length !== 1) return ''
  // Source numbering can differ after manual mapping; use the actual target filename.
  const name = videos[0]!.name || videos[0]!.targetPath.split('/').at(-1) || ''
  const matches = [...name.matchAll(/(?:^|[^a-z0-9])(S[0-9]{1,3}E[0-9]{1,4})(?=$|[^a-z0-9])/gi)]
  return matches.length === 1 ? matches[0]![1]!.toUpperCase() : ''
})
const summary = computed(() => props.task.status === 'completed' && counts.value.total > 0 && counts.value.done === counts.value.total ? `${counts.value.total} 个文件` : resultSummary(props.task))
const paths = computed(() => {
  const single = props.task.items.length === 1 ? props.task.items[0] : undefined
  return [
    { label: '来源', value: single?.source || props.task.source },
    { label: single?.titleOnly ? '预计目标' : '目标', value: single?.target || props.task.target },
  ].filter(row => row.value).map(row => ({ ...row, preview: row.value?.split('/').filter(Boolean).at(-1) || row.value }))
})
let pointerStart: { x: number; y: number } | undefined
function open(event: MouseEvent) {
  const dragged = pointerStart && Math.hypot(event.clientX - pointerStart.x, event.clientY - pointerStart.y) > 4
  pointerStart = undefined
  const selection = window.getSelection()
  if (event.detail && dragged && selection?.toString() && (event.currentTarget as HTMLElement).contains(selection.anchorNode)) return
  emit('details')
}
</script>

<template>
  <button type="button" class="task-card ambient-glow" :class="{ 'task-card-active': active }" :aria-label="`查看${task.title}的详情`" :aria-description="[episode, active && `${resultSummary(task)}，${activePhase(task)}`].filter(Boolean).join('，') || undefined" aria-haspopup="dialog" @pointerdown="pointerStart = { x: $event.clientX, y: $event.clientY }" @pointercancel="pointerStart = undefined" @click="open">
    <span class="task-card-heading">
      <component :is="typeIcon" :size="17" class="task-card-type" aria-hidden="true" />
      <span class="task-card-title" :title="task.title">{{ task.title }}</span>
      <span v-if="episode" class="task-card-episode">{{ episode }}</span>
      <span class="task-card-details" aria-hidden="true"><IconEye :size="19" /></span>
    </span>
    <span class="task-card-result"><span class="task-card-badge" :class="`badge-${taskTone(task)}`"><IconCircleCheck v-if="taskTone(task) === 'success'" :size="14" class="task-card-success" aria-hidden="true" />{{ taskStatus(task) }}</span><span>{{ summary }}</span></span>
    <span v-if="paths.length" class="task-card-paths"><span v-for="path in paths" :key="path.label"><span class="task-card-label">{{ path.label }}</span><span class="task-card-path" :title="path.value">{{ path.preview }}</span></span></span>
    <span v-if="active" class="task-card-progress">
      <span>{{ activePhase(task) }}</span>
      <span v-if="counts.total" role="progressbar" :aria-label="`${task.title}已完成文件进度`" :aria-valuemin="0" :aria-valuemax="counts.total" :aria-valuenow="counts.done" :aria-valuetext="`${resultSummary(task)}，${activePhase(task)}`" class="task-card-track"><span :style="{ width: `${counts.done / counts.total * 100}%` }" /></span>
    </span>
    <time :datetime="task.createdAt" :title="historyTime(task.createdAt, true)">{{ historyTime(task.createdAt, true) }}</time>
  </button>
</template>

<style scoped>
.task-card { display: flex; width: 100%; height: 100%; min-width: 0; flex-direction: column; gap: 8px; padding: 12px 14px; border: 1px solid transparent; border-radius: 12px; background: #fff; box-shadow: 0 1px 4px rgb(24 24 27 / .07); text-align: left; cursor: pointer; user-select: text; transition: box-shadow 200ms, transform 200ms; }
.task-card-heading { display: flex; align-items: center; gap: 8px; }
.task-card-type { flex: none; color: #71717a; }
.task-card-episode { flex: none; color: #52525b; font-size: 11px; font-variant-numeric: tabular-nums; line-height: 20px; }
.task-card-title { min-width: 0; flex: 1; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 14px; font-weight: 600; line-height: 20px; color: #18181b; }
.task-card-details { display: grid; flex: none; width: 32px; height: 32px; margin: -6px -5px -6px 0; place-items: center; border-radius: 8px; color: #71717a; transition: color 160ms; }
.task-card:focus-visible .task-card-details { color: #2563eb; }
.task-card:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
.task-card-result { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; color: #52525b; font-size: 12px; line-height: 20px; }
.task-card-badge { display: inline-flex; align-items: center; gap: 4px; border-radius: 6px; padding: 2px 8px; font-size: 11px; font-weight: 500; }
.badge-active { background: #dbeafe; color: #1d4ed8; }
.badge-success { padding-inline: 0; color: #52525b; }
.task-card-success { color: #15803d; }
.badge-warning { background: #fef3c7; color: #92400e; }
.task-card-paths { display: grid; gap: 3px; font-size: 12px; line-height: 18px; }
.task-card-paths > span { display: grid; grid-template-columns: 48px minmax(0, 1fr); gap: 4px; }
.task-card-label, .task-card time { color: #71717a; }
.task-card-path { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; color: #52525b; }
.task-card time { margin-top: auto; padding-top: 2px; font-size: 11px; font-variant-numeric: tabular-nums; }
.task-card-active { border-color: #bfdbfe; background: #eff6ff; }
.task-card-active .task-card-label, .task-card-active time { color: #52525b; }
.task-card-progress { display: grid; gap: 7px; color: #1d4ed8; font-size: 12px; }
.task-card-track { height: 4px; overflow: hidden; border-radius: 4px; background: #dbeafe; }
.task-card-track > span { display: block; height: 100%; border-radius: inherit; background: #2563eb; transition: width 160ms; }
.dark .task-card { background: rgb(var(--color-slate-900)); box-shadow: 0 1px 4px rgb(0 0 0 / .2); }
.dark .task-card-title { color: #fafafa; }
.dark .task-card-result, .dark .task-card-path { color: #d4d4d8; }
.dark .task-card-label, .dark .task-card time { color: #a1a1aa; }
.dark .task-card-details, .dark .task-card-type { color: #a1a1aa; }
.dark .task-card-episode { color: #d4d4d8; }
.dark .task-card:focus-visible .task-card-details { color: #93c5fd; }
.dark .badge-active { background: #1e3a5f; color: #bfdbfe; }
.dark .badge-success { color: #d4d4d8; }
.dark .task-card-success { color: #4ade80; }
.dark .badge-warning { background: #422f13; color: #fcd34d; }
.dark .task-card-active { border-color: #1e40af; background: #15233c; }
.dark .task-card-progress { color: #93c5fd; }
.dark .task-card-track { background: #1e3a5f; }
.dark .task-card-track > span { background: #60a5fa; }
@media (hover: hover) and (pointer: fine) {
  .task-card:hover { box-shadow: 0 10px 24px -12px rgb(15 23 42 / .22); }
  .task-card:hover .task-card-details { color: #2563eb; }
  .dark .task-card:hover { box-shadow: 0 10px 24px -12px rgb(0 0 0 / .45); }
  .dark .task-card:hover .task-card-details { color: #93c5fd; }
}
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .task-card:hover { transform: translateY(-2px); } .task-card:active { transform: translateY(0) scale(.995); } }
@media (prefers-reduced-motion: reduce) { .task-card, .task-card-details, .task-card-track > span { transition: none; } }
</style>
