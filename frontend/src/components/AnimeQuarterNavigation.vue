<script setup lang="ts">
import { computed } from 'vue'
import { IconCalendar, IconChevronLeft, IconChevronRight } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'

const props = defineProps<{
  modelValue: string
  options: { value: string; label: string }[]
  currentSeason: string
  providerName: string
  placeholder: string
}>()
defineEmits<{ change: [value: string] }>()
const previous = computed(() => props.options.find(option => option.value < props.modelValue))
const next = computed(() => [...props.options].reverse().find(option => option.value > props.modelValue))
</script>

<template>
  <div class="catalog-quarter-nav" role="group" :aria-label="`${providerName}季度浏览`">
    <button type="button" class="catalog-quarter-step" aria-label="上一季" :title="previous ? `切换到 ${previous.label}` : '没有更早的已收录季度'" :disabled="!previous" @click="previous && $emit('change', previous.value)"><IconChevronLeft :size="20" aria-hidden="true" /></button>
    <SelectMenu class="catalog-season" variant="anime" :model-value="modelValue" :options="options" :placeholder="placeholder" :label="`${providerName}季度`" :disabled="!options.length" @change="$emit('change', $event)">
      <template #trigger="{ label }"><span class="catalog-season-content"><IconCalendar :size="17" class="catalog-season-icon" aria-hidden="true" /><span class="catalog-season-label">{{ label }}</span><span v-if="modelValue === currentSeason" class="catalog-current-season">当季</span></span></template>
    </SelectMenu>
    <button type="button" class="catalog-quarter-step" aria-label="下一季" :title="next ? `切换到 ${next.label}` : '没有更晚的已收录季度'" :disabled="!next" @click="next && $emit('change', next.value)"><IconChevronRight :size="20" aria-hidden="true" /></button>
  </div>
</template>

<style scoped>
.catalog-quarter-nav { display: inline-flex; max-width: 100%; align-items: center; gap: 6px; }
.catalog-quarter-step, .catalog-season :deep(.select-menu-control) { min-height: 44px; border: 1px solid rgb(var(--line-default)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); box-shadow: 0 2px 0 rgb(148 163 184 / .18), 0 2px 5px rgb(15 23 42 / .025); transition: transform .2s cubic-bezier(.34, 1.56, .64, 1), background-color .15s, border-color .16s ease, box-shadow .16s ease; }
.catalog-quarter-step { display: inline-flex; flex: 0 0 44px; width: 44px; height: 44px; align-items: center; justify-content: center; }
.catalog-quarter-step:disabled { opacity: .35; box-shadow: none; cursor: default; }
.catalog-quarter-nav .catalog-season { flex: 1 1 180px; width: 180px; min-width: 0; }
.catalog-season :deep(.select-menu-control) { gap: 7px; padding: 8px 10px; font-size: 12px; }
.catalog-season-content { display: inline-flex; min-width: 0; align-items: center; gap: 7px; white-space: nowrap; }
.catalog-season-icon { flex: none; color: rgb(var(--action-link)); }
.catalog-season-label { overflow: hidden; text-overflow: ellipsis; font-size: 13px; font-weight: 650; color: rgb(var(--content-primary)); font-variant-numeric: tabular-nums; }
.catalog-current-season { flex: none; border-radius: 5px; padding: 2px 5px; background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); font-size: 11px; font-weight: 600; line-height: 16px; }
@media (hover: hover) and (pointer: fine) { .catalog-quarter-step:hover:not(:disabled), .catalog-season :deep(.select-menu-control:hover:not(:disabled)) { transform: translateY(-1px); background: rgb(var(--surface-hover)); box-shadow: 0 3px 0 rgb(148 163 184 / .14), 0 4px 8px rgb(15 23 42 / .04); } }
.catalog-quarter-step:active:not(:disabled), .catalog-season :deep(.select-menu-control:active:not(:disabled)) { transform: translateY(1px); box-shadow: none; }
.catalog-quarter-step:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.dark .catalog-quarter-step, .dark .catalog-season :deep(.select-menu-control) { box-shadow: 0 2px 0 rgb(0 0 0 / .2), 0 2px 5px rgb(0 0 0 / .08); }
.dark .catalog-quarter-step:disabled { box-shadow: none; }
@media (prefers-reduced-motion: reduce) { .catalog-quarter-step, .catalog-season :deep(.select-menu-control) { transition: none; transform: none !important; } }
</style>
