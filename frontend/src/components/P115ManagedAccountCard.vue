<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import type { P115Account } from '../types/p115Accounts'
import { loginClientLabel } from '../lib/p115-login-clients'
import { p115Request } from '../lib/p115-api'
import P115QRDialog from './P115QRDialog.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import P115PoolDetailsDialog from './P115PoolDetailsDialog.vue'
import P115ActionDialog from './P115ActionDialog.vue'
import P115Icon from './P115Icon.vue'
import { poolHealthLabel } from '../types/dynamicPool'

const props = defineProps<{ account: P115Account; token: string; startLogin?: boolean; maxSessions?: number; sharedConfigs?: number; poolBusy?: boolean; poolWorking?: boolean; poolError?: string; poolNotice?: string }>()
const emit = defineEmits<{ changed: []; loginStarted: []; interaction: [editing: boolean]; poolMode: [mode: string, revision: number]; poolCheck: [] }>()
const accountName = computed(() => props.account.primary ? '主账号' : props.account.name)
const needsDirectory = computed(() => !props.account.primary && !props.account.playbackParentCid)
const statusLabel = computed(() => !props.account.configured ? '未登录' : needsDirectory.value ? '待设置目录'
  : !props.account.primary ? poolHealthLabel(props.account.pool?.state)
    : checked.value ? connected.value ? '最近检查通过' : '登录失效' : '登录信息已保存')
const statusTone = computed(() => ['登录失效', '目录失效', '待设置目录'].includes(statusLabel.value) ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-400'
  : statusLabel.value === '最近检查通过' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-400'
    : ['限流冷却', '故障冷却', '等待复查'].includes(statusLabel.value) ? 'bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300' : 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400')
const disabled = computed(() => busy.value || props.poolBusy)
const overCapacity = computed(() => props.maxSessions !== undefined && (props.account.pool?.sessions || 0) > props.maxSessions)
const capacityPercent = computed(() => props.maxSessions ? Math.min(100, (props.account.pool?.sessions || 0) / props.maxSessions * 100) : 0)
const infoOpen = ref(false), poolDisable = ref<number>(), primaryCheckedAt = ref(0)
const summaryTitle = computed(() => props.account.summary?.checkedAt ? '更新于 ' + new Date(props.account.summary.checkedAt).toLocaleString() + '；检测可用性可更新' : '登录或检测可用性后更新')
function remainingSpace(bytes: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes, unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return value.toLocaleString(undefined, { maximumFractionDigits: unit ? 2 : 0 }) + ' ' + units[unit]
}
const confirmation = ref<'' | 'clear' | 'delete'>('')
const mutationSnapshot = ref<{ id: string; revision: number }>()
const more = ref(false), details = ref<'' | 'rename'>('')
const menu = ref<HTMLElement>(), menuTrigger = ref<HTMLButtonElement>(), menuContainer = ref<HTMLElement>()
const menuID = useId(), copied = ref(false)
const login = ref<'' | 'qr' | 'cookie'>(''), selecting = ref(false), busy = ref(false)
const checked = ref(false), connected = ref(false), error = ref(''), notice = ref('')
const directorySnapshot = ref<{ id: string; revision: number }>()
const lifecycle = new AbortController()
let noticeTimer: ReturnType<typeof setTimeout> | undefined
let copyTimer: ReturnType<typeof setTimeout> | undefined
let loginRevision = 0
let directoryAfterLogin = false
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
  if (!props.account.primary && props.maxSessions !== undefined) { emit('poolCheck'); return }
  void work(async () => {
    clearTimeout(noticeTimer); notice.value = ''
    checked.value = false; connected.value = false
    const revision = props.account.revision
    const data = await request<{ connected: boolean; clientMessage?: string }>('/status')
    if (revision !== props.account.revision) return
    checked.value = true; connected.value = data.connected
    primaryCheckedAt.value = Date.now()
    if (!data.connected) error.value = '登录已失效，请重新登录'
    else if (data.clientMessage) notify(data.clientMessage)
    emit('changed')
  })
}
function changeMode(event: Event) {
  const select = event.target as HTMLSelectElement, value = select.value
  select.value = props.account.pool?.mode || 'enabled'
  if (disabled.value || value === select.value) return
  if (value === 'disabled') poolDisable.value = props.account.revision
  else emit('poolMode', value, props.account.revision)
}
function confirmDisable() {
  if (poolDisable.value !== undefined) emit('poolMode', 'disabled', poolDisable.value)
  poolDisable.value = undefined
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
function openDetails(mode: 'rename') { closeMenu(); menuTrigger.value?.focus(); error.value = ''; details.value = mode }
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
function selectDirectory() { error.value = ''; directorySnapshot.value = { id: props.account.id, revision: props.account.revision }; selecting.value = true }
function saveDirectory(value: { id: string }) {
  const snapshot = directorySnapshot.value
  if (!snapshot) return
  void work(async () => {
    await p115Request(props.token, 'accounts/' + snapshot.id + '/playback-directory', { method: 'PUT', body: JSON.stringify({ revision: snapshot.revision, parentCid: value.id }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
    selecting.value = false
    emit('changed'); notify(needsDirectory.value ? '秒传目录已保存，小号配置完成' : '秒传目录已保存')
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
function loggedIn() {
  login.value = ''; loginRevision = props.account.revision + 1
  directoryAfterLogin = needsDirectory.value
  checked.value = true; connected.value = true; emit('changed')
  notify(needsDirectory.value ? '登录成功，请设置秒传目录' : '登录信息已保存')
}
watch([login, selecting], () => emit('interaction', !!login.value || selecting.value), { flush: 'sync' })
watch(() => props.startLogin, (start) => {
  if (start) { openLogin('qr'); emit('loginStarted') }
}, { immediate: true })
watch(() => props.account, async (next, previous) => {
  if (next.revision !== previous.revision) {
    login.value = ''; selecting.value = false; confirmation.value = ''; details.value = ''; poolDisable.value = undefined; checked.value = next.revision === loginRevision; primaryCheckedAt.value = 0
    const continueSetup = directoryAfterLogin && checked.value && next.configured && !next.playbackParentCid
    directoryAfterLogin = false
    // Wait for the login dialog to unmount and restore focus/scroll before opening the next dialog.
    await nextTick()
    if (continueSetup && !lifecycle.signal.aborted && props.account.revision === next.revision) selectDirectory()
  }
})
onMounted(() => { document.addEventListener('pointerdown', outside); document.addEventListener('focusin', outside); document.addEventListener('keydown', escapeMenu) })
onBeforeUnmount(() => { emit('interaction', false); lifecycle.abort(); clearTimeout(noticeTimer); clearTimeout(copyTimer); document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', escapeMenu) })
</script>

<template>
  <article class="account-card relative min-w-0 rounded-xl border border-slate-200 bg-white p-5 focus-within:z-10 dark:border-slate-700 dark:bg-slate-900" :class="account.primary ? 'primary-card' : ''">
    <header class="account-heading flex min-w-0 items-center gap-2">
      <div v-if="account.primary" class="mr-1 flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 text-blue-600 dark:border-slate-700 dark:bg-slate-800 dark:text-blue-400"><P115Icon name="drive" /></div>
      <div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2"><h2 class="min-w-0 truncate font-semibold text-slate-900 dark:text-slate-100">{{ accountName }}</h2><span v-if="account.primary" class="rounded-md px-2 py-1 text-[11px]" :class="statusTone">{{ statusLabel }}</span></div><p v-if="account.primary" class="mt-0.5 text-xs text-slate-500 dark:text-slate-400">资源管理与默认播放</p></div>
      <span v-if="!account.primary" class="shrink-0 rounded-md px-2 py-1 text-[11px]" :class="statusTone">{{ statusLabel }}</span>
      <div ref="menuContainer" class="relative shrink-0">
        <button ref="menuTrigger" aria-label="更多操作" :aria-controls="menuID" class="flex h-8 w-8 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800" :aria-expanded="more" @click="toggleMenu"><P115Icon name="more" /></button>
        <div v-if="more" :id="menuID" ref="menu" role="group" aria-label="账号操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900">
          <button class="pool-action" @click="closeMenu(); menuTrigger?.focus(); infoOpen = true">账号详情</button>
          <button v-if="!account.primary" :disabled="disabled" class="pool-action" @click="openDetails('rename')">修改名称</button>
          <button :disabled="disabled" class="pool-action" @click="openLogin('cookie')">手动配置 Cookie</button>
          <div class="my-1 border-t border-slate-100 dark:border-slate-800"></div>
          <button :disabled="disabled || !account.configured" class="pool-action text-red-600 dark:text-red-400" @click="disconnect">清除登录信息</button>
          <button :disabled="disabled" class="pool-action text-red-600 dark:text-red-400" @click="remove">删除配置</button>
        </div>
      </div>
    </header>
    <div class="account-meta min-w-0 text-xs text-slate-500 dark:text-slate-400">
      <p class="truncate leading-6" :title="'UID：' + (account.uid || '未登录') + '，登录客户端：' + (loginClientLabel(account.loginClient || '') || '待识别')">UID：{{ account.uid || '未登录' }}<span class="ml-3">客户端：{{ account.configured ? loginClientLabel(account.loginClient || '') || (checked ? '未识别' : '待识别') : '未登录' }}</span></p>
      <p class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1" :title="summaryTitle"><span :class="account.configured && account.summary?.vip === 'vip' ? 'text-amber-700 dark:text-amber-400' : ''">{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '会员未知' }}</span><span>{{ account.configured && account.summary?.spaceKnown ? '剩余 ' + remainingSpace(account.summary.remainingBytes) : '剩余空间未知' }}</span></p>
      <p v-if="account.primary && primaryCheckedAt" class="mt-1 text-[11px]">上次检查 {{ new Date(primaryCheckedAt).toLocaleTimeString() }}</p>
    </div>
    <template v-if="!account.primary">
      <div v-if="maxSessions !== undefined" class="my-4 rounded-lg bg-slate-50 p-3.5 dark:bg-slate-800/60">
        <div class="mb-2 flex items-baseline justify-between gap-2"><span class="text-xs text-slate-600 dark:text-slate-300">UID 共享名额占用</span><span class="text-lg font-semibold tabular-nums" :class="overCapacity ? 'text-amber-700 dark:text-amber-300' : 'text-slate-900 dark:text-slate-100'">{{ account.pool ? account.pool.sessions : '暂无数据' }}<small class="ml-1 text-xs font-normal text-slate-500 dark:text-slate-400">/ {{ maxSessions }}</small></span></div>
        <div aria-hidden="true" class="mb-2 h-1.5 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700"><div class="h-full rounded-full" :class="overCapacity ? 'bg-amber-500' : 'bg-blue-600 dark:bg-blue-400'" :style="{ width: capacityPercent + '%' }"></div></div>
        <p v-if="account.pool" class="flex justify-between gap-1 text-[11px] text-slate-500 dark:text-slate-400"><span>准备 {{ account.pool.preparing }}</span><span>活跃 {{ account.pool.active || 0 }}</span><span>残余 {{ account.pool.residual }}</span></p>
        <p v-if="overCapacity" class="mt-2 text-[11px] text-amber-700 dark:text-amber-300">超过当前上限，已有会话保留</p>
        <p v-if="(sharedConfigs || 0) > 1" class="mt-2 text-[11px] text-blue-600 dark:text-blue-400">{{ sharedConfigs }} 份配置共用此 UID 名额，容量不累加</p>
      </div>
      <div class="mb-4 border-b border-slate-100 pb-4 text-sm dark:border-slate-800" :class="needsDirectory ? 'mt-4 rounded-lg border border-red-100 bg-red-50/70 p-3 dark:border-red-900/50 dark:bg-red-950/20' : ''">
        <div class="flex flex-wrap items-center gap-2"><p class="min-w-0 flex-1 text-xs text-slate-600 dark:text-slate-300" :class="needsDirectory ? '' : 'truncate'" :title="account.playbackParentCid">秒传目录：<span :class="needsDirectory ? 'font-semibold text-red-600 dark:text-red-400' : 'font-mono'">{{ account.playbackParentCid || '未设置（必填）' }}</span></p><button :disabled="disabled || !account.configured" class="shrink-0 rounded-lg text-xs disabled:opacity-40" :class="needsDirectory ? 'bg-blue-600 px-3 py-2 text-white hover:bg-blue-700' : 'text-blue-600 dark:text-blue-400'" @click="selectDirectory">{{ needsDirectory ? '设置目录' : '修改' }}</button></div>
        <p v-if="needsDirectory" class="mt-2 text-xs leading-5 text-red-700 dark:text-red-300">{{ account.configured ? '设置后才能参与调度，用于保存播放秒传文件。' : '请先登录，再设置秒传目录，完成后才能参与调度。' }}</p>
      </div>
      <label v-if="maxSessions !== undefined" class="flex items-center gap-3 text-xs text-slate-600 dark:text-slate-300">调度模式<select :value="account.pool?.mode || 'enabled'" :disabled="disabled" :aria-label="account.name + ' 调度状态'" class="h-10 min-w-0 flex-1 rounded-lg border border-slate-200 bg-transparent px-3 text-xs disabled:opacity-40 dark:border-slate-700" @change="changeMode"><option value="enabled">参与调度</option><option value="draining">停止新分配</option><option value="disabled">禁用</option></select></label>
      <p class="mb-3 mt-2 text-[11px] leading-5 text-slate-500 dark:text-slate-400">{{ account.pool?.checkedAt ? '上次检查 ' + new Date(account.pool.checkedAt).toLocaleString() : '尚未检查' }}<span v-if="account.pool?.retryAt" class="block">最早复查：{{ new Date(account.pool.retryAt).toLocaleString() }}</span></p>
    </template>
    <div class="account-actions flex flex-wrap items-center gap-2">
      <button :disabled="disabled || !account.configured || (!account.primary && maxSessions !== undefined && needsDirectory)" class="account-button border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" @click="check"><P115Icon name="refresh" />{{ poolWorking ? '处理中…' : busy ? '处理中…' : account.primary ? '检测可用性' : '重新检查' }}</button>
      <button :disabled="disabled" class="account-button border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" :class="!account.configured ? '!border-blue-600 bg-blue-600 !text-white' : ''" @click="openLogin('qr')"><P115Icon name="qr" />扫码登录</button>
    </div>
    <p v-if="notice || poolNotice" role="status" class="account-feedback mt-3 text-xs leading-5 text-slate-600 dark:text-slate-300">{{ notice || poolNotice }}</p>
    <p v-if="(error && !confirmation && !details && !selecting) || poolError" role="alert" class="account-feedback mt-3 text-xs leading-5 text-red-600 dark:text-red-400">{{ poolError || error }}</p>
  </article>
  <P115ActionDialog v-if="infoOpen" :title="accountName + '详情'" @close="infoOpen = false">
    <p>UID：{{ account.uid || '未登录' }}</p><p>登录客户端：{{ loginClientLabel(account.loginClient || '') || '待识别' }}</p><p>{{ summaryTitle }}</p>
    <div class="flex items-center gap-2 text-xs"><span class="shrink-0">配置 ID：</span><span class="min-w-0 flex-1 break-all font-mono">{{ account.id }}</span><button type="button" :aria-label="copied ? '配置 ID 已复制' : '复制配置 ID'" class="shrink-0 rounded border border-slate-200 px-2 py-1 dark:border-slate-700" @click="copyID">{{ copied ? '已复制' : '复制' }}</button></div><p role="status" class="sr-only">{{ copied ? '配置 ID 已复制' : '' }}</p>
    <template #footer><button class="rounded-lg border border-slate-200 px-4 py-2 text-sm dark:border-slate-700" @click="infoOpen = false">关闭</button></template>
  </P115ActionDialog>
  <P115ActionDialog v-if="poolDisable !== undefined" :title="'禁用 ' + account.name + '？'" @close="poolDisable = undefined">
    <p>禁用后拒绝使用此配置的后续取链。已签发的直链无法立即撤回。</p><p class="text-slate-500">网盘文件不会删除。</p><template #footer><button class="px-3 py-2 text-sm" @click="poolDisable = undefined">取消</button><button :disabled="disabled" class="rounded-lg bg-red-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="confirmDisable">确认禁用</button></template>
  </P115ActionDialog>
  <P115PoolDetailsDialog v-if="details" :account="account" :mode="details" :busy="busy" :error="error" @close="details = ''" @save="saveName" />
  <P115QRDialog v-if="login" :token="token" :account-id="account.id" :account-name="accountName" :primary="account.primary" :needs-directory="needsDirectory" :revision="account.revision" :mode="login" @connected="loggedIn" @close="login = ''; emit('changed')" @changed="emit('changed')" />
  <P115ActionDialog v-if="confirmation" :title="confirmation === 'clear' ? '清除登录信息' : '删除账号配置'" :busy="busy" @close="confirmation = ''">
    <p v-if="account.primary">{{ confirmation === 'clear' ? '清除主账号凭据后，资源管理与使用主网盘的新播放请求将暂停，重新登录后恢复。' : '删除主账号后，资源管理与使用主网盘的新播放请求将暂停；需要重新创建主账号并配置各功能目录。' }}</p>
    <p v-else>{{ confirmation === 'clear' ? '清除“' + account.name + '”的登录凭据后，新取链将尝试池内其他可用账号。' : '删除“' + account.name + '”配置后，新取链将尝试池内其他可用账号。' }}</p>
    <p class="text-slate-500">不会删除网盘文件。</p>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <template #footer><button :disabled="busy" class="px-4 py-2 text-sm" @click="confirmation = ''">取消</button><button :disabled="busy" class="rounded-lg bg-red-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="applyMutation">{{ busy ? '处理中…' : confirmation === 'clear' ? '确认清除' : '确认删除' }}</button></template>
  </P115ActionDialog>
  <P115DirectoryDialog v-if="selecting" :token="token" :account-id="account.id" :endpoint="'accounts/' + account.id + '/paths'" :title="(needsDirectory ? '设置 ' : '修改 ') + account.name + ' 的秒传目录'" :description="(needsDirectory ? '设置秒传目录后才能参与调度。' : '') + '秒传文件直接保存到所选目录，不创建子目录，文件由你手动清理。可明确选择根目录。'" :saving="busy" :save-error="error" :confirm-label="needsDirectory ? '保存目录并完成配置' : '保存目录'" @close="selecting = false" @select="saveDirectory" />
</template>

<style scoped>
.pool-action { display: block; width: 100%; border-radius: 0.5rem; padding: 0.625rem 0.75rem; text-align: left; }
.pool-action:hover, .pool-action:focus-visible { background: rgb(148 163 184 / 12%); }
.pool-action:disabled { opacity: 0.4; }
.account-meta { margin-top:10px; }
.account-button { display:inline-flex; min-height:38px; align-items:center; justify-content:center; gap:6px; border-radius:8px; padding:8px 11px; font-size:12px; }
.account-button:disabled { opacity:.4; }
.account-button:hover:not(:disabled) { background:rgb(148 163 184 / 8%); }
.primary-card { display:flex; flex-wrap:wrap; align-items:center; gap:20px; }
.primary-card .account-heading { min-width:260px; flex:none; }
.primary-card .account-meta { margin-top:0; flex:1; }
.primary-card .account-actions { margin-left:auto; }
.primary-card .account-feedback { flex-basis:100%; margin-top:0; }
@media(max-width:767px) { .primary-card { gap:14px; } .primary-card .account-heading { flex-basis:100%; min-width:0; } .primary-card .account-meta { flex-basis:100%; } .primary-card .account-actions { margin-left:0; } }
</style>
