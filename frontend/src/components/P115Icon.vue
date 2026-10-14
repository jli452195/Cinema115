<script setup lang="ts">
defineProps<{ name: 'drive' | 'pool' | 'refresh' | 'settings' | 'chart' | 'plus' | 'more' | 'warning' | 'clock' | 'close' | 'qr' }>()
</script>
<template>
  <svg aria-hidden="true" class="h-[18px] w-[18px] shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
    <template v-if="name === 'drive'"><rect x="3" y="5" width="18" height="14" rx="3" /><path d="M3 13h18M7 16h.01M11 16h.01" /></template>
    <template v-else-if="name === 'pool'"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></template>
    <path v-else-if="name === 'refresh'" d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1" />
    <template v-else-if="name === 'settings'"><path d="M4 7h1m6 0h9M4 17h9m6 0h1" /><circle cx="8" cy="7" r="3" /><circle cx="16" cy="17" r="3" /></template>
    <path v-else-if="name === 'chart'" d="M4 3v17h17M8 15v-4M13 15V7M18 15v-6" />
    <path v-else-if="name === 'plus'" d="M12 5v14M5 12h14" />
    <template v-else-if="name === 'more'"><circle cx="5" cy="12" r="1" /><circle cx="12" cy="12" r="1" /><circle cx="19" cy="12" r="1" /></template>
    <path v-else-if="name === 'warning'" d="m10.4 4.2-8 14A1.9 1.9 0 0 0 4 21h16a1.9 1.9 0 0 0 1.6-2.8l-8-14a1.9 1.9 0 0 0-3.2 0ZM12 9v4M12 17h.01" />
    <template v-else-if="name === 'clock'"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></template>
    <path v-else-if="name === 'close'" d="m6 6 12 12M6 18 18 6" />
    <path v-else-if="name === 'qr'" d="M3 3h6v6H3ZM15 3h6v6h-6ZM3 15h6v6H3ZM15 15h3v3h3v3M21 12v3M12 3v3M12 12h4M12 18v3" />
  </svg>
</template>
