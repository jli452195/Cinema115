<script setup lang="ts" generic="T extends string | number">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'

interface Option { value: T; label: string; count?: number | string; disabled?: boolean; title?: string }
const props = defineProps<{ modelValue: T; options: readonly Option[]; label: string; disabled?: boolean; density?: 'default' | 'compact'; appearance?: 'default' | 'neutral' | 'underline' | 'subtle' | 'mobile-line' | 'mobile-pill' | 'pwa-pill' | 'filter-row' }>()
const emit = defineEmits<{ 'update:modelValue': [value: T]; change: [value: T] }>()
const strip = ref<HTMLElement>(), countID = useId()
const isFilterRow = computed(() => props.appearance === 'filter-row')
const indicatorContainer = computed(() => isFilterRow.value ? undefined : strip.value)
const { indicatorStyle } = useSlidingIndicator(indicatorContainer, '[aria-pressed="true"]', [() => props.modelValue, () => props.options])
const canScrollLeft = ref(false), canScrollRight = ref(false), isDragging = ref(false)
let observer: ResizeObserver | undefined
let contentObserver: MutationObserver | undefined
let motionQuery: MediaQueryList | undefined
let rowLayout = ''
let drag: { pointerId: number; startX: number; startLeft: number } | undefined
let suppressClick = false

function selectedButton() {
  const container = strip.value
  // 真实选项只按按下状态定位，额外动作可用选中类回显不在选项中的值。
  return container?.querySelector<HTMLButtonElement>(':scope > button[aria-pressed="true"]')
    ?? container?.querySelector<HTMLButtonElement>(':scope > button:not([aria-pressed]).is-selected')
}
function measureRowLayout() {
  const container = strip.value, selected = selectedButton()
  return container ? [container.clientWidth, container.scrollWidth, selected?.offsetLeft, selected?.offsetWidth].join(':') : ''
}
function updateScrollEdges() {
  const container = strip.value
  const maximum = isFilterRow.value && container ? Math.max(0, container.scrollWidth - container.clientWidth) : 0
  const left = Math.max(0, Math.min(maximum, container?.scrollLeft ?? 0))
  canScrollLeft.value = left > 1
  canScrollRight.value = maximum - left > 1
}
function centerButton(button: HTMLButtonElement, behavior: ScrollBehavior = 'instant') {
  const container = strip.value
  if (!container?.clientWidth) return
  const left = button.offsetLeft - (container.clientWidth - button.offsetWidth) / 2
  // 不使用 scrollIntoView，始终只改变当前轨道的横向位置。
  container.scrollTo({
    left: Math.max(0, Math.min(container.scrollWidth - container.clientWidth, left)),
    behavior: behavior === 'smooth' && !motionQuery?.matches ? 'smooth' : 'instant',
  })
  updateScrollEdges()
}
function revealSelected(behavior: ScrollBehavior = 'instant') {
  const container = strip.value
  if (isFilterRow.value) {
    rowLayout = measureRowLayout()
    const selected = selectedButton()
    if (selected) centerButton(selected, behavior)
    updateScrollEdges()
    return
  }
  const selected = container?.querySelector<HTMLButtonElement>('[aria-pressed=true]')
  if (!container || !selected) return
  // 只滚动控件本身，避免恢复筛选值时改变页面的滚动位置。
  if (selected.offsetLeft < container.scrollLeft + 4) container.scrollLeft = selected.offsetLeft - 4
  else if (selected.offsetLeft + selected.offsetWidth > container.scrollLeft + container.clientWidth - 4) container.scrollLeft = selected.offsetLeft + selected.offsetWidth - container.clientWidth + 4
}
function handleResize() {
  // 值切换已记录新的布局时，尺寸回调不打断正在进行的居中动画。
  if (isFilterRow.value && measureRowLayout() === rowLayout) updateScrollEdges()
  else revealSelected()
}
function observeResizeTargets() {
  observer?.disconnect()
  const container = strip.value
  if (!container || !observer) return
  observer.observe(container)
  if (isFilterRow.value) container.querySelectorAll<HTMLButtonElement>(':scope > button').forEach(button => observer!.observe(button))
}
function observeRowContent() {
  contentObserver?.disconnect()
  if (isFilterRow.value && strip.value) {
    contentObserver?.observe(strip.value, {
      childList: true, characterData: true, subtree: true,
      attributes: true, attributeFilter: ['class', 'aria-pressed', 'disabled', 'hidden'],
    })
  }
}
function handleMotionChange() {
  if (isFilterRow.value && motionQuery?.matches) revealSelected()
}
watch([() => props.modelValue, () => props.options], (values, previous) => {
  observeResizeTargets()
  revealSelected(isFilterRow.value && values[0] !== previous[0] ? 'smooth' : 'instant')
}, { deep: true, flush: 'post' })
watch(() => props.appearance, () => {
  stopDrag(true)
  observeResizeTargets()
  observeRowContent()
  revealSelected()
  updateScrollEdges()
}, { flush: 'post' })
onMounted(() => {
  motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
  motionQuery.addEventListener('change', handleMotionChange)
  observer = new ResizeObserver(handleResize)
  contentObserver = new MutationObserver(records => {
    // 忽略轨道自身的渐隐类变化，只响应按钮及插槽内容的变动。
    if (!records.some(record => record.type !== 'attributes' || (record.target as HTMLElement).parentElement === strip.value)) return
    observeResizeTargets()
    handleResize()
  })
  observeResizeTargets()
  observeRowContent()
  revealSelected()
})
onBeforeUnmount(() => {
  observer?.disconnect()
  contentObserver?.disconnect()
  motionQuery?.removeEventListener('change', handleMotionChange)
  stopDrag(true)
})

function stopDrag(clearClick = false) {
  drag = undefined
  isDragging.value = false
  if (clearClick) suppressClick = false
  window.removeEventListener('pointermove', moveDrag, true)
  window.removeEventListener('pointerup', endDrag, true)
  window.removeEventListener('pointercancel', endDrag, true)
  window.removeEventListener('blur', cancelDrag)
}
function cancelDrag() { stopDrag(true) }
function startDrag(event: PointerEvent) {
  if (!isFilterRow.value) return
  stopDrag(true)
  const container = strip.value
  // 只接管鼠标，触摸与手写笔继续使用浏览器的原生滚动。
  if (event.pointerType !== 'mouse' || event.button !== 0 || !container || container.scrollWidth <= container.clientWidth) return
  container.scrollTo({ left: container.scrollLeft, behavior: 'instant' })
  drag = { pointerId: event.pointerId, startX: event.clientX, startLeft: container.scrollLeft }
  window.addEventListener('pointermove', moveDrag, { capture: true, passive: false })
  window.addEventListener('pointerup', endDrag, true)
  window.addEventListener('pointercancel', endDrag, true)
  window.addEventListener('blur', cancelDrag)
}
function moveDrag(event: PointerEvent) {
  const container = strip.value
  if (!drag || drag.pointerId !== event.pointerId || !container) return
  if (!(event.buttons & 1)) { stopDrag(); return }
  const distance = event.clientX - drag.startX
  if (!isDragging.value && Math.abs(distance) <= 4) return
  isDragging.value = true
  suppressClick = true
  event.preventDefault()
  container.scrollLeft = drag.startLeft - distance
  updateScrollEdges()
}
function endDrag(event: PointerEvent) {
  if (drag?.pointerId === event.pointerId) stopDrag(event.type === 'pointercancel')
}
function preventDragClick(event: MouseEvent) {
  if (!isFilterRow.value || !suppressClick || event.detail === 0) return
  suppressClick = false
  event.preventDefault()
  event.stopImmediatePropagation()
}
function preventNativeDrag(event: DragEvent) {
  if (isFilterRow.value) event.preventDefault()
}

function choose(option: Option) {
  if (props.disabled || option.disabled) return
  if (isFilterRow.value && props.modelValue === option.value) revealSelected('smooth')
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
  const button = buttons[next]
  if (!button) return
  button.focus({ preventScroll: true })
  if (isFilterRow.value) centerButton(button)
  else button.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' })
}
</script>

<template>
  <nav ref="strip" :aria-label="label" :aria-disabled="disabled || undefined" class="segmented-control" :class="[density === 'compact' && 'segmented-control-compact', appearance && appearance !== 'default' && `segmented-control-${appearance}`, { 'has-scroll-left': canScrollLeft, 'has-scroll-right': canScrollRight, 'is-dragging': isDragging }]" @keydown="moveFocus" @scroll.passive="updateScrollEdges" @pointerdown="startDrag" @click.capture="preventDragClick" @dragstart="preventNativeDrag">
    <span v-if="!isFilterRow" class="segmented-control-indicator" :style="indicatorStyle" aria-hidden="true"></span>
    <button v-for="(option, index) in options" :key="option.value" type="button" :class="isFilterRow ? 'filter-chip' : 'segmented-control-option'" :aria-label="option.label" :aria-describedby="option.count !== undefined ? `${countID}-${index}` : undefined" :aria-pressed="modelValue === option.value" :disabled="disabled || option.disabled" :title="option.title" @click="choose(option)">
      <slot name="option" :option="option" :selected="modelValue === option.value">{{ option.label }}</slot>
      <span v-if="option.count !== undefined" :id="`${countID}-${index}`" class="segmented-control-count">{{ option.count }}</span>
    </button>
    <slot name="after" :disabled="disabled" />
  </nav>
</template>

<style scoped>
.segmented-control { position: relative; isolation: isolate; display: inline-flex; max-width: 100%; min-width: 0; gap: 4px; padding: 4px; border-radius: 12px; background: rgb(var(--color-slate-100)); overflow-x: auto; scrollbar-width: none; vertical-align: middle; }
.segmented-control::-webkit-scrollbar { display: none; }
.segmented-control-indicator { position: absolute; z-index: 0; pointer-events: none; border-radius: 8px; background: var(--segmented-selection-background, rgb(var(--surface-card))); box-shadow: 0 2px 8px rgb(15 23 42 / .08); }
.segmented-control-option { position: relative; z-index: 1; display: inline-flex; flex: 0 0 auto; align-items: center; justify-content: center; gap: 7px; min-width: 64px; min-height: 44px; padding: 8px 14px; border: 0; border-radius: 8px; background: transparent; color: rgb(var(--content-secondary)); font-size: 14px; font-weight: 500; line-height: 20px; white-space: nowrap; cursor: pointer; transition: color 150ms; }
.segmented-control-option[aria-pressed=true] { color: rgb(var(--selection-content)); }
.segmented-control-option:disabled { opacity: .5; cursor: default; }
.segmented-control-option:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: -2px; }
.segmented-control-count { font-size: 11px; font-weight: 400; font-variant-numeric: tabular-nums; opacity: .7; }
.segmented-control-compact .segmented-control-option { min-height: 36px; padding: 6px 12px; font-size: 13px; }
.dark .segmented-control { background: rgb(var(--color-slate-800) / .6); }
.segmented-control.segmented-control-filter-row {
  display: flex; flex-wrap: nowrap; align-items: center; gap: 4px;
  padding: 0; padding-inline: var(--filter-row-gutter, 0px); border-radius: 0; background: transparent;
  overflow-x: auto; overflow-y: hidden; overscroll-behavior-x: contain; scroll-behavior: auto;
  -webkit-overflow-scrolling: touch; -webkit-user-select: none; user-select: none;
  -webkit-mask-image: linear-gradient(to right, var(--filter-row-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--filter-row-fade-right, #000));
  mask-image: linear-gradient(to right, var(--filter-row-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--filter-row-fade-right, #000));
}
.segmented-control-filter-row.has-scroll-left { --filter-row-fade-left: transparent; }
.segmented-control-filter-row.has-scroll-right { --filter-row-fade-right: transparent; }
.segmented-control-filter-row:is(.has-scroll-left, .has-scroll-right) { cursor: grab; }
.segmented-control-filter-row.is-dragging { --filter-chip-cursor: grabbing; cursor: grabbing; }
@media (forced-colors: active) {
  .segmented-control.segmented-control-filter-row { -webkit-mask-image: none; mask-image: none; }
}
.dark .segmented-control-indicator { background: var(--segmented-selection-background, rgb(var(--color-slate-700))); box-shadow: 0 2px 8px rgb(0 0 0 / .2); }
/* 嵌套分段控件沿用玻璃底色和高光，不重复采样父表面。 */
.segmented-control.segmented-control-neutral { border: 1px solid var(--glass-inset-border); background: var(--glass-inset-surface); box-shadow: var(--glass-rim-shadow); }
.segmented-control-neutral .segmented-control-option { padding-inline: 18px; color: #64748b; border-radius: 9px; }
.segmented-control-neutral .segmented-control-option[aria-pressed=true] { color: #0f172a; font-weight: 600; }
.segmented-control-neutral .segmented-control-indicator { border-radius: 9px; background: #fff; box-shadow: 0 1px 3px rgb(15 23 42 / .08); }
.dark .segmented-control-neutral .segmented-control-option { color: #94a3b8; }
.dark .segmented-control-neutral .segmented-control-option[aria-pressed=true] { color: #f8fafc; }
.dark .segmented-control-neutral .segmented-control-indicator { background: #1e293b; box-shadow: 0 1px 3px rgb(0 0 0 / .35); }

/* 一级导航使用底线，沿用完整滑块定位以保留原有动画与尺寸变化处理。 */
.segmented-control-underline, .dark .segmented-control-underline { gap: 8px; padding: 0; border-radius: 0; background: transparent; }
.segmented-control-underline .segmented-control-indicator, .dark .segmented-control-underline .segmented-control-indicator { border-radius: 0; background: transparent; box-shadow: none; }
.segmented-control-underline .segmented-control-indicator::after { content: ''; position: absolute; right: 0; bottom: 0; left: 0; height: 2px; border-radius: 2px 2px 0 0; background: var(--search-dialog-primary, rgb(var(--selection-content))); }
.segmented-control-underline .segmented-control-option { min-width: 0; padding-inline: 10px; border-radius: 6px 6px 0 0; color: var(--search-dialog-muted, rgb(var(--content-secondary))); }
.segmented-control-underline .segmented-control-option[aria-pressed=true] { color: var(--search-dialog-primary, rgb(var(--selection-content))); font-weight: 600; }
.segmented-control-underline .segmented-control-count { min-width: 24px; padding: 1px 6px; border-radius: 999px; background: var(--search-dialog-secondary-background, rgb(148 163 184 / .14)); font-weight: 600; line-height: 16px; opacity: 1; }
.segmented-control-underline .segmented-control-option[aria-pressed=true] .segmented-control-count { background: var(--search-dialog-action-background, rgb(59 91 135 / .12)); }

/* 二级展示工具使用浅底槽，显式选择该外观时才影响控件样式。 */
.segmented-control-subtle { gap: 2px; padding: 2px; border-radius: 8px; background: var(--search-dialog-secondary-background, rgb(148 163 184 / .1)); box-shadow: inset 0 0 0 1px var(--search-dialog-card-border, rgb(148 163 184 / .2)); }
.dark .segmented-control-subtle { background: var(--search-dialog-secondary-background, rgb(148 163 184 / .1)); }
.segmented-control-subtle .segmented-control-indicator { border-radius: 6px; background: rgb(var(--surface-card)); box-shadow: 0 1px 2px rgb(15 23 42 / .06); }
.dark .segmented-control-subtle .segmented-control-indicator { background: var(--search-dialog-action-background, rgb(148 163 184 / .16)); box-shadow: inset 0 0 0 1px var(--search-dialog-card-border, rgb(148 163 184 / .2)); }
.segmented-control-subtle .segmented-control-option { min-width: 52px; border-radius: 6px; color: var(--search-dialog-muted, rgb(var(--content-secondary))); }
.segmented-control-subtle.segmented-control-compact .segmented-control-option { min-width: 48px; min-height: 32px; padding: 4px 10px; font-size: 12px; line-height: 18px; }
.segmented-control-subtle .segmented-control-option[aria-pressed=true] { color: var(--search-dialog-primary, rgb(var(--selection-content))); font-weight: 600; }
@media (hover: hover) { .segmented-control-option:not(:disabled):not([aria-pressed=true]):hover { color: rgb(var(--content-primary)); } }
@media (max-width: 639px) { .segmented-control-compact .segmented-control-option { min-height: 44px; } }
@media (max-width: 639px), (pointer: coarse) {
  .segmented-control-underline.segmented-control-compact .segmented-control-option { min-height: 44px; }
  .segmented-control-subtle { padding: 0; }
  .segmented-control-subtle.segmented-control-compact .segmented-control-option { min-width: 44px; min-height: 44px; padding-inline: 8px; font-size: 13px; }
}
@media (max-width: 639px) {
  .segmented-control-mobile-line, .dark .segmented-control-mobile-line { gap: 0; padding: 0; border-radius: 0; background: transparent; }
  .segmented-control-mobile-line .segmented-control-option { gap: 3px; min-width: 0; padding: 0 5px; font-size: 12px; }
  .segmented-control-mobile-line .segmented-control-option[aria-pressed=true] { color: rgb(var(--content-primary)); font-weight: 650; }
  .segmented-control-mobile-line .segmented-control-count { font-size: 10px; }
  .segmented-control-mobile-line .segmented-control-option[aria-pressed=true] .segmented-control-count { color: rgb(var(--selection-content)); opacity: 1; }
  .segmented-control-mobile-line .segmented-control-indicator, .dark .segmented-control-mobile-line .segmented-control-indicator { background: transparent; box-shadow: none; }
  .segmented-control-mobile-line .segmented-control-indicator::after {
    position: absolute; inset: auto 5px 4px; height: 2px; border-radius: 2px; background: rgb(var(--selection-content)); content: '';
  }
  .segmented-control-mobile-pill, .dark .segmented-control-mobile-pill { gap: 0; padding: 0 3px; border-radius: 999px; background: transparent; }
  .segmented-control-mobile-pill::before { position: absolute; inset: 7px 0; border-radius: inherit; background: rgb(var(--content-primary) / .075); content: ''; }
  .segmented-control-mobile-pill .segmented-control-option { min-width: 40px; padding: 0 7px; border-radius: 999px; font-size: 12px; }
  .segmented-control-mobile-pill .segmented-control-option[aria-pressed=true] { font-weight: 650; }
  .segmented-control-mobile-pill .segmented-control-indicator, .dark .segmented-control-mobile-pill .segmented-control-indicator {
    border-block: 9px solid transparent; border-radius: 999px; background: rgb(var(--selection-surface)); background-clip: padding-box; box-shadow: none;
  }
}
/* 整条分类底槽承载外阴影，不描边；选中项只用底色区分。 */
@media (max-width: 639px), (display-mode: standalone) {
  .segmented-control-pwa-pill, .dark .segmented-control-pwa-pill {
    gap: 0; padding: 0 4px; border-radius: 999px;
    background: color-mix(in srgb, rgb(var(--surface-hover)) 62%, rgb(var(--surface-page)));
    box-shadow: 0 1px 3px rgb(15 23 42 / .08), 0 3px 9px -2px rgb(15 23 42 / .12);
  }
  .dark .segmented-control-pwa-pill { box-shadow: 0 1px 3px rgb(0 0 0 / .25), 0 3px 9px -2px rgb(0 0 0 / .34); }
  .segmented-control-pwa-pill .segmented-control-option {
    flex: 1 0 auto; min-width: 44px; min-height: 44px; padding: 0 6px;
    border-radius: 999px; font-size: 13px; font-weight: 600; line-height: 18px;
  }
  .segmented-control-pwa-pill .segmented-control-option:not(:disabled):active { color: rgb(var(--selection-content)); }
  .segmented-control-pwa-pill .segmented-control-indicator,
  .dark .segmented-control-pwa-pill .segmented-control-indicator {
    border-radius: 0; background: transparent; box-shadow: none;
  }
  .segmented-control-pwa-pill .segmented-control-indicator::before {
    position: absolute; inset: 5px 2px; border-radius: 999px;
    background: color-mix(in srgb, rgb(var(--selection-surface)) 96%, rgb(var(--selection-content)));
    content: '';
  }
  @media (forced-colors: active) {
    .segmented-control-pwa-pill, .dark .segmented-control-pwa-pill { box-shadow: none; }
    .segmented-control-pwa-pill .segmented-control-option[aria-pressed=true] { color: Highlight; }
    .segmented-control-pwa-pill .segmented-control-indicator::before { border: 1px solid Highlight; }
  }
}
@media (max-width: 359px) {
  .segmented-control-mobile-line .segmented-control-option { padding-inline: 3px; }
  .segmented-control-mobile-pill .segmented-control-option { min-width: 37px; padding-inline: 5px; }
}
@media (prefers-reduced-motion: reduce) { .segmented-control-option { transition: none; } }
</style>
