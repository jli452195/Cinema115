<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef } from 'vue'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
import ConfirmCard from './ConfirmCard.vue'

defineProps<{
  message: string
  cancelLabel?: string
  confirmLabel: string
}>()

const emit = defineEmits<{
  cancel: []
  confirm: []
}>()

const dialogRef = useTemplateRef<HTMLDialogElement>('dialogRef')
// 保持默认 coverParent=false，确认时让下层弹层清晰可识别但禁止交互
const lifecycle = createDialogLifecycle()
let unmounting = false

function handleNativeCancel(event: Event) {
  // 阻止浏览器直接关闭原生 dialog，统一交由父组件控制卸载
  if (!event.cancelable) return
  event.preventDefault()
  emit('cancel')
}

function handleNativeClose() {
  // 仅在非程序卸载时兜底转成取消，避免卸载阶段重复触发事件
  if (!unmounting) emit('cancel')
}

onMounted(() => {
  const dialog = dialogRef.value
  if (!dialog) return
  // 先记录焦点与滚动锁，再打开原生模态层触发安全按钮 autofocus
  lifecycle.acquire()
  if (dialog.isConnected && !dialog.open) {
    dialog.showModal()
  }
})

onBeforeUnmount(() => {
  unmounting = true
  const dialog = dialogRef.value
  // 先关闭原生模态层解除下层 inert，再释放滚动锁并归还焦点
  if (dialog?.open) {
    dialog.close()
  }
  lifecycle.release()
})
</script>

<template>
  <Teleport to="body">
    <dialog
      ref="dialogRef"
      class="alert-dialog"
      role="alertdialog"
      aria-modal="true"
      :aria-label="message"
      @cancel="handleNativeCancel"
      @close="handleNativeClose"
    >
      <ConfirmCard
        :message="message"
        :cancel-label="cancelLabel"
        :confirm-label="confirmLabel"
        autofocus
        @cancel="emit('cancel')"
        @confirm="emit('confirm')"
      />
    </dialog>
  </Teleport>
</template>

<style scoped>
.alert-dialog {
  box-sizing: border-box;
  margin: auto;
  padding: 0;
  width: min(23.5rem, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
  border: 0;
  background: transparent;
  overflow: auto;
}

.alert-dialog::backdrop {
  background: rgb(0 0 0 / 0.48);
}
</style>
