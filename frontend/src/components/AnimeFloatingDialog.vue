<script setup lang="ts">
import { onBeforeUnmount, onMounted, onUnmounted, ref, useId } from 'vue'
import { IconX } from '@tabler/icons-vue'
defineProps<{ title: string; closeLabel: string }>()
const emit = defineEmits<{ close: []; scroll: [top: number] }>()
const dialog = ref<HTMLDialogElement>(), body = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const headingID = useId()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const previousOverflow = document.body.style.overflow
let backdropDown = false
function backdrop(event: MouseEvent) { if (backdropDown && event.target === dialog.value) emit('close'); backdropDown = false }
onMounted(() => { dialog.value?.showModal(); document.body.style.overflow = 'hidden'; closeButton.value?.focus({ preventScroll: true }) })
onBeforeUnmount(() => {
  dialog.value?.close()
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
})
// Descendant dialogs finish restoring their locks before the outer dialog
// restores the page's original overflow on a route change.
onUnmounted(() => { document.body.style.overflow = previousOverflow })
defineExpose({ scrollTo: (top: number) => body.value?.scrollTo({ top, behavior: 'instant' }) })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="anime-floating floating-surface" :aria-labelledby="headingID" @cancel.prevent="emit('close')" @pointerdown="backdropDown = $event.target === dialog" @click="backdrop">
      <header class="anime-floating-toolbar"><h2 :id="headingID">{{ title }}</h2><button ref="closeButton" type="button" :aria-label="closeLabel" @click="emit('close')"><IconX :size="20" aria-hidden="true" /></button></header>
      <div ref="body" class="anime-floating-body" @scroll="emit('scroll', ($event.target as HTMLElement).scrollTop)"><slot /></div>
      <div v-if="$slots.footer" class="anime-floating-footer"><slot name="footer" /></div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.anime-floating { width: min(760px, calc(100vw - 32px)); max-height: min(880px, calc(100dvh - 48px)); margin: auto; padding: 0; border-radius: 22px; color: var(--floating-surface-text); }
.anime-floating[open] { display: flex; flex-direction: column; }
.anime-floating-toolbar { display: flex; flex: none; min-height: 60px; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 14px 4px 24px; }
.anime-floating-toolbar h2 { font-size: 12px; color: var(--floating-surface-muted); }
.anime-floating-toolbar button { display: grid; place-items: center; width: 44px; height: 44px; border-radius: 12px; }
.anime-floating-toolbar button:hover { background: rgb(148 163 184 / .12); }
.anime-floating-body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 8px 26px 26px; }
.anime-floating-footer { flex: none; padding: 12px 26px 20px; box-shadow: 0 -1px 0 rgb(148 163 184 / .12); }
.anime-floating button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
@media (max-width: 639px) { .anime-floating { width: calc(100vw - 16px); max-height: calc(100dvh - 24px); border-radius: 18px; } .anime-floating-toolbar { padding-inline: 18px 10px; } .anime-floating-body { padding: 8px 18px 18px; } .anime-floating-footer { padding: 12px 18px max(16px, env(safe-area-inset-bottom)); } }
</style>
