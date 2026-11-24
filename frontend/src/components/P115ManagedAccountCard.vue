<script setup lang="ts">
import SelectMenu from './SelectMenu.vue'
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
const primaryCheck = ref<{ state: 'checking' | 'ready' | 'invalid' | 'unavailable'; at?: Date }>()
const primaryStatus = computed(() => {
  if (!props.account.configured) return '未登录'
  if (!primaryCheck.value) return '登录信息已保存'
  return { checking: '正在检测…', ready: '本次检测通过', invalid: '登录失效', unavailable: '暂时无法验证' }[primaryCheck.value.state]
})
const loginNeeded = computed(() => !props.account.configured || primaryCheck.value?.state === 'invalid')
const statusLabel = computed(() => !props.account.configured ? '未登录' : needsDirectory.value ? '待设置目录' : poolHealthLabel(props.account.pool?.state))
const statusTone = computed(() => ['登录失效', '目录失效', '待设置目录'].includes(statusLabel.value) ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-400'
  : statusLabel.value === '最近检查通过' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-400'
    : ['限流冷却', '故障冷却', '等待复查'].includes(statusLabel.value) ? 'bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300' : 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400')
const disabled = computed(() => busy.value || props.poolBusy)
const overCapacity = computed(() => props.maxSessions !== undefined && (props.account.pool?.sessions || 0) > props.maxSessions)
const infoOpen = ref(false), poolDisable = ref<number>()
const summaryUpdatedAt = computed(() => props.account.summary?.checkedAt ? new Date(props.account.summary.checkedAt).toLocaleString() : '')
const summaryTitle = computed(() => summaryUpdatedAt.value ? '更新于 ' + summaryUpdatedAt.value + '；检测可用性可更新' : '登录或检测可用性后更新')
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
const error = ref(''), notice = ref('')
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
    primaryCheck.value = { state: 'checking' }
    const revision = props.account.revision
    try {
      const data = await request<{ connected: boolean; clientMessage?: string }>('/status')
      if (lifecycle.signal.aborted || revision !== props.account.revision) return
      primaryCheck.value = { state: data.connected ? 'ready' : 'invalid', at: new Date() }
      if (data.connected && data.clientMessage) notify(data.clientMessage)
      emit('changed')
    } catch (reason) {
      if (lifecycle.signal.aborted || revision !== props.account.revision) return
      primaryCheck.value = { state: 'unavailable' }
      throw reason
    }
  })
}
function changeMode(value: string) {
  if (disabled.value || value === (props.account.pool?.mode || 'enabled')) return
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
    confirmation.value = ''; primaryCheck.value = undefined; emit('changed')
  })
}
function openLogin(mode: 'qr' | 'cookie') { if (more.value) menuTrigger.value?.focus(); closeMenu(); error.value = ''; login.value = mode }
function loggedIn() {
  login.value = ''; loginRevision = props.account.revision + 1
  directoryAfterLogin = needsDirectory.value
  primaryCheck.value = undefined; emit('changed')
  notify(needsDirectory.value ? '登录成功，请设置秒传目录' : '登录信息已保存')
}
watch([login, selecting], () => emit('interaction', !!login.value || selecting.value), { flush: 'sync' })
watch(() => props.startLogin, (start) => {
  if (start) { openLogin('qr'); emit('loginStarted') }
}, { immediate: true })
watch(() => props.account, async (next, previous) => {
  if (next.revision !== previous.revision) {
    login.value = ''; selecting.value = false; confirmation.value = ''; details.value = ''; poolDisable.value = undefined; primaryCheck.value = undefined
    const continueSetup = directoryAfterLogin && next.revision === loginRevision && next.configured && !next.playbackParentCid
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
  <article v-if="account.primary" class="space-y-6">
    <div class="primary-panel px-4 py-4 sm:px-5">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex min-w-0 items-center gap-3">
          <span class="grid h-10 w-10 shrink-0 place-items-center rounded-lg border border-slate-200 bg-slate-50 text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300"><P115Icon name="drive" /></span>
          <div class="min-w-0">
            <h2 class="font-semibold">主账号</h2>
            <p role="status" class="mt-1 flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
              <span class="primary-status inline-flex items-center gap-1.5" :data-state="account.configured ? primaryCheck?.state : undefined">
                <span aria-hidden="true" class="h-1.5 w-1.5 shrink-0 rounded-full bg-current"></span>{{ primaryStatus }}
              </span>
              <time v-if="account.configured && primaryCheck?.at" :datetime="primaryCheck.at.toISOString()" class="text-slate-500 dark:text-slate-400">{{ primaryCheck.at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</time>
            </p>
          </div>
        </div>
        <div class="primary-actions">
          <button type="button" :disabled="disabled" class="primary-button" :class="loginNeeded ? 'primary-button-accent' : 'primary-button-secondary'" @click="openLogin('qr')"><P115Icon name="qr" />扫码登录</button>
          <button v-if="account.configured" type="button" :disabled="disabled" :aria-busy="primaryCheck?.state === 'checking'" class="primary-button" :class="loginNeeded ? 'primary-button-secondary' : 'primary-button-accent'" @click="check">
            <P115Icon name="refresh" :class="{ 'motion-safe:animate-spin': primaryCheck?.state === 'checking' }" />{{ primaryCheck?.state === 'checking' ? '检测中…' : '检测可用性' }}
          </button>
        </div>
      </div>
      <p v-if="account.configured && primaryCheck?.state === 'invalid'" class="mt-3 text-xs leading-5 text-red-700 dark:text-red-300">请重新登录以恢复资源管理和主网盘的新播放。</p>
      <p v-if="notice" role="status" class="mt-3 text-xs leading-5 text-slate-600 dark:text-slate-300">{{ notice }}</p>
      <p v-if="error && !confirmation" role="alert" class="mt-3 text-xs leading-5" :class="primaryCheck?.state === 'unavailable' ? 'text-amber-700 dark:text-amber-300' : 'text-red-600 dark:text-red-400'">{{ error }}</p>
    </div>

    <section aria-label="账号资料">
      <header class="mb-2 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">账号资料</h3>
        <p v-if="account.configured && summaryUpdatedAt" class="text-xs text-slate-500 dark:text-slate-400">资料更新于 <time :datetime="account.summary?.checkedAt">{{ summaryUpdatedAt }}</time></p>
      </header>
      <dl class="primary-panel divide-y divide-slate-100 dark:divide-slate-800">
        <div class="primary-field"><dt>115 UID</dt><dd class="break-all font-mono">{{ account.uid || '—' }}</dd></div>
        <div class="primary-field"><dt>登录客户端</dt><dd>{{ account.configured ? loginClientLabel(account.loginClient || '') || (primaryCheck?.state === 'ready' ? '未识别' : '待识别') : '—' }}</dd></div>
        <div class="primary-field"><dt>115 会员</dt><dd>{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '未知' }}</dd></div>
        <div class="primary-field"><dt>剩余空间</dt><dd class="tabular-nums">{{ account.configured && account.summary?.spaceKnown ? remainingSpace(account.summary.remainingBytes) : '未知' }}</dd></div>
      </dl>
      <button type="button" class="primary-text-button mt-1" @click="infoOpen = true">账号详情<IconChevronDown class="h-4 w-4 -rotate-90" aria-hidden="true" /></button>
    </section>

    <details class="primary-panel primary-advanced">
      <summary class="flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded-xl px-4 text-sm font-medium sm:px-5">
        高级操作<IconChevronDown class="h-4 w-4 text-slate-500" aria-hidden="true" />
      </summary>
      <div class="divide-y divide-slate-100 border-t border-slate-100 px-4 dark:divide-slate-800 dark:border-slate-800 sm:px-5">
        <div class="primary-advanced-row">
          <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">暂停资源管理与主网盘的新播放，重新登录后恢复。</p>
          <button type="button" :disabled="disabled || !account.configured" class="primary-text-button primary-danger" @click="disconnect">清除登录信息</button>
        </div>
        <div class="primary-advanced-row">
          <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">移除主账号配置，恢复使用时需重新创建并配置目录。</p>
          <button type="button" :disabled="disabled" class="primary-text-button primary-danger" @click="remove">删除主账号配置</button>
        </div>
      </div>
    </details>
  </article>
  <article v-else class="account-card pool-card relative min-w-0 rounded-xl border border-slate-200 bg-white p-5 focus-within:z-10 dark:border-slate-700 dark:bg-slate-900">
    <header class="account-heading flex min-w-0 items-center gap-2 pr-10">
      <div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2"><h2 class="min-w-0 truncate font-semibold text-slate-900 dark:text-slate-100" :title="accountName">{{ accountName }}</h2><div class="pool-badges"><span>{{ account.configured && account.summary?.vip === 'vip' ? 'VIP' : account.configured && account.summary?.vip === 'normal' ? '普通用户' : '会员未知' }}</span><span :title="'登录客户端：' + (loginClientLabel(account.loginClient || '') || '待识别')">{{ account.configured ? loginClientLabel(account.loginClient || '') || '待识别' : '未登录' }}</span></div></div></div>
      <span class="pool-status shrink-0 text-[11px]" :class="statusTone" :title="statusLabel"><svg aria-hidden="true" viewBox="0 0 8 8"><circle cx="4" cy="4" r="3" fill="currentColor" /></svg><span>{{ statusLabel === '最近检查通过' ? '检查通过' : statusLabel }}</span></span>
    </header>
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
      <div v-if="maxSessions !== undefined" class="pool-mode-row"><IconSitemap aria-hidden="true" /><SelectMenu :model-value="account.pool?.mode || 'enabled'" :disabled="disabled" :label="account.name + ' 调度状态'" @change="changeMode" :options="[{ value: 'enabled', label: '参与调度' }, { value: 'draining', label: '停止新分配' }, { value: 'disabled', label: '禁用' }]" /></div>
      <p v-if="account.pool?.retryAt" class="mt-1 text-[11px] leading-5 text-amber-700 dark:text-amber-300">最早复查：{{ new Date(account.pool.retryAt).toLocaleString() }}</p>
    <div class="account-controls flex min-w-0 items-center gap-2">
      <div class="pool-footer">
        <div class="pool-update"><time v-if="account.pool?.checkedAt" :datetime="new Date(account.pool.checkedAt).toISOString()" :title="'上次检查 ' + new Date(account.pool.checkedAt).toLocaleString()">{{ new Date(account.pool.checkedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) }} 更新</time><span v-else>尚未检查</span><button :disabled="disabled || !account.configured || (maxSessions !== undefined && needsDirectory)" aria-label="重新检查" :aria-busy="!!poolWorking || busy" :title="poolWorking || busy ? '正在重新检查' : '重新检查'" class="pool-icon-button pool-refresh" @click="check"><P115Icon name="refresh" :class="poolWorking || busy ? 'animate-spin' : ''" /></button></div>
        <button :disabled="disabled" class="account-button pool-login border border-slate-200 text-slate-700 dark:border-slate-700 dark:text-slate-300" :class="!account.configured ? '!border-blue-600 !bg-blue-600 !text-white' : ''" @click="openLogin('qr')"><P115Icon name="qr" />扫码登录</button>
      </div>
      <div ref="menuContainer" class="account-menu relative shrink-0">
        <button ref="menuTrigger" aria-label="更多操作" :aria-controls="menuID" class="account-menu-trigger flex h-8 w-8 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800" :aria-expanded="more" @click="toggleMenu"><P115Icon name="more" /></button>
        <div v-if="more" :id="menuID" ref="menu" role="group" aria-label="账号操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900">
          <button class="pool-action" @click="closeMenu(); menuTrigger?.focus(); infoOpen = true">账号详情</button>
          <button :disabled="disabled" class="pool-action" @click="openDetails('rename')">修改名称</button>
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
.pool-card .account-controls { margin-top:12px; border-top:1px solid rgb(var(--color-slate-200) / 85%); padding-top:10px; }
.pool-footer { display:flex; width:100%; min-width:0; align-items:center; justify-content:space-between; gap:8px; }
.pool-update { display:flex; min-width:0; align-items:center; gap:5px; color:rgb(var(--color-slate-500)); font-size:10px; font-variant-numeric:tabular-nums; }
.pool-refresh { width:26px; height:26px; border-radius:50%; }
.pool-card .pool-login { min-height:30px; padding:5px 9px; background:rgb(var(--color-slate-50) / 85%); font-size:11px; font-weight:600; white-space:nowrap; }
.pool-card .pool-login :deep(svg) { width:15px; height:15px; }
.pool-card .account-menu { position:absolute; top:13px; right:13px; }
.pool-card .account-menu-trigger { width:28px; height:28px; border:1px solid rgb(var(--color-slate-200)); background:rgb(var(--color-slate-50) / 85%); }
.pool-card .account-menu-trigger :deep(svg) { width:16px; height:16px; }
.pool-icon-button:focus-visible { outline:2px solid rgb(59 130 246); outline-offset:2px; }
.dark .pool-badges span { background:rgb(var(--color-slate-800)); color:rgb(var(--color-slate-300)); }
.dark .pool-stat { border-color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-800) / 55%); }
.dark .pool-stat-label, .dark .pool-details, .dark .pool-update { color:rgb(var(--color-slate-400)); }
.dark .pool-mode-row { color:rgb(var(--color-slate-300)); }
.dark .pool-card .account-controls { border-color:rgb(var(--color-slate-700)); }
.dark .pool-icon-button, .dark .pool-card .account-menu-trigger, .dark .pool-card .pool-login { border-color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-800) / 55%); }
.dark .pool-icon-button { color:rgb(var(--color-slate-400)); }
.dark .pool-icon-button:hover:not(:disabled) { background:rgb(var(--color-slate-700)); color:rgb(var(--color-slate-200)); }
.account-menu-trigger:focus-visible { outline:2px solid rgb(59 130 246); outline-offset:3px; }
.primary-panel { @apply rounded-xl border border-slate-200 bg-white text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100; }
.primary-status { @apply text-slate-600 dark:text-slate-300; }
.primary-status[data-state="checking"] { @apply text-blue-700 dark:text-blue-300; }
.primary-status[data-state="ready"] { @apply text-emerald-700 dark:text-emerald-400; }
.primary-status[data-state="invalid"] { @apply text-red-700 dark:text-red-400; }
.primary-status[data-state="unavailable"] { @apply text-amber-700 dark:text-amber-300; }
.primary-actions { @apply flex flex-wrap gap-2; }
.primary-button { @apply inline-flex min-h-11 flex-1 items-center justify-center gap-2 whitespace-nowrap rounded-lg border px-2.5 text-sm font-medium disabled:opacity-50 sm:flex-none sm:px-3; }
.primary-button-accent { @apply border-blue-600 bg-blue-600 text-white enabled:hover:bg-blue-700; }
.primary-button-secondary { @apply border-slate-200 text-slate-700 enabled:hover:bg-slate-50 dark:border-slate-700 dark:text-slate-200 dark:enabled:hover:bg-slate-800; }
.primary-field { @apply grid min-h-11 grid-cols-[auto_minmax(0,1fr)] items-center gap-4 px-4 py-2 text-sm sm:px-5; }
.primary-field dt { @apply text-slate-500 dark:text-slate-400; }
.primary-field dd { @apply text-right font-medium; }
.primary-text-button { @apply inline-flex min-h-11 items-center gap-1 rounded-md text-sm text-blue-700 enabled:hover:underline disabled:opacity-50 dark:text-blue-300; }
.primary-text-button.primary-danger { @apply shrink-0 self-start text-red-700 dark:text-red-300 sm:self-auto; }
.primary-advanced summary { list-style:none; }
.primary-advanced summary::-webkit-details-marker { display:none; }
.primary-advanced[open] summary > :deep(svg) { transform:rotate(180deg); }
.primary-advanced-row { @apply flex flex-col items-start gap-x-6 px-0 py-2 sm:flex-row sm:items-center sm:justify-between sm:py-0; }
.primary-button:focus-visible, .primary-text-button:focus-visible, .primary-advanced summary:focus-visible { outline:2px solid rgb(var(--focus)); outline-offset:2px; }
</style>
