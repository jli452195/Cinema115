<script setup lang="ts">
defineProps<{ username: string }>()
const emit = defineEmits<{ openP115: []; openShare: []; logout: [] }>()
</script>

<template>
  <div class="flex min-h-screen bg-slate-100">
    <aside class="flex w-64 shrink-0 flex-col bg-slate-950 p-5 text-slate-100">
      <div class="mb-10 flex items-center gap-3"><div class="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-600 font-bold">C</div><span class="text-xl font-bold">Cinema115</span></div>
      <p class="mb-3 px-3 text-xs font-semibold uppercase tracking-wider text-slate-500">工作台</p>
      <nav class="space-y-2">
        <button class="flex w-full items-center gap-3 rounded-lg bg-blue-600 px-4 py-3 text-left text-sm font-medium shadow-lg shadow-blue-900/20" @click="emit('openP115')"><span>☁</span>115 网盘</button>
      </nav>
      <button class="mt-auto rounded-lg px-4 py-3 text-left text-sm text-slate-400 transition hover:bg-slate-800 hover:text-white" @click="emit('logout')">退出登录</button>
    </aside>
    <main class="min-w-0 flex-1 p-6 md:p-10">
      <div class="mb-8 flex items-start justify-between"><div><p class="text-sm text-slate-500">控制台 / 首页</p><h1 class="mt-2 text-3xl font-semibold text-slate-900">欢迎回来，{{ username }}</h1></div><div class="hidden rounded-full bg-white px-4 py-2 text-sm text-slate-600 shadow-sm md:block">管理员</div></div>
      <div class="grid gap-5 md:grid-cols-2">
        <button class="rounded-2xl bg-white p-6 text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md" @click="emit('openP115')"><div class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-xl">☁</div><h2 class="text-lg font-semibold text-slate-900">115 网盘</h2><p class="mt-2 text-sm text-slate-500">配置连接并管理网盘资源</p><span class="mt-5 inline-block text-sm font-medium text-blue-600">进入功能 →</span></button>
        <button class="rounded-2xl bg-white p-6 text-left shadow-sm transition hover:-translate-y-0.5 hover:shadow-md" @click="emit('openShare')">
          <div class="mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="3" /><path d="m10 8 6 4-6 4V8Z" stroke-linejoin="round" /></svg></div>
          <h2 class="text-lg font-semibold text-slate-900">分享播放 / STRM</h2>
          <p class="mt-2 text-sm text-slate-500">浏览分享文件，直接播放或生成 STRM</p>
          <span class="mt-5 inline-block text-sm font-medium text-blue-600">进入功能 →</span>
        </button>
      </div>
    </main>
  </div>
</template>
