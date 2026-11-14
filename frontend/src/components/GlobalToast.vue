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
let resizeObserver: ResizeObserver | undefined
const observedContents = new Set<Element>()
let disposed = false

function syncToastLayout() {
  // Sonner 只在挂载时缓存高度，同 ID 更新内容后同步展开间距与收起卡片高度。
  const contents = new Set(host.value?.querySelectorAll('[data-content]'))
  for (const content of observedContents) {
    if (!contents.has(content)) {
      resizeObserver?.unobserve(content)
      observedContents.delete(content)
    }
  }
  for (const content of contents) {
    if (!observedContents.has(content)) {
      resizeObserver?.observe(content)
      observedContents.add(content)
    }
  }
  for (const list of host.value?.querySelectorAll<HTMLElement>('[data-sonner-toaster]') || []) {
    let offset = 0
    const gap = parseFloat(getComputedStyle(list).getPropertyValue('--gap')) || 0
    for (const [index, toast] of [...list.querySelectorAll<HTMLElement>('[data-sonner-toast]')].entries()) {
      const previousHeight = toast.style.height
      toast.style.height = 'auto'
      const height = toast.offsetHeight
      toast.style.height = previousHeight
      toast.style.setProperty('--measured-height', `${height}px`)
      toast.style.setProperty('--measured-offset', `${offset}px`)
      if (index === 0) list.style.setProperty('--measured-front-height', `${height}px`)
      offset += height + gap
    }
  }
}

async function syncHost(refreshLayout = false) {
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
  if (!disposed && refreshLayout) syncToastLayout()
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
  resizeObserver = new ResizeObserver(syncToastLayout)
  observer = new MutationObserver(records => {
    for (const record of records) {
      const modal = record.target
      if (record.attributeName === 'open' && record.oldValue === null && modal instanceof HTMLDialogElement && modal.matches(':modal')) {
        modalStack = [...modalStack.filter(item => item !== modal), modal]
      }
    }
    void syncHost(records.some(record => host.value?.contains(record.target)))
  })
  observer.observe(document.body, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ['open'], attributeOldValue: true })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  void syncHost(true)
})
onBeforeUnmount(() => {
  disposed = true
  observer?.disconnect()
  resizeObserver?.disconnect()
  observedContents.clear()
  host.value?.hidePopover()
  useToast().clear()
})
</script>

<template>
  <Teleport :to="target">
    <div ref="host" class="toast-host" popover="manual">
      <Toaster position="top-right" :theme="theme" close-button close-button-position="top-right"
        :swipe-directions="['left', 'right', 'top']"
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
:global(body:has(.workbench-header) .toast-host [data-sonner-toaster]) {
  top: calc(var(--workbench-header-height) + 1px + 1rem);
}
.toast-host :deep([data-sonner-toast]) { width: 100%; padding-right: 44px; border-radius: 14px; pointer-events: none; overflow-wrap: anywhere; }
.toast-host :deep([data-sonner-toast][data-expanded='false'][data-front='false']) { height: var(--measured-front-height, var(--front-toast-height)); }
.toast-host :deep([data-sonner-toast][data-mounted='true'][data-expanded='true']) { --offset: var(--measured-offset, 0px) !important; height: var(--measured-height, auto); }
.toast-host :deep([data-sonner-toast][data-visible='true']:not([data-removed='true'])) { pointer-events: auto; }
.toast-host :deep([data-sonner-toast]:has([data-button])) { display: grid; grid-template-columns: 20px minmax(0, 1fr); align-items: start; }
.toast-host :deep([data-type='success'] [data-icon]) { color: rgb(var(--status-success)); }
.toast-host :deep([data-type='error'] [data-icon]) { color: rgb(var(--status-danger)); }
.toast-host :deep([data-type='warning'] [data-icon]) { color: rgb(var(--status-warning)); }
.toast-host :deep([data-type='info'] [data-icon]) { color: rgb(var(--action-primary)); }
.toast-host :deep([data-content]) { min-width: 0; }
.toast-host :deep([data-title]) { white-space: pre-wrap; }
.toast-host :deep([data-description]) { max-height: 9rem; overflow-y: auto; white-space: pre-wrap; }
.toast-host :deep(.task-resource-summary) { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.toast-host :deep([data-sonner-toast] [data-button]) { grid-column: 2; justify-self: start; height: 32px; margin: 0; padding-inline: 12px; border-radius: 8px; background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); }
.toast-host :deep([data-close-button]) { top: 8px; right: 8px; width: 28px; height: 28px; transform: none; }
.toast-host :deep([data-sonner-toast]:focus-visible), .toast-host :deep(button:focus-visible) { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
@media (max-width: 639px) {
  .toast-host :deep([data-sonner-toast]) { padding-right: 48px; }
  .toast-host :deep([data-close-button])::after { content: ''; position: absolute; inset: -8px; }
  .toast-host :deep([data-sonner-toast] [data-button]) { height: 44px; }
}
</style>
