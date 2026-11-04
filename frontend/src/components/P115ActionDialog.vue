<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import P115Icon from './P115Icon.vue'
import { lockAnimeDialogScroll } from '../lib/anime-dialog-lock'
const props = defineProps<{ title: string; busy?: boolean; size?: 'wide'; layout?: 'contained' }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>(), titleID = useId()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
let unlock: (() => void) | undefined
function close() { if (!props.busy) emit('close') }
onMounted(() => { unlock = lockAnimeDialogScroll(); dialog.value?.showModal() })
onBeforeUnmount(() => { dialog.value?.close(); unlock?.(); if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true }) })
</script>
<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-labelledby="titleID" :class="[size === 'wide' ? 'max-w-2xl' : 'max-w-md', { 'p115-contained': layout === 'contained' }]" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] overflow-y-auto rounded-2xl border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-200" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <header class="flex items-center gap-4 border-b border-slate-100 px-5 py-4 dark:border-slate-800"><h2 :id="titleID" class="min-w-0 flex-1 font-semibold">{{ title }}</h2><button type="button" aria-label="关闭弹窗" :disabled="busy" class="inline-flex items-center justify-center rounded-lg p-2 text-slate-500 disabled:opacity-40" @click="close"><P115Icon name="close" /></button></header>
      <div class="space-y-4 p-5 text-sm leading-6"><slot /></div>
      <footer v-if="$slots.footer" class="flex justify-end gap-3 border-t border-slate-100 px-5 py-4 dark:border-slate-800"><slot name="footer" /></footer>
    </dialog>
  </Teleport>
</template>
<style scoped>
/* Contained 布局：固定头尾与内部滚动，采用 HeroUI 风格中性边框与柔和投影 */
.p115-contained[open] { display:flex; flex-direction:column; overflow:hidden; color:#404040; background:#fff; border:1px solid #e5e5e5; box-shadow:0 24px 80px -24px rgb(0 0 0 / .3),0 12px 32px -20px rgb(0 0 0 / .2); }
.p115-contained>header,.p115-contained>footer { flex-shrink:0; }
.p115-contained>div { min-height:0; overflow-y:auto; overscroll-behavior:contain; padding:16px 20px; }
.p115-contained>header { background:#fff; border-color:#f0f0f0; padding:14px 20px; }
.p115-contained>header>h2 { color:#171717; font-size:16px; font-weight:600; }
.p115-contained>footer { background:#fafafa; border-color:#f0f0f0; padding:12px 20px; }
.p115-contained>header>button { color:#737373; min-width:40px; min-height:40px; display:inline-flex; align-items:center; justify-content:center; }
:global(.dark .p115-contained[open]) { color:#e5e5e5; background:#171717; border-color:#262626; }:global(.dark .p115-contained>header) { background:#171717; border-color:#262626; }:global(.dark .p115-contained>header>h2) { color:#f5f5f5; }:global(.dark .p115-contained>footer) { background:#191919; border-color:#262626; }:global(.dark .p115-contained>header>button) { color:#a3a3a3; }
@media(max-width:639px) { .p115-contained { width:calc(100vw - 1rem); max-height:calc(100dvh - 1rem); }.p115-contained>header,.p115-contained>footer { padding:12px 16px; }.p115-contained>div { padding:14px 16px; }.p115-contained>header>button { min-width:44px; min-height:44px; } }
</style>
