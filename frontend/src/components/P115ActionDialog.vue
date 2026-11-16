<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import P115Icon from './P115Icon.vue'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
const props = defineProps<{ title: string; busy?: boolean; size?: 'wide'; layout?: 'contained'; compact?: boolean; surface?: 'floating' }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>(), titleID = useId()
const dialogLifecycle = createDialogLifecycle()
function close() { if (!props.busy) emit('close') }
onMounted(() => { dialogLifecycle.acquire(); dialog.value?.showModal() })
onBeforeUnmount(() => { dialog.value?.close(); dialogLifecycle.release() })
</script>
<template>
  <Teleport to="body">
    <dialog ref="dialog" data-auto-scrollbar :aria-labelledby="titleID" :class="[size === 'wide' ? 'max-w-2xl' : 'max-w-md', surface === 'floating' ? 'p115-floating floating-surface' : 'border-0 bg-white text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-200', { 'p115-contained': layout === 'contained' && surface !== 'floating', 'p115-compact': compact }]" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] overflow-y-auto rounded-2xl p-0" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <header class="flex items-center gap-4 border-b border-slate-100 px-5 py-4 dark:border-slate-800"><h2 :id="titleID" class="min-w-0 flex-1 font-semibold">{{ title }}</h2><button type="button" aria-label="关闭弹窗" :disabled="busy" class="inline-flex items-center justify-center rounded-lg p-2 text-slate-500 disabled:opacity-40" @click="close"><P115Icon name="close" /></button></header>
      <div :data-auto-scrollbar="layout === 'contained' || surface === 'floating' ? '' : undefined" class="space-y-4 p-5 text-sm leading-6"><slot /></div>
      <footer v-if="$slots.footer" class="flex justify-end gap-3 border-t border-slate-100 px-5 py-4 dark:border-slate-800"><slot name="footer" /></footer>
    </dialog>
  </Teleport>
</template>
<style scoped>
/* 固定头尾与内部滚动，表面、边框和文字统一使用全站主题色。 */
.p115-contained[open] { @apply border border-slate-200 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200; display:flex; flex-direction:column; overflow:hidden; box-shadow:0 24px 80px -24px rgb(0 0 0 / .3),0 12px 32px -20px rgb(0 0 0 / .2); }
.p115-contained>header,.p115-contained>footer { flex-shrink:0; }
.p115-contained>div { min-height:0; overflow-y:auto; overscroll-behavior:contain; padding:16px 20px; }
.p115-contained>header { @apply border-slate-100 bg-white dark:border-slate-800 dark:bg-slate-900; padding:14px 20px; }
.p115-contained>header>h2 { @apply text-slate-900 dark:text-slate-100; font-size:16px; font-weight:600; }
.p115-contained>footer { @apply border-slate-100 bg-slate-50 dark:border-slate-800 dark:bg-slate-950; padding:12px 20px; }
.p115-contained>header>button { @apply text-slate-500 dark:text-slate-400; min-width:40px; min-height:40px; display:inline-flex; align-items:center; justify-content:center; }
/* 紧凑模式：减小头身尾留白，用于高信息密度弹窗 */
.p115-compact > header { padding: 8px 16px; }
.p115-compact > header > button { min-width: 36px; min-height: 36px; width: 36px; height: 36px; padding: 6px; }
.p115-compact > div { padding: 12px 16px; }
.p115-compact > div > :deep(:not([hidden]) ~ :not([hidden])) { margin-top: 8px; margin-bottom: 0; }
.p115-compact > footer { padding: 10px 16px; }
/* 按需复用公共浮层材质；头尾固定，仅内容区滚动，显隐滚动条由全局接管。 */
.p115-floating { max-height: min(760px, calc(100dvh - 48px)); overflow: hidden; border-radius: 20px; color: var(--floating-surface-text); }
.p115-floating[open] { display: flex; flex-direction: column; }
.p115-floating > header, .p115-floating > footer { flex: none; border-color: rgb(var(--line-default) / .65); }
.p115-floating > header { min-height: 52px; padding: 8px 12px 6px 20px; }
.p115-floating > header > h2 { color: rgb(var(--content-primary)); font-size: 16px; line-height: 24px; }
.p115-floating > header > button { display: inline-flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: 10px; color: var(--floating-surface-muted); }
.p115-floating > header > button:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.p115-floating > div { flex: 1 1 auto; min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 10px 20px 16px; }
.p115-floating > footer { padding: 12px 20px 14px; }
@media (hover: hover) {
  .p115-floating > header > button:not(:disabled):hover { background: rgb(var(--surface-hover) / .65); }
}
@media (prefers-contrast: more) {
  .p115-floating > header, .p115-floating > footer { border-color: rgb(var(--line-control)); }
}
@media(max-width:639px) {
  .p115-contained { width:calc(100vw - 1rem); max-height:calc(100dvh - 1rem); }
  .p115-contained>header,.p115-contained>footer { padding:12px 16px; }
  .p115-contained>div { padding:14px 16px; }
  .p115-contained>header>button { min-width:44px; min-height:44px; }
  .p115-compact > header > button { min-width: 44px; min-height: 44px; width: 44px; height: 44px; }
  .p115-floating { width: calc(100vw - 1rem); max-height: calc(100dvh - 24px); }
  .p115-floating > header { padding: 6px 10px 6px 16px; }
  .p115-floating > div { padding: 8px 16px 16px; }
  .p115-floating > footer { padding: 12px 16px max(16px, env(safe-area-inset-bottom)); }
}
@media (max-width: 639px), (pointer: coarse) {
  .p115-floating > header > button { min-width: 44px; min-height: 44px; width: 44px; height: 44px; }
}
</style>
