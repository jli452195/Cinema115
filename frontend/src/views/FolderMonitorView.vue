<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'

interface Monitor {
  id: string; sourceCID: string; sourcePath: string; enabled: boolean; intervalSeconds: number; stage: string
  shareUrl: string; shareReviewing: boolean; batchDirectoryName?: string; batchMoved?: number
  lastScanAt: string; nextScanAt: string; lastError: string
  running: boolean; queued: boolean; trigger: string; hasBatch: boolean
}
interface AutomationSettings { accountId: string }

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const monitor = ref<Monitor | null>(null)
const accountId = ref('')
const sourceDialog = ref(false)
const sourceCID = ref('')
const sourcePath = ref('')
const enabled = computed(() => monitor.value?.enabled ?? false)
const intervalValue = ref(5)
const intervalUnit = ref(60)
const intervalSeconds = computed(() => intervalValue.value * intervalUnit.value)
const intervalValid = computed(() => Number.isInteger(intervalValue.value) && intervalValue.value > 0 && Number.isInteger(intervalSeconds.value) && intervalSeconds.value >= 30 && intervalSeconds.value <= 86400)
const loading = ref(true)
const saving = ref(false)
const busy = ref(false)
const toggling = ref(false)
const error = ref('')
const notice = ref('')
const refreshError = ref('')
let pollTimer: ReturnType<typeof setTimeout> | undefined
let revision = 0

const stageLabels: Record<string, string> = {
  waiting: '暂无任务', empty: '目录为空，等待新文件', moving: '正在移动目录内容',
  stabilizing: '确认临时目录内容', sharing: '正在创建分享', configuring: '正在设置长期分享',
  verifying: '正在核对分享', generating: '正在生成 STRM', cleaning: '正在移入回收站',
  checking_share: '正在复核分享', completed: '上一批已完成', error: '处理失败，等待重试', blocked: '需要人工核对',
  sharing_legacy: '正在创建分享',
}
const stageText = computed(() => {
  if (loading.value) return '正在读取设置…'
  if (!monitor.value) return '尚未配置'
  if (monitor.value.queued) return '已排队，等待处理'
  if (monitor.value.lastError && monitor.value.stage !== 'blocked') return monitor.value.hasBatch ? '本次任务失败，临时目录已保留' : '检查失败，等待重试'
  return stageLabels[monitor.value.stage] || monitor.value.stage
})
const taskActive = computed(() => !!monitor.value && (monitor.value.running || monitor.value.queued || (monitor.value.hasBatch && !monitor.value.lastError && monitor.value.stage !== 'blocked')))
const editable = computed(() => !monitor.value || (!taskActive.value && !monitor.value.hasBatch && ['waiting', 'empty', 'completed', 'error'].includes(monitor.value.stage)))
const settingsDisabled = computed(() => loading.value || saving.value || busy.value || toggling.value || !editable.value)
const dirty = computed(() => monitor.value
  ? sourceCID.value !== monitor.value.sourceCID || intervalSeconds.value !== monitor.value.intervalSeconds
  : !!sourceCID.value)
const canProcess = computed(() => !!monitor.value && !['blocked', 'sharing_legacy'].includes(monitor.value.stage) && !(monitor.value.stage === 'error' && monitor.value.hasBatch) && !taskActive.value && !dirty.value && !loading.value && !saving.value && !busy.value && !toggling.value)
const processLabel = computed(() => busy.value ? '提交中…' : taskActive.value ? (monitor.value?.running ? '任务执行中' : '等待处理') : monitor.value?.lastError ? (monitor.value.hasBatch ? '重试本次任务' : '重新检查') : '立即处理')
const triggerText = computed(() => monitor.value?.trigger === 'manual' ? '手动触发' : monitor.value?.trigger === 'scheduled' ? '定时触发' : '')
const nextCheckText = computed(() => !monitor.value?.enabled ? '定时已关闭' : monitor.value.stage === 'blocked' ? '需先人工核对' : taskActive.value ? '本次任务结束后重新计时' : formatTime(monitor.value.nextScanAt))

function setMonitor(value?: Monitor) {
  monitor.value = value || null
  sourceCID.value = value?.sourceCID || ''
  sourcePath.value = value?.sourcePath || ''
  const seconds = value?.intervalSeconds || 300
  if (seconds % 3600 === 0) { intervalValue.value = seconds / 3600; intervalUnit.value = 3600 }
  else if (seconds % 60 === 0) { intervalValue.value = seconds / 60; intervalUnit.value = 60 }
  else { intervalValue.value = seconds; intervalUnit.value = 1 }
}

async function refresh(initial = false) {
  if (initial) loading.value = true
  const requestRevision = revision
  try {
    const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
    const [monitorData, automation] = await Promise.all([
      p115Request<{ monitors: Monitor[] }>(props.token, 'folder-monitors', { signal }),
      p115Request<{ settings: AutomationSettings }>(props.token, 'share/automation-config', { signal }),
    ])
    if (lifecycle.signal.aborted || requestRevision !== revision) return
    accountId.value = automation.settings.accountId || ''
    const value = monitorData.monitors?.[0]
    // Poll only runtime status when the user has unsaved form edits.
    if (!dirty.value && !saving.value) setMonitor(value)
    else monitor.value = value || null
    refreshError.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted && requestRevision === revision) refreshError.value = reason instanceof Error ? reason.message : '刷新状态失败'
  } finally { if (initial) loading.value = false }
}

function schedulePoll() {
  if (lifecycle.signal.aborted) return
  pollTimer = setTimeout(async () => {
    if (!saving.value && !busy.value && !toggling.value) await refresh()
    schedulePoll()
  }, taskActive.value ? 3000 : 10000)
}

async function toggleSchedule() {
  if (!monitor.value || toggling.value) return
  const next = !monitor.value.enabled
  revision++; toggling.value = true; error.value = ''; notice.value = ''
  try {
    const value = await p115Request<Monitor>(props.token, `folder-monitors/${monitor.value.id}/schedule`, {
      method: 'PUT', body: JSON.stringify({ enabled: next }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    monitor.value = value
    notice.value = next ? '定时检查已开启，将按已保存的间隔检查。' : '定时检查已关闭；仍可手动处理，已提交的任务会继续完成。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存定时开关失败'
  } finally { toggling.value = false; await refresh() }
}

function openSource() {
  if (!accountId.value) { error.value = '请先在 115 分享设置中选择已登录账号'; return }
  sourceDialog.value = true
}
function selectSource(value: { id: string; path: string }) {
  sourceDialog.value = false
  if (value.id === '0') { error.value = '请选择非根目录作为监控目录'; return }
  sourceCID.value = value.id; sourcePath.value = value.path
}

async function save() {
  if (!sourceCID.value) { error.value = '请选择监控目录'; return }
  if (!intervalValid.value) { error.value = '请输入正整数，巡检间隔须在 30 秒到 24 小时之间'; return }
  revision++; saving.value = true; error.value = ''; notice.value = ''
  try {
    const id = monitor.value?.id
    const payload = { id, sourceCID: sourceCID.value, sourcePath: sourcePath.value, enabled: enabled.value, intervalSeconds: intervalSeconds.value }
    const path = id ? `folder-monitors/${id}` : 'folder-monitors'
    const value = await p115Request<Monitor>(props.token, path, { method: id ? 'PUT' : 'POST', body: JSON.stringify(payload), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(20000)]) })
    setMonitor(value)
    notice.value = '目录自动分享设置已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存目录自动分享设置失败'
  } finally { saving.value = false }
}

async function scan() {
  if (!monitor.value || !canProcess.value) return
  revision++; busy.value = true; error.value = ''; notice.value = ''
  try {
    const result = await p115Request<{ accepted: boolean; message?: string }>(props.token, `folder-monitors/${monitor.value.id}/scan`, { method: 'POST', body: '{}', signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    notice.value = result.accepted ? '已提交一次处理任务，定时开关保持不变。' : result.message || '任务已在处理中。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '安排处理失败' }
  finally { await refresh(); busy.value = false }
}

async function remove() {
  if (!monitor.value || !window.confirm('删除目录自动分享配置？不会删除 115 文件、分享或已生成 STRM。')) return
  error.value = ''; notice.value = ''
  revision++; busy.value = true
  try {
    await p115Request(props.token, `folder-monitors/${monitor.value.id}`, { method: 'DELETE', signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    setMonitor(); notice.value = '目录自动分享配置已删除。'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '删除目录自动分享配置失败' }
  finally { busy.value = false }
}
function formatTime(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '—' }

function formatInterval(seconds: number) {
  const parts: string[] = []
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  const remainder = seconds % 60
  if (hours) parts.push(`${hours} 小时`)
  if (minutes) parts.push(`${minutes} 分钟`)
  if (remainder) parts.push(`${remainder} 秒`)
  return parts.join(' ') || '—'
}

onMounted(() => {
  document.title = '115 目录自动分享 · Cinema115'
  void refresh(true).finally(schedulePoll)
})
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(pollTimer) })
</script>

<template>
  <ManagementLayout title="115 目录自动分享" description="手动或定时处理指定目录，创建分享并生成 STRM。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <section class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm" :aria-busy="loading">
      <div class="p-5 sm:p-6">
        <div class="flex items-start justify-between gap-5">
          <div class="min-w-0">
            <h2 class="font-semibold text-slate-900">目录与处理设置</h2>
            <p class="mt-1 text-xs leading-6 text-slate-500">
              {{ accountId ? '使用「115 分享」中的账号与生成设置' : '请先在「115 分享」中选择已登录账号' }}
              <RouterLink to="/115/share" class="ml-2 inline-block rounded text-blue-600 hover:text-blue-700 focus-visible:outline-blue-500">设置 <span aria-hidden="true">↗</span></RouterLink>
            </p>
          </div>
        </div>

        <div class="mt-6 space-y-5">
          <div>
            <p id="monitor-directory-label" class="text-sm font-medium text-slate-700">监控目录</p>
            <div class="mt-2 flex flex-wrap items-center gap-3 rounded-xl border border-slate-200 bg-slate-50 px-4 py-3">
              <p class="min-w-0 flex-1 break-all text-sm text-slate-600" :title="sourceCID ? '目录编号：' + sourceCID : undefined" aria-labelledby="monitor-directory-label">{{ sourcePath || '请选择需要自动处理的目录' }}</p>
              <button type="button" :disabled="settingsDisabled" class="shrink-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 transition hover:border-blue-300 hover:text-blue-600 focus-visible:outline-blue-500 disabled:opacity-40" @click="openSource">选择目录</button>
            </div>
          </div>

          <div class="rounded-xl border border-slate-200 p-4">
            <div class="mb-4 flex items-start justify-between gap-4">
              <div>
                <p id="monitor-enabled-label" class="text-sm font-medium text-slate-700">定时检查 <span class="ml-2 text-xs font-normal text-slate-400">{{ toggling ? '保存中…' : enabled ? '已开启' : '已关闭' }}</span></p>
                <p id="monitor-schedule-hint" class="mt-1 text-xs leading-5 text-slate-500">{{ monitor ? '开关立即生效。关闭仅停止定时检查，不影响手动处理或已提交的任务。' : '请先选择目录并保存，再开启定时检查；也可以只手动处理。' }}</p>
              </div>
              <button type="button" role="switch" :aria-checked="enabled" aria-labelledby="monitor-enabled-label" aria-describedby="monitor-schedule-hint" :disabled="!monitor || loading || saving || busy || toggling" class="relative mt-1 h-6 w-11 shrink-0 rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-2 disabled:opacity-40" :class="enabled ? 'bg-blue-600' : 'bg-slate-300'" @click="toggleSchedule">
                <span aria-hidden="true" class="monitor-switch-thumb h-4 w-4 rounded-full bg-white shadow-sm"></span>
              </button>
            </div>
            <label for="monitor-interval" class="text-sm font-medium text-slate-700">检查间隔</label>
            <div class="mt-2 flex flex-wrap items-center gap-2 text-sm text-slate-500">
              <span>每</span>
              <input id="monitor-interval" v-model.number="intervalValue" type="number" :min="Math.ceil(30 / intervalUnit)" :max="Math.floor(86400 / intervalUnit)" step="1" :disabled="settingsDisabled" :aria-invalid="!intervalValid" aria-describedby="monitor-interval-hint" class="h-10 w-24 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100 disabled:opacity-40 sm:w-28" />
              <select v-model.number="intervalUnit" aria-label="检查间隔单位" :disabled="settingsDisabled" class="h-10 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100 disabled:opacity-40"><option :value="1">秒</option><option :value="60">分钟</option><option :value="3600">小时</option></select>
              <span class="whitespace-nowrap">检查一次</span>
            </div>
            <p id="monitor-interval-hint" class="mt-2 text-xs leading-5" :class="intervalValid ? 'text-slate-400' : 'text-red-600'">{{ intervalValid ? '支持 30 秒至 24 小时，保存后生效。开启定时后按此间隔检查，任务结束后重新计时。' : '请输入正整数，间隔须在 30 秒至 24 小时之间。' }}</p>
          </div>
        </div>

        <details class="mt-5 text-xs leading-6 text-slate-500">
          <summary class="cursor-pointer rounded hover:text-slate-700 focus-visible:outline-blue-500">目录内容会移入临时目录处理，成功后移入回收站。</summary>
          <p class="mt-2 pl-4 text-slate-400">任务会将监控目录内容移入独立临时目录，排空后分享其中的内容并生成 STRM。此后新增的文件留待下次处理；分享或生成失败时保留临时目录，不清空回收站。</p>
        </details>

        <p v-if="error" role="alert" class="mt-4 rounded-xl bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
        <p v-if="refreshError" role="alert" class="mt-4 rounded-xl bg-amber-50 p-3 text-sm text-amber-800">状态刷新失败：{{ refreshError }}。将自动重试。</p>
        <p v-if="notice" role="status" class="mt-4 rounded-xl bg-emerald-50 p-3 text-sm text-emerald-700">{{ notice }}</p>
        <div class="mt-5 flex flex-wrap items-center gap-3">
          <button type="button" :disabled="settingsDisabled || !intervalValid" class="rounded-xl bg-blue-600 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 focus-visible:outline-blue-500 disabled:opacity-40" @click="save">{{ saving ? '保存中…' : '保存设置' }}</button>
          <button v-if="monitor" type="button" :disabled="!canProcess" :title="dirty ? '请先保存设置' : taskActive ? '当前任务结束后可再次处理' : undefined" class="rounded-xl border border-slate-200 px-5 py-2.5 text-sm font-medium text-slate-700 transition hover:border-blue-300 hover:text-blue-600 focus-visible:outline-blue-500 disabled:opacity-40" @click="scan">{{ processLabel }}</button>
          <details v-if="monitor" class="relative ml-auto">
            <summary class="cursor-pointer rounded-lg px-2 py-2 text-xs text-slate-400 hover:bg-slate-50 hover:text-slate-600 focus-visible:outline-blue-500">更多</summary>
            <div class="absolute right-0 top-full z-10 mt-1 w-36 rounded-xl border border-slate-200 bg-white p-1.5 shadow-lg">
              <button type="button" :disabled="loading || saving || busy || toggling || taskActive || !['waiting','empty','completed','error','blocked'].includes(monitor.stage)" class="w-full rounded-lg px-3 py-2 text-left text-sm text-red-600 hover:bg-red-50 focus-visible:outline-red-500 disabled:opacity-40" @click="remove">删除配置</button>
            </div>
          </details>
        </div>
        <p id="monitor-save-hint" class="mt-3 text-xs leading-5" :class="dirty ? 'text-amber-600' : 'text-slate-400'">{{ !editable ? '当前批次尚未结束，目录与间隔暂不可修改；定时开关可单独调整。' : dirty ? '目录或间隔有未保存的修改，点击「保存设置」生效。' : '立即处理只执行一次，与定时开关无关。' }}</p>
      </div>

      <div class="border-t border-slate-100 bg-slate-50/60 px-5 py-4 sm:px-6">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p role="status" class="flex items-center gap-2 text-sm text-slate-600">
            <span aria-hidden="true" class="h-1.5 w-1.5 rounded-full" :class="monitor?.lastError || monitor?.stage === 'blocked' ? 'bg-amber-500' : taskActive ? 'bg-blue-500' : 'bg-slate-300'"></span>
            任务：{{ stageText }}<span v-if="triggerText" class="text-xs text-slate-400">· {{ triggerText }}</span>
          </p>
          <a v-if="monitor?.shareUrl" :href="monitor.shareUrl" target="_blank" rel="noopener noreferrer" class="rounded text-xs text-blue-600 hover:underline focus-visible:outline-blue-500">最近分享：查看链接 <span aria-hidden="true">↗</span></a>
        </div>
        <template v-if="monitor">
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-xs leading-6 text-slate-400">
            <span>定时：{{ monitor.enabled ? '已开启' : '已关闭' }}</span>
            <span>上次检查：{{ formatTime(monitor.lastScanAt) }}</span>
            <span>下次检查：{{ nextCheckText }}</span>
          </div>
          <p v-if="monitor.lastError" role="alert" class="mt-3 break-words rounded-lg bg-amber-50 px-3 py-2 text-xs leading-6 text-amber-800">{{ monitor.lastError }}</p>
          <details class="mt-3 text-xs text-slate-500">
            <summary class="w-fit cursor-pointer rounded hover:text-slate-700 focus-visible:outline-blue-500">处理详情</summary>
            <dl class="mt-3 grid gap-3 sm:grid-cols-2">
              <div><dt class="text-slate-400">已保存的检查间隔</dt><dd class="mt-1">{{ formatInterval(monitor.intervalSeconds) }}</dd></div>
              <div><dt class="text-slate-400">已搬入临时目录</dt><dd class="mt-1">{{ monitor.batchMoved || 0 }} 项</dd></div>
              <div class="sm:col-span-2"><dt class="text-slate-400">临时目录</dt><dd class="mt-1 break-all">{{ monitor.batchDirectoryName || '无' }}</dd></div>
            </dl>
            <p v-if="monitor.shareReviewing" class="mt-3 leading-6">最近分享仍在审核中，处理时已使用任务账号验证可读。</p>
          </details>
        </template>
      </div>
    </section>
    <P115DirectoryDialog v-if="sourceDialog" :token="token" :account-id="accountId" title="选择监控目录" @close="sourceDialog = false" @select="selectSource" />
  </ManagementLayout>
</template>

<style scoped>
.monitor-switch-thumb {
  position: absolute;
  left: 4px;
  top: 4px;
  transform: translateX(0);
  transition: transform 150ms ease;
}
button[aria-checked="true"] .monitor-switch-thumb { transform: translateX(20px); }
@media (prefers-reduced-motion: reduce) {
  .monitor-switch-thumb { transition: none; }
}
</style>
