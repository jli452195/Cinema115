<script setup lang="ts">
import { useData } from 'vitepress'
import { inject } from 'vue'

const { isDark } = useData()
const toggle = inject<(event: MouseEvent) => Promise<void>>('toggle-appearance')!
</script>

<template>
  <button type="button" class="appearance-demo" @click="toggle">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path v-if="isDark" d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79Z" />
      <template v-else><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5" /></template>
    </svg>
    切换为{{ isDark ? '浅色' : '深色' }}，体验主题动效
  </button>
</template>

<style scoped>
.appearance-demo { display: inline-flex; align-items: center; gap: 10px; min-height: 44px; padding: 10px 18px; border: 1px solid var(--vp-c-brand-1); border-radius: 10px; color: var(--vp-c-brand-1); background: var(--vp-c-brand-soft); font-size: 14px; font-weight: 600; cursor: pointer; }
.appearance-demo svg { width: 20px; height: 20px; }
.appearance-demo:hover { background: var(--vp-c-default-soft); }
.appearance-demo:focus-visible { outline: 2px solid var(--vp-c-brand-1); outline-offset: 3px; }
</style>
