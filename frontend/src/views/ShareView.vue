<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ token: string }>()
defineEmits<{ back: []; openTelegram: [] }>()
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
const newDirectory = ref('')
const outputRoot = ref('/strm')
const videoExtensions = ref('mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,m2ts,webm')
const minSizeMB = ref(0)
const recursive = ref(true)
const startCID = ref('0')
const generated = ref<{ count: number; directory: string; reshareUrl?: string; cleaned?: boolean }>()
const reshareEnabled = ref(false)
const savedReshareEnabled = ref(false)
const parentCID = ref('0')
const automationSaving = ref(false)
const automationReady = ref(false)
const botConfigured = ref(false)
const automationNotice = ref('')
const partialShareURL = ref('')
const generationProgress = ref('')
const generationSeconds = ref(0)
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
  return { baseUrl: baseURL.value.trim(), outputDir: outputDir.value, videoExtensions: videoExtensions.value, minSizeMB: minSizeMB.value, recursive: recursive.value }
}

function saveSettings() {
  try { localStorage.setItem(settingsKey, JSON.stringify({ ...settings(), outputDirectories: outputDirectories.value })) } catch { /* 浏览器禁用存储时仍可生成。 */ }
}

async function saveAutomation() {
  if (automationSaving.value || busy.value || !automationReady.value) return
  automationSaving.value = true; automationNotice.value = ''; error.value = ''
  try {
    const data = await request('automation-config', { reshareEnabled: reshareEnabled.value, parentCID: parentCID.value.trim(), generation: settings() })
    savedReshareEnabled.value = data.settings.reshareEnabled
    parentCID.value = data.settings.parentCID
    automationNotice.value = '生成设置已保存到服务器，播放地址与开关立即生效，Bot 使用本页保存的生成选项。'
    saveSettings()
  } catch (e) {
    reshareEnabled.value = savedReshareEnabled.value
    error.value = e instanceof Error ? e.message : String(e)
  } finally { automationSaving.value = false }
}

function resolvedDirectory(value: string) {
  return `${outputRoot.value.replace(/\/$/, '')}/${value}`
}

function validDirectory(value: unknown): value is string {
  return typeof value === 'string' && !/[<>:"\\|?*\x00-\x1f]/.test(value) && value.split('/').every(part => part.trim() === part && part !== '' && part !== '.' && part !== '..')
}

function addDirectory() {
  const value = newDirectory.value.trim()
  if (!validDirectory(value)) {
    error.value = '请输入媒体库内的相对目录，例如 strm2 或 strm/电影，不要填写绝对路径或 ..'
    return
  }
  if (!outputDirectories.value.includes(value)) outputDirectories.value.push(value)
  outputDir.value = value; newDirectory.value = ''; error.value = ''
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
  generated.value = undefined; partialShareURL.value = ''
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
    const data = await generateWithProgress({ ...options, url: shareURL.value.trim(), password: password.value.trim(), cid: startCID.value.trim() || '0' }, activeController.signal)
    if (!mounted) return
    generated.value = data
  } catch (e) {
    if (mounted) error.value = activeController.signal.aborted
      ? (activeController.signal.reason === 'generation-timeout' ? '等待结果超时，已结束本次请求。请检查后端任务记录，避免重复转存。' : '已停止本次请求。已转存的目录可能保留，请按后端任务记录检查。')
      : e instanceof Error ? e.message : String(e)
  }
  finally { window.clearTimeout(timeout); window.clearInterval(elapsedTimer); busy.value = false; generating.value = false }
}

async function generateWithProgress(body: object, signal: AbortSignal) {
  const response = await fetch('/api/admin/p115/share/generate-stream', {
    method: 'POST', signal, headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  }
  if (!response.body || !response.headers.get('Content-Type')?.includes('application/x-ndjson')) throw new Error('服务器未返回处理进度，请更新后端并刷新页面')
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''
  let result: typeof generated.value
  const consume = (line: string) => {
    if (!line.trim()) return
    const event = JSON.parse(line)
    if (event.event === 'progress') generationProgress.value = event.data.message
    if (event.event === 'error') {
      if (event.data.reshareUrl) partialShareURL.value = event.data.reshareUrl
      throw new Error(event.data.error ?? '生成失败')
    }
    if (event.event === 'result') result = event.data
  }
  try {
    for (;;) {
      const { value, done } = await reader.read()
      // 流式解码保留跨网络分片的中文字符，不能逐块直接解析 JSON。
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
      let newline: number
      while ((newline = buffer.indexOf('\n')) >= 0) { consume(buffer.slice(0, newline)); buffer = buffer.slice(newline + 1) }
      if (buffer.length > 65536) throw new Error('服务器进度数据异常')
      if (done) { consume(buffer); break }
    }
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
  if (!result) throw new Error('连接已中断，未收到最终结果。请检查任务记录后再决定是否重试。')
  return result
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
    const [config, automation] = await Promise.all([request('generation-config'), request('automation-config')])
    if (!mounted) return
    reshareEnabled.value = automation.settings.reshareEnabled
    savedReshareEnabled.value = automation.settings.reshareEnabled
    parentCID.value = automation.settings.parentCID
    botConfigured.value = automation.botConfigured
    automationReady.value = true
    baseURL.value = config.baseUrl || window.location.origin; videoExtensions.value = config.videoExtensions
    outputRoot.value = config.outputRoot
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
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : String(e) }
  finally { initializing.value = false }
})
watch(outputDir, () => { if (!initializing.value) saveSettings() })
onBeforeUnmount(() => { mounted = false; controller?.abort() })
</script>

<template>
  <main class="min-h-screen bg-slate-100 px-4 py-6 md:p-10">
    <div class="mx-auto max-w-5xl">
    <div class="mb-6 flex items-center justify-between gap-4">
      <button class="text-sm text-slate-500 hover:text-blue-600 disabled:opacity-40" :disabled="busy" @click="$emit('back')">← 返回 115 网盘</button>
      <button type="button" class="shrink-0 rounded-xl bg-blue-600 px-4 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50" :disabled="busy || initializing || automationSaving || !automationReady" @click="saveAutomation">{{ automationSaving ? '保存中…' : '保存生成设置' }}</button>
    </div>
    <header class="mb-6"><p class="text-xs tracking-widest text-slate-400">CINEMA115 / 分享播放</p><h1 class="mt-2 text-2xl font-semibold text-slate-900">STRM 生成配置</h1><p class="mt-2 text-sm leading-6 text-slate-500">粘贴 115 分享链接，生成 Emby 播放入口；也可先转换为自己的分享。</p></header>
    <p v-if="automationNotice" role="status" class="mb-5 rounded-xl bg-emerald-50 px-4 py-3 text-sm leading-6 text-emerald-700">{{ automationNotice }}</p>
    <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-8">
      <form @submit.prevent="generate">
        <fieldset :disabled="busy || initializing || automationSaving || !automationReady" class="min-w-0 space-y-6 disabled:opacity-70">
          <div class="flex items-center gap-3"><span class="flex h-10 w-10 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M14 3H5v18h14V8l-5-5Z" stroke-linejoin="round"/><path d="M14 3v5h5M8 13h8M8 17h5"/></svg></span><h2 class="font-semibold text-slate-800">分享链接</h2><span class="ml-auto rounded-full bg-blue-50 px-3 py-1 text-xs text-blue-600">手动生成</span></div>
          <div class="grid gap-5 md:grid-cols-4">
            <label class="block text-sm font-medium text-slate-700 md:col-span-3">115 分享链接<input v-model="shareURL" required type="url" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="https://115cdn.com/s/…?password=…" /></label>
            <label class="block text-sm font-medium text-slate-700">提取码<input v-model="password" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="链接包含时可留空" /></label>
          </div>
          <section class="rounded-xl border p-5 transition" :class="reshareEnabled ? 'border-blue-200 bg-blue-50/50' : 'border-slate-200 bg-slate-50'">
            <label class="flex cursor-pointer items-center justify-between gap-4">
              <span><span class="block text-sm font-semibold text-slate-800">转存再分享</span><span class="mt-1 block text-xs leading-5 text-slate-500">{{ reshareEnabled ? '先转存，创建自己的分享，再清理本次临时目录。' : '当前直接使用收到的分享生成 STRM。' }}</span></span>
              <input v-model="reshareEnabled" type="checkbox" role="switch" aria-label="转存再分享" class="peer sr-only" @change="saveAutomation" />
              <span class="relative h-6 w-11 shrink-0 rounded-full bg-slate-300 transition after:absolute after:left-0.5 after:top-0.5 after:h-5 after:w-5 after:rounded-full after:bg-white after:shadow-sm after:transition peer-checked:bg-blue-600 peer-checked:after:translate-x-5 peer-focus-visible:ring-2 peer-focus-visible:ring-blue-400 peer-focus-visible:ring-offset-2"></span>
            </label>
            <div v-if="reshareEnabled" class="mt-5 border-t border-blue-100 pt-4">
              <p class="text-xs leading-6 text-blue-800">接收分享 → 转存到独立临时目录 → 创建长期分享并校验快照 → 生成 STRM → 临时目录移入回收站</p>
              <p class="mt-2 text-xs leading-5 text-blue-700">新分享有效期：长期（无到期日期），设置成功后才会继续。</p>
              <label class="mt-4 block text-sm text-slate-700">115 转存父目录 ID<input v-model="parentCID" inputmode="numeric" pattern="[0-9]+" class="mt-2 block w-full rounded-lg border border-slate-200 bg-white p-3 md:max-w-sm" placeholder="0" /></label>
              <p class="mt-2 text-xs leading-5 text-slate-500">0 表示网盘根目录，每次自动创建独立临时目录。快照未完成或生成失败时保留目录；不会清空回收站。</p>
            </div>
            <p class="mt-4 text-xs leading-5 text-slate-500">播放地址和 Bot 生成选项通过右上角「保存生成设置」保存；切换开关立即保存。</p>
          </section>
          <div class="border-t border-slate-100 pt-6">
            <h2 class="mb-5 font-semibold text-slate-800">生成选项</h2>
            <div class="grid gap-x-6 gap-y-5 md:grid-cols-2">
              <div><p class="text-sm font-medium text-slate-700">使用 115 配置</p><p class="mt-2 rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm text-slate-600">当前已保存的 115 登录凭证</p><p class="mt-2 text-xs leading-5 text-slate-400">在「115 网盘」页面扫码或配置 Cookie。</p></div>
              <label class="block text-sm font-medium text-slate-700">起始目录 ID<input v-model="startCID" inputmode="numeric" pattern="[0-9]*" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="0" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">默认 0 表示整个分享；预览中可选定子目录。</span></label>
              <label class="block text-sm font-medium text-slate-700">STRM 播放地址<input v-model="baseURL" required type="url" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" placeholder="http://NAS_IP:8115" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">填写 Emby 能访问的 Cinema115 地址，点击「保存生成设置」生效。首次使用默认填入当前访问地址。</span></label>
              <label class="block text-sm font-medium text-slate-700">最小文件大小（MB）<input v-model.number="minSizeMB" type="number" required min="0" max="1073741824" step="1" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">设为 0 不限制；小于此大小的文件会跳过。</span></label>
              <label class="block text-sm font-medium text-slate-700 md:col-span-2">视频后缀<input v-model="videoExtensions" required class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 font-normal outline-none focus:border-blue-500" /><span class="mt-2 block text-xs font-normal leading-5 text-slate-400">使用逗号分隔，只为匹配的文件生成 STRM。</span></label>
            </div>
            <label class="mt-6 flex cursor-pointer items-start gap-3"><input v-model="recursive" type="checkbox" class="peer sr-only" /><span class="relative mt-0.5 h-5 w-9 shrink-0 rounded-full bg-slate-300 transition after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:bg-white after:shadow-sm after:transition peer-checked:bg-blue-600 peer-checked:after:translate-x-4 peer-focus-visible:ring-2 peer-focus-visible:ring-blue-400 peer-focus-visible:ring-offset-2"></span><span><span class="text-sm font-medium text-slate-700">包含子目录</span><span class="mt-1 block text-xs leading-5 text-slate-400">自动扫描所有子目录，并保留文件夹层级。</span></span></label>
          </div>
          <div class="border-t border-slate-100 pt-6">
            <h2 class="mb-2 font-semibold text-slate-800">STRM 生成目录</h2>
            <p class="mb-4 text-xs leading-6 text-slate-500">填写媒体库内的相对目录，例如 strm、strm2。添加子目录无需修改 Docker 挂载。</p>
            <div class="grid gap-3 md:grid-cols-2">
              <div v-for="directory in outputDirectories" :key="directory" class="flex items-start gap-3 rounded-xl border p-4" :class="outputDir === directory ? 'border-blue-400 bg-blue-50' : 'border-slate-200 bg-slate-50'">
                <label class="flex min-w-0 flex-1 cursor-pointer items-start gap-3"><input v-model="outputDir" type="radio" name="output-directory" :value="directory" :aria-label="'生成到 ' + directory" class="mt-1 accent-blue-600" /><span class="min-w-0"><span class="block break-all text-sm font-medium text-slate-800">{{ directory }}</span><span class="mt-1 block break-all text-xs leading-5 text-slate-500">{{ resolvedDirectory(directory) }}</span></span></label>
                <button type="button" :disabled="outputDirectories.length === 1" :aria-label="'移除目录 ' + directory" class="text-xs text-slate-400 hover:text-red-600 disabled:opacity-30" @click="removeDirectory(directory)">移除</button>
              </div>
            </div>
            <div class="mt-4 flex flex-wrap gap-3"><input v-model="newDirectory" aria-label="新生成目录" class="min-w-0 flex-1 rounded-lg border border-slate-200 bg-white p-3 text-sm outline-none focus:border-blue-500" placeholder="strm2 或 strm/电影" maxlength="1024" @keydown.enter.prevent="addDirectory" /><button type="button" class="rounded-lg bg-blue-50 px-4 py-3 text-sm text-blue-600" :disabled="!newDirectory.trim()" @click="addDirectory">＋ 添加目录</button></div>
            <p class="mt-3 break-all text-xs leading-6 text-slate-500">生成位置：{{ resolvedDirectory(outputDir) }}（媒体库内的 {{ outputDir }} 目录）</p><p class="text-xs leading-6 text-slate-400">保留分享文件夹结构，每次重新写入，同名 STRM 更新为本次生成内容。</p>
          </div>
          <div class="flex flex-wrap items-center gap-3 border-t border-slate-100 pt-6"><button type="submit" class="flex items-center gap-2 rounded-lg bg-blue-600 px-5 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"><svg v-if="generating" class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity=".3"/><path d="M12 3a9 9 0 0 1 9 9" stroke="currentColor" stroke-width="3"/></svg>{{ initializing ? '载入配置中…' : generating ? '正在生成…' : '一键生成 STRM' }}</button><button type="button" class="rounded-lg border border-slate-200 px-5 py-3 text-sm text-slate-600 hover:bg-slate-50" :disabled="!shareURL.trim()" @click="browse()">{{ busy && !generating ? '读取中…' : '预览分享文件' }}</button><p class="text-xs text-slate-400">生成选项会保存在当前浏览器。</p></div>
        </fieldset>
      </form>
      <section v-if="generating" role="status" class="mt-5 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700"><div class="flex items-center justify-between gap-4"><p class="font-medium">{{ generationProgress }}</p><button type="button" class="shrink-0 rounded-lg border border-blue-200 px-3 py-1 text-xs hover:bg-white" @click="controller?.abort('user-cancelled')">停止等待</button></div><p class="mt-2 text-xs text-blue-600">已用时 {{ generationSeconds }} 秒 · 单阶段等待最多 90 秒，整个任务最多 5 分钟。请保持页面打开。</p></section>
      <section v-if="generated" role="status" class="mt-6 rounded-xl border border-emerald-100 bg-emerald-50 p-5"><h2 class="font-semibold text-emerald-800">生成完成 · 新增 {{ generated.count }} 个 STRM</h2><p class="mt-2 break-all text-xs leading-6 text-emerald-700">已保存到：{{ generated.directory }}</p></section>
      <section v-if="generated?.reshareUrl || partialShareURL" class="mt-4 rounded-xl border border-blue-100 bg-blue-50 p-5"><h2 class="text-sm font-semibold text-blue-900">自己的分享链接</h2><a :href="generated?.reshareUrl || partialShareURL" target="_blank" rel="noopener noreferrer" class="mt-2 block break-all text-sm text-blue-700 underline">{{ generated?.reshareUrl || partialShareURL }}</a><p v-if="generated?.cleaned" class="mt-2 text-xs leading-5 text-blue-700">临时目录已移入 115 回收站，新分享已复查通过。</p></section>
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
    <section class="mt-5 rounded-2xl border border-slate-200 bg-white px-5 py-5 md:px-8"><div class="flex items-center justify-between gap-4"><h2 class="text-sm font-semibold text-slate-800">Telegram Bot</h2><span class="rounded-full px-3 py-1 text-xs" :class="botConfigured ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ botConfigured ? '已配置接收器' : '未启用' }}</span></div><p class="mt-3 text-xs leading-6 text-slate-500">{{ botConfigured ? '在允许的聊天中，每条消息发送一个 115 分享链接。Bot 按服务端保存的开关和生成选项处理，并回复结果。' : '在「Telegram 配置」中填写 Bot Token、允许的聊天 ID，并启用消息接收。' }}修改本页目录、后缀等选项后，点击「保存生成设置」使其对 Bot 生效。</p><button type="button" :disabled="busy" class="mt-3 text-xs font-medium text-blue-600 disabled:opacity-40" @click="$emit('openTelegram')">打开 Telegram 配置 →</button></section>
    </div>
  </main>
</template>
