<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import type { AnimeSourceProgress } from '../types/animeSubscription'
import { sourceProgressView } from '../utils/animeSubscriptionPresentation'

const props = defineProps<{ progress?: AnimeSourceProgress; pending?: boolean }>()
const view = computed(() => sourceProgressView(props.progress, props.pending))
const trigger = ref<HTMLButtonElement>(), panel = ref<HTMLElement>()
const opened = ref(false), position = ref<Record<string, string>>({})
const hintID = `source-progress-${useId()}`
let pinned = false

function show() {
  if (!panel.value || !trigger.value || opened.value || !view.value.hint) return
  const rect = trigger.value.getBoundingClientRect()
  panel.value.showPopover()
  const width = panel.value.offsetWidth, height = panel.value.offsetHeight
  position.value = {
    left: `${Math.max(12, Math.min(rect.left, innerWidth - width - 12))}px`,
    top: `${Math.max(12, rect.bottom + height > innerHeight - 12 ? rect.top - height + 1 : rect.bottom - 1)}px`,
  }
  opened.value = true
}
function close() {
  if (panel.value?.matches(':popover-open')) panel.value.hidePopover()
  opened.value = false; pinned = false
}
function toggle() {
  if (pinned) close()
  else { pinned = true; show() }
}
function escape(event: KeyboardEvent) {
  if (opened.value) { event.preventDefault(); event.stopPropagation(); close() }
}
function changed(event: Event) {
  opened.value = (event as ToggleEvent).newState === 'open'
  if (!opened.value) pinned = false
}
// 只在提示打开时监听滚动和缩放，不增加常驻监听或计时器。
watch(opened, (open, _, cleanup) => {
  if (!open) return
  document.addEventListener('scroll', close, true)
  window.addEventListener('resize', close)
  cleanup(() => { document.removeEventListener('scroll', close, true); window.removeEventListener('resize', close) })
})
watch([() => props.progress, () => props.pending], close)
onBeforeUnmount(close)
</script>

<template>
  <span class="as-source-progress" :data-state="view.state" @pointerenter="event => { if (event.pointerType === 'mouse') show() }" @pointerleave="() => { if (!pinned) close() }" @click.stop @keydown.esc="escape">
    <button ref="trigger" type="button" class="as-progress-trigger" :aria-label="`查看来源整理进度：${view.label}，${view.status}`" :aria-expanded="opened" :aria-describedby="opened ? hintID : undefined" @click="toggle">
      <span class="as-progress-label"><span>{{ view.label }}</span><span v-if="view.status" class="as-progress-status">{{ view.status }}</span></span>
      <span class="as-progress-track" role="progressbar" aria-label="来源整理进度" :aria-valuemin="0" :aria-valuenow="view.total ? view.count : undefined" :aria-valuemax="view.total || undefined" :aria-valuetext="`${view.label}，${view.status}。${view.hint}`">
        <span class="as-progress-fill">
          <span v-for="segment in view.segments" :key="segment.first" class="as-progress-segment" :data-kind="segment.state" :style="{ transform: `translateX(${(segment.first - (progress?.firstEpisode || 0)) / view.total * 100}%) scaleX(${(segment.last - segment.first + 1) / view.total})` }"></span>
        </span>
      </span>
    </button>
    <span :id="hintID" ref="panel" popover="auto" role="tooltip" class="floating-surface as-progress-hint" :style="position" @toggle="changed">
      <span>{{ view.coverage || view.label }}</span><span>{{ view.detail }}</span>
    </span>
  </span>
</template>

<style scoped>
.as-source-progress { display: block; flex: 1 1 200px; width: 220px; min-width: 160px; max-width: 280px; text-align: left; color: var(--ae-muted); font-size: 11px; font-weight: 400; line-height: 1.5; font-variant-numeric: tabular-nums; }
.as-progress-trigger { display: grid; width: 100%; gap: 7px; padding: 6px 0; text-align: left; cursor: pointer; border-radius: 6px; }
.as-progress-label { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; white-space: nowrap; }
.as-progress-label > span:first-child { font-weight: 600; }
.as-progress-status { font-size: 10px; }
.as-progress-track { position: relative; display: block; height: 8px; overflow: hidden; border-radius: 999px; background: rgb(148 163 184 / .12); box-shadow: inset 0 1px 2px rgb(15 23 42 / .15), inset 0 0 0 1px rgb(148 163 184 / .14); }
.as-progress-fill { position: absolute; inset: 0; transform-origin: left; animation: as-progress-enter 500ms cubic-bezier(.22, 1, .36, 1) both; }
.as-progress-segment { position: absolute; inset: 0; transform-origin: left; transition: transform 500ms cubic-bezier(.22, 1, .36, 1); }
.as-progress-segment[data-kind=organized] { background: linear-gradient(180deg, #96ceff 0%, #529fff 45%, #3281f5 100%); box-shadow: inset 0 1px 0 rgb(255 255 255 / .65), inset 0 -1px 0 rgb(29 78 216 / .18); }
.as-progress-segment[data-kind=missing] { background: linear-gradient(180deg, #ffc1b8, #f58279 48%, #ed625a); box-shadow: inset 0 1px 0 rgb(255 255 255 / .5); }
[data-state=complete] .as-progress-fill, [data-state=unknown] .as-progress-fill { animation: none; }
[data-state=complete] .as-progress-segment[data-kind=organized] { background: linear-gradient(180deg, #a0e2ac, #16a34a); }
[data-state=complete] .as-progress-status { color: #15803d; }
[data-state=missing] .as-progress-status { color: #b91c1c; }
.dark [data-state=complete] .as-progress-status { color: #86efac; }
.dark [data-state=missing] .as-progress-status { color: #fca5a5; }
.as-progress-hint[popover] { position: fixed; inset: auto; width: min(320px, calc(100vw - 24px)); max-height: min(200px, calc(100dvh - 24px)); margin: 0; padding: 11px 13px; border-radius: 10px; overflow: auto; color: var(--floating-surface-text); font-size: 11px; line-height: 1.8; overflow-wrap: anywhere; }
.as-progress-hint:popover-open { display: grid; gap: 3px; }
.as-progress-hint > span:last-child { color: var(--floating-surface-muted); }
@keyframes as-progress-enter { from { transform: scaleX(0); opacity: .6; } to { transform: scaleX(1); opacity: 1; } }
@keyframes as-progress-shine { from { transform: translateX(-110%); } to { transform: translateX(400%); } }
@media (hover: hover) and (pointer: fine) {
  .as-progress-track::after { content: ''; position: absolute; inset: 0 auto 0 0; width: 35%; background: linear-gradient(110deg, transparent, rgb(255 255 255 / .55), transparent); transform: translateX(-110%); pointer-events: none; }
  [data-state=ongoing]:hover .as-progress-track::after, [data-state=missing]:hover .as-progress-track::after { animation: as-progress-shine 850ms ease-out both; }
}
@media (prefers-reduced-motion: reduce) { .as-progress-fill, .as-progress-track::after { animation: none !important; } .as-progress-segment { transition: none; } }
@media (max-width: 639px) { .as-source-progress { grid-column: 2; width: 100%; min-width: 0; max-width: none; } }
</style>
