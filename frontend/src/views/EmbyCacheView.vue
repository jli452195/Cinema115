<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import DirectLinkCachePanel from '../components/DirectLinkCachePanel.vue'
import PlaybackCachePanel from '../components/PlaybackCachePanel.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'
import { embyRequest } from '../types/emby'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const minutes = ref(9), loaded = ref(false), error = ref('')
const accounts = ref<P115Account[]>([])
const lifecycle = new AbortController()
async function load() {
  error.value = ''
  try {
    const [data, accountList] = await Promise.all([embyRequest(props.token, 'proxy', lifecycle.signal), p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal })])
    accounts.value = accountList.accounts
    minutes.value = data.config.directLinkCacheMinutes ?? 9
    loaded.value = true
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取缓存设置失败' }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="缓存管理" description="查看 Emby 播放直链的缓存状态，管理分享直取和秒传播放地址。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions><RouterLink to="/emby/proxy" class="rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50">反代与缓存时长设置</RouterLink></template>
    <div v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button class="ml-3 underline" @click="load">重试</button></div>
    <DirectLinkCachePanel v-if="loaded" :token="token" :minutes="minutes" />
    <div v-if="loaded" class="mt-6"><PlaybackCachePanel :token="token" :accounts="accounts" /></div>
    <p v-else-if="!error" class="p-6 text-sm text-slate-500">正在加载缓存设置…</p>
  </ManagementLayout>
</template>
