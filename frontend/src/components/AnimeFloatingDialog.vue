<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconArrowsMaximize, IconArrowsMinimize, IconX } from '@tabler/icons-vue'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
import { useMobileSheet } from '../composables/useMobileSheet'
defineOptions({ inheritAttrs: false })
const props = defineProps<{ title: string; closeLabel: string; size?: 'config' | 'preview' | 'organize'; closeDisabled?: boolean; mobilePresentation?: 'dialog' | 'sheet' }>()
const emit = defineEmits<{ close: []; scroll: [top: number] }>()
const dialog = ref<HTMLDialogElement>(), body = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const headingID = useId()
const dialogLifecycle = createDialogLifecycle({ coverParent: true })
const nested = ref(false)
let backdropDown = false
const sheet = useMobileSheet(dialog, { enabled: () => props.mobilePresentation === 'sheet', closeDisabled: () => !!props.closeDisabled, onClose: () => emit('close') })
function close() { sheet.close() }
function backdrop(event: MouseEvent) { if (backdropDown && event.target === dialog.value) close(); backdropDown = false }
onMounted(() => { nested.value = [...document.querySelectorAll('dialog[open]')].some(el => el !== dialog.value); dialogLifecycle.acquire(); dialog.value?.showModal(); closeButton.value?.focus({ preventScroll: true }); sheet.open() })
onBeforeUnmount(() => {
  dialog.value?.close()
  dialogLifecycle.release()
})
defineExpose({ body, scrollTo: (top: number) => body.value?.scrollTo({ top, behavior: 'instant' }) })
</script>

<template>
  <Teleport to="body">
    <dialog v-bind="$attrs" ref="dialog" class="anime-floating floating-surface" :class="[{ 'anime-floating-nested': nested, 'anime-floating-sheet': mobilePresentation === 'sheet' }, size && `anime-floating-${size}`]" :aria-labelledby="headingID" @cancel.prevent="close" @pointerdown="backdropDown = $event.target === dialog" @click="backdrop">
      <div v-if="mobilePresentation === 'sheet'" class="anime-floating-grabber-zone" aria-hidden="true" @pointerdown="sheet.onPointerDown" @pointermove="sheet.onPointerMove" @pointerup="sheet.onPointerUp" @pointercancel="sheet.onPointerCancel" @lostpointercapture="sheet.onPointerCancel"><span class="anime-floating-grabber"></span></div>
      <header class="anime-floating-toolbar" @pointerdown="sheet.onPointerDown" @pointermove="sheet.onPointerMove" @pointerup="sheet.onPointerUp" @pointercancel="sheet.onPointerCancel" @lostpointercapture="sheet.onPointerCancel">
        <h2 :id="headingID"><slot name="title">{{ title }}</slot></h2>
        <div class="anime-floating-tools">
          <slot name="toolbar-actions" />
          <button v-if="mobilePresentation === 'sheet'" class="anime-floating-expand" type="button" :aria-label="sheet.expanded.value ? '恢复弹窗高度' : '展开弹窗'" :aria-pressed="sheet.expanded.value" @click="sheet.toggleExpanded()"><component :is="sheet.expanded.value ? IconArrowsMinimize : IconArrowsMaximize" :size="18" aria-hidden="true" /></button>
          <button ref="closeButton" type="button" :aria-label="closeLabel" :disabled="closeDisabled" @click="close"><IconX :size="20" aria-hidden="true" /></button>
        </div>
      </header>
      <div v-if="$slots.header" class="anime-floating-header"><slot name="header" /></div>
      <div ref="body" class="anime-floating-body" data-auto-scrollbar @scroll="emit('scroll', ($event.target as HTMLElement).scrollTop)"><slot /></div>
      <div v-if="$slots.footer" class="anime-floating-footer"><slot name="footer" /></div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.anime-floating { --ae-control-height: 36px; --ae-control-font: 13px; --ae-control-padding: 6px 10px; width: min(760px, calc(100vw - 32px)); max-height: min(880px, calc(100dvh - 48px)); margin: auto; padding: 0; overflow: clip; border-radius: 20px; color: var(--floating-surface-text); }
.anime-floating[open] { display: flex; flex-direction: column; }
.anime-floating-config { width: min(820px, calc(100vw - 32px)); height: min(760px, calc(100dvh - 48px)); }
.anime-floating-config .anime-floating-body { scrollbar-gutter: stable; }
.anime-floating-organize { width: min(1040px, calc(100vw - 48px)); height: min(820px, calc(100dvh - 48px)); border-radius: 24px; }
.anime-floating-organize .anime-floating-body { scrollbar-gutter: stable; padding-top: 0; }
.anime-floating-preview { width: min(1600px, calc(100vw - 40px)); height: min(760px, calc(100dvh - 40px)); max-height: calc(100dvh - 40px); }
.anime-floating-preview .anime-floating-body { display: flex; overflow: hidden; padding-bottom: 4px; }
.anime-floating-nested::backdrop { background: var(--floating-surface-scrim); backdrop-filter: none; }
.anime-floating-header { flex: none; padding: 0 22px 8px; }
.anime-floating-toolbar { display: flex; flex: none; min-height: 52px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px 6px 22px; }
.anime-floating-toolbar h2 { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 16px; font-weight: 600; line-height: 24px; color: rgb(var(--content-primary)); }
.anime-floating-tools { display: flex; flex: none; align-items: center; gap: 2px; }
.anime-floating-grabber-zone, .anime-floating .anime-floating-expand { display: none; }
.anime-floating-toolbar button { display: grid; flex-shrink: 0; place-items: center; width: var(--ae-control-height); height: var(--ae-control-height); border-radius: 10px; }
.anime-floating-toolbar button:hover { background: rgb(148 163 184 / .12); }
.anime-floating-toolbar button:disabled { opacity: .4; cursor: wait; }
.anime-floating-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 6px 22px 18px; }
.anime-floating-footer { flex: none; padding: 10px 22px 14px; box-shadow: 0 -1px 0 rgb(148 163 184 / .12); }
.anime-floating-preview .anime-floating-footer { padding-top: 6px; padding-bottom: max(12px, env(safe-area-inset-bottom)); box-shadow: none; }
.anime-floating button:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
@media (max-width: 639px) {
  .anime-floating { width: calc(100vw - 16px); max-height: calc(100dvh - 24px); border-radius: 20px; }
  .anime-floating-config { height: calc(100dvh - 24px); }
  .anime-floating-toolbar { padding-inline: 16px 10px; }
  .anime-floating-header { padding-inline: 16px; }
  .anime-floating-body { padding: 8px 16px 18px; }
  .anime-floating-footer { padding: 12px 16px max(16px, env(safe-area-inset-bottom)); }
}
@media (max-width: 639px), (pointer: coarse) { .anime-floating { --ae-control-height: 44px; --ae-control-font: 16px; --ae-control-padding: 8px 10px; } }
@media (max-width: 639px) { .anime-floating-organize { height: calc(100dvh - 24px); } }
/* 仅显式启用的手机抽屉贴齐屏幕边缘，其余弹窗保留原来的外壳。 */
@media (max-width: 639px) {
  .anime-floating-sheet { --sheet-top-gap: max(24px, calc(env(safe-area-inset-top) + 8px)); position: fixed; inset: auto 0 var(--sheet-viewport-bottom, 0px); width: 100vw; max-width: 100vw; height: calc(var(--sheet-viewport-height, 100dvh) - var(--sheet-top-gap)); max-height: calc(var(--sheet-viewport-height, 100dvh) - var(--sheet-top-gap)); margin: 0; border-inline: 0; border-bottom: 0; border-radius: 20px 20px 0 0; background: var(--floating-surface-container); -webkit-backdrop-filter: none; backdrop-filter: none; }
  .anime-floating-sheet[data-sheet-expanded='true'] { --sheet-top-gap: 0px; border-radius: 0; padding-top: env(safe-area-inset-top); }
  .anime-floating-sheet .anime-floating-grabber-zone { display: flex; flex: none; justify-content: center; padding: 8px 0 3px; touch-action: none; cursor: grab; }
  .anime-floating-grabber { width: 36px; height: 4px; border-radius: 999px; background: rgb(148 163 184 / .5); }
  .anime-floating-sheet .anime-floating-toolbar { min-height: 50px; gap: 8px; padding: 0 8px 6px 12px; touch-action: none; user-select: none; }
  .anime-floating-sheet .anime-floating-toolbar h2 { flex: 1; }
  .anime-floating-sheet .anime-floating-toolbar button { width: 44px; height: 44px; }
  .anime-floating-sheet .anime-floating-expand { display: grid; }
  .anime-floating-sheet .anime-floating-header { padding-inline: 10px; }
  .anime-floating-sheet .anime-floating-body { padding: 6px 8px max(12px, env(safe-area-inset-bottom)); scrollbar-gutter: auto; }
}
</style>
