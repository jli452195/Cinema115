<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconServer, IconFolder, IconFolderCheck, IconFolderExclamation, IconClock, IconRadar, IconAdjustmentsHorizontal, IconPlayerPause, IconPlayerPlay, IconPlayerStop, IconEdit, IconTrash, IconX } from '@tabler/icons-vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import SelectMenu from '../components/SelectMenu.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import MediaLocalDirectoryDialog from '../components/MediaLocalDirectoryDialog.vue'
import CD2DirectoryDialog from '../components/CD2DirectoryDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { mediaOrganizeRequest, type MediaMonitorStatus, type MediaRule, type MediaState } from '../lib/media-organize-api'
import { p115Request } from '../lib/p115-api'
import { cd2Request, type CD2Config, type CD2DirectorySelection } from '../lib/cd2-api'

const props = defineProps<{ token: string }>()
const rules = ref<MediaRule[]>([]), active = ref(false), activeRules = ref<string[]>([]), editor = ref<MediaRule>()
const stoppingRules = ref<string[]>([])
const monitoring = ref<Record<string, MediaMonitorStatus>>({})
const pending = ref<Record<string, string>>({})
const busy = computed(() => pending.value.editor || (editor.value?.id ? pending.value[editor.value.id] : '') || (deleting.value ? pending.value[deleting.value.id] : '') || '')
const requesting = computed(() => Object.keys(pending.value).length > 0)
const error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true), refreshing = ref(false)
const directory = ref<'source' | 'target' | 'failure'>(), deleting = ref<{ id: string; name: string }>()
const directoryTitle = computed(() => directory.value === 'source' ? '选择来源目录' : directory.value === 'target' ? '选择目标目录' : '选择整理失败目录')
const leaveDialog = ref(false), editorSnapshot = ref('')
const unsaved = computed(() => !!editor.value && ruleSignature(editor.value) !== editorSnapshot.value)
const duplicateCleanup = computed({
  get: () => editor.value?.deleteDuplicates === true,
  set: value => { if (editor.value) editor.value.deleteDuplicates = value },
})
const scanInterval = computed({
  get: () => (editor.value?.intervalSeconds ?? 1800) / (editor.value?.storage === 'cd2' ? 1 : 60),
  set: value => { if (editor.value) editor.value.intervalSeconds = Number(value) * (editor.value.storage === 'cd2' ? 1 : 60) },
})
let leaveDecision: ((value: boolean) => void) | undefined
const lifecycle = new AbortController()
let timer: number | undefined, stateGeneration = 0
const storageOptions = [{ value: 'local', label: 'NAS / 本地挂载' }, { value: '115', label: '115 主账号' }, { value: 'cd2', label: 'CloudDrive2' }]
const storageLabels: Record<string, string> = { local: '本地挂载', '115': '115 主账号', cd2: 'CloudDrive2' }
const directoryDescription = computed(() => editor.value?.storage === 'local' ? '选择或填写服务器／容器内已挂载且可写的目录。来源与目标不能重叠。新文件稳定 30 秒后处理。' : editor.value?.storage === 'cd2' ? '使用 CloudDrive2 授权范围内的可写目录。来源与目标不能重叠，更换连接后需重新选择目录。' : '使用 115 主账号，来源与目标不能重叠。更换主账号后需重新选择目录。')
function ruleSignature(rule: MediaRule) { return JSON.stringify([rule.name, rule.storage, rule.source, rule.target, rule.failure || '', rule.sourceId || '', rule.targetId || '', rule.failureId || '', rule.accountId || '', rule.connectionId || '', rule.connectionRevision || 0, rule.classify, rule.realtimeEnabled, rule.scheduleEnabled, rule.intervalSeconds, rule.attachmentCleanupEnabled, rule.attachmentGraceSeconds, rule.storage !== 'local' && rule.deleteDuplicates === true]) }
function realtimeLabel(rule: MediaRule) {
  if (!rule.realtimeEnabled) return '事件监控已暂停'
  const monitor = monitoring.value[rule.id], mode = monitor?.mode
  if (mode === 'realtime' || mode === 'event') return '事件监控已连接'
  if (mode === 'unavailable') return '事件监控不可用'
  if (mode === 'polling') return '事件监控等待恢复'
  return '正在建立监听'
}
function intervalLabel(seconds: number) { return seconds >= 60 && seconds % 60 === 0 ? `${seconds / 60} 分钟` : `${seconds} 秒` }
function attachmentCleanupLabel(rule: MediaRule) { return rule.attachmentCleanupEnabled === false ? '保留未匹配附件' : rule.attachmentGraceSeconds === 0 ? '未匹配附件复核后立即清理' : `未匹配附件宽限 ${rule.attachmentGraceSeconds ?? 10} 秒` }
function scanLabel(rule: MediaRule) { return rule.storage === 'cd2' ? '目录轮询' : '定时完整扫描' }
function scheduledLabel(rule: MediaRule) { return rule.scheduleEnabled ? `${rule.storage === 'cd2' ? '目录轮询' : '完整扫描'} · 每 ${intervalLabel(rule.intervalSeconds)}` : `${scanLabel(rule)}已暂停` }
function nextScanLabel(rule: MediaRule) {
  if (!rule.scheduleEnabled) return ''
  const action = rule.storage === 'cd2' ? '轮询' : '完整扫描'
  const next = rule.nextScanAt ? new Date(rule.nextScanAt) : undefined
  if (!next || !Number.isFinite(next.getTime()) || next.getFullYear() <= 1) return activeRules.value.includes(rule.id) ? '' : `即将${action}`
  if (next.getTime() <= Date.now()) return `${action}已到期`
  return `下次${action} ${next.toLocaleTimeString([], { hour12: false })}`
}
function ruleBusy(id: string) { return !!pending.value[id] }
function ruleStopping(id: string) { return stoppingRules.value.includes(id) }
function ruleActive(id: string) { return activeRules.value.includes(id) || ruleStopping(id) }
async function request<T>(path = '', init: RequestInit = {}) { return mediaOrganizeRequest<T>(props.token, path, { ...init, signal: lifecycle.signal }) }
async function refresh() {
  if (refreshing.value || document.hidden || lifecycle.signal.aborted) return
  refreshing.value = true
  const version = stateGeneration
  try {
    const state = await request<MediaState>()
    if (version !== stateGeneration) return
    rules.value = state.rules; active.value = state.active; activeRules.value = state.activeRules; stoppingRules.value = state.stoppingRules || []; monitoring.value = state.monitoring || {}; error.value = ''
  } catch (reason) { if (version === stateGeneration && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取失败' }
  finally { loading.value = false; refreshing.value = false; if (version !== stateGeneration) await refresh() }
}
async function act(name: string, fn: () => Promise<void>, dialog = false, scope = 'editor') {
  if (pending.value[scope]) return
  pending.value[scope] = name; (dialog ? dialogError : error).value = ''; notice.value = ''
  try { await fn() } catch (reason) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = reason instanceof Error ? reason.message : '操作失败' }
  finally { delete pending.value[scope]; poll() }
}
function edit(rule?: MediaRule) {
  editor.value = rule ? { ...rule, intervalSeconds: rule.storage === 'cd2' ? rule.intervalSeconds : Math.max(60, Math.ceil(rule.intervalSeconds / 60) * 60), attachmentCleanupEnabled: rule.attachmentCleanupEnabled ?? true, attachmentGraceSeconds: rule.attachmentGraceSeconds ?? 10 } : { id: '', name: '', storage: 'local', source: '', target: '', classify: true, realtimeEnabled: false, scheduleEnabled: false, intervalSeconds: 1800, attachmentCleanupEnabled: true, attachmentGraceSeconds: 10 }
  if (editor.value.storage !== 'local') editor.value.deleteDuplicates ??= false
  editorSnapshot.value = ruleSignature(editor.value); dialogError.value = ''
}
function storageChanged(resetInterval = false) {
  if (!editor.value) return
  Object.assign(editor.value, { source: '', target: '', failure: '', sourceId: '', targetId: '', failureId: '', accountId: '', connectionId: '', connectionRevision: 0 })
  if (resetInterval) {
    editor.value.realtimeEnabled = false
    editor.value.intervalSeconds = editor.value.storage === 'cd2' ? 20 : 1800
  }
  if (editor.value.storage === 'local') delete editor.value.deleteDuplicates
  else editor.value.deleteDuplicates ??= false
}
function clearFailure() { if (editor.value) { editor.value.failure = ''; editor.value.failureId = '' } }
async function chooseDirectory(which: 'source' | 'target' | 'failure') {
  if (!editor.value || busy.value) return
  const currentEditor = editor.value
  if (editor.value.storage === 'local') { dialogError.value = ''; directory.value = which; return }
  if (editor.value.storage === 'cd2') {
    await act('directory', async () => {
      const config = await cd2Request<CD2Config>(props.token, '', { signal: lifecycle.signal })
      if (!config.enabled || !config.url || !config.tokenConfigured) throw new Error('请先在资源配置中启用并配置 CloudDrive2')
      if (editor.value !== currentEditor || currentEditor.storage !== 'cd2') return
      if (editor.value.connectionId && (editor.value.connectionId !== config.id || editor.value.connectionRevision !== config.revision)) storageChanged()
      editor.value.connectionId = config.id; editor.value.connectionRevision = config.revision
      directory.value = which
    }, true)
    return
  }
  await act('directory', async () => {
    const data = await p115Request<{ accounts: { id: string; primary: boolean }[] }>(props.token, 'accounts', { signal: lifecycle.signal })
    if (editor.value !== currentEditor || currentEditor.storage !== '115') return
    const primary = data.accounts.find(a => a.primary)
    if (!primary) throw new Error('请先在 115 助手配置主账号')
    if (editor.value) { if (editor.value.accountId && editor.value.accountId !== primary.id) storageChanged(); editor.value.accountId = primary.id }
    directory.value = which
  }, true)
}
function picked(value: { id: string; path: string }) { if (editor.value && directory.value) { editor.value[directory.value] = value.path; editor.value[directory.value === 'source' ? 'sourceId' : directory.value === 'target' ? 'targetId' : 'failureId'] = value.id }; directory.value = undefined }
function pickedCD2(value: CD2DirectorySelection) {
  if (editor.value && directory.value) {
    editor.value[directory.value] = value.path
    editor.value.connectionId = value.connectionId; editor.value.connectionRevision = value.revision
    editor.value.accountId = ''; editor.value.sourceId = ''; editor.value.targetId = ''; editor.value.failureId = ''
  }
  directory.value = undefined
}
async function save() {
  const current = editor.value
  if (!current || busy.value) return
  const maxInterval = current.storage === 'cd2' ? 86400 : 1440
  if (!Number.isInteger(scanInterval.value) || scanInterval.value < 1 || scanInterval.value > maxInterval) { dialogError.value = current.storage === 'cd2' ? '轮询间隔须为 1 至 86400 的整数秒数' : '完整扫描间隔须为 1 至 1440 的整数分钟数'; return }
  if (!Number.isInteger(current.attachmentGraceSeconds) || current.attachmentGraceSeconds < 0 || current.attachmentGraceSeconds > 86400) { dialogError.value = '附件宽限须为 0 至 86400 的整数秒数'; return }
  await act('save', async () => {
    const saved = await request<MediaRule>('/rules', { method: 'POST', body: JSON.stringify(current) })
    stateGeneration++; rules.value = rules.value.some(item => item.id === saved.id) ? rules.value.map(item => item.id === saved.id ? saved : item) : [...rules.value, saved]
    editor.value = undefined; notice.value = '整理配置已保存，后续任务使用新配置'; void refresh()
  }, true, current.id || 'editor')
}
async function toggleAutomatic(rule: MediaRule, field: 'realtimeEnabled' | 'scheduleEnabled') {
  await act(field, async () => {
    const settings = { realtimeEnabled: rule.realtimeEnabled, scheduleEnabled: rule.scheduleEnabled, intervalSeconds: rule.intervalSeconds, [field]: !rule[field] }
    const saved = await request<MediaRule>(`/rules/${rule.id}/schedule`, { method: 'PATCH', body: JSON.stringify(settings) })
    stateGeneration++; rules.value = rules.value.map(item => item.id === saved.id ? saved : item)
    if (editor.value?.id === saved.id) {
      const previous = { ...editor.value }
      Object.assign(editor.value, settings)
      if (ruleSignature(previous) === editorSnapshot.value) editorSnapshot.value = ruleSignature(editor.value)
    }
    const kind = field === 'realtimeEnabled' ? '事件监控' : scanLabel(rule)
    notice.value = saved[field] ? `已启用${saved.name}的${kind}` : `已暂停${saved.name}的${kind}${activeRules.value.includes(saved.id) ? '，正在执行的批次会继续完成' : ''}`
    void refresh()
  }, false, rule.id)
}
async function runNow(rule: MediaRule) {
  await act('run', async () => {
    const run = await request<{ status: string }>(`/rules/${rule.id}/run`, { method: 'POST' }); stateGeneration++
    active.value = true; activeRules.value = [...new Set([...activeRules.value, rule.id])]
    notice.value = run.status === 'queued' ? `${rule.name}已按手动任务排队，等待当前批次完成；可在整理记录中查看进度` : run.status === 'scanning' ? `${rule.name}正在识别，继续执行现有批次；可在整理记录中查看进度` : run.status === 'running' ? `${rule.name}正在整理，继续执行现有批次；可在整理记录中查看进度` : `${rule.name}已提交，可在整理记录中查看进度与结果`
    void refresh()
  }, false, rule.id)
}
async function stop(rule: MediaRule) {
  if (ruleStopping(rule.id)) return
  await act('stop', async () => {
    const { rule: saved } = await request<{ rule: MediaRule }>(`/rules/${rule.id}/stop`, { method: 'POST' })
    stateGeneration++; rules.value = rules.value.map(item => item.id === saved.id ? saved : item)
    stoppingRules.value = [...new Set([...stoppingRules.value, saved.id])]
    if (editor.value?.id === saved.id) {
      const unchanged = ruleSignature(editor.value) === editorSnapshot.value
      editor.value.realtimeEnabled = saved.realtimeEnabled; editor.value.scheduleEnabled = saved.scheduleEnabled
      if (unchanged) editorSnapshot.value = ruleSignature(editor.value)
    }
    notice.value = `${rule.name}已暂停自动触发，正在结束当前文件操作`
    void refresh()
  }, false, rule.id)
}
async function remove() {
  const current = deleting.value
  if (!current) return
  await act('delete', async () => {
    await request(`/rules/${current.id}`, { method: 'DELETE' }); stateGeneration++
    rules.value = rules.value.filter(rule => rule.id !== current.id); deleting.value = undefined; await refresh()
  }, true, current.id)
}
function poll() { window.clearTimeout(timer); if (lifecycle.signal.aborted || document.hidden || !active.value && !rules.value.some(r => r.realtimeEnabled || r.scheduleEnabled)) return; timer = window.setTimeout(async () => { await refresh(); poll() }, active.value ? 5000 : 15000) }
async function visible() { if (!document.hidden) await refresh(); poll() }
function decideLeave(value: boolean) { leaveDialog.value = false; leaveDecision?.(value); leaveDecision = undefined }
function beforeUnload(event: BeforeUnloadEvent) { if (requesting.value || unsaved.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteLeave(() => {
  leaveDecision?.(false); leaveDecision = undefined
  if (busy.value) { error.value = '正在处理当前操作，请完成后再切换页面'; if (editor.value || deleting.value) dialogError.value = error.value; return false }
  if (!unsaved.value) { leaveDialog.value = false; return true }
  leaveDialog.value = true; return new Promise<boolean>(resolve => { leaveDecision = resolve })
})
onMounted(async () => { window.addEventListener('beforeunload', beforeUnload); document.addEventListener('visibilitychange', visible); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); window.removeEventListener('beforeunload', beforeUnload); document.removeEventListener('visibilitychange', visible); decideLeave(false) })
</script>

<template>
    <div class="mo-page">
      <p v-if="error" role="alert" class="mo-error">{{ error }}<button @click="refresh()">重新加载</button></p>
      <p v-if="notice" role="status" class="mo-muted">{{ notice }}</p>
      <section class="mo-rules" aria-label="整理规则">
        <header class="mo-section"><h2>整理规则</h2><button class="mo-button mo-primary" @click="edit()"><IconPlus :size="17" aria-hidden="true" />新增规则</button></header>
        <p v-if="loading" class="mo-empty">正在读取配置…</p>
        <div v-else-if="!rules.length" class="mo-empty"><IconFolder :size="30" /><p>添加来源与目标目录，立即运行或启用监控。</p><button class="mo-button" @click="edit()">添加整理规则</button></div>
        <div v-else class="mo-rule-grid">
          <article v-for="rule in rules" :key="rule.id" class="mo-rule-card ambient-glow" :aria-label="`${rule.name}整理规则`">
            <header class="mo-rule-heading">
              <span class="mo-rule-symbol" aria-hidden="true"><component :is="rule.storage === 'local' ? IconServer : IconCloud" :size="21" /></span>
              <div class="mo-rule-name"><h3 :title="rule.name">{{ rule.name }}</h3><span>{{ storageLabels[rule.storage] || rule.storage }}</span></div>
              <div class="mo-rule-tools"><button class="mo-icon" :disabled="ruleBusy(rule.id)" :aria-label="`编辑${rule.name}`" @click="edit(rule)"><IconEdit :size="17" /></button><button class="mo-icon mo-rule-delete" :disabled="ruleBusy(rule.id) || ruleActive(rule.id)" :aria-label="`删除${rule.name}`" @click="deleting = { id: rule.id, name: rule.name }"><IconTrash :size="17" /></button></div>
            </header>
            <div class="mo-rule-schedules" role="status" :aria-label="`${rule.name}监控状态`">
              <div v-if="rule.storage !== 'cd2'" class="mo-rule-schedule" :class="{ 'is-enabled': rule.realtimeEnabled, 'is-unavailable': rule.realtimeEnabled && monitoring[rule.id]?.mode === 'unavailable' }"><IconRadar :size="14" aria-hidden="true" /><span>{{ realtimeLabel(rule) }}</span></div>
              <div class="mo-rule-schedule" :class="{ 'is-enabled': rule.scheduleEnabled }"><component :is="rule.scheduleEnabled ? IconClock : IconPlayerPause" :size="14" aria-hidden="true" /><span>{{ scheduledLabel(rule) }}</span></div>
            </div>
            <p v-if="rule.storage !== 'cd2' && rule.realtimeEnabled && monitoring[rule.id]?.message" class="mo-rule-monitor-message">{{ monitoring[rule.id]?.message }}</p>
            <p v-if="rule.scheduleEnabled && nextScanLabel(rule)" class="mo-rule-monitor-message">{{ nextScanLabel(rule) }}</p>
            <dl class="mo-rule-details">
              <div class="mo-rule-path"><dt><IconFolder class="mo-rule-source-icon" :size="16" aria-hidden="true" />来源</dt><dd><code :title="rule.source">{{ rule.source }}</code></dd></div>
              <div class="mo-rule-path"><dt><IconFolderCheck class="mo-rule-target-icon" :size="16" aria-hidden="true" />目标</dt><dd><code :title="rule.target">{{ rule.target }}</code></dd></div>
              <div class="mo-rule-path"><dt><IconFolderExclamation class="mo-rule-failure-icon" :size="16" aria-hidden="true" />失败目录</dt><dd><code v-if="rule.failure" :title="rule.failure">{{ rule.failure }}</code><span v-else class="mo-rule-unset">未设置，识别失败的视频留在来源</span></dd></div>
              <div><dt><IconAdjustmentsHorizontal class="mo-rule-policy-icon" :size="16" aria-hidden="true" />整理策略</dt><dd>{{ rule.classify ? '按类型分类' : '使用目标目录' }}<span class="mo-rule-policy">{{ rule.storage !== 'local' && rule.deleteDuplicates ? '同名且 SHA1 相同删除来源' : '不覆盖同名文件' }}</span></dd></div>
              <div><dt><IconTrash class="mo-rule-cleanup-icon" :size="16" aria-hidden="true" />自动清理</dt><dd>无关文件、空子目录；{{ attachmentCleanupLabel(rule) }}</dd></div>
            </dl>
            <footer class="mo-rule-actions">
              <button v-if="rule.storage !== 'cd2'" class="mo-button mo-rule-toggle" :disabled="ruleBusy(rule.id) || ruleStopping(rule.id)" :aria-label="`${rule.realtimeEnabled ? '暂停' : '启用'}${rule.name}的事件监控`" @click="toggleAutomatic(rule, 'realtimeEnabled')"><component :is="rule.realtimeEnabled ? IconPlayerPause : IconRadar" :size="16" aria-hidden="true" />{{ pending[rule.id] === 'realtimeEnabled' ? '保存中…' : rule.realtimeEnabled ? '暂停事件' : '启用事件' }}</button>
              <button class="mo-button mo-rule-toggle" :disabled="ruleBusy(rule.id) || ruleStopping(rule.id)" :aria-label="`${rule.scheduleEnabled ? '暂停' : '启用'}${rule.name}的${rule.storage === 'cd2' ? '目录轮询' : '定时完整扫描'}`" @click="toggleAutomatic(rule, 'scheduleEnabled')"><component :is="rule.scheduleEnabled ? IconPlayerPause : IconClock" :size="16" aria-hidden="true" />{{ pending[rule.id] === 'scheduleEnabled' ? '保存中…' : rule.storage === 'cd2' ? (rule.scheduleEnabled ? '暂停轮询' : '启用轮询') : rule.scheduleEnabled ? '暂停定时' : '启用定时' }}</button>
              <button v-if="ruleActive(rule.id)" class="mo-button mo-rule-stop" :disabled="ruleBusy(rule.id) || ruleStopping(rule.id)" :aria-label="`停止${rule.name}的整理`" :aria-busy="ruleStopping(rule.id) || pending[rule.id] === 'stop'" @click="stop(rule)"><IconPlayerStop :size="16" aria-hidden="true" />{{ pending[rule.id] === 'stop' || ruleStopping(rule.id) ? '停止中…' : '停止整理' }}</button>
              <button v-else class="mo-button mo-rule-scan" :disabled="ruleBusy(rule.id)" :aria-label="`${rule.name}立即完整扫描`" @click="runNow(rule)"><IconPlayerPlay :size="16" aria-hidden="true" />{{ pending[rule.id] === 'run' ? '提交中…' : '立即完整扫描' }}</button>
            </footer>
          </article>
        </div>
      </section>
    </div>
    <FloatingDialog v-if="editor" title="整理规则" close-label="关闭整理规则" :close-disabled="!!busy" @close="editor = undefined">
      <form id="media-rule-form" class="mo-form" @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p>
        <p v-if="activeRules.includes(editor.id)" class="mo-muted">当前批次继续使用启动时的配置，保存的修改从后续任务生效。</p>
        <label>规则名称<input v-model="editor.name" required maxlength="80" placeholder="例如：NAS 下载目录" /></label>
        <label>存储位置<SelectMenu v-model="editor.storage" :options="storageOptions" :disabled="!!busy" label="存储位置" @change="storageChanged(true)" /></label>
        <label>来源目录<div class="mo-input-action"><input v-model="editor.source" required :readonly="editor.storage !== 'local'" :placeholder="editor.storage === 'local' ? '/media/inbox' : editor.storage === 'cd2' ? '选择 CloudDrive2 来源目录' : '选择 115 来源目录'" /><button type="button" class="mo-icon" aria-label="选择来源目录" :disabled="!!busy" @click="chooseDirectory('source')"><IconFolder :size="19" /></button></div></label>
        <label>目标目录<div class="mo-input-action"><input v-model="editor.target" required :readonly="editor.storage !== 'local'" :placeholder="editor.storage === 'local' ? '/media/library' : editor.storage === 'cd2' ? '选择 CloudDrive2 目标目录' : '选择 115 目标目录'" /><button type="button" class="mo-icon" aria-label="选择目标目录" :disabled="!!busy" @click="chooseDirectory('target')"><IconFolder :size="19" /></button></div></label>
        <label>整理失败目录（可选）<div class="mo-input-action"><input v-model="editor.failure" aria-label="整理失败目录" :readonly="editor.storage !== 'local'" placeholder="留空则保留在来源目录" /><button v-if="editor.failure" type="button" class="mo-icon" aria-label="清空整理失败目录" @click="clearFailure"><IconX :size="17" /></button><button type="button" class="mo-icon" aria-label="选择整理失败目录" :disabled="!!busy" @click="chooseDirectory('failure')"><IconFolder :size="19" /></button></div><span class="mo-muted">执行时将无法匹配、季集不明或冲突文件移入，保留原名与目录层级；网络故障保留原处。</span></label>
        <p class="mo-muted">{{ directoryDescription }}</p>
        <RouterLink v-if="editor.storage === 'cd2'" to="/resources/clouddrive2" class="mo-muted underline">配置 CloudDrive2 连接</RouterLink>
        <SettingSwitch v-model="editor.classify" label="按作品信息分类" description="根据作品信息及当前分类规则，在目标目录下生成分类子目录。" />
        <SettingSwitch v-if="editor.storage !== 'local'" v-model="duplicateCleanup" label="自动删除相同文件" description="目标同名且双方 SHA1 一致时删除来源文件；SHA1 不同或缺失时保留冲突。" />
        <div class="mo-monitor-settings">
          <SettingSwitch v-if="editor.storage !== 'cd2'" v-model="editor.realtimeEnabled" label="事件监控" description="发现变化后整理受影响目录。" :disabled="!!busy || ruleStopping(editor.id)" />
          <div class="mo-schedule-line">
            <SettingSwitch v-model="editor.scheduleEnabled" :label="editor.storage === 'cd2' ? '目录轮询' : '定时完整扫描'" :disabled="!!busy || ruleStopping(editor.id)" />
            <label class="mo-scan-interval">间隔<div><input v-model.number="scanInterval" :aria-label="editor.storage === 'cd2' ? '轮询间隔' : '完整扫描间隔'" type="number" min="1" :max="editor.storage === 'cd2' ? 86400 : 1440" step="1" required :disabled="!!busy" /><span>{{ editor.storage === 'cd2' ? '秒' : '分钟' }}</span></div></label>
          </div>
          <div class="mo-attachment-line">
            <SettingSwitch v-model="editor.attachmentCleanupEnabled" label="清理未匹配附件" :disabled="!!busy" />
            <label>附件宽限（秒）<input v-model.number="editor.attachmentGraceSeconds" aria-label="附件宽限" type="number" min="0" max="86400" step="1" required :disabled="!!busy || !editor.attachmentCleanupEnabled" /></label>
          </div>
        </div>
        <p v-if="editor.storage === 'cd2'" class="mo-muted">通过目录轮询来源目录，默认每轮完成后等待 20 秒再扫描。停止整理会暂停轮询，已发出的文件操作收尾后结束。</p>
        <p v-else class="mo-muted">事件监控与定时完整扫描分别控制，完整扫描用于补漏。停止整理会同时暂停两种自动触发，已发出的文件操作收尾后结束。</p>
        <p class="mo-muted">已匹配附件随视频整理；未匹配附件按宽限复查清理，0 秒表示立即复查。空子目录自动清理，来源根目录保留。附件处理失败不影响视频成功。</p>
        <p class="mo-muted">STRM 正文保持原样。已有同名文件不会覆盖，命名沿用全局电影和剧集模板。</p>
      </form>
      <template #footer><div class="mo-footer"><button class="mo-button" :disabled="!!busy" @click="editor = undefined">取消</button><button form="media-rule-form" type="submit" class="mo-button mo-primary" :disabled="!!busy">{{ busy === 'save' ? '保存中…' : '保存规则' }}</button></div></template>
    </FloatingDialog>
    <MediaLocalDirectoryDialog v-if="directory && editor?.storage === 'local'" :token="token" :title="directoryTitle" :initial-path="editor[directory] || ''" @close="directory = undefined" @select="picked({ id: '', path: $event })" />
    <P115DirectoryDialog v-if="directory && editor?.storage === '115' && editor.accountId" :token="token" :account-id="editor.accountId" :title="directoryTitle" :allow-root="false" @close="directory = undefined" @select="picked" />
    <CD2DirectoryDialog v-if="directory && editor?.storage === 'cd2' && editor.connectionId && editor.connectionRevision" :token="token" :connection-id="editor.connectionId" :revision="editor.connectionRevision" :title="directoryTitle" :initial-path="editor[directory] || '/'" :allow-root="false" require-writable @close="directory = undefined" @select="pickedCD2" />
    <FloatingDialog v-if="deleting" title="删除整理规则" close-label="取消删除规则" :close-disabled="!!busy" @close="deleting = undefined"><p class="py-4 text-sm">确认删除{{ deleting.name }}？同时删除该规则的整理记录；实际文件保持不变。</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p><template #footer><div class="mo-footer"><button class="mo-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button class="mo-button mo-primary" :disabled="!!busy" @click="remove">删除规则</button></div></template></FloatingDialog>
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的整理修改？" @close="decideLeave(false)"><p>整理规则尚未保存，离开后将丢弃这些修改。后台整理任务会继续运行。</p><template #footer><button type="button" class="mo-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="mo-button mo-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
</template>

<style scoped src="../styles/media-organize.css"></style>
<style scoped>
.mo-rules{min-width:0}.mo-rule-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr));gap:16px}
.mo-rule-card{display:flex;min-width:0;flex-direction:column;padding:18px;border:1px solid rgb(148 163 184/.16);border-radius:16px;background:#fff;box-shadow:0 2px 8px rgb(15 23 42/.035);transition:box-shadow .2s;--ambient-glow-strength:.55}
.mo-rule-symbol{display:flex;align-items:center;justify-content:center;flex:none;width:38px;height:38px;border-radius:11px;background:#dbeafe;color:#1d4ed8}
.mo-rule-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}.mo-rule-name{min-width:0;flex:1}.mo-rule-name h3{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:16px;font-weight:600;line-height:24px;color:#0f172a}.mo-rule-name>span{display:block;margin-top:2px;font-size:11px;line-height:18px;color:#64748b}
.mo-rule-tools{display:flex;flex:none;gap:2px;margin:-5px -5px 0 0}.mo-rule-tools .mo-icon{width:32px;min-height:32px;color:#64748b}
.mo-rule-schedule{display:inline-flex;align-items:center;align-self:flex-start;gap:5px;margin-top:10px;padding:3px 8px;border-radius:6px;background:#f1f5f9;color:#475569;font-size:12px;line-height:18px}.mo-rule-schedule.is-enabled{background:#2563eb;color:#fff}
.mo-rule-schedules{display:flex;flex-wrap:wrap;gap:6px}.mo-rule-schedule svg{flex:none}.mo-rule-schedule span{min-width:0;overflow-wrap:anywhere}.mo-rule-schedule.is-unavailable{background:#fff7ed;color:#9a3412}.mo-rule-monitor-message{margin-top:8px;font-size:12px;line-height:1.7;overflow-wrap:anywhere;color:#64748b}:global(.dark .mo-rule-schedule.is-enabled.is-unavailable){background:#3b291d;color:#fdba74}:global(.dark .mo-rule-monitor-message){color:#94a3b8}
.mo-rule-details{display:grid;gap:9px;margin-top:18px}.mo-rule-details>div{display:grid;grid-template-columns:80px minmax(0,1fr);gap:8px;align-items:baseline}.mo-rule-details dt{display:flex;align-items:center;gap:6px;white-space:nowrap;font-size:12px;line-height:21px;color:#64748b}.mo-rule-details dt svg{flex:none;color:#6688b5}.mo-rule-details dd{min-width:0;font-size:13px;line-height:21px;color:#334155}.mo-rule-path{min-height:42px}.mo-rule-path code{display:-webkit-box;overflow:hidden;-webkit-box-orient:vertical;-webkit-line-clamp:2;overflow-wrap:anywhere;font-size:13px;line-height:21px;color:#475569}.mo-rule-policy{display:inline-block;margin-left:8px;color:#64748b;font-size:12px}.mo-rule-unset{font-size:12px;color:#64748b}
.mo-rule-details .mo-rule-source-icon{color:#2563eb}.mo-rule-details .mo-rule-target-icon{color:#7c3aed}.mo-rule-details .mo-rule-failure-icon{color:#b45309}.mo-rule-details .mo-rule-policy-icon{color:#4f46e5}.mo-rule-details .mo-rule-cleanup-icon{color:#64748b}
:global(.dark .mo-rule-details .mo-rule-source-icon){color:#60a5fa}:global(.dark .mo-rule-details .mo-rule-target-icon){color:#c4b5fd}:global(.dark .mo-rule-details .mo-rule-failure-icon){color:#fbbf24}:global(.dark .mo-rule-details .mo-rule-policy-icon){color:#a5b4fc}:global(.dark .mo-rule-details .mo-rule-cleanup-icon){color:#94a3b8}
.mo-rule-actions{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-top:auto;padding-top:18px}.mo-rule-actions .mo-button{min-height:40px;font-size:13px}.mo-rule-actions .mo-rule-toggle{background:#f1f5f9;color:#334155}.mo-rule-actions .mo-rule-toggle:hover{background:#e2e8f0}.mo-rule-actions .mo-rule-scan{background:#dbeafe;color:#1d4ed8}.mo-rule-actions .mo-rule-scan:hover{background:#bfdbfe}
:global(.dark .mo-rule-card){background:#1e293b;border-color:rgb(148 163 184/.12);box-shadow:0 2px 8px rgb(0 0 0/.12)}:global(.dark .mo-rule-symbol){background:#1e3a5f;color:#93c5fd}:global(.dark .mo-rule-name h3){color:#f1f5f9}:global(.dark .mo-rule-name>span),:global(.dark .mo-rule-tools .mo-icon),:global(.dark .mo-rule-details dt),:global(.dark .mo-rule-policy),:global(.dark .mo-rule-unset){color:#94a3b8}:global(.dark .mo-rule-details dd),:global(.dark .mo-rule-path code){color:#cbd5e1}:global(.dark .mo-rule-details dt svg){color:#86a5ce}:global(.dark .mo-rule-schedule){background:#334155;color:#cbd5e1}:global(.dark .mo-rule-schedule.is-enabled){background:#1d4ed8;color:#fff}:global(.dark .mo-rule-actions .mo-rule-toggle){background:#334155;color:#e2e8f0}:global(.dark .mo-rule-actions .mo-rule-toggle:hover){background:#475569}:global(.dark .mo-rule-actions .mo-rule-toggle.is-start){background:#3769b5;color:#fff}:global(.dark .mo-rule-actions .mo-rule-toggle.is-start:hover){background:#4479ca}:global(.dark .mo-rule-actions .mo-rule-scan){background:#1e3a5f;color:#bfdbfe}:global(.dark .mo-rule-actions .mo-rule-scan:hover){background:#254972}
@media(hover:hover) and (pointer:fine){.mo-rule-card:hover{box-shadow:0 8px 24px -10px rgb(37 99 235/.2)}.mo-rule-tools .mo-rule-delete:hover{color:#dc2626}:global(.dark .mo-rule-card:hover){box-shadow:0 8px 24px -10px rgb(0 0 0/.35)}:global(.dark .mo-rule-tools .mo-rule-delete:hover){color:#fca5a5}}
@media(max-width:639px){.mo-rule-grid{gap:12px}.mo-rule-card{padding:16px}.mo-rule-tools .mo-icon{width:40px;min-height:40px}.mo-rule-actions .mo-button{min-height:44px}}
@media(prefers-reduced-motion:reduce){.mo-rule-card{transition:none}}

.mo-form{display:grid;gap:18px;padding:8px 0}.mo-form label{display:grid;gap:7px;font-size:12px;font-weight:500}.mo-form input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.23);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));font-size:13px;outline:none}.mo-form input:focus{border-color:#3b82f6}.mo-input-action{display:flex;gap:6px}.mo-input-action input{flex:1}.mo-input-action>.mo-icon{flex:none}.mo-form :deep(input[role="switch"]:checked + span){background:#2563eb}:global(.dark .mo-form input){background:#1e293b;color:#e2e8f0;border-color:rgb(148 163 184/.3)}:global(.dark .mo-form){color:#e2e8f0}
.mo-rule-actions .mo-rule-scan,.mo-rule-actions .mo-rule-stop{grid-column:1/-1}.mo-rule-actions .mo-rule-stop{background:#e2e8f0;color:#334155}.mo-rule-actions .mo-rule-stop:hover{background:#cbd5e1}:global(.dark .mo-rule-actions .mo-rule-stop){background:#334155;color:#e2e8f0}:global(.dark .mo-rule-actions .mo-rule-stop:hover){background:#475569}
.mo-monitor-settings{display:grid;gap:16px}.mo-schedule-line,.mo-attachment-line{display:flex;align-items:center;flex-wrap:wrap;gap:12px 24px}
.mo-scan-interval{min-width:140px;max-width:200px}.mo-scan-interval>div{display:flex;align-items:center;gap:8px}.mo-scan-interval input{width:100px}.mo-scan-interval span{white-space:nowrap;color:#64748b}:global(.dark .mo-scan-interval span){color:#94a3b8}
</style>
