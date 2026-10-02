<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import SettingSwitch from './SettingSwitch.vue'
import { memberRequest } from '../types/memberPlayback'
const props = defineProps<{ token: string; listening?: boolean }>()
const settings = ref({ allowDownload: false, publicUrl: '' }), loaded = ref(false), busy = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()
async function save() {
 if (busy.value || !loaded.value) return false
 busy.value = true; error.value = ''; notice.value = ''
 try { const data = await memberRequest(props.token, lifecycle.signal, '', settings.value); settings.value = { allowDownload: data.settings.allowDownload, publicUrl: data.settings.publicUrl }; notice.value = '播放设置已保存。'; return true }
 catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败'; return false }
 finally { busy.value = false }
}
defineExpose({ save, loaded })
onMounted(async () => {
 try { const data = await memberRequest(props.token, lifecycle.signal, '/settings'); settings.value = { allowDownload: data.settings.allowDownload, publicUrl: data.settings.publicUrl }; loaded.value = true }
 catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
})
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
 <div class="space-y-5">
  <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
  <p v-if="notice" role="status" class="text-sm text-emerald-700">{{ notice }}</p>
  <slot name="connection" :settings="settings" :disabled="busy || !loaded" />
  <form class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" @submit.prevent="save">
   <h2 class="font-semibold text-slate-900 dark:text-slate-100">播放与原文件下载</h2>
   <p class="text-sm leading-6 text-slate-500">已有用户保留 Emby 中的媒体库、下载、设备及分级权限。使用本服务播放时，必须同时满足 Emby 权限和网盘绑定条件；始终禁止转码与重新封装。</p>
   <SettingSwitch v-model="settings.allowDownload" :disabled="busy || !loaded" label="允许下载原文件" description="仅开放本服务的下载入口，不会替用户开启 Emby 下载权限。下载还需 Emby 已授权、网盘可用且用户未停用；302 关闭时入口停止。" />
   <button :disabled="busy || !loaded" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">保存播放设置</button>
   <div class="flex flex-wrap gap-4 text-sm"><RouterLink to="/emby/users" class="text-blue-600">用户播放状态与网盘绑定</RouterLink><RouterLink to="/users" class="text-blue-600">邀请与新用户媒体库模板</RouterLink></div>
  </form>
 </div>
</template>
