<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountEditor from '../components/P115AccountEditor.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const accounts = ref<P115Account[]>([])
const primary = computed(() => accounts.value.find(account => account.primary))
const secondary = computed(() => accounts.value.filter(account => !account.primary))
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
async function add(asPrimary = false) {
  if (adding.value) return
  adding.value = true; error.value = ''
  try { await p115Request(props.token, 'accounts', { method: 'POST', body: JSON.stringify({ name: asPrimary ? '主账号' : `小号 ${secondary.value.length + 1}`, primary: asPrimary }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) }); await refresh() }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '添加失败' }
  finally { adding.value = false }
}
onMounted(refresh)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 助手" description="主账号负责资源管理，小号用于用户播放和秒传。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="mb-6 flex flex-wrap items-center gap-3 border-b border-slate-200 dark:border-slate-700 pb-6">
      <button type="button" :disabled="loading || adding || accounts.length >= 20" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="add(false)">{{ adding ? '添加中…' : '＋ 添加小号' }}</button>
      <button type="button" class="rounded-lg border border-slate-200 dark:border-slate-700 px-4 py-2.5 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500" @click="refresh">刷新配置</button>
      <span class="text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ accounts.length }} 个账号配置</span>
      <p class="w-full text-xs leading-6 text-slate-400 dark:text-slate-500">分享、下载、整理和 Telegram 统一使用主账号。小号在用户管理中绑定，正在执行的任务保持启动时的账号。</p>
    </div>
    <p v-if="error" role="alert" class="mb-5 rounded-lg bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
    <p v-if="loading" role="status" class="py-10 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">正在读取账号配置…</p>
    <template v-else>
      <section class="mb-8 space-y-4" aria-label="主账号配置">
        <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">主账号</h2>
        <P115AccountEditor v-if="primary" :key="primary.id" :account="primary" :token="token" @changed="refresh" />
        <div v-else class="rounded-2xl border border-dashed border-slate-300 dark:border-slate-700 p-6">
          <p class="mb-4 text-sm text-slate-500 dark:text-slate-400">配置主账号后即可在各功能中使用。更换主账号需要重新设置各功能的网盘目录。</p>
          <button :disabled="adding || accounts.length >= 20" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="add(true)">配置主账号</button>
        </div>
      </section>
      <section class="space-y-4" aria-label="小号配置">
        <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">小号配置</h2>
        <p class="text-sm text-slate-500 dark:text-slate-400">为小号设置登录和秒传目录，然后在 Emby 用户管理中绑定。</p>
        <div class="grid items-start gap-5 lg:grid-cols-2"><P115AccountEditor v-for="account in secondary" :key="account.id" :account="account" :token="token" @changed="refresh" /></div>
      </section>
    </template>
  </ManagementLayout>
</template>
