<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'

const props = defineProps<{ modelValue: string; titleId?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; layout: [] }>()

const minDecade = 1900

// 年份上限随日历年份自动更新
function getMaxYear() {
  return new Date().getFullYear()
}

const maxYear = ref(getMaxYear())
const maxDecade = computed(() => Math.floor(maxYear.value / 10) * 10)

// 根据传入年份计算所在年代，越界或为空时回到上限年份所在年代
function decadeOf(value: string) {
  const num = Number(value)
  if (num >= minDecade && num <= maxYear.value) {
    return Math.floor(num / 10) * 10
  }
  return maxDecade.value
}

const activeDecade = ref(decadeOf(props.modelValue))
const grid = ref<HTMLElement>()
const resetBtn = ref<HTMLButtonElement>()

// 当前年代的完整 10 年（4 列升序排列）
const years = computed(() => {
  const start = activeDecade.value
  return Array.from({ length: 10 }, (_, index) => String(start + index))
})

const canPrevDecade = computed(() => activeDecade.value > minDecade)
const canNextDecade = computed(() => activeDecade.value < maxDecade.value)

// 判断年份是否超出边界（1900 年至当年）
function isYearDisabled(yearStr: string) {
  const num = Number(yearStr)
  return num < minDecade || num > maxYear.value
}

// 切换至上一年代并通知外层重算位置尺寸
async function prevDecade() {
  if (!canPrevDecade.value) return
  activeDecade.value = Math.max(minDecade, activeDecade.value - 10)
  await nextTick()
  emit('layout')
}

// 切换至下一年代并通知外层重算位置尺寸
async function nextDecade() {
  if (!canNextDecade.value) return
  activeDecade.value = Math.min(maxDecade.value, activeDecade.value + 10)
  await nextTick()
  emit('layout')
}

// 选择特定年份
function selectYear(value: string) {
  if (isYearDisabled(value)) return
  emit('update:modelValue', value)
}

// 选择不限年份
function selectAll() {
  emit('update:modelValue', '')
}

// 刷新边界并重置年代至已选年份
function resetToModel() {
  maxYear.value = getMaxYear()
  activeDecade.value = decadeOf(props.modelValue)
}

watch(() => props.modelValue, () => {
  resetToModel()
})

// 聚焦当前选中的年份或默认首项，供浮层打开时调用
async function focusSelected() {
  resetToModel()
  await nextTick()
  const activeCell = grid.value?.querySelector<HTMLButtonElement>('.catalog-year-cell[aria-pressed="true"]:not(:disabled)')
  if (activeCell) {
    activeCell.focus({ preventScroll: true })
    return
  }
  if (props.modelValue === '') {
    resetBtn.value?.focus({ preventScroll: true })
    return
  }
  const firstEnabled = grid.value?.querySelector<HTMLButtonElement>('.catalog-year-cell:not(:disabled)')
  firstEnabled?.focus({ preventScroll: true })
}

// 4 列键盘导航：左右步长 1，上下步长 4，Home/End 定位且不聚焦禁用项
function moveYear(event: KeyboardEvent) {
  const steps: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -4, ArrowDown: 4 }
  const buttons = [...(grid.value?.querySelectorAll<HTMLButtonElement>('.catalog-year-cell') || [])]
  const enabledButtons = buttons.filter(btn => !btn.disabled)
  if (enabledButtons.length === 0) return

  const currentTarget = event.target as HTMLButtonElement
  const index = buttons.indexOf(currentTarget)
  if (index < 0) return

  if (event.key === 'Home') {
    event.preventDefault()
    enabledButtons[0]?.focus({ preventScroll: true })
    return
  }
  if (event.key === 'End') {
    event.preventDefault()
    enabledButtons[enabledButtons.length - 1]?.focus({ preventScroll: true })
    return
  }
  if (event.key in steps) {
    event.preventDefault()
    const targetIndex = index + steps[event.key]!
    if (targetIndex >= 0 && targetIndex < buttons.length) {
      const targetBtn = buttons[targetIndex]
      if (targetBtn && !targetBtn.disabled) {
        targetBtn.focus({ preventScroll: true })
      }
    }
  }
}

defineExpose({ focusSelected, resetToModel })
onMounted(() => {
  resetToModel()
})
</script>

<template>
  <div class="catalog-year-picker">
    <!-- 顶部年代切换栏：左右双 chevron 导航，中间年代范围 -->
    <div class="catalog-year-header">
      <button
        type="button"
        class="catalog-year-nav-btn"
        :disabled="!canPrevDecade"
        aria-label="上一年代"
        @click="prevDecade"
      >
        <svg
          aria-hidden="true"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="m11 17-5-5 5-5" />
          <path d="m18 17-5-5 5-5" />
        </svg>
      </button>

      <span :id="titleId" class="catalog-year-header-label">
        {{ activeDecade }} - {{ activeDecade + 9 }}
      </span>

      <button
        type="button"
        class="catalog-year-nav-btn"
        :disabled="!canNextDecade"
        aria-label="下一年代"
        @click="nextDecade"
      >
        <svg
          aria-hidden="true"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="m6 17 5-5-5-5" />
          <path d="m13 17 5-5-5-5" />
        </svg>
      </button>
    </div>

    <!-- 4 列升序排列年份网格：无边框无色块，选中/悬停仅蓝字加粗 -->
    <div
      ref="grid"
      class="catalog-year-grid"
      role="group"
      :aria-label="activeDecade + '年代年份'"
      @keydown="moveYear"
    >
      <button
        v-for="val in years"
        :key="val"
        type="button"
        class="catalog-year-cell"
        :class="{ 'is-selected': modelValue === val }"
        :aria-pressed="modelValue === val"
        :disabled="isYearDisabled(val)"
        @click="selectYear(val)"
      >
        {{ val }}
      </button>
    </div>

    <!-- 底部低调无底色文字入口：不限年份 -->
    <div class="catalog-year-footer">
      <button
        ref="resetBtn"
        type="button"
        class="catalog-year-reset-btn"
        :class="{ 'is-selected': modelValue === '' }"
        :aria-pressed="modelValue === ''"
        @click="selectAll"
      >
        不限年份
      </button>
    </div>
  </div>
</template>
