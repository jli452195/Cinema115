<script setup lang="ts">
import WorkbenchSidebar from '../components/WorkbenchSidebar.vue'
defineProps<{ username: string; category: 'p115' | 'telegram' }>()
const emit = defineEmits<{ category: [value: 'p115' | 'telegram']; openP115: []; openShare: []; openTelegram: []; openLogs: []; logout: [] }>()
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-100 md:flex-row">
    <WorkbenchSidebar :active="category" @home="emit('category', 'p115')" @open-p115="emit('category', 'p115')" @open-telegram="emit('category', 'telegram')" @open-logs="emit('openLogs')" @logout="emit('logout')" />
    <main class="min-w-0 flex-1 p-6 md:p-10">
      <div class="mb-8 flex items-start justify-between gap-4"><div><p class="text-sm text-slate-500">控制台 / {{ category === 'p115' ? '115 网盘' : 'Telegram 配置' }}</p><h1 class="mt-2 text-3xl font-semibold text-slate-900">{{ category === 'p115' ? '115 网盘' : 'Telegram 配置' }}</h1><p class="mt-3 text-sm leading-6 text-slate-500">{{ category === 'p115' ? '管理网盘连接、分享播放与 STRM 生成。' : '配置 Telegram 机器人与消息接收。' }}</p></div><div class="hidden rounded-full bg-white px-4 py-2 text-sm text-slate-600 shadow-sm md:block">{{ username }}</div></div>
      <div class="grid gap-5 md:grid-cols-2">
        <template v-if="category === 'p115'">
        <button class="rounded-2xl bg-white p-6 text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md" @click="emit('openP115')"><div class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-xl">☁</div><h2 class="text-lg font-semibold text-slate-900">115 网盘</h2><p class="mt-2 text-sm text-slate-500">配置连接并管理网盘资源</p><span class="mt-5 inline-block text-sm font-medium text-blue-600">进入功能 →</span></button>
        <button class="rounded-2xl bg-white p-6 text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md" @click="emit('openShare')">
          <div class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3" /><path d="m10 8 6 4-6 4V8Z" stroke-linejoin="round" /></svg></div>
          <h2 class="text-lg font-semibold text-slate-900">分享播放 / STRM</h2>
          <p class="mt-2 text-sm text-slate-500">浏览分享文件，直接播放或生成 STRM</p>
          <span class="mt-5 inline-block text-sm font-medium text-blue-600">进入功能 →</span>
        </button>
        </template>
        <button v-else class="rounded-2xl bg-white p-6 text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md" @click="emit('openTelegram')">
          <div class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m21 3-5 18-5-7-8-3L21 3Z" stroke-linejoin="round"/><path d="m11 14 5-6"/></svg></div>
          <h2 class="text-lg font-semibold text-slate-900">Bot 配置</h2>
          <p class="mt-2 text-sm text-slate-500">配置机器人 Token、允许的聊天与消息接收</p>
          <span class="mt-5 inline-block text-sm font-medium text-blue-600">进入功能 →</span>
        </button>
      </div>
    </main>
  </div>
</template>
