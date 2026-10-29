<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import P115Icon from './P115Icon.vue'

const props = defineProps<{ title: string; description: string; busy?: boolean; wide?: boolean; narrow?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>(), titleID = useId(), descriptionID = useId()
const heading = ref<HTMLElement>(), scrollBody = ref<HTMLElement>()
let previousFocus: HTMLElement | null = null, previousOverflow = ''
function close() { if (!props.busy) emit('close') }
function backdrop(event: MouseEvent) {
  if (event.target !== dialog.value) return
  const rect = dialog.value.getBoundingClientRect()
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close()
}
watch(() => props.title, async () => {
  await nextTick()
  if (scrollBody.value) scrollBody.value.scrollTop = 0
  heading.value?.focus()
})
onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
})
onBeforeUnmount(() => dialog.value?.close())
onUnmounted(() => { document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected) previousFocus.focus() })
</script>
<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-labelledby="titleID" :aria-describedby="descriptionID" :class="wide ? 'left-0 right-auto ml-0 w-screen sm:left-auto sm:right-0 sm:ml-auto sm:w-[720px] sm:max-w-[calc(100vw-2rem)]' : narrow ? 'ml-auto w-full sm:w-[420px]' : 'ml-auto w-full sm:w-[540px]'" class="m-0 h-[100dvh] max-h-[100dvh] max-w-none overflow-hidden border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 sm:rounded-l-2xl dark:bg-slate-900 dark:text-slate-200" @cancel.prevent="close" @click="backdrop">
      <div class="flex h-full min-h-0 flex-col">
        <header class="flex shrink-0 items-start gap-4 border-b border-slate-200 px-5 py-5 sm:px-6 dark:border-slate-700">
          <div class="min-w-0 flex-1"><h2 :id="titleID" ref="heading" tabindex="-1" :class="wide ? 'line-clamp-2 break-all focus:outline-none' : ''" class="text-lg font-semibold text-slate-900 dark:text-slate-100" :title="title">{{ title }}</h2><p :id="descriptionID" class="mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ description }}</p></div>
          <button type="button" :disabled="busy" :aria-label="'关闭' + title" class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 disabled:opacity-40 dark:hover:bg-slate-800" @click="close"><P115Icon name="close" /></button>
        </header>
        <div v-if="$slots.actions" class="shrink-0 border-b border-slate-200 px-5 py-3 sm:px-6 dark:border-slate-700"><slot name="actions" /></div>
        <div ref="scrollBody" class="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-5 sm:px-6"><slot /></div>
        <footer v-if="$slots.footer" class="flex shrink-0 items-center gap-3 border-t border-slate-200 px-5 pb-[max(1rem,env(safe-area-inset-bottom))] pt-4 sm:px-6 dark:border-slate-700"><slot name="footer" /></footer>
      </div>
    </dialog>
  </Teleport>
</template>
