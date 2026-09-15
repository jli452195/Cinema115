<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { embyRequest, type EmbyConfig, type EmbyStatus } from '../types/emby'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ home: []; openDashboard: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
interface Library { ItemId: string; Name: string; CollectionType: string }
const defaults = new Set(['电视剧', '电影', '动漫', '动漫电影', '豆瓣top250', '国漫', '华语电影', '纪录片', '美剧', '日韩剧', '泰剧', '综艺'])
const config = ref<EmbyConfig>()
const status = ref<EmbyStatus>()
const url = ref('http://192.168.0.3:8096')
const apiKey = ref('')
const selected = ref<string[]>([])
const libraries = ref<Library[]>([])
const testedURL = ref('')
const busy = ref('')
const loading = ref(true)
const error = ref('')
const pollingError = ref('')
const notice = ref('')
const lifecycle = new AbortController()
let timer: number | undefined
const webhookBase = ref(window.location.port === '5173' ? `${window.location.protocol}//${window.location.hostname}:3002` : window.location.origin)
const webhookURL = computed(() => config.value?.webhookPath ? `${webhookBase.value.trim().replace(/\/$/, '')}${config.value.webhookPath}` : '')
const webhookDirty = computed(() => webhookBase.value.trim().replace(/\/$/, '') !== (config.value?.webhookBaseUrl || ''))
const webhookLoopback = computed(() => {
  try { return ['localhost', '127.0.0.1', '[::1]', '0.0.0.0', '[::]'].includes(new URL(webhookBase.value).hostname) }
  catch { return false }
})
const syncing = computed(() => status.value?.status === 'queued' || status.value?.status === 'running')
const metadataTotal = computed(() => (status.value?.metadataPending || 0) + (status.value?.metadataFailed || 0))
const notificationPending = computed(() => (status.value?.pending || 0) - (status.value?.metadataPending || 0))
const notificationFailed = computed(() => (status.value?.failed || 0) - (status.value?.metadataFailed || 0))
const stateLabel = computed(() => ({ queued: '等待同步', running: '同步中', failed: '同步失败', idle: status.value?.syncedAt ? '已建立索引' : '等待首次同步' })[status.value?.status || 'idle'])
const dirty = computed(() => !!apiKey.value || url.value.replace(/\/$/, '') !== config.value?.url || [...selected.value].sort().join(',') !== [...(config.value?.libraryIds || [])].sort().join(','))
async function request(path: string, body?: object) { return embyRequest(props.token, path, lifecycle.signal, body) }
function time(value?: string) { return value ? new Date(value).toLocaleString() : '尚无记录' }
async function refresh() {
  try { const data = await request('status'); if (!lifecycle.signal.aborted) { status.value = data; pollingError.value = '' } }
  catch (e) { if (!lifecycle.signal.aborted) pollingError.value = e instanceof Error ? e.message : '状态读取失败' }
}
async function action(kind: 'test' | 'save' | 'save-webhook' | 'sync') {
  if (busy.value) return
  busy.value = kind; error.value = ''; notice.value = ''
  try {
    if (kind === 'test') {
      const result = await request('test', { url: url.value.trim(), apiKey: apiKey.value.trim() })
      libraries.value = result.libraries; testedURL.value = url.value.trim()
      const available = new Set(libraries.value.map(lib => lib.ItemId))
      selected.value = config.value?.configured ? selected.value.filter(id => available.has(id)) : libraries.value.filter(lib => defaults.has(lib.Name)).map(lib => lib.ItemId)
      notice.value = `连接成功：${result.server.ServerName} · ${result.server.Version}，请选择媒体库并保存。`
    } else if (kind === 'save') {
      config.value = await request('config', { url: url.value.trim(), apiKey: apiKey.value.trim(), libraryIds: selected.value, webhookBaseUrl: webhookBase.value.trim() })
      url.value = config.value!.url; selected.value = [...config.value!.libraryIds]; apiKey.value = ''
      webhookBase.value = config.value!.webhookBaseUrl || webhookBase.value
      notice.value = '配置已加密保存。首次使用或更换媒体库后，请执行全量同步。'
    } else if (kind === 'save-webhook') {
      config.value = await request('webhook-config', { baseUrl: webhookBase.value.trim() })
      webhookBase.value = config.value!.webhookBaseUrl
      notice.value = 'Webhook 地址已保存，刷新页面或重启后仍会保留。请将下方完整地址复制到 Emby。'
    } else {
      await request('sync', {}); notice.value = '同步已加入后台队列，离开此页面也会继续。'
    }
    await refresh()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = '' }
}
async function copy() {
  try { await navigator.clipboard.writeText(webhookURL.value); notice.value = 'Webhook 地址已复制。' }
  catch { notice.value = '当前浏览器不支持复制，请选中地址手动复制。' }
}
onMounted(async () => {
  try { const data: EmbyConfig = await request('config'); config.value = data; url.value = data.url; selected.value = [...data.libraryIds]; webhookBase.value = data.webhookBaseUrl || webhookBase.value }
  catch (e) { error.value = e instanceof Error ? e.message : '配置加载失败' }
  finally { loading.value = false }
  await refresh()
  const poll = async () => { if (lifecycle.signal.aborted) return; await refresh(); if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 4000) }
  if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 4000)
})
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer) })
</script>

<template>
  <ManagementLayout title="媒体库同步" description="连接 Emby 并同步指定媒体库，通过 Webhook 持续更新影视入库状态。" :parent="{ label: 'Emby', to: '/emby' }" @logout="emit('logout')">
    <template #actions><button :disabled="loading || !!busy || !config || !selected.length || (testedURL !== url.trim() && !config?.configured)" class="shrink-0 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="action('save')">{{ busy === 'save' ? '正在保存…' : '保存配置' }}</button></template>
    <div class="space-y-5">
        <p v-if="error || pollingError" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error || pollingError }}</p>
        <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-800">{{ notice }}</p>
        <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
          <h2 class="font-semibold text-slate-900">连接设置</h2>
          <fieldset :disabled="loading || !!busy || !config" class="mt-5 grid gap-5 disabled:opacity-50 sm:grid-cols-2">
            <label class="text-sm text-slate-700">服务器地址<input v-model="url" type="url" placeholder="http://192.168.0.3:8096" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /></label>
            <label class="text-sm text-slate-700">API Key<input v-model="apiKey" type="password" autocomplete="new-password" :placeholder="config?.configured ? '已保存，留空保留原密钥' : '在 Emby 控制台 → API 密钥中创建'" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 outline-none focus:border-blue-500" /></label>
            <div class="sm:col-span-2"><button class="rounded-lg border border-slate-200 px-4 py-2.5 text-sm text-slate-700 hover:bg-slate-50" @click="action('test')">{{ busy === 'test' ? '正在连接…' : '测试连接并读取媒体库' }}</button><span class="ml-3 text-xs text-slate-400">API Key 加密保存，不回显到页面。</span></div>
          </fieldset>
          <div class="mt-6 border-t border-slate-100 pt-6"><div class="flex items-center justify-between"><h3 class="text-sm font-semibold text-slate-800">同步范围</h3><span class="text-xs text-slate-400">已选 {{ selected.length }} 个媒体库</span></div><p class="mt-2 text-xs leading-6 text-slate-500">首次读取默认勾选约定的 12 个媒体库；仅按勾选的媒体库 ID 建立索引。</p>
            <div v-if="libraries.length" class="mt-4 grid gap-3 sm:grid-cols-3"><label v-for="library in libraries" :key="library.ItemId" class="flex cursor-pointer items-center gap-3 rounded-xl border p-3 text-sm" :class="selected.includes(library.ItemId) ? 'border-blue-200 bg-blue-50 text-blue-800' : 'border-slate-200 text-slate-600'"><input v-model="selected" type="checkbox" :value="library.ItemId" :disabled="!!busy" class="h-4 w-4 accent-blue-600" />{{ library.Name }}</label></div>
            <p v-else class="mt-4 rounded-xl bg-slate-50 p-4 text-sm text-slate-500">{{ config?.configured ? '已保留选中的媒体库。点击「测试连接并读取媒体库」可查看或更改。' : '填写服务器地址与 API Key 后，点击上方按钮读取媒体库。' }}</p>
          </div>
        </section>
        <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
          <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">媒体索引</h2><span class="rounded-full px-3 py-1 text-xs" :class="status?.connected ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ status?.connected ? '连接正常' : '尚未连接或连接异常' }}</span></div>
          <dl class="mt-5 grid grid-cols-2 gap-5 text-sm sm:grid-cols-4"><div><dt class="text-slate-400">状态</dt><dd class="mt-2 font-medium">{{ stateLabel }}</dd></div><div><dt class="text-slate-400">索引条目</dt><dd class="mt-2 font-medium">{{ status?.count?.toLocaleString() || 0 }}</dd></div><div><dt class="text-slate-400">待处理通知</dt><dd class="mt-2 font-medium">{{ notificationPending }}<span v-if="notificationFailed" class="ml-2 text-red-600">失败 {{ notificationFailed }}</span></dd></div><div><dt class="text-slate-400">最近全量完成</dt><dd class="mt-2 text-xs leading-5">{{ time(status?.syncedAt) }}</dd></div></dl>
          <p v-if="syncing" role="status" class="mt-5 rounded-xl bg-blue-50 p-4 text-sm text-blue-700">{{ status?.status === 'queued' ? '等待后台开始同步…' : `已读取 ${status?.scanned?.toLocaleString()} 条，正在分页同步…` }}</p>
          <p v-if="status?.lastError || status?.connectionError || notificationFailed" class="mt-4 text-sm text-red-600">{{ status?.lastError || status?.connectionError || '部分通知多次重试失败，请执行全量同步修复索引。' }}</p>
          <p class="mt-4 text-xs leading-6 text-slate-500">入库状态按最近一次完整索引及已处理通知判断；尚未同步或缺少 TMDB ID 的媒体可能暂时显示未入库。</p>
          <p v-if="metadataTotal" class="mt-4 rounded-xl bg-amber-50 p-4 text-xs leading-6 text-amber-800">{{ metadataTotal }} 个条目缺少 TMDB ID、季号或集号，已单独安排刮削补查。请在 Emby 中补全元数据；其他影视仍按当前索引显示入库结果。<span v-if="status?.metadataFailed">其中 {{ status.metadataFailed }} 个已达到重试上限，补全后可通过媒体更新通知或手动同步重新识别。</span></p>
          <p v-if="status?.eventError" class="mt-3 text-xs leading-6 text-amber-700">最近通知处理错误：{{ status.eventError }}</p>
          <div class="mt-6 flex flex-wrap items-center gap-4"><button :disabled="!config?.configured || syncing || !!busy || dirty" class="rounded-xl bg-slate-900 px-5 py-3 text-sm text-white disabled:opacity-40" @click="action('sync')">{{ syncing ? '同步进行中…' : '开始全量同步' }}</button><p class="text-xs leading-6 text-slate-500">{{ dirty ? '请先保存修改后的配置。' : '首次或手动同步，后续通过 Webhook 更新，不定时扫描整个媒体库。' }}</p></div>
        </section>
        <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
          <h2 class="font-semibold text-slate-900">Webhook 增量更新</h2><p class="mt-3 text-sm leading-7 text-slate-500">在 Emby 通知中添加 Webhook，选择「已添加新媒体」和「媒体已移除」，关闭按剧集分组。使用 POST 和 JSON 格式；如提供媒体更新事件，也一并启用。</p>
          <template v-if="webhookURL"><label class="mt-4 block text-xs text-slate-500">Emby 能访问的 Cinema115 地址<input v-model="webhookBase" type="url" :disabled="!!busy" placeholder="http://192.168.0.24:3002" class="mt-2 block w-full rounded-xl border border-slate-200 p-3 text-sm text-slate-700" /></label><div class="mt-3 flex gap-2"><input :value="webhookURL" readonly aria-label="Webhook 地址" class="min-w-0 flex-1 rounded-xl border border-slate-200 bg-slate-50 p-3 text-xs text-slate-700" @focus="($event.target as HTMLInputElement).select()" /><button class="rounded-xl border border-slate-200 px-4 text-sm" @click="copy">复制</button></div><p class="mt-2 text-xs text-slate-400">此地址包含独立密钥，请只填入 Emby。修改后可点击「保存地址」或右上角「保存配置」。</p><div class="mt-4 flex flex-wrap items-center gap-3"><button :disabled="!!busy || !webhookBase.trim() || !webhookDirty" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40" @click="action('save-webhook')">{{ busy === 'save-webhook' ? '正在保存…' : '保存地址' }}</button><span class="text-xs" :class="webhookDirty ? 'text-amber-700' : 'text-emerald-700'">{{ webhookDirty ? '地址尚未保存' : '地址已保存' }}</span></div><p v-if="webhookLoopback" class="mt-3 text-xs leading-6 text-amber-700">localhost 只能指向访问者自身。Emby 在另一台设备时，请填写这台电脑的局域网 IP 与端口，例如 http://192.168.0.24:3002。</p></template>
          <p v-else class="mt-4 text-sm text-slate-400">保存连接配置后生成专用 Webhook 地址。</p>
          <div class="mt-5 border-t border-slate-100 pt-4 text-xs leading-7 text-slate-500"><p>最近通知：{{ time(status?.webhookAt) }} · {{ status?.webhookEvent || '尚未收到' }}</p><p v-if="status?.webhookError" class="text-amber-700">{{ status.webhookError }}</p><p>测试通知只验证连通性；媒体变更收到后会延迟处理，等待 Emby 完成刮削。</p></div>
        </section>
    </div>
  </ManagementLayout>
</template>
