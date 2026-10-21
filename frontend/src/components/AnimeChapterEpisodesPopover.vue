<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconListNumbers, IconX } from '@tabler/icons-vue'
import type { AnimeNamingTarget } from '../types/animeSubscription'
import AnimeChapterEpisodes from './AnimeChapterEpisodes.vue'

const props = defineProps<{ token: string; bangumiId: string; total?: number | null; target?: AnimeNamingTarget; targetBusy: boolean }>()
const button = ref<HTMLButtonElement>(), panel = ref<HTMLElement>()
const opened = ref(false), mounted = ref(false), restoreFocus = ref(false)
const panelID = 'anime-chapter-map-' + useId()
const panelStyle = ref<Record<string, string>>({})

async function show() {
  if (opened.value || !props.bangumiId) return
  mounted.value = true
  opened.value = true
  await nextTick()
  if (!panel.value || !button.value) return
  const rect = button.value.getBoundingClientRect()
  const inset = 12, gap = 8, width = Math.min(470, innerWidth - inset * 2)
  const maxHeight = Math.min(430, innerHeight - inset * 2)
  panelStyle.value = {
    width: width + 'px', maxHeight: maxHeight + 'px',
    left: Math.max(inset, Math.min(rect.right - width, innerWidth - width - inset)) + 'px',
    top: Math.max(inset, Math.min(rect.bottom + gap, innerHeight - maxHeight - inset)) + 'px',
  }
  if (!panel.value.matches(':popover-open')) panel.value.showPopover()
  await nextTick()
  panel.value.querySelector<HTMLButtonElement>('.ac-chapter-popover-close')?.focus({ preventScroll: true })
}

function close(focus = false) {
  restoreFocus.value = focus
  if (panel.value?.matches(':popover-open')) panel.value.hidePopover()
  else finishClose()
}

function finishClose() {
  const focus = restoreFocus.value
  opened.value = false
  restoreFocus.value = false
  if (focus) void nextTick(() => button.value?.focus({ preventScroll: true }))
}

function toggle(event: Event) {
  event.stopPropagation()
  if (opened.value) close(true)
  else void show()
}

function onToggle(event: Event) {
  if ((event as ToggleEvent).newState === 'closed') finishClose()
}

function onPointerDown(event: PointerEvent) {
  const target = event.target
  if (opened.value && target instanceof Node && !panel.value?.contains(target) && !button.value?.contains(target)) close()
}

function onEscape(event: KeyboardEvent) {
  if (!opened.value || event.key !== 'Escape') return
  event.preventDefault()
  event.stopImmediatePropagation()
  close(true)
}

function onScroll(event: Event) {
  const target = event.target
  if (opened.value && !(target instanceof Node && panel.value?.contains(target))) close()
}
function onResize() { if (opened.value) close() }
onMounted(() => {
  document.addEventListener('pointerdown', onPointerDown)
  document.addEventListener('keydown', onEscape, true)
  document.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onPointerDown)
  document.removeEventListener('keydown', onEscape, true)
  document.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', onResize)
  if (panel.value?.matches(':popover-open')) panel.value.hidePopover()
})
</script>

<template>
  <button
    ref="button"
    type="button"
    class="ae-button ac-chapter-reference-button"
    aria-label="查看篇章集数与 TMDB 映射"
    aria-haspopup="dialog"
    :aria-expanded="opened"
    :aria-controls="mounted ? panelID : undefined"
    @click="toggle"
  >
    <IconListNumbers :size="16" aria-hidden="true" />
    <span>集数参考<span v-if="total" class="ac-chapter-total"> · {{ total }} 集</span></span>
  </button>
  <section
    v-if="mounted"
    :id="panelID"
    ref="panel"
    popover="auto"
    role="dialog"
    aria-label="篇章集数对应关系"
    class="floating-surface ac-chapter-popover"
    :style="panelStyle"
    @toggle="onToggle"
  >
    <header class="ac-chapter-popover-header">
      <h3>篇章集数映射</h3>
      <button type="button" class="ae-icon ac-chapter-popover-close" aria-label="关闭集数映射" @click="close(true)"><IconX :size="17" aria-hidden="true" /></button>
    </header>
    <p class="ac-chapter-popover-hint">仅核对 Bangumi 与 TMDB 集号，不代表下载进度。</p>
    <div class="ac-chapter-popover-body">
      <AnimeChapterEpisodes :token="token" :bangumi-id="bangumiId" :target="target" :target-busy="targetBusy" :active="opened" />
    </div>
  </section>
</template>

<style scoped>
.ac-chapter-reference-button { min-height: 40px; padding-inline: 11px; }
.ac-chapter-total { font-variant-numeric: tabular-nums; }
.ac-chapter-popover[popover] { position: fixed; inset: auto; margin: 0; padding: 14px; overflow: clip; border-radius: 14px; color: var(--floating-surface-text); }
.ac-chapter-popover:popover-open { display: flex; flex-direction: column; }
.ac-chapter-popover-header { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.ac-chapter-popover-header h3 { font-size: 13px; font-weight: 600; }
.ac-chapter-popover-close { min-height: 36px; width: 36px; border-radius: 9px; }
.ac-chapter-popover-hint { flex: none; margin: 1px 0 10px; color: var(--floating-surface-muted); font-size: 11px; line-height: 1.7; }
.ac-chapter-popover-body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; }
.ac-chapter-popover-body :deep(.ae-episode-actions .ae-link) { min-height: 40px; }
@media (max-width: 639px) {
  .ac-chapter-reference-button { min-height: 44px; padding-inline: 8px; gap: 5px; font-size: 11px; }
  .ac-chapter-total { display: none; }
  .ac-chapter-popover[popover] { padding: 12px; }
  .ac-chapter-popover-close { width: 40px; min-height: 40px; }
}
</style>
