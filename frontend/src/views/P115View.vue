<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ManagedAccountCard from '../components/P115ManagedAccountCard.vue'
import DynamicPoolSection from '../components/DynamicPoolSection.vue'
import P115Icon from '../components/P115Icon.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const primary = ref<P115Account>(), pool = ref<P115Account[]>([])
const loading = ref(true), refreshing = ref(false), adding = ref(false)
const primaryError = ref(''), actionError = ref('')
const poolSection = ref<InstanceType<typeof DynamicPoolSection>>()
const loginAccountID = ref('')
const lifecycle = new AbortController()
let sequence = 0
let locatedPool = false
async function locatePool() {
  if (route.hash !== '#pool' || locatedPool || loading.value) return
  await nextTick()
  if (!lifecycle.signal.aborted) { document.getElementById('pool')?.scrollIntoView({ block: 'start' }); locatedPool = true }
}
async function refresh() {
  const current = ++sequence
  refreshing.value = true
  const results = await Promise.allSettled([p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) }), poolSection.value?.refresh()])
  if (lifecycle.signal.aborted || current !== sequence) return
  const [managed] = results
  if (managed.status === 'fulfilled') { primary.value = managed.value.accounts.find(account => account.primary); primaryError.value = '' }
  else primaryError.value = managed.reason instanceof Error ? managed.reason.message : '读取主账号失败'
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
watch(() => route.hash, () => { locatedPool = false; void locatePool() })
onMounted(refresh)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 助手" description="主账号负责资源管理与默认播放；共享小号用于用户播放和秒传。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions><button :disabled="refreshing || adding" class="flex items-center gap-2 rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300" @click="refresh"><P115Icon name="refresh" />{{ refreshing ? '刷新中…' : '刷新状态' }}</button></template>
    <p v-if="actionError" role="alert" class="mb-5 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ actionError }}</p>
    <section aria-label="主账号配置" class="mb-8">
      <p v-if="primaryError" role="alert" class="mb-3 text-sm text-red-600">{{ primaryError }}</p>
      <p v-if="loading" role="status" class="py-8 text-sm text-slate-500">正在读取主账号…</p>
      <P115ManagedAccountCard v-else-if="primary" :key="primary.id" :account="primary" :token="token" @changed="refresh" />
      <div v-else-if="!primaryError" class="rounded-2xl border border-dashed border-slate-300 p-5 dark:border-slate-700">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">主账号未配置</h2>
        <p class="my-3 text-sm leading-6 text-slate-500">配置主账号后，可使用资源管理与默认播放。主账号需单独创建。</p>
        <button :disabled="adding" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="add(true)">配置主账号</button>
      </div>
    </section>
    <DynamicPoolSection ref="poolSection" :token="token" :adding="adding" :login-account-id="loginAccountID" @add="add(false)" @login-started="loginAccountID = ''" @accounts-changed="pool = $event; locatePool()" />
  </ManagementLayout>
</template>
