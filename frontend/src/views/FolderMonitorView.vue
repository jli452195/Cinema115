<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115Icon from '../components/P115Icon.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import ShareGenerationSettingsDialog from '../components/ShareGenerationSettingsDialog.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath } from '../utils/strmDirectory'
import { useDirectoryShare, type DirectoryShareConfig } from '../composables/useDirectoryShare'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const { monitor, accountId, accountReady, generation, loading, submitting, error, notice, active, bindingChanged, unavailable, label, stageText, refresh, invalidate, submit, canEnd, endBatch } = useDirectoryShare(props.token)
const lifecycle = new AbortController()
const sourceDialog = ref(false), generationDialog = ref(false)
const directoryAccountId = ref(''), interval = ref('600'), actionError = ref('')
const editable = computed(() => !loading.value && !submitting.value && !active.value && !monitor.value?.hasBatch && (!monitor.value || ['waiting', 'empty', 'completed', 'error', 'ended'].includes(monitor.value.stage)))
const canSchedule = computed(() => editable.value && !!monitor.value && accountReady.value && !bindingChanged.value && !error.value)
const enabled = ref(false)
watch(() => monitor.value?.enabled, value => { enabled.value = !!value }, { immediate: true })
const scheduleState = computed(() => monitor.value?.schedulePaused ? '已暂停' : enabled.value ? '监控中' : '未开启')
const showTask = computed(() => !!monitor.value && (active.value || monitor.value.hasBatch || !!monitor.value.lastError || ['blocked', 'recovering', 'sharing_legacy', 'error'].includes(monitor.value.stage)))
watch(() => monitor.value?.intervalSeconds, value => { if (value) interval.value = String(value) }, { immediate: true })
function date(value?: string) {
  if (!value || value.startsWith('0001')) return '暂无'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '暂无' : parsed.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
function logLink(taskID: string) { return { path: '/logs', query: { module: 'STRM', level: 'ALL', taskID } } }
function request<T>(path: string, init: RequestInit = {}) {
  return p115Request<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(40000)]) })
}
async function mutate(work: () => Promise<void>) {
  if (submitting.value) return false
  submitting.value = true; invalidate(); actionError.value = ''; notice.value = ''
  try { await work(); return true }
  catch (reason) { if (!lifecycle.signal.aborted) actionError.value = reason instanceof Error ? reason.message : '操作失败'; return false }
  finally { submitting.value = false }
}
async function openSource() {
  await refresh()
  if (error.value || !editable.value) return
  if (!accountReady.value) { actionError.value = '请先在 115 助手中配置并登录主账号'; return }
  directoryAccountId.value = accountId.value
  actionError.value = ''; sourceDialog.value = true
}
async function selectSource(value: { id: string; path: string }) {
  sourceDialog.value = false
  if (value.id === '0') { actionError.value = '请选择非根目录'; return }
  await mutate(async () => {
    const id = monitor.value?.id
    monitor.value = await request<DirectoryShareConfig>(id ? `folder-monitors/${id}` : 'folder-monitors', {
      method: id ? 'PUT' : 'POST', body: JSON.stringify({ accountId: directoryAccountId.value, sourceCID: value.id, sourcePath: value.path }),
    })
    notice.value = '监控目录已保存。可开启自动监控，或立即运行。'
  })
}
async function saveSchedule(value: boolean, seconds = interval.value) {
  if (!monitor.value || (value ? !canSchedule.value : loading.value || submitting.value)) return false
  return mutate(async () => {
    monitor.value = await request<DirectoryShareConfig>(`folder-monitors/${monitor.value!.id}/schedule`, {
      method: 'PUT', body: JSON.stringify({ enabled: value, intervalSeconds: Number(seconds) }),
    })
    notice.value = value ? '自动监控已开启，将按设定间隔检查目录。' : '自动监控已关闭，当前任务继续处理。'
  })
}
async function changeInterval(event: Event) {
  const select = event.target as HTMLSelectElement
  if (!await saveSchedule(true, select.value)) select.value = interval.value
}
async function changeEnabled(value: boolean) {
  enabled.value = value
  await saveSchedule(value)
  enabled.value = !!monitor.value?.enabled
}
async function remove() {
  if (!monitor.value || !editable.value || !window.confirm('删除监控配置？现有网盘文件、分享和 STRM 会保留。')) return
  await mutate(async () => {
    await request(`folder-monitors/${monitor.value!.id}`, { method: 'DELETE' })
    monitor.value = undefined; notice.value = '监控配置已删除。'
  })
}
async function recover() {
  if (!monitor.value?.logTaskID) return
  await mutate(async () => {
    monitor.value = await request<DirectoryShareConfig>(`folder-monitors/${monitor.value!.id}/recover`, {
      method: 'POST', body: JSON.stringify({ taskId: monitor.value!.logTaskID }),
    })
    notice.value = monitor.value.recoveryMessage || '核对完成，未完成的文件已保留。'
  })
}
async function generationSaved() { generationDialog.value = false; await refresh(); notice.value = '生成设置已保存，用于后续任务。' }
async function start() { actionError.value = ''; await submit() }
onMounted(() => { document.title = '115 监控分享 · Cinema115' })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="监控分享" description="监控指定网盘目录，自动创建分享并生成本地 STRM。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="space-y-4" :aria-busy="loading">
      <p v-if="error || actionError" role="alert" class="fm-alert">{{ actionError || error }} <button v-if="error" type="button" :disabled="submitting" class="ml-3 underline" @click="refresh">刷新状态</button></p>
      <p v-if="notice" role="status" class="fm-notice">{{ notice }}</p>
      <section class="fm-panel" aria-labelledby="fm-settings-title">
        <div class="fm-heading">
          <h2 id="fm-settings-title"><P115Icon name="settings" class="text-blue-600 dark:text-blue-400" />监控设置</h2>
          <div class="flex items-center gap-3"><button type="button" :disabled="loading || submitting" class="fm-link" @click="generationDialog = true">生成设置</button><details v-if="monitor" class="relative"><summary class="fm-more" aria-label="配置操作"><P115Icon name="more" /></summary><div class="fm-menu"><button type="button" :disabled="!editable" class="fm-menu-action text-red-600 dark:text-red-400" @click="remove">删除监控配置</button></div></details></div>
        </div>
        <div class="fm-directories">
          <div class="min-w-0"><p class="fm-caption">监控目录</p><div class="mt-2 flex items-center gap-3"><P115Icon name="folder" class="text-blue-600 dark:text-blue-400" /><p class="fm-path">{{ loading ? '读取中…' : monitor?.sourcePath || (monitor?.sourceCID ? `目录 ${monitor.sourceCID}` : '请选择主网盘中的待处理目录') }}</p><button type="button" :disabled="!editable" class="fm-secondary shrink-0" @click="openSource">{{ monitor ? '更换目录' : '选择目录' }}</button></div><p v-if="!editable && monitor" class="fm-help">当前任务处理完毕后可更换目录。</p><p v-else-if="enabled" class="fm-help">更换目录后需重新开启自动监控。</p></div>
          <div class="min-w-0"><p class="fm-caption">STRM 输出目录</p><p class="fm-path mt-2">{{ generation?.outputDir ? strmDirectoryPath(generation.outputDir) : '请在生成设置中选择' }}</p><p v-if="generation" class="fm-help">{{ generation.recursive ? '包含子目录' : '仅顶层文件' }}，{{ generation.minSizeMB ? `跳过小于 ${generation.minSizeMB} MB 的视频` : '不限视频大小' }}。</p></div>
        </div>
        <p v-if="bindingChanged" class="fm-warning mt-3">主账号已变化，请重新选择监控目录。</p>
        <p v-else-if="!loading && !accountReady" class="fm-warning mt-3">请先前往 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号。</p>
        <div class="fm-controls">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-2"><SettingSwitch :model-value="enabled" label="自动监控" :disabled="loading || submitting || (!enabled && !canSchedule)" @update:model-value="changeEnabled" /><span class="fm-status" :class="monitor?.schedulePaused ? 'fm-warning' : enabled ? 'text-blue-700 dark:text-blue-300' : 'text-slate-500'">{{ scheduleState }}</span><button v-if="monitor?.schedulePaused" type="button" :disabled="!canSchedule" class="fm-link" @click="saveSchedule(true)">恢复监控</button></div>
          <label class="fm-interval"><span>检查间隔</span><select :value="interval" :disabled="!enabled || !canSchedule || !!monitor?.schedulePaused" @change="changeInterval"><option v-for="minutes in [5, 10, 30, 60]" :key="minutes" :value="String(minutes * 60)">每 {{ minutes }} 分钟</option></select></label>
          <button type="button" :disabled="!!unavailable" :title="unavailable || undefined" class="fm-primary" @click="start">{{ label }}</button>
        </div>
        <p class="fm-help">按设定间隔检查目录，发现稳定的新内容后自动生成分享 STRM。关闭监控后，当前任务继续处理。</p>
        <p v-if="monitor?.schedulePaused" role="status" class="fm-warning mt-2">{{ monitor.schedulePaused }}</p>
        <p v-if="unavailable && !active && !submitting && !loading && !showTask" class="fm-help">{{ unavailable }}</p>
        <div v-if="enabled" class="fm-times"><span>最近检查 {{ date(monitor?.lastScanAt) }}</span><span>下次检查 {{ monitor?.schedulePaused ? '已暂停' : active ? '当前任务结束后' : date(monitor?.nextScanAt) }}</span></div>
        <p class="fm-cleanup"><P115Icon name="warning" /><span>全部生成成功后，本批源文件移入 115 回收站；未完成时保留供继续处理。监控目录本身保留。</span></p>
      </section>
      <section v-if="showTask && monitor" class="fm-panel" aria-labelledby="fm-task-title">
        <div class="fm-heading"><div class="flex items-center gap-3"><h2 id="fm-task-title">当前任务</h2><span class="fm-status" :class="monitor.lastError ? 'text-red-700 dark:text-red-300' : 'text-blue-700 dark:text-blue-300'">{{ active ? '处理中' : '未完成' }}</span></div><RouterLink v-if="monitor.logTaskID" :to="logLink(monitor.logTaskID)" class="fm-link">查看日志</RouterLink></div>
        <p class="mt-3 break-words text-sm leading-6" :class="monitor.lastError ? 'text-red-700 dark:text-red-300' : ''" role="status">{{ stageText }}</p>
        <div class="mt-3 flex flex-wrap items-center gap-3"><button v-if="monitor.hasBatch && monitor.lastError" type="button" :disabled="submitting || active" class="fm-secondary" @click="recover">核对残留</button><button v-if="canEnd" type="button" :disabled="submitting" class="fm-secondary" @click="actionError = ''; endBatch()">结束本次任务</button><RouterLink v-if="monitor.generationTaskId" to="/115/share/logs" class="fm-link">生成日志</RouterLink></div>
        <details v-if="monitor.hasBatch || monitor.shareUrl" class="fm-details"><summary>任务详情</summary><p v-if="monitor.hasBatch">已准备 {{ monitor.batchMoved || 0 }} / {{ monitor.batchCount || 0 }} 项</p><p v-if="monitor.batchDirectoryName" class="break-all">本批临时目录：{{ monitor.batchDirectoryName }}</p><a v-if="monitor.shareUrl" :href="monitor.shareUrl" target="_blank" rel="noopener noreferrer" class="fm-link">查看本次分享</a><p v-if="monitor.hasBatch && monitor.lastError">核对残留只清理确认安全的空临时目录；仍有文件或状态不明时保留。</p></details>
      </section>
      <section v-if="monitor && (monitor.recentRuns?.length || monitor.endedBatches?.length)" class="fm-panel" aria-labelledby="fm-history-title">
        <div class="fm-heading"><h2 id="fm-history-title">最近记录</h2><span class="fm-caption">最近 20 次任务</span></div>
        <ul v-if="monitor.recentRuns?.length" class="mt-2 divide-y divide-slate-100 dark:divide-slate-800"><li v-for="run in monitor.recentRuns" :key="run.taskId" class="fm-run"><span class="fm-status" :class="run.status === 'completed' ? 'text-green-700 dark:text-green-300' : run.status === 'ended' ? 'text-slate-500' : 'text-red-700 dark:text-red-300'">{{ run.status === 'completed' ? '成功' : run.status === 'ended' ? '已结束' : '未完成' }}</span><div class="min-w-0 flex-1"><p class="break-words text-sm">{{ run.message }}</p><p class="fm-help">{{ date(run.time) }} · {{ run.trigger === 'scheduled' ? '自动执行' : '手动执行' }}</p></div><div class="flex shrink-0 flex-wrap gap-3"><a v-if="run.shareUrl" :href="run.shareUrl" target="_blank" rel="noopener noreferrer" class="fm-link">分享</a><RouterLink :to="logLink(run.taskId)" class="fm-link">日志</RouterLink></div></li></ul>
        <details v-if="monitor.endedBatches?.length" class="fm-details"><summary>保留文件位置</summary><div v-for="(batch, index) in monitor.endedBatches" :key="index" class="fm-retained"><p class="break-all font-medium">{{ batch.directoryName || '未创建临时目录' }}</p><p>{{ date(batch.endedAt) }} · {{ batch.cleaned ? '已清理' : '文件保留，重新处理前请移回监控目录' }}</p><p class="break-all">账号编号 {{ batch.accountId }}<span v-if="batch.directoryId"> · 目录编号 {{ batch.directoryId }}</span></p><a v-if="batch.shareUrl" :href="batch.shareUrl" target="_blank" rel="noopener noreferrer" class="fm-link">查看分享</a></div></details>
      </section>
    </div>
    <P115DirectoryDialog v-if="sourceDialog" :token="token" :account-id="directoryAccountId" title="选择监控目录 · 选择后自动保存" @close="sourceDialog = false" @select="selectSource" />
    <ShareGenerationSettingsDialog v-if="generationDialog" :token="token" @close="generationDialog = false" @saved="generationSaved" />
  </ManagementLayout>
</template>

<style scoped>
.fm-panel { @apply rounded-2xl border border-slate-200 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200; padding:20px; }.fm-heading { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:12px; }.fm-heading h2 { @apply text-slate-900 dark:text-slate-100; display:flex; align-items:center; gap:9px; font-size:15px; font-weight:600; }.fm-caption { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.7; }.fm-help { @apply text-slate-500 dark:text-slate-400; margin-top:6px; font-size:12px; line-height:1.7; }.fm-path { min-width:0; flex:1; overflow-wrap:anywhere; font-size:14px; line-height:1.7; font-weight:500; }
.fm-directories { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:20px; margin:18px 0; }.fm-directories>div+div { @apply border-l border-slate-200 dark:border-slate-700; padding-left:20px; }.fm-controls { @apply border-t border-slate-100 dark:border-slate-800; display:flex; flex-wrap:wrap; align-items:center; gap:14px 24px; padding-top:16px; }.fm-interval { display:flex; align-items:center; gap:10px; font-size:13px; }.fm-interval select { @apply border border-slate-300 bg-white dark:border-slate-600 dark:bg-slate-950; height:36px; border-radius:8px; padding:0 9px; }.fm-controls>.fm-primary { margin-left:auto; }.fm-status { @apply bg-slate-100 dark:bg-slate-800; display:inline-flex; align-items:center; flex-shrink:0; padding:3px 8px; border-radius:6px; font-size:12px; line-height:1.6; font-weight:400; }.fm-times { @apply text-slate-500 dark:text-slate-400; display:flex; flex-wrap:wrap; gap:6px 24px; margin-top:8px; font-size:12px; }
.fm-cleanup { @apply bg-amber-50/80 text-amber-900 dark:bg-amber-950/20 dark:text-amber-200; display:flex; align-items:flex-start; gap:8px; margin-top:14px; padding:10px 12px; border-radius:8px; font-size:12px; line-height:1.7; }.fm-cleanup>svg { margin-top:1px; }.fm-warning { @apply text-amber-700 dark:text-amber-300; font-size:13px; line-height:1.7; }.fm-alert { @apply rounded-xl bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300; }.fm-notice { @apply text-sm text-blue-700 dark:text-blue-300; line-height:1.7; }.fm-link { @apply text-blue-600 hover:underline dark:text-blue-400 disabled:opacity-40; font-size:12px; }.fm-primary { @apply rounded-lg bg-blue-600 text-white hover:bg-blue-700; padding:8px 16px; font-size:13px; font-weight:500; }.fm-secondary { @apply rounded-lg border border-slate-200 hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800; padding:7px 10px; font-size:12px; }.fm-primary:disabled,.fm-secondary:disabled,.fm-menu-action:disabled { cursor:not-allowed; opacity:.4; }
.fm-more { @apply text-slate-500; display:flex; align-items:center; padding:4px; cursor:pointer; list-style:none; }.fm-more::-webkit-details-marker { display:none; }.fm-menu { @apply border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-900; position:absolute; right:0; z-index:10; width:150px; margin-top:6px; border-radius:10px; padding:5px; }.fm-menu-action { @apply rounded-lg hover:bg-slate-50 dark:hover:bg-slate-800; width:100%; padding:9px; text-align:left; font-size:12px; }.fm-details { @apply border-t border-slate-100 text-slate-500 dark:border-slate-800 dark:text-slate-400; margin-top:14px; padding-top:10px; font-size:12px; line-height:1.8; }.fm-details summary { width:fit-content; cursor:pointer; }.fm-details p { margin-top:5px; }.fm-run { display:flex; align-items:flex-start; gap:12px; padding:13px 0; }.fm-run .fm-status { margin-top:1px; }.fm-retained { @apply rounded-lg bg-slate-50 dark:bg-slate-950; margin-top:10px; padding:10px 12px; }
@media(max-width:639px) { .fm-panel { padding:16px; }.fm-directories { grid-template-columns:1fr; gap:16px; }.fm-directories>div+div { border-left:0; padding-left:0; }.fm-controls { gap:14px; }.fm-controls>.fm-primary { margin-left:0; }.fm-interval { margin-left:auto; }.fm-interval select { height:44px; font-size:16px; }.fm-primary,.fm-secondary,.fm-link { min-height:40px; }.fm-link { display:inline-flex; align-items:center; }.fm-controls>.fm-primary { width:100%; min-height:44px; }.fm-directories .fm-link { min-height:24px; }.fm-run { flex-wrap:wrap; gap:8px; }.fm-run>div:last-child { margin-left:auto; }.fm-path { font-size:13px; }.fm-cleanup { padding:10px; } }
@media(max-width:359px) { .fm-interval { width:100%; margin-left:0; justify-content:space-between; } }
</style>
