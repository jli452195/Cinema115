<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import DashboardYearPicker from './DashboardYearPicker.vue'

defineProps<{ modelValue: string; recentYears: string[] }>()
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
  // 按照规范固定桌面端宽度约 320px，在窄视口下避让屏幕边缘
  const width = Math.min(320, document.documentElement.clientWidth - 24)
  const height = popover.value.offsetHeight
  const top = anchor.bottom + height + 12 <= innerHeight ? anchor.bottom + 8 : Math.max(12, anchor.top - height - 8)
  position.value = {
    left: Math.max(12, Math.min(anchor.left, document.documentElement.clientWidth - width - 12)) + 'px',
    top: top + 'px',
    width: width + 'px',
    maxHeight: Math.max(120, innerHeight - top - 12) + 'px'
  }
}

async function toggle() {
  if (open.value) {
    popover.value?.hidePopover()
    return
  }
  popover.value?.showPopover()
  open.value = true
  await picker.value?.focusSelected()
  if (popover.value?.matches(':popover-open')) {
    place()
  }
}

function changed(event: Event) {
  const isOpen = (event as ToggleEvent).newState === 'open'
  open.value = isOpen
  if (!isOpen) {
    picker.value?.resetToModel()
  }
}

function onPickerLayout() {
  if (open.value) {
    place()
  }
}

function select(value: string) {
  emit('update:modelValue', value)
  popover.value?.hidePopover()
  trigger.value?.focus({ preventScroll: true })
}

function onResize() {
  if (innerWidth < 640) {
    popover.value?.hidePopover()
  } else if (open.value) {
    place()
  }
}

function onScroll(event: Event) {
  // 允许浮层内部年份面板正常交互与滚动，不误触发关闭
  if (event.target instanceof Node && popover.value?.contains(event.target)) return
  if (open.value) popover.value?.hidePopover()
}

onMounted(() => {
  window.addEventListener('resize', onResize)
  window.addEventListener('scroll', onScroll, true)
})

onBeforeUnmount(() => {
  popover.value?.hidePopover()
  window.removeEventListener('resize', onResize)
  window.removeEventListener('scroll', onScroll, true)
})
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="catalog-chip catalog-year-trigger"
    :class="{
      'is-selected': open || (modelValue && !recentYears.includes(modelValue)),
      'is-open': open
    }"
    aria-haspopup="dialog"
    :aria-expanded="open"
    :aria-controls="id"
    @click="toggle"
  >
    {{ modelValue && !recentYears.includes(modelValue) ? modelValue + ' 年' : '更多年份' }}
    <svg
      aria-hidden="true"
      class="catalog-year-trigger-arrow"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="m7 10 5 5 5-5" />
    </svg>
  </button>
  <div
    :id="id"
    ref="popover"
    popover="auto"
    role="dialog"
    :aria-labelledby="id + '-title'"
    class="catalog-year-menu catalog-filter-theme"
    :style="position"
    @toggle="changed"
  >
    <DashboardYearPicker
      ref="picker"
      :title-id="id + '-title'"
      :model-value="modelValue"
      @update:model-value="select"
      @layout="onPickerLayout"
    />
  </div>
</template>

<style scoped>
.catalog-year-trigger-arrow {
  transition: transform 180ms ease;
}
.catalog-year-trigger.is-open .catalog-year-trigger-arrow {
  transform: rotate(180deg);
}
.catalog-year-menu {
  position: fixed;
  inset: auto;
  width: 320px;
  max-width: calc(100vw - 24px);
  margin: 0;
  padding: 0;
  overflow: auto;
  overscroll-behavior: contain;
  border-radius: 4px;
  border: 1px solid var(--filter-outline);
  background: var(--filter-surface);
  box-shadow: 0 4px 16px -2px rgba(0, 0, 0, 0.08), 0 2px 6px -1px rgba(0, 0, 0, 0.04);
  color: var(--filter-text);
}
.dark .catalog-year-menu {
  background: var(--filter-surface);
  box-shadow: 0 8px 24px -4px rgba(0, 0, 0, 0.4), 0 2px 8px -1px rgba(0, 0, 0, 0.3);
}
.catalog-year-menu:popover-open {
  animation: year-menu-enter 160ms ease-out;
}
@keyframes year-menu-enter {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
@media (prefers-reduced-motion: reduce) {
  .catalog-year-menu:popover-open {
    animation: none;
  }
  .catalog-year-trigger-arrow {
    transition: none;
  }
}
</style>
