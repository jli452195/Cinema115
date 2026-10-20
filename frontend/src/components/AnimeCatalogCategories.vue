<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

const props = defineProps<{
  entries: { value: string; label: string; count?: number; date?: string }[]
  modelValue: string
  label: string
  today?: string
}>()
defineEmits<{ select: [value: string] }>()
const root = ref<HTMLElement>(), id = useId()
let observer: ResizeObserver | undefined
function revealSelected() {
  const el = root.value, selected = el?.querySelector<HTMLElement>('[aria-pressed="true"]')
  if (!el || !selected) return
  const left = selected.offsetLeft - 4, right = selected.offsetLeft + selected.offsetWidth + 4
  if (left < el.scrollLeft) el.scrollLeft = left
  else if (right > el.scrollLeft + el.clientWidth) el.scrollLeft = right - el.clientWidth
}
watch([() => props.modelValue, () => props.entries, () => props.label], async () => { await nextTick(); revealSelected() })
onMounted(() => { observer = new ResizeObserver(revealSelected); if (root.value) observer.observe(root.value); revealSelected() })
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div ref="root" class="catalog-days" role="group" :aria-label="label">
    <button
      v-for="entry in entries"
      :key="entry.value"
      type="button"
      class="catalog-day-card"
      :class="{ 'catalog-day-overview': entry.value === 'all', 'catalog-day-extra': Number(entry.value) > 7 }"
      :aria-pressed="modelValue === entry.value"
      :aria-label="`${entry.label}${today === entry.value ? '，今天' : ''}`"
      :aria-describedby="`${id}-${entry.value}`"
      :data-date="entry.date"
      @click="$emit('select', entry.value)"
    >
      <span v-if="today === entry.value" class="catalog-day-today" aria-hidden="true">今天</span>
      <span class="catalog-day-number" aria-hidden="true"><template v-if="entry.date">{{ Number(entry.date.slice(-2)) }}</template><template v-else-if="entry.count !== undefined">{{ entry.count }}</template><span v-else class="catalog-day-placeholder"></span></span>
      <span class="catalog-day-meta" aria-hidden="true"><span class="catalog-day-label">{{ entry.value === 'all' ? '本季' : entry.label }}</span><span class="catalog-day-unit"><template v-if="!entry.date">部收录</template><template v-else-if="entry.count !== undefined">{{ entry.count }} 部</template><span v-else class="catalog-day-placeholder catalog-day-count-placeholder"></span></span></span>
      <span :id="`${id}-${entry.value}`" class="sr-only">{{ entry.date ? `${entry.date}，` : '' }}{{ entry.count === undefined ? '收录数量待更新' : `${entry.count} 部收录` }}</span>
    </button>
  </div>
</template>

<style scoped>
.catalog-days { position: relative; display: flex; gap: 8px; overflow-x: auto; padding: 12px 4px 10px; margin-inline: -4px; scrollbar-width: thin; scroll-padding-inline: 4px; }
.catalog-day-card { --catalog-lift: 0px; position: relative; display: inline-flex; flex: 1 0 100px; height: 60px; align-items: center; gap: 8px; padding: 0 10px; border: 1px solid rgb(148 163 184 / .13); border-radius: 12px; background: rgb(148 163 184 / .035); text-align: left; color: rgb(var(--color-slate-700)); transform: translateY(var(--catalog-lift)); transition: transform .25s cubic-bezier(.34, 1.56, .64, 1), background-color .16s ease, border-color .16s ease, box-shadow .16s ease; touch-action: manipulation; }
.catalog-day-overview, .catalog-day-extra { flex-basis: 112px; }
.catalog-day-card::before { content: ''; position: absolute; top: -2px; left: 50%; width: 30px; height: 4px; border-radius: 2px; transform: translateX(-50%) rotate(-3deg); background: repeating-linear-gradient(-55deg, #60a5fa 0 2px, #dbeafe 2px 4px); opacity: 0; pointer-events: none; }
.catalog-day-card[aria-pressed="true"] { border-color: rgb(148 163 184 / .3); background: #fff; box-shadow: 0 3px 9px rgb(15 23 42 / .07), 0 1px 2px rgb(15 23 42 / .04); animation: catalog-day-settle .28s cubic-bezier(.2, .7, .2, 1); }
.catalog-day-card[aria-pressed="true"]::before { opacity: 1; animation: catalog-tape-in .3s cubic-bezier(.2, .7, .2, 1); }
.catalog-day-number { display: inline-flex; flex: none; align-items: center; min-width: 2ch; height: 28px; color: rgb(var(--color-slate-800)); font-size: 23px; font-weight: 700; font-variant-numeric: tabular-nums; letter-spacing: -.6px; line-height: 1; transform-origin: left center; transition: transform .25s cubic-bezier(.34, 1.56, .64, 1), color .16s ease; }
.catalog-day-card[aria-pressed="true"] .catalog-day-number { color: #2563eb; }
.catalog-day-meta { display: flex; flex: none; flex-direction: column; gap: 3px; white-space: nowrap; }
.catalog-day-label { font-size: 12px; font-weight: 600; line-height: 16px; }
.catalog-day-unit { display: flex; align-items: center; height: 14px; color: rgb(var(--color-slate-500)); font-size: 11px; line-height: 14px; }
.catalog-day-placeholder { width: 24px; height: 18px; border-radius: 4px; background: rgb(148 163 184 / .18); }
.catalog-day-count-placeholder { display: inline-block; width: 24px; height: 10px; }
.catalog-day-today { position: absolute; top: -9px; right: 5px; padding: 1px 5px; border: 2px solid rgb(var(--color-slate-50)); border-radius: 7px; background: #3b82f6; color: #fff; font-size: 10px; font-weight: 600; line-height: 14px; transform: rotate(4deg); pointer-events: none; }
.catalog-day-card[aria-pressed="true"] .catalog-day-today { animation: catalog-today-settle .45s cubic-bezier(.34, 1.56, .64, 1); }
.catalog-day-card:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.dark .catalog-day-card { border-color: rgb(148 163 184 / .12); background: rgb(148 163 184 / .035); color: rgb(var(--color-slate-200)); }
.dark .catalog-day-card[aria-pressed="true"] { border-color: rgb(148 163 184 / .3); background: rgb(var(--color-slate-800)); box-shadow: 0 3px 9px rgb(0 0 0 / .16); }
.dark .catalog-day-number { color: rgb(var(--color-slate-200)); }
.dark .catalog-day-card[aria-pressed="true"] .catalog-day-number { color: #93c5fd; }
.dark .catalog-day-unit { color: rgb(var(--color-slate-400)); }
.dark .catalog-day-today { border-color: rgb(var(--color-slate-900)); }
.dark .catalog-day-card::before { background: repeating-linear-gradient(-55deg, #60a5fa 0 2px, #334155 2px 4px); }
@keyframes catalog-day-settle { from { transform: translateY(calc(var(--catalog-lift) + 2px)) scale(.98); } 65% { transform: translateY(calc(var(--catalog-lift) - 1px)) scale(1.01); } to { transform: translateY(var(--catalog-lift)); } }
@keyframes catalog-tape-in { from { opacity: 0; transform: translateX(-50%) rotate(-7deg) scaleX(.55); } to { opacity: 1; transform: translateX(-50%) rotate(-3deg) scaleX(1); } }
@keyframes catalog-today-settle { from { transform: rotate(-8deg) scale(.92); } 65% { transform: rotate(8deg) scale(1.03); } to { transform: rotate(4deg); } }
@media (hover: hover) and (pointer: fine) { .catalog-day-card:hover { --catalog-lift: -2px; border-color: rgb(148 163 184 / .3); background: #fff; box-shadow: 0 2px 7px rgb(15 23 42 / .05); } .catalog-day-card:hover .catalog-day-number { transform: scale(1.06); } .dark .catalog-day-card:hover { background: rgb(var(--color-slate-800)); box-shadow: 0 2px 7px rgb(0 0 0 / .12); } }
.catalog-day-card:active { transform: translateY(0) scale(.98); }
@media (max-width: 639px) { .catalog-day-card { flex: 0 0 104px; } .catalog-day-overview, .catalog-day-extra { flex-basis: 112px; } }
@media (prefers-reduced-motion: reduce) { .catalog-day-card, .catalog-day-card::before, .catalog-day-number, .catalog-day-today { transition: none; animation: none !important; } .catalog-day-card:hover, .catalog-day-card:active, .catalog-day-card:hover .catalog-day-number { --catalog-lift: 0px; transform: none; } }
</style>
