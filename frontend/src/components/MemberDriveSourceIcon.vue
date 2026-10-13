<script setup lang="ts">
defineProps<{ source: string }>()
</script>
<template>
 <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
  <template v-if="source === 'assigned'"><rect x="3" y="3" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="7" rx="2" /><path d="M7 6.5h.01M7 17.5h.01M12 6.5h5M12 17.5h5M12 10v4" /></template>
  <template v-else-if="source === 'personal' || source === 'personal_shared'"><path d="M7.5 17H6a4 4 0 0 1-.8-7.9A7 7 0 0 1 18.8 8a4.5 4.5 0 0 1 .2 8.9h-2.5" /><circle cx="12" cy="14" r="2.5" /><path d="M7.5 21v-.8a4.5 4.5 0 0 1 9 0v.8" /></template>
  <template v-else><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M3 14h18M7 17h.01M11 17h6" /><circle cx="12" cy="9" r="2" /></template>
 </svg>
</template>
<style scoped>
svg { width: 19px; height: 19px; }
</style>
