<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountEditor from '../components/P115AccountEditor.vue'
import { p115Request } from '../lib/p115-api'
import { memberRequest } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const accounts = ref<P115Account[]>([]), busy = ref(false), error = ref('')
const lifecycle = new AbortController()
async function load() {
  error.value = ''
  try { accounts.value = (await memberRequest(props.token, lifecycle.signal, '/accounts')).accounts }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取小号池失败' }
}
async function add() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    await p115Request(props.token, 'accounts', { method: 'POST', body: JSON.stringify({ name: `共享小号 ${accounts.value.length + 1}`, primary: false }), signal: lifecycle.signal })
    await load()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '添加失败' }
  finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
  <ManagementLayout title="管理员共享小号池" description="一个小号可供多个 Emby 用户共享，个人扫码网盘不进入此池。" :parent="{ label: '115 助手', to: '/helper115' }" @logout="$emit('logout')">
    <div class="mb-5 flex flex-wrap gap-4 text-sm"><button :disabled="busy" class="rounded-lg bg-blue-600 px-4 py-2 text-white disabled:opacity-40" @click="add">添加共享小号</button><button :disabled="busy" class="text-blue-600" @click="load">刷新</button><RouterLink to="/emby/users" class="self-center text-blue-600">分配 Emby 用户</RouterLink></div>
    <p class="mb-5 text-sm leading-6 text-slate-500">先登录小号并设置秒传目录，再给用户分配。注册自动分配使用人数较少的可用小号；没有可用小号时使用主网盘。已有绑定失效时保留并暂停，恢复后继续使用原账号。</p>
    <p v-if="error" role="alert" class="mb-5 rounded-lg bg-red-50 p-3 text-red-700">{{ error }}</p>
    <div class="grid items-start gap-5 lg:grid-cols-2"><div v-for="account in accounts" :key="account.id" class="min-w-0 space-y-2"><p class="text-sm text-slate-500">{{ account.userCount || 0 }} 人使用<span v-if="account.users?.length">：{{ account.users.join('、') }}</span></p><P115AccountEditor :account="account" :token="token" @changed="load" /></div></div>
    <p v-if="!accounts.length && !error" class="py-8 text-sm text-slate-500">尚未添加小号。选择小号池的注册用户将使用主网盘。</p>
  </ManagementLayout>
</template>
