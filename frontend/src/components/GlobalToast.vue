<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { Toaster } from 'vue-sonner'
import 'vue-sonner/style.css'
import { useToast } from '../composables/useToast'

const host = ref<HTMLElement>()
const target = shallowRef<HTMLElement>(document.body)
const theme = ref<'light' | 'dark'>('light')
let modalStack: HTMLDialogElement[] = []
let observer: MutationObserver | undefined
let disposed = false

async function syncHost() {
  theme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light'
  const modals = [...document.querySelectorAll<HTMLDialogElement>('dialog:modal')]
  modalStack = [...modalStack.filter(modal => modals.includes(modal)), ...modals.filter(modal => !modalStack.includes(modal))]
  const destination = modalStack.at(-1) || document.body
  if (target.value !== destination) {
    // 通知跟随最上层模态弹窗，保留键盘访问；普通 z-index 无法跨越原生顶层。
    host.value?.hidePopover()
    target.value = destination
  }
  await nextTick()
  if (!disposed && host.value?.isConnected && !host.value.matches(':popover-open')) host.value.showPopover()
}

function keepFocusPaused(event: FocusEvent | MouseEvent) {
  const list = event.currentTarget as HTMLElement
  if (list.contains(document.activeElement)) list.dispatchEvent(new MouseEvent('mouseenter'))
}

function keepKeyboardFocus(event: FocusEvent) {
  // 避免组件聚焦通知列表后立刻还原焦点，子按钮仍保留原生焦点事件。
  if (event.target === event.currentTarget) event.stopImmediatePropagation()
}

function resumeAfterFocus(event: FocusEvent) {
  const list = event.currentTarget as HTMLElement
  if (!list.contains(event.relatedTarget as Node | null) && !list.matches(':hover')) list.dispatchEvent(new MouseEvent('mouseleave'))
}

onMounted(() => {
  observer = new MutationObserver(records => {
    for (const record of records) {
      const modal = record.target
      if (record.attributeName === 'open' && record.oldValue === null && modal instanceof HTMLDialogElement && modal.matches(':modal')) {
        modalStack = [...modalStack.filter(item => item !== modal), modal]
      }
    }
    void syncHost()
  })
  observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['open'], attributeOldValue: true })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  void syncHost()
})
onBeforeUnmount(() => {
  disposed = true
  observer?.disconnect()
  host.value?.hidePopover()
  useToast().clear()
})
</script>

<template>
  <Teleport :to="target">
    <div ref="host" class="toast-host" popover="manual">
      <Toaster position="top-right" :theme="theme" close-button close-button-position="top-right"
        container-aria-label="全局通知" :visible-toasts="3" :gap="10" :offset="16" :mobile-offset="16"
        :toast-options="{ closeButtonAriaLabel: '关闭提示' }"
        @focus.capture="keepKeyboardFocus" @focusin="keepFocusPaused" @focusout="resumeAfterFocus" @mouseleave="keepFocusPaused" />
    </div>
  </Teleport>
</template>

<style scoped>
.toast-host { position: fixed; inset: 0; width: 100%; height: 100%; margin: 0; padding: 0; border: 0; overflow: visible; background: transparent; color: inherit; pointer-events: none; }
.toast-host::backdrop { background: transparent; pointer-events: none; }
.toast-host :deep([data-sonner-toaster]) {
  width: min(24rem, calc(100% - 2rem - env(safe-area-inset-left, 0px) - env(safe-area-inset-right, 0px)));
  top: max(1rem, env(safe-area-inset-top, 0px));
  right: max(1rem, env(safe-area-inset-right, 0px));
  left: auto;
  pointer-events: none;
}
.toast-host :deep([data-sonner-toast]) { width: 100%; padding-right: 44px; border-radius: 14px; pointer-events: none; overflow-wrap: anywhere; }
.toast-host :deep([data-sonner-toast][data-visible='true']:not([data-removed='true'])) { pointer-events: auto; }
.toast-host :deep([data-sonner-toast]:has([data-button])) { display: grid; grid-template-columns: 20px minmax(0, 1fr); align-items: start; }
.toast-host :deep([data-type='success'] [data-icon]) { color: rgb(var(--status-success)); }
.toast-host :deep([data-type='error'] [data-icon]) { color: rgb(var(--status-danger)); }
.toast-host :deep([data-type='warning'] [data-icon]) { color: rgb(var(--status-warning)); }
.toast-host :deep([data-type='info'] [data-icon]) { color: rgb(var(--action-primary)); }
.toast-host :deep([data-content]) { min-width: 0; }
.toast-host :deep([data-title]) { white-space: pre-wrap; }
.toast-host :deep([data-description]) { max-height: 9rem; overflow-y: auto; white-space: pre-wrap; }
.toast-host :deep([data-sonner-toast] [data-button]) { grid-column: 2; justify-self: start; height: 32px; margin: 0; padding-inline: 12px; border-radius: 8px; background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); }
.toast-host :deep([data-close-button]) { top: 8px; right: 8px; width: 28px; height: 28px; transform: none; }
.toast-host :deep([data-sonner-toast]:focus-visible), .toast-host :deep(button:focus-visible) { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
@media (max-width: 639px) {
  .toast-host :deep([data-sonner-toast] [data-button]) { height: 44px; }
}
</style>
