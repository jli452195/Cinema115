<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconHandMove, IconInfoCircle, IconPlus, IconPuzzle, IconRefresh } from '@tabler/icons-vue'
import TelegramBotEditor from '../components/TelegramBotEditor.vue'
import TelegramBotCard from '../components/TelegramBotCard.vue'
import TelegramModuleCard from '../components/TelegramModuleCard.vue'
import SortableList from '../components/SortableList.vue'
import FloatingDialog from '../components/FloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { telegramRequest } from '../lib/telegram-api'
import { useToast } from '../composables/useToast'
import { portalUser } from '../composables/usePortalAuth'
import { TELEGRAM_BOT_POLL_MS, telegramBotCache } from '../composables/useTelegramSettingsCache'
import type { TelegramBotConfig, TelegramBotIdentity, TelegramBotModule } from '../types/telegramBots'

const props = defineProps<{ token: string }>()
const toast = useToast()
const cached = telegramBotCache.get(props.token)
const bots = ref(cached?.data.bots ?? []), modules = ref(cached?.data.modules ?? [])
const loading = ref(!cached), refreshing = ref(false), adding = ref(false), deleting = ref(false)
const editingBot = ref<TelegramBotConfig>(), deletingBot = ref<TelegramBotConfig>()
const busyModules = ref(new Set<string>()), testingBots = ref(new Set<string>())
const draggingModuleId = ref(''), readError = ref('')
// 存储按浏览器来源隔离；管理员编号来自已验证的登录状态，不保存凭据。
const orderKey = portalUser.value?.id ? 'cinema115:telegram-module-order:' + portalUser.value.id : ''
const moduleOrder = ref<Record<string, string[]>>({})
try {
  const saved: unknown = JSON.parse(orderKey ? localStorage.getItem(orderKey) || '{}' : '{}')
  if (saved && typeof saved === 'object' && !Array.isArray(saved)) {
    moduleOrder.value = Object.fromEntries(Object.entries(saved).filter((entry): entry is [string, string[]] => Array.isArray(entry[1]) && entry[1].every(id => typeof id === 'string')))
  }
} catch { /* 本地排序不可用时采用服务端默认顺序。 */ }
const droppedLists = new Map<string, TelegramBotModule[]>()
const lifecycle = new AbortController()
let timer: number | undefined, sequence = 0
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])

const unassigned = computed(() => moduleByBot(''))
const assignmentBusy = computed(() => adding.value || deleting.value || busyModules.value.size > 0)
const onlineCount = computed(() => bots.value.filter(bot => bot.connected).length)
const assignedCount = computed(() => modules.value.filter(module => !!module.assignedBotId).length)
const deletingModules = computed(() => modules.value.filter(module => module.assignedBotId === deletingBot.value?.id))
function moduleByBot(botID: string) {
  const items = modules.value.filter(module => module.assignedBotId === botID)
  const positions = new Map((moduleOrder.value[botID] || []).map((id, index) => [id, index]))
  return items.sort((a, b) => (positions.get(a.id) ?? positions.size) - (positions.get(b.id) ?? positions.size))
}
function saveOrder() {
  moduleOrder.value = Object.fromEntries(['', ...bots.value.map(bot => bot.id)].map(id => [id, moduleByBot(id).map(module => module.id)]))
  try { if (orderKey) localStorage.setItem(orderKey, JSON.stringify(moduleOrder.value)) } catch { /* 存储受限时仍允许本次页面排序。 */ }
}
function startDrag(module: TelegramBotModule) {
  droppedLists.clear()
  draggingModuleId.value = module.id
  ++sequence; refreshing.value = false
}
function stageList(botId: string, items: TelegramBotModule[]) { droppedLists.set(botId, items) }
function endDrag() {
  const id = draggingModuleId.value
  draggingModuleId.value = ''
  const module = modules.value.find(item => item.id === id)
  const before = moduleOrder.value
  const target = [...droppedLists].find(([, items]) => items.some(item => item.id === id))
  if (module && target) {
    moduleOrder.value = { ...before, ...Object.fromEntries([...droppedLists].map(([botId, items]) => [botId, items.map(item => item.id)])) }
    if (target[0] !== module.assignedBotId) void updateModule(module, target[0], before)
    else saveOrder()
  }
  droppedLists.clear()
}
function moveModule(module: TelegramBotModule, direction: 'up' | 'down') {
  if (assignmentBusy.value || draggingModuleId.value) return
  const items = moduleByBot(module.assignedBotId), from = items.findIndex(item => item.id === module.id)
  const to = from + (direction === 'up' ? -1 : 1)
  if (from < 0 || to < 0 || to >= items.length) return
  items.splice(to, 0, items.splice(from, 1)[0]!)
  moduleOrder.value = { ...moduleOrder.value, [module.assignedBotId]: items.map(item => item.id) }
  saveOrder()
}
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，请重试' }

async function refresh(notify = false) {
  const current = ++sequence
  refreshing.value = true
  try {
    const data = await telegramBotCache.read(props.token)
    if (!data || current !== sequence || lifecycle.signal.aborted || draggingModuleId.value) return
    bots.value = data.bots; modules.value = data.modules; readError.value = ''
    saveOrder()
    if (notify) toast.success('Bot 状态与功能分配已刷新')
  } catch (reason) {
    if (current !== sequence || lifecycle.signal.aborted) return
    const failure = message(reason)
    if (notify || readError.value !== failure) toast.error(failure, { title: '读取 Bot 配置失败' })
    readError.value = failure
  } finally { if (current === sequence) { loading.value = false; refreshing.value = false } }
}

function setBusy(target: typeof busyModules, id: string, busy: boolean) {
  const next = new Set(target.value)
  busy ? next.add(id) : next.delete(id)
  target.value = next
}

async function updateModule(module: TelegramBotModule, botId: string, previousOrder = moduleOrder.value) {
  if (assignmentBusy.value) return
  const previousBotId = module.assignedBotId
  if (previousBotId === botId) return
  const previousBotName = bots.value.find(bot => bot.id === previousBotId)?.name || '原 Bot'
  setBusy(busyModules, module.id, true)
  draggingModuleId.value = ''
  ++sequence; refreshing.value = false
  // 仅显示分配结果，服务端仍通过 expectedBotId 校验原归属；失败恢复已知状态。
  modules.value = modules.value.map(item => item.id === module.id ? { ...item, assignedBotId: botId } : item)
  try {
    const token = props.token
    const result = await telegramBotCache.mutate(token, () => telegramRequest<{ modules: TelegramBotModule[] }>(token, 'modules/' + encodeURIComponent(module.id), { method: 'PUT', body: JSON.stringify({ botId, expectedBotId: previousBotId }), signal: signal() }))
    if (lifecycle.signal.aborted) return
    modules.value = result.modules
    saveOrder()
    await refresh()
    if (lifecycle.signal.aborted) return
    const botName = bots.value.find(bot => bot.id === botId)?.name || 'Bot'
    if (!botId) toast.success(module.name + '已退回待分配功能池', { title: '已解除分配' })
    else if (previousBotId) toast.info(module.name + '已从' + previousBotName + '转移给' + botName, { title: '功能已转移' })
    else toast.success(module.name + '已分配给' + botName, { title: '分配成功' })
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      modules.value = modules.value.map(item => item.id === module.id ? module : item)
      moduleOrder.value = previousOrder
      await refresh()
      if (lifecycle.signal.aborted) return
      toast.error(message(reason), { title: '操作未完成' })
    }
  } finally { setBusy(busyModules, module.id, false) }
}
function assign(module: TelegramBotModule, botId: string) { void updateModule(module, botId) }
function unassignModule(module: TelegramBotModule) { void updateModule(module, '') }

async function testBot(bot: TelegramBotConfig) {
  if (testingBots.value.has(bot.id) || !bot.configured || deleting.value) return
  setBusy(testingBots, bot.id, true)
  try {
    const identity = await telegramRequest<TelegramBotIdentity>(props.token, 'bots/' + bot.id + '/test', { method: 'POST', body: '{}', signal: signal() })
    if (lifecycle.signal.aborted) return
    if (identity.webhookConfigured) toast.warning(bot.name + '身份验证成功，但已配置 Webhook，启用前请停用原 Webhook', { title: '需要检查配置' })
    else toast.success(bot.name + '的 Telegram API 连接测试通过', { title: '连接测试成功' })
  } catch (reason) { if (!lifecycle.signal.aborted) toast.error(message(reason), { title: bot.name + '连接测试失败' }) }
  finally { setBusy(testingBots, bot.id, false) }
}

async function add() {
  if (adding.value || assignmentBusy.value || draggingModuleId.value || bots.value.length >= 20) return
  adding.value = true
  try {
    const token = props.token
    const result = await telegramBotCache.mutate(token, () => telegramRequest<{ id: string }>(token, 'bots', { method: 'POST', body: JSON.stringify({ name: 'Bot ' + (bots.value.length + 1) }), signal: signal() }))
    if (lifecycle.signal.aborted) return
    await refresh()
    editingBot.value = bots.value.find(bot => bot.id === result.id)
  } catch (reason) { if (!lifecycle.signal.aborted) toast.error(message(reason), { title: '新增 Bot 失败' }) }
  finally { adding.value = false }
}
async function removeBot() {
  const bot = deletingBot.value
  if (!bot || deleting.value || busyModules.value.size) return
  const releasedCount = deletingModules.value.length
  deleting.value = true
  try {
    const token = props.token
    await telegramBotCache.mutate(token, () => telegramRequest(token, 'bots/' + bot.id, { method: 'DELETE', body: JSON.stringify({ revision: bot.revision, releaseModules: true }), signal: signal() }))
    if (lifecycle.signal.aborted) return
    deletingBot.value = undefined
    await refresh()
    toast.success(releasedCount ? 'Bot 已删除，关联功能已退回待分配区' : 'Bot 已删除')
  } catch (reason) { if (!lifecycle.signal.aborted) { await refresh(); toast.error(message(reason), { title: '删除 Bot 失败' }) } }
  finally { deleting.value = false }
}
async function edited() { editingBot.value = undefined; await refresh() }

onMounted(async () => {
  if (!cached?.fresh) await refresh()
  const poll = async () => {
    if (!lifecycle.signal.aborted && !document.hidden && !editingBot.value && !assignmentBusy.value && !draggingModuleId.value && !refreshing.value) await refresh()
    if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, TELEGRAM_BOT_POLL_MS)
  }
  const nextPoll = cached?.fresh ? Math.max(0, cached.expiresAt - Date.now()) : TELEGRAM_BOT_POLL_MS
  if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, nextPoll)
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) window.clearTimeout(timer) })
</script>

<template>
  <section class="mb-5 flex flex-wrap items-center gap-3" aria-label="Bot 配置概览">
    <div class="flex flex-wrap items-center gap-2 rounded-2xl border border-slate-200 bg-slate-100/70 p-1.5 dark:border-slate-800 dark:bg-slate-900/70">
      <span class="flex items-center gap-2 rounded-xl bg-white px-3 py-1.5 text-xs text-slate-500 shadow-sm dark:bg-slate-800 dark:text-slate-400">Bot 总数<strong class="font-semibold tabular-nums text-slate-800 dark:text-slate-100">{{ bots.length }}</strong></span>
      <span class="flex items-center gap-2 rounded-xl border border-emerald-200/70 bg-emerald-50 px-3 py-1.5 text-xs text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/30 dark:text-emerald-300">已连接<strong class="tabular-nums">{{ onlineCount }}</strong></span>
      <span class="flex items-center gap-2 rounded-xl border border-blue-200/70 bg-blue-50 px-3 py-1.5 text-xs text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-300">功能已分配<strong class="tabular-nums">{{ assignedCount }} / {{ modules.length }}</strong></span>
    </div>
    <div class="ml-auto flex items-center gap-2">
      <button type="button" :disabled="loading || refreshing || assignmentBusy || !!draggingModuleId" class="bot-action grid h-9 w-9 place-items-center rounded-xl border border-slate-200 bg-white text-slate-600 hover:bg-slate-100 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800" aria-label="刷新 Bot 与功能" title="刷新" @click="refresh(true)"><IconRefresh :size="16" :class="{ 'motion-safe:animate-spin': refreshing }" aria-hidden="true" /></button>
      <button type="button" :disabled="loading || adding || assignmentBusy || !!draggingModuleId || bots.length >= 20" class="bot-action inline-flex h-9 items-center gap-1.5 rounded-xl bg-blue-600 px-3 text-sm font-medium text-white shadow-sm shadow-blue-600/20 hover:bg-blue-700 disabled:opacity-40" @click="add"><IconPlus :size="16" aria-hidden="true" />{{ adding ? '添加中' : '新增 Bot' }}</button>
    </div>
  </section>
  <p role="note" aria-label="Bot 链接接收规则" class="mb-5 flex items-start gap-2 text-xs leading-5 text-slate-500 dark:text-slate-400"><IconInfoCircle :size="15" class="mt-0.5 shrink-0 text-blue-600 dark:text-blue-400" aria-hidden="true" /><span>私聊直接发送链接或上传 TXT 清单。115 分享可选网盘转存或分享 STRM，纯 ED2K 按分配的用途和目录处理；仅一个适用目标且可用时自动处理，多个目标一次选择用途和目录。磁力及与 ED2K 的混合消息整批走云下载；115 分享请与磁力、ED2K 分开发送。</span></p>
  <p v-if="loading" role="status" class="py-8 text-sm text-slate-500 dark:text-slate-400">正在读取 Bot 和功能配置…</p>
  <div v-else class="bot-workspace grid items-start gap-5">
    <section aria-labelledby="bot-list-title" class="bot-primary min-w-0">
      <header class="mb-3 flex items-center justify-between gap-3"><div><h2 id="bot-list-title" class="text-sm font-semibold text-slate-800 dark:text-slate-100">Bot 配置</h2><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">每项功能只归属一个 Bot；Bot 启用后运行已分配功能。</p></div><span class="rounded-full bg-slate-100 px-2 py-1 text-xs tabular-nums text-slate-500 dark:bg-slate-800 dark:text-slate-400">{{ bots.length }} / 20</span></header>
      <TransitionGroup v-if="bots.length" name="bot-list" tag="div" class="bot-grid relative grid min-w-0 items-start gap-4">
        <TelegramBotCard v-for="bot in bots" :key="bot.id" :bot="bot" :modules="moduleByBot(bot.id)" :busy-module-ids="busyModules" :testing="testingBots.has(bot.id)" :disabled="assignmentBusy" @edit="editingBot = $event" @remove="deletingBot = $event" @test="testBot" @unassign-module="unassignModule" @start="startDrag" @end="endDrag" @update-modules="stageList(bot.id, $event)" @move="moveModule" />
      </TransitionGroup>
      <div v-else class="rounded-2xl border border-dashed border-slate-300 px-4 py-12 text-center dark:border-slate-700"><p class="text-sm font-medium text-slate-700 dark:text-slate-200">{{ readError ? '暂时无法读取 Bot 配置' : '还没有 Bot 配置' }}</p><p class="mt-2 text-xs text-slate-500 dark:text-slate-400">{{ readError || '新增 Bot 后填写 Token，再分配实际功能。' }}</p><button v-if="readError" type="button" class="mt-4 rounded-xl bg-blue-600 px-3 py-2 text-sm font-medium text-white" :disabled="refreshing" @click="refresh(true)">重新读取</button><button v-else type="button" class="mt-4 rounded-xl bg-blue-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-40" :disabled="adding" @click="add">新增 Bot</button></div>
    </section>
    <aside aria-labelledby="pending-modules-title" class="module-pool-panel min-w-0 overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
      <header class="flex items-center gap-3 px-4 pb-3 pt-4"><span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconPuzzle :size="19" aria-hidden="true" /></span><div class="min-w-0 flex-1"><h2 id="pending-modules-title" class="text-sm font-semibold text-slate-800 dark:text-slate-100">待分配功能</h2><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">每项功能只归属一个 Bot</p></div><span class="rounded-full border border-blue-200 bg-blue-50 px-2 py-1 text-xs tabular-nums text-blue-700 dark:border-blue-900 dark:bg-blue-950/60 dark:text-blue-300">{{ unassigned.length }} 项</span></header>
      <p class="mx-4 mb-4 flex items-start gap-2 rounded-xl bg-slate-50 p-3 text-xs leading-5 text-slate-500 dark:text-slate-400 dark:bg-slate-950/50"><IconInfoCircle :size="15" class="mt-0.5 shrink-0 text-blue-600 dark:text-blue-400" aria-hidden="true" />拖动卡片排序或移入 Bot，也可直接选择 Bot 分配。</p>
      <div role="region" aria-label="待分配功能列表" tabindex="0" class="module-pool-content min-h-32 border-y border-slate-100 bg-white p-4 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-blue-500 dark:border-slate-800 dark:bg-zinc-900">
        <SortableList class="bot-module-list" :model-value="unassigned" :item-key="item => item.id" group="telegram-modules" :pull="true" :put="true" direction="vertical" item-class="w-full" handle=".telegram-module-card" filter="button:not([data-drag-handle]), a, input, select, textarea, label, [role=combobox]" :disabled="assignmentBusy" @update:model-value="stageList('', $event)" @start="startDrag" @end="endDrag">
          <template #item="{ item }"><TelegramModuleCard :module="item" :bots="bots" placement="pool" :busy="assignmentBusy || adding" @assign="assign" @create-bot="add" @move="moveModule" /></template>
          <template #empty><div class="grid w-full justify-items-center gap-2 px-3 py-6 text-center"><IconPuzzle :size="24" class="text-slate-300 dark:text-slate-600" aria-hidden="true" /><p class="text-xs leading-5 text-slate-500 dark:text-slate-400">所有功能均已分配<br />拖回此处可解除分配</p></div></template>
        </SortableList>
      </div>
      <footer class="flex items-center gap-2 px-4 py-3 text-[11px] text-slate-500 dark:text-slate-400"><IconHandMove :size="14" aria-hidden="true" /><span>排序仅保存在当前浏览器</span><span class="ml-auto tabular-nums">共 {{ modules.length }} 项功能</span></footer>
    </aside>
  </div>
  <FloatingDialog v-if="editingBot" title="编辑 Bot 配置" close-label="关闭 Bot 配置" size="config" @close="editingBot = undefined"><TelegramBotEditor :key="editingBot.id" :bot="editingBot" :token="token" embedded @changed="edited" /></FloatingDialog>
  <P115ActionDialog v-if="deletingBot" title="删除 Bot 配置？" :busy="deleting" @close="deletingBot = undefined">
    <p>确定删除“{{ deletingBot.name }}”？</p><p v-if="deletingModules.length" class="mt-2">它当前分配了 {{ deletingModules.length }} 项功能。这些功能会解除分配并返回待分配区域，不会自动改派到其他 Bot。</p><p v-else class="mt-2">该 Bot 没有已分配的功能。</p>
    <template #footer><button type="button" class="rounded-xl border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="deleting" @click="deletingBot = undefined">取消</button><button type="button" class="rounded-xl bg-red-600 px-3 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40" :disabled="deleting || busyModules.size > 0" @click="removeBot">{{ deleting ? '删除中' : '确认删除' }}</button></template>
  </P115ActionDialog>
</template>

<style scoped>
.bot-workspace { grid-template-columns: minmax(0, 1fr); gap: 24px; }
.bot-primary { container-type: inline-size; }
.bot-grid { grid-template-columns: minmax(0, 1fr); gap: 20px; }
.module-pool-panel { display: flex; flex-direction: column; border-color: #e4e4e7; box-shadow: 0 2px 15px rgb(0 0 0 / .04), 0 0 1px rgb(0 0 0 / .1); }
.module-pool-content { min-height: 0; }
.bot-module-list { flex-direction: column; align-items: stretch; gap: 12px; min-height: 116px; }
:global(.dark) .module-pool-panel { border-color: rgb(255 255 255 / .08); background: #18181b; }
@container (min-width: 760px) { .bot-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1280px) {
  .bot-workspace { grid-template-columns: minmax(0, 2fr) minmax(320px, 1fr); }
  .module-pool-panel { position: sticky; top: 5rem; max-height: min(640px, calc(100dvh - 6rem)); }
  .module-pool-content { overflow-y: auto; overscroll-behavior: contain; scrollbar-width: thin; scrollbar-color: #94a3b8 transparent; }
  :global(.dark) .module-pool-content { scrollbar-color: #64748b transparent; }
  .module-pool-panel > header, .module-pool-panel > p, .module-pool-panel > footer { flex-shrink: 0; }
}
@media (prefers-reduced-motion: no-preference) {
  .bot-action { transition: background-color 150ms ease, transform 150ms ease, box-shadow 150ms ease; }
  .bot-action:not(:disabled):active { transform: scale(.97); }
  .bot-list-enter-active, .bot-list-leave-active, .bot-list-move { transition: opacity 200ms ease, transform 200ms cubic-bezier(.22,1,.36,1); }
  .bot-list-enter-from, .bot-list-leave-to { opacity: 0; transform: translateY(8px) scale(.98); }
  .bot-list-leave-active { position: absolute; }
}
</style>
