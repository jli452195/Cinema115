<script setup lang="ts">
import ManagementLayout from '../components/ManagementLayout.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ShareTaskLogs from '../components/ShareTaskLogs.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { cancelShareRun, generateShareSTRM } from '../utils/shareGeneration'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import TelegramBotSelect from '../components/TelegramBotSelect.vue'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig } from '../types/telegramBots'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import { validSTRMDirectory as validDirectory, strmDirectoryPath as resolvedDirectory, strmDirectoryName as directoryName } from '../utils/strmDirectory'

const props = defineProps<{ token: string }>()
const activeTab = ref<'basic' | 'generate' | 'logs'>('generate')
const generationMode = ref<'direct' | 'reshare'>('reshare')
const passwordExpanded = ref(false)
let parsedShareURL = ''
const accounts = ref<P115Account[]>([])
const accountId = ref('')
const bots = ref<TelegramBotConfig[]>([])
const botId = ref('')
const telegramEnabled = ref(false)
const tabs = [{ id: 'generate', label: '生成 STRM', icon: '↗' }, { id: 'logs', label: '任务记录', icon: '▤' }, { id: 'basic', label: '设置', icon: '⚙' }] as const
const reviewRetryEnabled = ref(false)
const reviewRetryMinutes = ref(30)
const reviewRetryMax = ref(6)
defineEmits<{ back: []; openTelegram: []; logout: [] }>()
interface ShareFile { id: string; name: string; size: number; directory: boolean }
const shareURL = ref('')
const password = ref('')
const files = ref<ShareFile[]>([])
const folders = ref([{ id: '0', name: '分享根目录' }])
const offset = ref(0)
const count = ref(0)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const playURL = ref('')
const playName = ref('')
const initializing = ref(true)
const generating = ref(false)
const browsed = ref(false)
const baseURL = ref('')
const outputDir = ref('strm')
const taskOutputDir = ref('strm')
const outputDirectories = ref(['strm'])
const directoryDialogOpen = ref(false)
const videoExtensions = ref('mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,m2ts,webm')
const metadataExtensions = ref('srt,ass,ssa')
const minSizeMB = ref(0)
const sharePath = ref('/')
const startCID = ref('0')
const generated = ref<{ cached?: boolean; count: number; metadataCount?: number; metadataFailed?: number; conflictRenamed?: number; directory: string; reshareUrl?: string; cleaned?: boolean }>()
const reshareEnabled = ref(false)
const savedReshareEnabled = ref(false)
const parentCID = ref('0')
const parentPath = ref('')
const parentDialogOpen = ref(false), parentOpening = ref(false)
const parentAccountId = ref('')
const parentSaving = ref(false), parentError = ref('')
const savedSettingsSnapshot = ref('')
const savedGeneration = ref<ReturnType<typeof settings>>()
const settingsSnapshot = () => JSON.stringify([settings(), botId.value, telegramEnabled.value, reshareEnabled.value, fixedSharePassword.value, tgBatchWaitSeconds.value, reviewRetryEnabled.value, reviewRetryMinutes.value, reviewRetryMax.value])
const settingsDirty = computed(() => automationReady.value && settingsSnapshot() !== savedSettingsSnapshot.value)
const parentLabel = computed(() => parentCID.value === '0' ? '115 根目录 /' : parentPath.value || (parentCID.value ? `已配置目录（ID：${parentCID.value}）` : '请选择主网盘目录'))

async function openParentDirectory() {
  if (parentOpening.value || busy.value || automationSaving.value || parentSaving.value) return
  parentError.value = ''
  parentOpening.value = true; error.value = ''
  try {
    const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.timeout(15000) })
    if (!mounted) return
    accounts.value = data.accounts
    const primary = data.accounts.find(account => account.primary)
    if (!primary?.configured) throw new Error('请先在 115 助手中登录主账号')
    if (accountId.value !== primary.id) {
      accountId.value = primary.id; parentCID.value = ''; parentPath.value = ''
    }
    parentAccountId.value = primary.id
    parentDialogOpen.value = true
  } catch (reason) { if (mounted) error.value = reason instanceof Error ? reason.message : '读取主账号失败' }
  finally { parentOpening.value = false }
}
async function selectParentDirectory(value: { id: string; path: string }) {
  if (parentSaving.value) return
  if (parentAccountId.value !== accountId.value) { parentError.value = '主账号已变化，请重新选择目录'; return }
  parentSaving.value = true; parentError.value = ''
  try {
    const data = await request('automation-config', { accountId: parentAccountId.value, parentCID: value.id, parentPath: value.path })
    if (!mounted) return
    if (data.settings.accountId !== parentAccountId.value) throw new Error('主账号已变化，请重新选择目录')
    parentCID.value = data.settings.parentCID; parentPath.value = data.settings.parentPath ?? ''
    parentDialogOpen.value = false; error.value = ''
    automationNotice.value = '转存父目录已保存，后续任务使用此目录。'
  } catch (reason) { parentError.value = reason instanceof Error ? reason.message : '保存目录失败' }
  finally { parentSaving.value = false }
}
const fixedSharePassword = ref('')
const tgBatchWaitSeconds = ref(5)
const automationSaving = ref(false)
const automationReady = ref(false)
const botConfigured = ref(false)
const automationNotice = ref('')
const partialShareURL = ref('')
const generationProgress = ref('')
const generationSeconds = ref(0)
const activeRunID = ref('')
const stoppingRun = ref(false)
let controller: AbortController | undefined
let mounted = true
const settingsKey = 'cinema115.strm-settings.v2'

async function request(endpoint: string, body?: object) {
  const response = await fetch(`/api/admin/p115/share/${endpoint}`, {
    method: body === undefined ? 'GET' : 'POST', signal: endpoint === 'automation-config' ? AbortSignal.timeout(15000) : controller?.signal,
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok && data?.reshareUrl) partialShareURL.value = data.reshareUrl
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请退出后重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  return data
}

function settings() {
  return { baseUrl: baseURL.value.trim(), outputDir: outputDir.value, videoExtensions: videoExtensions.value, metadataExtensions: metadataExtensions.value, minSizeMB: minSizeMB.value, recursive: true, sharePath: sharePath.value.trim() || '/' }
}

function taskSettings() {
  return { ...(savedGeneration.value ?? settings()), outputDir: taskOutputDir.value }
}

function parseShareText() {
  const text = shareURL.value.trim()
  const links = text.match(/https?:\/\/(?:115\.com|115cdn\.com|anxia\.com)\/s\/[a-zA-Z0-9]+(?:\?[^\s<>"，。]*)?/g)
  if (!links?.length) return
  if (links.length > 1) { error.value = '本页一次提交一个分享，请分别提交多个链接'; return }
  const url = new URL(links[0])
  const code = url.searchParams.get('password') || text.match(/(?:提取码|访问码|密码)\s*[:：]?\s*([a-zA-Z0-9]{4})/)?.[1]
  if (parsedShareURL !== url.href) { password.value = code ?? ''; startCID.value = '0' }
  else if (text !== links[0] && code) password.value = code
  parsedShareURL = url.href
  shareURL.value = url.href
  passwordExpanded.value = false
}

function saveSettings() {
  try { localStorage.setItem(settingsKey, JSON.stringify({ ...settings(), outputDirectories: outputDirectories.value })) } catch { /* 浏览器禁用存储时仍可生成。 */ }
}

async function saveAutomation() {
  if (automationSaving.value || parentSaving.value || busy.value || !automationReady.value) return
  automationSaving.value = true; automationNotice.value = ''; error.value = ''
  try {
    if (!validDirectory(outputDir.value)) throw new Error('请选择 STRM 生成目录')
    if (!/^\d+$/.test(parentCID.value)) throw new Error('请选择 115 转存目录')
    if (!accounts.value.some(account => account.primary && account.id === accountId.value && account.configured)) throw new Error('请先在 115 助手中登录主账号')
    if (telegramEnabled.value && !bots.value.some(bot => bot.id === botId.value && bot.configured && bot.enabled && bot.notificationChatID)) throw new Error('请先选择已启用且配置了通知聊天的 Telegram Bot')
    if (!Number.isInteger(tgBatchWaitSeconds.value) || tgBatchWaitSeconds.value < 0 || tgBatchWaitSeconds.value > 300) throw new Error('批次收集等待应为 0～300 秒的整数')
    const data = await request('automation-config', { accountId: accountId.value, botId: botId.value, telegramEnabled: telegramEnabled.value, tgBatchWaitSeconds: tgBatchWaitSeconds.value, reshareEnabled: reshareEnabled.value, parentCID: parentCID.value.trim(), parentPath: parentPath.value, fixedSharePassword: fixedSharePassword.value.trim(), reviewRetryEnabled: reviewRetryEnabled.value, reviewRetryMinutes: reviewRetryMinutes.value, reviewRetryMax: reviewRetryMax.value, generation: settings() })
    accountId.value = data.settings.accountId ?? ''
    botId.value = data.settings.botId ?? ''
    telegramEnabled.value = data.settings.telegramEnabled ?? false
    botConfigured.value = data.botConfigured ?? false
    savedReshareEnabled.value = data.settings.reshareEnabled
    parentCID.value = data.settings.parentCID
    parentPath.value = data.settings.parentPath ?? ''
    fixedSharePassword.value = data.settings.fixedSharePassword ?? ''
    tgBatchWaitSeconds.value = data.settings.tgBatchWaitSeconds ?? 5
    savedSettingsSnapshot.value = settingsSnapshot()
    if (taskOutputDir.value === savedGeneration.value?.outputDir) taskOutputDir.value = outputDir.value
    savedGeneration.value = settings()
    automationNotice.value = '生成设置已保存到服务器，播放地址与开关立即生效，Bot 使用本页保存的生成选项。'
    saveSettings()
  } catch (e) {
    reshareEnabled.value = savedReshareEnabled.value
    error.value = e instanceof Error ? e.message : String(e)
  } finally { automationSaving.value = false }
}

function addDirectory(value: string) {
  if (!validDirectory(value)) return
  if (activeTab.value === 'generate') { taskOutputDir.value = value; directoryDialogOpen.value = false; error.value = ''; return }
  if (!outputDirectories.value.includes(value)) outputDirectories.value.push(value)
  outputDir.value = value; directoryDialogOpen.value = false; error.value = ''
  saveSettings()
}

function removeDirectory(value: string) {
  outputDirectories.value = outputDirectories.value.filter(item => item !== value)
  if (outputDir.value === value) outputDir.value = outputDirectories.value[0] ?? ''
  saveSettings()
}

async function generate() {
  if (busy.value || initializing.value || automationSaving.value) return
  if (!validDirectory(taskOutputDir.value)) { error.value = '请选择 STRM 生成目录'; return }
  parseShareText()
  if (!/^https?:\/\/(?:115\.com|115cdn\.com|anxia\.com)\/s\/[a-zA-Z0-9]+(?:\?\S*)?$/.test(shareURL.value.trim())) { error.value = '请输入一个有效的 115 分享链接'; return }
  if (generationMode.value === 'reshare' && !parentCID.value) { error.value = '请先选择 115 转存父目录'; return }
  controller = new AbortController()
  busy.value = true; generating.value = true; error.value = ''; notice.value = ''; playURL.value = ''
  generated.value = undefined; partialShareURL.value = ''; activeRunID.value = ''; stoppingRun.value = false
  generationProgress.value = '正在连接服务器…'; generationSeconds.value = 0
  const activeController = controller
  const startedAt = Date.now()
  const elapsedTimer = window.setInterval(() => { generationSeconds.value = Math.floor((Date.now() - startedAt) / 1000) }, 1000)
  // 服务端限制 5 分钟，浏览器再留 30 秒接收最终结果，网络异常也会恢复按钮。
  const timeout = window.setTimeout(() => activeController.abort('generation-timeout'), 330000)
  try {
    const options = taskSettings()
    // 仅记住生成选项，不把分享链接、提取码或 Cookie 写入本地配置。
    saveSettings()
    const data = await generateWithProgress({ ...options, mode: generationMode.value, url: shareURL.value.trim(), password: password.value.trim(), cid: startCID.value.trim() || '0' }, activeController.signal)
    if (!mounted) return
    generated.value = { ...data, directory: resolvedDirectory(options.outputDir) }
  } catch (e) {
    if (mounted) error.value = activeController.signal.aborted
      ? '已停止查询进度，后台任务仍会继续。请查看分享日志或 Telegram 通知，避免重复转存。'
      : e instanceof Error ? e.message : String(e)
  }
  finally { window.clearTimeout(timeout); window.clearInterval(elapsedTimer); busy.value = false; generating.value = false }
}

function generateWithProgress(body: object, signal: AbortSignal) {
  return generateShareSTRM(props.token, body, signal, message => { generationProgress.value = message }, url => { partialShareURL.value = url }, id => { activeRunID.value = id })
}

async function stopGeneration() {
  if (!activeRunID.value || stoppingRun.value) return
  stoppingRun.value = true
  try { await cancelShareRun(props.token, activeRunID.value) }
  catch (e) { error.value = e instanceof Error ? e.message : '停止后台任务失败'; stoppingRun.value = false }
}

async function browse(next = [{ id: startCID.value.trim() || '0', name: startCID.value.trim() && startCID.value !== '0' ? '选定目录' : '分享根目录' }], start = 0) {
  if (busy.value) return
  parseShareText()
  controller?.abort()
  controller = new AbortController()
  busy.value = true; error.value = ''; notice.value = ''; playURL.value = ''
  try {
    const data = await request('files', { url: shareURL.value, password: password.value, cid: next[next.length - 1].id, offset: start })
    if (!mounted) return
    files.value = data.files; count.value = data.count; offset.value = start; folders.value = next; browsed.value = true
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { busy.value = false }
}

async function select(file: ShareFile, play: boolean) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''; playURL.value = ''
  try {
    const data = await request('strm', { url: shareURL.value, password: password.value, fileId: file.id, name: file.name, baseUrl: taskSettings().baseUrl })
    if (play) {
      // 先用 HEAD 验证登录与直链，避免播放器只显示通用的媒体错误。
      const response = await fetch(data.playUrl, { method: 'HEAD', signal: controller?.signal })
      if (!response.ok) throw new Error(`播放地址不可用（HTTP ${response.status}），请检查 115 登录状态或分享权限`)
      playURL.value = data.playUrl; playName.value = file.name
    } else {
      const objectURL = URL.createObjectURL(new Blob([data.content], { type: 'text/plain;charset=utf-8' }))
      const link = document.createElement('a'); link.href = objectURL; link.download = data.name; link.click()
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 1000)
      notice.value = 'STRM 已生成，请放入 Emby 媒体库。生成文件不代表已经通过实际播放验证。'
    }
  } catch (e) { error.value = e instanceof Error ? e.message : String(e) }
  finally { busy.value = false }
}

function size(value: number) { return `${(value / 1024 ** 3).toFixed(2)} GB` }
watch([shareURL, password, startCID], () => { files.value = []; browsed.value = false; playURL.value = ''; error.value = ''; notice.value = '' })
onMounted(async () => {
  controller = new AbortController()
  try {
    const [config, automation, accountData, botData] = await Promise.all([request('generation-config'), request('automation-config'), p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: controller.signal }), telegramRequest<{ bots: TelegramBotConfig[] }>(props.token, 'bots', { signal: controller.signal })])
    if (!mounted) return
    accounts.value = accountData.accounts
    bots.value = botData.bots
    accountId.value = automation.settings.accountId ?? ''
    botId.value = automation.settings.botId ?? ''
    telegramEnabled.value = automation.settings.telegramEnabled ?? false
    reshareEnabled.value = automation.settings.reshareEnabled
    savedReshareEnabled.value = automation.settings.reshareEnabled
    parentCID.value = automation.settings.parentCID
    parentPath.value = automation.settings.parentPath ?? ''
    fixedSharePassword.value = automation.settings.fixedSharePassword ?? ''
    tgBatchWaitSeconds.value = automation.settings.tgBatchWaitSeconds ?? 5
    reviewRetryEnabled.value = automation.settings.reviewRetryEnabled ?? false
    reviewRetryMinutes.value = automation.settings.reviewRetryMinutes ?? 30
    reviewRetryMax.value = automation.settings.reviewRetryMax ?? 6
    botConfigured.value = automation.botConfigured
    automationReady.value = true
    baseURL.value = config.baseUrl || window.location.origin; videoExtensions.value = config.videoExtensions
    try {
      // 保留旧版普通选项，绝对路径目录不会迁移到新的相对目录配置。
      const saved = JSON.parse(localStorage.getItem(settingsKey) ?? localStorage.getItem('cinema115.strm-settings.v1') ?? 'null')
      if (saved && typeof saved === 'object') {
        // 播放地址以服务器保存值为准，旧浏览器缓存不能覆盖新地址。
        if (Array.isArray(saved.outputDirectories)) {
          const valid = saved.outputDirectories.filter(validDirectory)
          if (valid.length) outputDirectories.value = [...new Set(valid)] as string[]
        }
        if (typeof saved.outputDir === 'string' && outputDirectories.value.includes(saved.outputDir)) outputDir.value = saved.outputDir
        else outputDir.value = outputDirectories.value[0] ?? 'strm'
        if (typeof saved.videoExtensions === 'string') videoExtensions.value = saved.videoExtensions
        if (Number.isSafeInteger(saved.minSizeMB) && saved.minSizeMB >= 0) minSizeMB.value = saved.minSizeMB
      }
    } catch { /* 忽略损坏的本地选项，继续使用后端默认配置。 */ }
    // 已保存的服务器选项优先，保证换浏览器或刷新后配置一致。
    const saved = automation.settings.generation
    if (saved) {
      baseURL.value = saved.baseUrl || baseURL.value
      videoExtensions.value = saved.videoExtensions || config.videoExtensions
      metadataExtensions.value = saved.metadataExtensions ?? config.metadataExtensions ?? 'srt,ass,ssa'
      minSizeMB.value = saved.minSizeMB ?? 0
      sharePath.value = saved.sharePath || '/'
      if (validDirectory(saved.outputDir)) {
        outputDir.value = saved.outputDir
        if (!outputDirectories.value.includes(saved.outputDir)) outputDirectories.value.push(saved.outputDir)
      }
    }
    savedGeneration.value = settings()
    savedSettingsSnapshot.value = settingsSnapshot()
    taskOutputDir.value = outputDir.value
    generationMode.value = reshareEnabled.value ? 'reshare' : 'direct'
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : String(e) }
  finally { initializing.value = false }
})
watch(outputDir, () => { if (!initializing.value) saveSettings() })
onBeforeUnmount(() => { mounted = false; controller?.abort() })
</script>

<template>
  <ManagementLayout title="115分享STRM" description="管理分享生成、转存再分享与审核重试，查看任务处理记录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template v-if="activeTab === 'basic'" #actions>
      <span v-if="settingsDirty" class="text-xs text-amber-700 dark:text-amber-300">有未保存修改</span><button type="button" class="shrink-0 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="busy || initializing || automationSaving || !automationReady || !settingsDirty" @click="saveAutomation">{{ automationSaving ? '保存中…' : settingsDirty ? '保存设置' : '已保存' }}</button>
    </template>
      <nav class="mb-5 flex overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 shadow-sm" aria-label="115 分享 STRM 功能">
        <button v-for="tab in tabs" :key="tab.id" type="button" :aria-current="activeTab === tab.id ? 'page' : undefined" class="flex shrink-0 items-center gap-2 border-b-2 px-4 py-4 text-sm transition" :class="activeTab === tab.id ? 'border-blue-600 dark:border-blue-400 text-blue-600 dark:text-blue-400' : 'border-transparent text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/50'" @click="activeTab = tab.id"><span aria-hidden="true">{{ tab.icon }}</span>{{ tab.label }}</button>
      </nav>
      <p v-if="automationNotice" role="status" class="mb-5 rounded-xl bg-emerald-50 dark:bg-emerald-950/30 px-4 py-3 text-sm text-emerald-700 dark:text-emerald-300">{{ automationNotice }}</p>
      <ShareTaskLogs v-if="activeTab === 'logs'" :token="token" />
      <section v-else class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7" :class="activeTab === 'generate' ? 'mx-auto w-full max-w-4xl' : ''">
        <form @submit.prevent="activeTab === 'basic' ? saveAutomation() : generate()">
          <fieldset :disabled="busy || initializing || automationSaving || !automationReady" class="min-w-0 space-y-6 disabled:opacity-70">
            <template v-if="activeTab === 'basic'">
              <div class="grid gap-5 border-b border-slate-100 pb-6 dark:border-slate-800 md:grid-cols-2">
                <div>
                  <TelegramBotSelect id="share-bot" v-model="botId" :bots="bots" :required="telegramEnabled" />
                  <p class="mt-2 text-xs leading-5 text-slate-400 dark:text-slate-400">生成成功通知和消息接收固定使用此配置。<RouterLink to="/telegram/bot" class="text-blue-600 dark:text-blue-400 hover:underline">管理 Bot</RouterLink></p>
                </div>
                <SettingSwitch v-model="telegramEnabled" label="启用 Telegram Bot" description="启用后，由所选 Bot 接收分享链接，并把生成成功结果发送到该 Bot 的通知聊天。" />
              </div>
              <div><h2 class="font-semibold text-slate-800 dark:text-slate-200">生成规则</h2><p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">各入口共用已保存的生成规则。修改后点击右上角保存，进行中的任务保持原设置。</p></div>
              <P115PrimaryStatus v-if="automationReady" :accounts="accounts" />
          <div>
            <div class="grid gap-x-6 gap-y-5 md:grid-cols-2">

              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">最小文件大小（MB）<input v-model.number="minSizeMB" type="number" required min="0" max="1073741824" step="1" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400 dark:text-slate-400">设为 0 不限制；小于此大小的文件会跳过。</span></label>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">视频后缀<input v-model="videoExtensions" required class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400 dark:text-slate-400">使用逗号分隔，只为匹配的文件生成 STRM。</span></label>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">元数据后缀<input v-model="metadataExtensions" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 font-normal outline-none focus:border-blue-500" placeholder="srt,ass,ssa" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400 dark:text-slate-400">使用逗号分隔，按原格式保存字幕等配套文件；留空则不下载，不受视频最小大小限制。</span></label>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">分享路径<input v-model="sharePath" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950" placeholder="/" maxlength="4096" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400 dark:text-slate-400">分享链接内的路径，不懂请保持默认 /。例如 /电影，包含该路径下的所有子目录。</span></label>
            </div>
          </div>
          <div class="border-t border-slate-100 dark:border-slate-800 pt-6">
            <h2 class="mb-2 font-semibold text-slate-800 dark:text-slate-200">STRM 生成目录</h2>
            <p class="mb-4 text-xs leading-6 text-slate-500 dark:text-slate-400">从媒体库挂载目录的真实文件夹中选择。页面统一显示为 /strm，Docker 中对应 NAS 挂载路径，Windows 中对应 STRM_ROOT。</p>
            <div class="grid gap-3 md:grid-cols-2">
              <div v-for="directory in outputDirectories" :key="directory" class="flex items-start gap-3 rounded-xl border p-4" :class="outputDir === directory ? 'border-blue-400 dark:border-blue-500 bg-blue-50 dark:bg-blue-950/30' : 'border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950'">
                <label class="flex min-w-0 flex-1 cursor-pointer items-start gap-3"><input v-model="outputDir" type="radio" name="output-directory" :value="directory" :aria-label="'生成到 ' + resolvedDirectory(directory)" class="mt-3 accent-blue-600" /><span class="grid h-10 w-10 shrink-0 place-items-center rounded-lg border border-blue-100 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 text-amber-500"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 7.5h6l2 2h10v9H3z"/><path d="M3 7.5V5h6l2 2h8v2.5"/></svg></span><span class="min-w-0 self-center"><span class="block break-all text-sm font-semibold text-slate-800 dark:text-slate-200">{{ directoryName(directory) }}</span><span class="mt-1 block break-all text-xs leading-5 text-slate-500 dark:text-slate-400">{{ resolvedDirectory(directory) }}</span></span></label>
                <button type="button" :aria-label="'移除目录 ' + resolvedDirectory(directory)" class="text-xs text-slate-400 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400" @click="removeDirectory(directory)">移除</button>
              </div>
            </div>
            <p v-if="outputDirectories.length === 0" class="rounded-xl border border-dashed border-slate-200 dark:border-slate-700 px-4 py-6 text-center text-sm text-slate-400 dark:text-slate-400">尚未选择 STRM 生成目录</p>
            <button type="button" class="mt-4 rounded-lg bg-blue-50 dark:bg-blue-950/30 px-4 py-3 text-sm text-blue-600 dark:text-blue-400 hover:bg-blue-100 dark:hover:bg-blue-900/40" @click="directoryDialogOpen = true">＋ 从真实文件夹添加目录</button>
            <p class="mt-3 break-all text-xs leading-6 text-slate-500 dark:text-slate-400">生成位置：{{ outputDir ? resolvedDirectory(outputDir) : '未选择' }}</p><p class="text-xs leading-6 text-slate-400 dark:text-slate-400">选择器只显示媒体库挂载范围内的真实文件夹；移除选项不会删除文件夹。保留分享文件夹结构；相同内容直接复用，同名但内容不同会保留旧文件并另存。</p>
          </div>

              <section class="space-y-4 border-t border-slate-100 pt-6 dark:border-slate-800">
              <SettingSwitch v-model="reshareEnabled" label="默认使用转存再分享" description="用于 Bot 和未指定模式的任务；网页生成区可单独切换生成方式。" />

                <h2 class="font-semibold text-slate-800 dark:text-slate-200">转存默认值</h2>
                <div class="flex items-center gap-3 rounded-lg bg-slate-50 px-4 py-3 dark:bg-slate-950"><span class="shrink-0 text-sm text-slate-500 dark:text-slate-400">115 转存父目录</span><span class="min-w-0 flex-1 break-all text-sm text-slate-800 dark:text-slate-200">{{ parentLabel }}</span><button type="button" :disabled="parentOpening" class="shrink-0 text-sm text-blue-600 dark:text-blue-400" @click="openParentDirectory">更改</button></div>
                <label class="block max-w-xs text-sm text-slate-700 dark:text-slate-300">新分享提取码<input v-model="fixedSharePassword" maxlength="4" pattern="[a-zA-Z0-9]{4}" class="mt-2 block w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" placeholder="留空随机生成" /></label>
                <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">父目录在选择后单独保存。每批创建独立临时目录，生成成功后移入回收站，失败时保留。</p>
              </section>
              <section class="space-y-4 border-t border-slate-100 pt-6 dark:border-slate-800">
                <h2 class="font-semibold text-slate-800 dark:text-slate-200">自动收集与队列</h2>
                <label class="block max-w-xs text-sm text-slate-700 dark:text-slate-300">停止新增后等待（秒）<input v-model.number="tgBatchWaitSeconds" type="number" min="0" max="300" step="1" required class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
                <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">新增资源重新计时，重复提交不延时；到期后开始处理，后续资源进入下一批。0 表示立即执行，不设最长收集时间。</p>
                <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">用于详情页 Bot、观影搜索和 Telegram 转存；订阅批次共用此等待时间。本页手动生成立即提交，不等待收集。</p>
                <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">公共 STRM 队列的不同批次并行处理，保留队列容量和 115 接口限速。Telegram 自身的接收与回复仍按顺序处理。</p>
              <SettingSwitch v-model="reviewRetryEnabled" label="分享审核中自动重试" description="无法读取审核中的分享时，后台定时重试；进度可在日志查看。关闭并保存后停止待重试任务。" class="md:col-span-2">
                <div v-if="reviewRetryEnabled" class="ml-12 mt-4 grid max-w-md grid-cols-2 gap-4">
                  <label class="text-xs text-slate-500 dark:text-slate-400">重试间隔（分钟）<input v-model.number="reviewRetryMinutes" type="number" min="1" max="1440" step="1" required class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-sm text-slate-800 dark:text-slate-200" /></label>
                  <label class="text-xs text-slate-500 dark:text-slate-400">最大重试次数<input v-model.number="reviewRetryMax" type="number" min="1" max="100" step="1" required class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-sm text-slate-800 dark:text-slate-200" /></label>
                </div>
              </SettingSwitch>
              </section>
              <section class="space-y-4 border-t border-slate-100 pt-6 dark:border-slate-800">
                <h2 class="font-semibold text-slate-800 dark:text-slate-200">服务地址</h2>
              <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">STRM 播放地址<input v-model="baseURL" required type="url" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 font-normal outline-none focus:border-blue-500" placeholder="http://NAS_IP:8115" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400 dark:text-slate-400">填写 Emby 能访问的 Cinema115 地址，点击「保存设置」生效。首次使用默认填入当前访问地址。</span></label>

              </section>
            </template>
            <template v-else>
              <div class="mx-auto w-full max-w-3xl space-y-5">
                <div class="inline-flex rounded-lg bg-slate-100 p-1 dark:bg-slate-800" aria-label="生成方式">
                  <button v-for="mode in [{ id: 'direct', label: '直接生成' }, { id: 'reshare', label: '转存再分享' }] as const" :key="mode.id" type="button" :aria-pressed="generationMode === mode.id" class="rounded-md px-4 py-2 text-sm transition" :class="generationMode === mode.id ? 'bg-white text-blue-600 shadow-sm dark:bg-slate-700 dark:text-blue-300' : 'text-slate-600 dark:text-slate-400'" @click="generationMode = mode.id">{{ mode.label }}</button>
                </div>
                <p class="text-sm text-slate-500 dark:text-slate-400">{{ generationMode === 'direct' ? '使用原分享生成 STRM，无需转存。' : '转存到主网盘并创建长期分享，再生成 STRM。' }}</p>
                <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">分享链接<input v-model="shareURL" required type="text" autocomplete="off" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950" placeholder="粘贴 115 分享链接或包含提取码的分享文本" @change="parseShareText" /></label>
                <div class="flex flex-wrap items-center gap-3 text-xs"><span v-if="password && !passwordExpanded" class="text-slate-500 dark:text-slate-400">已填写提取码</span><button type="button" class="text-blue-600 dark:text-blue-400" @click="passwordExpanded = !passwordExpanded">{{ passwordExpanded ? '收起提取码' : password ? '修改提取码' : '补充提取码' }}</button><input v-if="passwordExpanded" v-model="password" aria-label="分享提取码" maxlength="4" class="w-32 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-950" placeholder="4 位提取码" /></div>
                <div class="divide-y divide-slate-100 border-y border-slate-100 dark:divide-slate-800 dark:border-slate-800">
                  <div class="flex items-center gap-3 py-3"><span class="w-28 shrink-0 text-sm text-slate-500 dark:text-slate-400">STRM 输出目录</span><span class="min-w-0 flex-1 break-all text-sm text-slate-800 dark:text-slate-200">{{ resolvedDirectory(taskOutputDir) }}</span><button type="button" class="shrink-0 text-sm text-blue-600 dark:text-blue-400" @click="directoryDialogOpen = true">更改</button></div>
                  <div v-if="generationMode === 'reshare'" class="flex items-center gap-3 py-3"><span class="w-28 shrink-0 text-sm text-slate-500 dark:text-slate-400">115 转存父目录</span><span class="min-w-0 flex-1 break-all text-sm text-slate-800 dark:text-slate-200">{{ parentLabel }}</span><button type="button" :disabled="parentOpening" class="shrink-0 text-sm text-blue-600 dark:text-blue-400" @click="openParentDirectory">{{ parentOpening ? '读取中…' : '更改' }}</button></div>
                </div>
                <div class="flex flex-wrap items-center gap-3"><button type="submit" :disabled="!shareURL.trim()" class="rounded-lg bg-blue-600 px-5 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40">{{ generating ? '正在生成…' : '开始生成' }}</button><button type="button" :disabled="!shareURL.trim()" class="rounded-lg px-4 py-3 text-sm text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800" @click="browse()">预览文件</button></div>
                <p v-if="startCID !== '0'" class="text-xs text-slate-500 dark:text-slate-400">仅生成预览中选定的子目录。<button type="button" class="ml-2 text-blue-600 dark:text-blue-400" @click="startCID = '0'">恢复配置的分享路径</button></p>
                <p v-else-if="savedGeneration?.sharePath && savedGeneration.sharePath !== '/'" class="break-all text-xs text-slate-500 dark:text-slate-400">生成范围：{{ savedGeneration.sharePath }}（含子目录）</p>
                <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">输出目录仅用于本次提交，转存父目录保存后供后续任务使用。<button type="button" class="ml-1 text-blue-600 dark:text-blue-400" @click="activeTab = 'basic'">生成设置</button></p>
              </div>
            </template>

          </fieldset>
        </form>
      <section v-if="activeTab === 'generate' && generating" role="status" class="mt-5 rounded-xl bg-blue-50 dark:bg-blue-950/30 p-4 text-sm leading-6 text-blue-700 dark:text-blue-300"><div class="flex items-center justify-between gap-4"><p class="font-medium">{{ generationProgress }}</p><button v-if="activeRunID" type="button" :disabled="stoppingRun" class="shrink-0 rounded-lg border border-blue-200 dark:border-blue-800 px-3 py-1 text-xs hover:bg-white dark:hover:bg-slate-800 dark:bg-slate-900 disabled:opacity-50" @click="stopGeneration">{{ stoppingRun ? '正在停止…' : '停止任务' }}</button></div><p class="mt-2 text-xs text-blue-600 dark:text-blue-400">已用时 {{ generationSeconds }} 秒 · 提交成功后，关闭页面不会取消后台任务。每条执行最多 5 分钟，结果可在分享日志及 Telegram 通知查看。</p></section>
      <section v-if="activeTab === 'generate' && generated" role="status" class="mt-6 rounded-xl border p-5" :class="generated.metadataFailed ? 'border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30' : 'border-emerald-100 dark:border-emerald-900 bg-emerald-50 dark:bg-emerald-950/30'"><h2 class="font-semibold" :class="generated.metadataFailed ? 'text-amber-900 dark:text-amber-200' : 'text-emerald-800 dark:text-emerald-200'">{{ generated.cached ? `本地文件完整 · 已复用 ${generated.count} 个 STRM` : generated.metadataFailed ? `STRM 已生成 ${generated.count} 个 · 元数据部分失败` : `生成完成 · 已处理 ${generated.count} 个 STRM` }}</h2><p class="mt-2 break-all text-xs leading-6" :class="generated.metadataFailed ? 'text-amber-800 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">已保存到：{{ generated.directory }}</p><p v-if="generated.metadataCount || generated.metadataFailed" class="mt-1 text-xs" :class="generated.metadataFailed ? 'text-amber-800 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">元数据成功 {{ generated.metadataCount ?? 0 }} 个，失败 {{ generated.metadataFailed ?? 0 }} 个<span v-if="generated.metadataFailed">；再次提交同一分享会继续重试</span></p><p v-if="generated.conflictRenamed" class="mt-1 text-xs" :class="generated.metadataFailed ? 'text-amber-800 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">{{ generated.conflictRenamed }} 个同名目标内容不同，已保留旧文件并另存。</p></section>
      <section v-if="activeTab === 'generate' && (generated?.reshareUrl || partialShareURL)" class="mt-4 rounded-xl border border-blue-100 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 p-5"><h2 class="text-sm font-semibold text-blue-900 dark:text-blue-200">自己的分享链接</h2><a :href="generated?.reshareUrl || partialShareURL" target="_blank" rel="noopener noreferrer" class="mt-2 block break-all text-sm text-blue-700 dark:text-blue-300 underline">{{ generated?.reshareUrl || partialShareURL }}</a><p v-if="generated?.cleaned" class="mt-2 text-xs leading-5 text-blue-700 dark:text-blue-300">新分享视频及元数据数量已确认，临时目录已移入 115 回收站。</p></section>
      <p v-if="error" role="alert" class="my-4 rounded-lg bg-red-50 dark:bg-red-950/30 p-4 text-sm text-red-700 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="my-4 rounded-lg bg-blue-50 dark:bg-blue-950/30 p-4 text-sm text-blue-700 dark:text-blue-300">{{ notice }}</p>
      <section v-if="activeTab === 'generate' && browsed" class="mt-8 border-t border-slate-100 dark:border-slate-800 pt-6">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-800 dark:text-slate-200">分享文件预览</h2><button v-if="folders[folders.length - 1].id !== startCID" class="text-xs text-blue-600 dark:text-blue-400" :disabled="busy" @click="startCID = folders[folders.length - 1].id">设为生成起始目录</button></div>
      <nav class="flex flex-wrap gap-3 text-sm text-blue-600 dark:text-blue-400"><button v-for="(folder, i) in folders" :key="folder.id" :disabled="busy" @click="browse(folders.slice(0, i + 1))">{{ folder.name }} /</button></nav>
      <p v-if="files.length === 0" class="py-6 text-sm text-slate-400 dark:text-slate-400">该目录暂无文件。</p>
      <div class="mt-4 divide-y divide-slate-200 border-y border-slate-200 dark:divide-slate-800 dark:border-slate-800">
        <div v-for="file in files" :key="file.id" class="flex flex-wrap items-center justify-between gap-4 py-4">
          <div class="min-w-0 flex-1"><p class="break-words font-medium">{{ file.directory ? '📁 ' : '' }}{{ file.name }}</p><p class="mt-1 text-sm text-slate-400 dark:text-slate-400">{{ file.directory ? '文件夹' : size(file.size) }}</p></div>
          <button v-if="file.directory" class="text-sm text-blue-600 dark:text-blue-400" :disabled="busy" @click="browse([...folders, { id: file.id, name: file.name }])">打开</button>
          <template v-else><button class="text-sm text-blue-600 dark:text-blue-400" :disabled="busy" @click="select(file, false)">下载 STRM</button><button class="rounded-lg border px-3 py-2 text-sm" :disabled="busy" @click="select(file, true)">播放</button></template>
        </div>
      </div>
      <div v-if="count > 100" class="mt-4 flex justify-between text-sm"><button :disabled="busy || offset === 0" @click="browse(folders, Math.max(0, offset - 100))">上一页</button><span>{{ offset + 1 }}–{{ Math.min(offset + 100, count) }} / {{ count }}</span><button :disabled="busy || offset + 100 >= count" @click="browse(folders, offset + 100)">下一页</button></div>
      <section v-if="playURL" class="mt-8"><h2 class="mb-3 font-medium">{{ playName }}</h2><video :src="playURL" controls autoplay preload="metadata" class="w-full rounded-xl bg-black" @error="error = '无法解码或读取视频。请检查连接；当前格式也可能不受浏览器支持，可改用 Emby 播放。'"></video></section>
      </section>
      <p v-if="activeTab === 'basic'" class="mt-6 text-xs leading-6 text-slate-500 dark:text-slate-400">Emby 须能访问 STRM 内的服务地址，播放需要有效的 115 登录；仅提供原文件播放。</p>

      </section>
      <template v-if="activeTab === 'basic'">
    <section class="mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-5 py-5 md:px-8"><div class="flex items-center justify-between gap-4"><h2 class="text-sm font-semibold text-slate-800 dark:text-slate-200">Telegram Bot</h2><span class="rounded-full px-3 py-1 text-xs" :class="botConfigured ? 'bg-emerald-50 dark:bg-emerald-950/30 text-emerald-700 dark:text-emerald-300' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400'">{{ botConfigured ? '已配置接收器' : '未启用' }}</span></div><p class="mt-3 text-xs leading-6 text-slate-500 dark:text-slate-400">{{ botConfigured ? '在允许的聊天中，一条消息可发送多个 115 分享链接，建议每行一个。Bot 按保存的设置逐条处理并回复结果，单条失败继续下一条。' : '在「Telegram 配置」中填写 Bot Token、允许的聊天 ID，并启用消息接收。' }}修改本页目录、后缀等选项后，点击「保存设置」使其对 Bot 生效。</p><button type="button" :disabled="busy" class="mt-3 text-xs font-medium text-blue-600 dark:text-blue-400 disabled:opacity-40" @click="$emit('openTelegram')">打开 Telegram 配置 →</button></section>

      </template>
      <P115DirectoryDialog v-if="parentDialogOpen" :token="token" :account-id="parentAccountId" :saving="parentSaving" :save-error="parentError" confirm-label="保存并使用" title="选择 115 转存父目录" description="保存后供后续转存任务使用，进行中的任务保持原目录。" :allow-root="true" @close="parentDialogOpen = false" @select="selectParentDirectory" />
      <STRMDirectoryDialog v-if="directoryDialogOpen" :token="token" @close="directoryDialogOpen = false" @select="addDirectory" />
  </ManagementLayout>
</template>
