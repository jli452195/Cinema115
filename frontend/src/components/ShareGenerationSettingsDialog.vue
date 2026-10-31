<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import P115ActionDialog from './P115ActionDialog.vue'
import STRMDirectoryDialog from './STRMDirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath, validSTRMDirectory } from '../utils/strmDirectory'
import type { STRMSettings } from '../utils/shareGeneration'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const draft = ref<Omit<STRMSettings, 'recursive' | 'sharePath'>>()
const loading = ref(true), saving = ref(false), directoryDialog = ref(false), error = ref('')
const lifecycle = new AbortController()
function request<T>(init?: RequestInit) {
  return p115Request<T>(props.token, 'share/automation-config', { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const { settings } = await request<{ settings: { generation: STRMSettings } }>()
    // Only edit generation options used by directory sharing; preserve sharePath and automation options on the server.
    const { baseUrl, outputDir, videoExtensions, metadataExtensions, minSizeMB } = settings.generation
    draft.value = { baseUrl, outputDir, videoExtensions, metadataExtensions, minSizeMB }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取生成设置失败' }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
async function save() {
  if (!draft.value || saving.value || loading.value) return
  if (!validSTRMDirectory(draft.value.outputDir)) { error.value = '请选择 STRM 输出目录'; return }
  saving.value = true; error.value = ''
  try {
    // The existing endpoint merges omitted fields with the current configuration.
    await request({ method: 'POST', body: JSON.stringify({ generation: { ...draft.value, baseUrl: draft.value.baseUrl.trim() } }) })
    if (!lifecycle.signal.aborted) emit('saved')
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存生成设置失败' }
  finally { if (!lifecycle.signal.aborted) saving.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <P115ActionDialog title="分享 STRM 生成设置" size="wide" :busy="saving || directoryDialog" @close="emit('close')">
    <p class="text-xs text-slate-500 dark:text-slate-400">与 115 分享 STRM 共用，用于后续任务；进行中的任务保持原设置。</p>
    <p v-if="loading" role="status">正在读取设置…</p>
    <p v-if="error" role="alert" class="text-red-700 dark:text-red-300">{{ error }} <button v-if="!draft" type="button" class="underline" @click="load">重新加载</button></p>
    <form v-if="draft" id="directory-generation-settings" aria-label="分享 STRM 生成设置" @submit.prevent="save">
      <fieldset :disabled="saving || loading" class="grid min-w-0 gap-4 sm:grid-cols-2">
        <label class="sg-field sm:col-span-2"><span>STRM 播放地址</span><input v-model="draft.baseUrl" aria-label="STRM 播放地址" type="url" required placeholder="http://NAS_IP:8080" /><small>填写播放器或 Emby 能访问的 Cinema115 地址。</small></label>
        <div class="sg-field sm:col-span-2"><span>STRM 输出目录</span><button type="button" class="sg-directory" @click="directoryDialog = true"><span class="min-w-0 flex-1 break-all text-left">{{ strmDirectoryPath(draft.outputDir) }}</span><span class="shrink-0 text-blue-600 dark:text-blue-400">选择目录</span></button></div>
        <label class="sg-field"><span>视频后缀</span><input v-model="draft.videoExtensions" aria-label="视频后缀" required /><small>逗号分隔，仅为匹配的视频生成 STRM。</small></label>
        <label class="sg-field"><span>元数据后缀</span><input v-model="draft.metadataExtensions" aria-label="元数据后缀" placeholder="srt,ass,ssa" /><small>字幕、图片等按原格式下载，留空不下载。</small></label>
        <label class="sg-field"><span>最小文件大小（MB）</span><input v-model.number="draft.minSizeMB" aria-label="最小文件大小（MB）" type="number" required min="0" max="1073741824" step="1" /><small>0 表示不限制，仅过滤视频。</small></label>
      </fieldset>
    </form>
    <template #footer><button type="button" :disabled="saving" class="sg-cancel" @click="emit('close')">取消</button><button type="submit" form="directory-generation-settings" :disabled="saving || loading || !draft" class="sg-save">{{ saving ? '保存中…' : '保存设置' }}</button></template>
  </P115ActionDialog>
  <STRMDirectoryDialog v-if="directoryDialog && draft" :token="token" title="选择 STRM 输出目录" @close="directoryDialog = false" @select="path => { draft!.outputDir = path; directoryDialog = false }" />
</template>

<style scoped>
.sg-field { display:grid; gap:6px; min-width:0; font-size:13px; }.sg-field input { @apply border border-slate-300 bg-white dark:border-slate-600 dark:bg-slate-950; width:100%; min-width:0; height:38px; border-radius:8px; padding:0 10px; }.sg-field small { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.6; }.sg-directory { @apply border border-slate-300 dark:border-slate-600; display:flex; align-items:center; gap:12px; min-height:38px; padding:8px 10px; border-radius:8px; }.sg-save { @apply rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40; }.sg-cancel { @apply rounded-lg px-4 py-2 text-sm hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-40; }
@media(max-width:639px) { .sg-field input { height:44px; font-size:16px; }.sg-directory,.sg-save,.sg-cancel { min-height:44px; } }
</style>
