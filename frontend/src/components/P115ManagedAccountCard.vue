<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import type { P115Account } from '../types/p115Accounts'
import { loginClientLabel } from '../lib/p115-login-clients'
import { p115Request } from '../lib/p115-api'
import P115QRDialog from './P115QRDialog.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import P115PoolDetailsDialog from './P115PoolDetailsDialog.vue'
import P115ActionDialog from './P115ActionDialog.vue'

const props = defineProps<{ account: P115Account; token: string }>()
const emit = defineEmits<{ changed: [] }>()
const accountName = computed(() => props.account.primary ? '主账号' : props.account.name)
const summaryTitle = computed(() => props.account.summary?.checkedAt ? '更新于 ' + new Date(props.account.summary.checkedAt).toLocaleString() + '；检测可用性可更新' : '登录或检测可用性后更新')
function remainingSpace(bytes: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes, unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return value.toLocaleString(undefined, { maximumFractionDigits: unit ? 2 : 0 }) + ' ' + units[unit]
}
const confirmation = ref<'' | 'clear' | 'delete'>('')
const mutationSnapshot = ref<{ id: string; revision: number }>()
const more = ref(false), details = ref<'' | 'users' | 'rename'>('')
const menu = ref<HTMLElement>(), menuTrigger = ref<HTMLButtonElement>(), menuContainer = ref<HTMLElement>()
const menuID = useId(), copied = ref(false)
const login = ref<'' | 'qr' | 'cookie'>(''), selecting = ref(false), busy = ref(false)
const checked = ref(false), connected = ref(false), error = ref(''), notice = ref('')
const directorySnapshot = ref<{ id: string; revision: number }>()
const lifecycle = new AbortController()
let noticeTimer: ReturnType<typeof setTimeout> | undefined
let copyTimer: ReturnType<typeof setTimeout> | undefined
let loginRevision = 0
function notify(value: string) { notice.value = value; clearTimeout(noticeTimer); noticeTimer = setTimeout(() => notice.value = '', 3500) }
function request<T>(suffix: string, method = 'GET', body?: object) {
  return p115Request<T>(props.token, 'accounts/' + props.account.id + suffix, { method, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
}
async function work(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await action() } catch (reason) {
    if (lifecycle.signal.aborted) return
    error.value = reason instanceof Error ? reason.message : '操作失败，请重试'
    if ([404, 409].includes((reason as { status?: number })?.status || 0)) emit('changed')
  } finally { busy.value = false }
}
function check() {
  void work(async () => {
    clearTimeout(noticeTimer); notice.value = ''
    checked.value = false; connected.value = false
    const revision = props.account.revision
    const data = await request<{ connected: boolean; clientMessage?: string }>('/status')
    if (revision !== props.account.revision) return
    checked.value = true; connected.value = data.connected
    if (!data.connected) error.value = '登录已失效，请重新登录'
    else if (data.clientMessage) notify(data.clientMessage)
    emit('changed')
  })
}
function saveName(name: string) {
  void work(async () => { await request('', 'PUT', { name, revision: props.account.revision }); details.value = ''; emit('changed'); notify('名称已保存') })
}
function closeMenu() { more.value = false }
async function toggleMenu() {
  more.value = !more.value
  if (more.value) { await nextTick(); menu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus() }
}
function outside(event: Event) { if (event.target instanceof Node && !menuContainer.value?.contains(event.target)) closeMenu() }
function escapeMenu(event: KeyboardEvent) { if (event.key === 'Escape' && more.value) { event.preventDefault(); closeMenu(); menuTrigger.value?.focus() } }
function openDetails(mode: 'users' | 'rename') { closeMenu(); if (mode === 'rename') menuTrigger.value?.focus(); error.value = ''; details.value = mode }
async function copyID() {
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(props.account.id)
    else {
      // LAN HTTP pages lack the Clipboard API; retain a user-initiated fallback.
      const input = document.createElement('textarea'), focus = document.activeElement
      input.value = props.account.id; input.style.position = 'fixed'; input.style.opacity = '0'; document.body.append(input)
      try { input.select(); if (!document.execCommand('copy')) throw new Error('copy failed') }
      finally { input.remove(); if (focus instanceof HTMLElement) focus.focus() }
    }
    copied.value = true; clearTimeout(copyTimer); copyTimer = setTimeout(() => copied.value = false, 2500)
  } catch { error.value = '复制失败，请选择配置 ID 手动复制' }
}
function selectDirectory() { directorySnapshot.value = { id: props.account.id, revision: props.account.revision }; selecting.value = true }
function saveDirectory(value: { id: string }) {
  selecting.value = false
  const snapshot = directorySnapshot.value
  if (!snapshot) return
  void work(async () => {
    await p115Request(props.token, 'accounts/' + snapshot.id + '/playback-directory', { method: 'PUT', body: JSON.stringify({ revision: snapshot.revision, parentCid: value.id }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
    emit('changed'); notify('秒传目录已保存')
  })
}
function disconnect() {
  confirmMutation('clear')
}
function remove() {
  confirmMutation('delete')
}
function confirmMutation(action: 'clear' | 'delete') {
  menuTrigger.value?.focus(); closeMenu(); error.value = ''
  mutationSnapshot.value = { id: props.account.id, revision: props.account.revision }; confirmation.value = action
}
function applyMutation() {
  const snapshot = mutationSnapshot.value, action = confirmation.value
  if (!snapshot || !action) return
  void work(async () => {
    await p115Request(props.token, 'accounts/' + encodeURIComponent(snapshot.id) + (action === 'clear' ? '/login' : ''), { method: 'DELETE', body: JSON.stringify({ revision: snapshot.revision }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
    confirmation.value = ''; checked.value = false; emit('changed')
  })
}
function openLogin(mode: 'qr' | 'cookie') { if (more.value) menuTrigger.value?.focus(); closeMenu(); error.value = ''; login.value = mode }
function loggedIn() { login.value = ''; loginRevision = props.account.revision + 1; checked.value = true; connected.value = true; emit('changed'); notify('登录信息已保存') }
watch(() => props.account, (next, previous) => {
  if (next.revision !== previous.revision) {
    login.value = ''; selecting.value = false; confirmation.value = ''; details.value = ''; checked.value = next.revision === loginRevision
  }
})
onMounted(() => { document.addEventListener('pointerdown', outside); document.addEventListener('focusin', outside); document.addEventListener('keydown', escapeMenu) })
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(noticeTimer); clearTimeout(copyTimer); document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', escapeMenu) })
</script>

<template>
  <article class="flex min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
    <header class="flex items-center gap-2">
      <h2 class="min-w-0 flex-1 truncate font-semibold text-slate-900 dark:text-slate-100">{{ accountName }}</h2>
      <span class="shrink-0 rounded-full px-2.5 py-1 text-xs" :class="checked && connected ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500 dark:bg-slate-800'">{{ !account.configured ? '未登录' : checked ? connected ? '已连接' : '已失效' : '待验证' }}</span>
      <div ref="menuContainer" class="relative shrink-0">
        <button ref="menuTrigger" aria-label="更多操作" :aria-controls="menuID" class="rounded-lg p-2 text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800" :aria-expanded="more" @click="toggleMenu"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.5" /><circle cx="12" cy="12" r="1.5" /><circle cx="19" cy="12" r="1.5" /></svg></button>
        <div v-if="more" :id="menuID" ref="menu" role="group" aria-label="账号操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900">
          <button v-if="!account.primary" :disabled="busy" class="pool-action text-slate-700 dark:text-slate-200" @click="openDetails('rename')">修改名称</button>
          <button :disabled="busy" class="pool-action text-slate-700 dark:text-slate-200" @click="openLogin('cookie')">手动配置 Cookie</button>
          <div class="my-1 border-t border-slate-100 dark:border-slate-800"></div>
          <button :disabled="busy || !account.configured" class="pool-action text-red-600 dark:text-red-400" @click="disconnect">清除登录信息</button>
          <button :disabled="busy" class="pool-action text-red-600 dark:text-red-400" @click="remove">删除配置</button>
        </div>
      </div>
    </header>
    <p class="mt-3 truncate text-xs leading-6 text-slate-500" :title="'UID：' + (account.uid || '未登录') + ' · 登录客户端：' + (loginClientLabel(account.loginClient || '') || '待识别')">UID：{{ account.uid || '未登录' }} · 登录客户端：{{ account.configured ? loginClientLabel(account.loginClient || '') || (checked ? '未识别' : '待识别') : '未登录' }}</p>
    <p class="mt-2 flex items-center gap-2 text-xs text-slate-500" :title="summaryTitle"><span :class="account.configured && account.summary?.vip === 'vip' ? 'text-amber-700 dark:text-amber-400' : ''">{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '会员未知' }}</span><span aria-hidden="true">·</span><span>{{ account.configured && account.summary?.spaceKnown ? '剩余 ' + remainingSpace(account.summary.remainingBytes) : '剩余空间未知' }}</span></p>
    <p v-if="account.primary" class="mt-3 text-sm text-slate-500">用于资源管理与默认播放</p>
    <template v-else>
      <button class="mt-2 inline-flex items-center gap-1 self-start text-sm text-blue-600" aria-haspopup="dialog" @click="openDetails('users')">{{ account.userCount || 0 }} 人使用<svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 6 6 6-6 6" /></svg></button>
      <div class="mt-3 flex items-center gap-3 text-sm"><p class="min-w-0 flex-1 truncate text-slate-600 dark:text-slate-300" :title="account.playbackParentCid">秒传目录：{{ account.playbackParentCid || '未设置' }}</p><button :disabled="busy || !account.configured" class="shrink-0 text-blue-600 disabled:opacity-40" @click="selectDirectory">{{ account.playbackParentCid ? '修改' : '设置目录' }}</button></div>
    </template>
    <div class="mt-4 flex flex-wrap gap-3"><button :disabled="busy" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="openLogin('qr')">扫码登录</button><button :disabled="busy || !account.configured" class="rounded-lg border border-slate-200 px-4 py-2 text-sm text-blue-600 disabled:opacity-40 dark:border-slate-700" @click="check">{{ busy ? '处理中…' : '检测可用性' }}</button></div>
    <footer class="mt-4 flex items-center gap-2 border-t border-slate-100 pt-3 text-xs text-slate-500 dark:border-slate-800">
      <span class="shrink-0">配置 ID：</span><span class="flex min-w-0 select-text font-mono" :title="account.id"><span class="truncate">{{ account.id.slice(0, -6) }}</span><span class="shrink-0">{{ account.id.slice(-6) }}</span></span>
      <button type="button" :aria-label="copied ? '配置 ID 已复制' : '复制配置 ID'" :title="copied ? '已复制' : '复制配置 ID'" class="ml-auto flex h-7 w-7 shrink-0 items-center justify-center rounded text-slate-500 hover:bg-slate-100 hover:text-blue-600 dark:hover:bg-slate-800" @click="copyID"><svg v-if="copied" aria-hidden="true" class="h-4 w-4 text-emerald-600" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 4 4L19 6" /></svg><svg v-else aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="8" y="8" width="12" height="13" rx="2" /><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" /></svg></button>
      <span role="status" class="sr-only">{{ copied ? '配置 ID 已复制' : '' }}</span>
    </footer>
    <p v-if="notice" role="status" class="mt-3 text-xs text-emerald-600">{{ notice }}</p>
    <p v-if="error && !confirmation && !details" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
  </article>
  <P115PoolDetailsDialog v-if="details" :account="account" :mode="details" :busy="busy" :error="error" @close="details = ''" @save="saveName" />
  <P115QRDialog v-if="login" :token="token" :account-id="account.id" :account-name="accountName" :primary="account.primary" :revision="account.revision" :user-count="account.userCount || 0" :mode="login" @connected="loggedIn" @close="login = ''; emit('changed')" @changed="emit('changed')" />
  <P115ActionDialog v-if="confirmation" :title="confirmation === 'clear' ? '清除登录信息' : '删除账号配置'" :busy="busy" @close="confirmation = ''">
    <p v-if="account.primary">{{ confirmation === 'clear' ? '清除主账号凭据后，资源管理与使用主网盘的新播放请求将暂停，重新登录后恢复。' : '删除主账号后，资源管理与使用主网盘的新播放请求将暂停；需要重新创建主账号并配置各功能目录。' }}</p>
    <p v-else>{{ confirmation === 'clear' ? '清除“' + account.name + '”的登录凭据后，绑定用户的新播放请求将暂停。' : '删除“' + account.name + '”配置后，绑定用户需要重新分配账号。' }}</p>
    <p class="text-slate-500">不会删除网盘文件。</p>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <template #footer><button :disabled="busy" class="px-4 py-2 text-sm" @click="confirmation = ''">取消</button><button :disabled="busy" class="rounded-lg bg-red-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="applyMutation">{{ busy ? '处理中…' : confirmation === 'clear' ? '确认清除' : '确认删除' }}</button></template>
  </P115ActionDialog>
  <P115DirectoryDialog v-if="selecting" :token="token" :account-id="account.id" :endpoint="'accounts/' + account.id + '/paths'" :title="'修改 ' + account.name + ' 的秒传目录'" description="秒传文件直接保存到所选目录，不创建子目录，文件由你手动清理。" @close="selecting = false" @select="saveDirectory" />
</template>

<style scoped>
.pool-action { display: block; width: 100%; border-radius: 0.5rem; padding: 0.625rem 0.75rem; text-align: left; }
.pool-action:hover, .pool-action:focus-visible { background: rgb(148 163 184 / 12%); }
.pool-action:disabled { opacity: 0.4; }
</style>
