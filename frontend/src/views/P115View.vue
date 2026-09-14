<script setup lang="ts">
import ManagementLayout from '../components/ManagementLayout.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import QRCode from 'qrcode'

const props = defineProps<{ token: string }>()
defineEmits<{ back: []; logout: [] }>()
type Mode = 'cookie' | 'qrcode'
type Status = 'checking' | 'empty' | 'valid' | 'invalid' | 'unknown'
const mode = ref<Mode>('cookie')
const clientType = ref('alipay-miniapp')
const cookie = ref('')
const visible = ref(false)
const configured = ref(false)
const status = ref<Status>('checking')
const busy = ref('')
const saving = computed(() => busy.value === 'save' || busy.value === 'disconnect')
const message = ref('')
const tone = ref<'info' | 'success' | 'error'>('info')
const qrImage = ref('')
const qrStatus = ref('获取二维码后，使用 115 App 扫码确认')
const confirmDisconnect = ref(false)
const statusText = computed(() => ({ checking: '检查中', empty: '未配置', valid: '已连接', invalid: '已失效', unknown: '待验证' })[status.value])
const statusClass = computed(() => status.value === 'valid' ? 'bg-emerald-50 text-emerald-700' : status.value === 'invalid' ? 'bg-red-50 text-red-700' : 'bg-slate-100 text-slate-500')
let generation = 0
let controller: AbortController | undefined
let timer: number | undefined

class RequestError extends Error {
  constructor(message: string, readonly configured?: boolean) { super(message) }
}

// 操作序号隔离迟到的响应，切换页面或刷新二维码时取消旧请求。
function cancel() {
  generation++
  controller?.abort()
  if (timer !== undefined) window.clearTimeout(timer)
  timer = undefined
  busy.value = ''
}
function begin(label: string) {
  cancel()
  if (qrImage.value) {
    qrImage.value = ''
    qrStatus.value = '请重新获取二维码'
  }
  controller = new AbortController()
  busy.value = label
  message.value = ''
  confirmDisconnect.value = false
  return generation
}
function notify(text: string, kind: typeof tone.value = 'info') { message.value = text; tone.value = kind }
async function request(path: string, method = 'GET', body?: object) {
  const response = await fetch(`/api/admin/p115/${path}`, {
    method, signal: controller?.signal,
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (response.status === 204) return {}
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) {
    const error = new RequestError(response.status === 401 ? '管理员登录已过期，请退出后重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`, data?.configured)
    throw error
  }
  return data
}
function fail(error: unknown, id: number) {
  if (id !== generation || (error instanceof DOMException && error.name === 'AbortError')) return
  notify(error instanceof Error ? error.message : String(error), 'error')
}

async function loadStatus() {
  const id = begin('status')
  status.value = 'checking'
  try {
    const data = await request('status')
    if (id !== generation) return
    configured.value = data.configured
    status.value = data.connected ? 'valid' : data.configured ? 'invalid' : 'empty'
  } catch (error) {
    if (id === generation) {
      status.value = 'unknown'
      if (error instanceof RequestError && typeof error.configured === 'boolean') configured.value = error.configured
    }
    fail(error, id)
  } finally { if (id === generation) busy.value = '' }
}

async function testCookie() {
  const draft = cookie.value.trim()
  const id = begin('test')
  try {
    const data = await request('cookie/test', 'POST', { cookie: draft })
    if (id !== generation) return
    if (!draft) { configured.value = true; status.value = data.valid ? 'valid' : 'invalid' }
    notify(data.valid ? (draft ? '输入的 Cookie 有效，点击保存后生效。' : '已保存的 Cookie 验证通过。') : 'Cookie 无效或已过期，请重新获取。', data.valid ? 'success' : 'error')
  } catch (error) { fail(error, id) }
  finally { if (id === generation) busy.value = '' }
}

async function saveCookie() {
  if (!cookie.value.trim()) { notify('请输入新的 Cookie', 'error'); return }
  const id = begin('save')
  try {
    const data = await request('login', 'POST', { cookie: cookie.value.trim() })
    if (id !== generation) return
    cookie.value = ''; visible.value = false
    configured.value = true; status.value = 'valid'
    notify(data.message, 'success')
  } catch (error) { fail(error, id) }
  finally { if (id === generation) busy.value = '' }
}

async function loadCookie() {
  const id = begin('load')
  try {
    const data = await request('cookie')
    if (id !== generation) return
    cookie.value = data.cookie; visible.value = false
    configured.value = Boolean(data.cookie)
    notify(data.cookie ? '已载入保存的 Cookie，可以测试或修改。' : '目前没有已保存的 Cookie。')
  } catch (error) { fail(error, id) }
  finally { if (id === generation) busy.value = '' }
}

async function disconnect() {
  const id = begin('disconnect')
  try {
    await request('login', 'DELETE')
    if (id !== generation) return
    configured.value = false; status.value = 'empty'; cookie.value = ''; visible.value = false; qrImage.value = ''
    notify('已清除保存的登录信息。', 'success')
  } catch (error) { fail(error, id) }
  finally { if (id === generation) busy.value = '' }
}

async function createQRCode() {
  const id = begin('qrcode')
  qrImage.value = ''; qrStatus.value = '正在获取二维码…'
  try {
    const data = await request('qrcode', 'POST', { clientType: clientType.value })
    const image = await QRCode.toDataURL(data.qrcode, { width: 256, margin: 2 })
    if (id !== generation) return
    qrImage.value = image; qrStatus.value = '等待扫码'
    const expires = Date.parse(data.expiresAt)
    if (!Number.isFinite(expires)) throw new Error('二维码有效期无效，请重试')
    timer = window.setTimeout(() => poll(data.qrId, expires, id), 1500)
  } catch (error) { fail(error, id); if (id === generation) qrStatus.value = '获取失败，请重试' }
  finally { if (id === generation) busy.value = '' }
}

async function poll(qrId: string, expires: number, id: number) {
  if (id !== generation) return
  if (Date.now() >= expires) { qrImage.value = ''; qrStatus.value = '二维码已过期，请重新获取'; return }
  try {
    const data = await request(`qrcode/status?qrId=${encodeURIComponent(qrId)}`)
    if (id !== generation) return
    if (data.status === 'success' && data.connected) {
      configured.value = true; status.value = 'valid'; cookie.value = ''; visible.value = false; qrImage.value = ''
      qrStatus.value = '扫码登录成功'; notify('登录信息已加密保存，重启后自动恢复。', 'success')
      return
    }
    if (data.status === 'expired' || data.status === 'cancelled') {
      qrImage.value = ''; qrStatus.value = data.status === 'expired' ? '二维码已过期' : '已取消登录'
      return
    }
    qrStatus.value = data.status === 'scanned' ? '已扫码，请在手机上确认' : '等待扫码'
    timer = window.setTimeout(() => poll(qrId, expires, id), 2000)
  } catch (error) { fail(error, id); if (id === generation) { qrImage.value = ''; qrStatus.value = '查询中断，请重新获取二维码' } }
}

watch([mode, clientType], () => {
  cancel(); qrImage.value = ''; message.value = ''; visible.value = false; confirmDisconnect.value = false
  qrStatus.value = '获取二维码后，使用 115 App 扫码确认'
  if (status.value === 'checking') status.value = 'unknown'
})
onMounted(loadStatus)
onBeforeUnmount(cancel)
</script>

<template>
  <ManagementLayout title="115 登录配置" description="管理登录凭证，为分享播放与媒体访问提供连接。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
      <section class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <header class="flex flex-wrap items-center justify-between gap-4 border-b border-slate-100 px-5 py-5 sm:px-7">
          <div class="flex items-center gap-3"><span class="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg viewBox="0 0 24 24" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z" /></svg></span><div><h2 class="font-semibold text-slate-800">115 登录配置</h2><p class="mt-1 text-xs text-slate-400">凭证加密保存在服务器</p></div></div>
          <div class="flex items-center gap-3"><span class="rounded-full px-3 py-1 text-xs font-medium" :class="statusClass" role="status">{{ statusText }}</span><button class="text-xs text-slate-500 hover:text-blue-600 disabled:opacity-40" :disabled="!!busy" @click="loadStatus">刷新状态</button></div>
        </header>
        <div class="px-5 py-6 sm:px-7">
          <div class="mb-6 flex w-fit gap-1 rounded-lg bg-slate-100 p-1" role="tablist" aria-label="登录方式">
            <button v-for="tab in [{ id: 'cookie', label: 'Cookie 登录' }, { id: 'qrcode', label: '扫码登录' }]" :key="tab.id" role="tab" :aria-selected="mode === tab.id" :disabled="saving" class="rounded-md px-5 py-2 text-sm font-medium transition disabled:opacity-40" :class="mode === tab.id ? 'bg-white text-blue-600 shadow-sm' : 'text-slate-500 hover:text-slate-800'" @click="mode = tab.id as Mode">{{ tab.label }}</button>
          </div>

          <form v-if="mode === 'cookie'" class="space-y-5" @submit.prevent="saveCookie">
            <div class="flex flex-wrap items-center justify-between gap-2"><label for="p115-cookie" class="text-sm font-medium text-slate-700">Cookie</label><button type="button" class="text-xs text-blue-600 disabled:opacity-40" :disabled="!!busy" @click="loadCookie">{{ busy === 'load' ? '载入中…' : '载入已保存的 Cookie' }}</button></div>
            <div class="flex items-center rounded-xl border border-slate-200 bg-slate-50 focus-within:border-blue-500 focus-within:ring-2 focus-within:ring-blue-100">
              <input id="p115-cookie" v-model="cookie" :type="visible ? 'text' : 'password'" autocomplete="off" spellcheck="false" :disabled="!!busy" class="min-w-0 flex-1 bg-transparent px-4 py-3.5 font-mono text-sm outline-none" :placeholder="configured ? '已保存 Cookie；填写新值以替换' : '粘贴 UID=…; CID=…; SEID=…'" aria-describedby="cookie-help" @input="message = ''" />
              <button type="button" class="shrink-0 px-4 py-3 text-xs font-medium text-slate-500 hover:text-blue-600" :aria-label="visible ? '隐藏 Cookie' : '显示 Cookie'" :aria-pressed="visible" @click="visible = !visible">{{ visible ? '隐藏' : '显示' }}</button>
            </div>
            <p id="cookie-help" class="text-xs leading-6 text-slate-500">填写完整的 Cookie 内容。留空点击测试时，验证服务器已保存的凭证；输入新值时仅测试新值，保存后才会替换。</p>
            <div class="flex flex-wrap gap-3"><button type="button" class="rounded-lg bg-blue-50 px-5 py-2.5 text-sm font-medium text-blue-600 hover:bg-blue-100 disabled:opacity-50" :disabled="!!busy" @click="testCookie">{{ busy === 'test' ? '测试中…' : '测试 Cookie' }}</button><button type="submit" class="rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50" :disabled="!!busy || !cookie.trim()">{{ busy === 'save' ? '验证并保存中…' : '验证并保存' }}</button></div>
          </form>

          <div v-else class="space-y-5">
            <label class="block text-sm font-medium text-slate-700">客户端类型<select v-model="clientType" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 px-4 py-3 text-sm outline-none focus:border-blue-500"><option value="alipay-miniapp">支付宝小程序</option><option value="wechat-miniapp">微信小程序</option><option value="115-life-android">115生活 Android</option><option value="115-android">115 Android</option><option value="115-life-ios">115生活 iOS</option><option value="115-ios">115 iOS</option></select></label>
            <div class="flex flex-col items-center gap-5 rounded-xl border border-slate-100 bg-slate-50 p-6 sm:flex-row">
              <div class="flex aspect-square w-full max-w-52 shrink-0 items-center justify-center sm:h-52 sm:w-52 rounded-xl border border-slate-200 bg-white"><img v-if="qrImage" :src="qrImage" alt="115 登录二维码" class="h-full w-full rounded-xl" /><svg v-else class="h-16 w-16 text-slate-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 3h6v6H3zM15 3h6v6h-6zM3 15h6v6H3zM15 15h2v2h-2zM21 13v4h-2M13 21h4v-2M21 21h-2M3 12h9V3" /></svg></div>
              <div class="text-center sm:text-left"><h3 class="font-medium text-slate-800" role="status">{{ qrStatus }}</h3><p class="mt-2 text-xs leading-6 text-slate-500">选择需要登录的客户端，使用手机 115 App 扫码并确认。切换类型后需要重新获取二维码。</p><button class="mt-4 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-medium text-white disabled:opacity-50" :disabled="!!busy" @click="createQRCode">{{ busy === 'qrcode' ? '获取中…' : qrImage ? '刷新二维码' : '获取二维码' }}</button></div>
            </div>
          </div>

          <p v-if="message" :role="tone === 'error' ? 'alert' : 'status'" class="mt-5 rounded-lg px-4 py-3 text-sm leading-6" :class="tone === 'error' ? 'bg-red-50 text-red-700' : tone === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-blue-50 text-blue-700'">{{ message }}</p>
        </div>
        <footer class="border-t border-slate-100 bg-slate-50/60 px-5 py-4 sm:px-7">
          <div v-if="confirmDisconnect" class="flex flex-wrap items-center gap-3 text-sm"><span class="flex-1 text-slate-600">清除登录信息后，分享播放将暂时不可用。</span><button class="text-slate-500" @click="confirmDisconnect = false">取消</button><button class="rounded-lg bg-red-600 px-3 py-2 text-white" @click="disconnect">确认清除</button></div>
          <div v-else class="flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-slate-400">{{ configured ? '重启后自动恢复已保存的凭证' : '扫码或保存有效 Cookie 后即可开始使用' }}</p><button class="text-xs text-red-500 hover:text-red-700 disabled:opacity-40" :disabled="!!busy || status === 'empty'" @click="confirmDisconnect = true">{{ busy === 'disconnect' ? '清除中…' : '清除登录信息' }}</button></div>
        </footer>
      </section>
  </ManagementLayout>
</template>
