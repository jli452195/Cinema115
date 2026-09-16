<script setup lang="ts">
import ManagementLayout from '../components/ManagementLayout.vue'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ShareTaskLogs from '../components/ShareTaskLogs.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { cancelShareRun, generateShareSTRM } from '../utils/shareGeneration'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import TelegramBotSelect from '../components/TelegramBotSelect.vue'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig } from '../types/telegramBots'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'

const props = defineProps<{ token: string }>()
const activeTab = ref<'basic' | 'normal' | 'reshare' | 'logs'>('basic')
const accounts = ref<P115Account[]>([])
const accountId = ref('')
const bots = ref<TelegramBotConfig[]>([])
const botId = ref('')
const telegramEnabled = ref(false)
const tabs = [{ id: 'basic', label: '基础设置', icon: '⚙' }, { id: 'normal', label: '普通分享', icon: '↗' }, { id: 'reshare', label: '转存再分享', icon: '↻' }, { id: 'logs', label: '日志', icon: '▤' }] as const
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
const outputDirectories = ref(['strm'])
const directoryDialogOpen = ref(false)
const videoExtensions = ref('mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,m2ts,webm')
const metadataExtensions = ref('srt,ass,ssa')
const minSizeMB = ref(0)
const recursive = ref(true)
const startCID = ref('0')
const generated = ref<{ cached?: boolean; count: number; metadataCount?: number; metadataFailed?: number; conflictRenamed?: number; directory: string; reshareUrl?: string; cleaned?: boolean }>()
const reshareEnabled = ref(false)
const savedReshareEnabled = ref(false)
const parentCID = ref('0')
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
    method: body === undefined ? 'GET' : 'POST', signal: controller?.signal,
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok && data?.reshareUrl) partialShareURL.value = data.reshareUrl
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请退出后重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  return data
}

function settings() {
  return { baseUrl: baseURL.value.trim(), outputDir: outputDir.value, videoExtensions: videoExtensions.value, metadataExtensions: metadataExtensions.value, minSizeMB: minSizeMB.value, recursive: recursive.value }
}

function saveSettings() {
  try { localStorage.setItem(settingsKey, JSON.stringify({ ...settings(), outputDirectories: outputDirectories.value })) } catch { /* 浏览器禁用存储时仍可生成。 */ }
}

async function saveAutomation() {
  if (automationSaving.value || busy.value || !automationReady.value) return
  automationSaving.value = true; automationNotice.value = ''; error.value = ''
  try {
    if (!accounts.value.some(account => account.id === accountId.value && account.configured)) throw new Error('请先选择已登录的 115 账号配置')
    if (telegramEnabled.value && !bots.value.some(bot => bot.id === botId.value && bot.configured && bot.enabled && bot.notificationChatID)) throw new Error('请先选择已启用且配置了通知聊天的 Telegram Bot')
    if (!Number.isInteger(tgBatchWaitSeconds.value) || tgBatchWaitSeconds.value < 0 || tgBatchWaitSeconds.value > 300) throw new Error('批次收集等待应为 0～300 秒的整数')
    const data = await request('automation-config', { accountId: accountId.value, botId: botId.value, telegramEnabled: telegramEnabled.value, tgBatchWaitSeconds: tgBatchWaitSeconds.value, reshareEnabled: reshareEnabled.value, parentCID: parentCID.value.trim(), fixedSharePassword: fixedSharePassword.value.trim(), reviewRetryEnabled: reviewRetryEnabled.value, reviewRetryMinutes: reviewRetryMinutes.value, reviewRetryMax: reviewRetryMax.value, generation: settings() })
    accountId.value = data.settings.accountId ?? ''
    botId.value = data.settings.botId ?? ''
    telegramEnabled.value = data.settings.telegramEnabled ?? false
    botConfigured.value = data.botConfigured ?? false
    savedReshareEnabled.value = data.settings.reshareEnabled
    parentCID.value = data.settings.parentCID
    fixedSharePassword.value = data.settings.fixedSharePassword ?? ''
    tgBatchWaitSeconds.value = data.settings.tgBatchWaitSeconds ?? 5
    automationNotice.value = '生成设置已保存到服务器，播放地址与开关立即生效，Bot 使用本页保存的生成选项。'
    saveSettings()
  } catch (e) {
    reshareEnabled.value = savedReshareEnabled.value
    error.value = e instanceof Error ? e.message : String(e)
  } finally { automationSaving.value = false }
}

function resolvedDirectory(value: string) {
  return value ? `/strm/${value}` : '/strm'
}

function validDirectory(value: unknown): value is string {
  return typeof value === 'string' && !/[<>:"\\|?*\x00-\x1f]/.test(value) && value.split('/').every(part => part.trim() === part && part !== '' && part !== '.' && part !== '..')
}

function directoryName(value: string) {
  return value.split('/').filter(Boolean).at(-1) ?? value
}

function addDirectory(value: string) {
  if (!validDirectory(value)) return
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
    const options = settings()
    // 仅记住生成选项，不把分享链接、提取码或 Cookie 写入本地配置。
    saveSettings()
    const data = await generateWithProgress({ ...options, mode: activeTab.value === 'reshare' ? 'reshare' : 'direct', url: shareURL.value.trim(), password: password.value.trim(), cid: startCID.value.trim() || '0' }, activeController.signal)
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
    const data = await request('strm', { url: shareURL.value, password: password.value, fileId: file.id, name: file.name, baseUrl: baseURL.value.trim() })
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
        if (typeof saved.recursive === 'boolean') recursive.value = saved.recursive
      }
    } catch { /* 忽略损坏的本地选项，继续使用后端默认配置。 */ }
    // 已保存的服务器选项优先，保证换浏览器或刷新后配置一致。
    const saved = automation.settings.generation
    if (saved) {
      baseURL.value = saved.baseUrl || baseURL.value
      videoExtensions.value = saved.videoExtensions || config.videoExtensions
      metadataExtensions.value = saved.metadataExtensions ?? config.metadataExtensions ?? 'srt,ass,ssa'
      minSizeMB.value = saved.minSizeMB ?? 0
      recursive.value = saved.recursive ?? true
      if (validDirectory(saved.outputDir)) {
        outputDir.value = saved.outputDir
        if (!outputDirectories.value.includes(saved.outputDir)) outputDirectories.value.push(saved.outputDir)
      }
    }
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : String(e) }
  finally { initializing.value = false }
})
watch(outputDir, () => { if (!initializing.value) saveSettings() })
onBeforeUnmount(() => { mounted = false; controller?.abort() })
</script>

<template>
  <ManagementLayout title="115分享STRM" description="管理分享生成、转存再分享与审核重试，查看任务处理记录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions>
      <button type="button" class="shrink-0 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="busy || initializing || automationSaving || !automationReady" @click="saveAutomation">{{ automationSaving ? '保存中…' : '保存设置' }}</button>
    </template>
      <nav class="mb-5 flex overflow-x-auto rounded-xl border border-slate-200 bg-white px-2 shadow-sm" aria-label="115 分享 STRM 功能">
        <button v-for="tab in tabs" :key="tab.id" type="button" :aria-current="activeTab === tab.id ? 'page' : undefined" class="flex shrink-0 items-center gap-2 border-b-2 px-4 py-4 text-sm transition" :class="activeTab === tab.id ? 'border-blue-600 text-blue-600' : 'border-transparent text-slate-600 hover:bg-slate-50'" @click="activeTab = tab.id"><span aria-hidden="true">{{ tab.icon }}</span>{{ tab.label }}</button>
      </nav>
      <p v-if="automationNotice" role="status" class="mb-5 rounded-xl bg-emerald-50 px-4 py-3 text-sm text-emerald-700">{{ automationNotice }}</p>
      <ShareTaskLogs v-if="activeTab === 'logs'" :token="token" />
      <section v-else class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
        <form @submit.prevent="activeTab === 'basic' ? saveAutomation() : generate()">
          <fieldset :disabled="busy || initializing || automationSaving || !automationReady" class="min-w-0 space-y-6 disabled:opacity-70">
            <template v-if="activeTab === 'basic'">
              <div><h2 class="font-semibold text-slate-800">基础设置</h2><p class="mt-2 text-sm leading-6 text-slate-500">普通分享、转存再分享与 Telegram 共用以下生成选项，修改后点击右上角保存。</p></div>
          <div>
            <div class="grid gap-x-6 gap-y-5 md:grid-cols-2">
              <div>
                <P115AccountSelect id="share-account" v-model="accountId" :accounts="accounts" />
                <p class="mt-2 text-xs leading-5 text-slate-400">选择后点击右上角保存，分享生成、Telegram 与 STRM 播放共用此配置。<RouterLink to="/helper115" class="text-blue-600 hover:underline">管理账号</RouterLink></p>
              </div>
              <div>
                <TelegramBotSelect id="share-bot" v-model="botId" :bots="bots" :required="telegramEnabled" />
                <p class="mt-2 text-xs leading-5 text-slate-400">生成成功通知和消息接收固定使用此配置。<RouterLink to="/telegram/bot" class="text-blue-600 hover:underline">管理 Bot</RouterLink></p>
              </div>
              <label class="block text-sm font-medium text-slate-700">起始目录 ID<input v-model="startCID" inputmode="numeric" pattern="[0-9]*" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="0" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">默认 0 表示整个分享；预览中可选定子目录。</span></label>
              <label class="block text-sm font-medium text-slate-700">STRM 播放地址<input v-model="baseURL" required type="url" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="http://NAS_IP:8115" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">填写 Emby 能访问的 Cinema115 地址，点击「保存设置」生效。首次使用默认填入当前访问地址。</span></label>
              <label class="block text-sm font-medium text-slate-700">最小文件大小（MB）<input v-model.number="minSizeMB" type="number" required min="0" max="1073741824" step="1" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">设为 0 不限制；小于此大小的文件会跳过。</span></label>
              <label class="block text-sm font-medium text-slate-700">视频后缀<input v-model="videoExtensions" required class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">使用逗号分隔，只为匹配的文件生成 STRM。</span></label>
              <label class="block text-sm font-medium text-slate-700">元数据后缀<input v-model="metadataExtensions" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="srt,ass,ssa" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">使用逗号分隔，按原格式保存字幕等配套文件；留空则不下载，不受视频最小大小限制。</span></label>
            </div>
            <div class="mt-6 grid gap-6 border-t border-slate-100 pt-6 md:grid-cols-2">
              <SettingSwitch v-model="telegramEnabled" label="启用 Telegram Bot" description="启用后，由所选 Bot 接收分享链接，并把生成成功结果发送到该 Bot 的通知聊天。" />
              <SettingSwitch v-model="recursive" label="包含子目录" description="自动扫描所有子目录，并保留文件夹层级。" />
              <SettingSwitch v-model="reshareEnabled" label="默认使用转存再分享" description="开启后，未明确指定模式的分享 STRM 任务默认使用转存再分享；接口明确指定直接生成时跳过。网页手动生成按当前分享标签执行。" />
              <SettingSwitch v-model="reviewRetryEnabled" label="分享审核中自动重试" description="无法读取审核中的分享时，后台定时重试；进度可在日志查看。关闭并保存后停止待重试任务。" class="md:col-span-2">
                <div v-if="reviewRetryEnabled" class="ml-12 mt-4 grid max-w-md grid-cols-2 gap-4">
                  <label class="text-xs text-slate-500">重试间隔（分钟）<input v-model.number="reviewRetryMinutes" type="number" min="1" max="1440" step="1" required class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-800" /></label>
                  <label class="text-xs text-slate-500">最大重试次数<input v-model.number="reviewRetryMax" type="number" min="1" max="100" step="1" required class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-800" /></label>
                </div>
              </SettingSwitch>
            </div>
          </div>
          <div class="border-t border-slate-100 pt-6">
            <h2 class="mb-2 font-semibold text-slate-800">STRM 生成目录</h2>
            <p class="mb-4 text-xs leading-6 text-slate-500">从媒体库挂载目录的真实文件夹中选择。页面统一显示为 /strm，Docker 中对应 NAS 挂载路径，Windows 中对应 STRM_ROOT。</p>
            <div class="grid gap-3 md:grid-cols-2">
              <div v-for="directory in outputDirectories" :key="directory" class="flex items-start gap-3 rounded-xl border p-4" :class="outputDir === directory ? 'border-blue-400 bg-blue-50' : 'border-slate-200 bg-slate-50'">
                <label class="flex min-w-0 flex-1 cursor-pointer items-start gap-3"><input v-model="outputDir" type="radio" name="output-directory" :value="directory" :aria-label="'生成到 ' + resolvedDirectory(directory)" class="mt-3 accent-blue-600" /><span class="grid h-10 w-10 shrink-0 place-items-center rounded-lg border border-blue-100 bg-blue-50 text-amber-500"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 7.5h6l2 2h10v9H3z"/><path d="M3 7.5V5h6l2 2h8v2.5"/></svg></span><span class="min-w-0 self-center"><span class="block break-all text-sm font-semibold text-slate-800">{{ directoryName(directory) }}</span><span class="mt-1 block break-all text-xs leading-5 text-slate-500">{{ resolvedDirectory(directory) }}</span></span></label>
                <button type="button" :aria-label="'移除目录 ' + resolvedDirectory(directory)" class="text-xs text-slate-400 hover:text-red-600" @click="removeDirectory(directory)">移除</button>
              </div>
            </div>
            <p v-if="outputDirectories.length === 0" class="rounded-xl border border-dashed border-slate-200 px-4 py-6 text-center text-sm text-slate-400">尚未选择 STRM 生成目录</p>
            <button type="button" class="mt-4 rounded-lg bg-blue-50 px-4 py-3 text-sm text-blue-600 hover:bg-blue-100" @click="directoryDialogOpen = true">＋ 从真实文件夹添加目录</button>
            <p class="mt-3 break-all text-xs leading-6 text-slate-500">生成位置：{{ outputDir ? resolvedDirectory(outputDir) : '未选择' }}</p><p class="text-xs leading-6 text-slate-400">选择器只显示媒体库挂载范围内的真实文件夹；移除选项不会删除文件夹。保留分享文件夹结构；相同内容直接复用，同名但内容不同会保留旧文件并另存。</p>
          </div>

            </template>
            <template v-else>
              <div><h2 class="font-semibold text-slate-800">{{ activeTab === 'normal' ? '普通分享' : '转存再分享' }}</h2><p class="mt-2 text-sm leading-6 text-slate-500">{{ activeTab === 'normal' ? '使用原分享链接生成 STRM，无需转存到网盘。' : '先转存并创建自己的长期分享，再生成 STRM。' }}</p></div>
          <div class="grid gap-5 md:grid-cols-4">
            <label class="block text-sm font-medium text-slate-700 md:col-span-3">115 分享链接<input v-model="shareURL" required type="url" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="https://115cdn.com/s/…?password=…" /></label>
            <label class="block text-sm font-medium text-slate-700">提取码<input v-model="password" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="链接包含时可留空" /></label>
          </div>

              <section v-if="activeTab === 'reshare'" class="rounded-xl border border-blue-100 bg-blue-50/50 p-5">
              <p class="text-xs leading-6 text-blue-800">接收分享 → 转存到独立临时目录 → 确认媒体数量并创建长期分享 → 生成 STRM → 临时目录移入回收站</p>
              <p class="mt-2 text-xs leading-5 text-blue-700">新分享有效期：长期（无到期日期），设置成功后才会继续。</p>
              <label class="mt-4 block text-sm text-slate-700">批次收集等待（秒）<input v-model.number="tgBatchWaitSeconds" type="number" min="0" max="300" step="1" class="mt-2 block w-full rounded-lg border border-slate-200 bg-white p-3 md:max-w-sm" /></label>
              <p class="mt-2 text-xs leading-5 text-slate-500">Telegram 连续消息和订阅自动资源从首项起等待指定秒数，再进入公共后台队列。执行时按分享者 user ID 判断：本账号分享直接生成，其他账号的正常分享合并转存。默认 5 秒，0 表示不等待。</p>
              <label class="mt-4 block text-sm text-slate-700">固定分享密码（可选）<input v-model="fixedSharePassword" maxlength="4" pattern="[a-zA-Z0-9]{4}" autocomplete="off" spellcheck="false" class="mt-2 block w-full rounded-lg border border-slate-200 bg-white p-3 md:max-w-sm" placeholder="留空则每次随机生成" /></label>
              <p class="mt-2 text-xs leading-5 text-slate-500">仅用于转存后创建的新分享，填写 4 位英文字母或数字，留空则随机生成。开启转存模式后，将分享者 user ID 与基础设置所选账号的 Cookie UID 比较：相同则直接生成 STRM，沿用原分享有效期；不同则转存后创建自己的长期分享。无法确认分享者时停止本条，提取码不再用于判断归属。</p>
              <label class="mt-4 block text-sm text-slate-700">115 转存父目录 ID<input v-model="parentCID" inputmode="numeric" pattern="[0-9]+" class="mt-2 block w-full rounded-lg border border-slate-200 bg-white p-3 md:max-w-sm" placeholder="0" /></label>
              <p class="mt-2 text-xs leading-5 text-slate-500">0 表示网盘根目录，每次自动创建独立临时目录。快照未完成或生成失败时保留目录；不会清空回收站。</p>

              </section>
              <div class="rounded-lg bg-slate-50 p-4 text-xs leading-6 text-slate-500">生成位置：{{ outputDir ? resolvedDirectory(outputDir) : '未选择' }}<br/>{{ recursive ? '包含子目录' : '仅当前目录' }} · 审核自动重试{{ reviewRetryEnabled ? '已开启（保存后生效）' : '未开启' }}<button type="button" class="ml-3 text-blue-600" @click="activeTab = 'basic'">修改基础设置</button></div>
          <div class="flex flex-wrap items-center gap-3 border-t border-slate-100 pt-6"><button type="submit" class="flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"><svg v-if="generating" class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity=".3"/><path d="M12 3a9 9 0 0 1 9 9" stroke="currentColor" stroke-width="3"/></svg>{{ initializing ? '载入配置中…' : generating ? '正在生成…' : '一键生成 STRM' }}</button><button type="button" class="rounded-lg border border-slate-200 px-5 py-3 text-sm text-slate-600 hover:bg-slate-50" :disabled="!shareURL.trim()" @click="browse()">{{ busy && !generating ? '读取中…' : '预览分享文件' }}</button><p class="text-xs text-slate-400">使用基础设置中的输出目录与筛选条件。</p></div>

            </template>
          </fieldset>
        </form>
      <section v-if="generating" role="status" class="mt-5 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700"><div class="flex items-center justify-between gap-4"><p class="font-medium">{{ generationProgress }}</p><button v-if="activeRunID" type="button" :disabled="stoppingRun" class="shrink-0 rounded-lg border border-blue-200 px-3 py-1 text-xs hover:bg-white disabled:opacity-50" @click="stopGeneration">{{ stoppingRun ? '正在停止…' : '停止任务' }}</button></div><p class="mt-2 text-xs text-blue-600">已用时 {{ generationSeconds }} 秒 · 提交成功后，关闭页面不会取消后台任务。每条执行最多 5 分钟，结果可在分享日志及 Telegram 通知查看。</p></section>
      <section v-if="generated" role="status" class="mt-6 rounded-xl border p-5" :class="generated.metadataFailed ? 'border-amber-200 bg-amber-50' : 'border-emerald-100 bg-emerald-50'"><h2 class="font-semibold" :class="generated.metadataFailed ? 'text-amber-900' : 'text-emerald-800'">{{ generated.cached ? `本地文件完整 · 已复用 ${generated.count} 个 STRM` : generated.metadataFailed ? `STRM 已生成 ${generated.count} 个 · 元数据部分失败` : `生成完成 · 已处理 ${generated.count} 个 STRM` }}</h2><p class="mt-2 break-all text-xs leading-6" :class="generated.metadataFailed ? 'text-amber-800' : 'text-emerald-700'">已保存到：{{ generated.directory }}</p><p v-if="generated.metadataCount || generated.metadataFailed" class="mt-1 text-xs" :class="generated.metadataFailed ? 'text-amber-800' : 'text-emerald-700'">元数据成功 {{ generated.metadataCount ?? 0 }} 个，失败 {{ generated.metadataFailed ?? 0 }} 个<span v-if="generated.metadataFailed">；再次提交同一分享会继续重试</span></p><p v-if="generated.conflictRenamed" class="mt-1 text-xs" :class="generated.metadataFailed ? 'text-amber-800' : 'text-emerald-700'">{{ generated.conflictRenamed }} 个同名目标内容不同，已保留旧文件并另存。</p></section>
      <section v-if="generated?.reshareUrl || partialShareURL" class="mt-4 rounded-xl border border-blue-100 bg-blue-50 p-5"><h2 class="text-sm font-semibold text-blue-900">自己的分享链接</h2><a :href="generated?.reshareUrl || partialShareURL" target="_blank" rel="noopener noreferrer" class="mt-2 block break-all text-sm text-blue-700 underline">{{ generated?.reshareUrl || partialShareURL }}</a><p v-if="generated?.cleaned" class="mt-2 text-xs leading-5 text-blue-700">新分享视频及元数据数量已确认，临时目录已移入 115 回收站。</p></section>
      <p v-if="error" role="alert" class="my-4 rounded-lg bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="my-4 rounded-lg bg-blue-50 p-4 text-sm text-blue-700">{{ notice }}</p>
      <section v-if="browsed" class="mt-8 border-t border-slate-100 pt-6">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-800">分享文件预览</h2><button v-if="folders[folders.length - 1].id !== startCID" class="text-xs text-blue-600" :disabled="busy" @click="startCID = folders[folders.length - 1].id">设为生成起始目录</button></div>
      <nav class="flex flex-wrap gap-3 text-sm text-blue-600"><button v-for="(folder, i) in folders" :key="folder.id" :disabled="busy" @click="browse(folders.slice(0, i + 1))">{{ folder.name }} /</button></nav>
      <p v-if="files.length === 0" class="py-6 text-sm text-slate-400">该目录暂无文件。</p>
      <div class="mt-4 divide-y border-y">
        <div v-for="file in files" :key="file.id" class="flex flex-wrap items-center justify-between gap-4 py-4">
          <div class="min-w-0 flex-1"><p class="break-words font-medium">{{ file.directory ? '📁 ' : '' }}{{ file.name }}</p><p class="mt-1 text-sm text-slate-400">{{ file.directory ? '文件夹' : size(file.size) }}</p></div>
          <button v-if="file.directory" class="text-sm text-blue-600" :disabled="busy" @click="browse([...folders, { id: file.id, name: file.name }])">打开</button>
          <template v-else><button class="text-sm text-blue-600" :disabled="busy" @click="select(file, false)">下载 STRM</button><button class="rounded-lg border px-3 py-2 text-sm" :disabled="busy" @click="select(file, true)">播放</button></template>
        </div>
      </div>
      <div v-if="count > 100" class="mt-4 flex justify-between text-sm"><button :disabled="busy || offset === 0" @click="browse(folders, Math.max(0, offset - 100))">上一页</button><span>{{ offset + 1 }}–{{ Math.min(offset + 100, count) }} / {{ count }}</span><button :disabled="busy || offset + 100 >= count" @click="browse(folders, offset + 100)">下一页</button></div>
      <section v-if="playURL" class="mt-8"><h2 class="mb-3 font-medium">{{ playName }}</h2><video :src="playURL" controls autoplay preload="metadata" class="w-full rounded-xl bg-black" @error="error = '无法解码或读取视频。请检查连接；当前格式也可能不受浏览器支持，可改用 Emby 播放。'"></video></section>
      </section>
      <p class="mt-6 text-xs leading-6 text-slate-500">播放需要有效的 115 登录。Emby 必须能够访问 STRM 内的服务地址。浏览器直接播放取决于封装、视频和音频编码支持；此页面不执行转码。</p>

      </section>
      <template v-if="activeTab === 'basic'">
    <section class="mt-5 rounded-2xl border border-slate-200 bg-white px-5 py-5 md:px-8"><div class="flex items-center justify-between gap-4"><h2 class="text-sm font-semibold text-slate-800">Telegram Bot</h2><span class="rounded-full px-3 py-1 text-xs" :class="botConfigured ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ botConfigured ? '已配置接收器' : '未启用' }}</span></div><p class="mt-3 text-xs leading-6 text-slate-500">{{ botConfigured ? '在允许的聊天中，一条消息可发送多个 115 分享链接，建议每行一个。Bot 按保存的设置逐条处理并回复结果，单条失败继续下一条。' : '在「Telegram 配置」中填写 Bot Token、允许的聊天 ID，并启用消息接收。' }}修改本页目录、后缀等选项后，点击「保存设置」使其对 Bot 生效。</p><button type="button" :disabled="busy" class="mt-3 text-xs font-medium text-blue-600 disabled:opacity-40" @click="$emit('openTelegram')">打开 Telegram 配置 →</button></section>

      </template>
      <STRMDirectoryDialog v-if="directoryDialogOpen" :token="token" @close="directoryDialogOpen = false" @select="addDirectory" />
  </ManagementLayout>
</template>
