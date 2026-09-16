<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

interface SelectOption { value: string; label: string }

const props = defineProps<{ modelValue: string; options: SelectOption[]; disabled?: boolean; label: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; change: [value: string] }>()
const root = ref<HTMLElement>(), button = ref<HTMLButtonElement>()
const open = ref(false), activeIndex = ref(0)
const listboxID = `select-menu-${useId()}`
const selectedIndex = computed(() => Math.max(0, props.options.findIndex(option => option.value === props.modelValue)))
const selectedLabel = computed(() => props.options[selectedIndex.value]?.label ?? '')

function show() {
  if (props.disabled) return
  activeIndex.value = selectedIndex.value
  open.value = true
}
function close(focus = false) {
  open.value = false
  if (focus) void nextTick(() => button.value?.focus())
}
function toggle() { open.value ? close() : show() }
function choose(index: number) {
  const option = props.options[index]
  if (!option) return
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
    case 'Escape': if (open.value) { event.preventDefault(); close(true) }; break
    case 'Tab': close(); break
  }
}
function outside(event: PointerEvent) {
  if (open.value && event.target instanceof Node && !root.value?.contains(event.target)) close()
}

watch(() => props.modelValue, () => { activeIndex.value = selectedIndex.value })
watch(() => props.disabled, disabled => { if (disabled) close() })
onMounted(() => document.addEventListener('pointerdown', outside))
onBeforeUnmount(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="relative inline-block min-w-[7.5rem] text-left">
    <button
      ref="button"
      type="button"
      :disabled="disabled"
      :aria-label="label"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listboxID"
      :aria-activedescendant="open ? `${listboxID}-${activeIndex}` : undefined"
      class="flex h-10 w-full items-center justify-between gap-3 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 outline-none transition hover:border-slate-300 hover:bg-slate-50 focus:border-slate-400 focus:ring-2 focus:ring-slate-200 disabled:cursor-not-allowed disabled:opacity-50"
      @click="toggle"
      @keydown="keydown"
    >
      <span class="truncate">{{ selectedLabel }}</span>
      <span class="h-2 w-2 shrink-0 rotate-45 border-b-2 border-r-2 border-slate-400 transition-transform" :class="open ? 'rotate-[225deg] translate-y-0.5' : '-translate-y-0.5'" aria-hidden="true" />
    </button>
    <ul
      v-if="open"
      :id="listboxID"
      role="listbox"
      :aria-label="label"
      class="absolute left-0 z-50 mt-1.5 max-h-64 min-w-full max-w-[calc(100vw-2rem)] overflow-auto rounded-lg border border-slate-200 bg-white p-1 shadow-lg shadow-slate-900/10"
    >
      <li v-for="(option, index) in options" :id="`${listboxID}-${index}`" :key="option.value" role="option" :aria-selected="option.value === modelValue">
        <button
          type="button"
          tabindex="-1"
          class="flex w-full items-center justify-between gap-6 rounded-md px-3 py-2 text-left text-sm transition"
          :class="option.value === modelValue ? 'bg-slate-900 text-white' : index === activeIndex ? 'bg-slate-100 text-slate-900' : 'text-slate-700 hover:bg-slate-50'"
          @pointerenter="activeIndex = index"
          @click="choose(index)"
        >
          <span class="whitespace-nowrap">{{ option.label }}</span>
          <span v-if="option.value === modelValue" class="h-1.5 w-1.5 shrink-0 rounded-full bg-white" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </div>
</template>
