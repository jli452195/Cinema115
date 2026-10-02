<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ManagedAccountCard from '../components/P115ManagedAccountCard.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { p115Request } from '../lib/p115-api'
import { memberRequest } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const primary = ref<P115Account>(), pool = ref<P115Account[]>([])
const loading = ref(true), refreshing = ref(false), adding = ref(false), rules = ref(false)
const primaryError = ref(''), poolError = ref(''), actionError = ref('')
const loginAccountID = ref('')
const lifecycle = new AbortController()
let sequence = 0
async function locatePool() {
  if (route.hash !== '#pool') return
  await nextTick()
  if (!lifecycle.signal.aborted) document.getElementById('pool')?.scrollIntoView({ block: 'start' })
}
async function refresh() {
  const current = ++sequence
  refreshing.value = true
  const results = await Promise.allSettled([
    p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) }),
    memberRequest(props.token, AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]), '/accounts'),
  ])
  if (lifecycle.signal.aborted || current !== sequence) return
  const [managed, shared] = results
  if (managed.status === 'fulfilled') { primary.value = managed.value.accounts.find(account => account.primary); primaryError.value = '' }
  else primaryError.value = managed.reason instanceof Error ? managed.reason.message : '读取主账号失败'
  if (shared.status === 'fulfilled') { pool.value = shared.value.accounts; poolError.value = '' }
  else poolError.value = shared.reason instanceof Error ? shared.reason.message : '读取小号池失败'
  const initial = loading.value
  loading.value = false; refreshing.value = false
  if (initial) await locatePool()
}
async function add(isPrimary: boolean) {
  if (adding.value) return
  adding.value = true; actionError.value = ''
  try {
    const created = await p115Request<{ id: string }>(props.token, 'accounts', { method: 'POST', body: JSON.stringify({ name: isPrimary ? '主账号' : `共享小号 ${pool.value.length + 1}`, primary: isPrimary }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (!isPrimary) loginAccountID.value = created.id
    await refresh()
  } catch (e) { if (!lifecycle.signal.aborted) actionError.value = e instanceof Error ? e.message : '添加失败' }
  finally { adding.value = false }
}
watch(() => route.hash, locatePool)
onMounted(refresh)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 助手" description="主账号负责资源管理与默认播放；共享小号用于用户播放和秒传。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions><button :disabled="refreshing || adding" class="rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300" @click="refresh">{{ refreshing ? '刷新中…' : '刷新' }}</button></template>
    <p v-if="actionError" role="alert" class="mb-5 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ actionError }}</p>
    <section aria-label="主账号配置" class="mb-8 max-w-lg">
      <p v-if="primaryError" role="alert" class="mb-3 text-sm text-red-600">{{ primaryError }}</p>
      <p v-if="loading" role="status" class="py-8 text-sm text-slate-500">正在读取主账号…</p>
      <P115ManagedAccountCard v-else-if="primary" :key="primary.id" :account="primary" :token="token" @changed="refresh" />
      <div v-else-if="!primaryError" class="rounded-2xl border border-dashed border-slate-300 p-5 dark:border-slate-700">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">主账号未配置</h2>
        <p class="my-3 text-sm leading-6 text-slate-500">配置主账号后，可使用资源管理与默认播放。主账号需单独创建。</p>
        <button :disabled="adding" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="add(true)">配置主账号</button>
      </div>
    </section>
    <section id="pool" aria-label="共享小号池" class="scroll-mt-6">
      <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">共享小号池<span v-if="!loading && !poolError" class="ml-2 text-sm font-normal text-slate-500">{{ pool.length }} 个</span></h2>
        <div class="flex flex-wrap items-center gap-4 text-sm"><button class="text-slate-500 hover:text-blue-600" @click="rules = true">分配说明</button><RouterLink to="/emby/users" class="text-blue-600">分配 Emby 用户</RouterLink><button :disabled="adding || loading || !!poolError" class="rounded-lg bg-blue-600 px-4 py-2 text-white disabled:opacity-40" @click="add(false)">添加共享小号</button></div>
      </header>
      <p class="mb-5 text-sm text-slate-500">一个小号可供多位用户共享，个人网盘由用户在个人中心独立管理。</p>
      <p v-if="poolError" role="alert" class="mb-4 text-sm text-red-600">{{ poolError }}</p>
      <p v-if="loading" role="status" class="py-8 text-sm text-slate-500">正在读取小号池…</p>
      <template v-else>
        <div class="grid items-start gap-4 lg:grid-cols-2 xl:grid-cols-3"><P115ManagedAccountCard v-for="account in pool" :key="account.id" :account="account" :token="token" :start-login="loginAccountID === account.id" @login-started="loginAccountID = ''" @changed="refresh" /></div>
        <p v-if="!pool.length && !poolError" class="rounded-2xl border border-dashed border-slate-300 p-6 text-sm text-slate-500 dark:border-slate-700">尚未添加共享小号。可先添加小号，再扫码登录并设置秒传目录。</p>
      </template>
    </section>
  </ManagementLayout>
  <P115ActionDialog v-if="rules" title="共享小号分配说明" @close="rules = false">
    <p>先登录小号并设置秒传目录，再到 Emby 用户管理中分配。一个小号可以供多位用户使用。</p>
    <p>注册选择小号池时，自动分配优先使用人数较少的可用小号；首次分配没有可用小号时，使用主网盘。</p>
    <p>已分配的小号失效时保留关联并暂停播放，不自动回退或轮换；恢复后继续使用原账号。</p>
    <p class="text-slate-500">用户自行绑定的个人网盘不进入管理员共享小号池。</p>
    <template #footer><button class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white" @click="rules = false">知道了</button></template>
  </P115ActionDialog>
</template>
