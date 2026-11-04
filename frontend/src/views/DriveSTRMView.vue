<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconFolder, IconFolderCheck, IconEdit, IconTrash, IconRefresh, IconSearch, IconLoader2, IconSettings, IconCode, IconChevronRight, IconClock } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath } from '../utils/strmDirectory'

interface Settings {
  baseUrl: string; videoExtensions: string; metadataExtensions: string; minSizeMB: number
  eventIntervalSeconds: number
  metadataImageMode: 'cdn' | 'original'; metadataConcurrency: number; metadataBatchIntervalSeconds: number
}
interface Schedule {
  reconcileEnabled: boolean; reconcileIntervalHours: number
  fullScanEnabled: boolean; fullScanIntervalHours: number
}
interface Mapping {
  id: string; name: string; accountId: string; sourceCID: string; sourcePath: string; outputDir: string; enabled: boolean
  status: string; mode?: string; queued: boolean; lastScanAt?: string; lastError?: string
  metadataPending: number; metadataFailed: number
  schedule: Schedule
  stats: { videos: number; written: number; unchanged: number; metadataWritten: number; failed: number }
}
interface State {
  mappings: Mapping[]; settings: Settings; settingsSaved: boolean; primaryAccountId: string; accountReady: boolean
  events?: { lastCheckedAt?: string; lastError?: string; consumers?: Record<string, { pendingBatches: number; lastError?: string }> }
  attachments?: { coolingUntil?: string; lastError?: string; probing?: boolean }
}
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const state = ref<State>(), editor = ref<Mapping>(), deleting = ref<Mapping>()
const settingsDraft = ref<Settings>(), savedSettings = ref<Settings>()
let scheduleSnapshot = ''
let editorTrigger: HTMLElement | null = null
const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true)
const sourceDialog = ref(false), outputDialog = ref(false)
const scanSchedules = [
  { enabled: 'reconcileEnabled', interval: 'reconcileIntervalHours', name: '增量核对', intervals: [6, 12, 24, 72, 168], help: '比对目录树与补齐缺失，减少资料请求' },
  { enabled: 'fullScanEnabled', interval: 'fullScanIntervalHours', name: '全量扫描', intervals: [24, 72, 168, 336, 720], help: '读取全部文件资料，核验同路径替换' },
] as const
const lifecycle = new AbortController()
const addButton = ref<HTMLButtonElement>()
let timer: number | undefined, pendingRefresh: Promise<void> | undefined, generation = 0, snapshot = ''
const active = (m: Mapping) => m.status === 'running' || m.queued && !m.lastError
const pendingMetadata = (m: Mapping) => (m.metadataPending || 0) > 0
const downloadingMetadata = (m: Mapping) => (m.metadataPending || 0) > (m.metadataFailed || 0)
const attachmentCooling = computed(() => Date.parse(state.value?.attachments?.coolingUntil || '') > Date.now())
const attachmentMessage = computed(() => !state.value?.mappings.some(pendingMetadata) ? '' : attachmentCooling.value
  ? `附件下载已暂缓，预计 ${date(state.value?.attachments?.coolingUntil)} 后先尝试一个附件，成功后恢复。${state.value?.attachments?.lastError || ''}`
  : state.value?.attachments?.probing ? '附件下载正在尝试恢复，成功后继续处理待补文件。' : '')
const directoryDirty = computed(() => !!editor.value && JSON.stringify(directoryInput(editor.value)) !== snapshot)
const scheduleDirty = computed(() => !!editor.value && JSON.stringify(editor.value.schedule) !== scheduleSnapshot)
const editorDirty = computed(() => directoryDirty.value || scheduleDirty.value)
const settingsDirty = computed(() => JSON.stringify(settingsDraft.value) !== JSON.stringify(savedSettings.value))
const needsSettingsSave = computed(() => !!settingsDraft.value && (!state.value?.settingsSaved || settingsDirty.value))
const unsaved = computed(() => editorDirty.value || settingsDirty.value)
const settingsReady = computed(() => !!state.value?.settingsSaved && !settingsDirty.value)
const hasSchedule = computed(() => !!state.value && (state.value.mappings.length
  ? state.value.mappings.some(mappingHasSchedule)
  : state.value.settings.eventIntervalSeconds > 0))
const canStart = computed(() => settingsReady.value && !!state.value?.accountReady)
const editorRunning = computed(() => !!editor.value?.id && !!state.value?.mappings.some(m => m.id === editor.value!.id && (m.status === 'running' || m.queued)))
const directoryReadOnly = computed(() => editorRunning.value || !canStart.value)
const savesDirectory = computed(() => !!editor.value && (!editor.value.id || directoryDirty.value))
const editorSaveLabel = computed(() => !editor.value?.id ? '保存目录' : directoryDirty.value ? scheduleDirty.value ? '保存更改' : '保存目录' : '保存自动任务')
const canSaveEditor = computed(() => !!editor.value && (!editor.value.id || editorDirty.value) && (savesDirectory.value
  ? !directoryReadOnly.value && !!editor.value.name.trim() && !!editor.value.sourceCID && !!editor.value.outputDir && !changedAccount(editor.value)
  : scheduleDirty.value))
const startHint = computed(() => !state.value?.settingsSaved ? '请先保存基础设置' : settingsDirty.value ? '请先保存或还原基础设置的修改' : !state.value?.accountReady ? '请先配置并登录主账号' : undefined)
const strmPreview = computed(() => (settingsDraft.value?.baseUrl.trim().replace(/\/+$/, '') || 'http://NAS_IP:8080') + '/api/drive115/ecjq9ichcb40lzlvx/123456789/0123456789ABCDEF0123456789ABCDEF01234567/1073741824/Example.S01E01.mkv')
const changedAccount = (m: Mapping) => !!state.value && m.accountId !== state.value.primaryAccountId
const eventConsumerErrors = computed(() => Object.entries(state.value?.events?.consumers || {}).filter(([, value]) => value.lastError).map(([name, value]) => ({ name, label: name === 'strm' ? 'STRM' : name === 'organize' ? '自动整理' : name, message: value.lastError })))
const requestSignal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(60000)])
const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(props.token, 'drive-strm' + path, { ...init, signal: requestSignal() })
const failure = (e: unknown) => e instanceof Error && e.name === 'TimeoutError' ? '请求超时，请重新加载确认结果后再重试' : e instanceof Error ? e.message : '操作失败'
function acceptSettings(value: Settings) {
  const settings = { ...value }
  settings.metadataImageMode ||= 'cdn'
  settings.metadataConcurrency ??= 4
  settings.metadataBatchIntervalSeconds ??= 3
  savedSettings.value = { ...settings }; settingsDraft.value = settings
}
function refresh(): Promise<void> {
  if (pendingRefresh) return pendingRefresh
  if (document.hidden || lifecycle.signal.aborted) return Promise.resolve()
  pendingRefresh = (async () => {
    while (!lifecycle.signal.aborted) {
      const version = generation
      try {
        const next = await request<State>()
        if (version === generation) {
          if (!settingsDraft.value || !settingsDirty.value) acceptSettings(next.settings)
          state.value = next; error.value = ''; return
        }
      } catch (e) {
        if (version === generation) { if (!lifecycle.signal.aborted) error.value = failure(e); return }
      }
    }
  })().finally(() => { pendingRefresh = undefined; loading.value = false })
  return pendingRefresh
}
function poll() {
  window.clearTimeout(timer)
  if (lifecycle.signal.aborted || document.hidden) return
  const running = state.value?.mappings.some(m => active(m) || downloadingMetadata(m))
  if (!running && !state.value?.mappings.some(m => m.enabled || pendingMetadata(m))) return
  timer = window.setTimeout(async () => { await refresh(); poll() }, running ? 4000 : 30000)
}
async function act(key: string, fn: () => Promise<void>, dialog = false) {
  if (busy.value) return
  window.clearTimeout(timer)
  busy.value = key; (dialog ? dialogError : error).value = ''; notice.value = ''
  try { await fn() } catch (e) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = failure(e) }
  finally { busy.value = ''; poll() }
}
async function saveSettings() {
  if (!needsSettingsSave.value) return
  await act('settings', async () => {
    const input = { ...settingsDraft.value! }
    const settings = await request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(input) })
    generation++; acceptSettings(settings)
    if (state.value) { state.value.settings = settings; state.value.settingsSaved = true }
    notice.value = '基础设置已保存；图片策略用于后续任务，降低并发或延长取链间隔也会约束待补附件。已有 STRM 可通过完整扫描更新。'
    await refresh()
  })
}
function resetSettings() { if (savedSettings.value) acceptSettings(state.value?.settings || savedSettings.value) }
function initialSchedule(): Schedule {
  return { reconcileEnabled: true, reconcileIntervalHours: 24, fullScanEnabled: false, fullScanIntervalHours: 720 }
}
function normalizeSchedule(value: Schedule): Schedule {
  return {
    reconcileEnabled: value.reconcileEnabled && value.reconcileIntervalHours > 0,
    reconcileIntervalHours: value.reconcileIntervalHours || 24,
    fullScanEnabled: value.fullScanEnabled && value.fullScanIntervalHours > 0,
    fullScanIntervalHours: value.fullScanIntervalHours || 720,
  }
}
function effectiveSchedule(m: Mapping) { return normalizeSchedule(m.schedule || initialSchedule()) }
function mappingHasSchedule(m: Mapping) {
  const schedule = effectiveSchedule(m)
  return state.value!.settings.eventIntervalSeconds > 0 || schedule.reconcileEnabled || schedule.fullScanEnabled
}
function eventLabel() {
  const seconds = state.value!.settings.eventIntervalSeconds
  return seconds === 0 ? '事件已关闭' : seconds < 60 ? `事件每 ${seconds} 秒` : `事件每 ${seconds / 60} 分钟`
}
function scheduleLabel(m: Mapping, type: 'reconcile' | 'fullScan') {
  const schedule = effectiveSchedule(m)
  return schedule[type === 'reconcile' ? 'reconcileEnabled' : 'fullScanEnabled']
    ? intervalLabel(schedule[type === 'reconcile' ? 'reconcileIntervalHours' : 'fullScanIntervalHours']) : '关闭'
}
function directoryInput(m: Mapping) {
  return { name: m.name, accountId: m.accountId, sourceCID: m.sourceCID, outputDir: m.outputDir, enabled: m.enabled }
}
function edit(m?: Mapping) {
  if (busy.value || !m && !canStart.value) return
  if (editorDirty.value && !window.confirm('放弃未保存的修改？')) return
  editorTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  editor.value = m ? { ...m, stats: { ...m.stats } } : { id: '', name: '', accountId: state.value?.primaryAccountId || '', sourceCID: '', sourcePath: '', outputDir: '', enabled: true, status: 'idle', queued: false, schedule: initialSchedule(), metadataPending: 0, metadataFailed: 0, stats: { videos: 0, written: 0, unchanged: 0, metadataWritten: 0, failed: 0 } }
  editor.value.schedule = effectiveSchedule(editor.value)
  snapshot = JSON.stringify(directoryInput(editor.value)); scheduleSnapshot = JSON.stringify(editor.value.schedule)
  dialogError.value = ''
}
function closeEditor() { if (!busy.value && (!editorDirty.value || window.confirm('放弃未保存的修改？'))) editor.value = undefined }
async function selectSource() {
  if (directoryReadOnly.value) return
  await act('source', async () => {
    const data = await p115Request<{ accounts: { id: string; primary: boolean; configured: boolean }[] }>(props.token, 'accounts', { signal: requestSignal() })
    const primary = data.accounts.find(a => a.primary && a.configured)
    if (!primary) throw new Error('请先在 115 助手中配置并登录主账号')
    if (editor.value) {
      if (editor.value.accountId !== primary.id) { editor.value.sourceCID = ''; editor.value.sourcePath = '' }
      editor.value.accountId = primary.id
    }
    sourceDialog.value = true
  }, true)
}
function pickedSource(value: { id: string; path: string; name: string }) {
  if (editor.value) { editor.value.sourceCID = value.id; editor.value.sourcePath = value.path; if (!editor.value.name) editor.value.name = value.name }
  sourceDialog.value = false
}
async function save() {
  if (!canSaveEditor.value || !editor.value) return
  const trigger = editorTrigger
  await act('save', async () => {
    const m = editor.value!
    const directory = savesDirectory.value
    if (directory) {
      await request(m.id ? '/' + m.id : '', { method: m.id ? 'PUT' : 'POST', body: JSON.stringify({ ...directoryInput(m), ...(!m.id || scheduleDirty.value ? { schedule: { ...m.schedule } } : {}) }) })
    } else {
      await request('/' + m.id + '/schedule', { method: 'PUT', body: JSON.stringify({ schedule: { ...m.schedule } }) })
    }
    generation++; editor.value = undefined
    notice.value = directory ? '目录设置已保存；新目录或来源变更将完整扫描，自动任务按各自周期执行。' : '目录自动任务已保存；后续按新周期执行，当前任务会继续完成。'
    await refresh()
  }, true)
  if (!editor.value) { await nextTick(); if (trigger?.isConnected) trigger.focus({ preventScroll: true }) }
}
async function run(m: Mapping, full: boolean) {
  if (!canStart.value) return
  await act(m.id, async () => { await request('/' + m.id + (full ? '/scan' : '/check'), { method: 'POST' }); generation++; notice.value = full ? '已安排完整扫描，将补齐本地缺少的文件' : '已请求检查生活事件，变更将由后台处理'; await refresh() })
}
async function toggle(m: Mapping, enabled: boolean) {
  if (enabled && !canStart.value) return
  await act(m.id, async () => { await request('/' + m.id + '/monitor', { method: 'PUT', body: JSON.stringify({ enabled }) }); generation++; notice.value = enabled ? '自动监控已开启' : '自动监控已暂停，当前扫描会继续完成'; await refresh() })
}
async function stop(m: Mapping) {
  await act(m.id, async () => { await request('/' + m.id + '/stop', { method: 'POST' }); generation++; notice.value = '已请求停止，可稍后完整扫描补齐'; await refresh() })
}
async function remove() {
  await act('delete', async () => { await request('/' + deleting.value!.id, { method: 'DELETE' }); generation++; deleting.value = undefined; notice.value = '目录配置已删除，本地与网盘文件均已保留'; await refresh(); await nextTick(); addButton.value?.focus() }, true)
}
function requestDelete(m: Mapping) {
  deleting.value = m; dialogError.value = ''
}
function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未检查' }
function status(m: Mapping) { return m.status === 'running' ? (m.mode === 'changes' ? '正在检查变更' : m.mode === 'tree' ? '正在核对目录' : '正在完整扫描') : m.status === 'stopped' ? '已停止' : m.lastError ? '需检查' : m.queued ? '等待扫描' : pendingMetadata(m) ? attachmentCooling.value ? '附件冷却中' : downloadingMetadata(m) ? '附件补齐中' : '附件待重试' : !m.enabled ? '已暂停' : mappingHasSchedule(m) ? '监控中' : '仅手动' }
function intervalLabel(hours: number) { return hours < 24 ? `每 ${hours} 小时` : hours === 24 ? '每天' : hours === 168 ? '每周' : `每 ${hours / 24} 天` }
async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
function beforeUnload(e: BeforeUnloadEvent) { if (unsaved.value || busy.value) { e.preventDefault(); e.returnValue = '' } }
onBeforeRouteLeave(() => {
  if (busy.value) { (editor.value || deleting.value ? dialogError : error).value = '正在处理当前操作，请完成后再切换页面'; return false }
  return !unsaved.value || window.confirm('放弃未保存的修改？')
})
onMounted(async () => { document.title = '115网盘STRM · Cinema115'; document.addEventListener('visibilitychange', visible); window.addEventListener('beforeunload', beforeUnload); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout class="ds-page" title="115网盘STRM" description="从主网盘生成本地 STRM，保留原目录层级。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <p v-if="error" role="alert" class="ds-alert">{{ error }} <button type="button" class="underline" :disabled="!!busy" @click="refresh">重新加载</button></p>
    <p v-if="notice" role="status" class="ds-notice">{{ notice }}</p>
    <p v-if="state && !state.accountReady" class="ds-alert">请先在 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号，基础设置可先保存。</p>
    <p v-if="state?.events?.lastError" role="status" class="ds-alert">事件检查暂不可用：{{ state.events.lastError }}。仍可完整扫描补齐。</p>
    <p v-for="item in eventConsumerErrors" :key="item.name" role="status" class="ds-alert">{{ item.label }}事件处理暂缓：{{ item.message }}。后台将自动重试，生活事件采集独立运行。</p>
    <p v-if="attachmentMessage" role="status" class="ds-alert">{{ attachmentMessage }}</p>
    <div v-if="loading" class="ds-empty" role="status">正在读取配置…</div>
    <template v-else-if="state && settingsDraft">
      <form class="ds-settings" aria-label="基础设置" @submit.prevent="saveSettings">
        <div class="ds-section-heading">
          <h2><IconSettings :size="19" aria-hidden="true" />基础设置</h2>
          <div class="ds-save-actions">
            <button v-if="settingsDirty" type="button" class="ds-text-button" :disabled="!!busy" @click="resetSettings">还原修改</button>
            <button type="submit" class="ds-button ds-primary" :disabled="!!busy || !needsSettingsSave">{{ busy === 'settings' ? '保存中…' : needsSettingsSave ? '保存设置' : '已保存' }}</button>
          </div>
        </div>
        <p class="ds-help">所有目录共用；图片策略用于后续任务，降低并发或延长取链间隔也会约束待补附件。</p>
        <fieldset :disabled="!!busy" class="ds-settings-grid">
          <div><label class="ds-outlined"><span>STRM 播放地址</span><input v-model="settingsDraft.baseUrl" type="url" required placeholder="http://NAS_IP:8080" aria-describedby="ds-base-help" /></label><p id="ds-base-help" class="ds-help">填写播放器或 Emby 能访问的 Cinema115 地址。</p></div>
          <div><label class="ds-outlined"><span>视频后缀</span><input v-model="settingsDraft.videoExtensions" required placeholder="mp4,mkv,avi" aria-describedby="ds-video-help" /></label><p id="ds-video-help" class="ds-help">逗号分隔，为匹配的视频生成 STRM。</p></div>
          <div><label class="ds-outlined"><span>元数据后缀</span><input v-model="settingsDraft.metadataExtensions" placeholder="srt,ass,ssa" aria-describedby="ds-metadata-help" /></label><p id="ds-metadata-help" class="ds-help">按原名下载字幕、NFO 或图片，留空不下载；单个最多 32 MB。</p></div>
          <div><label class="ds-outlined"><span>最小文件大小（MB）</span><input v-model.number="settingsDraft.minSizeMB" type="number" required min="0" max="1073741824" step="1" aria-describedby="ds-size-help" /></label><p id="ds-size-help" class="ds-help">0 表示不限制，仅过滤视频，不影响元数据。</p></div>
          <div class="ds-download-controls">
            <label class="ds-outlined ds-image-mode"><span>图片下载策略</span><select v-model="settingsDraft.metadataImageMode" aria-describedby="ds-image-help"><option value="cdn">CDN 预览图优先</option><option value="original">原文件</option></select></label>
            <label class="ds-outlined ds-rate-input"><span>附件下载并发</span><input v-model.number="settingsDraft.metadataConcurrency" type="number" required min="1" max="8" step="1" aria-describedby="ds-rate-help" /></label>
            <label class="ds-outlined ds-rate-input"><span>取链批次间隔（秒）</span><input v-model.number="settingsDraft.metadataBatchIntervalSeconds" type="number" required min="0" max="60" step="1" aria-describedby="ds-rate-help" /></label>
            <div class="ds-download-help"><p id="ds-image-help" class="ds-help">CDN 预览图可能经过压缩；有可用图片地址时直接下载，缺少地址时合并获取原文件链接。</p><p id="ds-rate-help" class="ds-help">每批最多合并 32 个附件取链；0 秒不设批次间隔。未变化文件跳过，403 或 429 后自动暂停附件下载，冷却后逐步恢复。</p></div>
          </div>
        </fieldset>
        <fieldset :disabled="!!busy" class="ds-event-setting">
          <div><span class="ds-event-title">生活事件</span><p id="ds-event-help" class="ds-help">所有监控目录共享一次读取，按变更更新。</p></div>
          <label class="ds-outlined"><span>事件检查间隔</span><select v-model.number="settingsDraft.eventIntervalSeconds" aria-describedby="ds-event-help"><option :value="0">关闭</option><option :value="20">每 20 秒</option><option :value="60">每 1 分钟</option><option :value="300">每 5 分钟</option><option :value="900">每 15 分钟</option><option :value="1800">每 30 分钟</option></select></label>
        </fieldset>
        <details class="ds-preview"><summary><IconChevronRight class="ds-chevron" :size="14" aria-hidden="true" /><IconCode :size="16" aria-hidden="true" />STRM 内容预览</summary><code>{{ strmPreview }}</code><p class="ds-help">示例字段，生成时替换为实际文件信息；支持播放器直接播放，也支持通过 Cinema115 的 Emby 反代播放。</p></details>
      </form>
      <section class="ds-directories" aria-label="监控目录">
        <div class="ds-section-heading">
          <h2><IconFolder :size="19" aria-hidden="true" />监控目录<span class="ds-section-count">{{ state.mappings.length }}</span></h2>
          <span :title="startHint"><button ref="addButton" type="button" class="ds-button ds-check" :disabled="!!busy || !canStart" @click="edit()"><IconPlus :size="17" aria-hidden="true" />添加目录</button></span>
        </div>
        <p v-if="startHint" class="ds-help ds-start-hint">{{ startHint }}，再添加或调整目录、运行扫描。</p>
        <p v-else-if="!hasSchedule" class="ds-help ds-start-hint">自动周期均已关闭；首次生成、手动操作及已接受的任务仍会执行。</p>
        <div v-if="!state.mappings.length" class="ds-empty"><IconFolder :size="22" aria-hidden="true" /><p>尚未添加目录，选择主网盘来源与本地输出目录即可开始。</p></div>
        <div v-else class="ds-grid">
          <article v-for="m in state.mappings" :key="m.id" class="ds-card" :aria-label="m.name">
            <header class="ds-heading"><span class="ds-symbol"><IconCloud :size="21" aria-hidden="true" /></span><div class="ds-name"><h3 :title="m.name">{{ m.name }}</h3><span class="ds-status" aria-live="polite" :class="{ running: active(m) || downloadingMetadata(m), attention: !!m.lastError || !!m.metadataFailed }"><IconLoader2 v-if="active(m) || downloadingMetadata(m)" class="ds-spin" :size="12" aria-hidden="true" />{{ status(m) }}</span></div><button :id="'ds-edit-' + m.id" type="button" class="ds-icon" :disabled="!!busy" title="编辑目录与自动任务" :aria-label="'编辑' + m.name" @click="edit(m)"><IconEdit :size="17" aria-hidden="true" /></button><button type="button" class="ds-icon" :disabled="!!busy || active(m)" :aria-label="'删除' + m.name" @click="requestDelete(m)"><IconTrash :size="17" aria-hidden="true" /></button></header>
            <dl class="ds-paths"><div><dt><IconCloud :size="15" aria-hidden="true" />来源</dt><dd :title="m.sourcePath">{{ m.sourcePath || m.sourceCID }}</dd></div><div><dt><IconFolderCheck :size="15" aria-hidden="true" />输出</dt><dd :title="strmDirectoryPath(m.outputDir)">{{ strmDirectoryPath(m.outputDir) }}</dd></div></dl>
            <div class="ds-card-schedule">
              <div class="ds-schedule-summary"><span class="ds-schedule-title">自动任务</span></div>
              <p class="ds-schedule-description" :aria-label="m.name + '自动任务摘要'"><span class="ds-chip-event">{{ eventLabel() }}</span><span class="ds-chip-increment">增量 {{ scheduleLabel(m, 'reconcile') }}</span><span class="ds-chip-full">全量 {{ scheduleLabel(m, 'fullScan') }}</span></p>
            </div>
            <div class="ds-counts"><span>本次视频 <b>{{ m.stats.videos }}</b></span><span>写入 {{ m.stats.written }}</span><span>未变 {{ m.stats.unchanged }}</span><span v-if="m.stats.metadataWritten">附件 {{ m.stats.metadataWritten }}</span><span v-if="m.metadataPending">待补附件 {{ m.metadataPending }}</span><span v-if="m.metadataFailed" class="ds-retrying">待重试 {{ m.metadataFailed }}</span><span v-if="m.stats.failed" class="ds-failed">失败 {{ m.stats.failed }}</span></div>
            <p v-if="changedAccount(m)" class="ds-failed ds-message">主账号已变化，请重新选择来源目录。</p><p v-else-if="m.lastError" class="ds-failed ds-message">{{ m.lastError }}</p>
            <div class="ds-meta"><span>最近扫描 {{ date(m.lastScanAt) }}</span><div><button v-if="active(m) || pendingMetadata(m)" type="button" :disabled="!!busy" :aria-label="'停止' + m.name + (active(m) ? '本次扫描' : '附件补齐')" @click="stop(m)">停止本次</button><button type="button" :disabled="!!busy || !m.enabled && (changedAccount(m) || !canStart)" :title="!m.enabled ? startHint : undefined" :aria-label="(m.enabled ? '暂停' : '开启') + m.name + '监控'" @click="toggle(m, !m.enabled)">{{ m.enabled ? '暂停监控' : '开启监控' }}</button></div></div>
            <footer class="ds-actions"><button type="button" class="ds-button ds-check" :disabled="!!busy || active(m) || changedAccount(m) || !canStart || !m.enabled" :title="startHint || (!m.enabled ? '开启监控后检查事件更新；也可直接完整扫描' : undefined)" @click="run(m, false)"><IconRefresh :size="16" aria-hidden="true" />{{ active(m) ? '检查中…' : '检查更新' }}</button><button type="button" class="ds-button" :disabled="!!busy || active(m) || changedAccount(m) || !canStart" :title="startHint" @click="run(m, true)"><IconSearch :size="16" aria-hidden="true" />完整扫描补齐</button></footer>
          </article>
        </div>
        <details class="ds-notes"><summary>同步说明</summary><p>生活事件按全局间隔共享读取，增量核对和全量扫描在每个目录卡片中独立设置；目录暂停后不参与自动任务。修改设置不额外安排扫描，已经接受的任务会继续；STRM 先生成，附件在后台补齐，失败附件单独重试。停止本次会清除未完成的附件待办，已生成文件保留；已有 STRM 可手动完整扫描更新。同一文件改名或移动后，新输出生成成功才清理旧输出及空目录，不保留手动修改；云端删除不自动删除本地文件。目录树导出 TXT 由系统清理，网盘源文件保留。</p></details>
      </section>
    </template>
    <P115ActionDialog v-if="editor" :title="editor.id ? '编辑目录' : '新增目录'" size="wide" layout="contained" :busy="!!busy" @close="closeEditor">
      <form id="drive-strm-form" class="ds-editor-form" :aria-label="(editor.name || '新目录') + '设置'" novalidate @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="ds-editor-error">{{ dialogError }}</p>
        <p v-if="changedAccount(editor)" role="alert" class="ds-editor-error">主账号已变化，请重新选择来源目录。</p>
        <p v-else-if="directoryReadOnly" class="ds-editor-alert">{{ editorRunning ? '此目录正在执行任务，目录信息只读；自动任务可修改并于下个周期生效。' : startHint + '。当前可单独调整自动任务。' }}</p>

        <!-- 目录基本信息 -->
        <section aria-labelledby="ds-directory-heading" class="ds-editor-section">
          <div class="ds-editor-section-header">
            <h3 id="ds-directory-heading" class="ds-editor-section-heading">
              <IconFolder :size="16" aria-hidden="true" />目录信息
            </h3>
            <span v-if="editor.id && !directoryReadOnly" class="ds-editor-subtle-hint">变更路径将重新扫描</span>
          </div>

          <!-- 桌面端目录名称与自动监控同行 -->
          <div class="ds-editor-row ds-name-monitor-row">
            <div class="ds-field ds-name-field">
              <label for="ds-dir-name" class="ds-field-label">名称</label>
              <input
                id="ds-dir-name"
                v-model="editor.name"
                required
                maxlength="60"
                placeholder="例如：动漫媒体库"
                :disabled="!!busy || directoryReadOnly"
                class="ds-input"
              />
            </div>
            <div class="ds-field ds-monitor-field">
              <span class="ds-field-label">监控设置</span>
              <div class="ds-monitor-box">
                <SettingSwitch
                  v-model="editor.enabled"
                  label="自动监控"
                  :disabled="!!busy || directoryReadOnly"
                />
              </div>
            </div>
          </div>

          <!-- 桌面端来源与本地输出双列紧凑排布 -->
          <div class="ds-editor-row ds-paths-row">
            <div class="ds-field ds-source-field">
              <span id="ds-source-label" class="ds-field-label">主网盘来源</span>
              <button
                type="button"
                class="ds-directory-picker"
                :disabled="!!busy || directoryReadOnly"
                aria-labelledby="ds-source-label ds-source-value"
                @click="selectSource"
              >
                <IconCloud :size="16" class="ds-field-icon" aria-hidden="true" />
                <span id="ds-source-value" class="ds-directory-text" :title="editor.sourcePath || '选择云端目录'">
                  {{ editor.sourcePath || '选择云端目录' }}
                </span>
                <span class="ds-picker-action">选择</span>
              </button>
              <p class="ds-field-hint" title="主账号网盘目录">主账号网盘目录</p>
            </div>
            <div class="ds-field ds-output-field">
              <label for="ds-output" class="ds-field-label">本地输出</label>
              <div class="ds-output-group">
                <input
                  id="ds-output"
                  v-model="editor.outputDir"
                  required
                  placeholder="例如：动漫"
                  aria-describedby="ds-output-help"
                  :disabled="!!busy || directoryReadOnly"
                  class="ds-input"
                />
                <button
                  type="button"
                  class="ds-button ds-picker-btn"
                  :disabled="!!busy || directoryReadOnly"
                  @click="outputDialog = true"
                >
                  选择
                </button>
              </div>
              <p id="ds-output-help" class="ds-field-hint" :title="strmDirectoryPath(editor.outputDir)">
                {{ strmDirectoryPath(editor.outputDir) }}
              </p>
            </div>
          </div>
        </section>

        <!-- 自动任务设置（双列增量与全量） -->
        <section aria-labelledby="ds-schedule-heading" class="ds-editor-section ds-schedule-section">
          <div class="ds-editor-section-header">
            <h3 id="ds-schedule-heading" class="ds-editor-section-heading">
              <IconClock :size="16" aria-hidden="true" />自动任务
            </h3>
            <span class="ds-editor-subtle-hint">独立周期执行</span>
          </div>

          <div class="ds-schedule-grid">
            <div
              v-for="schedule in scanSchedules"
              :key="schedule.enabled"
              class="ds-schedule-card"
            >
              <div class="ds-schedule-card-header">
                <SettingSwitch
                  v-model="editor.schedule[schedule.enabled]"
                  :label="'自动' + schedule.name"
                  :disabled="!!busy"
                  class="ds-schedule-toggle"
                />
                <div class="ds-schedule-interval">
                  <label :for="'ds-interval-' + schedule.enabled" class="ds-sr">{{ schedule.name }}周期</label>
                  <select
                    :id="'ds-interval-' + schedule.enabled"
                    v-model.number="editor.schedule[schedule.interval]"
                    :disabled="!!busy || !editor.schedule[schedule.enabled]"
                    class="ds-compact-select"
                  >
                    <option v-for="hours in schedule.intervals" :key="hours" :value="hours">{{ intervalLabel(hours) }}</option>
                  </select>
                </div>
              </div>
              <p class="ds-schedule-card-hint">{{ schedule.help }}</p>
            </div>
          </div>

          <p class="ds-editor-bottom-note">生活事件按全局设置共享；{{ !editor.enabled ? '此目录已暂停监控，恢复后生效。' : '保存自动任务不触发重新扫描，当前任务继续完成。' }}</p>
          <p v-if="settingsDirty" class="ds-editor-bottom-note ds-dirty-warn">基础设置有未保存的修改，不会影响此目录。</p>
        </section>
      </form>
      <template #footer>
        <span class="ds-editor-footer-note">{{ editorDirty ? '有未保存的修改' : '各目录独立配置' }}</span>
        <button type="button" class="ds-button" :disabled="!!busy" @click="closeEditor">取消</button>
        <button type="submit" form="drive-strm-form" class="ds-button ds-primary" :disabled="!!busy || !canSaveEditor">{{ busy === 'save' ? '保存中…' : editorSaveLabel }}</button>
      </template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="sourceDialog && editor" :token="token" :account-id="editor.accountId" :allow-root="false" title="选择主网盘来源目录" @close="sourceDialog = false" @select="pickedSource" />
    <STRMDirectoryDialog v-if="outputDialog" :token="token" title="选择本地输出目录" @close="outputDialog = false" @select="path => { if (editor) editor.outputDir = path; outputDialog = false }" />
    <P115ActionDialog v-if="deleting" title="删除目录配置" :busy="!!busy" @close="deleting = undefined"><p>删除「{{ deleting.name }}」的配置与索引？已生成的本地文件和网盘文件都会保留。</p><p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p><template #footer><button type="button" class="ds-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button type="button" class="ds-button ds-danger" :disabled="!!busy" @click="remove">{{ busy ? '删除中…' : '删除配置' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ds-page { background-color:#f5f5f5; }:global(.dark .ds-page) { background-color:#0a0a0a; }
.ds-settings { @apply rounded-2xl bg-white dark:bg-neutral-900; padding:20px; }
.ds-section-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.ds-section-heading h2 { @apply text-neutral-800 dark:text-neutral-100; display:flex; align-items:center; gap:8px; min-width:0; font-size:15px; font-weight:600; }
.ds-section-heading h2>svg { @apply text-blue-500; flex-shrink:0; }
.ds-section-count { @apply bg-neutral-100 text-neutral-500 dark:bg-neutral-800 dark:text-neutral-400; padding:1px 7px; border-radius:6px; font-size:12px; font-variant-numeric:tabular-nums; }
.ds-save-actions { display:flex; align-items:center; gap:12px; flex-shrink:0; }
.ds-help { @apply text-neutral-500 dark:text-neutral-400; font-size:12px; line-height:1.7; margin-top:7px; }
.ds-settings-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:18px 20px; margin-top:22px; min-width:0; }
.ds-download-controls { @apply bg-neutral-50 dark:bg-neutral-800/50; grid-column:1/-1; display:flex; flex-wrap:wrap; align-items:center; gap:14px 18px; border-radius:10px; padding:13px 14px; }.ds-rate-input { width:160px; flex-shrink:0; }.ds-image-mode { width:200px; flex-shrink:0; }.ds-download-help { flex-basis:100%; min-width:0; }.ds-download-controls .ds-help { margin:0; }.ds-download-help .ds-help+.ds-help { margin-top:6px; }
.ds-event-setting { display:flex; align-items:center; gap:20px; margin-top:22px; min-width:0; }.ds-event-setting>div { flex:1; min-width:0; }.ds-event-title { @apply text-neutral-700 dark:text-neutral-200; font-size:13px; font-weight:500; }.ds-event-setting .ds-help { margin-top:3px; }.ds-event-setting>.ds-outlined { width:220px; flex-shrink:0; }
.ds-outlined { @apply border border-neutral-300 dark:border-neutral-600; position:relative; display:block; min-width:0; border-radius:8px; transition:border-color .15s; }
.ds-outlined:focus-within { @apply border-blue-500; box-shadow:0 0 0 1px #3b82f6; }
.ds-outlined>span { @apply bg-white text-neutral-600 dark:bg-neutral-900 dark:text-neutral-300; position:absolute; top:-9px; left:9px; padding:0 5px; font-size:12px; line-height:17px; }
.ds-outlined>input,.ds-outlined>select { @apply text-neutral-800 dark:text-neutral-100; display:block; width:100%; min-width:0; height:40px; padding:0 13px; border:0; border-radius:inherit; background:transparent; font-size:13px; outline:none; }
.ds-outlined>select { @apply dark:[color-scheme:dark]; padding-right:32px; cursor:pointer; }.ds-outlined option { @apply bg-white text-neutral-800 dark:bg-neutral-900 dark:text-neutral-100; }
.ds-outlined>input:focus-visible,.ds-outlined>select:focus-visible { outline:none; }
.ds-outlined:has(>input:disabled),.ds-outlined:has(>select:disabled) { @apply border-neutral-200 bg-neutral-50 dark:border-neutral-700 dark:bg-neutral-800/50; }.ds-outlined>input:disabled,.ds-outlined>select:disabled { @apply text-neutral-400 dark:text-neutral-500; cursor:not-allowed; }
.ds-preview { margin-top:14px; }.ds-preview summary { @apply text-neutral-600 dark:text-neutral-300; display:flex; align-items:center; gap:6px; width:fit-content; min-height:32px; cursor:pointer; list-style:none; font-size:12px; }.ds-preview summary::-webkit-details-marker { display:none; }.ds-chevron { transition:transform .15s; }.ds-preview[open] .ds-chevron { transform:rotate(90deg); }.ds-preview code { @apply rounded-lg bg-neutral-50 text-neutral-700 dark:bg-neutral-950 dark:text-neutral-300; display:block; padding:10px 12px; font-size:12px; line-height:1.8; overflow-wrap:anywhere; }
.ds-directories { margin-top:24px; }.ds-directories>.ds-section-heading { margin-bottom:14px; }.ds-start-hint { margin:-4px 0 12px; }
.ds-grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(min(100%,340px),1fr)); align-items:start; gap:14px; }
.ds-card { @apply rounded-2xl bg-white p-4 text-neutral-700 dark:bg-neutral-900 dark:text-neutral-200; min-width:0; box-shadow:0 4px 16px -10px rgb(0 0 0 / .15),0 1px 3px rgb(0 0 0 / .035); transition:box-shadow .2s; }.ds-card:hover { box-shadow:0 9px 26px -14px rgb(0 0 0 / .2),0 2px 6px rgb(0 0 0 / .04); }
.ds-heading { display:flex; align-items:center; gap:8px; margin-bottom:16px; }.ds-symbol { @apply bg-neutral-100 text-neutral-600 dark:bg-neutral-800 dark:text-neutral-300; display:grid; place-items:center; width:36px; height:36px; border-radius:11px; flex-shrink:0; }.ds-name { flex:1; min-width:0; }.ds-name h3 { font-size:15px; font-weight:600; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.ds-status { @apply text-neutral-500 dark:text-neutral-400; display:inline-flex; gap:5px; align-items:center; font-size:12px; margin-top:3px; }.ds-status.running { @apply text-blue-600 dark:text-blue-400; }.ds-status.attention { @apply text-amber-700 dark:text-amber-400; }
.ds-icon { @apply text-neutral-400 hover:bg-neutral-100 hover:text-blue-600 dark:hover:bg-neutral-800; display:grid; place-items:center; width:32px; height:36px; border-radius:8px; flex-shrink:0; }
.ds-paths { display:grid; gap:8px; font-size:13px; }.ds-paths>div { display:flex; align-items:baseline; gap:12px; }.ds-paths dt { @apply text-neutral-500 dark:text-neutral-400; display:flex; align-items:center; gap:6px; flex-shrink:0; }.ds-paths dd { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ds-card-schedule { margin-top:15px; }.ds-schedule-summary { display:flex; align-items:center; gap:8px; }.ds-schedule-title { @apply text-neutral-500 dark:text-neutral-400; font-size:11px; }.ds-schedule-description { display:flex; flex-wrap:wrap; gap:6px; margin-top:6px; font-size:11px; line-height:1.7; }.ds-schedule-description>span { @apply bg-neutral-50 text-neutral-500 dark:bg-neutral-800/70 dark:text-neutral-400; border-radius:6px; padding:2px 7px; }

/* 目录编辑弹窗 - 紧凑双列与 HeroUI 风格中性控件 */
.ds-editor-form { margin:0; display:flex; flex-direction:column; gap:12px; }
.ds-editor-error { @apply rounded-lg bg-red-50 text-red-600 dark:bg-red-950/40 dark:text-red-400; padding:8px 12px; font-size:12px; line-height:1.5; overflow-wrap:anywhere; margin:0; }
.ds-editor-alert { @apply rounded-lg bg-amber-50 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300; padding:8px 12px; font-size:12px; line-height:1.5; overflow-wrap:anywhere; margin:0; }
.ds-editor-section { display:flex; flex-direction:column; gap:8px; }
.ds-editor-section-header { display:flex; align-items:center; justify-content:space-between; gap:8px; }
.ds-editor-section-heading { @apply text-neutral-800 dark:text-neutral-100; display:flex; align-items:center; gap:6px; font-size:13px; font-weight:600; }
.ds-editor-section-heading>svg { @apply text-neutral-400 dark:text-neutral-500; }
.ds-editor-subtle-hint { @apply text-neutral-400 dark:text-neutral-500; font-size:11px; }

/* 字段与栅格排版 */
.ds-editor-row { display:flex; gap:12px; }
.ds-field { display:flex; flex-direction:column; gap:4px; min-width:0; }
.ds-field-label { @apply text-neutral-600 dark:text-neutral-300; font-size:12px; font-weight:500; }
.ds-field-hint { @apply text-neutral-400 dark:text-neutral-500; font-size:11px; line-height:1.4; margin:2px 0 0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }

/* 桌面端名称与自动监控同行 */
.ds-name-monitor-row { align-items:flex-end; }
.ds-name-field { flex:1; }
.ds-monitor-field { width:190px; flex-shrink:0; }
.ds-monitor-box { @apply border border-neutral-200 bg-neutral-50/80 dark:border-neutral-800 dark:bg-neutral-800/40; display:flex; align-items:center; height:36px; padding:0 10px; border-radius:8px; }
.ds-monitor-box :deep(label) { width:100%; display:flex; flex-direction:row-reverse; align-items:center; justify-content:space-between; gap:8px; cursor:pointer; }
.ds-monitor-box :deep(span.text-sm) { font-size:12px; font-weight:500; }
.ds-monitor-box:has(input:disabled) { @apply border-neutral-200 bg-neutral-50 opacity-60 dark:border-neutral-800 dark:bg-neutral-900/50; cursor:not-allowed; }
.ds-monitor-box:has(input:disabled) :deep(label) { cursor:not-allowed; }

/* 输入框通用 */
.ds-input { @apply border border-neutral-200 bg-white text-neutral-800 dark:border-neutral-700 dark:bg-neutral-900 dark:text-neutral-100; height:36px; padding:0 10px; border-radius:8px; font-size:13px; width:100%; min-width:0; box-sizing:border-box; transition:border-color .15s,box-shadow .15s; }
.ds-input:focus { @apply border-blue-500; outline:none; box-shadow:0 0 0 1px #3b82f6; }
.ds-input:disabled { @apply border-neutral-200 bg-neutral-50 text-neutral-400 dark:border-neutral-800 dark:bg-neutral-900/50 dark:text-neutral-500; cursor:not-allowed; }

/* 桌面端来源与输出双列紧凑 */
.ds-paths-row { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
.ds-directory-picker { @apply border border-neutral-200 bg-neutral-50/80 hover:bg-neutral-100 text-neutral-700 dark:border-neutral-700 dark:bg-neutral-800/40 dark:hover:bg-neutral-800 dark:text-neutral-200; display:flex; align-items:center; gap:6px; width:100%; height:36px; padding:0 10px; border-radius:8px; font-size:13px; text-align:left; box-sizing:border-box; min-width:0; cursor:pointer; transition:background-color .15s,border-color .15s; }
.ds-directory-picker:disabled { @apply cursor-not-allowed opacity-50 bg-neutral-50 dark:bg-neutral-900; }
.ds-directory-text { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:12px; }
.ds-field-icon { @apply text-neutral-400 dark:text-neutral-500; flex-shrink:0; }
.ds-picker-action { @apply text-blue-600 dark:text-blue-400; font-size:12px; font-weight:500; flex-shrink:0; }
.ds-output-group { display:flex; gap:6px; width:100%; min-width:0; }
.ds-output-group .ds-input { flex:1; }
.ds-picker-btn { flex-shrink:0; height:36px; min-height:36px; padding:0 12px; font-size:12px; }

/* 自动任务双列卡片 */
.ds-schedule-section { @apply border-t border-neutral-100 dark:border-neutral-800/80; margin-top:6px; padding-top:10px; gap:8px; }
.ds-schedule-grid { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
.ds-schedule-card { @apply border border-neutral-200/90 bg-neutral-50/60 dark:border-neutral-800 dark:bg-neutral-800/30; border-radius:10px; padding:10px 12px; display:flex; flex-direction:column; justify-content:space-between; min-width:0; gap:5px; }
.ds-schedule-card-header { display:flex; align-items:center; justify-content:space-between; gap:8px; min-width:0; }
.ds-schedule-toggle { flex:1; min-width:0; }
.ds-schedule-toggle :deep(label) { display:flex; align-items:center; gap:6px; cursor:pointer; }
.ds-schedule-toggle :deep(span.text-sm) { font-size:12px; font-weight:500; }
.ds-schedule-interval { flex-shrink:0; }
.ds-compact-select { @apply border border-neutral-300 bg-white text-neutral-800 dark:border-neutral-700 dark:bg-neutral-800 dark:text-neutral-100 dark:[color-scheme:dark]; height:28px; padding:0 16px 0 8px; border-radius:6px; font-size:12px; cursor:pointer; outline:none; transition:border-color .15s; }
.ds-compact-select:focus { @apply border-blue-500; box-shadow:0 0 0 1px #3b82f6; }
.ds-compact-select:disabled { @apply border-neutral-200 bg-neutral-100 text-neutral-400 dark:border-neutral-800 dark:bg-neutral-900 dark:text-neutral-600; cursor:not-allowed; }
.ds-schedule-card-hint { @apply text-neutral-500 dark:text-neutral-400; font-size:11px; line-height:1.4; margin:0; }

/* 底部状态提示文案 */
.ds-editor-bottom-note { @apply text-neutral-400 dark:text-neutral-500; font-size:11px; line-height:1.4; margin:0; }
.ds-dirty-warn { @apply text-amber-600 dark:text-amber-400; }
.ds-editor-footer-note { @apply text-neutral-400 dark:text-neutral-500; align-self:center; margin-right:auto; font-size:12px; }

/* 开关蓝色高亮与无障碍焦点圈 */
.ds-editor-form :deep(input[role='switch']:checked + span) { background-color:#2563eb; }
.ds-editor-form :deep(input[role='switch']:focus-visible + span) { --tw-ring-color:#60a5fa; }

.ds-counts { @apply bg-neutral-50 dark:bg-neutral-800/60; display:flex; flex-wrap:wrap; gap:5px 13px; border-radius:9px; padding:9px 10px; font-size:12px; margin-top:14px; }.ds-counts b { font-size:14px; font-variant-numeric:tabular-nums; }
.ds-retrying { @apply text-amber-700 dark:text-amber-400; }
.ds-meta { @apply text-neutral-500 dark:text-neutral-400; display:flex; flex-wrap:wrap; justify-content:space-between; align-items:center; gap:5px; font-size:12px; margin:10px 0; }.ds-meta button { @apply text-blue-600 dark:text-blue-400; padding:5px 0 5px 8px; }.ds-message { font-size:12px; line-height:1.6; margin-top:8px; overflow-wrap:anywhere; }.ds-failed { @apply text-red-600 dark:text-red-400; }
.ds-actions { display:grid; grid-template-columns:1fr 1fr; gap:8px; }.ds-button { @apply bg-neutral-100 text-neutral-700 hover:bg-neutral-200 dark:bg-neutral-800 dark:text-neutral-200 dark:hover:bg-neutral-700; display:inline-flex; align-items:center; justify-content:center; gap:6px; border-radius:9px; min-height:36px; padding:7px 11px; font-size:13px; font-weight:500; }.ds-primary { @apply bg-blue-600 text-white hover:bg-blue-700 dark:bg-blue-600 dark:text-white dark:hover:bg-blue-500; }.ds-check { @apply bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60; }.ds-danger { @apply bg-red-600 text-white hover:bg-red-700 dark:bg-red-600 dark:text-white; }.ds-text-button { @apply text-blue-600 dark:text-blue-400; font-size:12px; padding:8px 0; }button:disabled { cursor:not-allowed; opacity:.45; }button:focus-visible,input:focus-visible,summary:focus-visible { outline:2px solid #3b82f6; outline-offset:3px; }
.ds-alert { @apply rounded-xl bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300; padding:12px; margin-bottom:12px; font-size:13px; overflow-wrap:anywhere; }.ds-notice { @apply text-blue-700 dark:text-blue-300; margin-bottom:12px; font-size:13px; }.ds-empty { @apply rounded-xl bg-white text-neutral-500 dark:bg-neutral-900 dark:text-neutral-400; display:flex; align-items:center; gap:10px; padding:18px; font-size:13px; line-height:1.7; }.ds-empty>svg { flex-shrink:0; }
.ds-notes { @apply text-neutral-500 dark:text-neutral-400; font-size:12px; line-height:1.8; margin-top:12px; }.ds-notes summary { width:fit-content; min-height:32px; cursor:pointer; }.ds-notes p { margin-top:4px; max-width:900px; }
.ds-sr { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }.ds-spin { animation:ds-spin 1s linear infinite; }@keyframes ds-spin { to { transform:rotate(360deg) } }
@media(max-width:639px) { .ds-settings { padding:16px; }.ds-settings-grid { grid-template-columns:minmax(0,1fr); gap:20px; }.ds-download-controls { justify-content:space-between; gap:14px 10px; padding:14px 12px; }.ds-download-controls .ds-help { flex-basis:100%; }.ds-rate-input { width:118px; }.ds-button { min-height:44px; padding-inline:8px; white-space:nowrap; }.ds-save-actions { gap:10px; }.ds-icon { width:40px; height:40px; }.ds-meta button { min-height:40px; padding-inline:8px; }.ds-text-button,.ds-preview summary,.ds-notes summary { min-height:44px; }.ds-outlined>input,.ds-outlined>select { height:44px; font-size:16px; } }
@media(max-width:639px) {
  .ds-rate-input,.ds-image-mode { width:100%; }.ds-event-setting { align-items:flex-start; gap:16px; flex-direction:column; }.ds-event-setting>.ds-outlined { width:100%; }
  .ds-editor-row.ds-name-monitor-row { flex-direction:column; align-items:stretch; gap:8px; }
  .ds-monitor-field { width:100%; }
  .ds-monitor-box { height:44px; min-height:44px; padding:0 12px; }
  .ds-editor-row.ds-paths-row { grid-template-columns:1fr; gap:8px; }
  .ds-directory-picker { height:44px; min-height:44px; }
  .ds-input { height:44px; min-height:44px; font-size:16px; }
  .ds-picker-btn { height:44px; min-height:44px; padding:0 14px; font-size:13px; }
  .ds-schedule-grid { grid-template-columns:1fr; gap:8px; }
  .ds-schedule-card { padding:10px 12px; }
  .ds-schedule-card-header { flex-wrap:wrap; gap:8px; }
  .ds-schedule-toggle :deep(label) { min-height:44px; display:flex; align-items:center; }
  .ds-compact-select { height:44px; min-height:44px; font-size:14px; }
  .ds-editor-footer-note { display:none; }
}
@media(max-width:359px) {
  .ds-actions { grid-template-columns:1fr; }.ds-settings>.ds-section-heading { flex-wrap:wrap; }.ds-save-actions { margin-left:auto; }
  .ds-schedule-card-header { flex-direction:column; align-items:stretch; gap:6px; }
  .ds-schedule-interval { width:100%; }
  .ds-compact-select { width:100%; }
}
@media(prefers-reduced-motion:reduce) { .ds-spin { animation:none; }.ds-chevron,.ds-outlined,.ds-card { transition:none; } }
</style>
