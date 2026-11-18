<script setup lang="ts">
import { IconArrowsMaximize, IconArrowsMinimize } from '@tabler/icons-vue'
defineProps<{ expanded: boolean }>()
</script>

<template>
  <button type="button" class="bottom-sheet-expand" :aria-label="expanded ? '恢复弹窗高度' : '展开弹窗'" :aria-pressed="expanded"><component :is="expanded ? IconArrowsMinimize : IconArrowsMaximize" :size="18" aria-hidden="true" /></button>
</template>

<style scoped>
.bottom-sheet-expand { display: none; }
@media (max-width: 639px) {
  .bottom-sheet-expand { display: grid; flex: none; width: 44px; height: 44px; place-items: center; border-radius: 10px; }
  .bottom-sheet-expand:active { background: rgb(var(--surface-hover) / .65); }
}
</style>
