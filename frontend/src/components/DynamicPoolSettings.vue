<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { memberRequest } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig } from '../types/telegramBots'
const props = defineProps<{ token: string; pool: P115Account[] }>()
const emit = defineEmits<{ changed: [] }>()
const settings = ref({ maxSessions: 3, maxAttempts: 3, budgetSeconds: 90, leaseMinutes: 5, cooldownSeconds: 30, uidIntervalMs: 250, globalIntervalMs: 50, notificationBotId: '' })
type Metrics = { requests: number; succeeded: number; failed: number; switches: number; p50Ms: number; p95Ms: number; samples: number; peakUidSessions: number; copyPreferred: number; recentPreferred: number; resolved: number; copyResolutions: number; directLinkHits: number }
type Budget = { requests: number; throttled: number; rejected: number; waitMs: number; waiting: number; peakWaiting: number; sourceRequests: number }
const metrics = ref<Metrics>(), api = ref<Budget>(), bots = ref<TelegramBotConfig[]>([])
const events = ref<{ id: number; accountId: string; state: string; at: number }[]>([])
const now = ref(Date.now()), refreshing = ref(false)
const uidCount = computed(() => new Set(accounts.value.filter(a => !a.primary && a.configured && a.uid && a.playbackParentCid && a.pool?.mode !== 'disabled').map(a => a.uid)).size)
const successRate = computed(() => metrics.value?.requests ? `${(metrics.value.succeeded / metrics.value.requests * 100).toFixed(1)}%` : '暂无请求')
const healthText = (state: string) => ({ ready: '检查通过', pending: '等待复查', unknown: '尚未检查', unconfigured: '未完成配置', invalid_credentials: '登录失效', invalid_directory: '目录失效', cooldown: '故障冷却', throttled: '限流冷却' }[state] || '等待复查')
const remaining = (until: number) => Math.max(0, Math.ceil((until - now.value) / 1000))
let refreshTimer: ReturnType<typeof setInterval> | undefined
const accounts = ref<P115Account[]>([]), loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const controller = new AbortController()
async function load(initial = false) {
 if (refreshing.value || busy.value) return
 refreshing.value = true
 try { const data = await memberRequest(props.token, controller.signal, '/pool'); if (initial || !loaded.value) settings.value = { ...settings.value, ...data.settings }; accounts.value = data.accounts || []; metrics.value = data.metrics; api.value = data.api; events.value = data.events || []; loaded.value = true; now.value = Date.now() }
 catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
 finally { refreshing.value = false }
}
async function loadBots() {
 try { bots.value = (await telegramRequest<{ bots: TelegramBotConfig[] }>(props.token, 'bots', { signal: controller.signal })).bots || [] }
 catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '通知配置读取失败' }
}
async function save() {
 if (busy.value || !loaded.value) return
 busy.value = true; error.value = ''; notice.value = ''
 try { await memberRequest(props.token, controller.signal, '/pool', settings.value); notice.value = '池设置已保存'; emit('changed') }
 catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败' }
 finally { busy.value = false }
}
async function mode(account: P115Account, value: string) {
 if (busy.value) return
 busy.value = true; error.value = ''
 try { const data = await memberRequest(props.token, controller.signal, `/pool/${encodeURIComponent(account.id)}`, { mode: value }); accounts.value = data.accounts; emit('changed') }
 catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败' }
 finally { busy.value = false }
}
async function check(account: P115Account) {
 if (busy.value) return
 busy.value = true; error.value = ''
 try { const data = await memberRequest(props.token, controller.signal, `/pool/${encodeURIComponent(account.id)}/check`, {}); accounts.value = data.accounts; emit('changed') }
 catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '检查失败' }
 finally { busy.value = false }
}
watch(() => props.pool, value => { accounts.value = value })
onMounted(() => { void load(true); void loadBots(); refreshTimer = setInterval(() => { now.value = Date.now(); if (!document.hidden) void load() }, 10000) })
onBeforeUnmount(() => { controller.abort(); clearInterval(refreshTimer) })
</script>
<template>
 <details class="mb-6 rounded-xl border border-slate-200 p-4 dark:border-slate-700">
  <summary class="cursor-pointer font-medium">动态池容量与调度设置</summary>
  <p class="my-3 text-sm text-slate-500">按实际 UID 共享容量。停止新分配保留已有会话；禁用后拒绝旧账号的新取链。直链发出后不能立即撤回，也不能限制 CDN 下载速度。</p>
  <p class="mb-3 text-xs text-slate-500">同 UID 的多个配置共享额度，凭据分别保存。云端写入每 UID 串行，最多四个 UID 同时写入。</p>
  <p v-if="loaded && uidCount === 1" role="status" class="mb-3 text-sm text-amber-700 dark:text-amber-300">当前只有一个小号 UID，没有备用小号。失效或限流时等待恢复，不会使用主号替代；新增健康小号后自动参与调度。</p>
  <p v-else-if="loaded && uidCount === 0" role="status" class="mb-3 text-sm text-amber-700 dark:text-amber-300">当前没有可用池配置，请添加小号并完成登录和播放目录设置。</p>
  <section v-if="metrics" aria-label="动态池运行指标" class="mb-4 rounded-lg bg-slate-50 p-3 text-sm dark:bg-slate-900">
   <p>本次运行成功率 {{ successRate }}；成功 {{ metrics.succeeded }}，失败 {{ metrics.failed }}，切换 {{ metrics.switches }} 次。</p>
   <p class="mt-2">最近 {{ metrics.samples }} 次取链耗时：p50 {{ metrics.p50Ms }} ms，p95 {{ metrics.p95Ms }} ms；单 UID 峰值占用 {{ metrics.peakUidSessions }}。</p>
   <p v-if="api" class="mt-2">115 API 放行 {{ api.requests }} 次，收到 429 {{ api.throttled }} 次，队列拒绝 {{ api.rejected }} 次；当前等待 {{ api.waiting }}，累计等待 {{ api.waitMs }} ms。</p>
   <p class="mt-2">成功解析 {{ metrics.resolved || 0 }} 次，其中副本路径 {{ metrics.copyResolutions || 0 }} 次，直链缓存命中 {{ metrics.directLinkHits || 0 }} 次；源盘 API 放行 {{ api?.sourceRequests || 0 }} 次。</p>
   <p class="mt-2 text-xs text-slate-500">指标重启后重新统计；取链耗时含失败和重试，不代表播放器实际起播时间。状态每 10 秒刷新，不触发网盘探测。</p>
  </section>
  <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="save">
   <label>每 UID 会话上限<input v-model.number="settings.maxSessions" type="number" required min="1" max="20" /></label>
   <label>最多尝试账号数<input v-model.number="settings.maxAttempts" type="number" required min="1" max="3" /></label>
   <label>取链总预算（秒）<input v-model.number="settings.budgetSeconds" type="number" required min="5" max="90" /></label>
   <label>无停止通知时保守保留（分钟）<input v-model.number="settings.leaseMinutes" type="number" required min="5" max="1440" /></label>
   <label>临时错误冷却（秒）<input v-model.number="settings.cooldownSeconds" type="number" required min="5" max="3600" /></label>
   <label>每 UID API 请求间隔（毫秒）<input v-model.number="settings.uidIntervalMs" type="number" required min="100" max="10000" /></label>
   <label>全局 API 请求间隔（毫秒）<input v-model.number="settings.globalIntervalMs" type="number" required min="10" max="1000" /></label>
   <label>池健康通知<select v-model="settings.notificationBotId" aria-label="池健康通知" class="mt-1 block w-full rounded-lg border border-slate-400 bg-transparent p-2">
    <option value="">关闭外部通知</option>
    <option v-if="settings.notificationBotId && !bots.some(b => b.id === settings.notificationBotId)" :value="settings.notificationBotId" disabled>原 Bot 配置不可用</option>
    <option v-for="bot in bots" :key="bot.id" :value="bot.id" :disabled="!bot.enabled || !bot.configured || !bot.notificationChatID">{{ bot.name }}</option>
   </select></label>
   <button :disabled="busy || !loaded" class="self-end rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">保存池设置</button>
  </form>
  <div v-for="account in accounts" :key="account.id" class="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-slate-100 pt-3 text-sm dark:border-slate-800">
   <div><span>{{ account.name }}：UID 占用 {{ account.pool?.sessions || 0 }}，准备 {{ account.pool?.preparing || 0 }}，活跃 {{ account.pool?.active || 0 }}，残余 {{ account.pool?.residual || 0 }}</span>
    <p class="mt-1 text-xs text-slate-500">{{ healthText(account.pool?.state || 'unknown') }}<template v-if="remaining(account.pool?.retryAt || 0)">，冷却剩余 {{ remaining(account.pool?.retryAt || 0) }} 秒</template></p>
    <p v-if="account.pool?.message" class="mt-1 text-xs text-slate-500">{{ account.pool.message }}</p>
   </div>
   <select :value="account.pool?.mode || 'enabled'" :disabled="busy" :aria-label="`${account.name} 调度状态`" class="rounded-lg border border-slate-200 bg-transparent p-2 dark:border-slate-700" @change="mode(account, ($event.target as HTMLSelectElement).value)">
    <option value="enabled">参与调度</option><option value="draining">停止新分配</option><option value="disabled">禁用</option>
   </select>
   <button type="button" :disabled="busy || !account.configured || !account.playbackParentCid" class="text-blue-600 disabled:opacity-40" @click="check(account)">重新检查</button>
  </div>
  <details v-if="events.length" class="mt-4 text-sm"><summary class="cursor-pointer">最近健康事件</summary><p v-for="event in [...events].reverse().slice(0, 10)" :key="event.id" class="mt-2 break-words">{{ new Date(event.at).toLocaleString() }}：{{ accounts.find(a => a.id === event.accountId)?.name || '已移除的小号' }}，{{ healthText(event.state) }}</p></details>
  <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p><p v-if="notice" role="status" class="mt-3 text-sm text-emerald-600">{{ notice }}</p>
 </details>
</template>
<style scoped>
label { display: block; font-size: 14px; }
input { display: block; width: 100%; margin-top: 6px; padding: 8px; border: 1px solid #94a3b8; border-radius: 8px; background: transparent; }
</style>
