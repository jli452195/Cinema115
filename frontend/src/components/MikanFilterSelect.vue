<script setup lang="ts">
import { ref } from 'vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { useHorizontalScrollTrack } from '../composables/useHorizontalScrollTrack'

interface Option { value: string; label: string }
const props = defineProps<{ modelValue: string; options: Option[]; label: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const strip = ref<HTMLElement>()
const { indicatorStyle } = useSlidingIndicator(strip, '[aria-pressed="true"]', [() => props.modelValue, () => props.options], { insetY: 6 })
const {
  canScrollLeft, canScrollRight, isDragging,
  updateScrollEdges, centerSelected, moveFocus, handleFocusIn,
  onPointerdown, onClickCapture, onDragstart,
} = useHorizontalScrollTrack(strip, {
  selectedSelector: '[aria-pressed="true"]',
  smoothOn: () => props.modelValue,
  deps: [() => props.options],
  mouseDrag: true,
})

function choose(value: string) {
  if (props.disabled) return
  if (value === props.modelValue) { centerSelected('smooth'); return }
  emit('update:modelValue', value)
}
</script>

<template>
  <nav
    ref="strip"
    :aria-label="label"
    :aria-busy="disabled"
    class="mikan-filter-strip"
    :class="{ 'has-scroll-left': canScrollLeft, 'has-scroll-right': canScrollRight, 'is-dragging': isDragging }"
    @scroll.passive="updateScrollEdges"
    @pointerdown="onPointerdown"
    @click.capture="onClickCapture"
    @dragstart="onDragstart"
    @keydown="moveFocus"
    @focusin="handleFocusIn"
  >
    <div class="mikan-filter-indicator" :style="indicatorStyle" aria-hidden="true"></div>
    <button v-for="option in options" :key="option.value" type="button" :aria-pressed="modelValue === option.value" :aria-disabled="disabled" :title="option.label" class="mikan-filter-chip" @click="choose(option.value)">
      <span>{{ option.label }}</span>
    </button>
  </nav>
</template>

<style scoped>
.mikan-filter-strip {
  position: relative; isolation: isolate; display: flex; min-width: 0; gap: 6px; padding: 2px;
  overflow-x: auto; overflow-y: hidden; overscroll-behavior-x: contain; scrollbar-width: none;
  -webkit-overflow-scrolling: touch; -webkit-user-select: none; user-select: none;
  -webkit-mask-image: linear-gradient(to right, var(--scroll-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--scroll-fade-right, #000));
  mask-image: linear-gradient(to right, var(--scroll-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--scroll-fade-right, #000));
}
.mikan-filter-strip::-webkit-scrollbar { display: none; }
.mikan-filter-strip.has-scroll-left { --scroll-fade-left: transparent; }
.mikan-filter-strip.has-scroll-right { --scroll-fade-right: transparent; }
.mikan-filter-strip:is(.has-scroll-left, .has-scroll-right) { cursor: grab; }
.mikan-filter-strip.is-dragging, .mikan-filter-strip.is-dragging .mikan-filter-chip { cursor: grabbing; }
.mikan-filter-indicator { position: absolute; z-index: 1; border-radius: 10px; background: var(--search-dialog-action-background); pointer-events: none; }
.mikan-filter-chip { position: relative; display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; min-width: 44px; min-height: 44px; padding: 6px 10px; border: 0; border-radius: 10px; background: transparent; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; white-space: nowrap; cursor: pointer; }
.mikan-filter-chip::before { position: absolute; z-index: 0; inset: 6px 0; border-radius: inherit; background: var(--search-dialog-card); pointer-events: none; content: ''; }
.mikan-filter-chip > span { position: relative; z-index: 2; }
.mikan-filter-chip[aria-pressed="true"] { color: var(--search-dialog-on-tonal); font-weight: 600; }
.mikan-filter-chip[aria-disabled="true"] { cursor: wait; }
.mikan-filter-chip:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: -2px; }
@media (hover: hover) {
  .mikan-filter-chip:hover:not([aria-disabled="true"]):not([aria-pressed="true"])::before { background: var(--search-dialog-secondary-hover); }
  .mikan-filter-strip:has(.mikan-filter-chip[aria-pressed="true"]:hover:not([aria-disabled="true"])) > .mikan-filter-indicator { background: var(--search-dialog-action-hover); }
}
@media (forced-colors: active) {
  .mikan-filter-strip { -webkit-mask-image: none; mask-image: none; }
}
@media (prefers-reduced-motion: reduce) {
  .mikan-filter-indicator { transition: none !important; }
}
</style>
