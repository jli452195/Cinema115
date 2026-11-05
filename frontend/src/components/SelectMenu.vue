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
  <div ref="root" class="relative inline-block min-w-[7.5rem] max-w-full text-left">
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
      class="select-menu-control"
      :class="{ 'select-menu-trigger': variant === 'anime' }"
      @click="toggle"
      @keydown="keydown"
    >
      <slot name="trigger" :label="selectedLabel"><span class="select-menu-label">{{ selectedLabel }}</span></slot>
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
      :class="{ 'select-menu-anime': variant === 'anime' }"
    >
      <li v-for="(option, index) in options" :id="`${listboxID}-${index}`" :key="option.value" role="option" :aria-selected="option.value === modelValue">
        <button
          type="button"
          tabindex="-1"
          class="select-menu-option"
          :class="{ active: index === activeIndex, selected: option.value === modelValue }"
          @pointerenter="activeIndex = index"
          @pointerdown.prevent
          @click="choose(index)"
        >
          <div v-if="variant !== 'anime'" class="flex w-3 shrink-0 items-center justify-center">
            <span v-if="option.value === modelValue" class="h-1.5 w-1.5 rounded-full bg-selection-content" aria-hidden="true" />
          </div>
          <span class="select-menu-label">{{ option.label }}</span>
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.select-menu-control { display: flex; width: 100%; min-width: 0; min-height: 40px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; line-height: 1.5; text-align: left; cursor: pointer; transition: background-color .15s, border-color .15s, color .15s; }
.select-menu-trigger { min-height: 44px; gap: 16px; font-size: 12px; }
.select-menu-control:hover:not(:disabled) { background: rgb(var(--surface-hover)); }
.select-menu-control[aria-expanded=true], .select-menu-control:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.select-menu-control:disabled { opacity: .5; cursor: not-allowed; }
.select-menu-panel { position: fixed; inset: auto; margin: 0; padding: 4px; border: 1px solid rgb(var(--line-default)); border-radius: 12px; background: rgb(var(--surface-elevated)); color: rgb(var(--content-primary)); box-shadow: 0 8px 24px rgb(0 0 0 / .16); overflow: auto; overscroll-behavior: contain; }
.dark .select-menu-panel { box-shadow: 0 8px 24px rgb(0 0 0 / .32); }
.select-menu-panel::backdrop { background: transparent; }
.select-menu-anime { padding: 5px; }
.select-menu-option { display: flex; align-items: center; gap: 10px; width: 100%; min-height: 40px; padding: 8px 12px; border-radius: 8px; background: transparent; text-align: left; color: rgb(var(--content-secondary)); font-size: 14px; line-height: 1.6; cursor: pointer; transition: background-color .15s, color .15s; }
.select-menu-anime .select-menu-option { font-size: 13px; }
.select-menu-option.active { background: rgb(var(--surface-hover)); color: rgb(var(--content-primary)); }
.select-menu-option.selected { background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); font-weight: 600; }
.select-menu-label { min-width: 0; white-space: normal; overflow-wrap: anywhere; }
@media (max-width: 639px) { .select-menu-control, .select-menu-option { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .select-menu-control, .select-menu-option, .transition-transform { transition: none; } }
</style>
