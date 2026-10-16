<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import DashboardYearPicker from './DashboardYearPicker.vue'

const props = defineProps<{ modelValue: string; recentYears: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const id = useId()
const trigger = ref<HTMLButtonElement>()
const popover = ref<HTMLElement>()
const picker = ref<InstanceType<typeof DashboardYearPicker>>()
const open = ref(false)
const position = ref<Record<string, string>>({})
function place() {
  if (!trigger.value || !popover.value) return
  const anchor = trigger.value.getBoundingClientRect()
  const width = Math.min(340, document.documentElement.clientWidth - 24)
  const height = popover.value.offsetHeight
  const top = anchor.bottom + height + 12 <= innerHeight ? anchor.bottom + 8 : Math.max(12, anchor.top - height - 8)
  position.value = { left: Math.max(12, Math.min(anchor.left, document.documentElement.clientWidth - width - 12)) + 'px', top: top + 'px', width: width + 'px', maxHeight: Math.max(120, innerHeight - top - 12) + 'px' }
}
async function toggle() {
  if (open.value) { popover.value?.hidePopover(); return }
  popover.value?.showPopover()
  await nextTick()
  place()
  picker.value?.focusSelected()
}
function changed(event: Event) { open.value = (event as ToggleEvent).newState === 'open' }
function select(value: string) {
  emit('update:modelValue', value)
  popover.value?.hidePopover()
  trigger.value?.focus({ preventScroll: true })
}
function onResize() { if (innerWidth < 640) popover.value?.hidePopover(); else if (open.value) place() }
function onScroll(event: Event) {
  if (event.target instanceof Node && popover.value?.contains(event.target)) return
  if (open.value) popover.value?.hidePopover()
}
onMounted(() => { window.addEventListener('resize', onResize); window.addEventListener('scroll', onScroll, true) })
onBeforeUnmount(() => { popover.value?.hidePopover(); window.removeEventListener('resize', onResize); window.removeEventListener('scroll', onScroll, true) })
</script>

<template>
  <button ref="trigger" type="button" class="catalog-chip" :class="{ 'is-selected': modelValue && !recentYears.includes(modelValue) }" aria-haspopup="dialog" :aria-expanded="open" :aria-controls="id" @click="toggle">
    {{ modelValue && !recentYears.includes(modelValue) ? modelValue + ' 年' : '更多年份' }}
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m7 10 5 5 5-5" /></svg>
  </button>
  <div :id="id" ref="popover" popover="auto" role="dialog" :aria-labelledby="id + '-title'" class="catalog-year-menu catalog-filter-theme floating-surface" :style="position" @toggle="changed">
    <div class="catalog-year-menu-heading"><h2 :id="id + '-title'">选择年份</h2><span>{{ modelValue ? modelValue + ' 年' : '不限年份' }}</span></div>
    <DashboardYearPicker ref="picker" :model-value="modelValue" @update:model-value="select" />
  </div>
</template>

<style scoped>
.catalog-year-menu { position: fixed; inset: auto; width: 340px; max-width: calc(100vw - 24px); margin: 0; padding: 16px; overflow: auto; border-radius: 20px; color: var(--filter-text); }
.catalog-year-menu-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 8px; }
.catalog-year-menu-heading h2 { margin: 0; font-size: 14px; font-weight: 600; }
.catalog-year-menu-heading span { font-size: 12px; color: var(--filter-muted); }
.catalog-year-menu:popover-open { animation: year-menu-enter 160ms ease-out; }
@keyframes year-menu-enter { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .catalog-year-menu:popover-open { animation: none; } }
</style>
