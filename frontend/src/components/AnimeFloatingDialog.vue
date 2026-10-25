<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconX } from '@tabler/icons-vue'
import { lockAnimeDialogScroll } from '../lib/anime-dialog-lock'
defineOptions({ inheritAttrs: false })
const props = defineProps<{ title: string; closeLabel: string; size?: 'config' | 'preview' | 'organize'; closeDisabled?: boolean }>()
const emit = defineEmits<{ close: []; scroll: [top: number] }>()
const dialog = ref<HTMLDialogElement>(), body = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const headingID = useId()
let previousFocus: HTMLElement | null = null
let unlock: (() => void) | undefined
const nested = ref(false)
let backdropDown = false
function close() { if (!props.closeDisabled) emit('close') }
function backdrop(event: MouseEvent) { if (backdropDown && event.target === dialog.value) close(); backdropDown = false }
onMounted(() => { previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null; nested.value = [...document.querySelectorAll('dialog[open]')].some(el => el !== dialog.value); unlock = lockAnimeDialogScroll(); dialog.value?.showModal(); closeButton.value?.focus({ preventScroll: true }) })
onBeforeUnmount(() => {
  dialog.value?.close()
  unlock?.()
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
})
defineExpose({ body, scrollTo: (top: number) => body.value?.scrollTo({ top, behavior: 'instant' }) })
</script>

<template>
  <Teleport to="body">
    <dialog v-bind="$attrs" ref="dialog" class="anime-floating floating-surface" :class="[{ 'anime-floating-nested': nested }, size && `anime-floating-${size}`]" :aria-labelledby="headingID" @cancel.prevent="close" @pointerdown="backdropDown = $event.target === dialog" @click="backdrop">
      <header class="anime-floating-toolbar"><h2 :id="headingID">{{ title }}</h2><button ref="closeButton" type="button" :aria-label="closeLabel" :disabled="closeDisabled" @click="close"><IconX :size="20" aria-hidden="true" /></button></header>
      <div v-if="$slots.header" class="anime-floating-header"><slot name="header" /></div>
      <div ref="body" class="anime-floating-body" @scroll="emit('scroll', ($event.target as HTMLElement).scrollTop)"><slot /></div>
      <div v-if="$slots.footer" class="anime-floating-footer"><slot name="footer" /></div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.anime-floating { --scrollbar-track: transparent; --scrollbar-thumb: rgb(100 116 139 / .4); --ae-control-height: 36px; --ae-control-font: 13px; --ae-control-padding: 6px 10px; width: min(760px, calc(100vw - 32px)); max-height: min(880px, calc(100dvh - 48px)); margin: auto; padding: 0; overflow: clip; border-radius: 18px; color: var(--floating-surface-text); }
:global(.dark) .anime-floating { --scrollbar-thumb: rgb(184 196 212 / .4); }
.anime-floating[open] { display: flex; flex-direction: column; }
.anime-floating-config { width: min(820px, calc(100vw - 32px)); height: min(760px, calc(100dvh - 48px)); }
.anime-floating-config .anime-floating-body { scrollbar-gutter: stable; }
.anime-floating-organize { width: min(1040px, calc(100vw - 32px)); height: min(880px, calc(100dvh - 48px)); }
.anime-floating-organize .anime-floating-body { scrollbar-gutter: stable; padding-top: 0; }
.anime-floating-preview { width: min(1600px, calc(100vw - 40px)); height: min(760px, calc(100dvh - 40px)); max-height: calc(100dvh - 40px); }
.anime-floating-preview .anime-floating-body { display: flex; overflow: hidden; padding-bottom: 4px; }
.anime-floating-nested::backdrop { background: rgb(15 23 42 / .16); backdrop-filter: none; }
.anime-floating-header { flex: none; padding: 0 22px 8px; }
.anime-floating-toolbar { display: flex; flex: none; min-height: 46px; align-items: center; justify-content: space-between; gap: 12px; padding: 5px 12px 2px 22px; }
.anime-floating-toolbar h2 { font-size: 12px; color: var(--floating-surface-muted); }
.anime-floating-toolbar button { display: grid; place-items: center; width: var(--ae-control-height); height: var(--ae-control-height); border-radius: 9px; }
.anime-floating-toolbar button:hover { background: rgb(148 163 184 / .12); }
.anime-floating-toolbar button:disabled { opacity: .4; cursor: wait; }
.anime-floating-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 6px 22px 18px; }
.anime-floating-footer { flex: none; padding: 10px 22px 14px; box-shadow: 0 -1px 0 rgb(148 163 184 / .12); }
.anime-floating-preview .anime-floating-footer { padding-top: 6px; padding-bottom: max(12px, env(safe-area-inset-bottom)); box-shadow: none; }
.anime-floating button:focus-visible { outline: 2px solid var(--ae-blue, #2563eb); outline-offset: 2px; }
@media (max-width: 639px) { .anime-floating { width: calc(100vw - 16px); max-height: calc(100dvh - 24px); border-radius: 18px; } .anime-floating-config { height: calc(100dvh - 24px); } .anime-floating-toolbar { padding-inline: 18px 10px; } .anime-floating-body { padding: 8px 18px 18px; } .anime-floating-footer { padding: 12px 18px max(16px, env(safe-area-inset-bottom)); } }
@media (max-width: 639px), (pointer: coarse) { .anime-floating { --ae-control-height: 44px; --ae-control-font: 16px; --ae-control-padding: 8px 10px; } }
@media (max-width: 639px) { .anime-floating-organize { height: calc(100dvh - 24px); } }
</style>
