<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { useToast } from '../composables/useToast'

const { messages, dismiss, pause, resume, clear } = useToast()
const styles = {
  success: 'border-emerald-200 text-emerald-700 dark:border-emerald-800 dark:text-emerald-300',
  error: 'border-red-200 text-red-700 dark:border-red-800 dark:text-red-300',
  warning: 'border-amber-200 text-amber-700 dark:border-amber-800 dark:text-amber-300',
  info: 'border-blue-200 text-blue-700 dark:border-blue-800 dark:text-blue-300',
}
const labels = { success: '成功', error: '错误', warning: '提醒', info: '提示' }
onBeforeUnmount(clear)
</script>

<template>
  <Teleport to="body">
    <div class="toast-container pointer-events-none fixed inset-x-0 z-[100] flex flex-col items-center gap-3 px-4">
      <TransitionGroup name="toast">
        <div v-for="message in messages" :key="message.id" :role="message.type === 'error' ? 'alert' : 'status'" aria-atomic="true" class="pointer-events-auto flex w-full max-w-xl items-start gap-3 rounded-xl border bg-white px-4 py-3 shadow-lg dark:bg-slate-900" :class="styles[message.type]" @mouseenter="pause(message.id)" @mouseleave="resume(message.id)" @focusin="pause(message.id)" @focusout="resume(message.id)">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" class="mt-0.5 h-5 w-5 shrink-0" aria-hidden="true">
            <circle cx="12" cy="12" r="9" />
            <path v-if="message.type === 'success'" d="m8 12 3 3 5-6" stroke-linecap="round" stroke-linejoin="round" />
            <path v-else-if="message.type === 'error'" d="m9 9 6 6m0-6-6 6" stroke-linecap="round" />
            <path v-else d="M12 7v6m0 3v1" stroke-linecap="round" />
          </svg>
          <p class="min-w-0 flex-1 whitespace-pre-wrap break-words text-sm leading-6"><span class="sr-only">{{ labels[message.type] }}：</span>{{ message.message }}</p>
          <button type="button" aria-label="关闭提示" class="-mr-1 rounded-md p-1 opacity-60 transition hover:bg-slate-100 hover:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 dark:hover:bg-slate-800" @click="dismiss(message.id)">
            <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" class="h-4 w-4" aria-hidden="true"><path d="m5 5 10 10M15 5 5 15" stroke-linecap="round" /></svg>
          </button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container { top: calc(env(safe-area-inset-top, 0px) + 1rem); }
.toast-enter-active, .toast-leave-active, .toast-move { transition: opacity 180ms ease, transform 180ms ease; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateY(-0.75rem); }
@media (prefers-reduced-motion: reduce) {
  .toast-enter-active, .toast-leave-active, .toast-move { transition: none; }
}
</style>
