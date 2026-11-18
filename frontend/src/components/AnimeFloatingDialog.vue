<script setup lang="ts">
import { ref, useId } from 'vue'
import { IconX } from '@tabler/icons-vue'
import BottomSheetDialog from './BottomSheetDialog.vue'
import BottomSheetExpandButton from './BottomSheetExpandButton.vue'

defineOptions({ inheritAttrs: false })
defineProps<{ title: string; closeLabel: string; size?: 'config' | 'preview' | 'organize'; closeDisabled?: boolean; mobilePresentation?: 'dialog' | 'sheet' }>()
const emit = defineEmits<{ close: []; scroll: [top: number] }>()
const body = ref<HTMLElement>()
const headingID = useId()
defineExpose({ body, scrollTo: (top: number) => body.value?.scrollTo({ top, behavior: 'instant' }) })
</script>

<template>
  <Teleport to="body">
    <BottomSheetDialog v-bind="$attrs" class="anime-floating" :class="size && `anime-floating-${size}`" :aria-labelledby="headingID" :mobile-sheet="mobilePresentation === 'sheet'" :close-disabled="closeDisabled" desktop-backdrop-dismiss @close="emit('close')" v-slot="{ close, expanded, toggleExpanded }">
      <header class="anime-floating-toolbar" :data-sheet-drag="mobilePresentation === 'sheet' ? '' : undefined">
        <h2 :id="headingID"><slot name="title">{{ title }}</slot></h2>
        <div class="anime-floating-tools">
          <slot name="toolbar-actions" />
          <BottomSheetExpandButton v-if="mobilePresentation === 'sheet'" :expanded="expanded" @click="toggleExpanded" />
          <button autofocus type="button" :aria-label="closeLabel" :disabled="closeDisabled" @click="close"><IconX :size="20" aria-hidden="true" /></button>
        </div>
      </header>
      <div v-if="$slots.header" class="anime-floating-header"><slot name="header" /></div>
      <div ref="body" class="anime-floating-body" data-auto-scrollbar @scroll="emit('scroll', ($event.target as HTMLElement).scrollTop)"><slot /></div>
      <div v-if="$slots.footer" class="anime-floating-footer"><slot name="footer" /></div>
    </BottomSheetDialog>
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
.anime-floating-header { flex: none; padding: 0 22px 8px; }
.anime-floating-toolbar { display: flex; flex: none; min-height: 52px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px 6px 22px; }
.anime-floating-toolbar h2 { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 16px; font-weight: 600; line-height: 24px; color: rgb(var(--content-primary)); }
.anime-floating-tools { display: flex; flex: none; align-items: center; gap: 2px; }
.anime-floating-toolbar button:not(.bottom-sheet-expand) { display: grid; }
.anime-floating-toolbar button { flex-shrink: 0; place-items: center; width: var(--ae-control-height); height: var(--ae-control-height); border-radius: 10px; }
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
/* 手机抽屉只调整内边距，定位和手势交由共用外壳。 */
@media (max-width: 639px) {
  .anime-floating[data-sheet-expanded] .anime-floating-toolbar { min-height: 50px; gap: 8px; padding: 0 8px 6px 12px; }
  .anime-floating[data-sheet-expanded] .anime-floating-toolbar h2 { flex: 1; }
  .anime-floating[data-sheet-expanded] .anime-floating-toolbar button { width: 44px; height: 44px; }
  .anime-floating[data-sheet-expanded] .anime-floating-header { padding-inline: 10px; }
  .anime-floating[data-sheet-expanded] .anime-floating-body { padding: 6px 8px 12px; scrollbar-gutter: auto; }
  .anime-floating[data-sheet-expanded] .anime-floating-footer { padding-bottom: 16px; }
}
</style>
