<script setup lang="ts">
import { RouterLink } from 'vue-router'
import SelectMenu from './SelectMenu.vue'
import type { ShareTransferController } from '../composables/useShareTransfer'
defineProps<{ transfer: ShareTransferController }>()
defineEmits<{ settings: [] }>()
</script>

<template>
  <div class="transfer-directory">
    <span v-if="transfer.loading.value">正在读取转存目录…</span>
    <template v-else-if="transfer.error.value"><span role="alert">{{ transfer.error.value }}</span><button type="button" @click="transfer.load()">重试</button></template>
    <template v-else-if="transfer.targets.value.length">
      <span>转存到</span>
      <span v-if="transfer.targets.value.length === 1" class="transfer-directory-name" :title="transfer.target.value?.targetPath">{{ transfer.target.value?.name }}</span>
      <SelectMenu v-else :model-value="transfer.selectedID.value" :options="transfer.options.value" placeholder="请选择目录" label="转存目标目录" density="compact" :title="transfer.target.value?.targetPath" @update:model-value="transfer.select" />
    </template>
    <RouterLink v-else to="/115/cloud-download?tab=share" @click="$emit('settings')">配置转存目录</RouterLink>
  </div>
</template>

<style scoped>
.transfer-directory { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; min-width: 0; font-size: 12px; font-weight: 400; color: var(--search-dialog-muted); }
.transfer-directory :deep(.select-menu) { max-width: 200px; }.transfer-directory-name { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--search-dialog-text); }.transfer-directory a, .transfer-directory button { color: var(--search-dialog-primary, #2563eb); }.transfer-directory a:hover, .transfer-directory button:hover { text-decoration: underline; }
</style>
