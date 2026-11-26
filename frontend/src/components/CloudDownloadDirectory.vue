<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import SelectMenu from './SelectMenu.vue'
import type { CloudDownloadTargetController } from '../composables/useCloudDownloadTarget'

const props = withDefaults(defineProps<{
  controller: CloudDownloadTargetController
  label?: string
  variant?: 'inline' | 'panel'
  disabled?: boolean
}>(), {
  label: '云下载到',
  variant: 'inline',
})
defineEmits<{ settings: [] }>()

const singleLocked = computed(() => props.controller.targets.value.length === 1 && !!props.controller.selectedID.value && !props.controller.reconfirmMessage.value)
const placeholder = computed(() => props.controller.reconfirmMessage.value ? '请重新确认目录' : '请选择下载目录')
</script>

<template>
  <div class="cloud-directory" :class="`cloud-directory-${variant}`">
    <div class="cloud-directory-main">
      <span v-if="controller.loading.value && !controller.loaded.value" class="cloud-directory-muted">正在读取下载目录…</span>
      <template v-else-if="controller.error.value">
        <span role="alert" class="cloud-directory-error">{{ controller.error.value }}</span>
        <button type="button" class="cloud-directory-link" :disabled="disabled || controller.loading.value" @click="controller.load()">重试</button>
      </template>
      <template v-else-if="controller.targets.value.length">
        <span class="cloud-directory-label">{{ label }}</span>
        <span v-if="singleLocked" class="cloud-directory-name" :title="controller.target.value?.targetPath">
          <strong>{{ controller.target.value?.name }}</strong>
          <small v-if="variant === 'panel' && controller.target.value">{{ controller.target.value.targetPath }}</small>
        </span>
        <SelectMenu
          v-else
          :model-value="controller.selectedID.value"
          :options="controller.options.value"
          :placeholder="placeholder"
          :label="label"
          density="compact"
          :disabled="disabled || controller.loading.value"
          :title="controller.target.value?.targetPath"
          @update:model-value="controller.select"
        />
        <span v-if="variant === 'panel' && !singleLocked && controller.target.value" class="cloud-directory-path" :title="controller.target.value.targetPath">{{ controller.target.value.targetPath }}</span>
      </template>
      <template v-else>
        <span v-if="variant === 'panel'">请先在下方新增磁力/ED2K 转存目录</span>
        <RouterLink v-else to="/115/cloud-download" class="cloud-directory-link" @click="$emit('settings')">配置下载目录</RouterLink>
      </template>
    </div>
    <p v-if="controller.reconfirmMessage.value" role="status" class="cloud-directory-reconfirm">{{ controller.reconfirmMessage.value }}</p>
  </div>
</template>

<style scoped>
.cloud-directory { --cloud-directory-text: #1e293b; --cloud-directory-muted: #64748b; --cloud-directory-link: #2563eb; min-width: 0; font-size: 12px; font-weight: 400; color: var(--search-dialog-muted, var(--cloud-directory-muted)); }
.dark .cloud-directory { --cloud-directory-text: #e2e8f0; --cloud-directory-muted: #94a3b8; --cloud-directory-link: #93c5fd; }
.cloud-directory-main { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; min-width: 0; }
.cloud-directory-inline :deep(.select-menu) { max-width: 200px; }
.cloud-directory-panel :deep(.select-menu) { min-width: 180px; max-width: 260px; }
.cloud-directory-label { flex-shrink: 0; }
.cloud-directory-name { display: inline-flex; flex-wrap: wrap; align-items: baseline; gap: 2px 8px; min-width: 0; max-width: 100%; color: var(--search-dialog-text, var(--cloud-directory-text)); }
.cloud-directory-inline .cloud-directory-name { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cloud-directory-name strong { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cloud-directory-name small, .cloud-directory-path { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; color: var(--search-dialog-muted, var(--cloud-directory-muted)); }
.cloud-directory-muted { color: var(--search-dialog-muted, var(--cloud-directory-muted)); }
.cloud-directory-error { color: #b91c1c; }
.dark .cloud-directory-error { color: #fca5a5; }
.cloud-directory-reconfirm { margin: 4px 0 0; font-size: 12px; line-height: 1.5; color: #b45309; }
.dark .cloud-directory-reconfirm { color: #fcd34d; }
.cloud-directory-link { color: var(--search-dialog-primary, var(--cloud-directory-link)); text-decoration: none; }
.cloud-directory-link:hover { text-decoration: underline; }
.cloud-directory-link:focus-visible { outline: 2px solid var(--search-dialog-primary, var(--cloud-directory-link)); outline-offset: 2px; border-radius: 4px; }
</style>
