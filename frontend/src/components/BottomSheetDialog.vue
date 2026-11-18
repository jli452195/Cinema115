<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useMobileSheet } from '../composables/useMobileSheet'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'

const props = withDefaults(defineProps<{ mobileSheet?: boolean; closeDisabled?: boolean; desktopBackdropDismiss?: boolean }>(), { mobileSheet: true })
const emit = defineEmits<{ closing: []; close: []; 'viewport-change': [short: boolean] }>()
const dialog = ref<HTMLDialogElement>()
const lifecycle = createDialogLifecycle({ coverParent: true })
let backdropPointer: number | undefined
let backdropClick = false

const sheet = useMobileSheet(dialog, {
  enabled: () => props.mobileSheet,
  closeDisabled: () => !!props.closeDisabled,
  onClosing: () => emit('closing'),
  onClose: () => emit('close'),
  onViewportChange(height) {
    const short = height < 520
    emit('viewport-change', short)
    const field = document.activeElement
    if (short && (field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement) && dialog.value?.contains(field)) {
      void nextTick(() => { if (field === document.activeElement) field.scrollIntoView({ block: 'nearest' }) })
    }
  },
})

function canDismissOnBackdrop() {
  return props.desktopBackdropDismiss || (props.mobileSheet && window.matchMedia('(max-width: 639px)').matches)
}
function isBackdrop(event: MouseEvent) {
  const surface = dialog.value
  if (!surface || event.target !== surface) return false
  const box = surface.getBoundingClientRect()
  return event.clientX < box.left || event.clientX >= box.right || event.clientY < box.top || event.clientY >= box.bottom
}
function pointerDown(event: PointerEvent) {
  if (!event.isPrimary || event.button !== 0) return
  backdropClick = false
  backdropPointer = canDismissOnBackdrop() && isBackdrop(event) ? event.pointerId : undefined
  if (event.target instanceof Element && event.target.closest('[data-sheet-drag]')) sheet.onPointerDown(event)
}
function pointerUp(event: PointerEvent) {
  backdropClick = backdropPointer === event.pointerId && isBackdrop(event)
  backdropPointer = undefined
  sheet.onPointerUp(event)
}
function pointerCancel(event: PointerEvent) {
  backdropPointer = undefined
  backdropClick = false
  sheet.onPointerCancel(event)
}
function click(event: MouseEvent) {
  if (backdropClick && canDismissOnBackdrop() && isBackdrop(event)) sheet.close()
  backdropClick = false
}

onMounted(() => {
  const surface = dialog.value
  if (!surface) return
  lifecycle.acquire()
  surface.showModal()
  sheet.open()
})
onBeforeUnmount(() => {
  dialog.value?.close()
  lifecycle.release()
})
defineExpose({ dialog })
</script>

<template>
  <dialog ref="dialog" class="bottom-sheet-dialog floating-surface" :class="{ 'bottom-sheet-dialog-mobile': mobileSheet }" @cancel.prevent="sheet.close" @pointerdown="pointerDown" @pointermove="sheet.onPointerMove" @pointerup="pointerUp" @pointercancel="pointerCancel" @lostpointercapture="sheet.onPointerCancel" @click="click">
    <div v-if="mobileSheet" class="bottom-sheet-grabber-zone" data-sheet-drag aria-hidden="true"><span></span></div>
    <slot :close="sheet.close" :expanded="sheet.expanded.value" :toggle-expanded="sheet.toggleExpanded" />
  </dialog>
</template>

<style scoped>
.bottom-sheet-grabber-zone { display: none; }
@media (max-width: 639px) {
  .bottom-sheet-dialog.bottom-sheet-dialog-mobile {
    --sheet-top-gap: max(24px, calc(var(--app-safe-top) + 8px));
    --sheet-height: max(0px, calc(var(--sheet-viewport-height, 100dvh) - var(--sheet-top-gap)));
    position: fixed; inset: auto 0 var(--sheet-viewport-bottom, 0px); width: 100vw; max-width: none;
    height: var(--sheet-height); max-height: var(--sheet-height); margin: 0;
    padding: 0 var(--app-safe-right) max(0px, calc(var(--app-safe-bottom) - var(--sheet-viewport-bottom, 0px))) var(--app-safe-left);
    overflow: clip; border: 0; border-radius: 20px 20px 0 0;
    background: var(--floating-surface-container); box-shadow: 0 -4px 22px rgb(0 0 0 / .16);
    -webkit-backdrop-filter: none; backdrop-filter: none;
  }
  .bottom-sheet-dialog-mobile[open] { display: flex; flex-direction: column; }
  .bottom-sheet-dialog.bottom-sheet-dialog-mobile[data-sheet-expanded='true'] { --sheet-top-gap: 0px; border-radius: 0; padding-top: max(0px, calc(var(--app-safe-top) - var(--sheet-viewport-top, 0px))); }
  .bottom-sheet-dialog.bottom-sheet-dialog-mobile:not([data-dialog-covered])::backdrop { background: rgb(0 0 0 / .36); }
  .bottom-sheet-dialog-mobile :deep([data-sheet-drag]) { touch-action: none; user-select: none; }
  .bottom-sheet-dialog-mobile .bottom-sheet-grabber-zone { display: grid; flex: none; height: 16px; place-items: center; touch-action: none; cursor: grab; }
  .bottom-sheet-grabber-zone span { width: 36px; height: 4px; border-radius: 999px; background: rgb(var(--content-secondary) / .4); }
}
</style>
