<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { memberRequest } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
import type { DynamicPoolSettings, DynamicPoolSnapshot } from '../types/dynamicPool'
import { poolHealthLabel, poolUIDs } from '../types/dynamicPool'
import P115ManagedAccountCard from './P115ManagedAccountCard.vue'
import DynamicPoolSettingsDrawer from './DynamicPoolSettingsDrawer.vue'
import DynamicPoolMetricsDrawer from './DynamicPoolMetricsDrawer.vue'
import P115ActionDialog from './P115ActionDialog.vue'
import P115Icon from './P115Icon.vue'

const props = defineProps<{ token: string; adding: boolean; loginAccountId: string }>()
const emit = defineEmits<{ add: []; loginStarted: []; accountsChanged: [accounts: P115Account[]] }>()
const snapshot = ref<DynamicPoolSnapshot>(), refreshing = ref(false), refreshError = ref(''), updatedAt = ref(0)
const settingsOpen = ref(false), metricsOpen = ref(false), rules = ref(false), more = ref(false)
const menuContainer = ref<HTMLElement>(), menuTrigger = ref<HTMLButtonElement>(), menu = ref<HTMLElement>(), menuID = useId()
const pendingAccount = ref(''), accountError = ref<{ id: string; message: string }>(), accountNotice = ref<{ id: string; message: string }>(), notice = ref('')
const accounts = computed(() => snapshot.value?.accounts || [])
const uidCount = computed(() => new Set(accounts.value.map(a => a.uid).filter(Boolean)).size)
const participating = computed(() => poolUIDs(accounts.value))
const checkedUIDs = computed(() => poolUIDs(accounts.value, true))
const faulty = computed(() => accounts.value.filter(a => participating.value.has(a.uid) && (a.pool?.mode || 'enabled') === 'enabled'
  && ['invalid_credentials', 'invalid_directory', 'cooldown', 'throttled'].includes(a.pool?.state || '')))
const allFaulty = computed(() => {
  const participatingAccounts = accounts.value.filter(a => a.configured && a.uid && a.playbackParentCid && (a.pool?.mode || 'enabled') === 'enabled')
  return participatingAccounts.length > 0 && participatingAccounts.every(a => faulty.value.includes(a))
})
const successRate = computed(() => {
  const m = snapshot.value?.metrics
  return m?.requests ? `${(m.succeeded / m.requests * 100).toFixed(1)}%` : '暂无请求'
})
const lifecycle = new AbortController()
const interactingAccounts = new Set<string>()
let loadController: AbortController | undefined, sequence = 0, timer: ReturnType<typeof setInterval> | undefined, noticeTimer: ReturnType<typeof setTimeout> | undefined
function notify(message: string) { notice.value = message; clearTimeout(noticeTimer); noticeTimer = setTimeout(() => notice.value = '', 5000) }
function invalidateRead() { sequence++; loadController?.abort(); refreshing.value = false }
async function load(force = false) {
  if (pendingAccount.value || (refreshing.value && !force)) return
  invalidateRead()
  const current = sequence, controller = new AbortController()
  loadController = controller; refreshing.value = true
  try {
    const data = await memberRequest(props.token, AbortSignal.any([lifecycle.signal, controller.signal, AbortSignal.timeout(15000)]), '/pool') as DynamicPoolSnapshot
    if (lifecycle.signal.aborted || current !== sequence) return
    // A passive read must not close a QR success screen or change a directory picker mid-interaction.
    // Explicit refreshes after commits/conflicts still install the current server revision.
    if (!force && interactingAccounts.size) data.accounts = data.accounts.map(a => interactingAccounts.has(a.id) ? accounts.value.find(old => old.id === a.id) || a : a)
    for (const feedback of [accountError, accountNotice]) {
      if (!feedback.value) continue
      const previous = accounts.value.find(a => a.id === feedback.value?.id), latest = data.accounts.find(a => a.id === feedback.value?.id)
      if (!latest || latest.revision !== previous?.revision || latest.pool?.state !== previous?.pool?.state || latest.pool?.mode !== previous?.pool?.mode) feedback.value = undefined
    }
    snapshot.value = data; refreshError.value = ''; updatedAt.value = Date.now(); emit('accountsChanged', data.accounts)
  } catch (e) { if (!lifecycle.signal.aborted && current === sequence && !controller.signal.aborted) refreshError.value = e instanceof Error ? e.message : '读取共享池失败' }
  finally { if (current === sequence) refreshing.value = false }
}
async function updateAccount(account: P115Account, action: 'check' | 'mode', value?: string, revision?: number) {
  if (pendingAccount.value || !snapshot.value) return
  if (revision !== undefined && accounts.value.find(a => a.id === account.id)?.revision !== revision) {
    accountError.value = { id: account.id, message: '账号配置已变化，请重新发起操作' }; return
  }
  invalidateRead(); pendingAccount.value = account.id; accountError.value = undefined; accountNotice.value = undefined
  try {
    const data = await memberRequest(props.token, lifecycle.signal, `/pool/${encodeURIComponent(account.id)}${action === 'check' ? '/check' : ''}`, action === 'check' ? {} : { mode: value }) as { accounts: P115Account[] }
    if (lifecycle.signal.aborted) return
    snapshot.value = { ...snapshot.value, accounts: data.accounts }; emit('accountsChanged', data.accounts)
    const latest = data.accounts.find(a => a.id === account.id)
    // The check endpoint can complete with a failure health state. Its HTTP status is not a health assertion.
    const message = action === 'check'
      ? latest?.revision !== account.revision ? '账号配置已变化，已刷新最新状态，请重新检查' : `检查结果：${poolHealthLabel(latest?.pool?.state)}`
      : value === 'draining' ? '已停止为此配置分配新播放，已有会话保留。'
        : value === 'disabled' ? '已禁用此配置，后续取链将被拒绝。' : '已恢复参与调度，仍需满足健康与容量条件。'
    if (action === 'check' && (latest?.revision !== account.revision || ['invalid_credentials', 'invalid_directory', 'cooldown', 'throttled'].includes(latest?.pool?.state || ''))) accountError.value = { id: account.id, message }
    else accountNotice.value = { id: account.id, message }
  } catch (e) { if (!lifecycle.signal.aborted) accountError.value = { id: account.id, message: e instanceof Error ? e.message : '操作失败，请重试' } }
  finally { pendingAccount.value = ''; if (!lifecycle.signal.aborted) void load(true) }
}
function saved(settings: DynamicPoolSettings) {
  invalidateRead()
  if (snapshot.value) snapshot.value = { ...snapshot.value, settings }
  settingsOpen.value = false; notify('池设置已保存'); void load(true)
}
async function toggleMenu() {
  more.value = !more.value
  if (more.value) { await nextTick(); menu.value?.querySelector<HTMLElement>('button,a')?.focus() }
}
function outside(event: Event) { if (event.target instanceof Node && !menuContainer.value?.contains(event.target)) more.value = false }
function escapeMenu(event: KeyboardEvent) { if (event.key === 'Escape' && more.value) { event.preventDefault(); more.value = false; menuTrigger.value?.focus() } }
function openRules() { more.value = false; menuTrigger.value?.focus(); rules.value = true }
defineExpose({ refresh: () => load(true) })
onMounted(() => {
  void load()
  timer = setInterval(() => { if (!document.hidden) void load() }, 10000)
  document.addEventListener('pointerdown', outside); document.addEventListener('focusin', outside); document.addEventListener('keydown', escapeMenu)
})
onBeforeUnmount(() => {
  lifecycle.abort(); invalidateRead(); clearInterval(timer); clearTimeout(noticeTimer)
  document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', escapeMenu)
})
</script>
<template>
  <section id="pool" aria-label="共享小号池" class="scroll-mt-6">
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-wrap items-baseline gap-2"><h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">共享小号池</h2><span v-if="snapshot" class="text-xs text-slate-500 dark:text-slate-400">{{ accounts.length }} 份配置 / {{ uidCount }} 个 UID</span></div>
      <div class="flex w-full flex-wrap items-center gap-1.5 sm:w-auto">
        <button :disabled="!snapshot" class="pool-button border-transparent text-slate-500 dark:text-slate-400" @click="metricsOpen = true"><P115Icon name="chart" />运行详情</button>
        <button :disabled="!snapshot || !!pendingAccount" class="pool-button border-slate-200 dark:border-slate-700" @click="settingsOpen = true"><P115Icon name="settings" />调度设置</button>
        <button :disabled="adding || !snapshot || !!pendingAccount" aria-label="添加共享小号" class="pool-button ml-auto border-blue-600 bg-blue-600 text-white sm:ml-0" @click="emit('add')"><P115Icon name="plus" /><span v-if="adding">添加中…</span><template v-else><span class="sm:hidden">添加小号</span><span class="hidden sm:inline">添加共享小号</span></template></button>
        <div ref="menuContainer" class="relative"><button ref="menuTrigger" aria-label="共享池更多操作" :aria-controls="menuID" :aria-expanded="more" class="flex h-9 w-8 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800" @click="toggleMenu"><P115Icon name="more" /></button><div v-if="more" :id="menuID" ref="menu" role="group" aria-label="共享池操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900"><button class="menu-action" @click="openRules">分配说明</button><RouterLink to="/users/list" class="menu-action" @click="more = false">管理 Emby 用户</RouterLink></div></div>
      </div>
    </header>
    <p v-if="refreshError" role="alert" class="mb-4 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ snapshot ? '状态刷新失败，显示上次成功更新的数据。' : '读取共享池失败。' }}{{ refreshError }} <button :disabled="refreshing || !!pendingAccount" class="ml-2 text-blue-600 disabled:opacity-40 dark:text-blue-400" @click="load(true)">重试</button></p>
    <p v-if="!snapshot && !refreshError" role="status" class="py-8 text-sm text-slate-500">正在读取共享池…</p>
    <template v-if="snapshot">
      <div aria-label="动态池运行概览" class="grid grid-cols-2 overflow-hidden rounded-xl border border-slate-200 bg-white sm:grid-cols-4 dark:border-slate-700 dark:bg-slate-900">
        <div class="metric-cell"><p class="metric-label">检查通过的参与 UID</p><p class="metric-value">{{ checkedUIDs.size }}<span class="metric-unit">个</span></p><p class="metric-note">参与调度涉及 {{ participating.size }} 个 UID</p></div>
        <div class="metric-cell border-l border-slate-200 dark:border-slate-700"><p class="metric-label">本次运行取链成功率</p><p :class="snapshot.metrics.requests ? 'metric-value' : 'metric-empty'">{{ successRate }}</p><p class="metric-note">{{ snapshot.metrics.succeeded }} 成功 / {{ snapshot.metrics.failed }} 失败</p></div>
        <div class="metric-cell border-t border-slate-200 sm:border-l sm:border-t-0 dark:border-slate-700"><p class="metric-label">最近取链耗时 p95</p><p v-if="snapshot.metrics.samples" class="metric-value">{{ snapshot.metrics.p95Ms }}<span class="metric-unit">ms</span></p><p v-else class="metric-empty">暂无样本</p><p class="metric-note">{{ snapshot.metrics.samples ? `最近 ${snapshot.metrics.samples} 个完成请求，含重试` : '有完成请求后显示耗时' }}</p></div>
        <div class="metric-cell border-l border-t border-slate-200 sm:border-t-0 dark:border-slate-700"><p class="metric-label">当前 API 等待</p><p class="metric-value">{{ snapshot.api.waiting }}<span class="metric-unit">个</span></p><p class="metric-note">等待 115 API 放行的请求</p></div>
      </div>
      <div class="mb-4 mt-2 flex flex-wrap justify-between gap-1 text-[11px] text-slate-500 dark:text-slate-400"><span class="flex items-center gap-1"><P115Icon name="refresh" class="!h-3 !w-3" />每 10 秒刷新状态，不触发网盘检查</span><span>上次更新 {{ new Date(updatedAt).toLocaleTimeString() }}</span></div>
      <div v-if="allFaulty || faulty.length || participating.size === 1" role="status" class="mb-5 flex flex-wrap items-start gap-2 rounded-lg border border-amber-200/60 bg-amber-50/70 p-3 text-xs leading-5 text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-300">
        <P115Icon name="warning" class="mt-0.5" /><div class="min-w-0 flex-1"><p class="font-medium">{{ allFaulty ? '参与调度的小号目前均处于故障或冷却状态' : faulty.length ? '部分小号需要处理' : '仅一个 UID 参与调度，暂无其他 UID 备用' }}</p><p>{{ faulty.length ? '请查看登录、目录和恢复状态。系统会在请求预算内尝试其他小号，不会使用主账号替代。' : '发生限流或故障时需等待恢复，不会使用主账号替代。添加不同 UID 的健康小号可提供备用。' }}</p></div><button class="shrink-0 text-amber-800 disabled:opacity-40 dark:text-amber-300" :disabled="adding || !!pendingAccount" @click="faulty.length ? metricsOpen = true : emit('add')">{{ faulty.length ? '查看运行详情' : '添加备用小号' }}</button>
      </div>
      <p v-else-if="accounts.length && !participating.size" role="status" class="mb-5 rounded-lg bg-slate-100 p-3 text-xs leading-5 text-slate-600 dark:bg-slate-800 dark:text-slate-400">当前没有完成配置且参与新分配的小号。请完成登录和目录设置，并检查调度模式。</p>
      <div class="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
        <P115ManagedAccountCard v-for="account in accounts" :key="account.id" :account="account" :token="token" :max-sessions="snapshot.settings.maxSessions" :shared-configs="accounts.filter(a => a.uid && a.uid === account.uid).length" :pool-busy="!!pendingAccount" :pool-working="pendingAccount === account.id" :pool-error="accountError?.id === account.id ? accountError.message : ''" :pool-notice="accountNotice?.id === account.id ? accountNotice.message : ''" :start-login="loginAccountId === account.id" @login-started="emit('loginStarted')" @interaction="editing => editing ? interactingAccounts.add(account.id) : interactingAccounts.delete(account.id)" @changed="load(true)" @pool-mode="(mode, revision) => updateAccount(account, 'mode', mode, revision)" @pool-check="updateAccount(account, 'check', undefined, account.revision)" />
      </div>
      <div v-if="!accounts.length" class="rounded-xl border border-dashed border-slate-300 bg-white px-5 py-8 text-center dark:border-slate-700 dark:bg-slate-900"><P115Icon name="pool" class="mx-auto mb-3 !h-7 !w-7 text-slate-400" /><h3 class="font-medium text-slate-900 dark:text-slate-100">共享池还没有小号</h3><p class="mb-4 mt-2 text-sm text-slate-500 dark:text-slate-400">添加小号，完成登录并设置秒传目录后，即可参与调度。</p><button :disabled="adding" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" @click="emit('add')">添加第一个小号</button></div>
      <p class="mt-3 text-[11px] leading-5 text-slate-500 dark:text-slate-400">名额按实际 UID 共享，为逻辑调度容量，不代表 CDN 真实连接数。个人网盘由用户在个人中心管理。</p>
    </template>
    <p v-if="notice" role="status" class="mt-3 text-sm text-emerald-700 dark:text-emerald-400">{{ notice }}</p>
  </section>
  <DynamicPoolSettingsDrawer v-if="settingsOpen && snapshot" :token="token" :settings="snapshot.settings" @close="settingsOpen = false" @saved="saved" />
  <DynamicPoolMetricsDrawer v-if="metricsOpen && snapshot" :snapshot="snapshot" :refresh-error="refreshError" :updated-at="updatedAt" @close="metricsOpen = false" />
  <P115ActionDialog v-if="rules" title="共享小号分配说明" @close="rules = false">
    <p>登录小号并设置秒传目录后，自动参与共享池调度。用户只需选择共享池来源。</p><p>播放会话优先选择低负载账号，同一会话保持稳定；容量按实际 UID 汇总。同 UID 的多个配置共享额度，凭据分别保存。</p><p>账号故障时会在请求预算内尝试其他小号；全池繁忙或不可用时停止新取链，不回退主网盘。已签发直链无法立即撤回。</p><p class="text-slate-500">个人网盘不进入管理员共享池。云端写入每 UID 串行，最多四个 UID 同时写入。</p><template #footer><button class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white" @click="rules = false">知道了</button></template>
  </P115ActionDialog>
</template>
<style scoped>
.pool-button { display:inline-flex; min-height:38px; align-items:center; justify-content:center; gap:6px; border-width:1px; border-radius:8px; padding:8px 10px; font-size:12px; white-space:nowrap; }
.pool-button:disabled { opacity:.4; }
.menu-action { display:block; width:100%; border-radius:6px; padding:9px 10px; text-align:left; }
.menu-action:hover { background:rgb(148 163 184 / 12%); }
.metric-cell { padding:16px; min-width:0; }
.metric-label { font-size:12px; color:rgb(var(--color-slate-500)); }
.metric-value { margin:6px 0 3px; font-size:28px; font-weight:600; line-height:1.5; font-variant-numeric:tabular-nums; }
.metric-empty { margin:6px 0 3px; font-size:20px; font-weight:600; line-height:2.1; }
.metric-unit { margin-left:5px; font-size:12px; font-weight:400; color:rgb(var(--color-slate-500)); }
.metric-note { font-size:11px; line-height:1.6; color:rgb(var(--color-slate-500)); }
@media(max-width:639px) { .pool-button { padding:8px 7px; gap:4px; font-size:12px; } .pool-button :deep(svg) { width:14px; height:14px; } }
@media(max-width:379px) { .pool-button { padding:8px 5px; font-size:11px; } .metric-cell { padding:12px; } }
</style>
