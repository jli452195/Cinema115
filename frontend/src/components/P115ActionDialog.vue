<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import P115Icon from './P115Icon.vue'
import { lockAnimeDialogScroll } from '../lib/anime-dialog-lock'
const props = defineProps<{ title: string; busy?: boolean }>()
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
    <dialog ref="dialog" :aria-labelledby="titleID" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-md overflow-y-auto rounded-2xl border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-200" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <header class="flex items-center gap-4 border-b border-slate-100 px-5 py-4 dark:border-slate-800"><h2 :id="titleID" class="min-w-0 flex-1 font-semibold">{{ title }}</h2><button type="button" aria-label="关闭弹窗" :disabled="busy" class="rounded-lg p-2 text-slate-500 disabled:opacity-40" @click="close"><P115Icon name="close" /></button></header>
      <div class="space-y-4 p-5 text-sm leading-6"><slot /></div>
      <footer v-if="$slots.footer" class="flex justify-end gap-3 border-t border-slate-100 px-5 py-4 dark:border-slate-800"><slot name="footer" /></footer>
    </dialog>
  </Teleport>
</template>
