<script setup lang="ts">
import { useId } from 'vue'

const value = defineModel<boolean>({ required: true })
defineProps<{ label: string; description?: string; disabled?: boolean }>()
const descriptionID = useId()
</script>

<template>
  <div>
    <label class="setting-switch">
      <input v-model="value" :disabled="disabled" type="checkbox" role="switch" :aria-label="label" :aria-describedby="description ? descriptionID : undefined" class="peer sr-only" />
      <span class="setting-switch-track" aria-hidden="true"></span>
      <span class="min-w-0"><span class="setting-switch-label">{{ label }}</span><span v-if="description" :id="descriptionID" class="setting-switch-description">{{ description }}</span></span>
    </label>
    <slot />
  </div>
</template>

<style scoped>
.setting-switch { display: flex; align-items: flex-start; gap: 12px; cursor: pointer; }
.setting-switch-track { position: relative; flex-shrink: 0; width: 36px; height: 20px; margin-top: 2px; border-radius: 9999px; background: rgb(var(--line-control)); transition: background-color 160ms; }
.setting-switch-track::after { content: ''; position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: inherit; background: rgb(var(--action-on-primary)); box-shadow: 0 1px 2px rgb(0 0 0 / .15); transition: transform 160ms; }
.setting-switch input:checked + .setting-switch-track { background: rgb(var(--action-primary)); box-shadow: inset 0 0 0 1px rgb(var(--action-link)); }
.setting-switch input:checked + .setting-switch-track::after { transform: translateX(16px); }
.setting-switch input:focus-visible + .setting-switch-track { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
/* 原生禁用状态同时覆盖外层 fieldset，避免仅依赖组件属性。 */
.setting-switch:has(input:disabled) { cursor: not-allowed; }
.setting-switch input:disabled + .setting-switch-track { opacity: .5; }
.setting-switch-label { font-size: 14px; font-weight: 500; line-height: 20px; color: rgb(var(--content-primary)); overflow-wrap: anywhere; }
.setting-switch-description { display: block; margin-top: 4px; font-size: 12px; line-height: 24px; color: rgb(var(--content-secondary)); overflow-wrap: anywhere; }
@media (max-width: 639px) { .setting-switch { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .setting-switch-track, .setting-switch-track::after { transition: none; } }
@media (forced-colors: active) {
  .setting-switch-track { forced-color-adjust: none; border: 1px solid ButtonText; background: Canvas; }
  .setting-switch-track::after { top: 1px; left: 1px; background: ButtonText; box-shadow: none; }
  .setting-switch input:checked + .setting-switch-track { border-color: Highlight; background: Highlight; box-shadow: none; }
  .setting-switch input:checked + .setting-switch-track::after { background: HighlightText; }
  .setting-switch input:focus-visible + .setting-switch-track { outline-color: Highlight; }
  .setting-switch input:disabled + .setting-switch-track { border-color: GrayText; opacity: 1; }
  .setting-switch input:disabled + .setting-switch-track::after { background: GrayText; }
}
</style>
