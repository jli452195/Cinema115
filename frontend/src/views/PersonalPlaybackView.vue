<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import ManagementLayout from '../components/ManagementLayout.vue'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
interface Playback { needsDirectoryConfirmation: boolean; canPreparePersonal: boolean; pendingDrive: boolean; enabled: boolean; stage: string; message: string; uid: string; connected: boolean; directoryId: string; embyName: string; embyUrl: string; suspended: boolean; blocked: boolean; allowDownload: boolean; syncPending: boolean; source: string }
const state = ref<Playback>(), error = ref(''), notice = ref(''), busy = ref(false)
const directoryId = ref('')
const consent = ref(false), disconnectConfirm = ref(false)
function applyState(value: Playback) { state.value = value }
const qrImage = ref(''), qrID = ref(''), expiresAt = ref(0), qrStatus = ref('')
const lifecycle = new AbortController()
let pollTimer: ReturnType<typeof setTimeout> | undefined
const label = computed(() => state.value?.blocked ? '管理员已停用' : state.value?.syncPending ? '权限同步中' : ({ unbound: '待注册 Emby', bound: '待注册 Emby', pending: '已注册，播放待开通', creating: '注册待确认', provisioning: '正在配置账号', paused: '播放已暂停', ready: '播放已开通' }[state.value?.stage || ''] || '正在加载'))
const safeEmbyURL = computed(() => { try { const url = new URL(state.value?.embyUrl || ''); return ['http:', 'https:'].includes(url.protocol) ? url.href : '' } catch { return '' } })
async function request(path = '', body?: object) {
  const response = await fetch(`/api/me/playback${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '个人播放服务暂时不可用')
  return data
}
async function load() { try { applyState(await request()) } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '状态读取失败' } }
function stopQR() { clearTimeout(pollTimer); qrID.value = ''; qrImage.value = '' }
async function poll(id: string) {
  if (!id || id !== qrID.value || lifecycle.signal.aborted) return
  if (Date.now() >= expiresAt.value) { stopQR(); qrStatus.value = '二维码已过期，请重新获取'; return }
  try {
    const result = await request(`/qrcode/status?qrId=${encodeURIComponent(id)}`)
    if (id !== qrID.value) return
    if (result.status === 'success') { state.value = result.playback; stopQR(); qrStatus.value = '115 绑定成功'; return }
    if (['expired', 'canceled', 'cancel'].includes(result.status)) { stopQR(); qrStatus.value = '二维码已失效，请重新获取'; return }
    qrStatus.value = result.status === 'scanned' ? '已扫码，请在手机确认' : '请使用 115 手机客户端扫码并确认'
    pollTimer = setTimeout(() => { void poll(id) }, 2500)
  } catch (reason) { if (!lifecycle.signal.aborted) { stopQR(); error.value = reason instanceof Error ? reason.message : '扫码查询失败'; await load() } }
}
async function createQR() {
  if (busy.value || !consent.value) return
  busy.value = true; error.value = ''; stopQR()
  try {
    const qr = await request('/qrcode', { replace: true })
    qrID.value = qr.qrId; expiresAt.value = new Date(qr.expiresAt).getTime()
    qrImage.value = await QRCode.toDataURL(qr.qrcode, { width: 224, margin: 2 })
    qrStatus.value = '请使用 115 手机客户端扫码并确认'
    pollTimer = setTimeout(() => { void poll(qr.qrId) }, 2500)
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '二维码获取失败' }
  finally { busy.value = false }
}
async function mutate(path: string, body: object) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''; stopQR()
  try { applyState(await request(path, body)); disconnectConfirm.value = false; notice.value = '状态已更新' }
  catch (reason) { if (!lifecycle.signal.aborted) { error.value = reason instanceof Error ? reason.message : '操作结果未确认，请刷新状态'; await load() } }
  finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); stopQR() })
</script>

<template>
  <ManagementLayout title="我的播放" description="使用 Emby 账号登录，管理自己的网盘与播放状态。" @logout="emit('logout')">
    <div class="mx-auto max-w-3xl space-y-5">
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
      <section class="rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
        <div class="flex items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">{{ label }}</h2><button :disabled="busy" class="text-sm text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="mutate('/refresh', {})">检查状态</button></div>
        <p class="mt-3 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ state?.message || '播放需要管理员授权和可用的网盘账号。' }}</p>
        <p v-if="state?.source" class="mt-2 text-sm text-slate-700 dark:text-slate-300">播放方式：{{ state.source === 'primary' ? '主网盘，无需扫码' : state.source === 'assigned' ? '管理员共享小号，无需扫码' : '绑定自己的 115 网盘' }}</p>
        <p v-if="state?.enabled" class="mt-2 text-xs leading-6 text-slate-500 dark:text-slate-400">仅支持原文件直链，禁止转码及重新封装。{{ state.allowDownload ? '管理员已允许下载原文件。' : '管理员未开放下载。' }}</p>
        <p v-if="state && !state.enabled" class="mt-3 text-sm text-amber-700 dark:text-amber-300">管理员尚未开放自助播放，请稍后再试。</p>
      </section>
      <section v-if="state" class="space-y-3 rounded-2xl border border-slate-200 p-5 dark:border-slate-700"><h2 class="font-semibold">Emby 账号</h2><p class="text-sm">{{ state.embyName }}</p><a v-if="safeEmbyURL" :href="safeEmbyURL" target="_blank" rel="noopener noreferrer" class="break-all text-sm text-blue-600">{{ state.embyUrl }}</a><p class="text-xs text-slate-500">个人中心与 Emby 使用同一账号和密码。</p></section>
      <section v-if="state?.source === 'personal' || state?.canPreparePersonal" class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">{{ state.source === 'personal' ? '绑定自己的 115，开通播放' : '准备自己的 115 网盘' }}</h2>
        <p v-if="state.source !== 'personal'" class="text-sm leading-6 text-slate-500 dark:text-slate-400">扫码仅准备本人网盘与专用目录，不改变当前播放账号。准备完成后可主动切换，切换后失效不会回退管理员账号。</p>
        <p class="text-sm leading-6 text-slate-500 dark:text-slate-400">播放时会在你的网盘专用目录中秒传保存文件，占用网盘空间。凭据失效会暂停播放，重新扫码可登录原账号或换号，新账号目录验证成功后切换。</p>
        <p v-if="state?.uid" class="text-sm text-slate-700 dark:text-slate-300">当前 115 UID：{{ state.uid }} · {{ state.connected ? '已保存登录凭据' : '需要重新扫码' }}</p>
        <p v-if="state?.directoryId" class="text-xs text-slate-500 dark:text-slate-400">播放目录编号：{{ state.directoryId }}。文件由你在 115 中管理，系统不自动删除。</p>
        <label class="flex items-start gap-2 text-sm text-slate-600 dark:text-slate-300"><input v-model="consent" type="checkbox" class="mt-1" />我同意保存凭据用于本人播放和只读秒传校验，并在专用目录保存播放文件。</label>
        <button :disabled="busy || !consent || !state?.enabled || state?.blocked" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="createQR()">{{ state?.uid ? '扫码登录或换号' : '扫码绑定 115' }}</button>
        <p class="text-xs text-slate-500">已记录的播放文件可为其他用户提供秒传校验片段，不会扫描其他文件。切换播放来源后仍可参与校验，解绑后停止参与新请求。个人网盘不进入小号池，管理员无法查看或修改个人 Cookie。</p>
        <form v-if="state.needsDirectoryConfirmation" class="space-y-2" @submit.prevent="mutate('/directory',{directoryId})"><label class="block text-sm">确认个人播放目录编号<input v-model="directoryId" required pattern="[0-9]+" class="mt-2 block w-full rounded-lg border p-2 dark:bg-slate-900" /></label><p class="text-xs text-slate-500">目录创建结果未确认，请在自己的 115 网盘核查后填写。</p><button :disabled="busy" class="text-sm text-blue-600">确认目录</button></form>
        <div v-if="state.pendingDrive" class="space-y-2 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20"><p>有待完成的网盘绑定，原播放来源保留。</p><button :disabled="busy" class="mr-4 underline" @click="mutate('/drive-change', {cancel:false})">继续校验并完成</button><button :disabled="busy" class="underline" @click="mutate('/drive-change', {cancel:true})">取消本次绑定</button></div>
        <div v-if="qrImage" class="flex flex-col items-center gap-3"><img :src="qrImage" alt="115 登录二维码" width="224" height="224" class="rounded-xl" /><button class="text-sm text-slate-500 dark:text-slate-400" @click="stopQR">关闭二维码</button></div>
        <p v-if="qrStatus" role="status" class="text-sm text-slate-500 dark:text-slate-400">{{ qrStatus }}</p>
        <button v-if="state.source !== 'personal'" :disabled="busy || !state.connected || !state.directoryId || state.blocked || !state.enabled" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="mutate('/use-personal', {})">切换到我的网盘播放</button>
      </section>

      <section v-if="state && state.source !== 'personal' && !state.canPreparePersonal" class="space-y-3 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">个人网盘 · {{ state.uid ? '未用于当前播放' : '未绑定' }}</h2>
        <template v-if="state.uid">
          <p class="text-sm text-slate-700 dark:text-slate-300">UID：{{ state.uid }} · {{ state.connected ? '已保存登录凭据' : '登录凭据已清除' }}</p>
          <p class="text-sm leading-6 text-slate-500 dark:text-slate-400">当前播放使用管理员提供的账号。个人网盘归属保留，管理员切回个人网盘后可继续使用原账号。</p>
          <p v-if="state.directoryId" class="text-xs text-slate-500 dark:text-slate-400">个人播放目录编号：{{ state.directoryId }}。网盘文件由你在 115 中管理。</p>
        </template>
        <p v-else class="text-sm text-slate-500 dark:text-slate-400">当前无需绑定个人网盘，也不会创建个人网盘配置。</p>
      </section>
      <section v-if="state && (state.uid || state.pendingDrive)" class="rounded-2xl border border-slate-200 p-5 dark:border-slate-700">
        <label class="flex items-start gap-2 text-sm text-slate-500 dark:text-slate-400"><input v-model="disconnectConfirm" type="checkbox" class="mt-1" />{{ state.source === 'personal' ? '解除个人网盘绑定并暂停播放，清除保存的凭据；网盘文件保留，可重新绑定任意账号。' : '解除个人网盘绑定并清除凭据，网盘文件保留，不影响当前管理员提供的播放账号。' }}</label>
        <button :disabled="busy || !disconnectConfirm" class="mt-3 text-sm text-red-600 disabled:opacity-40 dark:text-red-300" @click="mutate('/disconnect', {})">解绑个人网盘</button>
      </section>
    </div>
  </ManagementLayout>
</template>
