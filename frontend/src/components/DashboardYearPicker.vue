<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const currentYear = new Date().getFullYear()
const decades = Array.from({ length: Math.floor(currentYear / 10) - 189 }, (_, index) => Math.floor(currentYear / 10) * 10 - index * 10)
function decadeOf(value: string) { return Math.floor((Number(value) || currentYear) / 10) * 10 }
const activeDecade = ref(decadeOf(props.modelValue))
const decadeStrip = ref<HTMLElement>()
const grid = ref<HTMLElement>()
const years = computed(() => Array.from({ length: Math.min(currentYear, activeDecade.value + 9) - activeDecade.value + 1 }, (_, index) => String(Math.min(currentYear, activeDecade.value + 9) - index)))
function revealDecade() {
  const strip = decadeStrip.value
  const active = strip?.querySelector<HTMLElement>('[aria-pressed="true"]')
  if (strip && active) strip.scrollLeft = active.offsetLeft - strip.offsetLeft - (strip.clientWidth - active.clientWidth) / 2
}
watch(() => props.modelValue, value => { activeDecade.value = decadeOf(value); void nextTick(revealDecade) })
function focusSelected() {
  revealDecade()
  const target = grid.value?.querySelector<HTMLButtonElement>('[aria-pressed="true"]') || grid.value?.querySelector<HTMLButtonElement>('button')
  target?.focus({ preventScroll: true })
}
function moveYear(event: KeyboardEvent) {
  const steps: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -5, ArrowDown: 5 }
  const buttons = [...(grid.value?.querySelectorAll<HTMLButtonElement>('button') || [])]
  const index = buttons.indexOf(event.target as HTMLButtonElement)
  if (index < 0 || !(event.key in steps || event.key === 'Home' || event.key === 'End')) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : Math.max(0, Math.min(buttons.length - 1, index + steps[event.key]!))
  buttons[next]?.focus()
}
defineExpose({ focusSelected })
onMounted(revealDecade)
</script>

<template>
  <div class="catalog-year-picker">
    <nav ref="decadeStrip" class="catalog-decade-strip" aria-label="定位年代">
      <button v-for="decade in decades" :key="decade" type="button" class="catalog-chip" :aria-pressed="activeDecade === decade" @click="activeDecade = decade">{{ decade }}年代</button>
    </nav>
    <div ref="grid" class="catalog-year-grid" role="group" :aria-label="activeDecade + '年代年份'" @keydown="moveYear">
      <button v-for="value in years" :key="value" type="button" class="catalog-chip" :aria-pressed="modelValue === value" @click="emit('update:modelValue', value)">{{ value }}</button>
    </div>
    <button type="button" class="catalog-chip catalog-year-reset" :aria-pressed="modelValue === ''" @click="emit('update:modelValue', '')">不限年份</button>
  </div>
</template>
