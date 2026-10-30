<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconInfoCircle } from '@tabler/icons-vue'
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
const pollHelpID = useId(), warningHelpID = useId()
const tooltipsDismissed = ref(false)
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
  return m?.requests ? (m.succeeded / m.requests * 100).toFixed(1) : '--'
})
const poolWarning = computed(() => allFaulty.value ? '调度 UID 均待恢复' : faulty.value.length ? '部分 UID 需要处理' : participating.value.size === 1 ? '仅单 UID 调度中' : '')
const poolWarningDescription = computed(() => faulty.value.length
  ? '请查看登录、目录和恢复状态。系统会在请求预算内尝试其他小号，不会使用主账号替代。'
  : '仅一个 UID 参与调度，暂无其他 UID 备用。发生限流或故障时需等待恢复，不会使用主账号替代。添加不同 UID 的健康小号可提供备用。')
const lifecycle = new AbortController()
const interactingAccounts = new Set<string>()
let loadController: AbortController | undefined, sequence = 0, timer: ReturnType<typeof setInterval> | undefined, noticeTimer: ReturnType<typeof setTimeout> | undefined
let tooltipContainer: HTMLElement | undefined
function positionTooltip() {
  const container = tooltipContainer, tooltip = container?.querySelector<HTMLElement>('.dashboard-tooltip'), panel = container?.closest('.pool-dashboard')
  if (!container?.isConnected || !tooltip || !panel || tooltipsDismissed.value || !container.matches(':hover, :focus-within')) return
  tooltip.style.transform = ''
  const bounds = tooltip.getBoundingClientRect(), parent = panel.getBoundingClientRect()
  const minimum = Math.max(12, parent.left + 10), maximum = Math.min(document.documentElement.clientWidth - 12, parent.right - 10) - bounds.width
  tooltip.style.transform = `translateX(${Math.max(minimum, Math.min(bounds.left, maximum)) - bounds.left}px)`
}
async function showTooltip(event: Event) {
  tooltipContainer = event.currentTarget as HTMLElement
  tooltipsDismissed.value = false
  await nextTick()
  positionTooltip()
}
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
function escapeMenu(event: KeyboardEvent) { if (event.key !== 'Escape') return; tooltipsDismissed.value = true; if (more.value) { event.preventDefault(); more.value = false; menuTrigger.value?.focus() } }
function openRules() { more.value = false; menuTrigger.value?.focus(); rules.value = true }
defineExpose({ refresh: () => load(true) })
onMounted(() => {
  void load()
  timer = setInterval(() => { if (!document.hidden) void load() }, 10000)
  document.addEventListener('pointerdown', outside); document.addEventListener('focusin', outside); document.addEventListener('keydown', escapeMenu)
  window.addEventListener('resize', positionTooltip)
})
onBeforeUnmount(() => {
  lifecycle.abort(); invalidateRead(); clearInterval(timer); clearTimeout(noticeTimer)
  document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', escapeMenu)
  window.removeEventListener('resize', positionTooltip)
})
</script>
<template>
  <section id="pool" aria-label="共享小号池" class="scroll-mt-6">
    <div class="pool-dashboard">
      <header class="dashboard-heading">
        <div class="dashboard-heading-copy">
          <div class="dashboard-title-row"><h2>共享小号池</h2><span v-if="snapshot" class="dashboard-account-count">{{ accounts.length }} 份配置 / {{ uidCount }} 个 UID</span></div>
          <div v-if="snapshot && poolWarning" class="dashboard-warning-wrap" @pointerenter="showTooltip" @focusin="showTooltip">
            <span tabindex="0" role="status" class="dashboard-warning" :aria-describedby="warningHelpID"><P115Icon name="warning" />{{ poolWarning }}</span>
            <span v-show="!tooltipsDismissed" :id="warningHelpID" role="tooltip" class="dashboard-tooltip">{{ poolWarningDescription }}</span>
          </div>
        </div>
        <div class="dashboard-actions">
          <button type="button" :disabled="!snapshot" class="pool-button dashboard-details" @click="metricsOpen = true"><P115Icon name="chart" />运行详情</button>
          <button type="button" :disabled="!snapshot || !!pendingAccount" class="pool-button dashboard-settings" @click="settingsOpen = true"><P115Icon name="settings" />调度设置</button>
          <button type="button" :disabled="adding || !snapshot || !!pendingAccount" aria-label="添加共享小号" class="pool-button pool-add" @click="emit('add')"><P115Icon name="plus" /><span v-if="adding">添加中…</span><template v-else><span class="sm:hidden">添加小号</span><span class="hidden sm:inline">添加共享小号</span></template></button>
          <div ref="menuContainer" class="relative"><button ref="menuTrigger" type="button" aria-label="共享池更多操作" :aria-controls="menuID" :aria-expanded="more" class="dashboard-more" @click="toggleMenu"><P115Icon name="more" /></button><div v-if="more" :id="menuID" ref="menu" role="group" aria-label="共享池操作" class="absolute right-0 top-full z-20 mt-1 w-44 rounded-xl border border-slate-200 bg-white p-1.5 text-sm shadow-xl dark:border-slate-700 dark:bg-slate-900"><button type="button" class="menu-action" @click="openRules">分配说明</button><RouterLink to="/users/list" class="menu-action" @click="more = false">管理 Emby 用户</RouterLink></div></div>
        </div>
      </header>
      <p v-if="refreshError" role="alert" class="mt-4 rounded-lg bg-amber-50/50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/20 dark:text-amber-300">{{ snapshot ? '状态刷新失败，显示上次成功更新的数据。' : '读取共享池失败。' }}{{ refreshError }} <button type="button" :disabled="refreshing || !!pendingAccount" class="ml-2 text-blue-600 disabled:opacity-40 dark:text-blue-400" @click="load(true)">重试</button></p>
      <p v-if="!snapshot && !refreshError" role="status" class="py-8 text-sm text-slate-500 dark:text-slate-400">正在读取共享池…</p>
      <template v-if="snapshot">
        <div aria-label="动态池运行概览" :aria-busy="refreshing" class="dashboard-metrics">
          <div class="metric-cell"><p class="metric-label">参与调度 UID</p><p class="metric-value"><span class="metric-number">{{ participating.size }}</span></p><p class="metric-note">{{ checkedUIDs.size }} 个检查通过</p></div>
          <div class="metric-cell"><p class="metric-label">取链成功率</p><p class="metric-value" :class="!snapshot.metrics.requests ? 'metric-empty' : ''"><span class="metric-number">{{ successRate }}</span><span class="metric-unit">%</span></p><p class="metric-note">{{ snapshot.metrics.succeeded }} 成功 / {{ snapshot.metrics.failed }} 失败<span v-if="!snapshot.metrics.requests" class="sr-only">，暂无请求</span></p></div>
          <div class="metric-cell"><p class="metric-label">P95 取链耗时</p><p class="metric-value" :class="!snapshot.metrics.samples ? 'metric-empty' : ''"><span class="metric-number">{{ snapshot.metrics.samples ? snapshot.metrics.p95Ms : '--' }}</span><span class="metric-unit">ms</span></p><p class="metric-note">{{ snapshot.metrics.samples ? `最近 ${snapshot.metrics.samples} 个完成请求，含重试` : '等待请求完成' }}</p></div>
          <div class="metric-cell"><p class="metric-label">API 等待队列</p><p class="metric-value"><span class="metric-number">{{ snapshot.api.waiting }}</span></p><p class="metric-note">等待 115 API 放行</p></div>
        </div>
        <div v-if="faulty.length" role="status" class="dashboard-issue"><span>{{ allFaulty ? '参与调度的小号均处于故障或冷却状态。' : '部分小号需要处理。' }}{{ poolWarningDescription }}</span><button type="button" :disabled="!!pendingAccount" class="shrink-0 font-medium underline-offset-4 hover:underline disabled:opacity-40" @click="metricsOpen = true">查看运行详情</button></div>
        <p v-else-if="accounts.length && !participating.size" role="status" class="dashboard-inactive">当前没有完成配置且参与新分配的小号。请完成登录和目录设置，并检查调度模式。</p>
        <footer class="dashboard-footer">
          <div class="dashboard-refresh"><P115Icon name="refresh" :class="{ 'is-refreshing': refreshing, 'is-polling': !refreshing && !refreshError }" /><span>{{ refreshing ? '更新中' : refreshError ? '上次成功更新' : '上次更新' }}</span><time :datetime="new Date(updatedAt).toISOString()">{{ new Date(updatedAt).toLocaleTimeString([], { hour12: false }) }}</time></div>
          <div class="dashboard-help" @pointerenter="showTooltip" @focusin="showTooltip"><button type="button" aria-label="自动刷新说明" :aria-describedby="pollHelpID" class="dashboard-help-trigger"><IconInfoCircle aria-hidden="true" /></button><span v-show="!tooltipsDismissed" :id="pollHelpID" role="tooltip" class="dashboard-tooltip">每 10 秒刷新状态，不触发网盘检查。</span></div>
        </footer>
      </template>
    </div>
    <template v-if="snapshot">
      <div class="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
        <P115ManagedAccountCard v-for="account in accounts" :key="account.id" :account="account" :token="token" :max-sessions="snapshot.settings.maxSessions" :shared-configs="accounts.filter(a => a.uid && a.uid === account.uid).length" :pool-busy="!!pendingAccount" :pool-working="pendingAccount === account.id" :pool-error="accountError?.id === account.id ? accountError.message : ''" :pool-notice="accountNotice?.id === account.id ? accountNotice.message : ''" :start-login="loginAccountId === account.id" @login-started="emit('loginStarted')" @interaction="editing => editing ? interactingAccounts.add(account.id) : interactingAccounts.delete(account.id)" @changed="load(true)" @pool-mode="(mode, revision) => updateAccount(account, 'mode', mode, revision)" @pool-check="updateAccount(account, 'check', undefined, account.revision)" />
      </div>
      <div v-if="!accounts.length" class="rounded-xl border border-dashed border-slate-300 bg-white px-5 py-8 text-center dark:border-slate-700 dark:bg-slate-900"><P115Icon name="pool" class="mx-auto mb-3 !h-7 !w-7 text-slate-400" /><h3 class="font-medium text-slate-900 dark:text-slate-100">共享池还没有小号</h3><p class="mb-4 mt-2 text-sm text-slate-500 dark:text-slate-400">添加小号，完成登录并设置秒传目录后，即可参与调度。</p><button type="button" :disabled="adding" class="pool-button pool-add" @click="emit('add')">添加第一个小号</button></div>
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
.pool-dashboard { position:relative; min-width:0; margin-bottom:16px; border:1px solid rgb(var(--color-slate-300) / 45%); border-radius:14px; padding:16px 20px 10px; background:rgb(255 255 255 / 58%); box-shadow:inset 0 1px 0 rgb(255 255 255 / 70%), 0 3px 14px rgb(15 23 42 / 2%); -webkit-backdrop-filter:blur(16px); backdrop-filter:blur(16px); }
.pool-dashboard:focus-within { z-index:20; }
.dashboard-heading { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:12px; }
.dashboard-heading-copy { display:flex; flex:1 1 280px; min-width:0; flex-wrap:wrap; align-items:center; gap:10px 12px; }
.dashboard-title-row { display:flex; min-width:0; flex-wrap:wrap; align-items:baseline; gap:6px 8px; }
.dashboard-title-row h2 { color:rgb(var(--color-slate-900)); font-size:16px; font-weight:650; line-height:24px; }
.dashboard-account-count { color:rgb(var(--color-slate-500)); font-size:11px; line-height:18px; }
.dashboard-warning-wrap, .dashboard-help { position:relative; display:inline-flex; flex-shrink:0; }
.dashboard-warning { display:inline-flex; align-items:center; gap:5px; border:1px solid rgb(180 83 9 / 12%); border-radius:6px; padding:3px 7px; background:rgb(180 83 9 / 3%); color:#916116; font-size:10px; line-height:16px; cursor:help; }
.dashboard-warning :deep(svg) { width:13px; height:13px; }
.dashboard-actions { display:flex; max-width:100%; flex-wrap:wrap; align-items:center; gap:6px; }
.pool-button { display:inline-flex; min-height:32px; flex-shrink:0; align-items:center; justify-content:center; gap:5px; border:1px solid transparent; border-radius:7px; padding:6px 9px; color:rgb(var(--color-slate-600)); font-size:11px; line-height:18px; white-space:nowrap; transition:background-color 150ms ease, border-color 150ms ease; }
.pool-button :deep(svg) { width:16px; height:16px; }
.pool-button:disabled { opacity:.4; }
.pool-button:hover:not(:disabled), .dashboard-more:hover { background:rgb(148 163 184 / 10%); }
.dashboard-settings { border-color:rgb(148 163 184 / 24%); }
.pool-add { border-color:rgb(var(--color-slate-700)); background:linear-gradient(135deg, rgb(var(--color-slate-700)), rgb(var(--color-slate-800))); color:#fff; box-shadow:0 1px 2px rgb(15 23 42 / 8%); }
.pool-add:hover:not(:disabled) { background:rgb(var(--color-slate-900)); }
.dashboard-more { display:inline-flex; width:28px; height:32px; align-items:center; justify-content:center; border-radius:8px; color:rgb(var(--color-slate-500)); }
.menu-action { display:block; width:100%; border-radius:6px; padding:9px 10px; text-align:left; }
.menu-action:hover { background:rgb(148 163 184 / 12%); }
.dashboard-metrics { display:grid; grid-template-columns:repeat(4, minmax(0, 1fr)); align-items:start; gap:24px; margin-top:18px; }
.metric-cell { min-width:0; }
.metric-label { color:rgb(var(--color-slate-500)); font-size:11px; line-height:16px; }
.metric-value { display:flex; min-height:35px; flex-wrap:wrap; align-items:baseline; gap:5px; margin:5px 0 2px; color:rgb(var(--color-slate-900)); font-size:28px; font-weight:600; line-height:1.25; font-variant-numeric:tabular-nums; letter-spacing:-.03em; }
.metric-number { min-width:0; overflow-wrap:anywhere; }
.metric-empty { color:rgb(var(--color-slate-400)); }
.metric-unit { color:rgb(var(--color-slate-500)); font-size:12px; font-weight:400; letter-spacing:normal; }
.metric-note { color:rgb(var(--color-slate-500)); font-size:10px; line-height:16px; }
.dashboard-issue { display:flex; flex-wrap:wrap; align-items:baseline; gap:6px 12px; margin-top:18px; color:#916116; font-size:11px; line-height:18px; }
.dashboard-issue > span { flex:1 1 260px; min-width:0; }
.dashboard-inactive { margin-top:18px; color:rgb(var(--color-slate-500)); font-size:11px; line-height:18px; }
.dashboard-footer { display:flex; align-items:center; justify-content:flex-end; gap:5px; margin-top:8px; }
.dashboard-refresh { display:flex; min-width:0; flex-wrap:wrap; align-items:center; gap:5px; color:rgb(var(--color-slate-500)); font-size:10px; line-height:18px; font-variant-numeric:tabular-nums; }
.dashboard-refresh :deep(svg) { width:12px; height:12px; }
.is-polling { animation:pool-poll-breathe 3s ease-in-out infinite; }
.is-refreshing { animation:pool-poll-spin 1s linear infinite; }
.dashboard-help-trigger { display:inline-flex; width:22px; height:22px; align-items:center; justify-content:center; border-radius:6px; color:rgb(var(--color-slate-500)); }
.dashboard-help-trigger :deep(svg) { width:15px; height:15px; }
.dashboard-help-trigger:hover { background:rgb(148 163 184 / 10%); color:rgb(var(--color-slate-700)); }
.dashboard-tooltip { position:absolute; top:calc(100% + 8px); left:0; z-index:30; display:none; width:max-content; max-width:min(290px, calc(100vw - 64px)); border:1px solid rgb(var(--color-slate-300) / 70%); border-radius:9px; padding:10px 12px; background:rgb(255 255 255 / 97%); color:rgb(var(--color-slate-600)); font-size:11px; line-height:19px; text-align:left; box-shadow:0 4px 16px rgb(15 23 42 / 10%); opacity:0; visibility:hidden; pointer-events:none; }
.dashboard-tooltip::before { position:absolute; right:0; bottom:100%; left:0; height:8px; content:''; }
.dashboard-warning-wrap:hover .dashboard-tooltip, .dashboard-warning-wrap:focus-within .dashboard-tooltip, .dashboard-help:hover .dashboard-tooltip, .dashboard-help:focus-within .dashboard-tooltip { display:block; opacity:1; visibility:visible; pointer-events:auto; }
.dashboard-help .dashboard-tooltip { top:auto; right:0; bottom:calc(100% + 8px); left:auto; }
.dashboard-help .dashboard-tooltip::before { top:100%; bottom:auto; }
.pool-button:focus-visible, .dashboard-more:focus-visible, .dashboard-help-trigger:focus-visible, .dashboard-warning:focus-visible { outline:2px solid rgb(59 130 246); outline-offset:3px; }
.dark .pool-dashboard { border-color:rgb(var(--color-slate-600) / 30%); background:rgb(var(--color-slate-800) / 40%); box-shadow:inset 0 1px 0 rgb(255 255 255 / 3%), 0 3px 14px rgb(0 0 0 / 5%); }
.dark .dashboard-title-row h2, .dark .metric-value { color:rgb(var(--color-slate-100)); }
.dark .dashboard-account-count, .dark .metric-label, .dark .metric-note, .dark .metric-unit, .dark .dashboard-inactive, .dark .dashboard-refresh, .dark .dashboard-more, .dark .dashboard-help-trigger { color:rgb(var(--color-slate-400)); }
.dark .metric-empty { color:rgb(var(--color-slate-500)); }
.dark .pool-button { color:rgb(var(--color-slate-300)); }
.dark .pool-add { border-color:rgb(var(--color-slate-600) / 55%); color:rgb(var(--color-slate-100)); }
.dark .pool-add:hover:not(:disabled) { background:rgb(var(--color-slate-700)); }
.dark .dashboard-warning { border-color:rgb(214 179 122 / 16%); background:rgb(214 179 122 / 4%); color:#d6b37a; }
.dark .dashboard-issue { color:#d6b37a; }
.dark .dashboard-tooltip { border-color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-900) / 98%); color:rgb(var(--color-slate-300)); }
.dark .dashboard-help-trigger:hover { color:rgb(var(--color-slate-200)); }
@keyframes pool-poll-breathe { 0%, 100% { opacity:.45; } 50% { opacity:.85; } }
@keyframes pool-poll-spin { to { transform:rotate(360deg); } }
@media(max-width:767px) { .pool-dashboard { padding:16px 18px 10px; } .dashboard-metrics { gap:20px; } .metric-value { font-size:26px; } }
@media(max-width:639px) {
  .pool-dashboard { padding:16px 16px 10px; }
  .dashboard-heading-copy { flex-basis:100%; }
  .dashboard-actions { width:100%; flex-wrap:nowrap; }
  .pool-button { padding:6px; gap:5px; font-size:11px; }
  .pool-button :deep(svg) { width:14px; height:14px; }
  .dashboard-actions .pool-add { margin-left:auto; }
  .dashboard-metrics { grid-template-columns:repeat(2, minmax(0, 1fr)); gap:16px 20px; margin-top:18px; }
  .dashboard-heading-copy { row-gap:6px; }
}
@media(max-width:379px) { .pool-dashboard { padding:14px 14px 8px; } .dashboard-actions { gap:4px; } .pool-button { padding:6px 4px; gap:4px; font-size:10px; } .metric-value { font-size:24px; } }
@media(prefers-reduced-motion:reduce) { .is-polling, .is-refreshing { animation:none; } .pool-button { transition:none; } }
</style>
