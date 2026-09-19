<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

interface Option { value: string; label: string }
const props = defineProps<{ modelValue: string; options: Option[]; label: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const root = ref<HTMLElement>(), button = ref<HTMLButtonElement>(), popup = ref<HTMLElement>()
const open = ref(false), active = ref(0)
const listID = `mikan-filter-${useId()}`
const popupStyle = ref({ left: '0px', top: '0px', width: '200px', maxHeight: '280px' })
const options = computed(() => props.options)
const label = computed(() => props.options.find(item => item.value === props.modelValue)?.label || props.options[0]?.label || '')
const activeID = computed(() => open.value && options.value[active.value] ? `${listID}-${active.value}` : undefined)

function reposition() {
  if (!open.value || !button.value) return
  const box = button.value.getBoundingClientRect()
  const dialog = button.value.closest('dialog')?.getBoundingClientRect()
  const left = Math.max(8, (dialog?.left ?? 0) + 8), right = Math.min(innerWidth - 8, (dialog?.right ?? innerWidth) - 8)
  const top = Math.max(8, (dialog?.top ?? 0) + 8), bottom = Math.min(innerHeight - 8, (dialog?.bottom ?? innerHeight) - 8)
  const width = Math.min(Math.max(box.width, 144), right - left)
  const desired = Math.min(280, Math.max(1, options.value.length) * 40 + 12)
  const below = bottom - box.bottom - 6, above = box.top - top - 6
  const upward = below < desired && above > below
  const height = Math.max(0, Math.min(desired, upward ? above : below))
  popupStyle.value = { left: `${Math.max(left, Math.min(box.left, right - width))}px`, top: `${upward ? box.top - height - 6 : box.bottom + 6}px`, width: `${width}px`, maxHeight: `${height}px` }
}
function scrollActive() {
  void nextTick(() => { if (activeID.value) document.getElementById(activeID.value)?.scrollIntoView({ block: 'nearest' }) })
}
async function show() {
  if (props.disabled || open.value) return
  open.value = true
  await nextTick()
  if (!open.value || !popup.value) return
  active.value = Math.max(0, options.value.findIndex(item => item.value === props.modelValue))
  reposition()
  // 浮层仍属于模态窗口内部，使用浏览器顶层避免被搜索结果滚动区域裁切。
  popup.value.showPopover()
  scrollActive()
}
function close(focus = false) {
  popup.value?.hidePopover()
  open.value = false
  if (focus) button.value?.focus()
}
function choose(index: number) {
  const item = options.value[index]
  if (!item || props.disabled) return
  close(true)
  if (item.value !== props.modelValue) emit('update:modelValue', item.value)
}
function keydown(event: KeyboardEvent) {
  if (event.isComposing || props.disabled) return
  switch (event.key) {
    case 'ArrowDown':
    case 'ArrowUp':
      event.preventDefault()
      if (!open.value) { void show(); break }
      if (options.value.length) active.value = (active.value + (event.key === 'ArrowDown' ? 1 : -1) + options.value.length) % options.value.length
      scrollActive()
      break
    case 'Home':
    case 'End':
      event.preventDefault()
      if (!open.value) { void show(); break }
      active.value = event.key === 'Home' ? 0 : Math.max(0, options.value.length - 1)
      scrollActive()
      break
    case 'Enter': event.preventDefault(); open.value ? choose(active.value) : void show(); break
    case ' ': event.preventDefault(); open.value ? choose(active.value) : void show(); break
    case 'Escape': if (open.value) { event.preventDefault(); event.stopPropagation(); close(true) }; break
    case 'Tab': if (open.value) close(); break
  }
}
function outside(event: PointerEvent) {
  if (open.value && event.target instanceof Node && !root.value?.contains(event.target)) close()
}
function focusout() {
  void nextTick(() => { if (open.value && !root.value?.contains(document.activeElement)) close() })
}
function scroll(event: Event) {
  if (popup.value && event.target instanceof Node && popup.value.contains(event.target)) return
  reposition()
}
watch(() => props.options, () => { active.value = Math.max(0, options.value.findIndex(item => item.value === props.modelValue)); void nextTick(reposition) })
watch(() => props.disabled, disabled => { if (disabled) close() })
onMounted(() => {
  document.addEventListener('pointerdown', outside)
  document.addEventListener('scroll', scroll, true)
  window.addEventListener('resize', reposition)
})
onBeforeUnmount(() => {
  popup.value?.hidePopover()
  document.removeEventListener('pointerdown', outside)
  document.removeEventListener('scroll', scroll, true)
  window.removeEventListener('resize', reposition)
})
</script>

<template>
  <div ref="root" class="min-w-0" @keydown="keydown" @focusout="focusout">
    <button ref="button" type="button" role="combobox" :aria-label="props.label" aria-haspopup="listbox" :aria-expanded="open" :aria-controls="open ? listID : undefined" :aria-activedescendant="activeID" :disabled="disabled" :title="label"
      class="flex h-10 w-full items-center justify-between gap-3 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 outline-none transition hover:border-orange-300 focus:border-orange-400 focus:ring-2 focus:ring-orange-100 disabled:cursor-not-allowed disabled:opacity-50" @click="open ? close() : show()">
      <span class="truncate">{{ label }}</span>
      <span aria-hidden="true" class="h-2 w-2 shrink-0 rotate-45 border-b-2 border-r-2 border-slate-400 transition-transform" :class="open ? 'rotate-[225deg] translate-y-0.5' : '-translate-y-0.5'" />
    </button>
    <div v-if="open" ref="popup" popover="manual" :style="popupStyle" class="mikan-filter-popup fixed inset-auto m-0 flex-col overflow-hidden rounded-xl border border-orange-100 bg-white p-1 text-slate-700 shadow-lg shadow-slate-900/10">
      <ul :id="listID" role="listbox" :aria-label="props.label" class="min-h-0 overflow-y-auto overscroll-contain p-0.5">
        <li v-for="(item, index) in options" :id="`${listID}-${index}`" :key="item.value" role="option" :aria-selected="item.value === modelValue" :title="item.label"
          class="flex min-h-9 cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-2 text-left text-sm transition"
          :class="item.value === modelValue || index === active ? 'bg-orange-50 text-orange-700' : 'text-slate-700 hover:bg-orange-50'" @pointerenter="active = index" @pointerdown.prevent @click="choose(index)">
          <span class="min-w-0 break-words">{{ item.label }}</span><span class="w-4 shrink-0 text-center" aria-hidden="true">{{ item.value === modelValue ? '✓' : '' }}</span>
        </li>
      </ul>
      <p v-if="!options.length" role="status" class="px-3 py-4 text-center text-xs text-slate-400">暂无选项</p>
    </div>
  </div>
</template>

<style scoped>
.mikan-filter-popup:popover-open { display: flex; }
</style>
