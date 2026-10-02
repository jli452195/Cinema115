<script setup lang="ts">
import { onBeforeUnmount, onMounted, onUnmounted, ref } from 'vue'
import QRCode from 'qrcode'
import { p115LoginClients } from '../lib/p115-login-clients'
import { p115Request } from '../lib/p115-api'
import P115DirectoryDialog from './P115DirectoryDialog.vue'

const props = withDefaults(defineProps<{ token: string; accountId: string; accountName: string; primary?: boolean; needsDirectory?: boolean; clientType?: string; revision?: number; mode?: 'qr' | 'cookie' }>(), { clientType: 'alipay-miniapp', mode: 'qr' })
const emit = defineEmits<{ close: []; connected: [client: string]; changed: [] }>()
const snapshot = { ...props }
interface Replacement { sessionId: string; oldUid: string; newUid: string; expiresAt: string }
interface Result { status: string; connected: boolean; loginClient?: string; replacement?: Replacement }
const client = ref(snapshot.clientType)
const dialog = ref<HTMLDialogElement>()
const qrImage = ref(''), cookie = ref(''), message = ref(''), error = ref('')
const phase = ref<'loading' | 'waiting' | 'scanned' | 'failed' | 'success' | 'directory' | 'saving' | 'cookie'>('loading')
const replacement = ref<Replacement>(), selecting = ref(false), directory = ref<{ id: string; path: string }>()
const sessionID = ref('')
let generation = 0
let controller: AbortController | undefined
let timer: number | undefined, expiryTimer: number | undefined
let previousFocus: HTMLElement | null = null
let previousOverflow = ''

function cancelSession(id: string) {
  if (!id) return
  // Independent of the cancelled poll, and scoped to its opaque session token.
  void p115Request(snapshot.token, 'accounts/' + encodeURIComponent(snapshot.accountId) + '/replacement?sessionId=' + encodeURIComponent(id), { method: 'DELETE', signal: AbortSignal.timeout(10000) }).catch(() => {})
}
function stop() { generation++; controller?.abort(); window.clearTimeout(timer); window.clearTimeout(expiryTimer) }
function clearSession() { cancelSession(sessionID.value); sessionID.value = '' }
function close() { stop(); clearSession(); emit('close') }
function request<T>(path: string, init: RequestInit = {}) {
  return p115Request<T>(snapshot.token, 'accounts/' + encodeURIComponent(snapshot.accountId) + (path ? '/' + path : ''), {
    ...init, signal: AbortSignal.any([controller!.signal, AbortSignal.timeout(45000)]),
  })
}
function failed(reason: unknown, current: number) {
  if (current !== generation) return
  error.value = reason instanceof Error ? reason.message : '操作失败，请重试'
  const status = (reason as { status?: number })?.status
  if (status === 409 || status === 404) {
    stop(); clearSession(); replacement.value = undefined; selecting.value = false; phase.value = 'failed'; emit('changed')
  } else if (!replacement.value) { stop(); clearSession(); phase.value = snapshot.mode === 'cookie' ? 'cookie' : 'failed'; qrImage.value = '' }
}
function expire(at: string, current: number) {
  window.clearTimeout(expiryTimer)
  const remaining = Date.parse(at) - Date.now()
  if (!Number.isFinite(remaining) || remaining <= 0) throw new Error('登录会话已过期，请重新登录')
  expiryTimer = window.setTimeout(() => {
    if (generation !== current) return
    stop(); clearSession(); qrImage.value = ''; replacement.value = undefined; selecting.value = false
    phase.value = snapshot.mode === 'cookie' ? 'cookie' : 'failed'; error.value = '登录会话已过期，请重新登录'
  }, remaining)
}
function accept(data: Result, current: number) {
  if (data.replacement) {
    window.clearTimeout(timer); qrImage.value = ''; cookie.value = ''
    replacement.value = data.replacement; sessionID.value = data.replacement.sessionId
    directory.value = undefined; phase.value = 'directory'; message.value = ''; error.value = ''
    expire(data.replacement.expiresAt, current)
    return true
  }
  if (data.status === 'success' && data.connected) {
    const needsDirectory = snapshot.needsDirectory && !replacement.value
    sessionID.value = ''; stop(); replacement.value = undefined; cookie.value = ''; qrImage.value = ''
    phase.value = 'success'; error.value = ''; message.value = needsDirectory ? '登录成功，接下来设置秒传目录' : '登录成功，配置已保存'
    timer = window.setTimeout(() => emit('connected', data.loginClient || ''), 500)
    return true
  }
  return false
}
async function createQRCode() {
  stop(); clearSession(); controller = new AbortController()
  const current = generation
  phase.value = 'loading'; qrImage.value = ''; error.value = ''; message.value = '正在获取二维码…'
  try {
    const data = await request<{ qrId: string; sessionId: string; qrcode: string; expiresAt: string }>('qrcode', { method: 'POST', body: JSON.stringify({ clientType: client.value }) })
    if (current !== generation) { cancelSession(data.sessionId); return }
    sessionID.value = data.sessionId || ''
    expire(data.expiresAt, current)
    const image = await QRCode.toDataURL(data.qrcode, { width: 256, margin: 2 })
    if (current !== generation) return
    qrImage.value = image; phase.value = 'waiting'; message.value = '等待扫码'
    timer = window.setTimeout(() => poll(data.qrId, current), 1500)
  } catch (reason) { failed(reason, current) }
}
async function poll(qrId: string, current: number) {
  if (current !== generation) return
  try {
    const data = await request<Result>('qrcode/status?qrId=' + encodeURIComponent(qrId))
    if (current !== generation) { if (data.replacement) cancelSession(data.replacement.sessionId); return }
    if (accept(data, current)) return
    if (data.status === 'expired' || data.status === 'cancelled') throw new Error(data.status === 'expired' ? '二维码已过期，请重新获取' : '已取消登录，请重新获取二维码')
    phase.value = data.status === 'scanned' ? 'scanned' : 'waiting'
    message.value = data.status === 'scanned' ? '已扫码，请在手机上确认登录' : '等待扫码'
    timer = window.setTimeout(() => poll(qrId, current), 2000)
  } catch (reason) { failed(reason, current) }
}
async function saveCookie() {
  if (!cookie.value.trim() || phase.value === 'saving') return
  stop(); clearSession(); controller = new AbortController(); const current = generation
  phase.value = 'saving'; error.value = ''; message.value = '正在验证凭据…'
  try {
    const data = await request<Result>('', { method: 'PUT', body: JSON.stringify({ cookie: cookie.value.trim(), revision: snapshot.revision }) })
    if (current !== generation) { if (data.replacement) cancelSession(data.replacement.sessionId); return }
    accept(data, current)
  } catch (reason) { failed(reason, current) }
}
async function loadDirectories(cid: string, offset: number, signal: AbortSignal) {
  return p115Request<{ directories: { id: string; name: string }[]; hasMore: boolean; nextOffset: number }>(snapshot.token,
    'accounts/' + encodeURIComponent(snapshot.accountId) + '/replacement/paths?' + new URLSearchParams({ sessionId: sessionID.value, cid, offset: String(offset) }), { signal })
}
async function confirm() {
  if (!directory.value || !replacement.value || phase.value === 'saving') return
  const current = generation; phase.value = 'saving'; error.value = ''
  try {
    const data = await request<Result>('replacement/confirm', { method: 'POST', body: JSON.stringify({ sessionId: sessionID.value, directoryId: directory.value.id }) })
    if (current !== generation) return
    accept(data, current)
  } catch (reason) {
    if (current !== generation) return
    phase.value = 'directory'; failed(reason, current)
  }
}
onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  if (snapshot.mode === 'cookie') { phase.value = 'cookie'; controller = new AbortController() } else void createQRCode()
})
onBeforeUnmount(() => { stop(); clearSession(); cookie.value = ''; dialog.value?.close() })
onUnmounted(() => { document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected) previousFocus.focus() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="p115-qr-title" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-md overflow-y-auto rounded-2xl border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-300" @cancel.prevent="close">
      <header class="relative border-b border-slate-100 px-12 py-4 text-center dark:border-slate-800">
        <h2 id="p115-qr-title" class="text-lg font-semibold text-slate-900 dark:text-slate-100">{{ replacement ? '选择新网盘目录' : mode === 'cookie' ? '手动登录' : '115 扫码登录' }}</h2>
        <p class="mt-1 text-xs text-slate-500">{{ accountName }}</p>
        <button type="button" aria-label="关闭登录弹窗" class="absolute right-3 top-3 rounded-lg p-3 text-slate-500" @click="close"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
      </header>
      <div class="space-y-4 p-5">
        <p v-if="primary && phase !== 'success'" class="rounded-lg bg-slate-50 p-3 text-xs leading-5 text-slate-500 dark:bg-slate-800">重新登录同一 UID 保留各功能目录；更换为其他 UID 后，需要重新配置各功能目录。</p>
        <p v-if="needsDirectory && !replacement && phase !== 'success'" class="rounded-lg bg-blue-50 p-3 text-xs leading-5 text-blue-700 dark:bg-blue-950/30 dark:text-blue-300">先登录，再设置秒传目录。完成目录设置后，才能分配用户使用。</p>
        <template v-if="replacement">
          <p class="text-sm">原 UID：{{ replacement.oldUid }} → 新 UID：{{ replacement.newUid }}</p>
          <p class="text-sm text-slate-500">确认后旧账号的新取链需要重新选择并核验目标，原网盘文件不会迁移。</p>
          <button type="button" :disabled="phase === 'saving'" class="w-full break-all rounded-xl border border-slate-200 px-4 py-3 text-left text-sm text-blue-600 dark:border-slate-700" @click="selecting = true">{{ directory ? directory.path + '（' + directory.id + '）' : '选择新网盘中的秒传目录' }}</button>
          <div class="flex justify-end gap-3"><button type="button" class="px-4 py-2 text-sm" @click="close">取消</button><button type="button" :disabled="!directory || phase === 'saving'" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="confirm">{{ phase === 'saving' ? '验证并保存中…' : '确认更换' }}</button></div>
        </template>
        <p v-else-if="phase === 'success'" role="status" class="flex items-center justify-center gap-2 py-8 text-emerald-600"><svg class="h-5 w-5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12 4 4L19 6" /></svg>{{ message }}</p>
        <form v-else-if="mode === 'cookie'" class="space-y-4" @submit.prevent="saveCookie">
          <label class="block text-sm">Cookie<input v-model="cookie" type="password" autocomplete="off" :disabled="phase === 'saving'" class="mt-2 block w-full rounded-xl border border-slate-200 bg-transparent px-3 py-3 dark:border-slate-700" placeholder="UID=…; CID=…; SEID=…" /></label>
          <p class="text-xs text-slate-500">{{ primary ? '新 Cookie 验证通过后保存，失败保留原配置。' : '验证后保存；换成其他账号时，需要先选择新网盘目录。' }}</p>
          <button :disabled="!cookie.trim() || phase === 'saving'" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">{{ phase === 'saving' ? '验证中…' : '验证并继续' }}</button>
        </form>
        <template v-else>
          <label class="block text-sm">登录设备<select v-model="client" aria-label="登录设备" :disabled="phase === 'saving'" class="mt-2 block w-full rounded-xl border border-slate-200 bg-transparent px-3 py-2 dark:border-slate-700" @change="createQRCode"><option v-for="item in p115LoginClients" :key="item.value" :value="item.value">{{ item.label }}</option></select></label>
          <div :aria-busy="phase === 'loading'" class="mx-auto flex aspect-square w-full max-w-64 items-center justify-center overflow-hidden rounded-xl border border-slate-200 bg-white p-2">
            <img v-if="qrImage" :src="qrImage" alt="115 登录二维码" width="256" height="256" class="h-full w-full object-contain" />
            <span v-else class="px-4 text-sm text-slate-500">{{ phase === 'loading' ? '正在获取二维码…' : '请重新获取二维码' }}</span>
          </div>
          <p class="text-center text-sm">请使用 115 App 扫码并确认</p>
          <p v-if="!error" role="status" class="text-center text-sm text-slate-500">{{ message }}</p>
          <div class="text-center"><button type="button" :disabled="phase === 'loading'" class="rounded-lg bg-blue-600 px-6 py-2 text-sm text-white disabled:opacity-40" @click="createQRCode">{{ phase === 'failed' ? '重新获取二维码' : '刷新二维码' }}</button></div>
        </template>
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      </div>
    </dialog>
  </Teleport>
  <P115DirectoryDialog v-if="selecting && replacement" :token="token" :account-id="accountId" :load-page="loadDirectories" :title="'选择新网盘目录 · UID ' + replacement.newUid" description="秒传文件直接保存到所选目录，不创建子目录，文件由你手动清理。" @close="selecting = false" @select="directory = $event; selecting = false" />
</template>
