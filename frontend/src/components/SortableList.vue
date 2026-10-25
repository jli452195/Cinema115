<script lang="ts">
// Only a live drag is shared between lists; no business data is retained.
const draggedItems = new WeakMap<HTMLElement, unknown>()
</script>

<script setup lang="ts" generic="T extends object">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useDraggable, type DraggableEvent, type UseDraggableOptions } from 'vue-draggable-plus'

// Render content in #item and mark a grip or the whole item with data-drag-handle. Lists sharing
// a group can opt into receiving (put) and exporting (pull or pull="clone").
// Keep validation in accepts and persist through update:modelValue after drop.
const props = withDefaults(defineProps<{
  modelValue: T[]
  itemKey: (item: T) => string | number
  group?: string
  pull?: boolean | 'clone'
  put?: boolean
  sort?: boolean
  disabled?: boolean
  itemDisabled?: (item: T) => boolean
  accepts?: (item: T) => boolean
  clone?: (item: T) => T
  itemClass?: string
  handle?: string
  direction?: 'horizontal' | 'vertical'
}>(), { group: '', pull: false, put: false, sort: true, handle: '[data-drag-handle]' })
const emit = defineEmits<{
  'update:modelValue': [items: T[]]
  start: [item: T]
  end: [item: T, changed: boolean]
}>()
defineSlots<{
  item(props: { item: T; index: number }): unknown
  empty(): unknown
}>()

const root = ref<HTMLElement>(), dragging = ref(false), reducedMotion = ref(false)
const list = computed({ get: () => props.modelValue, set: items => { if (!props.disabled) emit('update:modelValue', items) } })
let suppressClick = false, clickTimer: ReturnType<typeof setTimeout> | undefined
let motion: MediaQueryList | undefined, draggedElement: HTMLElement | undefined
const updateMotion = () => { reducedMotion.value = !!motion?.matches }

function start(event: DraggableEvent<T>) {
  draggedElement = event.item
  draggedItems.set(event.item, event.clonedData)
  dragging.value = true; suppressClick = true; clearTimeout(clickTimer)
  emit('start', event.data)
}
function end(event: DraggableEvent<T>) {
  draggedItems.delete(event.item); draggedElement = undefined; dragging.value = false
  clickTimer = setTimeout(() => { suppressClick = false }, 250)
  emit('end', event.data, event.from !== event.to || event.oldDraggableIndex !== event.newDraggableIndex)
}
function captureClick(event: MouseEvent) {
  if (suppressClick) { event.preventDefault(); event.stopImmediatePropagation() }
}

// useDraggable updates the computed list on drop, rather than on every hover.
useDraggable(root, list, computed<UseDraggableOptions<T>>(() => ({
  group: {
    name: props.group,
    pull: props.pull,
    put: (_target, source, element) => !props.disabled && props.put && !!props.group
      && (typeof source.options.group === 'string' ? source.options.group : source.options.group?.name) === props.group
      && draggedItems.has(element) && (!props.accepts || props.accepts(draggedItems.get(element) as T)),
  },
  sort: props.sort,
  disabled: props.disabled,
  draggable: '[data-sortable-item]',
  filter: '[data-drag-disabled="true"]',
  preventOnFilter: false,
  handle: props.handle,
  direction: props.direction,
  clone: props.clone,
  animation: reducedMotion.value ? 0 : 150,
  delay: 300,
  delayOnTouchOnly: true,
  touchStartThreshold: 5,
  fallbackTolerance: 4,
  forceFallback: true,
  fallbackOnBody: true,
  // Touch Events allow quick swipes to scroll, then prevent scrolling after
  // the long press; native panning can cancel the Pointer Events path instead.
  supportPointer: false,
  ghostClass: 'drag-list-placeholder',
  fallbackClass: 'drag-list-fallback',
  onStart: start,
  onEnd: end,
})))

onMounted(() => { motion = matchMedia('(prefers-reduced-motion: reduce)'); updateMotion(); motion.addEventListener('change', updateMotion) })
onBeforeUnmount(() => {
  motion?.removeEventListener('change', updateMotion); clearTimeout(clickTimer)
  if (draggedElement) draggedItems.delete(draggedElement)
})
</script>

<template>
  <div ref="root" class="drag-list" :class="{ 'is-dragging': dragging }" :aria-disabled="disabled || undefined" @click.capture="captureClick">
    <div v-for="(item, index) in modelValue" :key="itemKey(item)" class="drag-list-item" :class="itemClass" data-sortable-item :data-drag-disabled="disabled || itemDisabled?.(item) || undefined">
      <slot name="item" :item="item" :index="index" />
    </div>
    <div v-if="!modelValue.length" class="drag-list-empty"><slot name="empty" /></div>
  </div>
</template>

<style scoped>
.drag-list { position: relative; display: flex; flex-wrap: wrap; align-items: center; gap: 7px; min-width: 0; min-height: 44px; }
.drag-list-empty { position: absolute; inset: 0; display: flex; align-items: center; }
.drag-list:has(> .drag-list-placeholder) > .drag-list-empty { visibility: hidden; }
.drag-list-item { display: flex; max-width: 100%; border-radius: 8px; }
.drag-list-item :deep([data-drag-handle]) { cursor: grab; touch-action: manipulation; user-select: none; -webkit-user-select: none; -webkit-touch-callout: none; }
.drag-list-item[data-drag-disabled=true] :deep([data-drag-handle]) { cursor: default; touch-action: auto; pointer-events: none; }
.is-dragging :deep([data-drag-handle]) { cursor: grabbing; }
.drag-list-placeholder { background: rgb(59 130 246 / .12); outline: 1px dashed rgb(59 130 246 / .65); outline-offset: -1px; }
.drag-list-placeholder > :deep(*) { opacity: 0; }
.drag-list-fallback { opacity: .95 !important; box-shadow: 0 5px 18px rgb(15 23 42 / .16); cursor: grabbing; }
</style>
