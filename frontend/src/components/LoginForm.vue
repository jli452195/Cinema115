<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'

const props = defineProps<{
  endpoint: '/api/auth/login' | '/api/auth/admin/login'
  title: string
  description: string
  usernameLabel: string
  submitLabel: string
  initialUsername?: string
}>()
const emit = defineEmits<{ loggedIn: [token: string] }>()
const username = ref(props.initialUsername || '')
const password = ref('')
const message = ref('')
const loading = ref(false)
const lifecycle = new AbortController()
onBeforeUnmount(() => lifecycle.abort())

async function login() {
  if (loading.value) return
  loading.value = true
  message.value = ''
  try {
    const response = await fetch(props.endpoint, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: username.value, password: password.value }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(30000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok) throw new Error(data?.error || '登录失败，请检查账号和密码')
    if (typeof data?.token !== 'string' || !data.token) throw new Error('登录响应异常，请重试')
    if (!lifecycle.signal.aborted) emit('loggedIn', data.token)
  } catch (error) {
    if (!lifecycle.signal.aborted) message.value = error instanceof Error && !['TypeError', 'TimeoutError'].includes(error.name) ? error.message : '暂时无法登录，请稍后重试'
  } finally { loading.value = false }
}
</script>

<template>
  <form class="space-y-5 rounded-2xl bg-white p-6 shadow-2xl dark:bg-slate-900 sm:p-8" :aria-busy="loading" @submit.prevent="login">
    <div>
      <h2 class="text-xl font-semibold text-slate-900 dark:text-slate-100">{{ title }}</h2>
      <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">{{ description }}</p>
    </div>
    <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">{{ usernameLabel }}<input v-model="username" name="username" required autocomplete="username" maxlength="64" class="mt-2 w-full rounded-lg border border-slate-200 bg-white p-3 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 dark:border-slate-700 dark:bg-slate-950" :placeholder="`输入${usernameLabel}`" /></label>
    <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">密码<input v-model="password" name="password" required type="password" autocomplete="current-password" maxlength="128" class="mt-2 w-full rounded-lg border border-slate-200 bg-white p-3 outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 dark:border-slate-700 dark:bg-slate-950" placeholder="输入密码" /></label>
    <button type="submit" class="w-full rounded-lg bg-blue-600 px-4 py-3 font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-60" :disabled="loading">{{ loading ? '登录中…' : submitLabel }}</button>
    <p v-if="message" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-600 dark:bg-red-400/10 dark:text-red-300">{{ message }}</p>
    <slot />
  </form>
</template>
