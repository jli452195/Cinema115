<script setup lang="ts">
import { useTemplateRef } from 'vue'

withDefaults(
  defineProps<{
    message: string
    cancelLabel?: string
    confirmLabel: string
    cancelDisabled?: boolean
    confirmDisabled?: boolean
    autofocus?: boolean
  }>(),
  {
    cancelLabel: '继续编辑',
    cancelDisabled: false,
    confirmDisabled: false,
    autofocus: false,
  },
)

const emit = defineEmits<{
  cancel: []
  confirm: []
}>()

const cancelButton = useTemplateRef<HTMLButtonElement>('cancelButton')

// 供外层在打开确认卡时把焦点移到安全按钮
function focusCancel() {
  cancelButton.value?.focus({ preventScroll: true })
}

defineExpose({ focusCancel })
</script>

<template>
  <div class="confirm-card">
    <p class="confirm-card__message">{{ message }}</p>
    <div class="confirm-card__actions">
      <button
        ref="cancelButton"
        type="button"
        class="confirm-card__button confirm-card__button--cancel"
        :disabled="cancelDisabled"
        :autofocus="autofocus"
        @click="emit('cancel')"
      >
        {{ cancelLabel }}
      </button>
      <button
        type="button"
        class="confirm-card__button confirm-card__button--danger"
        :disabled="confirmDisabled"
        @click="emit('confirm')"
      >
        {{ confirmLabel }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.confirm-card {
  box-sizing: border-box;
  padding: 16px;
  border-radius: 12px;
  background: var(--confirm-card-surface, rgb(var(--surface-elevated)));
  color: var(--confirm-card-ink, rgb(var(--content-primary)));
  box-shadow: 0 4px 18px rgb(15 23 42 / 0.1);
  font-size: 13px;
  line-height: 1.7;
}

.confirm-card__message {
  margin: 0;
  overflow-wrap: anywhere;
}

.confirm-card__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

.confirm-card__button {
  box-sizing: border-box;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 14px;
  border: 0;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.25;
  white-space: nowrap;
  cursor: pointer;
}

.confirm-card__button:focus-visible {
  outline: 2px solid rgb(var(--focus));
  outline-offset: 2px;
}

.confirm-card__button:hover:not(:disabled) {
  filter: brightness(.94);
}

.confirm-card__button:disabled {
  opacity: 0.45;
  cursor: default;
}

.confirm-card__button--cancel {
  background: var(--confirm-card-neutral, rgb(var(--surface-hover)));
  color: inherit;
}

.confirm-card__button--danger {
  background: rgb(var(--status-danger-surface));
  color: rgb(var(--status-danger));
}
</style>
