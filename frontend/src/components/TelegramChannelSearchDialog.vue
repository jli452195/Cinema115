<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconCopy, IconExternalLink, IconFile, IconInfoCircle, IconRefresh, IconSearch, IconSettings } from '@tabler/icons-vue'
import FloatingDialog from './FloatingDialog.vue'
import ShareTransferDirectory from './ShareTransferDirectory.vue'
import ShareTransferButton from './ShareTransferButton.vue'
import SegmentedControl from './SegmentedControl.vue'
import ED2KFileList from './ED2KFileList.vue'
import { useShareTransfer } from '../composables/useShareTransfer'
import { telegramUserRequest, TelegramSearchError, type TelegramChannelConfig, type TelegramChannelLink, type TelegramChannelResource, type TelegramChannelSearchResult } from '../types/telegramUser'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { useTelegramChannelSources } from '../composables/useTelegramChannelSources'
import { telegramFiles } from '../utils/telegramFiles'
import { copyText } from '../utils/clipboard'
import telegramIcon from '../assets/telegram/telegram.svg'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const config = ref<TelegramChannelConfig>(), result = ref<TelegramChannelSearchResult>()
const phase = ref('config'), error = ref(''), errorCode = ref(''), query = ref(''), copied = ref('')
const tab = ref<'ed2k' | '115'>('ed2k')
const tabChosen = ref(false)
const helpOpen = ref(false), helpID = useId()
let searchRevision = 0
const lifecycle = new AbortController()
const transfers = useShareTransfer(props.token, lifecycle.signal)
const shares = useShareSTRMRun(props.token, lifecycle.signal, { scope: `tg-channel/${props.kind}/${props.tmdbId}` })
const sourcePages = useTelegramChannelSources({
  token: props.token, signal: lifecycle.signal, kind: props.kind, tmdbId: props.tmdbId,
  resources: () => result.value?.items || [],
  enabled: () => tab.value === 'ed2k' && !!result.value && !phase.value && errorCode.value !== 'channel_search_expired',
  onExpired: showError,
})
const { candidates, pages, statuses: pageStatus, resolvedItems, progress: sourceProgress, paused: sourcesPaused } = sourcePages
const files = computed(() => telegramFiles([...(result.value?.items || []), ...resolvedItems.value]))
const shareItems = computed(() => (result.value?.items || []).map(item => ({ ...item, links: item.links.filter(link => link.type === '115') })).filter(item => item.links.length))
const shareCount = computed(() => shareItems.value.reduce((sum, item) => sum + item.links.length, 0))
const tabs = computed(() => [{ value: 'ed2k' as const, label: 'ED2K 文件', count: files.value.length }, { value: '115' as const, label: '115 分享', count: shareCount.value }])
const filteredShares = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  return shareItems.value.filter(item => !needle || [item.title, item.metadata?.episode, item.metadata?.specs?.join(' '), item.metadata?.size, ...item.links.flatMap(link => [link.metadata?.episode, link.metadata?.specs?.join(' '), link.metadata?.size])].join(' ').toLocaleLowerCase().includes(needle))
})
const retryAt = ref(0), clock = ref(Date.now())
let retryTimer: ReturnType<typeof setInterval> | undefined
const retrySeconds = computed(() => Math.max(0, Math.ceil((retryAt.value - clock.value) / 1000)))
const enabled = computed(() => !!config.value?.account.hasSession && !!config.value?.channels.length)
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
  const revision = more ? searchRevision : ++searchRevision
  if (!more) sourcePages.cancelPending()
  try {
    const value = await request<TelegramChannelSearchResult>('channels/search', { kind: props.kind, tmdbId: props.tmdbId, ...(more ? { cursor: result.value?.cursor } : { refresh }) })
    if (lifecycle.signal.aborted || revision !== searchRevision) return
    if (!more) sourcePages.reset()
    result.value = value; cooldown(value.retryAt)
    if (!tabChosen.value) tab.value = files.value.length || candidates.value.length || !shareCount.value ? 'ed2k' : '115'
    void shares.restore(value.items.flatMap(item => item.links.filter(link => link.type === '115').map(link => shareResource(item, link))))
  } catch (reason) { if (!lifecycle.signal.aborted) showError(reason) }
  finally { phase.value = '' }
}
function linkID(item: TelegramChannelResource, link: TelegramChannelLink) { return `${item.id}/${link.url}` }
function shareResource(item: TelegramChannelResource, link: TelegramChannelLink): ShareSTRMResource {
  const id = linkID(item, link), size = link.metadata?.size || item.metadata?.size
  const description = ['TG · ' + item.channel.title, item.title || props.title, size && !item.title.includes(size) ? size : ''].filter(Boolean).join(' · ')
  return { id, identity: `tg-channel/${id}`, description, load: async () => [{ url: link.url }] }
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
  <FloatingDialog title="TG 频道搜索" close-label="关闭 TG 频道搜索" size="config" mobile-presentation="sheet" class="tg-channel-search media-search-dialog" @close="emit('close')">
    <template #title><span class="tg-window-title"><img :src="telegramIcon" width="22" height="22" alt="" /><span>{{ title }}</span><small>TG 频道搜索</small></span></template>
    <template #toolbar-actions><button class="tg-help-toggle" type="button" aria-label="搜索说明" :aria-expanded="helpOpen" :aria-controls="helpID" @click="helpOpen = !helpOpen"><IconInfoCircle :size="19" aria-hidden="true" /></button></template>
    <template #header>
      <div v-if="helpOpen" :id="helpID" class="tg-help"><p>ED2K 按文件去重；点击文件信息可查看完整文件名与来源。</p><div><RouterLink :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click="emit('close')">查看日志<IconExternalLink :size="13" aria-hidden="true" /></RouterLink><RouterLink to="/resources/telegram" aria-label="Telegram 搜索配置" @click="emit('close')"><IconSettings :size="15" aria-hidden="true" />配置频道</RouterLink></div></div>
      <div class="tg-toolbar"><label class="tg-filter"><IconSearch :size="17" aria-hidden="true" /><input v-model="query" type="search" aria-label="筛选频道资源" :placeholder="tab === 'ed2k' ? '筛选文件名、季集或规格' : '筛选分享资源'" /></label><button class="tg-button tg-refresh" aria-label="刷新搜索" :disabled="!!phase || !enabled || retrySeconds > 0" @click="search(false, true)"><IconRefresh :size="16" aria-hidden="true" /><span>刷新搜索</span></button></div>
      <div v-if="result" class="tg-tabs"><SegmentedControl :model-value="tab" :options="tabs" label="资源类型" density="compact" appearance="underline" @update:model-value="tab = $event; tabChosen = true" /><span v-if="result.cached">频道搜索缓存</span></div>
    </template>
    <p v-if="phase === 'config'" role="status" class="tg-notice">正在读取频道配置…</p>
    <div v-else-if="config && !enabled" class="tg-empty"><h3>先选择资源频道</h3><p>使用已登录的 Telegram 账号，选择需要聚合搜索的频道。</p><RouterLink to="/resources/telegram" class="tg-button tg-primary" @click="emit('close')">配置搜索频道</RouterLink></div>
    <p v-if="phase === 'search'" role="status" class="tg-notice">正在搜索频道资源…</p>
    <div v-if="error" role="alert" class="tg-notice tg-error">{{ error }}<button v-if="!phase && !retrySeconds" class="tg-text" @click="config && enabled ? search(errorCode !== 'channel_search_expired' && !!result?.cursor) : initialize()">{{ errorCode === 'channel_search_expired' ? '重新搜索' : '重试' }}</button></div>
    <p v-if="retrySeconds" role="status" class="tg-notice">Telegram 搜索频率受限，{{ retrySeconds }} 秒后可继续。已加载的文件仍可使用。</p>
    <template v-if="result">
      <details v-if="result.warnings.length" class="tg-warnings"><summary>部分频道暂不可用（{{ result.warnings.length }}）</summary><p v-for="warning in result.warnings" :key="warning.channel.id">{{ warning.channel.title }}：{{ warning.error }}</p></details>
      <p v-if="result.truncated" class="tg-notice">本次频道消息已达到加载上限，可到来源频道查看更多。</p>
      <div v-show="tab === 'ed2k'">
        <div v-if="candidates.length || canContinue" class="tg-load-status">
          <span v-if="candidates.length" role="status" aria-live="polite">清单已载入 {{ sourceProgress.loaded }} / {{ sourceProgress.total }} 页<span v-if="sourceProgress.loading">，读取中 {{ sourceProgress.loading }} 页</span><span v-if="sourceProgress.failed">，失败 {{ sourceProgress.failed }} 页</span><span v-if="sourcesPaused && sourceProgress.pending">，已暂停</span></span>
          <span v-if="sourceProgress.references > sourceProgress.total" class="tg-merged-hint">{{ sourceProgress.references }} 个入口已合并为 {{ sourceProgress.total }} 页</span>
          <span v-if="canContinue" class="tg-more-hint">频道还有更多结果</span>
          <div class="tg-load-actions">
            <button v-if="sourceProgress.pending && !sourcesPaused" type="button" class="tg-text" @click="sourcePages.pause()">暂停后续读取</button>
            <button v-if="sourcesPaused && (sourceProgress.pending || sourceProgress.failed)" type="button" class="tg-text" :disabled="!!phase" @click="sourcePages.resume()">继续读取清单</button>
            <button v-if="sourceProgress.failed" type="button" class="tg-text" :disabled="!!phase || sourcesPaused || errorCode === 'channel_search_expired'" @click="sourcePages.retryFailed()">重试失败清单</button>
          </div>
        </div>
        <ED2KFileList :files="files" :query="query" :token="token" :signal="lifecycle.signal" @settings="emit('close')" />
        <details v-if="candidates.length" class="tg-candidates" :open="!files.length">
          <summary>清单来源与读取状态（{{ candidates.length }} 页）</summary>
          <p class="tg-candidate-hint">相同网址只读取一次，文件自动加入列表；不同网址分别读取，保留各版本与原帖来源。</p>
          <div v-for="candidate in candidates" :key="candidate.url" class="tg-candidate">
            <div><strong>{{ candidate.item.title }}</strong><span>{{ candidate.item.channel.title }}</span><span v-if="candidate.references.length > 1">合并 {{ candidate.references.length }} 个入口</span><a :href="candidate.url" target="_blank" rel="noopener noreferrer">查看清单<IconExternalLink :size="12" aria-hidden="true" /></a><a :href="candidate.item.sourceUrl" target="_blank" rel="noopener noreferrer">原帖</a></div>
            <span v-if="pages[candidate.url]">{{ pages[candidate.url]!.groups.length }} 个版本，{{ pages[candidate.url]!.groups.reduce((sum, group) => sum + group.links.length, 0) }} 个文件</span>
            <span v-else-if="pageStatus[candidate.url]?.busy">正在读取…</span>
            <button v-else-if="pageStatus[candidate.url]?.error" type="button" class="tg-button" :disabled="!!phase || sourcesPaused || errorCode === 'channel_search_expired'" @click="sourcePages.resolve(candidate)">重试读取</button>
            <span v-else>{{ sourcesPaused ? '已暂停，等待继续' : '等待读取' }}</span>
            <p v-if="pageStatus[candidate.url]?.error" role="status">{{ pageStatus[candidate.url]!.error }}</p>
          </div>
        </details>
      </div>
      <div v-show="tab === '115'">
        <div class="tg-share-tools"><span>显示 {{ filteredShares.reduce((sum, item) => sum + item.links.length, 0) }} / {{ shareCount }} 个分享</span><ShareTransferDirectory :transfer="transfers" @settings="emit('close')" /><button v-if="shares.activeRunID.value" class="tg-button" :disabled="shares.stopping.value" @click="shares.stop()">{{ shares.stopping.value ? '停止中…' : '停止分享批次' }}</button></div>
        <p v-if="!filteredShares.length" class="tg-empty">{{ query.trim() ? '没有匹配的分享' : '暂未找到 115 分享' }}</p>
        <ul class="tg-results" aria-label="115 分享列表">
          <li v-for="item in filteredShares" :key="item.id" class="tg-resource">
            <div class="tg-resource-title"><h4>{{ item.title }}</h4><span v-if="item.metadata?.episode" class="tg-episode">{{ item.metadata.episode }}</span></div>
            <p v-if="item.metadata?.specs?.length || item.metadata?.size" class="tg-specs"><span v-for="spec in item.metadata.specs" :key="spec">{{ spec }}</span><span>{{ item.metadata.size }}</span></p>
            <div v-for="link in item.links" :key="link.url" class="tg-share-row">
              <p v-if="link.metadata" class="tg-specs"><span>{{ link.metadata.episode }}</span><span v-for="spec in link.metadata.specs" :key="spec">{{ spec }}</span><span>{{ link.metadata.size }}</span></p>
              <div class="tg-link-heading"><button class="tg-copy" aria-label="复制分享链接" @click="copy(link)"><IconCopy :size="14" aria-hidden="true" />{{ copied === link.url ? '已复制' : '复制' }}</button><a :href="link.url" target="_blank" rel="noopener noreferrer" class="tg-copy">打开分享<IconExternalLink :size="12" aria-hidden="true" /></a></div>
              <div class="tg-actions"><ShareTransferButton :transfer="transfers" :resource="{ load: async () => [{ url: link.url }] }" /><button type="button" class="resource-action-button resource-primary-button" :disabled="shares.pending.value[linkID(item, link)] || shares.statuses.value[linkID(item, link)]?.success" :title="shares.statuses.value[linkID(item, link)]?.message" @click="shares.submit(shareResource(item, link))"><IconFile :size="15" aria-hidden="true" /><span>{{ shares.pending.value[linkID(item, link)] ? '处理中…' : shares.statuses.value[linkID(item, link)]?.success ? '已生成' : '分享 STRM' }}</span></button></div>
              <p v-if="shares.statuses.value[linkID(item, link)]?.message" class="tg-task-status" role="status">{{ shares.statuses.value[linkID(item, link)]?.message }}</p>
            </div>
            <div class="tg-resource-meta"><a :href="item.sourceUrl" target="_blank" rel="noopener noreferrer">{{ item.channel.title }}<IconExternalLink :size="12" aria-hidden="true" /></a><time :datetime="item.postedAt">{{ date(item.postedAt) }}</time></div>
          </li>
        </ul>
      </div>
      <div class="tg-list-end"><button v-if="canContinue" class="tg-button" :disabled="!!phase || retrySeconds > 0" @click="search(true)">{{ phase === 'more' ? '正在加载…' : '加载更多资源' }}</button><span v-else-if="!phase && !error && !result.partial && !result.truncated">{{ tab === 'ed2k' && sourceProgress.loaded < sourceProgress.total ? '频道结果已加载，清单尚未全部载入' : '已加载全部频道结果' }}</span></div>
    </template>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.floating-dialog.tg-channel-search .floating-dialog-header) { padding-bottom: 0; }
.tg-tabs { border-bottom: 1px solid var(--search-dialog-card-border); }
.tg-tabs > :deep(.segmented-control) { margin-bottom: -1px; }
.tg-tabs > span { align-self: center; white-space: nowrap; }
:global(dialog.floating-dialog.tg-channel-search) { color: var(--search-dialog-text); }
@media (min-width: 640px) { :global(dialog.floating-dialog.tg-channel-search) { width: min(1100px, calc(100vw - 32px)); max-width: calc(100vw - 32px); height: min(860px, calc(100dvh - 48px)); max-height: calc(100dvh - 48px); } }
.tg-window-title { display: flex; align-items: center; gap: 8px; min-width: 0; }.tg-window-title img { flex: none; }.tg-window-title > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; }.tg-window-title small { flex: none; font-size: var(--search-dialog-font-meta, 11px); font-weight: 400; color: var(--search-dialog-muted); }
.tg-help-toggle { display: grid; flex: none; place-items: center; width: 36px; height: 36px; border-radius: 10px; color: var(--search-dialog-muted); }.tg-help-toggle[aria-expanded='true'] { color: var(--search-dialog-primary); background: var(--search-dialog-action-background); }
.tg-help-toggle:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
.tg-help { margin-bottom: 8px; padding: 8px 10px; border-radius: 8px; background: var(--search-dialog-card); font-size: 12px; line-height: 1.7; color: var(--search-dialog-muted); }.tg-help > div { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; }.tg-help a { display: inline-flex; align-items: center; gap: 5px; min-height: 36px; color: var(--search-dialog-primary); }
.tg-toolbar { display: flex; align-items: center; gap: 8px; }.tg-filter { display: flex; flex: 1; min-width: 0; align-items: center; gap: 8px; border-radius: 9px; padding: 0 10px; background: var(--search-dialog-card); color: var(--search-dialog-muted); }.tg-filter input { min-width: 0; width: 100%; padding: 9px 0; background: transparent; outline: none; font-size: 13px; color: var(--search-dialog-text); }
.tg-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 10px; border-radius: 8px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; text-decoration: none; white-space: nowrap; }.tg-button.tg-primary { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }.tg-button:disabled { opacity: .45; cursor: default; }
.tg-tabs { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 8px; }.tg-tabs > span { font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }.tg-notice { display: flex; flex-wrap: wrap; gap: 6px 12px; margin-bottom: 10px; padding: 10px; border-radius: 9px; background: var(--search-dialog-card); font-size: 12px; line-height: 1.7; }.tg-error { color: #b91c1c; background: rgb(239 68 68 / .08); }.dark .tg-error { color: #fca5a5; }.tg-text { color: var(--search-dialog-primary); text-decoration: underline; }
.tg-warnings { padding: 8px 10px; margin-bottom: 8px; background: var(--search-dialog-card); border-radius: 8px; font-size: 12px; color: var(--search-dialog-muted); }.tg-warnings summary { cursor: pointer; }.tg-warnings p { margin-top: 6px; overflow-wrap: anywhere; }
.tg-load-status { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; min-width: 0; font-size: var(--search-dialog-font-meta, 11px); line-height: 1.6; color: var(--search-dialog-muted); }.tg-load-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-left: auto; }.tg-load-actions button { min-height: 30px; }.tg-load-actions button:disabled { opacity: .45; cursor: default; }.tg-more-hint { margin-left: auto; }
.tg-candidates { margin-top: 12px; padding: 10px 12px; border: 1px solid var(--search-dialog-card-border); border-radius: 10px; font-size: 12px; }.tg-candidates > summary { cursor: pointer; color: var(--search-dialog-primary); }.tg-candidate-hint { margin-block: 8px; color: var(--search-dialog-muted); }.tg-candidate { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; padding-block: 10px; border-top: 1px solid var(--search-dialog-card-border); }.tg-candidate > div { display: flex; flex-wrap: wrap; align-items: center; gap: 5px 12px; min-width: 0; }.tg-candidate strong { max-width: 100%; overflow-wrap: anywhere; font-weight: 500; }.tg-candidate span { color: var(--search-dialog-muted); }.tg-candidate a { display: inline-flex; align-items: center; gap: 4px; color: var(--search-dialog-primary); }.tg-candidate > p { width: 100%; color: #b45309; overflow-wrap: anywhere; }.dark .tg-candidate > p { color: #fcd34d; }
.tg-share-tools { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin: 4px 0 10px; font-size: 12px; color: var(--search-dialog-muted); }.tg-results { display: grid; gap: 8px; }.tg-resource { min-width: 0; padding: 12px; border: 1px solid var(--search-dialog-card-border); border-radius: 10px; background: var(--search-dialog-card); }.tg-resource h4 { font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }.tg-resource-title { display: flex; flex-wrap: wrap; align-items: baseline; gap: 6px 12px; }.tg-resource-title h4 { min-width: 0; }.tg-episode { font-variant-numeric: tabular-nums; color: var(--search-dialog-primary); font-size: 12px; }.tg-specs { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; font-size: 12px; margin-top: 5px; }.tg-resource-meta { display: flex; flex-wrap: wrap; gap: 8px 14px; margin-top: 6px; font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }.tg-resource-meta a { display: inline-flex; align-items: center; gap: 4px; }.tg-share-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 6px; margin-top: 8px; }.tg-share-row > .tg-specs { width: 100%; }.tg-link-heading, .tg-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }.tg-copy { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; padding: 4px 6px; border-radius: 6px; font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }.tg-task-status { width: 100%; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; color: var(--search-dialog-muted); }
.tg-empty { padding: 24px 12px; text-align: center; font-size: 12px; line-height: 1.8; color: var(--search-dialog-muted); }.tg-empty p { margin-bottom: 10px; }.tg-list-end { display: flex; justify-content: center; margin-top: 14px; font-size: 12px; color: var(--search-dialog-muted); }.search-log-footer { padding: 0; }
.tg-button:focus-visible, .tg-copy:focus-visible, .tg-text:focus-visible, .tg-filter:focus-within, summary:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (hover: hover) { .tg-button:hover:not(:disabled), .tg-copy:hover { background: var(--search-dialog-secondary-hover); } }
@media (max-width: 639px) { .tg-window-title { font-size: var(--sheet-font-title); }.tg-window-title small { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }.tg-help-toggle { width: 44px; height: 44px; }.tg-help a { min-height: 44px; }.tg-filter input { font-size: 16px; }.tg-button { min-height: var(--sheet-touch-size); font-size: var(--sheet-font-body); }.tg-resource h4 { font-size: var(--sheet-font-body); line-height: var(--sheet-line-body); }.tg-toolbar > .tg-button { flex: none; min-width: 44px; padding-inline: 10px; }.tg-refresh span { display: none; }.tg-tabs { margin-top: 6px; }.tg-actions { flex: 1 0 100%; }.tg-actions > * { flex: 1; }.tg-copy { min-height: 44px; } }
@media (max-width: 639px) { .tg-tabs > span, .tg-merged-hint { display: none; }.tg-load-actions button { min-height: 44px; }.tg-more-hint { margin-left: 0; } }
</style>
