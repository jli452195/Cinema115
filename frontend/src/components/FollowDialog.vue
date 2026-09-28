<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

const props = defineProps<{ title: string; busy?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
function close() { if (!props.busy) emit('close') }
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal() })
onBeforeUnmount(() => {
  dialog.value?.close(); document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-label="title" class="m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="close">
      <header class="flex items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 px-5 py-4"><h2 class="font-semibold text-slate-900 dark:text-slate-100">{{ title }}</h2><button type="button" autofocus :disabled="busy" :aria-label="`关闭${title}`" class="rounded-lg px-2 py-1 text-xl text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 disabled:opacity-40" @click="close">×</button></header>
      <div class="p-5"><slot /></div>
    </dialog>
  </Teleport>
</template>
