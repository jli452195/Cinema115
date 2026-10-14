<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import ManagementLayout from '../components/ManagementLayout.vue'
import PlaybackChildren from '../components/PlaybackChildren.vue'
import PersonalDriveDisclosure from '../components/PersonalDriveDisclosure.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115SidePanel from '../components/P115SidePanel.vue'
import P115Icon from '../components/P115Icon.vue'
import { p115LoginClients } from '../lib/p115-login-clients'
import '../styles/personal-playback.css'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
interface DirectoryAccount { accountId: string; revision: number; uid: string; directoryId: string; creationUncertain: boolean }
interface Playback { accountId: string; revision: number; directoryAccount?: DirectoryAccount; needsDirectoryConfirmation: boolean; canPreparePersonal: boolean; pendingDrive: boolean; enabled: boolean; stage: string; message: string; uid: string; connected: boolean; directoryId: string; embyName: string; embyUrl: string; suspended: boolean; allowDownload: boolean; source: string }
type Panel = '' | 'bind' | 'directory' | 'manage' | 'privacy' | 'help' | 'connection' | 'bound' | 'switch' | 'disconnect'
const state = ref<Playback>(), error = ref(''), notice = ref(''), busy = ref(false), loading = ref(true)
const panel = ref<Panel>(''), driveHeading = ref<HTMLElement>()
const directoryId = ref(''), directoryPath = ref(''), selecting = ref(false), directorySnapshot = ref<DirectoryAccount>()
const manualDirectory = ref(false)
const uncertainDirectories = ref(new Set<string>())
const clientType = ref('alipay-miniapp'), consent = ref(false), disconnectConfirm = ref(false)
const qrImage = ref(''), qrID = ref(''), qrStatus = ref(''), expiresAt = ref(0)
const lifecycle = new AbortController()
let generation = 0, pollTimer: ReturnType<typeof setTimeout> | undefined, expiryTimer: ReturnType<typeof setTimeout> | undefined
let qrController: AbortController | undefined
const canManage = computed(() => !!state.value && (state.value.source === 'personal' || state.value.canPreparePersonal))
const driveReady = computed(() => !!state.value?.connected && !!state.value.uid && !!state.value.directoryId && !state.value.suspended)
const sourceLabel = computed(() => ({ primary: '管理员主网盘', assigned: '管理员动态池', personal: '我的 115 网盘' }[state.value?.source || ''] || '等待确认播放来源'))
const statusLabel = computed(() => {
  const s = state.value
  if (!s) return '正在读取状态'
  if (!s.enabled) return '直链服务暂未运行'
  if (s.suspended) return '个人网盘已解绑'
  if (s.source === 'personal' && !s.uid && !s.pendingDrive) return '等待绑定个人网盘'
  if (s.source === 'personal' && s.uid && !s.connected) return '个人网盘需要重新登录'
  if (s.source === 'personal' && s.uid && !s.directoryId) return '等待确认保存目录'
  if (s.source === 'assigned' && s.stage === 'ready') return '共享池自动调度'
  return ({ ready: '网盘最近检查通过', pending: '网盘检查需要处理', paused: '新的播放请求暂不可用', creating: '账号创建待确认', provisioning: '账号正在配置', unbound: '账号状态待确认', bound: '账号状态待确认' }[s.stage] || '等待状态确认')
})
const statusClass = computed(() => statusLabel.value === '网盘最近检查通过' ? 'pp-badge-green' : statusLabel.value === '共享池自动调度' ? 'pp-badge-blue' : 'pp-badge-amber')
const statusDetail = computed(() => {
  const s = state.value
  if (!s) return ''
  if (!s.enabled) return '仍可打开 Emby 浏览，并管理个人网盘。'
  if (s.message && s.message !== statusLabel.value) return s.message
  return s.source === 'assigned' ? '播放时检查并分配账号，无需自行选择。' : '这是最近一次网盘检查结果；播放以 Emby 授权和请求时状态为准。'
})
const safeEmbyURL = computed(() => { try { const url = new URL(state.value?.embyUrl || ''); return ['http:', 'https:'].includes(url.protocol) ? url.href : '' } catch { return '' } })
const panelTitle = computed(() => ({ bind: state.value?.uid ? '重新登录或更换网盘' : '绑定个人网盘', directory: state.value?.pendingDrive ? '确认待绑定网盘的目录' : '更换秒传保存目录', manage: '管理个人网盘', privacy: '个人网盘与隐私说明', help: '播放遇到问题', connection: 'Emby 连接信息', bound: '个人网盘绑定已完成', switch: '切换到我的网盘播放', disconnect: '解除个人网盘绑定' }[panel.value || 'help']))
const directoryCanCreate = computed(() => !!directorySnapshot.value && !uncertainDirectories.value.has(directorySnapshot.value.accountId) && !directorySnapshot.value.creationUncertain && !state.value?.directoryAccount?.creationUncertain)

async function request(path = '', body?: object, signal?: AbortSignal) {
  const response = await fetch(`/api/me/playback${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000), ...(signal ? [signal] : [])]) })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '个人播放服务暂时不可用')
  return data
}
function applyState(value: Playback) {
  const previous = state.value?.directoryAccount, next = value.directoryAccount
  if (next) {
    if (next.creationUncertain) uncertainDirectories.value.add(next.accountId)
    else uncertainDirectories.value.delete(next.accountId)
  }
  if (previous?.accountId !== next?.accountId || previous?.revision !== next?.revision) {
    directoryId.value = next?.directoryId || ''; directoryPath.value = ''; directorySnapshot.value = next ? { ...next } : undefined
  }
  state.value = value
}
async function load(clearError = true) {
  try { const value = await request(); if (lifecycle.signal.aborted) return false; applyState(value); if (clearError) error.value = ''; return true }
  catch (reason) { if (!lifecycle.signal.aborted && clearError) error.value = reason instanceof Error ? reason.message : '状态读取失败'; return false }
  finally { loading.value = false }
}
function stopQR() {
  generation++; qrController?.abort(); clearTimeout(pollTimer); clearTimeout(expiryTimer)
  qrID.value = ''; qrImage.value = ''; qrStatus.value = ''
}
function openPanel(value: Panel) {
  if (busy.value) return
  if (panel.value === 'bind' && value !== 'bind') stopQR()
  error.value = ''; panel.value = value; disconnectConfirm.value = false
}
async function closePanel() {
  if (busy.value) return
  stopQR(); panel.value = ''; consent.value = false; disconnectConfirm.value = false
  await nextTick()
  if (document.activeElement === document.body) driveHeading.value?.focus()
}
async function openBind() {
  if (busy.value || !canManage.value) return
  const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  busy.value = true
  const loaded = await load()
  busy.value = false
  if (!loaded || lifecycle.signal.aborted) return
  if (state.value?.pendingDrive) { openDirectoryPanel(); return }
  if (!canManage.value) { error.value = '当前不能管理个人网盘，请刷新状态或联系管理员'; return }
  await nextTick()
  if (trigger?.isConnected) trigger.focus()
  stopQR(); consent.value = false; openPanel('bind')
}
function changeClient() { stopQR(); qrStatus.value = '客户端已切换，请重新获取二维码' }
function openDirectoryPanel() {
  if (busy.value || !canManage.value || !state.value?.directoryAccount) return
  stopQR(); directorySnapshot.value = { ...state.value.directoryAccount }
  directoryId.value = directorySnapshot.value.directoryId; manualDirectory.value = !directoryId.value; directoryPath.value = ''; openPanel('directory')
}
async function poll(id: string, current: number, accountId: string, signal: AbortSignal) {
  if (current !== generation || lifecycle.signal.aborted) return
  try {
    const result = await request('/qrcode/status?' + new URLSearchParams({ qrId: id, accountId }), undefined, signal)
    if (current !== generation || lifecycle.signal.aborted) return
    if (result.status === 'success') {
      applyState(result.playback); stopQR()
      if (state.value?.pendingDrive || !state.value?.directoryId) {
        if (state.value?.directoryAccount) { openDirectoryPanel(); notice.value = '115 登录成功，请选择秒传保存目录完成绑定' }
        else { panel.value = 'manage'; notice.value = '115 登录已确认，请检查当前绑定状态' }
      } else { panel.value = 'bound'; notice.value = '115 登录成功，已保留原秒传目录' }
      return
    }
    if (['expired', 'cancelled', 'canceled', 'cancel'].includes(result.status)) { stopQR(); qrStatus.value = '二维码已失效，请重新获取'; return }
    qrStatus.value = result.status === 'scanned' ? '已扫码，请在手机确认' : '请使用 115 手机客户端扫码并确认'
    pollTimer = setTimeout(() => { void poll(id, current, accountId, signal) }, 2500)
  } catch (reason) {
    if (current === generation && !lifecycle.signal.aborted) { stopQR(); error.value = reason instanceof Error ? reason.message : '扫码查询失败'; await load(false) }
  }
}
async function createQR() {
  if (busy.value || !consent.value || !state.value || state.value.pendingDrive || !canManage.value) return
  busy.value = true; error.value = ''; notice.value = ''; stopQR()
  const current = generation, accountId = state.value.accountId || ''
  qrController = new AbortController()
  const signal = qrController.signal
  try {
    const qr = await request('/qrcode', { replace: true, clientType: clientType.value, accountId, revision: state.value.revision || 0 }, signal)
    const image = await QRCode.toDataURL(qr.qrcode, { width: 224, margin: 2 })
    if (current !== generation || lifecycle.signal.aborted) return
    qrID.value = qr.qrId; expiresAt.value = new Date(qr.expiresAt).getTime(); qrImage.value = image
    qrStatus.value = '请使用 115 手机客户端扫码并确认'
    expiryTimer = setTimeout(() => { if (current === generation) { stopQR(); qrStatus.value = '二维码已过期，请重新获取' } }, Math.max(0, expiresAt.value - Date.now()))
    pollTimer = setTimeout(() => { void poll(qr.qrId, current, accountId, signal) }, 2500)
  } catch (reason) {
    if (current === generation && !lifecycle.signal.aborted) { error.value = reason instanceof Error ? reason.message : '二维码获取失败'; await load(false) }
  } finally { busy.value = false }
}
async function openDirectories() {
  if (busy.value || !directorySnapshot.value || !canManage.value) return
  panel.value = ''; await nextTick()
  if (!lifecycle.signal.aborted) selecting.value = true
}
async function closeDirectories() {
  if (busy.value) return
  selecting.value = false; await nextTick()
  if (!lifecycle.signal.aborted) panel.value = 'directory'
}
async function loadDirectories(cid: string, offset: number, signal: AbortSignal) {
  const target = directorySnapshot.value!
  return request('/directories?' + new URLSearchParams({ accountId: target.accountId, revision: String(target.revision), cid, offset: String(offset) }), undefined, signal)
}
async function selectDirectory(value: { id: string; path: string }) {
  directoryId.value = value.id; directoryPath.value = value.path; await closeDirectories()
}
async function mutate(path: string, body: object, success: string) {
  if (busy.value) return false
  busy.value = true; error.value = ''; notice.value = ''; stopQR()
  try {
    const value = await request(path, body)
    if (lifecycle.signal.aborted) return false
    applyState(value); notice.value = success; return true
  } catch (reason) {
    if (!lifecycle.signal.aborted) { error.value = reason instanceof Error ? reason.message : '操作结果未确认，请刷新状态'; await load(false) }
    return false
  } finally { busy.value = false }
}
async function saveDirectory(create = false, parent?: { id: string; path: string }) {
  const target = directorySnapshot.value
  if (!target || (create && !directoryCanCreate.value)) return
  // Block another creation even if the recovery GET also fails after an uncertain write.
  if (create) uncertainDirectories.value.add(target.accountId)
  const wasPending = state.value?.pendingDrive
  const ok = await mutate('/directory', { accountId: target.accountId, revision: target.revision, directoryId: parent?.id || directoryId.value.trim(), create }, '秒传保存目录已确认')
  const current = state.value?.directoryAccount
  // An unsuccessful response can follow an upstream commit. Reconcile before another attempt.
  if (ok || target.accountId !== current?.accountId || target.revision !== current?.revision) {
    selecting.value = false; await nextTick()
    if (lifecycle.signal.aborted) return
    panel.value = ok ? wasPending ? 'bound' : 'manage' : current ? 'directory' : 'manage'
  }
}
async function switchSource() {
  if (!canManage.value || !driveReady.value || state.value?.pendingDrive) return
  if (await mutate('/use-personal', {}, '当前播放来源已切换为个人网盘')) await closePanel()
}
async function disconnect() {
  if (!disconnectConfirm.value) return
  if (await mutate('/disconnect', {}, '个人网盘已解绑，网盘文件保留')) await closePanel()
}
async function cancelPending() {
  if (await mutate('/drive-change', { cancel: true }, '已取消本次绑定，原绑定保留')) await closePanel()
}
async function checkStatus() {
  if (await mutate('/refresh', {}, '检查已完成，请查看当前检查结果')) {
    if (state.value?.enabled && state.value.stage !== 'ready') notice.value = ''
  }
}
async function copyAddress() {
  try { await navigator.clipboard.writeText(safeEmbyURL.value); notice.value = 'Emby 地址已复制' }
  catch { error.value = '复制失败，请手动选择并复制地址' }
}
onMounted(() => { void load() })
onBeforeUnmount(() => { lifecycle.abort(); stopQR() })
</script>

<template>
  <ManagementLayout title="我的播放" description="查看播放来源，管理个人网盘和播放子账号。" content-class="max-w-[1120px]" @logout="emit('logout')">
    <template #actions><button type="button" class="pp-button !px-3" aria-label="播放与网盘使用说明" @click="openPanel('help')"><P115Icon name="help" /></button></template>
    <p v-if="error && !panel && !selecting" role="alert" class="pp-error mb-4">{{ error }} <button v-if="!state" type="button" class="ml-2 underline" :disabled="busy" @click="load()">重试</button></p>
    <p v-if="notice && !panel" role="status" class="mb-4 text-sm text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
    <p v-if="loading" role="status" class="pp-card pp-muted">正在读取播放状态…</p>
    <section v-if="state" aria-label="播放概览" class="mb-5 overflow-hidden rounded-2xl border border-slate-200 bg-white dark:border-slate-700 dark:bg-slate-900">
      <div class="flex flex-wrap items-center gap-5 p-5 sm:p-7">
        <div class="hidden h-14 w-14 shrink-0 items-center justify-center rounded-2xl bg-blue-50 text-blue-600 sm:flex dark:bg-blue-400/10 dark:text-blue-300"><P115Icon name="play" class="!h-7 !w-7" /></div>
        <div class="min-w-0 flex-1 basis-52"><p class="pp-muted">当前播放来源</p><h2 class="mb-3 mt-1 text-2xl font-semibold text-slate-900 dark:text-slate-100">{{ sourceLabel }}</h2><span class="pp-badge" :class="statusClass"><P115Icon :name="statusClass === 'pp-badge-amber' ? 'warning' : 'check'" class="!h-3.5 !w-3.5" />{{ statusLabel }}</span><p class="pp-muted mt-3 break-words">{{ statusDetail }}</p></div>
        <div class="flex w-full gap-2 sm:w-auto sm:min-w-40 sm:flex-col"><a v-if="safeEmbyURL" :href="safeEmbyURL" target="_blank" rel="noopener noreferrer" class="pp-button pp-primary flex-1"><P115Icon name="external" />打开 Emby</a><p v-else class="pp-muted max-w-48">Emby 入口尚未配置，请联系管理员。</p><button type="button" class="pp-button" :disabled="busy" @click="checkStatus"><P115Icon name="refresh" :class="{ 'animate-spin': busy }" />{{ busy ? '处理中…' : '检查状态' }}</button></div>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-x-5 gap-y-2 border-t border-slate-200 bg-slate-50 px-5 py-3 sm:px-7 dark:border-slate-700 dark:bg-slate-800/40"><div class="pp-muted flex flex-wrap gap-x-5"><span>Emby 账号 <strong class="font-medium text-slate-800 dark:text-slate-200">{{ state.embyName || '待确认' }}</strong></span><span>仅支持原文件直链播放</span></div><button type="button" class="pp-link" @click="openPanel('connection')">连接信息与下载说明</button></div>
    </section>
    <div class="grid items-start gap-5 min-[900px]:grid-cols-[1.14fr_1fr]">
      <section v-if="state" class="pp-card">
        <div class="mb-5 flex items-center justify-between gap-3"><h2 ref="driveHeading" tabindex="-1" class="pp-title"><P115Icon name="drive" class="text-slate-400" />我的个人网盘</h2><span v-if="state.source === 'personal' && state.uid" class="pp-badge pp-badge-blue">当前来源</span></div>
        <template v-if="state.uid">
          <div class="flex flex-wrap items-center gap-3"><div class="flex h-11 w-11 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 text-blue-600 dark:border-slate-700 dark:bg-slate-800 dark:text-blue-300"><P115Icon name="drive" /></div><div class="min-w-0 flex-1"><h3 class="font-medium text-slate-900 dark:text-slate-100">115 个人网盘</h3><p class="pp-muted break-all">UID {{ state.uid }}</p></div><span class="pp-badge"><P115Icon name="key" class="!h-3 !w-3" />{{ state.connected ? '已保存凭据' : '未保存凭据' }}</span></div>
          <div class="mt-5 flex justify-between gap-4 text-sm"><span class="shrink-0 text-slate-500 dark:text-slate-400">用于本人当前播放</span><span class="text-right text-slate-700 dark:text-slate-200">{{ state.source === 'personal' ? '当前播放来源' : '未使用，当前来源由管理员提供' }}</span></div>
          <div class="pp-info mt-4"><div class="flex items-center justify-between gap-3"><span class="flex items-center gap-2"><P115Icon name="folder" />秒传保存目录</span><button v-if="canManage && state.directoryAccount && !state.pendingDrive" type="button" class="pp-link shrink-0" :disabled="busy" @click="openDirectoryPanel">更换秒传目录</button></div><p class="mt-2 break-all font-mono text-xs text-slate-500 dark:text-slate-400">{{ state.directoryId ? '目录编号 ' + state.directoryId : '尚未确认保存目录' }}</p></div>
        </template>
        <div v-if="state.pendingDrive" class="pp-warning mt-4"><h3 class="font-semibold">有一项网盘绑定待完成</h3><p class="mt-1 break-all">本次待绑定 UID {{ state.directoryAccount?.uid || '待确认' }}。{{ state.uid ? '目录确认成功前，保留当前绑定和播放来源。' : '尚无已生效的个人网盘绑定。' }}</p><div class="mt-2 flex flex-wrap gap-4"><button v-if="canManage && state.directoryAccount" type="button" class="pp-link" :disabled="busy" @click="openDirectoryPanel">继续选择目录</button><button type="button" class="pp-link" :disabled="busy" @click="cancelPending">取消本次绑定</button></div></div>
        <div v-if="!state.uid && !state.pendingDrive" class="py-5 text-center"><h3 class="font-medium text-slate-900 dark:text-slate-100">{{ canManage ? '绑定自己的 115 网盘' : '当前无需绑定个人网盘' }}</h3><p class="pp-muted mx-auto mb-5 mt-2 max-w-72">{{ canManage ? state.source === 'personal' ? '播放文件会秒传到你选择的目录，占用个人网盘空间。' : '准备个人网盘不会改变当前播放来源，完成后可主动切换。' : '当前使用管理员提供的播放来源，不会创建个人网盘配置。' }}</p><button v-if="canManage" type="button" class="pp-button pp-primary" :disabled="busy" @click="openBind"><P115Icon name="qr" />开始绑定</button></div>
        <p v-if="state.uid && state.source !== 'personal'" class="pp-info mt-4">个人网盘归属保留，未用于本人当前播放；子账号仍共享本人已绑定的个人网盘。</p>
        <div v-if="state.uid || state.pendingDrive" class="mt-5 flex flex-wrap gap-2"><button v-if="canManage && state.source !== 'personal'" type="button" class="pp-button pp-primary" :disabled="busy || !driveReady || state.pendingDrive" @click="openPanel('switch')">切换到我的网盘播放</button><button v-else-if="canManage && !state.pendingDrive" type="button" class="pp-button" :disabled="busy" @click="openBind"><P115Icon name="qr" />重新扫码或换号</button><button type="button" class="pp-button" :disabled="busy" @click="openPanel('manage')"><P115Icon name="more" />管理网盘</button></div>
        <p v-if="state.uid" class="pp-muted mt-3">文件由你在 115 中管理，系统不自动删除。个人网盘不会加入管理员共享池。</p>
      </section>
      <PlaybackChildren :token="token" :drive-ready="driveReady" :playback-loaded="!!state" />
    </div>
    <div class="pp-muted mt-6 flex flex-wrap justify-between gap-x-5 gap-y-3 border-t border-slate-200 pt-4 dark:border-slate-800"><button type="button" class="pp-link" @click="openPanel('privacy')"><P115Icon name="shield" />个人网盘与隐私说明</button><button type="button" class="pp-link" @click="openPanel('help')"><P115Icon name="help" />播放遇到问题？</button><span>个人中心与 Emby 使用相同账号和密码</span></div>

    <P115SidePanel v-if="panel" :title="panelTitle" description="管理本人播放与个人网盘，变更以服务端确认结果为准。" :busy="busy" @close="closePanel">
      <p v-if="error" role="alert" class="pp-error mb-4">{{ error }}</p>
      <p v-if="notice" role="status" class="mb-4 text-xs text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
      <template v-if="panel === 'bind'">
        <div class="mb-5 grid grid-cols-3 gap-2 text-xs"><span class="border-t-2 border-blue-500 pt-2 text-blue-600 dark:text-blue-300">1 使用说明</span><span class="border-t-2 pt-2" :class="qrImage ? 'border-blue-500 text-blue-600 dark:text-blue-300' : 'border-slate-200 text-slate-400 dark:border-slate-700'">2 扫码登录</span><span class="border-t-2 border-slate-200 pt-2 text-slate-400 dark:border-slate-700">3 确认目录</span></div>
        <div v-if="state?.pendingDrive" class="pp-warning mb-4">已有一项绑定待完成，请继续确认目录，或取消该绑定后重新扫码。</div>
        <div class="pp-info"><PersonalDriveDisclosure /></div>
        <label class="pp-check my-5"><input v-model="consent" :disabled="busy || !!qrID" type="checkbox" />我同意保存凭据用于本人播放和只读秒传校验，并在专用目录保存播放文件。</label>
        <label class="pp-field">登录客户端<select v-model="clientType" class="pp-input" :disabled="busy || state?.pendingDrive" @change="changeClient"><option v-for="client in p115LoginClients" :key="client.value" :value="client.value">{{ client.label }}</option></select></label>
        <p class="pp-muted mt-2">切换客户端后需重新获取二维码。关闭面板停止扫码轮询；已经保存的待绑定可在主页继续。</p>
        <div v-if="qrImage" class="mt-5 flex flex-col items-center gap-3"><img :src="qrImage" alt="115 登录二维码" width="224" height="224" class="rounded-xl bg-white" /><p class="pp-muted">请使用相应手机客户端扫码并确认</p></div>
        <p v-if="qrStatus" role="status" class="mt-4 text-sm text-slate-600 dark:text-slate-300">{{ qrStatus }}</p>
      </template>
      <template v-else-if="panel === 'directory'">
        <div v-if="directorySnapshot" class="space-y-5"><div class="pp-info"><p class="break-all font-medium">本次操作网盘 UID {{ directorySnapshot.uid }}</p><p v-if="state?.pendingDrive" class="mt-2">当前绑定{{ state.uid ? ' UID ' + state.uid : '尚未生效' }}。新网盘目录验证成功前，保留当前播放来源。</p></div><p v-if="state?.directoryAccount?.creationUncertain" class="pp-warning">上次创建结果未确认，请浏览网盘并选择已创建的文件夹。</p><p class="pp-muted">秒传文件保存到所选文件夹。更换目录只影响新任务，已启动任务继续使用原目录，已有文件保留。</p><button type="button" class="pp-button w-full" :disabled="busy" @click="openDirectories"><P115Icon name="folder" />选择秒传目录</button><p v-if="directoryPath" class="pp-info break-all">已选择：{{ directoryPath }}</p><details :open="manualDirectory" @toggle="manualDirectory = ($event.target as HTMLDetailsElement).open"><summary class="pp-link cursor-pointer">手动填写目录编号</summary><label class="pp-field mt-3">秒传保存目录 ID<input v-model="directoryId" :disabled="busy" form="personal-directory-form" required pattern="[1-9][0-9]*" inputmode="numeric" placeholder="选择文件夹，也可以填写目录编号" class="pp-input" @input="directoryPath = ''" /></label></details><form id="personal-directory-form" @submit.prevent="saveDirectory()" /></div>
        <p v-else class="pp-warning">当前没有可设置目录的个人网盘，请重新检查绑定状态。</p>
      </template>
      <template v-else-if="panel === 'manage'">
        <div class="pp-info"><p class="break-all">已绑定 UID：{{ state?.uid || '无' }}</p><p class="mt-2">{{ state?.connected ? '已保存登录凭据，这不代表实时健康；检查结果以概览中的说明为准。' : '未保存登录凭据。' }}</p></div>
        <button v-if="canManage && state?.directoryAccount" type="button" class="pp-action" :disabled="busy" @click="openDirectoryPanel"><span>更换秒传保存目录<small>{{ state.pendingDrive ? '继续确认待绑定网盘的目录。' : '只影响新任务，已有文件保留。' }}</small></span><P115Icon name="folder" /></button>
        <button v-if="canManage" type="button" class="pp-action" :disabled="busy || state?.pendingDrive" @click="openBind"><span>重新扫码或换号<small>新网盘目录验证成功前，保留原绑定。</small></span><P115Icon name="qr" /></button>
        <button type="button" class="pp-action" @click="openPanel('privacy')"><span>凭据与文件使用说明</span><P115Icon name="shield" /></button>
        <button v-if="state?.uid || state?.pendingDrive" type="button" class="pp-link mt-7 !text-red-600 dark:!text-red-300" @click="openPanel('disconnect')">解除个人网盘绑定</button>
      </template>
      <template v-else-if="panel === 'bound'"><div class="pp-info space-y-3"><p class="break-all">已绑定 UID {{ state?.uid }}，秒传保存目录已确认。</p><p>{{ state?.source === 'personal' ? '本人当前播放来源为个人网盘。' : '当前播放来源仍为' + sourceLabel + '。若要本人使用个人网盘播放，请单独确认切换来源。' }}</p><p>实际播放仍需 Emby 授权及可用的网盘与直链服务。</p></div></template>
      <template v-else-if="panel === 'switch'"><div class="pp-info space-y-4"><p>当前来源：{{ sourceLabel }}。切换后使用你的 115 网盘，秒传文件会占用个人网盘空间。</p><p>此页不提供切回管理员来源的功能；凭据失效后不会自动回退。需要调整来源时，请联系管理员。</p><p>子账号仍共享本人个人网盘。实际播放仍需 Emby 授权。</p></div></template>
      <template v-else-if="panel === 'disconnect'"><div class="pp-warning space-y-3"><p>{{ state?.source === 'personal' ? '解绑后，本人新的播放取链将被拒绝，需要重新绑定个人网盘。' : '本人当前使用的管理员播放来源不受影响。' }}</p><p>共享此个人网盘的子账号将无法发起新的播放请求。</p><p>将清除个人网盘配置、凭据与关联。115 中已有文件保留，已签发直链不保证立即失效。</p></div><label class="pp-check mt-5"><input v-model="disconnectConfirm" type="checkbox" :disabled="busy" />我已了解上述影响，确认解除个人网盘绑定。</label></template>
      <PersonalDriveDisclosure v-else-if="panel === 'privacy'" class="pp-info" />
      <template v-else-if="panel === 'connection'"><div class="space-y-5"><div><p class="pp-muted">Emby 账号</p><p class="break-all font-medium">{{ state?.embyName || '待确认' }}</p></div><div><p class="pp-muted">服务地址</p><a v-if="safeEmbyURL" :href="safeEmbyURL" target="_blank" rel="noopener noreferrer" class="mt-2 block break-all text-sm text-blue-600 dark:text-blue-300">{{ safeEmbyURL }}</a><p v-else class="pp-warning">Emby 入口尚未配置，请联系管理员。</p><button v-if="safeEmbyURL" type="button" class="pp-link mt-2" @click="copyAddress">复制 Emby 地址</button></div><div class="pp-info space-y-3"><p>个人中心与 Emby 使用同一账号和密码。</p><p>仅支持原文件直链，禁止转码及重新封装。</p><p>{{ state?.allowDownload ? '本服务已开放原文件下载，仍需 Emby 用户下载授权。' : '管理员未开放下载。' }}</p></div></div></template>
      <div v-else-if="panel === 'help'" class="space-y-4"><div class="pp-info"><h3 class="font-semibold">个人网盘登录或目录异常</h3><p class="mt-2">先检查状态。按服务端原因重新扫码或选择有效目录，新网盘确认成功前原绑定保留。</p></div><div class="pp-info"><h3 class="font-semibold">使用管理员来源</h3><p class="mt-2">无需选择管理员账号。若来源不可用，请联系管理员，不会自动使用其他来源兜底。</p></div><div class="pp-info"><h3 class="font-semibold">检查通过仍无法播放</h3><p class="mt-2">检查结果是网盘状态快照。Emby 授权、直链服务、网络与播放器对原文件的支持仍影响实际播放。</p></div></div>
      <template #footer>
        <button type="button" class="pp-button" :disabled="busy" @click="closePanel">{{ panel === 'bound' && state?.source !== 'personal' ? '保持当前来源' : '关闭' }}</button>
        <button v-if="panel === 'bind' && state?.pendingDrive" type="button" class="pp-button pp-primary ml-auto" :disabled="busy" @click="openDirectoryPanel">继续选择目录</button>
        <button v-else-if="panel === 'bind'" type="button" class="pp-button pp-primary ml-auto" :disabled="busy || !consent || !!qrID || !canManage" @click="createQR">{{ busy ? '正在获取…' : qrID ? '等待扫码确认' : '获取二维码' }}</button>
        <button v-else-if="panel === 'directory'" type="submit" form="personal-directory-form" class="pp-button pp-primary ml-auto" :disabled="busy || !directoryId || !directorySnapshot || !canManage">{{ busy ? '保存中…' : state?.pendingDrive ? '确认目录并完成绑定' : '保存秒传目录' }}</button>
        <button v-else-if="panel === 'bound' && state?.source !== 'personal' && canManage" type="button" class="pp-button pp-primary ml-auto" :disabled="!driveReady || busy || state?.pendingDrive" @click="openPanel('switch')">切换到我的网盘播放</button>
        <button v-else-if="panel === 'switch'" type="button" class="pp-button pp-primary ml-auto" :disabled="busy || !canManage || !driveReady || state?.pendingDrive" @click="switchSource">{{ busy ? '切换中…' : '确认切换' }}</button>
        <button v-else-if="panel === 'disconnect'" type="button" class="pp-button pp-danger ml-auto" :disabled="busy || !disconnectConfirm" @click="disconnect">{{ busy ? '解绑中…' : '确认解绑' }}</button>
      </template>
    </P115SidePanel>
    <P115DirectoryDialog v-if="selecting && directorySnapshot" :token="token" :account-id="directorySnapshot.accountId" :title="'选择秒传目录 · UID ' + directorySnapshot.uid" :description="directoryCanCreate ? '不能直接使用根目录；可以在根目录下新建 Cinema115 文件夹。新建会立即保存目录。' : '上次创建结果未确认，请浏览并选择已有文件夹，不要重复创建。'" :load-page="loadDirectories" :allow-root="false" :allow-create="directoryCanCreate" :saving="busy" :save-error="error" @close="closeDirectories" @select="selectDirectory" @create="saveDirectory(true, $event)" />
  </ManagementLayout>
</template>
