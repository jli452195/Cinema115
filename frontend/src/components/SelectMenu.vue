<script setup lang="ts" generic="T extends string | number = string">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useAttrs, useId, watch } from 'vue'
import type { CSSProperties } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'

interface SelectOption { value: T; label: string; disabled?: boolean }

defineOptions({ inheritAttrs: false })
const props = defineProps<{ modelValue: T; options: SelectOption[]; disabled?: boolean; label: string; placeholder?: string; variant?: 'anime'; density?: 'compact'; menuMinWidth?: number; required?: boolean; name?: string; form?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: T]; change: [value: T] }>()
const attrs = useAttrs()
const controlAttrs = () => Object.fromEntries(Object.entries(attrs).filter(([key]) => key !== 'class' && key !== 'style'))
const root = ref<HTMLElement>(), button = ref<HTMLButtonElement>()
const nativeControl = ref<HTMLSelectElement>()
const panel = ref<HTMLElement>(), panelStyle = ref<Record<string, string>>({})
const open = ref(false), activeIndex = ref(0)
const inheritedDisabled = ref(false)
const unavailable = computed(() => props.disabled || inheritedDisabled.value)
const listboxID = `select-menu-${useId()}`
const selectedIndex = computed(() => props.options.findIndex(option => option.value === props.modelValue && !option.disabled))
const selectedLabel = computed(() => props.options.find(option => option.value === props.modelValue)?.label ?? props.placeholder ?? props.options[0]?.label ?? '')
let fieldsetObserver: MutationObserver | undefined
let searchText = '', searchTime = 0

function syncDisabled() {
  inheritedDisabled.value = !!nativeControl.value?.matches(':disabled')
  if (unavailable.value) close()
}
function enabledIndex(start: number, step: number) {
  for (let offset = 0; offset < props.options.length; offset++) {
    const index = (start + offset * step + props.options.length) % props.options.length
    if (!props.options[index]?.disabled) return index
  }
  return -1
}

async function show() {
  if (unavailable.value || button.value?.matches(':disabled') || !props.options.length) return
  activeIndex.value = selectedIndex.value >= 0 ? selectedIndex.value : enabledIndex(0, 1)
  open.value = true
  await nextTick()
  if (!open.value || !panel.value || !button.value) return
  positionPanel()
  panel.value.showPopover()
}
function positionPanel() {
  if (!panel.value || !button.value) return
  const rect = button.value.getBoundingClientRect(), gap = 6, inset = 8
  const width = Math.min(Math.max(rect.width, props.menuMinWidth || 160), innerWidth - inset * 2)
  const below = innerHeight - rect.bottom - gap - inset, above = rect.top - gap - inset
  const upward = below < Math.min(props.options.length * 44 + 8, 200) && above > below
  const maxHeight = Math.max(44, Math.min(264, upward ? above : below))
  panelStyle.value = { width: `${width}px`, maxHeight: `${maxHeight}px`, left: `${Math.max(inset, Math.min(rect.left, innerWidth - width - inset))}px`, top: upward ? 'auto' : `${rect.bottom + gap}px`, bottom: upward ? `${innerHeight - rect.top + gap}px` : 'auto' }
}
function close(focus = false) {
  if (panel.value?.matches(':popover-open')) panel.value.hidePopover()
  open.value = false
  if (focus) void nextTick(() => button.value?.focus({ preventScroll: true }))
}
function toggle() { open.value ? close() : show() }
function choose(index: number) {
  const option = props.options[index]
  if (!option || option.disabled || unavailable.value || button.value?.matches(':disabled')) return
  emit('update:modelValue', option.value)
  emit('change', option.value)
  close(true)
}
function move(step: number) {
  if (!props.options.length) return
  if (!open.value) { void show(); return }
  activeIndex.value = enabledIndex(activeIndex.value + step, step)
}
function keydown(event: KeyboardEvent) {
  if (unavailable.value || button.value?.matches(':disabled')) return
  switch (event.key) {
    case 'ArrowDown': event.preventDefault(); move(1); break
    case 'ArrowUp': event.preventDefault(); move(-1); break
    case 'Home': event.preventDefault(); show(); activeIndex.value = enabledIndex(0, 1); break
    case 'End': event.preventDefault(); show(); activeIndex.value = enabledIndex(props.options.length - 1, -1); break
    case 'Enter':
    case ' ': event.preventDefault(); open.value ? choose(activeIndex.value) : show(); break
    case 'Escape': if (open.value) { event.preventDefault(); event.stopPropagation(); close(true) }; break
    case 'Tab': close(); break
    default:
      if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return
      event.preventDefault()
      if (!open.value) void show()
      searchText = Date.now() - searchTime > 700 ? event.key : searchText + event.key
      searchTime = Date.now()
      const query = searchText.toLocaleLowerCase()
      const start = activeIndex.value + 1
      for (let offset = 0; offset < props.options.length; offset++) {
        const index = (start + offset) % props.options.length, option = props.options[index]
        if (option && !option.disabled && option.label.toLocaleLowerCase().startsWith(query)) { activeIndex.value = index; break }
      }
  }
}
function invalid(event: Event) {
  event.preventDefault()
  button.value?.focus()
  void show()
}
function outside(event: PointerEvent) {
  if (open.value && event.target instanceof Node && !root.value?.contains(event.target)) close()
}
function viewportChanged(event: Event) {
  if (open.value && !(event.target instanceof Node && panel.value?.contains(event.target))) positionPanel()
}

watch(() => props.modelValue, () => { activeIndex.value = selectedIndex.value })
watch(() => props.disabled, async () => { await nextTick(); syncDisabled() })
watch(() => JSON.stringify(props.options), () => { activeIndex.value = selectedIndex.value; close() })
watch(activeIndex, async index => {
  await nextTick()
  const item = panel.value?.children[index] as HTMLElement | undefined
  if (!open.value || !item || !panel.value) return
  const top = item.offsetTop, bottom = top + item.offsetHeight
  if (top < panel.value.scrollTop) panel.value.scrollTop = top
  else if (bottom > panel.value.scrollTop + panel.value.clientHeight) panel.value.scrollTop = bottom - panel.value.clientHeight
})
onMounted(() => {
  syncDisabled()
  fieldsetObserver = new MutationObserver(syncDisabled)
  let ancestor = root.value?.parentElement
  while (ancestor) {
    if (ancestor instanceof HTMLFieldSetElement) fieldsetObserver.observe(ancestor, { attributes: true, attributeFilter: ['disabled'] })
    ancestor = ancestor.parentElement
  }
  document.addEventListener('pointerdown', outside); document.addEventListener('scroll', viewportChanged, true); window.addEventListener('resize', viewportChanged)
})
onBeforeUnmount(() => { fieldsetObserver?.disconnect(); document.removeEventListener('pointerdown', outside); document.removeEventListener('scroll', viewportChanged, true); window.removeEventListener('resize', viewportChanged) })
</script>

<template>
  <div ref="root" class="select-menu relative inline-block min-w-0 max-w-full text-left" :class="attrs.class" :style="attrs.style as CSSProperties">
    <!-- 原生控件仅负责表单校验与提交，交互及外观统一由公共下拉实现。 -->
    <select ref="nativeControl" class="select-menu-native" tabindex="-1" aria-hidden="true" :value="String(modelValue)" :required="required" :name="name" :form="form" :disabled="disabled" @invalid="invalid">
      <option v-if="!options.some(option => option.value === modelValue)" value="" />
      <option v-for="option in options" :key="option.value" :value="String(option.value)" :disabled="option.disabled">{{ option.label }}</option>
    </select>
    <button
      v-bind="controlAttrs()"
      ref="button"
      type="button"
      role="combobox"
      :disabled="unavailable"
      :form="form"
      :data-value="modelValue"
      :aria-required="required || undefined"
      :aria-label="label"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listboxID"
      :aria-activedescendant="open && activeIndex >= 0 ? `${listboxID}-${activeIndex}` : undefined"
      class="select-menu-control"
      :class="{ 'select-menu-trigger': variant === 'anime', 'select-menu-compact': density === 'compact' }"
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
      <li v-for="(option, index) in options" :id="`${listboxID}-${index}`" :key="option.value" role="option" :data-value="option.value" :aria-disabled="option.disabled || undefined" :aria-selected="option.value === modelValue">
        <button
          type="button"
          tabindex="-1"
          :disabled="option.disabled || unavailable"
          class="select-menu-option"
          :class="{ active: index === activeIndex, selected: option.value === modelValue }"
          @pointerenter="!option.disabled && (activeIndex = index)"
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
.select-menu-native { position: absolute; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
.select-menu-control { display: flex; width: 100%; min-width: 0; min-height: 44px; align-items: center; justify-content: space-between; gap: 7px; padding: 8px 10px; border: 1px solid rgb(var(--line-default)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; line-height: 1.5; text-align: left; cursor: pointer; box-shadow: 0 2px 0 rgb(148 163 184 / .18), 0 2px 5px rgb(15 23 42 / .025); transition: transform .2s cubic-bezier(.34, 1.56, .64, 1), background-color .15s, border-color .16s ease, box-shadow .16s ease; }
.select-menu-trigger { font-size: 12px; }
.select-menu-compact { min-height: 36px; padding: 6px 10px; gap: 8px; font-size: 13px; }
.dark .select-menu-control { box-shadow: 0 2px 0 rgb(0 0 0 / .2), 0 2px 5px rgb(0 0 0 / .08); }
@media (hover: hover) and (pointer: fine) { .select-menu-control:hover:not(:disabled) { transform: translateY(-1px); box-shadow: 0 3px 0 rgb(148 163 184 / .14), 0 4px 8px rgb(15 23 42 / .04); } }
.select-menu-control:active:not(:disabled) { transform: translateY(1px); box-shadow: none; }
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
.select-menu-option:disabled { opacity: .45; cursor: not-allowed; }
.select-menu-label { min-width: 0; white-space: normal; overflow-wrap: anywhere; }
@media (max-width: 639px) { .select-menu-control, .select-menu-option { min-height: 44px; } }
@media (max-width: 639px), (pointer: coarse) { .select-menu-compact { min-height: 44px; padding: 8px 10px; font-size: 16px; } }
@media (prefers-reduced-motion: reduce) { .select-menu-control, .select-menu-option, .transition-transform { transition: none; } .select-menu-control { transform: none !important; } }
</style>
