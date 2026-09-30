<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import DirectLinkCachePanel from '../components/DirectLinkCachePanel.vue'
import PlaybackCachePanel from '../components/PlaybackCachePanel.vue'
import { embyRequest } from '../types/emby'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const minutes = ref(9), loaded = ref(false), error = ref('')
const activePanel = ref<'links' | 'files'>('links')
const lifecycle = new AbortController()
async function load() {
  error.value = ''
  try {
    const data = await embyRequest(props.token, 'proxy', lifecycle.signal)
    minutes.value = data.config.directLinkCacheMinutes ?? 9
    loaded.value = true
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取缓存设置失败' }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="缓存管理" description="查看 Emby 播放直链的缓存状态，管理分享直取和秒传播放地址。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions><RouterLink to="/emby/proxy" class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-4 py-2.5 text-sm text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950">反代与缓存时长设置</RouterLink></template>
    <div v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button class="ml-3 underline" @click="load">重试</button></div>
    <template v-if="loaded">
      <div class="mb-5 inline-flex max-w-full gap-1 rounded-xl border border-slate-200 bg-white p-1 dark:border-slate-700 dark:bg-slate-900" aria-label="缓存分类">
        <button type="button" :aria-pressed="activePanel === 'links'" class="rounded-lg px-4 py-2 text-sm font-medium transition-colors" :class="activePanel === 'links' ? 'bg-slate-100 text-slate-900 dark:bg-slate-800 dark:text-slate-100' : 'text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200'" @click="activePanel = 'links'">直链缓存</button>
        <button type="button" :aria-pressed="activePanel === 'files'" class="rounded-lg px-4 py-2 text-sm font-medium transition-colors" :class="activePanel === 'files' ? 'bg-slate-100 text-slate-900 dark:bg-slate-800 dark:text-slate-100' : 'text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200'" @click="activePanel = 'files'">网盘文件复用</button>
      </div>
      <DirectLinkCachePanel v-show="activePanel === 'links'" :token="token" :minutes="minutes" />
      <PlaybackCachePanel v-show="activePanel === 'files'" :token="token" />
    </template>
    <p v-else-if="!error" class="p-6 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">正在加载缓存设置…</p>
  </ManagementLayout>
</template>
