<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { IconChevronDown, IconCopy, IconPencil, IconSitemap } from '@tabler/icons-vue'
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
const menuID = useId(), copied = ref(false), uidCopied = ref(false)
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
async function copyValue(value: string, target: 'id' | 'uid') {
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(value)
    else {
      // LAN HTTP pages lack the Clipboard API; retain a user-initiated fallback.
      const input = document.createElement('textarea'), focus = document.activeElement
      input.value = value; input.style.position = 'fixed'; input.style.opacity = '0'; document.body.append(input)
      try { input.select(); if (!document.execCommand('copy')) throw new Error('copy failed') }
      finally { input.remove(); if (focus instanceof HTMLElement) focus.focus() }
    }
    copied.value = target === 'id'; uidCopied.value = target === 'uid'
    clearTimeout(copyTimer); copyTimer = setTimeout(() => { copied.value = false; uidCopied.value = false }, 2500)
  } catch { error.value = '复制失败，请选择' + (target === 'id' ? '配置 ID' : ' UID') + ' 手动复制' }
}
function copyID() { void copyValue(props.account.id, 'id') }
function copyUID() { if (props.account.uid) void copyValue(props.account.uid, 'uid') }
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
  <article class="account-card relative min-w-0 rounded-xl border border-slate-200 bg-white p-5 focus-within:z-10 dark:border-slate-700 dark:bg-slate-900" :class="account.primary ? 'primary-card' : 'pool-card'">
    <header class="account-heading flex min-w-0 items-center gap-2" :class="!account.primary ? 'pr-10' : ''">
      <div v-if="account.primary" class="primary-icon flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-slate-200/80 bg-slate-100/60 text-slate-600 dark:border-slate-600/50 dark:bg-slate-700/30 dark:text-slate-300"><P115Icon name="drive" /></div>
      <div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2"><h2 class="min-w-0 truncate font-semibold text-slate-900 dark:text-slate-100" :title="accountName">{{ accountName }}</h2><span v-if="account.primary" class="primary-status rounded-md text-[11px]" :class="statusTone">{{ statusLabel }}</span><div v-else class="pool-badges"><span>{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '会员未知' }}</span><span :title="'登录客户端：' + (loginClientLabel(account.loginClient || '') || '待识别')">{{ account.configured ? loginClientLabel(account.loginClient || '') || '待识别' : '未登录' }}</span></div></div><p v-if="account.primary" class="mt-1 text-xs text-slate-500 dark:text-slate-400">资源管理与默认播放</p></div>
      <span v-if="!account.primary" class="pool-status shrink-0 text-[11px]" :class="statusTone" :title="statusLabel"><svg aria-hidden="true" viewBox="0 0 8 8"><circle cx="4" cy="4" r="3" fill="currentColor" /></svg><span>{{ statusLabel === '最近检查通过' ? '检查通过' : statusLabel }}</span></span>
    </header>
    <div v-if="account.primary" class="account-meta min-w-0 text-xs text-slate-500 dark:text-slate-400">
      <dl class="primary-metadata">
        <div class="primary-meta-field"><dt>UID：</dt><dd class="tabular-nums">{{ account.uid || '未登录' }}</dd></div>
        <div class="primary-meta-field"><dt>客户端：</dt><dd>{{ account.configured ? loginClientLabel(account.loginClient || '') || (checked ? '未识别' : '待识别') : '未登录' }}</dd></div>
        <div :title="summaryTitle"><dt class="sr-only">会员</dt><dd class="primary-vip">{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '会员未知' }}</dd></div>
        <div :title="summaryTitle"><dt class="sr-only">剩余空间</dt><dd class="primary-space"><template v-if="account.configured && account.summary?.spaceKnown"><strong class="font-semibold tabular-nums text-slate-700 dark:text-slate-200">{{ remainingSpace(account.summary.remainingBytes) }}</strong><span>可用</span></template><span v-else>剩余空间未知</span></dd></div>
      </dl>
      <p v-if="account.primary && primaryCheckedAt" class="mt-1 text-[11px]">上次检查 {{ new Date(primaryCheckedAt).toLocaleTimeString() }}</p>
    </div>
    <template v-if="!account.primary">
      <div class="pool-stats" :class="maxSessions === undefined ? 'pool-stats-single' : ''">
        <div class="pool-stat" :title="summaryTitle">
          <template v-if="account.configured && account.summary?.spaceKnown"><p class="pool-stat-value text-slate-900 dark:text-slate-100">{{ remainingSpace(account.summary.remainingBytes).split(' ')[0] }}</p><p class="pool-stat-label">{{ remainingSpace(account.summary.remainingBytes).split(' ')[1] }} 剩余可用</p></template>
          <template v-else><p class="pool-stat-empty">未知</p><p class="pool-stat-label">剩余空间</p></template>
        </div>
        <div v-if="maxSessions !== undefined" class="pool-stat" title="同一 UID 的配置共享逻辑名额，不代表真实 CDN 连接数">
          <p class="pool-stat-value" :class="overCapacity ? 'text-amber-700 dark:text-amber-300' : 'text-slate-900 dark:text-slate-100'">{{ account.pool ? account.pool.sessions : '—' }}<span class="pool-stat-limit">/ {{ maxSessions }}</span></p>
          <p class="pool-stat-label">名额占用</p>
          <p v-if="account.pool" class="pool-session-breakdown"><span>准备 {{ account.pool.preparing }}</span><span>活跃 {{ account.pool.active || 0 }}</span><span>残余 {{ account.pool.residual }}</span></p>
          <p v-else class="pool-session-breakdown">暂无数据</p>
        </div>
      </div>
      <p v-if="overCapacity" class="mt-2 text-[11px] text-amber-700 dark:text-amber-300">超过当前上限，已有会话保留</p>
      <p v-if="(sharedConfigs || 0) > 1" class="mt-2 text-[11px] text-blue-600 dark:text-blue-400">{{ sharedConfigs }} 份配置共用此 UID 名额，容量不累加</p>
      <div class="pool-details">
        <div class="pool-detail-row"><p class="min-w-0 truncate" :title="account.uid">UID：{{ account.uid || '未登录' }}</p><button v-if="account.uid" class="pool-icon-button" :aria-label="uidCopied ? 'UID 已复制' : '复制 UID'" :title="uidCopied ? 'UID 已复制' : '复制 UID'" @click="copyUID"><P115Icon v-if="uidCopied" name="check" /><IconCopy v-else aria-hidden="true" /></button><span role="status" class="sr-only">{{ uidCopied ? 'UID 已复制' : '' }}</span></div>
        <div class="pool-directory" :class="needsDirectory ? 'rounded-lg border border-red-100 bg-red-50/70 p-3 dark:border-red-900/50 dark:bg-red-950/20' : ''">
        <div class="pool-detail-row"><p class="min-w-0 truncate" :title="account.playbackParentCid">秒传目录：<span :class="needsDirectory ? 'font-semibold text-red-600 dark:text-red-400' : 'tabular-nums'">{{ account.playbackParentCid || '未设置（必填）' }}</span></p><button :disabled="disabled || !account.configured" :aria-label="needsDirectory ? '设置目录' : '修改秒传目录'" :title="needsDirectory ? '设置目录' : '修改秒传目录'" class="shrink-0 disabled:opacity-40" :class="needsDirectory ? 'rounded-lg bg-blue-600 px-3 py-2 text-xs text-white hover:bg-blue-700' : 'pool-icon-button'" @click="selectDirectory"><span v-if="needsDirectory">设置目录</span><IconPencil v-else aria-hidden="true" /></button></div>
        <p v-if="needsDirectory" class="mt-2 text-xs leading-5 text-red-700 dark:text-red-300">{{ account.configured ? '设置后才能参与调度，用于保存播放秒传文件。' : '请先登录，再设置秒传目录，完成后才能参与调度。' }}</p>
        </div>
      </div>
      <label v-if="maxSessions !== undefined" class="pool-mode-row"><IconSitemap aria-hidden="true" /><span class="sr-only">调度模式</span><span class="pool-mode-control"><select :value="account.pool?.mode || 'enabled'" :disabled="disabled" :aria-label="account.name + ' 调度状态'" class="pool-mode-select disabled:opacity-40" @change="changeMode"><option value="enabled">参与调度</option><option value="draining">停止新分配</option><option value="disabled">禁用</option></select><IconChevronDown aria-hidden="true" /></span></label>
      <p v-if="account.pool?.retryAt" class="mt-1 text-[11px] leading-5 text-amber-700 dark:text-amber-300">最早复查：{{ new Date(account.pool.retryAt).toLocaleString() }}</p>
    </template>
    <div class="account-controls flex min-w-0 items-center gap-2">
      <div v-if="account.primary" class="account-actions flex flex-wrap items-center gap-2">
        <button :disabled="disabled || !account.configured || (!account.primary && maxSessions !== undefined && needsDirectory)" class="account-button border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" @click="check"><P115Icon name="refresh" />{{ poolWorking ? '处理中…' : busy ? '处理中…' : account.primary ? '检测可用性' : '重新检查' }}</button>
        <button :disabled="disabled" class="account-button border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" :class="!account.configured ? '!border-blue-600 !bg-blue-600 !text-white' : ''" @click="openLogin('qr')"><P115Icon name="qr" />扫码登录</button>
      </div>
      <div v-else class="pool-footer">
        <div class="pool-update"><time v-if="account.pool?.checkedAt" :datetime="new Date(account.pool.checkedAt).toISOString()" :title="'上次检查 ' + new Date(account.pool.checkedAt).toLocaleString()">{{ new Date(account.pool.checkedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) }} 更新</time><span v-else>尚未检查</span><button :disabled="disabled || !account.configured || (maxSessions !== undefined && needsDirectory)" aria-label="重新检查" :aria-busy="!!poolWorking || busy" :title="poolWorking || busy ? '正在重新检查' : '重新检查'" class="pool-icon-button pool-refresh" @click="check"><P115Icon name="refresh" :class="poolWorking || busy ? 'animate-spin' : ''" /></button></div>
        <button :disabled="disabled" class="account-button pool-login border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" :class="!account.configured ? '!border-blue-600 !bg-blue-600 !text-white' : ''" @click="openLogin('qr')"><P115Icon name="qr" />扫码登录</button>
      </div>
      <div ref="menuContainer" class="account-menu relative shrink-0">
        <button ref="menuTrigger" aria-label="更多操作" :aria-controls="menuID" class="account-menu-trigger flex h-8 w-8 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800" :aria-expanded="more" @click="toggleMenu"><P115Icon name="more" /></button>
        <div v-if="more" :id="menuID" ref="menu" role="group" aria-label="账号操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900">
          <button class="pool-action" @click="closeMenu(); menuTrigger?.focus(); infoOpen = true">账号详情</button>
          <button v-if="!account.primary" :disabled="disabled" class="pool-action" @click="openDetails('rename')">修改名称</button>
          <button :disabled="disabled" class="pool-action" @click="openLogin('cookie')">手动配置 Cookie</button>
          <div class="my-1 border-t border-slate-100 dark:border-slate-800"></div>
          <button :disabled="disabled || !account.configured" class="pool-action text-red-600 dark:text-red-400" @click="disconnect">清除登录信息</button>
          <button :disabled="disabled" class="pool-action text-red-600 dark:text-red-400" @click="remove">删除配置</button>
        </div>
      </div>
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
.pool-card { padding:14px; border-radius:15px; box-shadow:0 2px 10px rgb(15 23 42 / 3%); }
.pool-card .account-heading { min-height:28px; gap:7px; padding-right:36px; }
.pool-card .account-heading > div > div { gap:6px; }
.pool-card .account-heading h2 { max-width:100%; font-size:15px; line-height:22px; }
.pool-badges { display:flex; min-width:0; align-items:center; gap:4px; }
.pool-badges span { max-width:92px; overflow:hidden; border-radius:999px; padding:3px 7px; background:rgb(var(--color-slate-100)); color:rgb(var(--color-slate-600)); font-size:10px; font-weight:500; line-height:15px; text-overflow:ellipsis; white-space:nowrap; }
.pool-card .pool-status { display:inline-flex; align-items:center; gap:4px; background:transparent; font-size:10px; line-height:16px; }
.pool-status svg { width:8px; height:8px; flex-shrink:0; }
.pool-stats { display:grid; grid-template-columns:repeat(2, minmax(0, 1fr)); align-items:start; gap:10px; margin-top:14px; }
.pool-stats-single { grid-template-columns:minmax(0, 1fr); }
.pool-stat { min-width:0; min-height:72px; border:1px solid rgb(var(--color-slate-200) / 85%); border-radius:11px; padding:10px; background:rgb(var(--color-slate-50) / 85%); }
.pool-stat-value { display:flex; flex-wrap:wrap; align-items:baseline; gap:5px; font-size:24px; font-weight:650; line-height:28px; font-variant-numeric:tabular-nums; }
.pool-stat-limit { font-size:20px; }
.pool-stat-empty { font-size:18px; font-weight:600; line-height:28px; color:rgb(var(--color-slate-500)); }
.pool-stat-label { margin-top:4px; color:rgb(var(--color-slate-500)); font-size:11px; line-height:16px; }
.pool-session-breakdown { display:flex; flex-wrap:wrap; gap:2px 7px; margin-top:5px; color:rgb(var(--color-slate-400)); font-size:10px; line-height:14px; }
.pool-details { display:grid; grid-template-columns:minmax(0, 1fr); min-width:0; gap:6px; margin-top:14px; color:rgb(var(--color-slate-500)); font-size:11px; }
.pool-directory { min-width:0; }
.pool-detail-row { display:flex; min-width:0; min-height:26px; align-items:center; gap:7px; }
.pool-icon-button { display:inline-flex; width:25px; height:25px; flex-shrink:0; align-items:center; justify-content:center; border:1px solid rgb(var(--color-slate-200)); border-radius:7px; background:rgb(var(--color-slate-50) / 85%); color:rgb(var(--color-slate-500)); }
.pool-icon-button :deep(svg) { width:16px; height:16px; }
.pool-icon-button:hover:not(:disabled) { background:rgb(var(--color-slate-100)); color:rgb(var(--color-slate-700)); }
.pool-icon-button:disabled { opacity:.4; }
.pool-mode-row { display:flex; align-items:center; gap:7px; min-height:30px; margin-top:7px; color:rgb(var(--color-slate-700)); font-size:11px; font-weight:600; }
.pool-mode-row > :deep(svg) { width:16px; height:16px; flex-shrink:0; }
.pool-mode-control { position:relative; display:inline-flex; min-width:0; }
.pool-mode-control > :deep(svg) { position:absolute; right:2px; top:50%; width:13px; height:13px; transform:translateY(-50%); pointer-events:none; }
.pool-mode-select { min-width:0; max-width:100%; appearance:none; border:0; border-radius:5px; padding:5px 20px 5px 0; background:transparent; font:inherit; cursor:pointer; }
.pool-card .account-controls { margin-top:12px; border-top:1px solid rgb(var(--color-slate-200) / 85%); padding-top:10px; }
.pool-footer { display:flex; width:100%; min-width:0; align-items:center; justify-content:space-between; gap:8px; }
.pool-update { display:flex; min-width:0; align-items:center; gap:5px; color:rgb(var(--color-slate-500)); font-size:10px; font-variant-numeric:tabular-nums; }
.pool-refresh { width:26px; height:26px; border-radius:50%; }
.pool-card .pool-login { min-height:30px; padding:5px 9px; background:rgb(var(--color-slate-50) / 85%); font-size:11px; font-weight:600; white-space:nowrap; }
.pool-card .pool-login :deep(svg) { width:15px; height:15px; }
.pool-card .account-menu { position:absolute; top:13px; right:13px; }
.pool-card .account-menu-trigger { width:28px; height:28px; border:1px solid rgb(var(--color-slate-200)); background:rgb(var(--color-slate-50) / 85%); }
.pool-card .account-menu-trigger :deep(svg) { width:16px; height:16px; }
.pool-icon-button:focus-visible, .pool-mode-select:focus-visible { outline:2px solid rgb(59 130 246); outline-offset:2px; }
.dark .pool-badges span { background:rgb(var(--color-slate-800)); color:rgb(var(--color-slate-300)); }
.dark .pool-stat { border-color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-800) / 55%); }
.dark .pool-stat-label, .dark .pool-details, .dark .pool-update { color:rgb(var(--color-slate-400)); }
.dark .pool-mode-row { color:rgb(var(--color-slate-300)); }
.dark .pool-card .account-controls { border-color:rgb(var(--color-slate-700)); }
.dark .pool-icon-button, .dark .pool-card .account-menu-trigger, .dark .pool-card .pool-login { border-color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-800) / 55%); }
.dark .pool-icon-button { color:rgb(var(--color-slate-400)); }
.dark .pool-icon-button:hover:not(:disabled) { background:rgb(var(--color-slate-700)); color:rgb(var(--color-slate-200)); }
.primary-card { display:flex; flex-wrap:wrap; align-items:center; gap:24px; min-height:104px; padding:20px 24px; border-color:rgb(var(--color-slate-300) / 65%); background:rgb(255 255 255 / 72%); box-shadow:inset 0 1px 0 rgb(255 255 255 / 70%), 0 2px 8px rgb(15 23 42 / 2%); -webkit-backdrop-filter:blur(16px); backdrop-filter:blur(16px); }
.primary-card .account-heading { flex:none; gap:14px; }
.primary-icon { box-shadow:inset 0 1px 0 rgb(255 255 255 / 50%); }
.primary-card .primary-status { padding:2px 6px; background:transparent; line-height:18px; box-shadow:inset 0 0 0 1px rgb(148 163 184 / 18%); }
.primary-card .account-meta { flex:1; min-width:208px; margin-top:0; padding-left:24px; border-left:1px solid rgb(148 163 184 / 20%); }
.primary-metadata { display:grid; grid-template-columns:max-content minmax(0, max-content); align-items:center; gap:8px 24px; }
.primary-meta-field { display:flex; flex-wrap:wrap; align-items:baseline; gap:6px; line-height:18px; }
.primary-meta-field dt { color:rgb(var(--color-slate-500)); }
.primary-vip { display:inline-flex; align-items:center; min-height:22px; border:1px solid rgb(148 163 184 / 25%); border-radius:5px; padding:0 7px; background:rgb(148 163 184 / 8%); color:rgb(var(--color-slate-600)); font-size:10px; font-weight:500; letter-spacing:.03em; }
.primary-space { display:flex; flex-wrap:wrap; align-items:baseline; gap:6px; line-height:22px; }
.primary-space strong { font-size:14px; }
.primary-card .account-controls { flex:none; margin-left:auto; }
.primary-card .account-actions { flex-wrap:nowrap; }
.primary-card .account-button { min-height:40px; padding:9px 12px; background:transparent; border-color:rgb(148 163 184 / 30%); white-space:nowrap; }
.primary-card .account-menu-trigger { width:36px; height:40px; }
.primary-card .account-button, .primary-card .account-menu-trigger { transition:background-color 150ms ease, border-color 150ms ease, color 150ms ease; }
.primary-card .account-button:hover:not(:disabled), .primary-card .account-menu-trigger:hover { background:rgb(148 163 184 / 10%); }
.primary-card .account-button:focus-visible, .account-menu-trigger:focus-visible { outline:2px solid rgb(59 130 246); outline-offset:3px; }
.primary-card .account-feedback { flex-basis:100%; margin-top:0; }
.dark .primary-card { border-color:rgb(var(--color-slate-600) / 45%); background:rgb(var(--color-slate-800) / 55%); box-shadow:inset 0 1px 0 rgb(255 255 255 / 3%), 0 2px 8px rgb(0 0 0 / 8%); }
.dark .primary-icon { box-shadow:inset 0 1px 0 rgb(255 255 255 / 4%); }
.dark .primary-meta-field dt { color:rgb(var(--color-slate-400)); }
.dark .primary-vip { background:rgb(var(--color-slate-700) / 30%); border-color:rgb(var(--color-slate-500) / 35%); color:rgb(var(--color-slate-300)); }
@media(max-width:767px) {
  .primary-card { gap:18px; padding:18px; }
  .primary-card .account-heading { flex-basis:100%; min-width:0; }
  .primary-card .account-meta { flex-basis:100%; min-width:0; padding-left:0; border-left:0; }
  .primary-card .primary-metadata { column-gap:28px; }
  .primary-card .account-controls { flex-basis:100%; margin-left:0; padding-top:14px; border-top:1px solid rgb(148 163 184 / 16%); }
  .primary-card .account-actions { flex:1; min-width:0; }
  .primary-card .account-button { flex:1; min-width:0; }
}
@media(max-width:380px) {
  .primary-card { padding:16px; }
  .primary-card .account-button { gap:5px; padding:9px 8px; font-size:11px; }
  .primary-card .account-button :deep(svg) { width:16px; height:16px; }
  .primary-card .account-menu-trigger { width:32px; }
}
@media(prefers-reduced-motion:reduce) { .primary-card .account-button, .primary-card .account-menu-trigger { transition:none; } }
</style>
