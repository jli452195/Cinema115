<script setup lang="ts">
import { ref } from 'vue'
import { IconArrowLeft, IconX } from '@tabler/icons-vue'
import BottomSheetExpandButton from './BottomSheetExpandButton.vue'

withDefaults(defineProps<{
  title: string
  headingID: string
  closeLabel: string
  backLabel?: string
  closeDisabled?: boolean
  backDisabled?: boolean
  back?: boolean
  closable?: boolean
  expanded: boolean
  expandable: boolean
}>(), { backLabel: '返回上一级', back: false, closable: true, closeDisabled: false, backDisabled: false })
const emit = defineEmits<{ back: []; close: []; expand: []; scroll: [top: number] }>()
const container = ref<HTMLElement>()
const body = ref<HTMLElement>()
function requestBack(event: MouseEvent) {
  if (event.detail > 1) return
  emit('back')
}
function requestClose(event: MouseEvent) {
  if (event.detail > 1) return
  emit('close')
}
defineExpose({ container, body, scrollTo: (top: number) => body.value?.scrollTo({ top, behavior: 'instant' }) })
</script>

<template>
  <section ref="container" class="floating-dialog-page" tabindex="-1" :aria-labelledby="headingID">
    <header class="floating-dialog-toolbar" :data-sheet-drag="expandable ? '' : undefined">
      <div v-if="back || $slots['toolbar-leading']" class="floating-dialog-leading">
        <button v-if="back" type="button" class="floating-dialog-back" data-sheet-back :aria-label="backLabel" :disabled="backDisabled" @click="requestBack"><IconArrowLeft :size="18" aria-hidden="true" /><span class="floating-dialog-back-label">返回</span></button>
        <slot name="toolbar-leading" />
      </div>
      <h2 :id="headingID"><slot name="title">{{ title }}</slot></h2>
      <div class="floating-dialog-tools">
        <slot name="toolbar-actions" />
        <BottomSheetExpandButton v-if="expandable" :expanded="expanded" @click="emit('expand')" />
        <button v-if="closable" :autofocus="!back" type="button" class="floating-dialog-close" data-sheet-close :aria-label="closeLabel" :title="closeLabel" :disabled="closeDisabled" @click="requestClose"><IconX :size="20" aria-hidden="true" /></button>
      </div>
    </header>
    <div v-if="$slots.header" class="floating-dialog-header"><slot name="header" /></div>
    <div ref="body" class="floating-dialog-body" data-auto-scrollbar @scroll="emit('scroll', ($event.target as HTMLElement).scrollTop)"><slot /></div>
    <div v-if="$slots.footer" class="floating-dialog-footer"><slot name="footer" /></div>
  </section>
</template>

<style scoped>
.floating-dialog-page { display: flex; flex: 1 1 auto; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; outline: none; }
.floating-dialog-toolbar { display: flex; flex: none; min-height: 52px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 12px 6px var(--dialog-content-inset); }
.floating-dialog-toolbar h2 { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--sheet-font-title); font-weight: 600; line-height: 24px; }
.floating-dialog-tools { display: flex; flex: none; align-items: center; gap: 2px; }
.floating-dialog-leading { display: flex; flex: none; align-items: center; }
.floating-dialog-close { display: grid; flex: none; place-items: center; width: var(--dialog-control-height); height: var(--dialog-control-height); border-radius: 10px; }
.floating-dialog-back { display: inline-flex; flex: none; min-height: var(--dialog-control-height); align-items: center; justify-content: center; gap: 4px; margin-left: -6px; padding: 0 10px 0 6px; border-radius: 10px; color: rgb(var(--action-link)); font-size: var(--sheet-font-body, 14px); font-weight: 500; line-height: 20px; white-space: nowrap; transition: background-color .15s; }
.floating-dialog-close:hover, .floating-dialog-back:hover { background: rgb(var(--surface-hover) / .65); }
.floating-dialog-back:hover:not(:disabled) { background: rgb(var(--surface-hover) / .7); }
.floating-dialog-close:disabled, .floating-dialog-back:disabled { opacity: .4; cursor: wait; }
.floating-dialog-header { flex: none; min-width: 0; padding: 0 var(--dialog-content-inset) 8px; }
.floating-dialog-body { flex: 1 1 auto; min-width: 0; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 6px var(--dialog-content-inset) 18px; }
.floating-dialog-footer { flex: none; min-width: 0; padding: 10px var(--dialog-content-inset) 14px; border-top: 1px solid rgb(var(--line-default) / .7); }
button:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
@media (min-width: 640px) {
  .floating-dialog-leading:has(.floating-dialog-back)::after { content: ''; width: 1px; height: 14px; margin-left: 4px; background: rgb(var(--line-default)); }
}
@media (max-width: 639px) {
  .floating-dialog-toolbar { min-height: 48px; gap: 4px; padding: 0 4px 4px var(--dialog-content-inset); }
  .floating-dialog-toolbar h2 { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; white-space: normal; overflow-wrap: anywhere; }
  .floating-dialog-back { min-height: var(--sheet-touch-size, 44px); padding: 0 8px 0 4px; }
  .floating-dialog-body { padding-block: 6px 12px; scrollbar-gutter: auto; }
  .floating-dialog-footer { padding-block: 10px 12px; }
}
@media (max-width: 339px) {
  .floating-dialog-back { width: var(--sheet-touch-size, 44px); padding: 0; }
  .floating-dialog-back-label { display: none; }
}
@media (prefers-reduced-motion: reduce) { .floating-dialog-back { transition: none; } }
</style>
