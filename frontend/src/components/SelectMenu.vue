<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'

interface SelectOption { value: string; label: string }

const props = defineProps<{ modelValue: string; options: SelectOption[]; disabled?: boolean; label: string; placeholder?: string; variant?: 'anime'; menuMinWidth?: number }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; change: [value: string] }>()
const root = ref<HTMLElement>(), button = ref<HTMLButtonElement>()
const panel = ref<HTMLElement>(), panelStyle = ref<Record<string, string>>({})
const open = ref(false), activeIndex = ref(0)
const listboxID = `select-menu-${useId()}`
const selectedIndex = computed(() => Math.max(0, props.options.findIndex(option => option.value === props.modelValue)))
const selectedLabel = computed(() => props.options.find(option => option.value === props.modelValue)?.label ?? props.placeholder ?? props.options[0]?.label ?? '')

async function show() {
  if (props.disabled || !props.options.length) return
  activeIndex.value = selectedIndex.value
  open.value = true
  await nextTick()
  if (!open.value || !panel.value || !button.value) return
  const rect = button.value.getBoundingClientRect(), gap = 6, inset = 8
  const width = Math.min(Math.max(rect.width, props.menuMinWidth || 160), innerWidth - inset * 2)
  const below = innerHeight - rect.bottom - gap - inset, above = rect.top - gap - inset
  const upward = below < Math.min(props.options.length * 44 + 8, 200) && above > below
  const maxHeight = Math.max(44, Math.min(264, upward ? above : below))
  panelStyle.value = { width: `${width}px`, maxHeight: `${maxHeight}px`, left: `${Math.max(inset, Math.min(rect.left, innerWidth - width - inset))}px`, top: upward ? 'auto' : `${rect.bottom + gap}px`, bottom: upward ? `${innerHeight - rect.top + gap}px` : 'auto' }
  panel.value.showPopover()
}
function close(focus = false) {
  if (panel.value?.matches(':popover-open')) panel.value.hidePopover()
  open.value = false
  if (focus) void nextTick(() => button.value?.focus({ preventScroll: true }))
}
function toggle() { open.value ? close() : show() }
function choose(index: number) {
  const option = props.options[index]
  if (!option || props.disabled) return
  emit('update:modelValue', option.value)
  emit('change', option.value)
  close(true)
}
function move(step: number) {
  if (!props.options.length) return
  if (!open.value) show()
  activeIndex.value = (activeIndex.value + step + props.options.length) % props.options.length
}
function keydown(event: KeyboardEvent) {
  switch (event.key) {
    case 'ArrowDown': event.preventDefault(); move(1); break
    case 'ArrowUp': event.preventDefault(); move(-1); break
    case 'Home': event.preventDefault(); show(); activeIndex.value = 0; break
    case 'End': event.preventDefault(); show(); activeIndex.value = Math.max(0, props.options.length - 1); break
    case 'Enter':
    case ' ': event.preventDefault(); open.value ? choose(activeIndex.value) : show(); break
    case 'Escape': if (open.value) { event.preventDefault(); event.stopPropagation(); close(true) }; break
    case 'Tab': close(); break
  }
}
function outside(event: PointerEvent) {
  if (open.value && event.target instanceof Node && !root.value?.contains(event.target)) close()
}
function viewportChanged(event: Event) {
  if (open.value && !(event.target instanceof Node && panel.value?.contains(event.target))) close()
}

watch(() => props.modelValue, () => { activeIndex.value = selectedIndex.value })
watch(() => props.disabled, disabled => { if (disabled) close() })
watch(() => props.options, () => { activeIndex.value = selectedIndex.value; close() })
watch(activeIndex, async index => {
  await nextTick()
  const item = panel.value?.children[index] as HTMLElement | undefined
  if (!open.value || !item || !panel.value) return
  const top = item.offsetTop, bottom = top + item.offsetHeight
  if (top < panel.value.scrollTop) panel.value.scrollTop = top
  else if (bottom > panel.value.scrollTop + panel.value.clientHeight) panel.value.scrollTop = bottom - panel.value.clientHeight
})
onMounted(() => { document.addEventListener('pointerdown', outside); document.addEventListener('scroll', viewportChanged, true); window.addEventListener('resize', viewportChanged) })
onBeforeUnmount(() => { document.removeEventListener('pointerdown', outside); document.removeEventListener('scroll', viewportChanged, true); window.removeEventListener('resize', viewportChanged) })
</script>

<template>
  <div ref="root" class="relative inline-block min-w-[7.5rem] text-left">
    <button
      ref="button"
      type="button"
      role="combobox"
      :disabled="disabled"
      :aria-label="label"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listboxID"
      :aria-activedescendant="open ? `${listboxID}-${activeIndex}` : undefined"
      :class="variant === 'anime' ? 'select-menu-trigger' : 'flex h-10 w-full items-center justify-between gap-3 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 text-sm text-slate-700 dark:text-slate-300 outline-none transition hover:border-slate-300 dark:hover:border-slate-600 hover:bg-slate-50 dark:hover:bg-slate-800 focus:border-slate-400 focus:ring-2 focus:ring-slate-200 dark:focus:ring-slate-700 disabled:cursor-not-allowed disabled:opacity-50'"
      @click="toggle"
      @keydown="keydown"
    >
      <slot name="trigger" :label="selectedLabel"><span class="truncate">{{ selectedLabel }}</span></slot>
      <IconChevronDown :size="16" class="shrink-0 transition-transform" :class="{ 'rotate-180': open }" aria-hidden="true" />
    </button>
    <ul
      v-if="open"
      ref="panel"
      :id="listboxID"
      popover="manual"
      role="listbox"
      :aria-label="label"
      :style="panelStyle"
      class="select-menu-panel"
      :class="variant === 'anime' ? 'select-menu-anime' : 'rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-1 shadow-lg shadow-slate-900/10'"
    >
      <li v-for="(option, index) in options" :id="`${listboxID}-${index}`" :key="option.value" role="option" :aria-selected="option.value === modelValue">
        <button
          type="button"
          tabindex="-1"
          :class="variant === 'anime' ? ['select-menu-option', { active: index === activeIndex, selected: option.value === modelValue }] : ['flex w-full items-center gap-2.5 rounded-md px-2 py-2 text-left text-sm transition-colors',
            index === activeIndex ? 'bg-slate-100 dark:bg-slate-800/80' : 'bg-transparent',
            option.value === modelValue ? 'font-medium text-slate-900 dark:text-white' : 'text-slate-600 dark:text-slate-400'
          ]"
          @pointerenter="activeIndex = index"
          @pointerdown.prevent
          @click="choose(index)"
        >
          <div v-if="variant !== 'anime'" class="flex w-3 shrink-0 items-center justify-center">
            <span v-if="option.value === modelValue" class="h-1.5 w-1.5 rounded-full bg-amber-500 shadow-[0_0_4px_rgba(245,158,11,0.5)] dark:bg-amber-400 dark:shadow-[0_0_6px_rgba(251,191,36,0.5)]" aria-hidden="true" />
          </div>
          <span :class="variant === 'anime' ? 'select-menu-label' : 'whitespace-nowrap'">{{ option.label }}</span>
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.select-menu-trigger { display: flex; width: 100%; min-width: 0; min-height: 44px; align-items: center; justify-content: space-between; gap: 16px; padding: 8px 12px; border: 1px solid rgb(148 163 184 / .16); border-radius: 10px; background: var(--ae-surface, #fff); color: var(--ae-ink, #1e293b); font-size: 12px; cursor: pointer; }
.select-menu-trigger:hover:not(:disabled) { border-color: rgb(96 165 250 / .55); }
.select-menu-trigger[aria-expanded=true], .select-menu-trigger:focus-visible { outline: 2px solid var(--ae-blue, #2563eb); outline-offset: 2px; }
.select-menu-trigger:disabled { opacity: .45; cursor: default; }
.select-menu-panel { position: fixed; inset: auto; margin: 0; overflow: auto; overscroll-behavior: contain; }
.select-menu-panel::backdrop { background: transparent; }
.select-menu-anime { padding: 5px; border: 0; border-radius: 12px; background: var(--ae-surface, #fff); color: var(--ae-ink, #1e293b); box-shadow: 0 6px 24px rgb(15 23 42 / .15), 0 0 0 1px rgb(148 163 184 / .08); }
.select-menu-option { display: flex; align-items: center; width: 100%; min-height: 40px; padding: 8px 12px; border-radius: 8px; text-align: left; color: var(--ae-muted, #475569); font-size: 13px; line-height: 1.6; cursor: pointer; }
.select-menu-option.active { background: var(--ae-inset, #edf1f6); }
.select-menu-option.selected { color: var(--ae-blue, #2563eb); font-weight: 600; }
.select-menu-label { min-width: 0; white-space: normal; overflow-wrap: anywhere; }
@media (max-width: 639px) { .select-menu-option { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .transition-transform { transition: none; } }
</style>
