<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { IconCopy, IconDownload, IconExternalLink, IconFile, IconInfoCircle, IconRefresh, IconSearch, IconSettings } from '@tabler/icons-vue'
import FloatingDialog from './FloatingDialog.vue'
import ShareTransferDirectory from './ShareTransferDirectory.vue'
import ShareTransferButton from './ShareTransferButton.vue'
import SegmentedControl from './SegmentedControl.vue'
import ED2KFileList from './ED2KFileList.vue'
import { useShareTransfer } from '../composables/useShareTransfer'
import { telegramUserRequest, TelegramSearchError, type TelegramChannelConfig, type TelegramChannelLink, type TelegramChannelSearchResult, type TelegramChannelSourceResult } from '../types/telegramUser'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { useTelegramChannelSources } from '../composables/useTelegramChannelSources'
import { telegramFiles } from '../utils/telegramFiles'
import { telegramShareCards, telegramShareIdentities, type TelegramShareCard, type TelegramShareLink } from '../utils/telegramShares'
import { copyText } from '../utils/clipboard'
import telegramIcon from '../assets/telegram/telegram.svg'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const config = ref<TelegramChannelConfig>(), result = ref<TelegramChannelSearchResult>()
const phase = ref('config'), error = ref(''), errorCode = ref(''), query = ref(''), copied = ref('')
const tab = ref<'ed2k' | '115'>('ed2k')
const tabChosen = ref(false)
const ed2kList = ref<InstanceType<typeof ED2KFileList>>()
const ed2kBatch = computed(() => tab.value === 'ed2k' ? ed2kList.value?.batchBar : undefined)
const helpOpen = ref(false), helpID = useId()
let searchRevision = 0
const lifecycle = new AbortController()
const transfers = useShareTransfer(props.token, lifecycle.signal)
const shares = useShareSTRMRun(props.token, lifecycle.signal, { scope: `tg-channel/${props.kind}/${props.tmdbId}` })
const sourcePages = useTelegramChannelSources({
  token: props.token, signal: lifecycle.signal, kind: props.kind, tmdbId: props.tmdbId,
  resources: () => result.value?.items || [],
  enabled: () => !!result.value && !phase.value && errorCode.value !== 'channel_search_expired',
  onExpired: showError,
})
const { candidates, pages, statuses: pageStatus, resolvedItems, progress: sourceProgress, paused: sourcesPaused } = sourcePages
const files = computed(() => telegramFiles([...(result.value?.items || []), ...resolvedItems.value]))
const shareItems = computed(() => telegramShareCards(result.value?.items || [], candidates.value, pages.value))
const shareCount = computed(() => shareItems.value.reduce((sum, item) => sum + item.links.length, 0))
const tabs = computed(() => [{ value: 'ed2k' as const, label: 'ED2K 文件', count: files.value.length }, { value: '115' as const, label: '115 分享', count: shareCount.value }])
const filteredShares = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  if (!needle) return shareItems.value
  return shareItems.value.filter(item => [item.title, item.pageTitle, item.metadata?.episode, item.metadata?.specs?.join(' '), item.metadata?.size, ...item.links.flatMap(link => [...link.versions, link.metadata?.episode, link.metadata?.specs?.join(' '), link.metadata?.size])].join(' ').toLocaleLowerCase().includes(needle))
})
const emptyShareMessage = computed(() => {
  const { pending, loading, failed } = sourceProgress.value
  if (pending || loading) return sourcesPaused.value && !loading ? '清单读取已暂停，继续读取可搜索更多分享。' : '正在读取资源清单，分享将逐步显示。'
  if (failed) return '已读取的资源中没有匹配的分享，可重试失败清单。'
  return query.value.trim() ? '没有匹配的分享' : '暂未找到 115 分享'
})
watch([() => files.value.length, shareCount, () => !sourceProgress.value.pending && !sourceProgress.value.loading], ([fileCount, count, sourcesSettled]) => {
  if (tabChosen.value) return
  if (tab.value === 'ed2k' && !fileCount && count && sourcesSettled) tab.value = '115'
  else if (tab.value === '115' && !count && fileCount) tab.value = 'ed2k'
})
const restoredShares = new Set<string>()
let shareRestoration = Promise.resolve()
watch(shareItems, items => {
  const resources: ShareSTRMResource[] = []
  for (const item of items) for (const link of item.links) {
    if (shares.pending.value[link.id] || shares.statuses.value[link.id]?.success) continue
    for (const identity of telegramShareIdentities(link, props.kind, props.tmdbId)) {
      if (restoredShares.has(identity)) continue
      restoredShares.add(identity)
      resources.push(shareResource(item, link, identity))
    }
  }
  if (!resources.length) return
  shareRestoration = shareRestoration.then(async () => {
    for (const resource of resources) {
      if (shares.pending.value[resource.id] || shares.statuses.value[resource.id]?.success) continue
      await shares.restore([resource])
    }
  }).catch(() => {})
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
  } catch (reason) { if (!lifecycle.signal.aborted) showError(reason) }
  finally { phase.value = '' }
}
function shareResource(item: TelegramShareCard, link: TelegramShareLink, identity = telegramShareIdentities(link, props.kind, props.tmdbId)[0]!): ShareSTRMResource {
  const size = link.metadata?.size || item.metadata?.size
  const description = ['TG · ' + item.channelTitle, item.title || props.title, size && !item.title.includes(size) ? size : ''].filter(Boolean).join(' · ')
  return { id: link.id, identity, description, load: async () => [{ url: link.url }] }
}
async function submitShare(item: TelegramShareCard, link: TelegramShareLink) {
  // 先尝试恢复该行已有的任务，避免在候补身份仍待查找时再次提交。
  await shareRestoration
  await shares.submit(shareResource(item, link))
}
function sourceSummary(page: TelegramChannelSourceResult) {
  const files = page.groups.reduce((sum, group) => sum + group.links.length, 0)
  const shares = page.shareGroups?.reduce((sum, group) => sum + group.links.length, 0) || 0
  return [files && `${page.groups.length} 个版本，${files} 个文件`, shares && `${shares} 个分享`].filter(Boolean).join(' · ')
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
      <div v-if="helpOpen" :id="helpID" class="tg-help"><p>ED2K 按文件去重。点击文件行勾选，点击箭头查看完整文件名与来源。</p><div><RouterLink :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click="emit('close')">查看日志<IconExternalLink :size="13" aria-hidden="true" /></RouterLink><RouterLink to="/resources/telegram" aria-label="Telegram 搜索配置" @click="emit('close')"><IconSettings :size="15" aria-hidden="true" />配置频道</RouterLink></div></div>
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
      <ED2KFileList ref="ed2kList" v-show="tab === 'ed2k'" :files="files" :query="query" :token="token" :signal="lifecycle.signal" @settings="emit('close')" />
      <div v-show="tab === '115'">
        <div class="tg-share-tools"><span>显示 {{ filteredShares.reduce((sum, item) => sum + item.links.length, 0) }} / {{ shareCount }} 个分享</span><ShareTransferDirectory :transfer="transfers" @settings="emit('close')" /><button v-if="shares.activeRunID.value" class="tg-button" :disabled="shares.stopping.value" @click="shares.stop()">{{ shares.stopping.value ? '停止中…' : '停止分享批次' }}</button></div>
        <p v-if="!filteredShares.length" class="tg-empty">{{ emptyShareMessage }}</p>
        <ul class="tg-results" aria-label="115 分享列表">
          <li v-for="item in filteredShares" :key="item.id" class="tg-resource">
            <div class="tg-resource-title"><h4>{{ item.title }}</h4><span v-if="item.metadata?.episode" class="tg-episode">{{ item.metadata.episode }}</span></div>
            <div v-if="item.metadata?.specs?.length || item.metadata?.size || date(item.postedAt)" class="tg-resource-summary">
              <span v-for="spec in item.metadata?.specs" :key="spec">{{ spec }}</span>
              <span v-if="item.metadata?.size" class="tg-spec-size">{{ item.metadata.size }}</span>
              <time v-if="date(item.postedAt)" :datetime="item.postedAt" class="tg-post-date">{{ date(item.postedAt) }}</time>
            </div>
            <div v-for="link in item.links" :key="link.id" class="tg-share-row" :class="{ 'is-multi': item.links.length > 1 }">
              <div v-if="link.metadata || link.versions.some(title => title !== item.title) || link.sources.length" class="tg-share-info">
                <p v-if="link.metadata" class="tg-specs"><span v-if="link.metadata.episode" class="tg-episode">{{ link.metadata.episode }}</span><span v-for="spec in link.metadata.specs" :key="spec">{{ spec }}</span><span v-if="link.metadata.size" class="tg-spec-size">{{ link.metadata.size }}</span></p>
                <div v-if="link.versions.some(title => title !== item.title) || link.sources.length" class="tg-resource-meta tg-share-sources"><span v-for="version in link.versions.filter(title => title !== item.title)" :key="version">{{ version }}</span><a v-for="source in link.sources" :key="source.url" :href="source.url" target="_blank" rel="noopener noreferrer">{{ source.label }}<IconExternalLink :size="12" aria-hidden="true" /></a></div>
              </div>
              <div class="tg-share-controls">
                <div class="tg-link-heading"><button type="button" class="tg-copy" aria-label="复制分享链接" @click="copy(link)"><IconCopy :size="14" aria-hidden="true" />{{ copied === link.url ? '已复制' : '复制' }}</button><a :href="link.url" target="_blank" rel="noopener noreferrer" class="tg-copy">打开分享<IconExternalLink :size="12" aria-hidden="true" /></a></div>
                <div class="tg-actions"><ShareTransferButton :transfer="transfers" :resource="{ load: async () => [{ url: link.url }] }" /><button type="button" class="resource-action-button resource-primary-button" :disabled="shares.pending.value[link.id] || shares.statuses.value[link.id]?.success" :title="shares.statuses.value[link.id]?.message" @click="submitShare(item, link)"><IconFile :size="15" aria-hidden="true" /><span>{{ shares.pending.value[link.id] ? '处理中…' : shares.statuses.value[link.id]?.success ? '已生成' : '分享 STRM' }}</span></button></div>
              </div>
              <p v-if="shares.statuses.value[link.id]?.message" class="tg-task-status" role="status">{{ shares.statuses.value[link.id]?.message }}</p>
            </div>
          </li>
        </ul>
      </div>
      <details v-if="candidates.length" class="tg-candidates" :open="!(tab === 'ed2k' ? files.length : shareCount)">
        <summary>清单来源与读取状态（{{ candidates.length }} 页）</summary>
        <div v-for="candidate in candidates" :key="candidate.url" class="tg-candidate">
          <div><strong>{{ candidate.item.title }}</strong><span>{{ candidate.item.channel.title }}</span><span v-if="candidate.references.length > 1">合并 {{ candidate.references.length }} 个入口</span><a :href="candidate.url" target="_blank" rel="noopener noreferrer">查看清单<IconExternalLink :size="12" aria-hidden="true" /></a><a :href="candidate.item.sourceUrl" target="_blank" rel="noopener noreferrer">原帖</a></div>
          <span v-if="pages[candidate.url]">{{ sourceSummary(pages[candidate.url]!) }}</span>
          <span v-else-if="pageStatus[candidate.url]?.busy">正在读取…</span>
          <button v-else-if="pageStatus[candidate.url]?.error" type="button" class="tg-button" :disabled="!!phase || sourcesPaused || errorCode === 'channel_search_expired'" @click="sourcePages.resolve(candidate)">重试读取</button>
          <span v-else>{{ sourcesPaused ? '已暂停，等待继续' : '等待读取' }}</span>
          <p v-if="pageStatus[candidate.url]?.error" role="status">{{ pageStatus[candidate.url]!.error }}</p>
        </div>
      </details>
      <div class="tg-list-end"><button v-if="canContinue" class="tg-button" :disabled="!!phase || retrySeconds > 0" @click="search(true)">{{ phase === 'more' ? '正在加载…' : '加载更多资源' }}</button><span v-else-if="!phase && !error && !result.partial && !result.truncated">{{ sourceProgress.loaded < sourceProgress.total ? '频道结果已加载，清单尚未全部载入' : '已加载全部频道结果' }}</span></div>
    </template>
    <template v-if="ed2kBatch?.visible" #footer>
      <div class="tg-batch-footer" role="group" aria-label="ED2K 批量操作">
        <div class="tg-batch-summary">
          <div class="tg-batch-heading">
            <strong aria-live="polite">已选 {{ ed2kBatch.chosenCount }} 项</strong>
            <span v-if="ed2kBatch.hiddenCount" class="tg-batch-hidden">（{{ ed2kBatch.hiddenCount }} 项在其他筛选中）</span>
            <button v-if="ed2kBatch.chosenCount" type="button" class="tg-batch-clear" :disabled="ed2kBatch.busy" @click="ed2kBatch.clear()">清空选择</button>
          </div>
          <p v-if="ed2kBatch.cloudTargetName || ed2kBatch.strmTargetName" class="tg-batch-targets">
            <span v-if="ed2kBatch.cloudTargetName">云下载：{{ ed2kBatch.cloudTargetName }}</span>
            <span v-if="ed2kBatch.strmTargetName">STRM：{{ ed2kBatch.strmTargetName }}</span>
          </p>
        </div>
        <div class="tg-batch-actions">
          <button v-if="ed2kBatch.cloudBatchRunning" type="button" class="tg-batch-pill" @click="ed2kBatch.stop()">停止后续提交</button>
          <template v-else>
            <button type="button" class="tg-batch-pill" aria-label="复制所选 ED2K 链接" :disabled="!ed2kBatch.chosenCount" @click="ed2kBatch.copySelected()"><IconCopy :size="14" aria-hidden="true" /><span>复制</span></button>
            <button type="button" class="tg-batch-pill" aria-label="云下载所选文件" :disabled="!ed2kBatch.canCloud" @click="ed2kBatch.downloadSelected()"><IconDownload :size="14" aria-hidden="true" /><span>云下载</span></button>
            <button type="button" class="tg-batch-pill tg-batch-primary" aria-label="为所选文件生成 ED2K STRM" :disabled="!ed2kBatch.canStrm" @click="ed2kBatch.generateSelected()"><IconFile :size="14" aria-hidden="true" /><span>ED2K STRM</span></button>
          </template>
        </div>
      </div>
    </template>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.floating-dialog.tg-channel-search .floating-dialog-header) { padding-bottom: 0; }
.tg-tabs { border-bottom: 1px solid var(--search-dialog-card-border); }
.tg-tabs > :deep(.segmented-control) { margin-bottom: -1px; }
.tg-tabs > span { align-self: center; white-space: nowrap; }
/* 在弹窗作用域内将 SelectMenu 的焦点色与选中色对齐低饱和灰蓝，避免跳出刺眼电光蓝。 */
:global(dialog.floating-dialog.tg-channel-search) { color: var(--search-dialog-text); --focus: 54 91 131; --selection-content: 54 91 131; --selection-surface: 235 241 248; }
:global(.dark dialog.floating-dialog.tg-channel-search) { --focus: 173 199 236; --selection-content: 173 199 236; --selection-surface: 34 45 62; }
@media (min-width: 640px) { :global(dialog.floating-dialog.tg-channel-search) { width: min(1100px, calc(100vw - 32px)); max-width: calc(100vw - 32px); height: min(860px, calc(100dvh - 48px)); max-height: calc(100dvh - 48px); } }
.tg-window-title { display: flex; align-items: center; gap: 8px; min-width: 0; }.tg-window-title img { flex: none; }.tg-window-title > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; }.tg-window-title small { flex: none; font-size: var(--search-dialog-font-meta, 11px); font-weight: 400; color: var(--search-dialog-muted); }
.tg-help-toggle { display: grid; flex: none; place-items: center; width: 36px; height: 36px; border-radius: 10px; color: var(--search-dialog-muted); }.tg-help-toggle[aria-expanded='true'] { color: var(--search-dialog-primary); background: var(--search-dialog-action-background); }
.tg-help-toggle:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
.tg-help { margin-bottom: 8px; padding: 8px 10px; border-radius: 8px; background: var(--search-dialog-card); font-size: 12px; line-height: 1.7; color: var(--search-dialog-muted); }.tg-help > div { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; }.tg-help a { display: inline-flex; align-items: center; gap: 5px; min-height: 36px; color: var(--search-dialog-primary); }
.tg-toolbar { display: flex; align-items: center; gap: 8px; }.tg-filter { display: flex; flex: 1; min-width: 0; align-items: center; gap: 8px; border-radius: 999px; padding: 0 14px; background: var(--search-dialog-card); color: var(--search-dialog-muted); }.tg-filter input { min-width: 0; width: 100%; padding: 9px 0; background: transparent; outline: none; font-size: 13px; color: var(--search-dialog-text); }
.tg-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 13px; border-radius: 999px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; font-weight: 500; text-decoration: none; white-space: nowrap; }.tg-button.tg-primary { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }.tg-button:disabled { opacity: .45; cursor: default; }
.tg-tabs { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 8px; }.tg-tabs > span { font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }.tg-notice { display: flex; flex-wrap: wrap; gap: 6px 12px; margin-bottom: 10px; padding: 10px; border-radius: 9px; background: var(--search-dialog-card); font-size: 12px; line-height: 1.7; }.tg-error { color: #b91c1c; background: rgb(239 68 68 / .08); }.dark .tg-error { color: #fca5a5; }.tg-text { color: var(--search-dialog-primary); text-decoration: underline; }
.tg-warnings { padding: 8px 10px; margin-bottom: 8px; background: var(--search-dialog-card); border-radius: 8px; font-size: 12px; color: var(--search-dialog-muted); }.tg-warnings summary { cursor: pointer; }.tg-warnings p { margin-top: 6px; overflow-wrap: anywhere; }
.tg-load-status { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; min-width: 0; margin-bottom: 4px; font-size: var(--search-dialog-font-meta, 11px); line-height: 1.6; color: var(--search-dialog-muted); }
.tg-load-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-left: auto; }
.tg-load-actions button { position: relative; isolation: isolate; display: inline-flex; align-items: center; justify-content: center; min-height: 30px; padding: 3px 10px; border-radius: 999px; color: var(--search-dialog-primary); font-weight: 600; text-decoration: none; white-space: nowrap; }
.tg-load-actions button::before { content: ''; position: absolute; z-index: -1; inset: 2px 0; border-radius: inherit; background: var(--search-dialog-action-background); pointer-events: none; transition: background-color .15s ease; }
.tg-load-actions button:disabled { opacity: .45; cursor: default; }
.tg-more-hint { margin-left: auto; }
.tg-candidates { margin-top: 12px; padding: 10px 12px; border: 1px solid var(--search-dialog-card-border); border-radius: 10px; font-size: 12px; }.tg-candidates > summary { cursor: pointer; color: var(--search-dialog-primary); }.tg-candidate { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; padding-block: 10px; border-top: 1px solid var(--search-dialog-card-border); }.tg-candidate > div { display: flex; flex-wrap: wrap; align-items: center; gap: 5px 12px; min-width: 0; }.tg-candidate strong { max-width: 100%; overflow-wrap: anywhere; font-weight: 500; }.tg-candidate span { color: var(--search-dialog-muted); }.tg-candidate a { display: inline-flex; align-items: center; gap: 4px; color: var(--search-dialog-primary); }.tg-candidate > p { width: 100%; color: #b45309; overflow-wrap: anywhere; }.dark .tg-candidate > p { color: #fcd34d; }
.tg-share-tools { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin: 4px 0 10px; font-size: 12px; color: var(--search-dialog-muted); }
.tg-results { display: grid; gap: 8px; }
.tg-resource { min-width: 0; padding: 12px; border: 1px solid var(--search-dialog-card-border); border-radius: 12px; background: var(--search-dialog-card); }
.tg-resource h4 { font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }
.tg-resource-title { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; }
.tg-resource-title h4 { min-width: 0; }
.tg-episode { font-variant-numeric: tabular-nums; color: var(--search-dialog-primary); font-size: 12px; font-weight: 600; }
.tg-resource-summary { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; margin-top: 5px; font-size: 12px; color: var(--search-dialog-text); }
.tg-spec-size { font-variant-numeric: tabular-nums; }
.tg-post-date { font-variant-numeric: tabular-nums; font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }
.tg-specs { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; font-size: 12px; }
.tg-resource-meta { display: flex; flex-wrap: wrap; gap: 4px 12px; font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }
.tg-resource-meta a { display: inline-flex; align-items: center; gap: 4px; color: var(--search-dialog-muted); }
/* 115 分享按「规格与来源在上、操作在下」分组，多链接时以细线区分各自对应关系。 */
.tg-share-row { display: grid; gap: 6px; margin-top: 8px; }
.tg-share-row.is-multi { padding-top: 10px; border-top: 1px solid var(--search-dialog-card-border); }
.tg-share-info { display: grid; gap: 4px; min-width: 0; }
.tg-share-sources { width: 100%; overflow-wrap: anywhere; }
.tg-share-controls { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 6px 10px; }
.tg-link-heading, .tg-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.tg-actions .resource-primary-button::before { box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--search-dialog-primary) 26%, transparent); }
.tg-copy { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; padding: 4px 6px; border-radius: 8px; font-size: var(--search-dialog-font-meta, 11px); color: var(--search-dialog-muted); }
.tg-task-status { width: 100%; font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; color: var(--search-dialog-muted); }
.tg-empty { padding: 24px 12px; text-align: center; font-size: 12px; line-height: 1.8; color: var(--search-dialog-muted); }.tg-empty p { margin-bottom: 10px; }.tg-list-end { display: flex; justify-content: center; margin-top: 14px; font-size: 12px; color: var(--search-dialog-muted); }.search-log-footer { padding: 0; }
/* ED2K 固定批量底栏：常驻已选计数、当前目标目录摘要与批量操作按钮。 */
.tg-batch-footer { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 14px; }
.tg-batch-summary { display: grid; gap: 2px; min-width: 0; }
.tg-batch-heading { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; font-size: 12px; }
.tg-batch-heading strong { font-size: 13px; font-weight: 600; color: var(--search-dialog-text); font-variant-numeric: tabular-nums; }
.tg-batch-hidden { font-size: 11px; color: var(--search-dialog-muted); }
.tg-batch-clear { padding: 2px 6px; border-radius: 999px; font-size: 12px; color: var(--search-dialog-primary); }
.tg-batch-targets { display: flex; flex-wrap: wrap; gap: 2px 12px; font-size: 11px; color: var(--search-dialog-muted); overflow-wrap: anywhere; }
.tg-batch-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-left: auto; }
.tg-batch-pill { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 36px; padding: 6px 14px; border-radius: 999px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; font-weight: 600; white-space: nowrap; transition: background-color .15s ease; }
.tg-batch-pill.tg-batch-primary { background: var(--search-dialog-action-background); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--search-dialog-primary) 24%, transparent); color: var(--search-dialog-primary); }
.tg-batch-pill:disabled, .tg-batch-clear:disabled { opacity: .45; cursor: default; }
.tg-button:focus-visible, .tg-copy:focus-visible, .tg-text:focus-visible, .tg-batch-pill:focus-visible, .tg-batch-clear:focus-visible, .tg-filter:focus-within, summary:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (hover: hover) {
  .tg-button:hover:not(:disabled), .tg-copy:hover, .tg-batch-pill:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }
  .tg-batch-pill.tg-batch-primary:hover:not(:disabled), .tg-batch-clear:hover:not(:disabled) { background: var(--search-dialog-action-hover); }
  .tg-load-actions button:hover:not(:disabled)::before { background: var(--search-dialog-action-hover); }
  .tg-resource-meta a:hover { color: var(--search-dialog-primary); }
}
@media (max-width: 639px) {
  .tg-window-title { font-size: var(--sheet-font-title); }
  .tg-window-title small { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .tg-help-toggle { width: 44px; height: 44px; }
  .tg-help a { min-height: 44px; }
  .tg-filter input { font-size: 16px; }
  .tg-button { min-height: var(--sheet-touch-size); font-size: var(--sheet-font-body); }
  .tg-resource h4 { font-size: var(--sheet-font-body); line-height: var(--sheet-line-body); }
  .tg-toolbar > .tg-button { flex: none; min-width: 44px; padding-inline: 10px; }
  .tg-refresh span { display: none; }
  .tg-tabs { margin-top: 6px; }
  .tg-tabs > span, .tg-merged-hint, .tg-more-hint { display: none; }
  .tg-load-status { flex-wrap: nowrap; gap: 8px; }
  .tg-load-status > span { flex: 1; min-width: 0; }
  .tg-load-actions { flex: none; margin-left: 0; }
  .tg-load-actions button { min-height: 44px; padding: 2px 8px; }
  .tg-load-actions button::before { inset: 7px 0; }
  .tg-batch-footer { gap: 6px; }
  .tg-batch-summary { width: 100%; }
  .tg-batch-actions { width: 100%; margin-left: 0; gap: 4px; }
  .tg-batch-pill { flex: 1 1 auto; min-height: 44px; padding: 6px 10px; font-size: 12px; }
  .tg-share-controls { gap: 2px 8px; }
  .tg-link-heading { margin-left: -6px; }
  .tg-actions { flex: 1 0 100%; }
  .tg-actions > * { flex: 1 1 0; min-width: 0; }
  .tg-actions :deep(.transfer-action) { max-width: none; }
  .tg-copy { min-height: 44px; }
}
@media (max-width: 359px) {
  .tg-batch-pill { padding-inline: 8px; }
  .tg-resource { padding: 10px; }
  .tg-actions { gap: 6px; }
  .tg-actions :deep(.resource-action-button) { padding-inline: 8px; font-size: 13px; }
}
@media (prefers-contrast: more) {
  .tg-actions .resource-primary-button::before { box-shadow: inset 0 0 0 1px var(--search-dialog-primary); }
}
@media (prefers-reduced-motion: reduce) { .tg-load-actions button::before, .tg-batch-pill { transition: none; } }
</style>
