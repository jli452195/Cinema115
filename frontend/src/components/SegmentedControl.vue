<script setup lang="ts" generic="T extends string | number">
import { onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'

interface Option { value: T; label: string; count?: number | string; disabled?: boolean; title?: string }
const props = defineProps<{ modelValue: T; options: readonly Option[]; label: string; disabled?: boolean; density?: 'default' | 'compact' }>()
const emit = defineEmits<{ 'update:modelValue': [value: T]; change: [value: T] }>()
const strip = ref<HTMLElement>(), countID = useId()
const { indicatorStyle } = useSlidingIndicator(strip, '[aria-pressed="true"]', [() => props.modelValue, () => props.options])
let observer: ResizeObserver | undefined

function revealSelected() {
  const container = strip.value, selected = container?.querySelector<HTMLButtonElement>('[aria-pressed=true]')
  if (!container || !selected) return
  // 只滚动控件本身，避免恢复筛选值时改变页面的滚动位置。
  if (selected.offsetLeft < container.scrollLeft + 4) container.scrollLeft = selected.offsetLeft - 4
  else if (selected.offsetLeft + selected.offsetWidth > container.scrollLeft + container.clientWidth - 4) container.scrollLeft = selected.offsetLeft + selected.offsetWidth - container.clientWidth + 4
}
watch([() => props.modelValue, () => props.options], revealSelected, { deep: true, flush: 'post' })
onMounted(() => { observer = new ResizeObserver(revealSelected); observer.observe(strip.value!); revealSelected() })
onBeforeUnmount(() => observer?.disconnect())

function choose(option: Option) {
  if (props.disabled || option.disabled) return
  // 重选当前项也通知页面，保留筛选时复位分页等行为。
  emit('update:modelValue', option.value)
  emit('change', option.value)
}
function moveFocus(event: KeyboardEvent) {
  if (event.altKey || event.ctrlKey || event.metaKey || !(event.target instanceof HTMLButtonElement) || !strip.value) return
  const buttons = [...strip.value.querySelectorAll<HTMLButtonElement>(':scope > button:not(:disabled)')]
  const index = buttons.indexOf(event.target)
  if (index < 0 || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : buttons.length - 1)) % buttons.length
  // 方向键只移动焦点，确认后才筛选，避免浏览选项时连续请求。
  buttons[next]?.focus({ preventScroll: true })
  buttons[next]?.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' })
}
</script>

<template>
  <nav ref="strip" :aria-label="label" :aria-disabled="disabled || undefined" class="segmented-control" :class="{ 'segmented-control-compact': density === 'compact' }" @keydown="moveFocus">
    <span class="segmented-control-indicator" :style="indicatorStyle" aria-hidden="true"></span>
    <button v-for="(option, index) in options" :key="option.value" type="button" class="segmented-control-option" :aria-label="option.label" :aria-describedby="option.count !== undefined ? `${countID}-${index}` : undefined" :aria-pressed="modelValue === option.value" :disabled="disabled || option.disabled" :title="option.title" @click="choose(option)">
      <slot name="option" :option="option" :selected="modelValue === option.value">{{ option.label }}</slot>
      <span v-if="option.count !== undefined" :id="`${countID}-${index}`" class="segmented-control-count">{{ option.count }}</span>
    </button>
  </nav>
</template>

<style scoped>
.segmented-control { position: relative; isolation: isolate; display: inline-flex; max-width: 100%; min-width: 0; gap: 4px; padding: 4px; border-radius: 12px; background: rgb(var(--color-slate-100)); overflow-x: auto; scrollbar-width: none; vertical-align: middle; }
.segmented-control::-webkit-scrollbar { display: none; }
.segmented-control-indicator { position: absolute; z-index: 0; pointer-events: none; border-radius: 8px; background: rgb(var(--surface-card)); box-shadow: 0 2px 8px rgb(15 23 42 / .08); }
.segmented-control-option { position: relative; z-index: 1; display: inline-flex; flex: 0 0 auto; align-items: center; justify-content: center; gap: 7px; min-width: 64px; min-height: 44px; padding: 8px 14px; border: 0; border-radius: 8px; background: transparent; color: rgb(var(--content-secondary)); font-size: 14px; font-weight: 500; line-height: 20px; white-space: nowrap; cursor: pointer; transition: color 150ms; }
.segmented-control-option[aria-pressed=true] { color: rgb(var(--selection-content)); }
.segmented-control-option:disabled { opacity: .5; cursor: default; }
.segmented-control-option:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: -2px; }
.segmented-control-count { font-size: 11px; font-weight: 400; font-variant-numeric: tabular-nums; opacity: .7; }
.segmented-control-compact .segmented-control-option { min-height: 36px; padding: 6px 12px; font-size: 13px; }
.dark .segmented-control { background: rgb(var(--color-slate-800) / .6); }
.dark .segmented-control-indicator { background: rgb(var(--color-slate-700)); box-shadow: 0 2px 8px rgb(0 0 0 / .2); }
@media (hover: hover) { .segmented-control-option:not(:disabled):not([aria-pressed=true]):hover { color: rgb(var(--content-primary)); } }
@media (max-width: 639px) { .segmented-control-compact .segmented-control-option { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .segmented-control-option { transition: none; } }
</style>
