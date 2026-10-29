<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconFolder, IconFolderCheck, IconEdit, IconTrash, IconRefresh, IconSearch, IconLoader2 } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { p115Request } from '../lib/p115-api'
import { strmDirectoryPath } from '../utils/strmDirectory'

interface Mapping {
  id: string; name: string; accountId: string; sourceCID: string; sourcePath: string; outputDir: string; baseUrl: string
  enabled: boolean; videoExtensions: string; metadataExtensions: string; minSizeMB: number
  status: string; mode?: string; queued: boolean; lastScanAt?: string; lastError?: string
  stats: { videos: number; written: number; unchanged: number; metadataWritten: number; failed: number }
}
interface State { mappings: Mapping[]; primaryAccountId: string; accountReady: boolean; events?: { lastCheckedAt?: string; lastError?: string }; defaults: { baseUrl: string; videoExtensions: string } }
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const state = ref<State>(), editor = ref<Mapping>(), deleting = ref<Mapping>()
const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true)
const sourceDialog = ref(false), outputDialog = ref(false), subtitle = ref(false), nfo = ref(false), images = ref(false)
const lifecycle = new AbortController()
const addButton = ref<HTMLButtonElement>()
let timer: number | undefined, pendingRefresh: Promise<void> | undefined, generation = 0, snapshot = ''
const active = (m: Mapping) => m.status === 'running' || m.queued && !m.lastError
const signature = () => JSON.stringify([editor.value, subtitle.value, nfo.value, images.value])
const unsaved = computed(() => !!editor.value && signature() !== snapshot)
const changedAccount = (m: Mapping) => !!state.value && m.accountId !== state.value.primaryAccountId
const request = <T,>(path = '', init: RequestInit = {}) => p115Request<T>(props.token, `drive-strm${path}`, { ...init, signal: lifecycle.signal })
function refresh(): Promise<void> {
  if (pendingRefresh) return pendingRefresh
  if (document.hidden || lifecycle.signal.aborted) return Promise.resolve()
  pendingRefresh = (async () => {
    while (!lifecycle.signal.aborted) {
      const version = generation
      try { const next = await request<State>(); if (version === generation) { state.value = next; error.value = ''; return } }
      catch (e) { if (version === generation) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取配置失败'; return } }
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
  try { await fn() } catch (e) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = ''; poll() }
}
function edit(m?: Mapping) {
  editor.value = m ? { ...m, stats: { ...m.stats } } : { id: '', name: '', accountId: state.value?.primaryAccountId || '', sourceCID: '', sourcePath: '', outputDir: '', baseUrl: state.value?.defaults.baseUrl || window.location.origin, enabled: true, videoExtensions: state.value?.defaults.videoExtensions || '', metadataExtensions: '', minSizeMB: 0, status: 'idle', queued: false, stats: { videos: 0, written: 0, unchanged: 0, metadataWritten: 0, failed: 0 } }
  const extensions = editor.value.metadataExtensions.split(',')
  subtitle.value = extensions.some(e => ['srt', 'ass', 'ssa', 'sub', 'idx', 'vtt', 'sup'].includes(e))
  nfo.value = extensions.includes('nfo'); images.value = extensions.some(e => ['jpg', 'jpeg', 'png', 'webp'].includes(e))
  snapshot = signature(); dialogError.value = ''
}
function closeEditor() { if (!busy.value && (!unsaved.value || window.confirm('放弃未保存的修改？'))) editor.value = undefined }
async function selectSource() {
  await act('source', async () => {
    const data = await p115Request<{ accounts: { id: string; primary: boolean; configured: boolean }[] }>(props.token, 'accounts', { signal: lifecycle.signal })
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
  await act('save', async () => {
    const m = editor.value!
    const metadataExtensions = [...(subtitle.value ? ['srt', 'ass', 'ssa', 'sub', 'idx', 'vtt', 'sup'] : []), ...(nfo.value ? ['nfo'] : []), ...(images.value ? ['jpg', 'jpeg', 'png', 'webp'] : [])].join(',')
    await request(m.id ? `/${m.id}` : '', { method: m.id ? 'PUT' : 'POST', body: JSON.stringify({ name: m.name, accountId: m.accountId, sourceCID: m.sourceCID, sourcePath: m.sourcePath, outputDir: m.outputDir, baseUrl: m.baseUrl, enabled: m.enabled, videoExtensions: m.videoExtensions, metadataExtensions, minSizeMB: m.minSizeMB }) })
    generation++; editor.value = undefined; notice.value = '映射已保存，首次扫描将补齐本地文件'; await refresh()
  }, true)
}
async function run(m: Mapping, full: boolean) {
  await act(m.id, async () => { await request(`/${m.id}/${full ? 'scan' : 'check'}`, { method: 'POST' }); generation++; notice.value = full ? '已安排完整扫描，将补齐本地缺少的文件' : '已检查最新变更，有更新时将生成文件'; await refresh() })
}
async function toggle(m: Mapping, enabled: boolean) {
  await act(m.id, async () => { await request(`/${m.id}/monitor`, { method: 'PUT', body: JSON.stringify({ enabled }) }); generation++; notice.value = enabled ? '自动监控已开启' : '自动监控已暂停，当前扫描会继续完成'; await refresh() })
}
async function stop(m: Mapping) {
  await act(m.id, async () => { await request(`/${m.id}/stop`, { method: 'POST' }); generation++; notice.value = '已请求停止，可稍后完整扫描补齐'; await refresh() })
}
async function remove() {
  await act('delete', async () => { await request(`/${deleting.value!.id}`, { method: 'DELETE' }); generation++; deleting.value = undefined; notice.value = '映射已删除，本地与网盘文件均已保留'; await refresh(); await nextTick(); addButton.value?.focus() }, true)
}
function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未检查' }
function status(m: Mapping) { return m.status === 'running' ? (m.mode === 'changes' ? '正在检查变更' : '正在完整扫描') : m.status === 'stopped' ? '已停止' : m.lastError ? '需检查' : m.queued ? '等待扫描' : m.enabled ? '监控中' : '已暂停' }
async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
function beforeUnload(e: BeforeUnloadEvent) { if (unsaved.value || busy.value) { e.preventDefault(); e.returnValue = '' } }
onBeforeRouteLeave(() => {
  if (busy.value === 'save' || busy.value === 'delete') { dialogError.value = '正在保存当前操作，请完成后再切换页面'; return false }
  return !unsaved.value || window.confirm('放弃未保存的修改？')
})
onMounted(async () => { document.title = '目录 STRM · Cinema115'; document.addEventListener('visibilitychange', visible); window.addEventListener('beforeunload', beforeUnload); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout title="目录 STRM" description="从主网盘生成本地 STRM，保留原目录层级。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions><button ref="addButton" class="ds-button ds-primary" :disabled="loading || !!busy" @click="edit()"><IconPlus :size="17" aria-hidden="true" />新增映射</button></template>
    <p v-if="error" role="alert" class="ds-alert">{{ error }} <button class="underline" @click="refresh">重新加载</button></p>
    <p v-if="notice" role="status" class="ds-notice">{{ notice }}</p>
    <p v-if="state && !state.accountReady" class="ds-alert">请先在 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号。</p>
    <p v-if="state?.events?.lastError" role="status" class="ds-alert">事件检查暂不可用：{{ state.events.lastError }}。仍可完整扫描补齐。</p>
    <div v-if="loading" class="ds-empty" role="status">正在读取映射…</div>
    <div v-else-if="!state?.mappings.length" class="ds-empty"><IconFolder :size="30" aria-hidden="true" /><p>选择云端来源与本地输出目录，开始生成 STRM。</p><button class="ds-button" @click="edit()">添加第一个映射</button></div>
    <section v-else class="ds-grid" aria-label="STRM 目录映射">
      <article v-for="m in state.mappings" :key="m.id" class="ds-card ambient-glow" :aria-label="m.name">
        <header class="ds-heading"><span class="ds-symbol"><IconCloud :size="21" aria-hidden="true" /></span><div class="ds-name"><h2 :title="m.name">{{ m.name }}</h2><span class="ds-status" aria-live="polite" :class="{ running: active(m), attention: !!m.lastError }"><IconLoader2 v-if="active(m)" class="ds-spin" :size="12" aria-hidden="true" />{{ status(m) }}</span></div><button class="ds-icon" :disabled="!!busy || active(m)" :aria-label="`编辑${m.name}`" @click="edit(m)"><IconEdit :size="17" aria-hidden="true" /></button><button class="ds-icon" :disabled="!!busy || active(m)" :aria-label="`删除${m.name}`" @click="deleting = m; dialogError = ''"><IconTrash :size="17" aria-hidden="true" /></button></header>
        <dl class="ds-paths"><div><dt><IconCloud :size="15" aria-hidden="true" />来源</dt><dd :title="m.sourcePath">{{ m.sourcePath || m.sourceCID }}</dd></div><div><dt><IconFolderCheck :size="15" aria-hidden="true" />输出</dt><dd :title="strmDirectoryPath(m.outputDir)">{{ strmDirectoryPath(m.outputDir) }}</dd></div></dl>
        <div class="ds-counts"><span>本次视频 <b>{{ m.stats.videos }}</b></span><span>写入 {{ m.stats.written }}</span><span>未变 {{ m.stats.unchanged }}</span><span v-if="m.stats.metadataWritten">附件 {{ m.stats.metadataWritten }}</span><span v-if="m.stats.failed" class="ds-failed">失败 {{ m.stats.failed }}</span></div>
        <p v-if="changedAccount(m)" class="ds-failed ds-message">主账号已变化，请重新选择来源目录。</p><p v-else-if="m.lastError" class="ds-failed ds-message">{{ m.lastError }}</p>
        <div class="ds-meta"><span>最近扫描 {{ date(m.lastScanAt) }}</span><div><button v-if="active(m)" :disabled="!!busy" :aria-label="`停止${m.name}本次扫描`" @click="stop(m)">停止本次</button><button :disabled="!!busy || changedAccount(m)" :aria-label="`${m.enabled ? '暂停' : '开启'}${m.name}监控`" @click="toggle(m, !m.enabled)">{{ m.enabled ? '暂停监控' : '开启监控' }}</button></div></div>
        <footer class="ds-actions"><button class="ds-button ds-check" :disabled="!!busy || active(m) || changedAccount(m) || !state.accountReady || !m.enabled" :title="!m.enabled ? '开启监控后检查事件更新；也可直接完整扫描' : undefined" @click="run(m, false)"><IconRefresh :size="16" aria-hidden="true" />{{ active(m) ? '检查中…' : '检查更新' }}</button><button class="ds-button" :disabled="!!busy || active(m) || changedAccount(m) || !state.accountReady" @click="run(m, true)"><IconSearch :size="16" aria-hidden="true" />完整扫描补齐</button></footer>
      </article>
    </section>
    <p class="ds-footnote">开启监控后按生活事件检查变更，每天完整扫描补漏。云端改名或移动后会保留旧本地文件；移走或删除本地文件后，可完整扫描补齐。播放需通过 Cinema115 的 Emby 反代。</p>
    <P115ActionDialog v-if="editor" :title="editor.id ? '编辑 STRM 映射' : '新增 STRM 映射'" :busy="!!busy" @close="closeEditor">
      <form id="drive-strm-form" class="ds-form" @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p>
        <small v-if="editor.id">更换来源或输出目录会保留原有文件，并重新完整扫描。</small>
        <p v-if="changedAccount(editor)" role="alert" class="ds-failed">主账号已变化，请重新选择来源目录。</p>
        <label>名称<input v-model="editor.name" required maxlength="60" placeholder="例如：动漫媒体库" :disabled="!!busy" /></label>
        <div><span class="ds-label">主网盘来源</span><button type="button" class="ds-directory" :disabled="!!busy" @click="selectSource"><IconCloud :size="17" aria-hidden="true" /><span>{{ editor.sourcePath || '选择云端目录' }}</span><span class="ds-link">选择</span></button></div>
        <div><span class="ds-label">本地输出</span><div class="ds-output"><label class="ds-sr" for="ds-output">本地输出相对目录</label><input id="ds-output" v-model="editor.outputDir" required placeholder="例如：动漫（相对 /strm）" :disabled="!!busy" /><button type="button" class="ds-button" :disabled="!!busy" @click="outputDialog = true">选择</button></div><small>{{ strmDirectoryPath(editor.outputDir) }} · 可填写新的子目录</small></div>
        <label>STRM 服务地址<input v-model="editor.baseUrl" type="url" required placeholder="http://192.168.0.24:8080" :disabled="!!busy" /><small>填写 Cinema115 地址，不含路径；STRM 不保存登录凭据。</small></label>
        <SettingSwitch v-model="editor.enabled" label="自动监控" description="事件发现变更，每天完整扫描补漏。保存后先生成一次。" :disabled="!!busy" />
        <details class="ds-options"><summary>附加文件<span>字幕、NFO、图片</span></summary><div><SettingSwitch v-model="subtitle" label="下载外挂字幕" :disabled="!!busy" /><SettingSwitch v-model="nfo" label="下载 NFO" :disabled="!!busy" /><SettingSwitch v-model="images" label="下载海报与剧照" :disabled="!!busy" /><small>按原名保存，单个附件最大 32 MB。附件失败可重试，不阻止其余 STRM 生成。</small></div></details>
      </form>
      <template #footer><button class="ds-button" :disabled="!!busy" @click="closeEditor">取消</button><button type="submit" form="drive-strm-form" class="ds-button ds-primary" :disabled="!!busy || !editor.sourceCID || !editor.outputDir || changedAccount(editor)">{{ busy === 'save' ? '保存中…' : '保存映射' }}</button></template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="sourceDialog && editor" :token="token" :account-id="editor.accountId" :allow-root="false" title="选择主网盘来源目录" @close="sourceDialog = false" @select="pickedSource" />
    <STRMDirectoryDialog v-if="outputDialog" :token="token" title="选择本地输出目录" @close="outputDialog = false" @select="path => { if (editor) editor.outputDir = path; outputDialog = false }" />
    <P115ActionDialog v-if="deleting" title="删除 STRM 映射" :busy="!!busy" @close="deleting = undefined"><p>删除「{{ deleting.name }}」的配置与索引？已生成的本地文件和网盘文件都会保留。</p><p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p><template #footer><button class="ds-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button class="ds-button ds-danger" :disabled="!!busy" @click="remove">{{ busy ? '删除中…' : '删除配置' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ds-grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(min(100%,340px),1fr)); gap:14px; }
.ds-card { @apply rounded-2xl bg-white p-4 text-slate-700 shadow-sm dark:bg-slate-900 dark:text-slate-200; min-width:0; }
.ds-heading { display:flex; align-items:center; gap:8px; margin-bottom:16px; }.ds-symbol { @apply bg-blue-50 text-blue-600 dark:bg-blue-950/50 dark:text-blue-400; display:grid; place-items:center; width:36px; height:36px; border-radius:11px; flex-shrink:0; }.ds-name { flex:1; min-width:0; }.ds-name h2 { font-size:15px; font-weight:600; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.ds-status { @apply text-slate-500 dark:text-slate-400; display:inline-flex; gap:5px; align-items:center; font-size:12px; margin-top:3px; }.ds-status.running { @apply text-blue-600 dark:text-blue-400; }.ds-status.attention { @apply text-amber-700 dark:text-amber-400; }
.ds-icon { @apply text-slate-400 hover:bg-slate-100 hover:text-blue-600 dark:hover:bg-slate-800; display:grid; place-items:center; width:32px; height:36px; border-radius:8px; flex-shrink:0; }
.ds-paths { display:grid; gap:8px; font-size:13px; }.ds-paths>div { display:flex; align-items:baseline; gap:12px; }.ds-paths dt { @apply text-slate-500 dark:text-slate-400; display:flex; align-items:center; gap:6px; flex-shrink:0; }.ds-paths dd { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ds-counts { @apply bg-slate-50 dark:bg-slate-800/60; display:flex; flex-wrap:wrap; gap:5px 13px; border-radius:9px; padding:9px 10px; font-size:12px; margin-top:14px; }.ds-counts b { font-size:14px; font-variant-numeric:tabular-nums; }
.ds-meta { @apply text-slate-500 dark:text-slate-400; display:flex; flex-wrap:wrap; justify-content:space-between; align-items:center; gap:5px; font-size:12px; margin:10px 0; }.ds-meta button { @apply text-blue-600 dark:text-blue-400; padding:5px 0 5px 8px; }.ds-message { font-size:12px; line-height:1.6; margin-top:8px; overflow-wrap:anywhere; }.ds-failed { @apply text-red-600 dark:text-red-400; }
.ds-actions { display:grid; grid-template-columns:1fr 1fr; gap:8px; }.ds-button { @apply bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700; display:inline-flex; align-items:center; justify-content:center; gap:6px; border-radius:9px; min-height:36px; padding:7px 11px; font-size:13px; font-weight:500; }.ds-primary { @apply bg-blue-600 text-white hover:bg-blue-700 dark:bg-blue-600 dark:text-white dark:hover:bg-blue-500; }.ds-check { @apply bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60; }.ds-danger { @apply bg-red-600 text-white hover:bg-red-700 dark:bg-red-600 dark:text-white; }button:disabled { cursor:not-allowed; opacity:.45; }button:focus-visible,input:focus-visible,summary:focus-visible { outline:2px solid #3b82f6; outline-offset:3px; }
.ds-alert { @apply rounded-xl bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300; padding:12px; margin-bottom:12px; font-size:13px; overflow-wrap:anywhere; }.ds-notice { @apply text-blue-700 dark:text-blue-300; margin-bottom:12px; font-size:13px; }.ds-empty { @apply rounded-2xl bg-white text-slate-500 dark:bg-slate-900 dark:text-slate-400; display:flex; align-items:center; justify-content:center; flex-wrap:wrap; gap:12px; padding:28px 18px; font-size:14px; }.ds-footnote { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.8; margin-top:18px; }
.ds-form { display:grid; gap:15px; }.ds-form label { display:grid; gap:6px; font-size:13px; }.ds-form input { @apply border border-slate-200 bg-white text-slate-800 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100; border-radius:8px; min-width:0; width:100%; height:36px; padding:0 10px; font-size:13px; }.ds-form small { @apply text-slate-500 dark:text-slate-400; display:block; font-size:12px; line-height:1.6; margin-top:4px; }.ds-label { font-size:13px; display:block; margin-bottom:6px; }.ds-directory { @apply bg-slate-50 dark:bg-slate-800; display:flex; align-items:center; gap:8px; width:100%; padding:9px 10px; border-radius:8px; text-align:left; font-size:13px; }.ds-directory>span:first-of-type { min-width:0; flex:1; overflow-wrap:anywhere; }.ds-link { @apply text-blue-600 dark:text-blue-400; font-size:12px; flex-shrink:0; }.ds-output { display:flex; gap:8px; }.ds-output input { flex:1; }.ds-options summary { font-size:13px; cursor:pointer; }.ds-options summary span { @apply text-slate-400; font-size:12px; margin-left:8px; }.ds-options>div { display:grid; gap:12px; padding-top:12px; }.ds-sr { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }.ds-spin { animation:ds-spin 1s linear infinite; }@keyframes ds-spin { to { transform:rotate(360deg) } }
@media(max-width:639px) { .ds-button { min-height:44px; padding-inline:8px; white-space:nowrap; }.ds-directory { min-height:44px; }.ds-icon { width:40px; height:40px; }.ds-meta button { min-height:40px; padding-inline:8px; }.ds-form input { height:44px; font-size:16px; } }
@media(max-width:359px) { .ds-actions { grid-template-columns:1fr; } }
@media(prefers-reduced-motion:reduce) { .ds-spin { animation:none; } }
</style>
