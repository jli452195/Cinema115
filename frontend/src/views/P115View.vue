<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import RouteTabs from '../components/RouteTabs.vue'
import P115ManagedAccountCard from '../components/P115ManagedAccountCard.vue'
import DynamicPoolSection from '../components/DynamicPoolSection.vue'
import PersonalDriveStatusSection from '../components/PersonalDriveStatusSection.vue'
import P115Icon from '../components/P115Icon.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const tabs = [
  { name: 'primary', label: '主账号', to: { name: 'helper115', hash: '' } },
  { name: 'pool', label: '小号池', to: { name: 'helper115', hash: '#pool' } },
  { name: 'personal', label: '个人网盘', to: { name: 'helper115', hash: '#personal-drives' } },
] as const
const activeTab = computed(() => tabs.find(tab => tab.to.hash === route.hash)?.name ?? 'primary')
const primary = ref<P115Account>(), poolCount = ref(0)
const primaryLoaded = ref(false), adding = ref(false)
const primaryError = ref(''), actionError = ref('')
const poolSection = ref<InstanceType<typeof DynamicPoolSection>>()
const personalDrives = ref<InstanceType<typeof PersonalDriveStatusSection>>()
const loginAccountID = ref('')
const lifecycle = new AbortController()
const primaryRead = shallowRef<AbortController>()
const refreshing = computed(() => {
  if (activeTab.value === 'pool') return !!poolSection.value?.refreshing
  if (activeTab.value === 'personal') return !!personalDrives.value?.busy
  return !!primaryRead.value
})

async function loadPrimary() {
  primaryRead.value?.abort()
  const read = new AbortController()
  primaryRead.value = read
  try {
    const signal = AbortSignal.any([read.signal, AbortSignal.timeout(15000)])
    const { accounts } = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal })
    if (read.signal.aborted) return
    primary.value = accounts.find(account => account.primary)
    primaryError.value = ''
  } catch (error) {
    if (!read.signal.aborted) primaryError.value = error instanceof Error ? error.message : '读取主账号失败'
  } finally {
    if (primaryRead.value === read) {
      primaryRead.value = undefined
      primaryLoaded.value = true
    }
  }
}

function refresh() {
  if (activeTab.value === 'pool') return poolSection.value?.refresh()
  if (activeTab.value === 'personal') return personalDrives.value?.refresh()
  return loadPrimary()
}

async function add(isPrimary: boolean) {
  if (adding.value) return
  const tab = isPrimary ? 'primary' : 'pool'
  adding.value = true; actionError.value = ''
  try {
    const created = await p115Request<{ id: string }>(props.token, 'accounts', { method: 'POST', body: JSON.stringify({ name: isPrimary ? '主账号' : `共享小号 ${poolCount.value + 1}`, primary: isPrimary }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (lifecycle.signal.aborted || activeTab.value !== tab) return
    if (!isPrimary) loginAccountID.value = created.id
    await refresh()
  } catch (e) { if (!lifecycle.signal.aborted && activeTab.value === tab) actionError.value = e instanceof Error ? e.message : '添加失败' }
  finally { adding.value = false }
}
watch(activeTab, (tab, _, onCleanup) => {
  actionError.value = ''
  loginAccountID.value = ''
  if (tab === 'primary') {
    void loadPrimary()
    onCleanup(() => {
      primaryRead.value?.abort()
      primaryRead.value = undefined
    })
  }
}, { immediate: true })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 助手" description="主账号负责资源管理与默认播放；共享小号用于用户播放和秒传。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions><button :disabled="refreshing || adding" class="flex items-center gap-2 rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300" @click="refresh"><P115Icon name="refresh" />{{ refreshing ? '刷新中…' : '刷新状态' }}</button></template>
    <RouteTabs class="p115-tabs mb-3" label="115 助手导航" :active="activeTab" :tabs="tabs" fill-on-mobile>
      <template #icon="{ tab }"><P115Icon :name="tab.name === 'primary' ? 'drive' : tab.name === 'pool' ? 'pool' : 'user'" class="!h-4 !w-4" /></template>
    </RouteTabs>
    <p v-if="actionError" role="alert" class="mb-5 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ actionError }}</p>
    <section v-if="activeTab === 'primary'" aria-label="主账号配置">
      <p v-if="primaryError" role="alert" class="mb-3 text-sm text-red-600">{{ primaryError }}</p>
      <p v-if="!primaryLoaded" role="status" class="py-8 text-sm text-slate-500">正在读取主账号…</p>
      <P115ManagedAccountCard v-else-if="primary" :key="primary.id" :account="primary" :token="token" @changed="refresh" />
      <div v-else-if="!primaryError" class="rounded-2xl border border-dashed border-slate-300 p-5 dark:border-slate-700">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">主账号未配置</h2>
        <p class="my-3 text-sm leading-6 text-slate-500">配置主账号后，可使用资源管理与默认播放。主账号需单独创建。</p>
        <button :disabled="adding" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="add(true)">配置主账号</button>
      </div>
    </section>
    <DynamicPoolSection v-else-if="activeTab === 'pool'" ref="poolSection" :token="token" :adding="adding" :login-account-id="loginAccountID" @add="add(false)" @login-started="loginAccountID = ''" @accounts-changed="poolCount = $event.length" />
    <PersonalDriveStatusSection v-else ref="personalDrives" :token="token" />
  </ManagementLayout>
</template>

<style scoped>
@media (max-width: 639px) {
  .p115-tabs { --route-tab-slot-width: 94px; }
}
</style>
