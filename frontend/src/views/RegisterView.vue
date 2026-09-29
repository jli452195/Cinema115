<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

const emit = defineEmits<{ registered: [username: string] }>()
const route = useRoute(), router = useRouter()
const code = ref(new URLSearchParams(route.hash.slice(1)).get('invite') || '')
const username = ref(''), password = ref(''), confirmation = ref('')
const message = ref(''), notice = ref(''), busy = ref(false)
const lifecycle = new AbortController()
async function request(path: string, input: object) {
  const response = await fetch(`/api/auth/${path}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '注册服务暂时不可用')
  return data
}
async function check() {
  if (busy.value || !code.value.trim()) return
  busy.value = true; message.value = ''; notice.value = ''
  try { await request('invite/check', { code: code.value }); notice.value = '邀请码有效，完成注册后使用。' }
  catch (error) { if (!lifecycle.signal.aborted) message.value = error instanceof Error ? error.message : '邀请码检查失败' }
  finally { busy.value = false }
}
async function register() {
  if (busy.value) return
  message.value = ''; notice.value = ''
  if (password.value !== confirmation.value) { message.value = '两次输入的密码不一致'; return }
  busy.value = true
  try {
    const data = await request('register', { username: username.value, password: password.value, code: code.value })
    if (!lifecycle.signal.aborted) emit('registered', data.user.username)
  } catch (error) {
    if (!lifecycle.signal.aborted) message.value = error instanceof Error && error.name !== 'TimeoutError' && error.name !== 'TypeError' ? error.message : '未确认注册结果；如果已提交过，请先尝试登录。'
  } finally { busy.value = false }
}
onMounted(async () => {
  // Keep the invitation only in component memory, out of URLs and storage.
  if (route.hash) await router.replace({ path: '/register', hash: '' })
  if (code.value) await check()
})
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <main class="flex min-h-screen items-center justify-center bg-slate-950 px-4 py-10">
    <form class="w-full max-w-md space-y-5 rounded-2xl bg-white p-6 shadow-2xl dark:bg-slate-900 sm:p-8" @submit.prevent="register">
      <div><h1 class="text-xl font-semibold text-slate-900 dark:text-slate-100">邀请码注册</h1><p class="mt-2 text-sm text-slate-500 dark:text-slate-400">使用管理员发给你的邀请码创建 Cinema115 网站账号。</p></div>
      <label class="block text-sm text-slate-700 dark:text-slate-300">邀请码<input v-model="code" required maxlength="64" autocomplete="off" spellcheck="false" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 font-mono text-sm dark:border-slate-700 dark:bg-slate-950" @input="notice = ''; message = ''" /></label>
      <button type="button" :disabled="busy || !code.trim()" class="text-sm text-blue-600 disabled:opacity-50 dark:text-blue-300" @click="check">检查邀请码</button>
      <label class="block text-sm text-slate-700 dark:text-slate-300">用户名<input v-model="username" required minlength="3" maxlength="32" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]{2,31}" autocomplete="username" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" placeholder="3–32 位字母、数字、下划线、点或短横线" /></label>
      <label class="block text-sm text-slate-700 dark:text-slate-300">密码<input v-model="password" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" placeholder="至少 8 位" /></label>
      <label class="block text-sm text-slate-700 dark:text-slate-300">确认密码<input v-model="confirmation" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
      <p v-if="message" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ message }}</p>
      <p v-if="notice" role="status" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300">{{ notice }}</p>
      <button :disabled="busy" class="w-full rounded-lg bg-blue-600 px-4 py-3 font-medium text-white hover:bg-blue-700 disabled:opacity-50">{{ busy ? '处理中…' : '注册账号' }}</button>
      <RouterLink to="/" class="block text-center text-sm text-slate-500 dark:text-slate-400">返回登录</RouterLink>
    </form>
  </main>
</template>
