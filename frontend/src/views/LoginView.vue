<script setup lang="ts">
import { ref } from 'vue'

const emit = defineEmits<{ loggedIn: [token: string] }>()
const username = ref('')
const password = ref('')
const message = ref('')
const loading = ref(false)

async function login() {
  loading.value = true
  message.value = ''
  try {
    const response = await fetch('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: username.value, password: password.value }) })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error ?? '登录失败')
    emit('loggedIn', data.token)
  } catch (error) {
    message.value = error instanceof Error ? error.message : String(error)
  } finally { loading.value = false }
}
</script>
<template>
  <main class="flex min-h-screen items-center justify-center bg-slate-950 px-4 py-12">
    <div class="w-full max-w-md">
      <div class="mb-8 text-center text-white">
        <div class="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-600 text-2xl font-bold shadow-lg shadow-blue-600/30">C</div>
        <h1 class="text-3xl font-semibold tracking-tight">Cinema115</h1>
        <p class="mt-2 text-sm text-slate-400">媒体资源管理控制台</p>
      </div>
      <form class="space-y-5 rounded-2xl bg-white p-8 shadow-2xl" @submit.prevent="login">
        <div>
          <h2 class="text-xl font-semibold text-slate-900">管理员登录</h2>
          <p class="mt-1 text-sm text-slate-500">登录后管理你的网盘与媒体资源</p>
        </div>
        <label class="block text-sm font-medium text-slate-700">账号<input v-model="username" required class="mt-2 w-full rounded-lg border border-slate-200 p-3 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-100" placeholder="输入管理员账号" /></label>
        <label class="block text-sm font-medium text-slate-700">密码<input v-model="password" required type="password" class="mt-2 w-full rounded-lg border border-slate-200 p-3 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-100" placeholder="输入管理员密码" /></label>
        <button class="w-full rounded-lg bg-blue-600 px-4 py-3 font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60" :disabled="loading">{{ loading ? '登录中…' : '登录' }}</button>
        <p v-if="message" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">{{ message }}</p>
      </form>
    </div>
  </main>
</template>
