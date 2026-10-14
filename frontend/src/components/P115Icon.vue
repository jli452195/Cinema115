<script setup lang="ts">
defineProps<{ name: 'drive' | 'pool' | 'refresh' | 'settings' | 'chart' | 'plus' | 'more' | 'warning' | 'clock' | 'close' | 'qr' | 'play' | 'external' | 'folder' | 'users' | 'user' | 'shield' | 'help' | 'check' | 'key' }>()
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
    <template v-else-if="name === 'play'"><rect x="3" y="4" width="18" height="14" rx="3" /><path d="m10 8 5 3-5 3ZM8 22h8M12 18v4" /></template>
    <path v-else-if="name === 'external'" d="M14 3h7v7M21 3l-9 9M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5" />
    <path v-else-if="name === 'folder'" d="M3 6V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
    <template v-else-if="name === 'users'"><circle cx="9" cy="8" r="3" /><path d="M3 21v-3a6 6 0 0 1 12 0v3M16 5a3 3 0 0 1 0 6M18 15a5 5 0 0 1 3 5" /></template>
    <template v-else-if="name === 'user'"><circle cx="12" cy="8" r="3.5" /><path d="M5 21v-2a7 7 0 0 1 14 0v2" /></template>
    <path v-else-if="name === 'shield'" d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6ZM8 12l3 3 5-6" />
    <template v-else-if="name === 'help'"><circle cx="12" cy="12" r="9" /><path d="M9 9a3 3 0 0 1 6 0c0 2-3 2-3 5M12 17h.01" /></template>
    <path v-else-if="name === 'check'" d="m5 12 4 4L19 6" />
    <template v-else-if="name === 'key'"><circle cx="8" cy="8" r="5" /><path d="m12 12 9 9M17 17l3-3M14 14l2-2" /></template>
  </svg>
</template>
