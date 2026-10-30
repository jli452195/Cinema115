<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import SelectMenu from '../components/SelectMenu.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath } from '../utils/strmDirectory'
import { useDirectoryShare, type DirectoryShareConfig } from '../composables/useDirectoryShare'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const { monitor, accountId, accountReady, generation, loading, submitting, error, notice, active, bindingChanged, unavailable, label, stageText, refresh, invalidate, submit, canEnd, endBatch } = useDirectoryShare(props.token)
const lifecycle = new AbortController()
const sourceDialog = ref(false), scheduleDialog = ref(false)
const directoryAccountId = ref(''), interval = ref('600'), actionError = ref('')
const editable = computed(() => !loading.value && !submitting.value && !active.value && !monitor.value?.hasBatch)
const canSchedule = computed(() => editable.value && !!monitor.value && accountReady.value && !bindingChanged.value && !error.value)
const scheduleState = computed(() => monitor.value?.enabled ? monitor.value.schedulePaused ? '已暂停' : '已开启' : '未开启')
const steps = ['检查目录', '搬入临时目录', '创建分享', '生成 STRM', '回收临时目录']
const stepIndex = computed(() => {
  const stage = monitor.value?.stage || ''
  if (stage === 'completed') return 5
  if (['cleaning', 'checking_share'].includes(stage)) return 4
  if (stage === 'generating') return 3
  if (['sharing', 'configuring', 'verifying'].includes(stage)) return 2
  if (['creating', 'moving'].includes(stage)) return 1
  return 0
})
const intervals = [5, 10, 30, 60].map(minutes => ({ value: String(minutes * 60), label: `每 ${minutes} 分钟` }))
function date(value?: string) {
  if (!value || value.startsWith('0001')) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '—' : parsed.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
function logLink(taskID: string) { return { path: '/logs', query: { module: 'STRM', level: 'ALL', taskID } } }
function request<T>(path: string, init: RequestInit = {}) {
  return p115Request<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(40000)]) })
}
async function mutate(work: () => Promise<void>) {
  if (submitting.value) return
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
    notice.value = '目录已保存。可立即执行，或开启自动执行。'
  })
}
function openSchedule() { interval.value = String(monitor.value?.intervalSeconds || 600); actionError.value = ''; scheduleDialog.value = true }
async function saveSchedule(enabled: boolean) {
  if (!monitor.value) return
  const ok = await mutate(async () => {
    monitor.value = await request<DirectoryShareConfig>(`folder-monitors/${monitor.value!.id}/schedule`, {
      method: 'PUT', body: JSON.stringify({ enabled, intervalSeconds: Number(interval.value) }),
    })
    notice.value = enabled ? '自动执行已开启，目录稳定后将自动分享并生成 STRM。' : '自动执行已关闭，已提交的任务会继续处理。'
  })
  if (ok) scheduleDialog.value = false
}
async function remove() {
  if (!monitor.value || !editable.value || !window.confirm('删除目录分享配置？已生成的 STRM 和网盘文件会保留。')) return
  await mutate(async () => {
    await request(`folder-monitors/${monitor.value!.id}`, { method: 'DELETE' })
    monitor.value = undefined; notice.value = '目录分享配置已删除。'
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
async function start() { actionError.value = ''; await submit() }
onMounted(() => { document.title = '115 目录分享 · Cinema115' })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="目录分享" description="将主网盘目录分享并生成 STRM，支持手动或定时执行。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="space-y-5" :aria-busy="loading">
      <div v-if="error || actionError" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">
        {{ actionError || error }} <button v-if="error" type="button" :disabled="submitting" class="ml-3 underline" @click="refresh">刷新状态</button>
      </div>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ notice }}</p>
      <section class="panel">
        <div class="section-heading"><h2>来源与输出</h2><RouterLink to="/115/share" class="link">修改 STRM 生成规则 →</RouterLink></div>
        <div class="mt-5 grid gap-4 lg:grid-cols-2">
          <div class="min-w-0 rounded-xl bg-slate-50 p-4 dark:bg-slate-950">
            <p class="hint">主网盘 · 来源目录</p>
            <div class="mt-2 flex items-center gap-3">
              <p class="min-w-0 flex-1 break-all text-sm font-medium">{{ loading ? '读取中…' : monitor?.sourcePath || (monitor?.sourceCID ? `目录 ${monitor.sourceCID}` : '尚未选择目录') }}</p>
              <button type="button" :disabled="!editable" class="secondary shrink-0" @click="openSource">{{ monitor ? '更换' : '选择目录' }}</button>
            </div>
            <p v-if="!editable && monitor" class="hint mt-2">当前任务结束后可更换目录。</p>
            <p v-else-if="monitor?.enabled" class="hint mt-2">更换目录后需重新开启自动执行。</p>
          </div>
          <div class="min-w-0 rounded-xl bg-slate-50 p-4 dark:bg-slate-950">
            <p class="hint">STRM 输出目录</p>
            <p class="mt-2 break-all text-sm font-medium">{{ generation?.outputDir ? strmDirectoryPath(generation.outputDir) : '请在分享 STRM 中设置' }}</p>
            <p v-if="generation" class="hint mt-2 break-words">{{ generation.recursive ? '包含子目录' : '仅顶层文件' }} · 最小 {{ generation.minSizeMB || 0 }} MB · {{ generation.videoExtensions || '未设置视频后缀' }}</p>
          </div>
        </div>
        <p v-if="bindingChanged" class="mt-3 text-sm text-amber-700 dark:text-amber-300">主账号已变化，请重新选择来源目录。</p>
        <p v-else-if="!loading && !accountReady" class="mt-3 text-sm text-amber-700 dark:text-amber-300">请先前往 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号。</p>
        <div class="mt-5 flex flex-wrap items-center gap-3">
          <button type="button" :disabled="!!unavailable" :title="unavailable || undefined" class="primary" @click="start">{{ label }}</button>
          <span v-if="unavailable && !active && !submitting" class="hint">{{ unavailable }}</span>
        </div>
        <p class="hint mt-3">全部生成成功后，本批源文件随临时目录移入 115 回收站；失败时保留文件供核对或重试。</p>
      </section>
      <section class="panel">
        <div class="section-heading">
          <div class="flex items-center gap-3"><h2>自动执行</h2><span class="rounded-full bg-slate-100 px-2.5 py-1 text-xs dark:bg-slate-800" :class="monitor?.schedulePaused ? 'text-amber-600 dark:text-amber-300' : monitor?.enabled ? 'text-emerald-600 dark:text-emerald-300' : 'text-slate-500'">{{ scheduleState }}</span></div>
          <div class="flex items-center gap-3"><button v-if="monitor?.enabled" type="button" :disabled="submitting || loading" class="hint px-3 py-2 disabled:opacity-40" @click="saveSchedule(false)">关闭</button><button type="button" :disabled="!canSchedule" class="secondary" @click="openSchedule">{{ monitor?.schedulePaused ? '重新开启' : monitor?.enabled ? '调整间隔' : '开启自动执行' }}</button></div>
        </div>
        <p class="mt-3 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ monitor?.enabled ? `每 ${Math.round((monitor.intervalSeconds || 600) / 60)} 分钟发现新内容，5 秒后复查稳定即执行。下载整理完成后立即检查。` : '开启后，下载整理完成立即检查；其他新内容按设定间隔发现，5 秒后复查稳定即执行。' }}</p>
        <p class="hint mt-2">遇到整理占用时每 5 秒重试；本批完成立即检查下一批，目录为空后恢复定时间隔。</p>
        <p v-if="monitor?.schedulePaused" role="status" class="mt-3 text-sm text-amber-700 dark:text-amber-300">{{ monitor.schedulePaused }}</p>
        <p v-else-if="monitor?.enabled && monitor.scheduleMessage && !active" class="mt-3 text-sm">{{ monitor.scheduleMessage }}</p>
        <div v-if="monitor?.enabled" class="hint mt-3 flex flex-wrap gap-x-6 gap-y-2"><span>最近检查 {{ date(monitor.lastScanAt) }}</span><span>下次检查 {{ active ? '当前任务结束后' : date(monitor.nextScanAt) }}</span></div>
      </section>
      <section v-if="monitor" class="panel">
        <div class="section-heading">
          <h2>{{ active || monitor.hasBatch ? '当前任务' : '任务状态' }}</h2>
          <div class="flex items-center gap-4 text-xs">
            <RouterLink v-if="monitor.logTaskID" :to="logLink(monitor.logTaskID)" class="link">查看日志</RouterLink>
            <details class="relative"><summary class="cursor-pointer text-slate-500 dark:text-slate-400">更多操作</summary><div class="absolute right-0 z-10 mt-2 w-44 space-y-1 rounded-xl border border-slate-200 bg-white p-2 shadow-lg dark:border-slate-700 dark:bg-slate-900"><button type="button" :disabled="!canEnd" class="menu-action text-amber-700 dark:text-amber-300" @click="endBatch">结束本次任务</button><button type="button" :disabled="!editable" class="menu-action text-red-600 dark:text-red-400" @click="remove">删除目录配置</button></div></details>
          </div>
        </div>
        <ol v-if="active || monitor.hasBatch || monitor.stage === 'completed'" class="mt-5 grid grid-cols-2 gap-2 sm:grid-cols-5" aria-label="任务进度">
          <li v-for="(step, index) in steps" :key="step" class="rounded-lg px-3 py-2 text-xs leading-5" :aria-current="index === stepIndex ? 'step' : undefined" :class="index < stepIndex ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : index === stepIndex ? 'bg-blue-50 text-blue-700 dark:bg-blue-950/30 dark:text-blue-300' : 'bg-slate-50 text-slate-400 dark:bg-slate-950 dark:text-slate-500'">{{ index < stepIndex ? '✓' : index + 1 }} {{ step }}</li>
        </ol>
        <p class="mt-4 break-words text-sm leading-6" :class="monitor.lastError ? 'text-red-700 dark:text-red-300' : ''" role="status">{{ stageText }}</p>
        <div v-if="monitor.hasBatch && monitor.lastError" class="mt-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-950/20 dark:text-amber-300">
          <p>核对残留会清理确认安全的空临时目录；仍有文件或状态不明时保留。</p>
          <button type="button" :disabled="submitting || monitor.running || monitor.queued" class="secondary mt-3" @click="recover">{{ submitting ? '处理中…' : '核对残留' }}</button>
        </div>
        <div v-if="monitor.hasBatch || monitor.shareUrl" class="hint mt-4 flex flex-wrap items-center gap-x-5 gap-y-2">
          <span v-if="monitor.hasBatch">已搬入 {{ monitor.batchMoved || 0 }} / {{ monitor.batchCount || 0 }} 项</span>
          <a v-if="monitor.shareUrl" :href="monitor.shareUrl" target="_blank" rel="noopener noreferrer" class="link">查看本次分享 ↗</a>
          <RouterLink v-if="monitor.generationTaskId" to="/115/share/logs" class="link">查看 STRM 生成任务</RouterLink>
        </div>
        <details v-if="monitor.batchDirectoryName || monitor.endedBatches?.length" class="hint mt-5 border-t border-slate-100 pt-4 dark:border-slate-800">
          <summary class="cursor-pointer">临时目录与保留文件</summary>
          <p v-if="monitor.batchDirectoryName" class="mt-2 break-all">本批临时目录：{{ monitor.batchDirectoryName }}</p>
          <div v-for="(batch, index) in monitor.endedBatches" :key="index" class="mt-2 rounded-lg bg-slate-50 p-3 dark:bg-slate-950"><p class="break-all">{{ batch.directoryName || '尚未创建临时目录' }}</p><p>{{ date(batch.endedAt) }} · {{ batch.cleaned ? '结束前已清理' : '文件保留，请自行核对' }}</p><p class="break-all">账号编号 {{ batch.accountId }}<span v-if="batch.directoryId"> · 目录编号 {{ batch.directoryId }}</span></p></div>
        </details>
      </section>
      <section v-if="monitor?.recentRuns?.length" class="panel">
        <h2 class="font-semibold">最近记录 <span class="hint ml-2 font-normal">保留最近 20 次任务</span></h2>
        <ul class="mt-3 divide-y divide-slate-100 dark:divide-slate-800">
          <li v-for="run in monitor.recentRuns" :key="run.taskId" class="flex items-start gap-3 py-3">
            <span class="mt-0.5 shrink-0 rounded bg-slate-50 px-2 py-1 text-xs dark:bg-slate-950" :class="run.status === 'completed' ? 'text-emerald-600 dark:text-emerald-300' : run.status === 'ended' ? 'text-slate-500' : 'text-amber-700 dark:text-amber-300'">{{ run.status === 'completed' ? '完成' : run.status === 'ended' ? '已结束' : '未完成' }}</span>
            <div class="min-w-0 flex-1"><p class="break-words text-sm">{{ run.message }}</p><p class="hint mt-1">{{ date(run.time) }} · {{ run.trigger === 'scheduled' ? '定时执行' : '手动执行' }}</p></div>
            <RouterLink :to="logLink(run.taskId)" class="link shrink-0 py-1">日志</RouterLink>
          </li>
        </ul>
      </section>
    </div>
    <P115DirectoryDialog v-if="sourceDialog" :token="token" :account-id="directoryAccountId" title="选择分享目录 · 选择后自动保存" @close="sourceDialog = false" @select="selectSource" />
    <P115ActionDialog v-if="scheduleDialog" title="自动分享并生成 STRM" :busy="submitting" @close="scheduleDialog = false">
      <p class="break-all">来源：{{ monitor?.sourcePath || monitor?.sourceCID }}</p>
      <div class="flex items-center justify-between gap-4"><span>常规检查间隔</span><SelectMenu v-model="interval" :options="intervals" :disabled="submitting" label="常规检查间隔" /></div>
      <p>到点发现内容后等待 5 秒复查，无变化即开始处理，不等下一轮定时。下载整理完成后立即检查；遇到占用每 5 秒重试，本批完成立即检查下一批。发生错误后暂停，等待核对。</p>
      <p class="rounded-xl bg-amber-50 p-3 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">全部生成成功后，本批源文件随临时目录移入 115 回收站。关闭自动执行只停止新批次，已提交的任务继续处理。</p>
      <p v-if="actionError" role="alert" class="text-red-600 dark:text-red-400">{{ actionError }}</p>
      <template #footer><button type="button" :disabled="submitting" class="secondary" @click="scheduleDialog = false">取消</button><button type="button" :disabled="submitting" class="primary" @click="saveSchedule(true)">{{ submitting ? '保存中…' : '确认开启' }}</button></template>
    </P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.panel { @apply rounded-2xl border border-slate-200 bg-white p-5 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200 sm:p-6; }
.section-heading { @apply flex flex-wrap items-center justify-between gap-3; }
.section-heading h2 { @apply font-semibold text-slate-900 dark:text-slate-100; }
.hint { @apply text-xs leading-6 text-slate-500 dark:text-slate-400; }
.link { @apply text-xs text-blue-600 hover:underline dark:text-blue-400; }
.primary { @apply rounded-xl bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40; }
.secondary { @apply rounded-lg border border-slate-200 px-3 py-2 text-xs disabled:opacity-40 dark:border-slate-700; }
.menu-action { @apply w-full rounded-lg px-3 py-2 text-left hover:bg-slate-50 disabled:opacity-40 dark:hover:bg-slate-800; }
</style>
