<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountEditor from '../components/P115AccountEditor.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const accounts = ref<P115Account[]>([])
const loading = ref(true)
const adding = ref(false)
const error = ref('')
const lifecycle = new AbortController()
let sequence = 0
async function refresh() {
  const current = ++sequence
  try {
    const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (current === sequence && !lifecycle.signal.aborted) { accounts.value = data.accounts; error.value = '' }
  } catch (e) { if (!lifecycle.signal.aborted && current === sequence) error.value = e instanceof Error ? e.message : '读取账号失败' }
  finally { loading.value = false }
}
async function add() {
  if (adding.value) return
  adding.value = true; error.value = ''
  try { await p115Request(props.token, 'accounts', { method: 'POST', body: JSON.stringify({ name: `账号 ${accounts.value.length + 1}` }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) }); await refresh() }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '添加失败' }
  finally { adding.value = false }
}
onMounted(refresh)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 助手" description="管理各账号的 Cookie 和扫码登录，在具体功能中选择要使用的账号。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="mb-6 flex flex-wrap items-center gap-3 border-b border-slate-200 pb-6">
      <button type="button" :disabled="loading || adding || accounts.length >= 20" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="add">{{ adding ? '添加中…' : '＋ 添加配置' }}</button>
      <button type="button" class="rounded-lg border border-slate-200 px-4 py-2.5 text-sm text-slate-600" @click="refresh">刷新配置</button>
      <span class="text-xs text-slate-500">{{ accounts.length }} 个账号配置</span>
      <p class="w-full text-xs leading-6 text-slate-400">各功能独立选择账号，保存或新增账号不会改变任何功能的选择。正在执行的任务继续使用启动时的凭据。</p>
    </div>
    <p v-if="error" role="alert" class="mb-5 rounded-lg bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
    <p v-if="loading" role="status" class="py-10 text-sm text-slate-500">正在读取账号配置…</p>
    <p v-else-if="!accounts.length && !error" class="py-10 text-sm text-slate-500">暂无账号，请添加配置并扫码或保存 Cookie，再到相应功能中选择使用。</p>
    <div class="grid items-start gap-5 lg:grid-cols-2">
      <P115AccountEditor v-for="account in accounts" :key="account.id" :account="account" :token="token" @changed="refresh" />
    </div>
  </ManagementLayout>
</template>
