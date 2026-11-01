<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { IconAlertTriangle, IconCircleCheck, IconCircleX, IconInfoCircle, IconX } from '@tabler/icons-vue'
import { useToast } from '../composables/useToast'

const { messages, dismiss, pause, resume, clear } = useToast()
const styles = {
  success: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/70 dark:bg-emerald-950/60 dark:text-emerald-300',
  error: 'border-red-200 bg-red-50 text-red-700 dark:border-red-800/70 dark:bg-red-950/60 dark:text-red-300',
  warning: 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/70 dark:bg-amber-950/60 dark:text-amber-300',
  info: 'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-800/70 dark:bg-blue-950/60 dark:text-blue-300',
}
const icons = { success: IconCircleCheck, error: IconCircleX, warning: IconAlertTriangle, info: IconInfoCircle }
const labels = { success: '操作成功', error: '操作失败', warning: '请注意', info: '提示' }

function focusOut(event: FocusEvent, id: number) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)) resume(id, 'focus')
}
onBeforeUnmount(clear)
</script>

<template>
  <Teleport to="body">
    <div class="toast-container pointer-events-none fixed z-[100]" role="region" aria-label="全局通知">
      <TransitionGroup name="toast" tag="div" class="toast-stack">
        <div v-for="message in messages" :key="message.id" :role="message.type === 'error' ? 'alert' : 'status'" aria-atomic="true"
          class="toast-card pointer-events-auto flex w-full items-start gap-3 rounded-2xl border border-slate-200 bg-white p-3.5 shadow-lg shadow-slate-900/10 dark:border-slate-700 dark:bg-slate-900 dark:shadow-black/30"
          @mouseenter="pause(message.id, 'hover')" @mouseleave="resume(message.id, 'hover')" @focusin="pause(message.id, 'focus')" @focusout="focusOut($event, message.id)">
          <span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl border" :class="styles[message.type]"><component :is="icons[message.type]" :size="19" aria-hidden="true" /></span>
          <div class="min-w-0 flex-1">
            <p class="toast-copy text-sm font-semibold leading-5 text-slate-900 dark:text-slate-100">{{ message.title || labels[message.type] }}</p>
            <p class="toast-copy mt-0.5 max-h-36 overflow-y-auto whitespace-pre-wrap text-xs leading-5 text-slate-600 dark:text-slate-300">{{ message.message }}</p>
          </div>
          <button type="button" aria-label="关闭提示" class="-mr-1 -mt-1 grid h-8 w-8 shrink-0 place-items-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 dark:hover:bg-slate-800 dark:hover:text-slate-100" @click="dismiss(message.id)"><IconX :size="16" aria-hidden="true" /></button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container { right: max(1rem, env(safe-area-inset-right, 0px)); top: max(1rem, env(safe-area-inset-top, 0px)); width: min(24rem, calc(100vw - 2rem - env(safe-area-inset-left, 0px) - env(safe-area-inset-right, 0px))); }
.toast-stack { position: relative; display: flex; flex-direction: column; gap: .625rem; }
.toast-copy { overflow-wrap: anywhere; }
.toast-enter-active, .toast-leave-active, .toast-move { transition: opacity 220ms ease, transform 220ms cubic-bezier(.22, 1, .36, 1); }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateY(-12px) scale(.96); }
.toast-leave-active { position: absolute; }
@media (prefers-reduced-motion: reduce) {
  .toast-enter-active, .toast-leave-active, .toast-move { transition: none; }
}
</style>
