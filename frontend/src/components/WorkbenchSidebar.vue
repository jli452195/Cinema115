<script setup lang="ts">
import { RouterLink } from 'vue-router'
defineProps<{ active: 'dashboard' | 'p115' | 'telegram' | 'logs' | 'emby' }>()
const emit = defineEmits<{ home: []; openDashboard: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
</script>

<template>
  <aside class="flex w-full shrink-0 flex-col bg-slate-950 p-4 text-slate-100 md:sticky md:top-0 md:h-screen md:w-64 md:p-5">
    <button class="mb-5 flex items-center gap-3 text-left md:mb-10" aria-label="返回首页" @click="emit('home')"><span class="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-600 font-bold">C</span><span class="text-xl font-bold">Cinema115</span></button>
    <p class="mb-3 px-3 text-xs font-semibold tracking-wider text-slate-500">工作台</p>
    <nav class="grid grid-cols-2 gap-2 md:grid-cols-1">
      <button class="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium transition" :class="active === 'dashboard' ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20' : 'text-slate-300 hover:bg-slate-800'" :aria-current="active === 'dashboard' ? 'page' : undefined" @click="emit('openDashboard')"><svg class="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>看板</button>
      <button class="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium transition" :class="active === 'p115' ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20' : 'text-slate-300 hover:bg-slate-800'" :aria-current="active === 'p115' ? 'page' : undefined" @click="emit('openP115')"><span aria-hidden="true">☁</span>115 网盘</button>
      <button class="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium transition" :class="active === 'telegram' ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20' : 'text-slate-300 hover:bg-slate-800'" :aria-current="active === 'telegram' ? 'page' : undefined" @click="emit('openTelegram')"><svg class="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m21 3-5 18-5-7-8-3L21 3Z" stroke-linejoin="round"/><path d="m11 14 5-6"/></svg>Telegram 配置</button>
      <button class="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium transition" :class="active === 'logs' ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20' : 'text-slate-300 hover:bg-slate-800'" :aria-current="active === 'logs' ? 'page' : undefined" @click="emit('openLogs')"><svg class="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/></svg>查看日志</button>
      <RouterLink to="/emby" class="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium transition" :class="active === 'emby' ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/20' : 'text-slate-300 hover:bg-slate-800'" :aria-current="active === 'emby' ? 'page' : undefined"><svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="4" width="18" height="14" rx="2"/><path d="m10 8 5 3-5 3ZM8 21h8"/></svg>Emby</RouterLink>
    </nav>
    <button class="mt-3 rounded-lg px-4 py-2 text-left text-sm text-slate-400 transition hover:bg-slate-800 hover:text-white md:mt-auto md:py-3" @click="emit('logout')">退出登录</button>
  </aside>
</template>
