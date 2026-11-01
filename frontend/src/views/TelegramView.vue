<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconHandMove, IconInfoCircle, IconPlus, IconPuzzle, IconRefresh } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import TelegramBotEditor from '../components/TelegramBotEditor.vue'
import TelegramBotCard from '../components/TelegramBotCard.vue'
import TelegramModuleCard from '../components/TelegramModuleCard.vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { telegramRequest } from '../lib/telegram-api'
import { p115Request } from '../lib/p115-api'
import { useToast } from '../composables/useToast'
import type { TelegramBotConfig, TelegramBotIdentity, TelegramBotModule } from '../types/telegramBots'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const toast = useToast()
const bots = ref<TelegramBotConfig[]>([]), modules = ref<TelegramBotModule[]>([]), accounts = ref<P115Account[]>([])
const loading = ref(true), refreshing = ref(false), adding = ref(false), deleting = ref(false)
const editingBot = ref<TelegramBotConfig>(), deletingBot = ref<TelegramBotConfig>()
const busyModules = ref(new Set<string>()), testingBots = ref(new Set<string>())
const draggingModuleId = ref(''), readError = ref('')
const poolDropTarget = ref(false)
const lifecycle = new AbortController()
let timer: number | undefined, sequence = 0
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])

const unassigned = computed(() => modules.value.filter(module => !module.assignedBotId))
const onlineCount = computed(() => bots.value.filter(bot => bot.connected).length)
const assignedCount = computed(() => modules.value.filter(module => !!module.assignedBotId).length)
const deletingModules = computed(() => modules.value.filter(module => module.assignedBotId === deletingBot.value?.id))
function moduleByBot(botID: string) { return modules.value.filter(module => module.assignedBotId === botID) }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，请重试' }

async function refresh(notify = false) {
  const current = ++sequence
  refreshing.value = true
  try {
    const [botData, moduleData] = await Promise.all([
      telegramRequest<{ bots: TelegramBotConfig[] }>(props.token, 'bots', { signal: signal() }),
      telegramRequest<{ modules: TelegramBotModule[] }>(props.token, 'modules', { signal: signal() }),
    ])
    if (current !== sequence || lifecycle.signal.aborted) return
    bots.value = botData.bots; modules.value = moduleData.modules; readError.value = ''
    if (notify) toast.success('Bot 状态与功能分配已刷新')
  } catch (reason) {
    if (current !== sequence || lifecycle.signal.aborted) return
    const failure = message(reason)
    if (notify || readError.value !== failure) toast.error(failure, { title: '读取 Bot 配置失败' })
    readError.value = failure
  } finally { if (current === sequence) { loading.value = false; refreshing.value = false } }
}

async function loadAccounts() {
  try {
    const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: signal() })
    if (!lifecycle.signal.aborted) accounts.value = data.accounts
  } catch (reason) { if (!lifecycle.signal.aborted) toast.error(message(reason), { title: '读取主账号失败' }) }
}
function setBusy(target: typeof busyModules, id: string, busy: boolean) {
  const next = new Set(target.value)
  busy ? next.add(id) : next.delete(id)
  target.value = next
}

async function updateModule(module: TelegramBotModule, botId: string) {
  if (busyModules.value.has(module.id) || deleting.value) return
  const previousBotId = module.assignedBotId
  if (previousBotId === botId) return
  const previousBotName = bots.value.find(bot => bot.id === previousBotId)?.name || '原 Bot'
  setBusy(busyModules, module.id, true)
  draggingModuleId.value = ''
  poolDropTarget.value = false
  try {
    await telegramRequest(props.token, 'modules/' + module.id, { method: 'PUT', body: JSON.stringify({ botId, expectedBotId: previousBotId }), signal: signal() })
    if (lifecycle.signal.aborted) return
    await refresh()
    const botName = bots.value.find(bot => bot.id === botId)?.name || 'Bot'
    if (!botId) toast.success(module.name + '已退回待分配功能池', { title: '已解除分配' })
    else if (previousBotId) toast.info(module.name + '已从' + previousBotName + '转移给' + botName, { title: '功能已转移' })
    else toast.success(module.name + '已分配给' + botName, { title: '分配成功' })
  } catch (reason) {
    if (!lifecycle.signal.aborted) { await refresh(); toast.error(message(reason), { title: '操作未完成' }) }
  } finally { setBusy(busyModules, module.id, false) }
}
function assign(module: TelegramBotModule, botId: string) { void updateModule(module, botId) }
function dropModule(moduleId: string, botId: string) {
  draggingModuleId.value = ''
  poolDropTarget.value = false
  const module = modules.value.find(item => item.id === moduleId)
  const bot = bots.value.find(item => item.id === botId)
  if (!module) { toast.warning('功能不存在，请刷新后重试'); return }
  if (!bot) { toast.warning('目标 Bot 不存在，请刷新后重试'); return }
  assign(module, botId)
}
function unassignModule(module: TelegramBotModule) { void updateModule(module, '') }

function poolDragOver(event: DragEvent) {
  if (!draggingModuleId.value || !event.dataTransfer?.types.includes('application/x-cinema115-telegram-module') || deleting.value) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'move'
  poolDropTarget.value = true
}
function poolDragLeave(event: DragEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)) poolDropTarget.value = false
}
function dropToPool(event: DragEvent) {
  event.preventDefault()
  const moduleId = event.dataTransfer?.getData('application/x-cinema115-telegram-module') || draggingModuleId.value
  poolDropTarget.value = false
  const module = modules.value.find(item => item.id === moduleId)
  if (module?.assignedBotId) unassignModule(module)
  else draggingModuleId.value = ''
}

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
  if (adding.value || deleting.value || bots.value.length >= 20) return
  adding.value = true
  try {
    const result = await telegramRequest<{ id: string }>(props.token, 'bots', { method: 'POST', body: JSON.stringify({ name: 'Bot ' + (bots.value.length + 1) }), signal: signal() })
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
    await telegramRequest(props.token, 'bots/' + bot.id, { method: 'DELETE', body: JSON.stringify({ revision: bot.revision, releaseModules: true }), signal: signal() })
    if (lifecycle.signal.aborted) return
    deletingBot.value = undefined
    await refresh()
    toast.success(releasedCount ? 'Bot 已删除，关联功能已退回待分配区' : 'Bot 已删除')
  } catch (reason) { if (!lifecycle.signal.aborted) { await refresh(); toast.error(message(reason), { title: '删除 Bot 失败' }) } }
  finally { deleting.value = false }
}
async function edited() { editingBot.value = undefined; await refresh() }

onMounted(async () => {
  await Promise.all([refresh(), loadAccounts()])
  const poll = async () => {
    if (!lifecycle.signal.aborted && !document.hidden && !editingBot.value && !deletingBot.value && !busyModules.value.size && !draggingModuleId.value && !refreshing.value) await refresh()
    if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 8000)
  }
  if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 8000)
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) window.clearTimeout(timer) })
</script>

<template>
  <ManagementLayout title="Telegram Bot" description="管理 Bot 连接，并将项目中的独立功能分配给唯一 Bot。" :parent="{ label: 'Telegram 配置', to: '/telegram' }" @logout="emit('logout')">
    <section class="mb-5 flex flex-wrap items-center gap-3" aria-label="Bot 配置概览">
      <div class="flex flex-wrap items-center gap-2 rounded-2xl border border-slate-200 bg-slate-100/70 p-1.5 dark:border-slate-800 dark:bg-slate-900/70">
        <span class="flex items-center gap-2 rounded-xl bg-white px-3 py-1.5 text-xs text-slate-500 shadow-sm dark:bg-slate-800 dark:text-slate-400">Bot 总数<strong class="font-semibold tabular-nums text-slate-800 dark:text-slate-100">{{ bots.length }}</strong></span>
        <span class="flex items-center gap-2 rounded-xl border border-emerald-200/70 bg-emerald-50 px-3 py-1.5 text-xs text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/30 dark:text-emerald-300">已连接<strong class="tabular-nums">{{ onlineCount }}</strong></span>
        <span class="flex items-center gap-2 rounded-xl border border-blue-200/70 bg-blue-50 px-3 py-1.5 text-xs text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-300">功能已分配<strong class="tabular-nums">{{ assignedCount }} / {{ modules.length }}</strong></span>
      </div>
      <div class="ml-auto flex items-center gap-2">
        <button type="button" :disabled="loading || refreshing" class="bot-action grid h-9 w-9 place-items-center rounded-xl border border-slate-200 bg-white text-slate-600 hover:bg-slate-100 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800" aria-label="刷新 Bot 与功能" title="刷新" @click="refresh(true)"><IconRefresh :size="16" :class="{ 'motion-safe:animate-spin': refreshing }" aria-hidden="true" /></button>
        <button type="button" :disabled="loading || adding || deleting || bots.length >= 20" class="bot-action inline-flex h-9 items-center gap-1.5 rounded-xl bg-blue-600 px-3 text-sm font-medium text-white shadow-sm shadow-blue-600/20 hover:bg-blue-700 disabled:opacity-40" @click="add"><IconPlus :size="16" aria-hidden="true" />{{ adding ? '添加中' : '新增 Bot' }}</button>
      </div>
    </section>
    <p v-if="loading" role="status" class="py-8 text-sm text-slate-500 dark:text-slate-400">正在读取 Bot 和功能配置…</p>
    <div v-else class="bot-workspace grid items-start gap-5">
      <section aria-labelledby="bot-list-title" class="bot-primary min-w-0">
        <header class="mb-3 flex items-center justify-between gap-3"><div><h2 id="bot-list-title" class="text-sm font-semibold text-slate-800 dark:text-slate-100">Bot 配置</h2><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">每项功能只归属一个 Bot；Bot 启用后运行已分配功能。</p></div><span class="rounded-full bg-slate-100 px-2 py-1 text-xs tabular-nums text-slate-500 dark:bg-slate-800 dark:text-slate-400">{{ bots.length }} / 20</span></header>
        <TransitionGroup v-if="bots.length" name="bot-list" tag="div" class="bot-grid relative grid min-w-0 items-start gap-4">
          <TelegramBotCard v-for="bot in bots" :key="bot.id" :bot="bot" :modules="moduleByBot(bot.id)" :busy-module-ids="busyModules" :dragging-module-id="draggingModuleId" :testing="testingBots.has(bot.id)" :disabled="deleting" @edit="editingBot = $event" @remove="deletingBot = $event" @test="testBot" @drop-module="dropModule" @unassign-module="unassignModule" @drag-start="draggingModuleId = $event" @drag-end="draggingModuleId = ''" />
        </TransitionGroup>
        <div v-else class="rounded-2xl border border-dashed border-slate-300 px-4 py-12 text-center dark:border-slate-700"><p class="text-sm font-medium text-slate-700 dark:text-slate-200">{{ readError ? '暂时无法读取 Bot 配置' : '还没有 Bot 配置' }}</p><p class="mt-2 text-xs text-slate-500 dark:text-slate-400">{{ readError || '新增 Bot 后填写 Token，再分配实际功能。' }}</p><button v-if="readError" type="button" class="mt-4 rounded-xl bg-blue-600 px-3 py-2 text-sm font-medium text-white" :disabled="refreshing" @click="refresh(true)">重新读取</button><button v-else type="button" class="mt-4 rounded-xl bg-blue-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-40" :disabled="adding" @click="add">新增 Bot</button></div>
      </section>
      <aside aria-labelledby="pending-modules-title" class="module-pool-panel min-w-0 overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900" :class="{ 'is-pool-drop-target': poolDropTarget && !!draggingModuleId }" @dragover="poolDragOver" @dragleave="poolDragLeave" @drop="dropToPool">
        <header class="flex items-center gap-3 px-4 pb-3 pt-4"><span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconPuzzle :size="19" aria-hidden="true" /></span><div class="min-w-0 flex-1"><h2 id="pending-modules-title" class="text-sm font-semibold text-slate-800 dark:text-slate-100">待分配功能</h2><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">每项功能只归属一个 Bot</p></div><span class="rounded-full border border-blue-200 bg-blue-50 px-2 py-1 text-xs tabular-nums text-blue-700 dark:border-blue-900 dark:bg-blue-950/60 dark:text-blue-300">{{ unassigned.length }} 项</span></header>
        <p class="mx-4 mb-4 flex items-start gap-2 rounded-xl bg-slate-50 p-3 text-xs leading-5 text-slate-500 dark:bg-slate-950/50 dark:text-slate-400"><IconInfoCircle :size="15" class="mt-0.5 shrink-0 text-blue-600 dark:text-blue-400" aria-hidden="true" />拖入 Bot 或点击“选择 Bot”分配。解除分配后才会返回此处。</p>
        <div class="module-pool-content min-h-32 border-y border-slate-100 bg-white p-4 dark:border-slate-800 dark:bg-zinc-900">
          <div class="relative space-y-3">
            <TelegramModuleCard v-for="module in unassigned" :key="module.id" :module="module" :bots="bots" placement="pool" :busy="deleting || busyModules.has(module.id)" @assign="assign" @drag-start="draggingModuleId = $event" @drag-end="draggingModuleId = ''" />
          </div>
          <div v-if="!unassigned.length" class="grid justify-items-center gap-2 px-3 py-8 text-center"><IconPuzzle :size="24" class="text-slate-300 dark:text-slate-600" aria-hidden="true" /><p class="text-xs leading-5 text-slate-500 dark:text-slate-400">所有功能均已分配<br />解除分配后会回到这里</p></div>
        </div>
        <footer class="flex items-center gap-2 px-4 py-3 text-[11px] text-slate-500 dark:text-slate-400"><IconHandMove :size="14" aria-hidden="true" /><span>支持拖拽或点选分配</span><span class="ml-auto tabular-nums">共 {{ modules.length }} 项功能</span></footer>
      </aside>
    </div>
    <FloatingDialog v-if="editingBot" title="编辑 Bot 配置" close-label="关闭 Bot 配置" size="config" @close="editingBot = undefined"><TelegramBotEditor :key="editingBot.id" :bot="editingBot" :token="token" :accounts="accounts" embedded @changed="edited" /></FloatingDialog>
    <P115ActionDialog v-if="deletingBot" title="删除 Bot 配置？" :busy="deleting" @close="deletingBot = undefined">
      <p>确定删除“{{ deletingBot.name }}”？</p><p v-if="deletingModules.length" class="mt-2">它当前分配了 {{ deletingModules.length }} 项功能。这些功能会解除分配并返回待分配区域，不会自动改派到其他 Bot。</p><p v-else class="mt-2">该 Bot 没有已分配的功能。</p>
      <template #footer><button type="button" class="rounded-xl border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="deleting" @click="deletingBot = undefined">取消</button><button type="button" class="rounded-xl bg-red-600 px-3 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40" :disabled="deleting || busyModules.size > 0" @click="removeBot">{{ deleting ? '删除中' : '确认删除' }}</button></template>
    </P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.bot-workspace { grid-template-columns: minmax(0, 1fr); gap: 24px; }
.bot-primary { container-type: inline-size; }
.bot-grid { grid-template-columns: minmax(0, 1fr); gap: 20px; }
.module-pool-panel { display: flex; flex-direction: column; border-color: #e4e4e7; box-shadow: 0 2px 15px rgb(0 0 0 / .04), 0 0 1px rgb(0 0 0 / .1); }
.module-pool-content { min-height: 0; overflow-y: auto; overscroll-behavior: contain; }
.module-pool-panel.is-pool-drop-target .module-pool-content { background: rgb(59 130 246 / .06); }
.module-pool-panel.is-pool-drop-target { border-color: rgb(59 130 246); box-shadow: 0 0 0 3px rgb(59 130 246 / .14), 0 8px 24px rgb(37 99 235 / .08); }
:global(.dark) .module-pool-panel { border-color: rgb(255 255 255 / .08); background: #18181b; }
:global(.dark) .module-pool-panel.is-pool-drop-target { border-color: rgb(96 165 250); }
@container (min-width: 760px) { .bot-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1280px) { .bot-workspace { grid-template-columns: minmax(0, 2fr) minmax(320px, 1fr); } .module-pool-panel { position: sticky; top: 5rem; max-height: calc(100dvh - 6rem); } .module-pool-panel > header, .module-pool-panel > p, .module-pool-panel > footer { flex-shrink: 0; } }
@media (prefers-reduced-motion: no-preference) {
  .bot-action { transition: background-color 150ms ease, transform 150ms ease, box-shadow 150ms ease; }
  .bot-action:not(:disabled):active { transform: scale(.97); }
  .bot-list-enter-active, .bot-list-leave-active, .bot-list-move { transition: opacity 200ms ease, transform 200ms cubic-bezier(.22,1,.36,1); }
  .bot-list-enter-from, .bot-list-leave-to { opacity: 0; transform: translateY(8px) scale(.98); }
  .bot-list-leave-active { position: absolute; }
}
</style>
