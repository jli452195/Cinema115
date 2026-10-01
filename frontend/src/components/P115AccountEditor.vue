<script setup lang="ts">
import P115AccountCard from '../components/P115AccountCard.vue'
import P115PlaybackDirectory from './P115PlaybackDirectory.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { p115LoginClients } from '../lib/p115-login-clients'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string; account: P115Account }>()
const emit = defineEmits<{ changed: [] }>()
const name = ref(props.account.name)
let knownRevision = props.account.revision
const confirmDelete = ref(false)
type Mode = 'cookie' | 'qrcode'
type Status = 'checking' | 'empty' | 'valid' | 'invalid' | 'unknown'
const mode = ref<Mode>('cookie')
const clientType = ref('alipay-miniapp')
const cookie = ref('')
const replacementDirectory = ref('')
const visible = ref(false)
const configured = ref(false)
const status = ref<Status>('checking')
const busy = ref('')
const message = ref('')
const tone = ref<'info' | 'success' | 'error'>('info')
const qrImage = ref('')
const qrStatus = ref('获取二维码后，使用 115 App 扫码确认')
const confirmDisconnect = ref(false)
const statusText = computed(() => ({ checking: '检查中', empty: '未配置', valid: '已连接', invalid: '已失效', unknown: '待验证' })[status.value])
const statusClass = computed(() => status.value === 'valid' ? 'bg-emerald-50 text-emerald-700' : status.value === 'invalid' ? 'bg-red-50 text-red-700' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 dark:text-slate-500')
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
  const response = await fetch(`/api/admin/p115/accounts/${props.account.id}${path ? "/" + path : ""}`, {
    method, cache: 'no-store', signal: AbortSignal.any([...(controller ? [controller.signal] : []), AbortSignal.timeout(45000)]),
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (response.status === 204) return {}
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) {
    if (response.status === 409) emit('changed')
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
  if (!name.value.trim()) { notify('请填写账号名称', 'error'); return }
  const id = begin('save')
  try {
    const data = await request('', 'PUT', { name: name.value, cookie: cookie.value.trim(), revision: props.account.revision, directoryId: replacementDirectory.value.trim() })
    if (id !== generation) return
    cookie.value = ''; visible.value = false; replacementDirectory.value = ''
    knownRevision = props.account.revision + 1
    configured.value = data.configured; status.value = data.connected ? 'valid' : configured.value ? 'unknown' : 'empty'
    emit('changed')
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
    await request('login', 'DELETE', { revision: props.account.revision })
    knownRevision = props.account.revision + 1
    emit('changed')
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
    const data = await request('qrcode', 'POST', { clientType: clientType.value, directoryId: replacementDirectory.value.trim() })
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
      knownRevision = props.account.revision + 1
      emit('changed')
      configured.value = true; status.value = 'valid'; cookie.value = ''; visible.value = false; qrImage.value = ''
      replacementDirectory.value = ''; qrStatus.value = '扫码登录成功'; notify('登录信息已加密保存，重启后自动恢复。', 'success')
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

async function remove() {
  const id = begin('delete')
  try { await request('', 'DELETE', { revision: props.account.revision }); emit('changed') }
  catch (error) { fail(error, id) }
  finally { if (id === generation) busy.value = ''; confirmDelete.value = false }
}

// 刷新另一标签页保存的配置时，废弃旧二维码及已载入的凭据，保留未保存的名称草稿。
watch(() => props.account, (next, previous) => {
  if (name.value === previous.name) name.value = next.name
  configured.value = next.configured
  if (next.revision !== knownRevision) {
    cancel(); cookie.value = ''; visible.value = false; qrImage.value = ''
    status.value = next.configured ? 'unknown' : 'empty'
    qrStatus.value = '账号配置已更新，请重新获取二维码'
  }
  knownRevision = next.revision
})
watch([mode, clientType], () => {
  cancel(); qrImage.value = ''; message.value = ''; visible.value = false; confirmDisconnect.value = false
  qrStatus.value = '获取二维码后，使用 115 App 扫码确认'
  if (status.value === 'checking') status.value = 'unknown'
})
onMounted(() => { configured.value = props.account.configured; status.value = configured.value ? 'unknown' : 'empty' })
onBeforeUnmount(() => { cancel(); cookie.value = ''; visible.value = false })
</script>

<template>
      <P115AccountCard :name="account.name" :config-id="account.id" :status="statusText" :status-class="statusClass">
        <template #actions>
          <button type="button" :disabled="!!busy || !name.trim()" class="rounded-lg bg-blue-600 px-3 py-2 text-xs font-medium text-white disabled:opacity-40" @click="saveCookie">{{ busy === 'save' ? '保存中…' : '保存配置' }}</button>
          <button type="button" :disabled="!!busy" :aria-pressed="mode === 'cookie'" class="rounded-lg px-3 py-2 text-xs font-medium disabled:opacity-40" :class="mode === 'cookie' ? 'bg-blue-50 text-blue-600' : 'text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950'" @click="mode = 'cookie'">Cookie 配置</button>
          <button type="button" :disabled="!!busy" :aria-pressed="mode === 'qrcode'" class="rounded-lg px-3 py-2 text-xs font-medium disabled:opacity-40" :class="mode === 'qrcode' ? 'bg-sky-50 text-sky-600' : 'text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950'" @click="mode = 'qrcode'">扫码登录</button>
          <button type="button" :disabled="!!busy" class="rounded-lg border border-blue-100 px-3 py-2 text-xs font-medium text-blue-600 hover:bg-blue-50 disabled:opacity-40" @click="testCookie">{{ busy === 'test' ? '测试中…' : '测试 Cookie' }}</button>
          <button type="button" :disabled="!!busy" class="ml-auto rounded-lg px-2 py-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 disabled:opacity-40" @click="loadStatus">刷新状态</button>
        </template>
        <p class="text-xs leading-6 text-slate-500">{{ account.primary ? '更换主账号后，需要重新配置各功能目录。' : '换号会同时影响此小号的 ' + (account.userCount || 0) + ' 位用户；验证失败保留原账号。' }}</p>
        <label v-if="!account.primary && account.uid" class="block text-sm text-slate-600 dark:text-slate-300">换号时的新网盘目录编号<input v-model="replacementDirectory" :disabled="!!busy || !!qrImage" inputmode="numeric" placeholder="同账号重新登录无需填写" class="mt-2 block w-full rounded-lg border border-slate-200 bg-transparent px-3 py-2 dark:border-slate-700" /></label>
        <form v-if="mode === 'cookie'" class="space-y-5" @submit.prevent="saveCookie">
          <div><h3 class="text-xs font-medium text-slate-400 dark:text-slate-500">基础配置</h3><p class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ account.uid ? '115 用户 ID：' + account.uid : '尚未保存登录凭证' }}</p></div>
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">名称<input v-model="name" maxlength="50" :disabled="!!busy" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-4 py-3 text-sm outline-none focus:border-blue-500" /></label>
          <div>
            <div class="mb-2 flex flex-wrap items-center justify-between gap-2"><label :for="'p115-cookie-' + account.id" class="text-sm font-medium text-slate-700 dark:text-slate-300">Cookie</label><button type="button" class="text-xs text-blue-600 disabled:opacity-40" :disabled="!!busy" @click="loadCookie">{{ busy === 'load' ? '载入中…' : '载入已保存的 Cookie' }}</button></div>
            <div class="flex items-center rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 focus-within:border-blue-500 focus-within:ring-2 focus-within:ring-blue-100">
              <input :id="'p115-cookie-' + account.id" v-model="cookie" :type="visible ? 'text' : 'password'" autocomplete="off" spellcheck="false" :disabled="!!busy" class="min-w-0 flex-1 bg-transparent px-4 py-3.5 font-mono text-sm outline-none" :placeholder="configured ? '已保存 Cookie；填写新值以替换' : '粘贴 UID=…; CID=…; SEID=…'" :aria-describedby="'cookie-help-' + account.id" @input="message = ''" />
              <button type="button" class="shrink-0 px-4 py-3 text-xs font-medium text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:text-blue-600" :aria-label="visible ? '隐藏 Cookie' : '显示 Cookie'" :aria-pressed="visible" @click="visible = !visible">{{ visible ? '隐藏' : '显示' }}</button>
            </div>
            <p :id="'cookie-help-' + account.id" class="mt-2 text-xs leading-6 text-slate-400 dark:text-slate-500">Cookie 留空保留原凭证，新 Cookie 验证通过后加密保存。{{ account.primary ? '各项资源管理功能统一使用此主账号。' : '此小号仅用于用户播放和秒传。' }}可直接保存其他账号的凭据完成换号。</p>
          </div>
          <button type="submit" class="sr-only" tabindex="-1" :disabled="!!busy || !name.trim()">保存账号配置</button>
        </form>
        <div v-else class="space-y-5">
          <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">客户端类型<select v-model="clientType" :disabled="!!busy" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-4 py-3 text-sm outline-none focus:border-blue-500"><option v-for="client in p115LoginClients" :key="client.value" :value="client.value">{{ client.label }}</option></select></label>
          <div class="rounded-xl border border-slate-100 dark:border-slate-800 bg-slate-50 dark:bg-slate-950 p-5 text-center">
            <div class="mx-auto flex aspect-square w-full max-w-52 items-center justify-center rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900"><img v-if="qrImage" :src="qrImage" alt="115 登录二维码" class="h-full w-full rounded-xl" /><svg v-else class="h-16 w-16 text-slate-300" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 3h6v6H3zM15 3h6v6h-6zM3 15h6v6H3zM15 15h2v2h-2zM21 13v4h-2M13 21h4v-2M21 21h-2M3 12h9V3" /></svg></div>
            <h3 class="mt-4 text-sm font-medium text-slate-800 dark:text-slate-200" role="status">{{ qrStatus }}</h3><p class="mt-2 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">使用手机 115 App 扫码并确认，成功后自动保存到当前账号配置。</p><button type="button" class="mt-4 rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-medium text-white disabled:opacity-50" :disabled="!!busy" @click="createQRCode">{{ busy === 'qrcode' ? '获取中…' : qrImage ? '刷新二维码' : '获取二维码' }}</button>
          </div>
        </div>
        <P115PlaybackDirectory v-if="!account.primary" :token="token" :account="account" @changed="emit('changed')" />
        <p v-if="message" :role="tone === 'error' ? 'alert' : 'status'" class="rounded-lg px-4 py-3 text-sm leading-6" :class="tone === 'error' ? 'bg-red-50 text-red-700' : tone === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-blue-50 text-blue-700'">{{ message }}</p>
        <template #footer>
          <div v-if="confirmDelete" class="mb-3 flex flex-wrap items-center gap-3 text-sm"><span>{{ account.primary ? '删除主账号后资源管理功能将暂停，需要重新创建主账号并配置目录。' : '删除小号后，绑定的用户需要重新绑定。' }}不会删除网盘文件。</span><button class="text-slate-500 dark:text-slate-400 dark:text-slate-500" @click="confirmDelete = false">取消删除</button><button :disabled="!!busy" class="rounded-lg bg-red-600 px-3 py-2 text-white disabled:opacity-40" @click="remove">确认删除</button></div>
          <div v-if="confirmDisconnect" class="flex flex-wrap items-center gap-3 text-sm"><span class="w-full text-slate-600 dark:text-slate-400 dark:text-slate-500">清除这张卡片的登录凭据？使用它的功能需要重新登录后恢复，也可更换为其他账号。</span><button class="text-slate-500 dark:text-slate-400 dark:text-slate-500" @click="confirmDisconnect = false">取消</button><button :disabled="!!busy" class="rounded-lg bg-red-600 px-3 py-2 text-white disabled:opacity-40" @click="disconnect">确认清除</button></div>
          <div v-else class="flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-slate-400 dark:text-slate-500">{{ configured ? '已保存凭证 · 重启后自动恢复' : '扫码或保存有效 Cookie 后即可使用' }}</p><button class="text-xs text-red-500 hover:text-red-700 disabled:opacity-40" :disabled="!!busy || status === 'empty'" @click="confirmDisconnect = true">{{ busy === 'disconnect' ? '清除中…' : '清除登录信息' }}</button></div>
          <button type="button" :disabled="!!busy" class="mt-3 text-xs text-red-500 disabled:opacity-40" @click="confirmDelete = true">删除配置</button>
        </template>
      </P115AccountCard>
</template>
