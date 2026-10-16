<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { copyText } from '../utils/clipboard'
import { embyRequest, type EmbyConfig, type EmbyStatus } from '../types/emby'
import { invalidateEmbyMissingCache, observeEmbyMissingSync } from '../composables/useEmbyMissingSession'
import '../styles/emby-library.css'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ home: []; openDashboard: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
interface Library { ItemId: string; Name: string; CollectionType: string }
interface LibraryResult { libraries: Library[]; server: { Id: string; ServerName: string; Version: string } }
const config = ref<EmbyConfig>()
const status = ref<EmbyStatus>()
const url = ref('http://192.168.0.3:8096'), apiKey = ref(''), selected = ref<string[]>([])
const libraries = ref<Library[]>([]), libraryIdentity = ref(''), query = ref('')
const libraryLoading = ref(false), loading = ref(true), busy = ref(''), refreshing = ref(false)
const loadError = ref(''), libraryError = ref(''), pollingError = ref('')
const connectionError = ref(''), connectionNotice = ref(''), syncError = ref(''), syncNotice = ref('')
const webhookError = ref(''), webhookNotice = ref(''), connectionOpen = ref(false), revealWebhook = ref(false)
const syncAccepted = ref(false)
const connectionForm = ref<HTMLFormElement>(), webhookForm = ref<HTMLFormElement>()
const metadataDetails = ref<HTMLDetailsElement>(), webhookDetails = ref<HTMLDetailsElement>()
const lifecycle = new AbortController()
let timer: number | undefined
let statusRequest: Promise<void> | undefined
const logsRoute = { path: '/logs', query: { module: 'Emby', level: 'ALL' } }
const webhookBase = ref(window.location.port === '5173' ? `${window.location.protocol}//${window.location.hostname}:3002` : window.location.origin)
function normalizeBase(value: string) { return value.trim().replace(/\/+$/, '') }
const identity = computed(() => `${normalizeBase(url.value)}\n${apiKey.value.trim()}`)
const urlChanged = computed(() => normalizeBase(url.value) !== config.value?.url)
const connectionDirty = computed(() => !!apiKey.value.trim() || urlChanged.value)
const scopeDirty = computed(() => [...selected.value].sort().join(',') !== [...(config.value?.libraryIds || [])].sort().join(','))
const dirty = computed(() => connectionDirty.value || scopeDirty.value)
const librariesCurrent = computed(() => !!libraryIdentity.value && libraryIdentity.value === identity.value)
const missingLibraries = computed(() => librariesCurrent.value ? selected.value.filter(id => !libraries.value.some(library => library.ItemId === id)) : [])
const filteredLibraries = computed(() => {
  const search = query.value.trim().toLocaleLowerCase()
  return libraries.value.filter(library => !search || `${library.Name} ${library.CollectionType}`.toLocaleLowerCase().includes(search))
})
const locked = computed(() => loading.value || !!busy.value || !config.value)
const connectionLocked = computed(() => locked.value || libraryLoading.value)
const canSave = computed(() => !connectionLocked.value && dirty.value && selected.value.length > 0 && !missingLibraries.value.length && (!urlChanged.value || librariesCurrent.value))
// Copy the persisted address, never an unsaved preview. Connection saves leave the Webhook draft alone.
const webhookURL = computed(() => config.value?.webhookBaseUrl && config.value.webhookPath ? `${config.value.webhookBaseUrl}${config.value.webhookPath}` : '')
const webhookDirty = computed(() => normalizeBase(webhookBase.value) !== (config.value?.webhookBaseUrl || ''))
const webhookLoopback = computed(() => {
  try { return ['localhost', '127.0.0.1', '[::1]', '0.0.0.0', '[::]'].includes(new URL(webhookBase.value).hostname) }
  catch { return false }
})
const syncing = computed(() => syncAccepted.value || ['queued', 'running'].includes(status.value?.status || ''))
const metadataTotal = computed(() => (status.value?.metadataPending || 0) + (status.value?.metadataFailed || 0))
const notificationPending = computed(() => Math.max(0, (status.value?.pending || 0) - (status.value?.metadataPending || 0)))
const notificationFailed = computed(() => Math.max(0, (status.value?.failed || 0) - (status.value?.metadataFailed || 0)))
const stateLabel = computed(() => syncAccepted.value ? '等待开始' : status.value ? ({ queued: '等待开始', running: '同步中', failed: '同步失败', idle: status.value.syncedAt ? '已建立索引' : '等待首次同步' }[status.value.status] || '状态待确认') : '读取中')
const connectionLabel = computed(() => !config.value ? loadError.value ? '配置待重试' : '读取中' : !status.value ? '读取中' : pollingError.value ? '状态待刷新' : !config.value.configured ? '未配置' : status.value.connected ? '连接正常' : '连接异常')
const updatesLabel = computed(() => status.value ? ({ ready: '已就绪', building: '正在构建', failed: '构建失败', waiting: '等待全量同步' }[status.value.updatesStatus] || '状态待确认') : '读取中')
const syncDisabledReason = computed(() => !config.value?.configured ? '先连接 Emby 并保存同步范围。' : dirty.value ? '连接或同步范围有修改，请先保存。' : syncing.value ? '任务在后台运行，离开页面也会继续。' : '首次使用或调整范围后执行，日常通过 Webhook 增量更新。')
watch(connectionDirty, changed => { if (changed) connectionOpen.value = true })
watch(identity, () => { connectionNotice.value = ''; connectionError.value = ''; libraryError.value = '' }, { flush: 'sync' })
function message(reason: unknown, fallback: string) { return reason instanceof Error ? reason.message : fallback }
function time(value?: string) { return value ? new Date(value).toLocaleString() : '尚无记录' }
function request(path: string, body?: object) { return embyRequest(props.token, path, lifecycle.signal, body) }
function selectVisible() { selected.value = [...new Set([...selected.value, ...filteredLibraries.value.map(library => library.ItemId)])] }
function clearVisible() {
  if (!query.value.trim()) { selected.value = []; return }
  const visible = new Set(filteredLibraries.value.map(library => library.ItemId))
  selected.value = selected.value.filter(id => !visible.has(id))
}

async function refresh() {
  if (statusRequest) return statusRequest
  refreshing.value = true
  const requestAfterAcceptance = syncAccepted.value
  statusRequest = (async () => {
    try {
      const data: EmbyStatus = await request('status')
      if (!lifecycle.signal.aborted) {
        observeEmbyMissingSync(data.syncedAt || '')
        status.value = data; pollingError.value = ''
        if (requestAfterAcceptance) syncAccepted.value = false
      }
    }
    catch (reason) { if (!lifecycle.signal.aborted) pollingError.value = message(reason, '状态读取失败') }
    finally { refreshing.value = false; statusRequest = undefined }
  })()
  return statusRequest
}
function schedulePoll() {
  window.clearTimeout(timer)
  if (lifecycle.signal.aborted || document.hidden) return
  timer = window.setTimeout(async () => { await refresh(); schedulePoll() }, syncing.value ? 4000 : 15000)
}
function visibilityChanged() {
  window.clearTimeout(timer)
  if (!document.hidden && !lifecycle.signal.aborted) void refresh().then(schedulePoll)
}
async function refreshNow() { await refresh(); schedulePoll() }
async function retryInitialLoad() { await Promise.all([loadConfig(), refresh()]); schedulePoll() }
async function readLibraries(manual = false) {
  if (libraryLoading.value || !config.value || lifecycle.signal.aborted) return
  libraryLoading.value = true; libraryError.value = ''
  const requestedIdentity = identity.value
  try {
    const data: LibraryResult = await request('test', { url: url.value.trim(), apiKey: apiKey.value.trim() })
    if (lifecycle.signal.aborted || requestedIdentity !== identity.value) return
    libraries.value = data.libraries; libraryIdentity.value = requestedIdentity
    if (manual) connectionNotice.value = `连接成功：${data.server.ServerName}，已读取 ${data.libraries.length} 个媒体库。`
    // Retain missing selections until the administrator explicitly removes them.
  } catch (reason) {
    if (!lifecycle.signal.aborted && requestedIdentity === identity.value) { libraryIdentity.value = ''; libraryError.value = message(reason, '媒体库读取失败') }
  } finally { libraryLoading.value = false }
}
async function loadConfig() {
  if (config.value || lifecycle.signal.aborted) return
  loading.value = true; loadError.value = ''
  try {
    const data: EmbyConfig = await request('config')
    if (lifecycle.signal.aborted) return
    config.value = data; url.value = data.url; selected.value = [...data.libraryIds]
    webhookBase.value = data.webhookBaseUrl || webhookBase.value; connectionOpen.value = !data.configured
    if (data.configured) void readLibraries()
    if (!data.webhookBaseUrl && webhookDetails.value) webhookDetails.value.open = true
  } catch (reason) { if (!lifecycle.signal.aborted) loadError.value = message(reason, '配置加载失败') }
  finally { loading.value = false }
}
async function testConnection() {
  if (connectionLocked.value) return
  if (connectionDirty.value || !config.value?.configured) { connectionOpen.value = true; await nextTick() }
  if (!connectionForm.value?.reportValidity()) return
  busy.value = 'test'; connectionNotice.value = ''; connectionError.value = ''
  try { await readLibraries(true) } finally { busy.value = '' }
}
async function saveConnection() {
  if (!canSave.value) return
  if (connectionDirty.value || !config.value?.configured) { connectionOpen.value = true; await nextTick() }
  if (!connectionForm.value?.reportValidity()) return
  busy.value = 'save'; connectionError.value = ''; connectionNotice.value = ''
  try {
    // The backend validates the credentials and every selected library again on save.
    const data: EmbyConfig = await request('config', { url: url.value.trim(), apiKey: apiKey.value.trim(), libraryIds: [...selected.value] })
    invalidateEmbyMissingCache()
    if (lifecycle.signal.aborted) return
    const retainLibraries = librariesCurrent.value
    config.value = data; url.value = data.url; apiKey.value = ''; selected.value = [...data.libraryIds]
    if (retainLibraries) libraryIdentity.value = identity.value
    connectionNotice.value = '连接与范围已保存。首次使用或调整范围后，请执行全量同步。'
    if (!retainLibraries) void readLibraries()
    await refreshNow()
  } catch (reason) { if (!lifecycle.signal.aborted) connectionError.value = message(reason, '配置保存失败') }
  finally { busy.value = '' }
}
async function resetConnection() {
  if (connectionLocked.value || !config.value) return
  url.value = config.value.url; apiKey.value = ''; selected.value = [...config.value.libraryIds]
  connectionError.value = ''; connectionNotice.value = ''; libraryError.value = ''
  if (config.value.configured && !librariesCurrent.value) await readLibraries()
}
async function startSync() {
  if (locked.value || !config.value?.configured || syncing.value || dirty.value) return
  busy.value = 'sync'; syncError.value = ''; syncNotice.value = ''
  try {
    await request('sync', {})
    invalidateEmbyMissingCache()
    if (lifecycle.signal.aborted) return
    syncAccepted.value = true
    // An in-flight poll may describe the state before the task was accepted.
    if (statusRequest) await statusRequest
    if (lifecycle.signal.aborted) return
    if (status.value) status.value = { ...status.value, status: 'queued' }
    syncNotice.value = '已加入后台队列，离开页面也会继续。'
    await refreshNow()
  } catch (reason) { if (!lifecycle.signal.aborted) syncError.value = message(reason, '同步启动失败') }
  finally { busy.value = '' }
}
async function saveWebhook() {
  if (locked.value || !webhookDirty.value || !webhookForm.value?.reportValidity()) return
  busy.value = 'save-webhook'; webhookError.value = ''; webhookNotice.value = ''
  try {
    const data: EmbyConfig = await request('webhook-config', { baseUrl: webhookBase.value.trim() })
    if (lifecycle.signal.aborted || !config.value) return
    config.value = { ...config.value, webhookBaseUrl: data.webhookBaseUrl, webhookPath: data.webhookPath }
    webhookBase.value = data.webhookBaseUrl
    webhookNotice.value = '地址已保存。请复制完整地址，并更新 Emby 中的 Webhook 配置。'
  } catch (reason) { if (!lifecycle.signal.aborted) webhookError.value = message(reason, '地址保存失败') }
  finally { busy.value = '' }
}
async function copyWebhook() {
  if (!webhookURL.value) return
  webhookNotice.value = ''; webhookError.value = ''
  try { await copyText(webhookURL.value); if (!lifecycle.signal.aborted) webhookNotice.value = '已复制当前保存的 Webhook 地址。' }
  catch { if (!lifecycle.signal.aborted) webhookError.value = '复制失败，请显示地址后选中并手动复制。' }
}
async function showMetadata() {
  if (!metadataDetails.value) return
  metadataDetails.value.open = true; await nextTick()
  metadataDetails.value.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'center' })
  metadataDetails.value.querySelector('summary')?.focus({ preventScroll: true })
}
onMounted(async () => { document.addEventListener('visibilitychange', visibilityChanged); await Promise.all([loadConfig(), refresh()]); schedulePoll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<template>
  <ManagementLayout class="library-page" title="媒体库同步" description="管理 Emby 同步范围，查看索引与增量更新状态。" :parent="{ label: 'Emby', to: '/emby' }" @logout="emit('logout')">
    <template #actions><RouterLink :to="logsRoute" class="library-tool library-header-link">查看日志</RouterLink></template>
    <div class="library-ui">
      <div v-if="loadError" role="alert" class="library-alert is-error library-load-error"><span>{{ loadError }}</span><button type="button" class="library-tool" :disabled="loading" @click="retryInitialLoad">重试读取</button></div>
      <section class="library-overview" aria-labelledby="library-overview-title" :aria-busy="refreshing">
        <div class="library-section-heading"><h2 id="library-overview-title">运行概况</h2><button type="button" class="library-icon-button" :disabled="refreshing" aria-label="刷新运行状态" @click="refreshNow"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7A7 7 0 0 1 18 5l2 2M4 17l2 2a7 7 0 0 0 11.9-2"/></svg></button></div>
        <dl class="library-metrics">
          <div><dt>Emby 连接</dt><dd><span class="library-state" :class="{ 'is-success': config?.configured && status?.connected && !pollingError, 'is-error': status && config?.configured && !status.connected && !pollingError }">{{ connectionLabel }}</span></dd></div>
          <div><dt>索引条目</dt><dd class="library-metric-value">{{ status ? status.count.toLocaleString() : '读取中' }}</dd></div>
          <div><dt>已保存范围</dt><dd class="library-metric-value">{{ config ? `${config.libraryIds.length} 个媒体库` : '读取中' }}</dd></div>
          <div><dt>最近全量完成</dt><dd class="library-metric-time">{{ status ? time(status.syncedAt) : '读取中' }}</dd></div>
        </dl>
        <div v-if="metadataTotal || pollingError || status?.connectionError" class="library-overview-notes">
          <button v-if="metadataTotal" type="button" class="library-warning-link" @click="showMetadata"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v4m0 4h.01"/></svg>{{ metadataTotal }} 个条目元数据待补全</button>
          <p v-if="pollingError" role="alert" class="library-help library-error-text">状态刷新失败，当前显示上次结果：{{ pollingError }}</p>
          <p v-if="status?.connectionError" class="library-help library-error-text">{{ status.connectionError }}</p>
        </div>
      </section>

      <div class="library-grid">
        <section class="library-card library-sync" aria-labelledby="library-sync-title">
          <div class="library-section-heading"><h2 id="library-sync-title">媒体索引</h2><span class="library-state" :class="{ 'is-accent': syncing, 'is-error': status?.status === 'failed' }">{{ stateLabel }}</span></div>
          <div class="library-task"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/></svg><div><strong>{{ syncing ? syncAccepted || status?.status === 'queued' ? '等待后台开始' : `已读取 ${(status?.scanned || 0).toLocaleString()} 条` : !status ? '正在读取索引状态' : status.syncedAt ? '完整索引已建立' : '等待首次全量同步' }}</strong><p class="library-help">{{ syncing ? '正在同步所选媒体库' : '入库状态由完整索引与已处理通知共同更新。' }}</p></div></div>
          <p v-if="status?.lastError || syncError" role="alert" class="library-alert is-error">{{ syncError || status?.lastError }}</p>
          <details v-if="metadataTotal || status?.eventError" ref="metadataDetails" class="library-details library-metadata"><summary><span>{{ metadataTotal ? `${metadataTotal} 个条目元数据待补全` : '通知处理提示' }}</span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 9 6 6 6-6"/></svg></summary><div><p v-if="metadataTotal">请在 Emby 补全 TMDB ID、季号或集号。其他条目仍按当前索引显示入库结果。</p><p v-if="status?.metadataFailed">其中 {{ status.metadataFailed }} 个已达补查上限。补全后通过媒体更新通知或全量同步重新识别。</p><p v-if="status?.eventError">最近处理提示：{{ status.eventError }}</p><RouterLink :to="logsRoute" class="library-text-button">查看处理日志</RouterLink></div></details>
          <div class="library-sync-actions"><button type="button" class="library-button" :disabled="locked || !config?.configured || syncing || dirty" @click="startSync">{{ busy === 'sync' ? '正在提交' : syncing ? '同步进行中' : '开始全量同步' }}</button><p class="library-help">{{ syncDisabledReason }}</p></div>
          <p v-if="syncNotice" role="status" class="library-alert is-success">{{ syncNotice }}</p>
        </section>

        <section class="library-card library-settings" aria-labelledby="library-settings-title">
          <div class="library-section-heading"><h2 id="library-settings-title">连接与同步范围</h2><span class="library-caption">{{ dirty && config ? '有未保存修改' : config?.configured ? '已配置' : '首次配置' }}</span></div>
          <form ref="connectionForm" @submit.prevent="saveConnection">
            <div class="library-connection-summary"><div><strong>{{ config?.serverName || 'Emby 服务器' }}</strong><p>{{ config?.configured ? config.url : '填写连接信息后读取媒体库' }}</p></div><button type="button" class="library-text-button" :disabled="connectionLocked" :aria-expanded="connectionOpen" :aria-controls="connectionOpen ? 'library-connection-fields' : undefined" @click="connectionOpen = !connectionOpen">{{ connectionOpen ? '收起' : '编辑连接' }}</button></div>
            <fieldset v-if="connectionOpen" id="library-connection-fields" class="library-connection-fields" :disabled="connectionLocked">
              <legend class="sr-only">Emby 连接信息</legend>
              <label>服务器地址<input v-model="url" type="url" required class="library-input" placeholder="http://192.168.0.3:8096" /></label>
              <label>API Key<input v-model="apiKey" type="password" :required="!config?.configured || urlChanged" autocomplete="new-password" class="library-input" :placeholder="config?.configured && !urlChanged ? '已保存，留空保留原密钥' : '填写此服务器的 API Key'" /></label>
              <div class="library-field-actions"><button type="button" class="library-tool" @click="testConnection">{{ busy === 'test' ? '正在测试' : '测试并读取' }}</button><p class="library-help">密钥加密保存，不回显。</p></div>
            </fieldset>
            <p v-if="connectionError" role="alert" class="library-alert is-error">{{ connectionError }}</p>
            <p v-if="connectionNotice" role="status" class="library-alert is-success">{{ connectionNotice }}</p>
            <div class="library-scope-heading"><h3>同步范围</h3><span class="library-caption">已选 {{ selected.length }} 个</span></div>
            <p class="library-help">仅用于影视索引，不改变 Emby 用户权限。</p>
            <div v-if="libraryError" role="alert" class="library-alert is-error"><p>{{ libraryError }}</p><button type="button" class="library-text-button" :disabled="connectionLocked" @click="testConnection">重新读取</button></div>
            <div v-if="libraryLoading" class="library-loading" role="status"><p class="library-help">正在读取媒体库，已保存的选择会保留。</p><div class="library-skeleton-grid" aria-hidden="true"><div v-for="n in 6" :key="n" /></div></div>
            <template v-else-if="librariesCurrent">
              <div class="library-scope-tools"><label class="library-search"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg><input v-model="query" type="search" aria-label="搜索同步媒体库" placeholder="搜索媒体库" /></label><button type="button" class="library-text-button" :disabled="connectionLocked" @click="testConnection">刷新列表</button></div>
              <fieldset class="library-scope-fieldset" :disabled="connectionLocked"><legend class="sr-only">选择参与同步的媒体库</legend>
                <div class="library-selection-tools"><span class="library-caption">{{ query.trim() ? `找到 ${filteredLibraries.length} 个` : `共 ${libraries.length} 个媒体库` }}</span><div><button type="button" class="library-text-button" :disabled="!filteredLibraries.length" @click="selectVisible">{{ query.trim() ? '选择搜索结果' : '全选' }}</button><button type="button" class="library-text-button" :disabled="query.trim() ? !filteredLibraries.some(library => selected.includes(library.ItemId)) : !selected.length" @click="clearVisible">{{ query.trim() ? '清除搜索结果' : '清空' }}</button></div></div>
                <div class="library-options"><label v-for="library in filteredLibraries" :key="library.ItemId" class="library-option" :class="{ 'is-selected': selected.includes(library.ItemId) }"><input v-model="selected" type="checkbox" :value="library.ItemId" /><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><template v-if="library.CollectionType === 'movies'"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 4v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4"/></template><template v-else-if="library.CollectionType === 'tvshows'"><rect x="3" y="6" width="18" height="14" rx="2"/><path d="m8 2 4 4 4-4"/></template><template v-else-if="library.CollectionType === 'music'"><path d="M9 18V5l11-2v13M9 9l11-2"/><ellipse cx="6" cy="18" rx="3" ry="2"/><ellipse cx="17" cy="16" rx="3" ry="2"/></template><template v-else><path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3V7Z"/></template></svg><span>{{ library.Name }}</span></label></div>
                <p v-if="!filteredLibraries.length" class="library-empty">{{ libraries.length ? '没有匹配的媒体库，试试其他关键词。' : '此服务器尚未返回媒体库。' }}</p>
              </fieldset>
              <div v-if="missingLibraries.length" role="alert" class="library-alert is-warning"><p>{{ missingLibraries.length }} 个已选媒体库不在当前列表中，原选择已保留。请核对连接或明确移除后保存。</p><button type="button" class="library-text-button" :disabled="connectionLocked" @click="selected = selected.filter(id => !missingLibraries.includes(id))">移除失效选择</button></div>
            </template>
            <div v-else-if="!libraryError" class="library-empty"><p>{{ connectionDirty ? '连接参数已修改，请读取媒体库后确认同步范围。' : config?.configured ? '已保存的选择已保留，可重新读取媒体库。' : '先填写连接信息并读取媒体库，首次读取不会自动勾选。' }}</p><button v-if="config?.configured" type="button" class="library-text-button" :disabled="connectionLocked" @click="testConnection">读取媒体库</button></div>
            <div class="library-save-row"><button type="submit" class="library-button" :disabled="!canSave">{{ busy === 'save' ? '正在保存' : connectionDirty ? '验证并保存' : '保存连接与范围' }}</button><button v-if="dirty && config" type="button" class="library-text-button" :disabled="connectionLocked" @click="resetConnection">撤销修改</button><span v-else class="library-caption">{{ loading ? '读取配置中' : config?.configured ? '当前配置已保存' : '至少选择一个媒体库' }}</span></div>
            <p v-if="urlChanged && !librariesCurrent && config?.configured" class="library-help library-save-help">更换服务器后，请先读取并确认同步范围。</p>
            <p v-else-if="!selected.length && librariesCurrent" class="library-help library-save-help">至少选择一个媒体库才能保存。</p>
          </form>
        </section>

        <section class="library-card library-webhook" aria-labelledby="library-webhook-title">
          <div class="library-section-heading"><h2 id="library-webhook-title">Webhook 增量更新</h2><span class="library-state" :class="{ 'is-warning': notificationFailed || status?.webhookError }">{{ !status ? '读取中' : notificationFailed || status.webhookError ? '需检查' : status.webhookAt ? '收到过通知' : '等待通知' }}</span></div>
          <dl class="library-webhook-metrics"><div><dt>待处理通知</dt><dd>{{ status ? notificationPending : '读取中' }}<span v-if="notificationFailed" class="library-error-text">失败 {{ notificationFailed }}</span></dd></div><div><dt>最近更新列表</dt><dd>{{ updatesLabel }}</dd></div></dl>
          <p class="library-help library-last-event">最近通知：{{ status ? time(status.webhookAt) : '读取中' }}<span v-if="status?.webhookEvent">{{ status.webhookEvent }}</span></p>
          <p v-if="status?.webhookError || notificationFailed || status?.updatesError" class="library-alert is-warning">{{ status?.webhookError || (notificationFailed ? '部分通知已达到重试上限，可执行全量同步重新核对索引。' : '') }}<span v-if="status?.updatesError">{{ status.updatesError }}</span></p>
          <template v-if="webhookURL"><label class="library-caption library-address-label" for="library-saved-webhook">已保存的 Webhook 地址</label><div class="library-address-row"><input id="library-saved-webhook" :value="webhookURL" :type="revealWebhook ? 'text' : 'password'" readonly aria-label="已保存的 Webhook 地址" class="library-input" @focus="($event.target as HTMLInputElement).select()" /><button type="button" class="library-icon-button" :aria-label="revealWebhook ? '隐藏 Webhook 地址' : '显示 Webhook 地址'" :aria-pressed="revealWebhook" @click="revealWebhook = !revealWebhook"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/><path v-if="revealWebhook" d="m3 3 18 18"/></svg></button><button type="button" class="library-tool" @click="copyWebhook">复制地址</button></div><p class="library-help library-address-help">地址包含密钥，仅用于 Emby 通知配置。</p></template>
          <p v-else class="library-empty">{{ config?.webhookPath ? '保存 Emby 能访问的基础地址后，即可复制完整地址。' : '保存连接配置后生成专用 Webhook 地址。' }}</p>
          <p v-if="webhookError" role="alert" class="library-alert is-error">{{ webhookError }}</p><p v-if="webhookNotice" role="status" class="library-alert is-success">{{ webhookNotice }}</p>
          <details ref="webhookDetails" class="library-details library-webhook-edit"><summary><span>地址设置<span v-if="webhookDirty" class="library-draft-dot">未保存</span></span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 9 6 6 6-6"/></svg></summary><form ref="webhookForm" @submit.prevent="saveWebhook"><label>Emby 能访问的 Cinema115 基础地址<input v-model="webhookBase" type="url" required :disabled="locked" placeholder="http://192.168.0.24:3002" class="library-input" /></label><p v-if="webhookLoopback" class="library-help library-warning-text">Emby 在另一台设备时，请使用 Cinema115 的局域网地址或可访问的域名。</p><p v-if="webhookDirty && webhookURL" class="library-help">上方复制的仍是已保存地址，请先保存此处修改。</p><div class="library-field-actions"><button type="submit" class="library-tool is-accent" :disabled="locked || !config?.webhookPath || !webhookDirty || !webhookBase.trim()">{{ busy === 'save-webhook' ? '正在保存' : '保存地址' }}</button><button v-if="webhookDirty && config?.webhookBaseUrl" type="button" class="library-text-button" :disabled="locked" @click="webhookBase = config!.webhookBaseUrl">撤销修改</button></div></form></details>
          <details class="library-details library-webhook-guide"><summary><span>如何在 Emby 配置通知</span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 9 6 6 6-6"/></svg></summary><ol><li>在 Emby 通知中添加 Webhook，粘贴已保存的完整地址。</li><li>选择“已添加新媒体”和“媒体已移除”，关闭按剧集分组；支持媒体更新事件时也一并启用。</li><li>使用 POST 和 JSON 格式。测试通知只验证连通性，媒体变更会等待刮削后延迟处理。</li></ol></details>
        </section>
      </div>
    </div>
  </ManagementLayout>
</template>
