<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconCopy, IconDownload, IconExternalLink, IconFile, IconRefresh, IconSearch, IconSettings } from '@tabler/icons-vue'
import FloatingDialog from './AnimeFloatingDialog.vue'
import { telegramUserRequest, TelegramSearchError, type TelegramChannelConfig, type TelegramChannelLink, type TelegramChannelResource, type TelegramChannelSearchResult } from '../types/telegramUser'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import { copyText } from '../utils/clipboard'
import telegramIcon from '../assets/telegram/telegram.svg'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const config = ref<TelegramChannelConfig>(), result = ref<TelegramChannelSearchResult>()
const phase = ref('config'), error = ref(''), errorCode = ref(''), query = ref(''), copied = ref('')
const lifecycle = new AbortController()
const shares = useShareSTRMRun(props.token, lifecycle.signal, { scope: `tg-channel/${props.kind}/${props.tmdbId}` })
const cloudPending = ref<Record<string, boolean>>({}), cloudStatus = ref<Record<string, { message: string; blocked: boolean; success: boolean; needsConfig?: boolean }>>({})
const retryAt = ref(0), clock = ref(Date.now())
let retryTimer: ReturnType<typeof setInterval> | undefined
const retrySeconds = computed(() => Math.max(0, Math.ceil((retryAt.value - clock.value) / 1000)))
const enabled = computed(() => !!config.value?.account.hasSession && !!config.value?.channels.length)
const filtered = computed(() => (result.value?.items || []).filter(item => `${item.title} ${item.text}`.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const canContinue = computed(() => !!result.value?.hasMore && errorCode.value !== 'channel_search_expired')
const request = <T,>(path: string, body?: object) => telegramUserRequest<T>(props.token, path, lifecycle.signal, body)
function cooldown(value?: string) {
  clearInterval(retryTimer)
  retryAt.value = value ? Date.parse(value) || 0 : 0; clock.value = Date.now()
  if (retryAt.value > clock.value) retryTimer = setInterval(() => { clock.value = Date.now(); if (!retrySeconds.value) clearInterval(retryTimer) }, 1000)
}
function showError(reason: unknown) {
  error.value = reason instanceof Error ? reason.message : '频道搜索失败'
  errorCode.value = reason instanceof TelegramSearchError ? reason.code : ''
  if (reason instanceof TelegramSearchError && reason.retryAt) cooldown(reason.retryAt)
}
async function initialize() {
  if (phase.value && phase.value !== 'config') return
  phase.value = 'config'; error.value = ''; errorCode.value = ''
  try { config.value = await request<TelegramChannelConfig>('channels') }
  catch (reason) { if (!lifecycle.signal.aborted) showError(reason) }
  finally { phase.value = '' }
  if (enabled.value && !lifecycle.signal.aborted) await search()
}
async function search(more = false, refresh = false) {
  if (phase.value || retrySeconds.value) return
  phase.value = more ? 'more' : 'search'; error.value = ''; errorCode.value = ''
  try {
    const value = await request<TelegramChannelSearchResult>('channels/search', { kind: props.kind, tmdbId: props.tmdbId, ...(more ? { cursor: result.value?.cursor } : { refresh }) })
    if (lifecycle.signal.aborted) return
    result.value = value; cooldown(value.retryAt)
    void shares.restore(value.items.flatMap(item => item.links.filter(link => link.type === '115').map(link => shareResource(item, link))))
  } catch (reason) { if (!lifecycle.signal.aborted) showError(reason) }
  finally { phase.value = '' }
}
function linkID(item: TelegramChannelResource, link: TelegramChannelLink) { return `${item.id}/${link.url}` }
function shareResource(item: TelegramChannelResource, link: TelegramChannelLink): ShareSTRMResource {
  const id = linkID(item, link)
  return { id, identity: `tg-channel/${id}`, load: async () => [{ url: link.url }] }
}
async function download(item: TelegramChannelResource, link: TelegramChannelLink) {
  const id = linkID(item, link)
  if (link.type !== 'ed2k' || cloudPending.value[id] || cloudStatus.value[id]?.blocked) return
  cloudPending.value[id] = true; delete cloudStatus.value[id]
  try {
    const settings = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (!settings.accountId || !settings.targetCID) throw Object.assign(new Error('请先在 115 云下载保存主账号和下载目录'), { needsConfig: true })
    const receipt = await submitCloudDownload(props.token, settings.accountId, link.url, settings.targetCID, lifecycle.signal)
    if (!lifecycle.signal.aborted) cloudStatus.value[id] = { message: cloudDownloadNotice(receipt, settings.targetPath), success: true, blocked: true }
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      const info = reason as { outcomeUncertain?: boolean; needsConfig?: boolean }
      cloudStatus.value[id] = { message: reason instanceof Error ? reason.message : '提交失败', success: false, blocked: !!info?.outcomeUncertain, needsConfig: !!info?.needsConfig }
    }
  } finally { delete cloudPending.value[id] }
}
async function copy(link: TelegramChannelLink) {
  try { await copyText(link.url); copied.value = link.url }
  catch { copied.value = ''; error.value = '复制失败，请再次尝试' }
}
function date(value: string) { const d = new Date(value); return Number.isFinite(d.valueOf()) ? d.toLocaleDateString('zh-CN') : '' }
onMounted(initialize)
onBeforeUnmount(() => { lifecycle.abort(); clearInterval(retryTimer) })
</script>

<template>
  <FloatingDialog title="TG 频道搜索" close-label="关闭 TG 频道搜索" size="config" class="tg-channel-search media-search-dialog" @close="emit('close')">
    <template #header>
      <div class="tg-heading"><img :src="telegramIcon" width="32" height="32" alt=""/><div><h3>{{ title }}</h3><p>已配置频道的聚合资源</p></div></div>
      <div class="tg-toolbar"><label class="tg-filter"><IconSearch :size="17" aria-hidden="true"/><input v-model="query" type="search" aria-label="筛选频道资源" placeholder="筛选已加载的资源" /></label><button class="tg-button" :disabled="!!phase || !enabled || retrySeconds > 0" @click="search(false, true)"><IconRefresh :size="17" aria-hidden="true"/><span>刷新搜索</span></button><RouterLink to="/telegram/channels" class="tg-button" aria-label="TG 频道配置" @click="emit('close')"><IconSettings :size="17" aria-hidden="true"/></RouterLink></div>
    </template>
    <p v-if="phase === 'config'" role="status" class="tg-notice">正在读取频道配置…</p>
    <div v-else-if="config && !enabled" class="tg-empty"><h3>先选择资源频道</h3><p>使用已登录的 Telegram 账号，选择需要聚合搜索的频道。</p><RouterLink to="/telegram/channels" class="tg-button tg-primary" @click="emit('close')">配置搜索频道</RouterLink></div>
    <p v-if="phase === 'search'" role="status" class="tg-notice">正在搜索频道资源…</p>
    <div v-if="error" role="alert" class="tg-notice tg-error">{{ error }} <button v-if="!phase && !retrySeconds" class="tg-text" @click="config && enabled ? search(errorCode !== 'channel_search_expired' && !!result?.cursor) : initialize()">{{ errorCode === 'channel_search_expired' ? '重新搜索' : '重试' }}</button></div>
    <p v-if="retrySeconds" role="status" class="tg-notice">Telegram 搜索频率受限，{{ retrySeconds }} 秒后可继续。已加载的资源仍可使用。</p>
    <div v-if="shares.busy.value" class="tg-notice"><span>{{ shares.activeRunID.value ? 'STRM 任务在后台执行，可继续选择其他资源。' : '正在准备 STRM 任务…' }}</span><button v-if="shares.activeRunID.value" :disabled="shares.stopping.value" class="tg-text" @click="shares.stop()">停止已提交批次</button></div>
    <template v-if="result">
      <details v-if="result.warnings.length" class="tg-warnings"><summary>部分频道暂不可用（{{ result.warnings.length }}）</summary><p v-for="warning in result.warnings" :key="warning.channel.id">{{ warning.channel.title }}：{{ warning.error }}</p><p>其他频道的结果已保留，可加载更多重试；无访问权限的频道请在配置页处理。</p></details>
      <div class="tg-results-meta" role="status"><span>{{ query.trim() ? `显示 ${filtered.length} / ${result.items.length} 条` : `已加载 ${result.items.length} 条资源` }}</span><span v-if="result.cached">已复用缓存</span></div>
      <p v-if="result.truncated" class="tg-notice">本次已展示 1000 条资源，可到来源频道查看其余消息。</p>
      <div v-if="!filtered.length && !phase" class="tg-empty"><p>{{ query.trim() ? '没有匹配的已加载资源' : result.partial ? '部分频道未完成查询，暂未取得可用资源' : result.hasMore ? '本批消息没有可用链接，可继续加载' : '未找到包含 115 分享或 ED2K 的资源' }}</p><button v-if="query" class="tg-text" @click="query = ''">清除筛选</button></div>
      <ul class="tg-results" aria-label="TG 频道资源列表">
        <li v-for="item in filtered" :key="item.id" class="tg-resource">
          <h4>{{ item.title }}</h4><div class="tg-resource-meta"><a :href="item.sourceUrl" target="_blank" rel="noopener noreferrer" :aria-label="`在 Telegram 查看 ${item.channel.title} 原帖`">{{ item.channel.title }}<IconExternalLink :size="12" aria-hidden="true"/></a><time :datetime="item.postedAt">{{ date(item.postedAt) }}</time></div>
          <div v-for="(link, index) in item.links" :key="link.url" class="tg-link-row">
            <div class="tg-link-heading"><span>{{ link.type === '115' ? '115 分享' : 'ED2K' }}{{ item.links.length > 1 ? ` ${index + 1}` : '' }}</span><button class="tg-copy" :aria-label="`复制${link.type === '115' ? '分享' : 'ED2K'}链接`" @click="copy(link)"><IconCopy :size="15" aria-hidden="true"/>{{ copied === link.url ? '已复制' : '复制' }}</button></div>
            <div class="tg-actions"><template v-if="link.type === '115'"><a :href="link.url" target="_blank" rel="noopener noreferrer" class="tg-button"><IconExternalLink :size="16" aria-hidden="true"/>打开分享</a><button class="tg-button tg-primary" :disabled="shares.pending.value[linkID(item, link)] || shares.statuses.value[linkID(item, link)]?.success" @click="shares.submit(shareResource(item, link))"><IconFile :size="16" aria-hidden="true"/>{{ shares.pending.value[linkID(item, link)] ? '已加入任务…' : shares.statuses.value[linkID(item, link)]?.success ? '已生成' : '分享 STRM' }}</button></template><button v-else class="tg-button tg-primary" :disabled="cloudPending[linkID(item, link)] || cloudStatus[linkID(item, link)]?.blocked" @click="download(item, link)"><IconDownload :size="16" aria-hidden="true"/>{{ cloudPending[linkID(item, link)] ? '提交中…' : cloudStatus[linkID(item, link)]?.success ? '已提交' : cloudStatus[linkID(item, link)]?.blocked ? '待核对' : '115 云下载' }}</button></div>
            <p v-if="shares.statuses.value[linkID(item, link)]" role="status" class="tg-task-status">{{ shares.statuses.value[linkID(item, link)]?.message }}</p>
            <p v-if="cloudStatus[linkID(item, link)]" role="status" class="tg-task-status">{{ cloudStatus[linkID(item, link)]?.message }} <RouterLink v-if="cloudStatus[linkID(item, link)]?.needsConfig" to="/115/cloud-download" @click="emit('close')">前往配置</RouterLink></p>
          </div>
          <details class="tg-message"><summary>消息内容</summary><p>{{ item.text }}</p></details>
        </li>
      </ul>
      <div class="tg-list-end"><button v-if="canContinue" class="tg-button" :disabled="!!phase || retrySeconds > 0" @click="search(true)">{{ phase === 'more' ? '正在加载…' : '加载更多资源' }}</button><span v-else-if="!phase && !error && !result.partial && !result.truncated">已加载全部资源</span></div>
    </template>
    <template #footer><div class="search-log-footer"><span>115 分享生成 STRM，ED2K 使用已保存的云下载目录。</span><RouterLink :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click="emit('close')">查看日志</RouterLink></div></template>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.tg-channel-search) { width: min(920px, calc(100vw - 32px)); height: min(760px, calc(100dvh - 48px)); color: var(--search-dialog-text); }
.tg-heading { display: flex; align-items: center; gap: 12px; margin: 4px 0 16px; }.tg-heading > div { min-width: 0; }.tg-heading h3 { font-size: 18px; font-weight: 600; overflow-wrap: anywhere; }.tg-heading p { margin-top: 3px; font-size: 12px; color: var(--search-dialog-muted); }
.tg-toolbar { display: flex; align-items: center; gap: 8px; }.tg-filter { display: flex; flex: 1; min-width: 0; align-items: center; gap: 8px; border-radius: 10px; padding: 0 10px; background: var(--search-dialog-card); color: var(--search-dialog-muted); }.tg-filter input { min-width: 0; width: 100%; padding: 9px 0; background: transparent; outline: none; font-size: 13px; color: var(--search-dialog-text); }
.tg-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; font-weight: 500; text-decoration: none; white-space: nowrap; }.tg-button.tg-primary { background: #2563eb; color: white; }.tg-button:disabled { opacity: .45; cursor: default; }.tg-button:focus-visible, .tg-copy:focus-visible, .tg-text:focus-visible, .tg-filter:focus-within, summary:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
.tg-notice { display: flex; flex-wrap: wrap; gap: 6px 12px; margin-bottom: 12px; padding: 12px; border-radius: 10px; background: var(--search-dialog-card); font-size: 12px; line-height: 1.7; }.tg-error { color: #b91c1c; background: rgb(239 68 68 / .08); }:global(.dark) .tg-error { color: #fca5a5; }
.tg-text { color: #2563eb; font-size: 12px; text-decoration: underline; }:global(.dark) .tg-text { color: #93c5fd; }.tg-results-meta { display: flex; justify-content: space-between; gap: 8px; margin: 10px 0 12px; font-size: 12px; color: var(--search-dialog-muted); }
.tg-results { display: grid; gap: 12px; }.tg-resource { min-width: 0; padding: 16px; border-radius: 14px; background: var(--search-dialog-card); }.tg-resource h4 { font-size: 14px; font-weight: 600; line-height: 1.7; overflow-wrap: anywhere; }.tg-resource-meta { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 5px; font-size: 11px; color: var(--search-dialog-muted); }.tg-resource-meta a { display: inline-flex; gap: 4px; align-items: center; overflow-wrap: anywhere; }.tg-resource-meta a:hover { text-decoration: underline; }
.tg-link-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 6px 14px; margin-top: 12px; }.tg-link-heading { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--search-dialog-muted); }.tg-copy { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; padding: 4px 6px; border-radius: 6px; font-size: 11px; }.tg-actions { display: flex; flex-wrap: wrap; gap: 8px; }.tg-task-status { flex-basis: 100%; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; color: var(--search-dialog-muted); }.tg-task-status a { text-decoration: underline; }
.tg-message { margin-top: 8px; color: var(--search-dialog-muted); font-size: 12px; }.tg-message summary { width: fit-content; padding: 6px 0; cursor: pointer; }.tg-message p { padding-top: 8px; line-height: 1.8; white-space: pre-wrap; overflow-wrap: anywhere; }
.tg-warnings { padding: 10px 12px; margin-bottom: 12px; background: var(--search-dialog-card); border-radius: 10px; font-size: 12px; color: var(--search-dialog-muted); }.tg-warnings summary { cursor: pointer; }.tg-warnings p { margin-top: 8px; overflow-wrap: anywhere; }
.tg-empty { padding: 30px 12px; text-align: center; font-size: 13px; line-height: 1.8; color: var(--search-dialog-muted); }.tg-empty h3 { font-weight: 600; }.tg-empty p { margin-bottom: 12px; }.tg-list-end { display: flex; justify-content: center; margin-top: 18px; font-size: 12px; color: var(--search-dialog-muted); }.search-log-footer { padding: 0; }
@media (hover: hover) { .tg-button:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }.tg-button.tg-primary:hover:not(:disabled) { background: #1d4ed8; }.tg-copy:hover { background: rgb(148 163 184 / .12); } }
@media (max-width: 639px) { :global(dialog.tg-channel-search) { width: calc(100vw - 16px); height: calc(100dvh - 24px); }.tg-heading h3 { font-size: 16px; }.tg-toolbar { flex-wrap: wrap; }.tg-filter { flex-basis: 100%; }.tg-filter input { font-size: 16px; }.tg-button { min-height: 44px; }.tg-resource { padding: 12px; }.tg-actions { flex: 1 0 100%; }.tg-actions > * { flex: 1; }.tg-copy { min-height: 44px; } }
@media (prefers-reduced-motion: no-preference) { .tg-button { transition: background-color .18s ease; } }
</style>
