<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconFolder, IconFolderCheck, IconEdit, IconTrash, IconRefresh, IconSearch, IconLoader2, IconSettings, IconCode, IconChevronRight } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath } from '../utils/strmDirectory'

interface Settings {
  baseUrl: string; videoExtensions: string; metadataExtensions: string; minSizeMB: number
  eventIntervalSeconds: number; reconcileIntervalHours: number; fullScanIntervalHours: number
}
interface Mapping {
  id: string; name: string; accountId: string; sourceCID: string; sourcePath: string; outputDir: string; enabled: boolean
  status: string; mode?: string; queued: boolean; lastScanAt?: string; lastError?: string
  stats: { videos: number; written: number; unchanged: number; metadataWritten: number; failed: number }
}
interface State {
  mappings: Mapping[]; settings: Settings; settingsSaved: boolean; primaryAccountId: string; accountReady: boolean
  events?: { lastCheckedAt?: string; lastError?: string }
}
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const state = ref<State>(), editor = ref<Mapping>(), deleting = ref<Mapping>()
const settingsDraft = ref<Settings>(), savedSettings = ref<Settings>()
const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true)
const sourceDialog = ref(false), outputDialog = ref(false)
const lifecycle = new AbortController()
const addButton = ref<HTMLButtonElement>()
let timer: number | undefined, pendingRefresh: Promise<void> | undefined, generation = 0, snapshot = ''
const active = (m: Mapping) => m.status === 'running' || m.queued && !m.lastError
const editorDirty = computed(() => !!editor.value && JSON.stringify(editor.value) !== snapshot)
const settingsDirty = computed(() => JSON.stringify(settingsDraft.value) !== JSON.stringify(savedSettings.value))
const needsSettingsSave = computed(() => !!settingsDraft.value && (!state.value?.settingsSaved || settingsDirty.value))
const unsaved = computed(() => editorDirty.value || settingsDirty.value)
const settingsReady = computed(() => !!state.value?.settingsSaved && !settingsDirty.value)
const hasSchedule = computed(() => !!state.value && [state.value.settings.eventIntervalSeconds, state.value.settings.reconcileIntervalHours, state.value.settings.fullScanIntervalHours].some(value => value > 0))
const canStart = computed(() => settingsReady.value && !!state.value?.accountReady)
const startHint = computed(() => !state.value?.settingsSaved ? '请先保存基础设置' : settingsDirty.value ? '请先保存或还原基础设置的修改' : !state.value?.accountReady ? '请先配置并登录主账号' : undefined)
const strmPreview = computed(() => (settingsDraft.value?.baseUrl.trim().replace(/\/+$/, '') || 'http://NAS_IP:8080') + '/api/drive115/ecjq9ichcb40lzlvx/123456789/0123456789ABCDEF0123456789ABCDEF01234567/1073741824/Example.S01E01.mkv')
const changedAccount = (m: Mapping) => !!state.value && m.accountId !== state.value.primaryAccountId
const requestSignal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(60000)])
const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(props.token, 'drive-strm' + path, { ...init, signal: requestSignal() })
const failure = (e: unknown) => e instanceof Error && e.name === 'TimeoutError' ? '请求超时，请重新加载确认结果后再重试' : e instanceof Error ? e.message : '操作失败'
function acceptSettings(value: Settings) { savedSettings.value = { ...value }; settingsDraft.value = { ...value } }
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
  const running = state.value?.mappings.some(active)
  if (!running && !state.value?.mappings.some(m => m.enabled)) return
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
    const settings = await request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(settingsDraft.value) })
    generation++; acceptSettings(settings)
    if (state.value) { state.value.settings = settings; state.value.settingsSaved = true }
    notice.value = '基础设置已保存，周期将在后续调度中生效；进行中的任务继续，已有 STRM 可通过完整扫描更新。'
    await refresh()
  })
}
function resetSettings() { if (savedSettings.value) acceptSettings(state.value?.settings || savedSettings.value) }
function edit(m?: Mapping) {
  if (busy.value || !canStart.value) return
  editor.value = m ? { ...m, stats: { ...m.stats } } : { id: '', name: '', accountId: state.value?.primaryAccountId || '', sourceCID: '', sourcePath: '', outputDir: '', enabled: true, status: 'idle', queued: false, stats: { videos: 0, written: 0, unchanged: 0, metadataWritten: 0, failed: 0 } }
  snapshot = JSON.stringify(editor.value); dialogError.value = ''
}
function closeEditor() { if (!busy.value && (!editorDirty.value || window.confirm('放弃未保存的修改？'))) editor.value = undefined }
async function selectSource() {
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
  if (!canStart.value || !editor.value) return
  await act('save', async () => {
    const m = editor.value!
    await request(m.id ? '/' + m.id : '', { method: m.id ? 'PUT' : 'POST', body: JSON.stringify({ name: m.name, accountId: m.accountId, sourceCID: m.sourceCID, outputDir: m.outputDir, enabled: m.enabled }) })
    generation++; editor.value = undefined; notice.value = '目录已保存，首次扫描将补齐本地文件'; await refresh()
  }, true)
}
async function run(m: Mapping, full: boolean) {
  if (!canStart.value) return
  await act(m.id, async () => { await request('/' + m.id + (full ? '/scan' : '/check'), { method: 'POST' }); generation++; notice.value = full ? '已安排完整扫描，将补齐本地缺少的文件' : '已检查最新变更，有更新时将生成文件'; await refresh() })
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
function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未检查' }
function status(m: Mapping) { return m.status === 'running' ? (m.mode === 'changes' ? '正在检查变更' : m.mode === 'tree' ? '正在核对目录' : '正在完整扫描') : m.status === 'stopped' ? '已停止' : m.lastError ? '需检查' : m.queued ? '等待扫描' : !m.enabled ? '已暂停' : hasSchedule.value ? '监控中' : '仅手动' }
async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
function beforeUnload(e: BeforeUnloadEvent) { if (unsaved.value || busy.value) { e.preventDefault(); e.returnValue = '' } }
onBeforeRouteLeave(() => {
  if (busy.value) { (editor.value || deleting.value ? dialogError : error).value = '正在处理当前操作，请完成后再切换页面'; return false }
  return !unsaved.value || window.confirm('放弃未保存的修改？')
})
onMounted(async () => { document.title = '目录 STRM · Cinema115'; document.addEventListener('visibilitychange', visible); window.addEventListener('beforeunload', beforeUnload); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout title="目录 STRM" description="从主网盘生成本地 STRM，保留原目录层级。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <p v-if="error" role="alert" class="ds-alert">{{ error }} <button type="button" class="underline" :disabled="!!busy" @click="refresh">重新加载</button></p>
    <p v-if="notice" role="status" class="ds-notice">{{ notice }}</p>
    <p v-if="state && !state.accountReady" class="ds-alert">请先在 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号，基础设置可先保存。</p>
    <p v-if="state?.events?.lastError" role="status" class="ds-alert">事件检查暂不可用：{{ state.events.lastError }}。仍可完整扫描补齐。</p>
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
        <p class="ds-help">所有目录共用；保存后用于后续任务，进行中的任务保持原设置。</p>
        <fieldset :disabled="!!busy" class="ds-settings-grid">
          <div><label class="ds-outlined"><span>STRM 播放地址</span><input v-model="settingsDraft.baseUrl" type="url" required placeholder="http://NAS_IP:8080" aria-describedby="ds-base-help" /></label><p id="ds-base-help" class="ds-help">填写播放器或 Emby 能访问的 Cinema115 地址。</p></div>
          <div><label class="ds-outlined"><span>视频后缀</span><input v-model="settingsDraft.videoExtensions" required placeholder="mp4,mkv,avi" aria-describedby="ds-video-help" /></label><p id="ds-video-help" class="ds-help">逗号分隔，为匹配的视频生成 STRM。</p></div>
          <div><label class="ds-outlined"><span>元数据后缀</span><input v-model="settingsDraft.metadataExtensions" placeholder="srt,ass,ssa" aria-describedby="ds-metadata-help" /></label><p id="ds-metadata-help" class="ds-help">按原名下载字幕、NFO 或图片，留空不下载；单个最多 32 MB。</p></div>
          <div><label class="ds-outlined"><span>最小文件大小（MB）</span><input v-model.number="settingsDraft.minSizeMB" type="number" required min="0" max="1073741824" step="1" aria-describedby="ds-size-help" /></label><p id="ds-size-help" class="ds-help">0 表示不限制，仅过滤视频，不影响元数据。</p></div>
        </fieldset>
        <fieldset :disabled="!!busy" class="ds-schedules" aria-describedby="ds-schedules-help">
          <legend>自动任务</legend>
          <p id="ds-schedules-help" class="ds-help">仅用于开启监控的目录；各项独立运行，关闭后仍可手动检查或完整扫描。</p>
          <div class="ds-schedule-grid">
            <div><label class="ds-outlined"><span>事件检查间隔</span><select v-model.number="settingsDraft.eventIntervalSeconds" aria-describedby="ds-event-help"><option :value="0">关闭</option><option :value="20">每 20 秒</option><option :value="60">每 1 分钟</option><option :value="300">每 5 分钟</option><option :value="900">每 15 分钟</option><option :value="1800">每 30 分钟</option></select></label><p id="ds-event-help" class="ds-help">共享读取生活事件，按变更更新目录。</p></div>
            <div><label class="ds-outlined"><span>目录核对周期</span><select v-model.number="settingsDraft.reconcileIntervalHours" aria-describedby="ds-reconcile-help"><option :value="0">关闭</option><option :value="6">每 6 小时</option><option :value="12">每 12 小时</option><option :value="24">每天</option><option :value="72">每 3 天</option><option :value="168">每周</option></select></label><p id="ds-reconcile-help" class="ds-help">比对目录树，补齐本地缺失文件。</p></div>
            <div><label class="ds-outlined"><span>全量扫描周期</span><select v-model.number="settingsDraft.fullScanIntervalHours" aria-describedby="ds-full-help"><option :value="0">关闭</option><option :value="24">每天</option><option :value="72">每 3 天</option><option :value="168">每周</option><option :value="336">每 14 天</option><option :value="720">每 30 天</option></select></label><p id="ds-full-help" class="ds-help">读取全部文件资料，核验同路径替换。</p></div>
          </div>
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
          <article v-for="m in state.mappings" :key="m.id" class="ds-card ambient-glow" :aria-label="m.name">
            <header class="ds-heading"><span class="ds-symbol"><IconCloud :size="21" aria-hidden="true" /></span><div class="ds-name"><h3 :title="m.name">{{ m.name }}</h3><span class="ds-status" aria-live="polite" :class="{ running: active(m), attention: !!m.lastError }"><IconLoader2 v-if="active(m)" class="ds-spin" :size="12" aria-hidden="true" />{{ status(m) }}</span></div><button type="button" class="ds-icon" :disabled="!!busy || active(m) || !canStart" :title="startHint || '编辑目录'" :aria-label="'编辑' + m.name" @click="edit(m)"><IconEdit :size="17" aria-hidden="true" /></button><button type="button" class="ds-icon" :disabled="!!busy || active(m)" :aria-label="'删除' + m.name" @click="deleting = m; dialogError = ''"><IconTrash :size="17" aria-hidden="true" /></button></header>
            <dl class="ds-paths"><div><dt><IconCloud :size="15" aria-hidden="true" />来源</dt><dd :title="m.sourcePath">{{ m.sourcePath || m.sourceCID }}</dd></div><div><dt><IconFolderCheck :size="15" aria-hidden="true" />输出</dt><dd :title="strmDirectoryPath(m.outputDir)">{{ strmDirectoryPath(m.outputDir) }}</dd></div></dl>
            <div class="ds-counts"><span>本次视频 <b>{{ m.stats.videos }}</b></span><span>写入 {{ m.stats.written }}</span><span>未变 {{ m.stats.unchanged }}</span><span v-if="m.stats.metadataWritten">附件 {{ m.stats.metadataWritten }}</span><span v-if="m.stats.failed" class="ds-failed">失败 {{ m.stats.failed }}</span></div>
            <p v-if="changedAccount(m)" class="ds-failed ds-message">主账号已变化，请重新选择来源目录。</p><p v-else-if="m.lastError" class="ds-failed ds-message">{{ m.lastError }}</p>
            <div class="ds-meta"><span>最近扫描 {{ date(m.lastScanAt) }}</span><div><button v-if="active(m)" type="button" :disabled="!!busy" :aria-label="'停止' + m.name + '本次扫描'" @click="stop(m)">停止本次</button><button type="button" :disabled="!!busy || !m.enabled && (changedAccount(m) || !canStart)" :title="!m.enabled ? startHint : undefined" :aria-label="(m.enabled ? '暂停' : '开启') + m.name + '监控'" @click="toggle(m, !m.enabled)">{{ m.enabled ? '暂停监控' : '开启监控' }}</button></div></div>
            <footer class="ds-actions"><button type="button" class="ds-button ds-check" :disabled="!!busy || active(m) || changedAccount(m) || !canStart || !m.enabled" :title="startHint || (!m.enabled ? '开启监控后检查事件更新；也可直接完整扫描' : undefined)" @click="run(m, false)"><IconRefresh :size="16" aria-hidden="true" />{{ active(m) ? '检查中…' : '检查更新' }}</button><button type="button" class="ds-button" :disabled="!!busy || active(m) || changedAccount(m) || !canStart" :title="startHint" @click="run(m, true)"><IconSearch :size="16" aria-hidden="true" />完整扫描补齐</button></footer>
          </article>
        </div>
        <details class="ds-notes"><summary>同步说明</summary><p>事件检查、目录核对和全量扫描按各自周期执行，目录暂停后不参与自动任务。修改设置不额外安排扫描，已经接受的任务会继续；已有 STRM 可手动完整扫描更新。目录树导出 TXT 由系统清理；网盘源文件与旧本地文件均保留。</p></details>
      </section>
    </template>
    <P115ActionDialog v-if="editor" :title="editor.id ? '编辑目录' : '新增目录'" :busy="!!busy" @close="closeEditor">
      <form id="drive-strm-form" class="ds-form" @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p>
        <small v-if="editor.id">更换来源或输出目录会保留原有文件，并重新完整扫描。</small>
        <p v-if="changedAccount(editor)" role="alert" class="ds-failed">主账号已变化，请重新选择来源目录。</p>
        <label>名称<input v-model="editor.name" required maxlength="60" placeholder="例如：动漫媒体库" :disabled="!!busy" /></label>
        <div><span id="ds-source-label" class="ds-label">主网盘来源</span><button type="button" class="ds-directory" :disabled="!!busy" aria-labelledby="ds-source-label ds-source-value" @click="selectSource"><IconCloud :size="17" aria-hidden="true" /><span id="ds-source-value">{{ editor.sourcePath || '选择云端目录' }}</span><span class="ds-link">选择</span></button></div>
        <div><span class="ds-label">本地输出</span><div class="ds-output"><label class="ds-sr" for="ds-output">本地输出相对目录</label><input id="ds-output" v-model="editor.outputDir" required placeholder="例如：动漫（相对 /strm）" aria-describedby="ds-output-help" :disabled="!!busy" /><button type="button" class="ds-button" :disabled="!!busy" @click="outputDialog = true">选择</button></div><small id="ds-output-help">{{ strmDirectoryPath(editor.outputDir) }} · 可填写新的子目录</small></div>
        <SettingSwitch v-model="editor.enabled" label="自动监控" description="保存后先生成一次；开启后参与基础设置中的自动任务。" :disabled="!!busy" />
      </form>
      <template #footer><button type="button" class="ds-button" :disabled="!!busy" @click="closeEditor">取消</button><button type="submit" form="drive-strm-form" class="ds-button ds-primary" :disabled="!!busy || !canStart || !editor.sourceCID || !editor.outputDir || changedAccount(editor)">{{ busy === 'save' ? '保存中…' : '保存目录' }}</button></template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="sourceDialog && editor" :token="token" :account-id="editor.accountId" :allow-root="false" title="选择主网盘来源目录" @close="sourceDialog = false" @select="pickedSource" />
    <STRMDirectoryDialog v-if="outputDialog" :token="token" title="选择本地输出目录" @close="outputDialog = false" @select="path => { if (editor) editor.outputDir = path; outputDialog = false }" />
    <P115ActionDialog v-if="deleting" title="删除目录配置" :busy="!!busy" @close="deleting = undefined"><p>删除「{{ deleting.name }}」的配置与索引？已生成的本地文件和网盘文件都会保留。</p><p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p><template #footer><button type="button" class="ds-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button type="button" class="ds-button ds-danger" :disabled="!!busy" @click="remove">{{ busy ? '删除中…' : '删除配置' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ds-settings { @apply rounded-2xl bg-white dark:bg-slate-900; padding:20px; }
.ds-section-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.ds-section-heading h2 { @apply text-slate-800 dark:text-slate-100; display:flex; align-items:center; gap:8px; min-width:0; font-size:15px; font-weight:600; }
.ds-section-heading h2>svg { @apply text-blue-500; flex-shrink:0; }
.ds-section-count { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; padding:1px 7px; border-radius:6px; font-size:12px; font-variant-numeric:tabular-nums; }
.ds-save-actions { display:flex; align-items:center; gap:12px; flex-shrink:0; }
.ds-help { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.7; margin-top:7px; }
.ds-settings-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:18px 20px; margin-top:22px; min-width:0; }
.ds-schedules { margin-top:20px; min-width:0; }.ds-schedules legend { @apply text-slate-700 dark:text-slate-200; font-size:13px; font-weight:500; }.ds-schedule-grid { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:20px; margin-top:20px; }
.ds-outlined { @apply border border-slate-300 dark:border-slate-600; position:relative; display:block; min-width:0; border-radius:8px; transition:border-color .15s; }
.ds-outlined:focus-within { @apply border-blue-500; box-shadow:0 0 0 1px #3b82f6; }
.ds-outlined>span { @apply bg-white text-slate-600 dark:bg-slate-900 dark:text-slate-300; position:absolute; top:-9px; left:9px; padding:0 5px; font-size:12px; line-height:17px; }
.ds-outlined>input,.ds-outlined>select { @apply text-slate-800 dark:text-slate-100; display:block; width:100%; min-width:0; height:40px; padding:0 13px; border:0; border-radius:inherit; background:transparent; font-size:13px; outline:none; }
.ds-outlined>select { @apply dark:[color-scheme:dark]; padding-right:32px; cursor:pointer; }.ds-outlined option { @apply bg-white text-slate-800 dark:bg-slate-900 dark:text-slate-100; }
.ds-outlined>input:focus-visible,.ds-outlined>select:focus-visible { outline:none; }
.ds-preview { margin-top:14px; }.ds-preview summary { @apply text-slate-600 dark:text-slate-300; display:flex; align-items:center; gap:6px; width:fit-content; min-height:32px; cursor:pointer; list-style:none; font-size:12px; }.ds-preview summary::-webkit-details-marker { display:none; }.ds-chevron { transition:transform .15s; }.ds-preview[open] .ds-chevron { transform:rotate(90deg); }.ds-preview code { @apply rounded-lg bg-slate-50 text-slate-700 dark:bg-slate-950 dark:text-slate-300; display:block; padding:10px 12px; font-size:12px; line-height:1.8; overflow-wrap:anywhere; }
.ds-directories { margin-top:24px; }.ds-directories>.ds-section-heading { margin-bottom:14px; }.ds-start-hint { margin:-4px 0 12px; }
.ds-grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(min(100%,340px),1fr)); gap:14px; }
.ds-card { @apply rounded-2xl bg-white p-4 text-slate-700 shadow-sm dark:bg-slate-900 dark:text-slate-200; min-width:0; }
.ds-heading { display:flex; align-items:center; gap:8px; margin-bottom:16px; }.ds-symbol { @apply bg-blue-50 text-blue-600 dark:bg-blue-950/50 dark:text-blue-400; display:grid; place-items:center; width:36px; height:36px; border-radius:11px; flex-shrink:0; }.ds-name { flex:1; min-width:0; }.ds-name h3 { font-size:15px; font-weight:600; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.ds-status { @apply text-slate-500 dark:text-slate-400; display:inline-flex; gap:5px; align-items:center; font-size:12px; margin-top:3px; }.ds-status.running { @apply text-blue-600 dark:text-blue-400; }.ds-status.attention { @apply text-amber-700 dark:text-amber-400; }
.ds-icon { @apply text-slate-400 hover:bg-slate-100 hover:text-blue-600 dark:hover:bg-slate-800; display:grid; place-items:center; width:32px; height:36px; border-radius:8px; flex-shrink:0; }
.ds-paths { display:grid; gap:8px; font-size:13px; }.ds-paths>div { display:flex; align-items:baseline; gap:12px; }.ds-paths dt { @apply text-slate-500 dark:text-slate-400; display:flex; align-items:center; gap:6px; flex-shrink:0; }.ds-paths dd { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ds-counts { @apply bg-slate-50 dark:bg-slate-800/60; display:flex; flex-wrap:wrap; gap:5px 13px; border-radius:9px; padding:9px 10px; font-size:12px; margin-top:14px; }.ds-counts b { font-size:14px; font-variant-numeric:tabular-nums; }
.ds-meta { @apply text-slate-500 dark:text-slate-400; display:flex; flex-wrap:wrap; justify-content:space-between; align-items:center; gap:5px; font-size:12px; margin:10px 0; }.ds-meta button { @apply text-blue-600 dark:text-blue-400; padding:5px 0 5px 8px; }.ds-message { font-size:12px; line-height:1.6; margin-top:8px; overflow-wrap:anywhere; }.ds-failed { @apply text-red-600 dark:text-red-400; }
.ds-actions { display:grid; grid-template-columns:1fr 1fr; gap:8px; }.ds-button { @apply bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700; display:inline-flex; align-items:center; justify-content:center; gap:6px; border-radius:9px; min-height:36px; padding:7px 11px; font-size:13px; font-weight:500; }.ds-primary { @apply bg-blue-600 text-white hover:bg-blue-700 dark:bg-blue-600 dark:text-white dark:hover:bg-blue-500; }.ds-check { @apply bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60; }.ds-danger { @apply bg-red-600 text-white hover:bg-red-700 dark:bg-red-600 dark:text-white; }.ds-text-button { @apply text-blue-600 dark:text-blue-400; font-size:12px; padding:8px 0; }button:disabled { cursor:not-allowed; opacity:.45; }button:focus-visible,input:focus-visible,summary:focus-visible { outline:2px solid #3b82f6; outline-offset:3px; }
.ds-alert { @apply rounded-xl bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300; padding:12px; margin-bottom:12px; font-size:13px; overflow-wrap:anywhere; }.ds-notice { @apply text-blue-700 dark:text-blue-300; margin-bottom:12px; font-size:13px; }.ds-empty { @apply rounded-xl bg-white text-slate-500 dark:bg-slate-900 dark:text-slate-400; display:flex; align-items:center; gap:10px; padding:18px; font-size:13px; line-height:1.7; }.ds-empty>svg { flex-shrink:0; }
.ds-notes { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.8; margin-top:12px; }.ds-notes summary { width:fit-content; min-height:32px; cursor:pointer; }.ds-notes p { margin-top:4px; max-width:900px; }
.ds-form { display:grid; gap:15px; }.ds-form label { display:grid; gap:6px; font-size:13px; }.ds-form input { @apply border border-slate-200 bg-white text-slate-800 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100; border-radius:8px; min-width:0; width:100%; height:36px; padding:0 10px; font-size:13px; }.ds-form small { @apply text-slate-500 dark:text-slate-400; display:block; font-size:12px; line-height:1.6; margin-top:4px; }.ds-label { font-size:13px; display:block; margin-bottom:6px; }.ds-directory { @apply bg-slate-50 dark:bg-slate-800; display:flex; align-items:center; gap:8px; width:100%; padding:9px 10px; border-radius:8px; text-align:left; font-size:13px; }.ds-directory>span:first-of-type { min-width:0; flex:1; overflow-wrap:anywhere; }.ds-link { @apply text-blue-600 dark:text-blue-400; font-size:12px; flex-shrink:0; }.ds-output { display:flex; gap:8px; }.ds-output input { flex:1; }.ds-sr { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }.ds-spin { animation:ds-spin 1s linear infinite; }@keyframes ds-spin { to { transform:rotate(360deg) } }
@media(max-width:639px) { .ds-settings { padding:16px; }.ds-settings-grid,.ds-schedule-grid { grid-template-columns:minmax(0,1fr); gap:20px; }.ds-button { min-height:44px; padding-inline:8px; white-space:nowrap; }.ds-save-actions { gap:10px; }.ds-directory { min-height:44px; }.ds-icon { width:40px; height:40px; }.ds-meta button { min-height:40px; padding-inline:8px; }.ds-text-button,.ds-preview summary,.ds-notes summary { min-height:44px; }.ds-form input,.ds-outlined>input,.ds-outlined>select { height:44px; font-size:16px; } }
@media(max-width:359px) { .ds-actions { grid-template-columns:1fr; }.ds-settings>.ds-section-heading { flex-wrap:wrap; }.ds-save-actions { margin-left:auto; } }
@media(prefers-reduced-motion:reduce) { .ds-spin { animation:none; }.ds-chevron,.ds-outlined { transition:none; } }
</style>
