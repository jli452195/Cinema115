<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import { gyingRequest, type GyingConfigView, type GyingResourceLink, type GyingSearchAttempt, type GyingSearchItem, type GyingSearchResponse } from '../types/gying'
import { normalize115Share } from '../utils/shareLink'
import gyingLogo from '../assets/gying/logo-gying.png'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; originalTitle: string; imdbId: string; themeColors?: Record<string, string> }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const viewportStyle = ref<Record<string, string>>({})
const dialogStyle = computed(() => ({ ...props.themeColors, ...viewportStyle.value }))
const mobileViewport = ref(window.matchMedia('(max-width: 639px)').matches)
const shortViewport = ref(false)
const displayLimit = ref(30)
const config = ref<GyingConfigView>()
const result = ref<GyingSearchResponse>()
const activeItemId = ref('all')
const activeResourceType = ref('all')
const activeMagnetSubType = ref('all')
type LoadPhase = '' | 'config' | 'search'
const phase = ref<LoadPhase>('')
const lifecycle = new AbortController()
const shareRuns = useShareSTRMRun(props.token, lifecycle.signal, { scope: `gying/${props.kind}/${props.tmdbId}` })
const busy = computed(() => phase.value !== '' || shareRuns.preparing.value > 0)
const elapsedSeconds = ref(0)
const error = ref('')
const copied = ref('')
const copyError = ref('')
const coverErrors = ref<Record<string, boolean>>({})
function coverSource(item: GyingSearchItem) {
  // 仅加载 NAS 同源封面，不回退到外站，也不向图片地址传递凭据。
  const source = item.coverUrl || ''
  return /^\/api\/gying\/image\/(mv|tv|ac|zy)\/[A-Za-z0-9]{1,64}\.webp$/.test(source) ? source : ''
}
type CloudDownloadConfig = { accountId: string; targetCID: string; targetPath: string }
type CloudDownloadStatus = { success: boolean; message: string; needsConfig?: boolean }
const cloudSubmitting = ref<Record<string, boolean>>({})
const cloudStatuses = ref<Record<string, CloudDownloadStatus>>({})
let progressTimer: number | undefined
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const mediaLabels: Record<string, string> = { movie: '电影', tv: '剧集', anime: '动漫', other: '其他' }
const defaultResourceOrder = ['magnet', 'aliyun', 'quark', 'baidu', 'xunlei', 'mobile', '115', '123', 'guangya', 'uc', 'tianyi', 'other']
const magnetSubTypeOrder = ['720P', '中字720P', '1080P', '中字1080P', '4K', '中字4K', '原盘', '未知']
const resourceOrder = computed(() => config.value?.resourceOrder?.length ? config.value.resourceOrder : defaultResourceOrder)
function resourceRank(type: string) {
  const rank = resourceOrder.value.indexOf(type)
  return rank < 0 ? resourceOrder.value.length : rank
}
function sortResourceLinks(links: GyingResourceLink[]) {
  return links.map((link, index) => ({ link, index })).sort((a, b) => resourceRank(a.link.type) - resourceRank(b.link.type) || a.index - b.index).map(item => item.link)
}
function shareResource(item: GyingSearchItem, link: GyingResourceLink): ShareSTRMResource {
  const normalized = normalize115Share(link.url)
  const password = link.password?.trim() || ''
  return {
    id: `${item.id}\u0000${normalized}\u0000${password}`,
    identity: `gying/${props.kind}/${props.tmdbId}/${item.id}/${normalized}/${password}`,
    load: async () => [{ url: link.url, password }],
  }
}
function shareResourceID(item: GyingSearchItem, link: GyingResourceLink) { return shareResource(item, link).id }
function canShareSTRM(link: GyingResourceLink) { return link.type === '115' && normalize115Share(link.url) !== '' }
function allShareResources() {
  return resultItems.value.flatMap(item => item.links.filter(canShareSTRM).map(link => shareResource(item, link)))
}
function restoreShareRuns() { void shareRuns.restore(allShareResources()) }
function shareSTRM(item: GyingSearchItem, link: GyingResourceLink) { void shareRuns.submit(shareResource(item, link)) }
function stopShareRun() { void shareRuns.stop() }
function canCloudDownload(link: GyingResourceLink) { return link.type === 'magnet' && link.category === 'torrent' }
function cloudResourceID(item: GyingSearchItem, link: GyingResourceLink) { return `${item.id}\u0000${link.url}` }
function cloudErrorNeedsConfig(reason: unknown) {
  const message = reason instanceof Error ? reason.message : ''
  return /请先.*(?:选择|配置)|尚未登录|账号.*不存在|配置|凭据/.test(message)
}
async function cloudDownload(item: GyingSearchItem, link: GyingResourceLink) {
  if (!canCloudDownload(link)) return
  const id = cloudResourceID(item, link)
  if (cloudSubmitting.value[id]) return
  cloudSubmitting.value = { ...cloudSubmitting.value, [id]: true }
  const clearedStatuses = { ...cloudStatuses.value }
  delete clearedStatuses[id]
  cloudStatuses.value = clearedStatuses
  try {
    // 提交前读取云下载明确绑定的账号和目录，弹窗不改写配置。
    const config = await p115Request<CloudDownloadConfig>(props.token, 'cloud-download/config', {
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    if (!config.accountId || !config.targetCID) {
      throw Object.assign(new Error('请先配置主账号，并在“115 云下载”保存下载目录'), { needsConfig: true })
    }
    const receipt = await submitCloudDownload(props.token, config.accountId, link.url, config.targetCID, lifecycle.signal)
    cloudStatuses.value = { ...cloudStatuses.value, [id]: { success: true, message: cloudDownloadNotice(receipt, config.targetPath) } }
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      const message = reason instanceof Error ? reason.message : '提交 115 云下载失败'
      cloudStatuses.value = { ...cloudStatuses.value, [id]: { success: false, message, needsConfig: Boolean((reason as { needsConfig?: boolean })?.needsConfig) || cloudErrorNeedsConfig(reason) } }
    }
  } finally {
    const next = { ...cloudSubmitting.value }
    delete next[id]
    cloudSubmitting.value = next
  }
}
const resultItems = computed(() => result.value?.items || [])
const selectedItems = computed(() => {
  return activeItemId.value === 'all' ? resultItems.value : resultItems.value.filter(item => item.id === activeItemId.value)
})
const resourceOptions = computed(() => {
  const values = new Map<string, { type: string; label: string; count: number }>()
  for (const item of selectedItems.value) for (const link of item.links) {
    const current = values.get(link.type)
    if (current) current.count++
    else values.set(link.type, { type: link.type, label: link.typeLabel, count: 1 })
  }
  const options = [...values.values()].sort((a, b) => {
    return resourceRank(a.type) - resourceRank(b.type) || a.label.localeCompare(b.label, 'zh-CN')
  })
  return [{ type: 'all', label: '全部资源', count: options.reduce((sum, item) => sum + item.count, 0) }, ...options]
})
function magnetSubType(link: GyingResourceLink) { return link.subTypeLabel?.trim() || '未知' }
const magnetSubTypeOptions = computed(() => {
  const values = new Map<string, { type: string; label: string; count: number }>()
  for (const item of selectedItems.value) for (const link of item.links) {
    if (link.type !== 'magnet') continue
    // Group by the site's label so missing/unrecognized codes form one
    // Unknown option, and matching labels across film entries combine.
    const type = magnetSubType(link)
    const label = type
    const current = values.get(type)
    if (current) current.count++
    else values.set(type, { type, label, count: 1 })
  }
  const options = [...values.values()].sort((a, b) => {
    const rankA = magnetSubTypeOrder.indexOf(a.label), rankB = magnetSubTypeOrder.indexOf(b.label)
    return (rankA < 0 ? magnetSubTypeOrder.length : rankA) - (rankB < 0 ? magnetSubTypeOrder.length : rankB) || a.label.localeCompare(b.label, 'zh-CN')
  })
  return [{ type: 'all', label: '全部磁力', count: options.reduce((sum, item) => sum + item.count, 0) }, ...options]
})
const itemStrip = ref<HTMLElement>()
const sourceStrip = ref<HTMLElement>()
const subTypeStrip = ref<HTMLElement>()
const { indicatorStyle: itemIndicatorStyle } = useSlidingIndicator(itemStrip, '[aria-pressed="true"]', [activeItemId, resultItems])
const { indicatorStyle: sourceIndicatorStyle } = useSlidingIndicator(sourceStrip, '[aria-pressed="true"]', [activeResourceType, resourceOptions], { insetY: 6 })
const { indicatorStyle: subTypeIndicatorStyle } = useSlidingIndicator(subTypeStrip, '[aria-pressed="true"]', [activeMagnetSubType, magnetSubTypeOptions], { insetY: 6 })
const visibleItems = computed(() => {
  return selectedItems.value.flatMap(item => {
    const resourceLinks = activeResourceType.value === 'all' ? item.links : item.links.filter(link => link.type === activeResourceType.value)
    const links = sortResourceLinks(resourceLinks.filter(link => activeResourceType.value !== 'magnet' || activeMagnetSubType.value === 'all' || magnetSubType(link) === activeMagnetSubType.value))
    return links.length ? [{ ...item, links }] : []
  })
})
const selectedItem = computed(() => activeItemId.value === 'all' ? undefined : resultItems.value.find(item => item.id === activeItemId.value))
const singleItemSelected = computed(() => Boolean(selectedItem.value))
const filteredLinkCount = computed(() => visibleItems.value.reduce((count, item) => count + item.links.length, 0))
const displayedItems = computed(() => {
  let remaining = displayLimit.value
  return visibleItems.value.flatMap(item => {
    if (remaining <= 0) return []
    const links = item.links.slice(0, remaining)
    remaining -= links.length
    return [{ ...item, links, totalLinks: item.links.length }]
  })
})
const displayedLinkCount = computed(() => Math.min(displayLimit.value, filteredLinkCount.value))
function resetDisplay() {
  displayLimit.value = 30
  void nextTick(() => {
    const body = dialog.value?.querySelector<HTMLElement>('.gy-body')
    const anchor = dialog.value?.querySelector<HTMLElement>('.gy-filter-anchor')
    if (body && anchor) body.scrollTop = Math.min(body.scrollTop, anchor.offsetTop)
  })
}
function selectMagnetSubType(type: string) {
  activeMagnetSubType.value = type
  resetDisplay()
}
function resetFilters() { selectItem('all') }
async function showMore() {
  const previousCount = displayedLinkCount.value
  displayLimit.value += 30
  await nextTick()
  const row = dialog.value?.querySelectorAll<HTMLElement>('[data-resource-row]')[previousCount]
  row?.querySelector<HTMLElement>('button, a')?.focus({ preventScroll: true })
  const body = dialog.value?.querySelector<HTMLElement>('.gy-body')
  const filters = dialog.value?.querySelector<HTMLElement>('.gy-filters')
  if (row && body && filters) body.scrollTop += row.getBoundingClientRect().top - filters.getBoundingClientRect().bottom - 12
}
const searchAttempts = computed(() => result.value?.attempts || [])
const currentSearchQuery = computed(() => searchAttempts.value.find(attempt => attempt.method === result.value?.matchedBy)?.query || result.value?.query || '')
const truncatedAttempts = computed(() => searchAttempts.value.filter(attempt => attempt.truncated))
const emptySummary = computed(() => {
  if (result.value?.partial) return '部分资源详情读取失败，尚不能确认是否有可用资源，请稍后重新搜索。'
  if (result.value?.unavailableDetails) return '匹配资源已下架或不可用，暂未取得可用的网盘或磁力资源。'
  const methods = new Set(searchAttempts.value.map(attempt => attempt.method))
  if (methods.has('title') && methods.has('imdb')) return '已搜索影视原名和 IMDb 编号，均未发现可用资源。'
  if (methods.has('imdb')) return 'IMDb 编号搜索未发现可用资源。'
  return '原名搜索未发现可用资源。当前条目没有可用的 IMDb 编号，因此未执行兜底搜索。'
})

function attemptDescription(attempt: GyingSearchAttempt) {
  if (!attempt.total) return '站点未返回匹配条目'
  const detail = attempt.usable ? `读取到 ${attempt.usable} 个可用资源条目` : '未发现可用网盘或磁力资源'
  return `站点匹配 ${attempt.total} 个条目，${detail}${attempt.unavailable ? `，已跳过 ${attempt.unavailable} 个下架或不可用条目` : ''}${attempt.failed ? `，另有 ${attempt.failed} 个详情读取失败` : ''}`
}

function beginPhase(next: Exclude<LoadPhase, ''>) {
  finishPhase()
  phase.value = next
  elapsedSeconds.value = 0
  const started = Date.now()
  progressTimer = window.setInterval(() => { elapsedSeconds.value = Math.floor((Date.now() - started) / 1000) }, 1000)
}

function finishPhase(expected?: Exclude<LoadPhase, ''>) {
  if (expected && phase.value !== expected) return
  if (progressTimer !== undefined) {
    window.clearInterval(progressTimer)
    progressTimer = undefined
  }
  phase.value = ''
}

function requestErrorMessage(value: unknown, action: string) {
  if (value instanceof Error && value.name === 'TimeoutError') return `${action}超时，请稍后重新尝试。`
  if (value instanceof Error && value.name === 'AbortError') return `${action}已取消，请重新尝试。`
  return value instanceof Error ? value.message : `${action}失败`
}

function selectItem(id: string) {
  activeItemId.value = id
  activeResourceType.value = 'all'
  activeMagnetSubType.value = 'all'
  resetDisplay()
}

function selectResourceType(type: string) {
  activeResourceType.value = type
  activeMagnetSubType.value = 'all'
  resetDisplay()
}

async function search(refresh = false) {
  if (busy.value) return
  beginPhase('search'); error.value = ''; copied.value = ''
  try {
    result.value = await gyingRequest<GyingSearchResponse>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: props.tmdbId, refresh })
    coverErrors.value = {}
    activeItemId.value = 'all'; activeResourceType.value = 'all'; activeMagnetSubType.value = 'all'
    displayLimit.value = 30; copyError.value = ''
    await nextTick()
    const body = dialog.value?.querySelector<HTMLElement>('.gy-body')
    if (body) body.scrollTop = 0
  } catch (e) { if (!lifecycle.signal.aborted) error.value = requestErrorMessage(e, '观影搜索') }
  finally { finishPhase('search') }
  if (result.value && !lifecycle.signal.aborted) restoreShareRuns()
}

async function initialize() {
  if (busy.value) return
  beginPhase('config'); error.value = ''
  let shouldSearch = false
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal)
    shouldSearch = config.value.configured
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = requestErrorMessage(e, '读取观影配置')
  } finally {
    finishPhase('config')
  }
  if (shouldSearch && !lifecycle.signal.aborted) await search()
}

async function copyLink(link: GyingResourceLink) {
  const text = `${link.url}${link.password ? `  提取码：${link.password}` : ''}`
  copied.value = ''; copyError.value = ''
  try {
    await copyText(text, dialog.value)
    copied.value = link.url
  }
  catch { copied.value = ''; copyError.value = link.url }
}

function syncVisualViewport() {
  mobileViewport.value = window.matchMedia('(max-width: 639px)').matches
  const viewport = window.visualViewport
  shortViewport.value = (viewport?.height ?? window.innerHeight) < 520
  viewportStyle.value = {
    '--gy-visible-height': `${viewport?.height ?? window.innerHeight}px`,
    '--gy-visible-top': `${viewport?.offsetTop ?? 0}px`,
    '--gy-visible-bottom': `${viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0}px`,
  }
}

onMounted(async () => {
  syncVisualViewport()
  window.visualViewport?.addEventListener('resize', syncVisualViewport)
  window.visualViewport?.addEventListener('scroll', syncVisualViewport)
  window.addEventListener('resize', syncVisualViewport)
  try {
    document.body.style.overflow = 'hidden'
    if (!dialog.value) throw new Error('观影搜索弹窗初始化失败')
    if (!dialog.value.open) dialog.value.showModal()
  } catch (e) {
    error.value = requestErrorMessage(e, '打开观影搜索弹窗')
    finishPhase()
    return
  }
  await initialize()
})
onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', syncVisualViewport)
  window.visualViewport?.removeEventListener('scroll', syncVisualViewport)
  window.removeEventListener('resize', syncVisualViewport)
  lifecycle.abort()
  finishPhase()
  if (dialog.value?.open) dialog.value.close()
  document.body.style.overflow = previousOverflow
  previousFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :style="dialogStyle" aria-labelledby="gying-search-title" aria-describedby="gying-work-title" class="gy-dialog media-search-dialog" :class="{ 'gy-short-viewport': shortViewport }" @cancel.prevent="emit('close')">
      <div class="gy-layout">
        <header class="gy-header">
          <div class="gy-brand"><img :src="gyingLogo" alt="" width="34" height="34" /></div>
          <div class="gy-heading">
            <h2 id="gying-search-title">观影搜索</h2>
            <div class="gy-heading-meta">
              <p id="gying-work-title" :title="title">{{ title }}</p>
              <span v-if="result" class="gy-search-caption" :class="{ 'gy-cached-caption': result.cached && phase !== 'search' && !error }">{{ phase === 'search' ? '搜索中' : error ? '上次结果' : result.cached ? '已复用缓存' : result.matchedBy === 'imdb' ? 'IMDb 搜索' : '原名搜索' }}</span>
            </div>
          </div>
          <button autofocus type="button" aria-label="关闭观影搜索" class="gy-icon-button" @click="emit('close')">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>
        <div class="gy-tools">
          <span v-if="result && currentSearchQuery" class="gy-search-query" :title="currentSearchQuery">
            <span class="gy-query-method">{{ result.matchedBy === 'imdb' ? 'IMDb' : '原名' }}</span>
            <span class="gy-query-text">{{ currentSearchQuery }}</span>
          </span>
          <span v-else>选择影视条目，筛选网盘与磁力资源</span>
          <div>
            <button v-if="config?.configured" type="button" :disabled="busy" class="gy-tool-button" aria-label="重新搜索" @click="search(true)">
              <svg aria-hidden="true" :class="{ 'gy-spinning': phase === 'search' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg>
              {{ phase === 'search' ? '搜索中 ' + elapsedSeconds + ' 秒' : '重搜' }}
            </button>
            <button v-else-if="!config && error" type="button" :disabled="busy" class="gy-tool-button" @click="initialize">重试</button>
            <RouterLink to="/gying" class="gy-tool-button" aria-label="观影配置" @click="emit('close')">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="3" fill="var(--gy-container)" /><circle cx="15" cy="17" r="3" fill="var(--gy-container)" /></svg>
              配置
            </RouterLink>
          </div>
        </div>
        <div class="gy-body">
          <div v-if="config && !config.configured" class="gy-setup">
            <h3>登录观影后搜索资源</h3><p>请先配置并登录观影站账号。</p>
            <RouterLink to="/gying" class="gy-button gy-primary-button" @click="emit('close')">配置并登录观影</RouterLink>
          </div>
          <p v-if="phase === 'config'" role="status" class="gy-notice gy-info">正在读取观影搜索配置…<span v-if="elapsedSeconds >= 10">已等待 {{ elapsedSeconds }} 秒，请检查后端连接状态。</span></p>
          <p v-else-if="phase === 'search'" role="status" class="gy-notice gy-info">正在搜索影视原名并读取资源详情…
            <span v-if="elapsedSeconds < 10">原名没有有效资源时会自动尝试 IMDb 编号；匹配条目较多时会需要更长时间。</span>
            <span v-else>已等待 {{ elapsedSeconds }} 秒，最多等待约 2 分钟。</span>
            <span v-if="result">下方保留上次搜索结果，搜索完成后更新。</span>
          </p>
          <div v-if="shareRuns.busy.value" role="status" class="gy-notice gy-info gy-task-notice">
            <div><strong>{{ shareRuns.activeRunID.value ? 'STRM 任务在后台执行' : '正在准备 STRM 任务' }}</strong><span>{{ shareRuns.activeRunID.value ? '可以继续选择其他资源，关闭窗口不影响已提交任务。' : '提交成功后可关闭窗口。' }}</span></div>
            <button v-if="shareRuns.activeRunID.value" type="button" :disabled="shareRuns.stopping.value" class="gy-text-button" @click="stopShareRun">{{ shareRuns.stopping.value ? '正在停止…' : '停止已提交批次' }}</button>
          </div>
          <p v-if="error" role="alert" class="gy-notice gy-error">{{ error }}<span v-if="result">本次更新未成功，当前展示上次搜索结果。</span></p>
          <template v-if="result">
            <section v-if="!resultItems.length" class="gy-search-process" aria-label="搜索过程">
              <div class="gy-process-heading"><span>搜索过程</span><span v-if="result.cached && phase !== 'search' && !error" class="gy-cache">已复用缓存</span></div>
              <ul><li v-for="attempt in searchAttempts" :key="attempt.method + ':' + attempt.query">
                <span class="gy-badge gy-info">{{ attempt.method === 'imdb' ? 'IMDb' : '原名' }}</span>
                <code>{{ attempt.query }}</code><span>{{ attemptDescription(attempt) }}</span>
              </li></ul>
            </section>
            <p v-if="result.partial" role="status" class="gy-notice gy-warning">有 {{ result.failedDetails }} 个资源详情读取失败，当前展示其余成功结果。<span>可重新搜索补充；本次结果不写入搜索缓存。</span></p>
            <p v-for="attempt in truncatedAttempts" :key="'truncated:' + attempt.method" role="status" class="gy-notice gy-warning">{{ attempt.method === 'imdb' ? 'IMDb' : '原名' }}搜索返回 {{ attempt.total }} 个影视条目，本次仅读取前 100 个。</p>
            <template v-if="resultItems.length">
              <section id="gy-item-picker" class="gy-item-picker" aria-label="选择影视条目">
                <div class="gy-picker-heading"><h3>影视条目</h3><span>{{ resultItems.length }} 个条目<span class="gy-picker-hint">，可能包含不同季度与版本</span></span></div>
                <nav ref="itemStrip" aria-label="影视条目" class="gy-item-strip gy-scroll">
                  <div class="gy-selection-indicator gy-item-indicator" :style="itemIndicatorStyle" aria-hidden="true"></div>
                  <button type="button" class="gy-item-card gy-all-items" :aria-pressed="activeItemId === 'all'" @click="selectItem('all')">
                    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></svg>
                    <span><strong>全部条目</strong><small>{{ resultItems.length }} 个影视条目</small></span>
                  </button>
                  <button v-for="item in resultItems" :key="item.id" type="button" :aria-label="item.title + (item.year ? ' (' + item.year + ')' : '')" :aria-pressed="activeItemId === item.id" class="gy-item-card" :title="item.title" @click="selectItem(item.id)">
                    <span class="gy-cover">
                      <img v-if="coverSource(item) && !coverErrors[item.id]" :src="coverSource(item)" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" @error="coverErrors[item.id] = true" />
                      <svg v-else aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="4" y="3" width="16" height="18" rx="2" /><path d="m9 8 6 4-6 4Z" /></svg>
                    </span>
                    <span class="gy-item-name"><strong>{{ item.title }}</strong><small>{{ item.year || '年份未标注' }}<span>{{ mediaLabels[item.mediaType] }}</span></small></span>
                  </button>
                </nav>
              </section>
              <div class="gy-filter-anchor" aria-hidden="true"></div>
              <section class="gy-filters" aria-label="观影结果筛选">
                <div class="gy-filter-context">
                  <h3 class="gy-filter-title">资源筛选</h3>
                  <span role="status" :aria-label="'当前筛选显示 ' + displayedLinkCount + ' 条，共 ' + filteredLinkCount + ' 条资源'">{{ displayedLinkCount < filteredLinkCount ? '显示 ' + displayedLinkCount + ' / ' + filteredLinkCount : filteredLinkCount }} 条资源</span>
                </div>
                <nav ref="sourceStrip" aria-label="资源来源" class="gy-source-strip gy-scroll">
                  <div class="gy-selection-indicator gy-chip-indicator" :style="sourceIndicatorStyle" aria-hidden="true"></div>
                  <button v-for="option in resourceOptions" :key="option.type" type="button" :aria-pressed="activeResourceType === option.type" class="gy-chip" @click="selectResourceType(option.type)">
                    <span class="gy-chip-label">{{ option.label }}</span><span class="gy-chip-count">{{ option.count }}</span>
                  </button>
                </nav>
                <nav v-if="activeResourceType === 'magnet' && magnetSubTypeOptions.length > 1" ref="subTypeStrip" aria-label="磁力清晰度分类" class="gy-source-strip gy-subtypes gy-scroll">
                  <div class="gy-selection-indicator gy-chip-indicator" :style="subTypeIndicatorStyle" aria-hidden="true"></div>
                  <button v-for="option in magnetSubTypeOptions" :key="option.type" type="button" :aria-pressed="activeMagnetSubType === option.type" class="gy-chip" @click="selectMagnetSubType(option.type)"><span class="gy-chip-label">{{ option.label }}</span><span class="gy-chip-count">{{ option.count }}</span></button>
                </nav>
              </section>
            </template>
            <div v-if="!resultItems.length" role="status" class="gy-empty">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4M8 10.5h5" /></svg>
              <h3>{{ result.partial ? '资源详情读取不完整' : '暂未找到可用资源' }}</h3><p>{{ emptySummary }}</p>
              <span v-if="!result.partial">空结果缓存 2 分钟，也可以立即重新搜索。</span>
            </div>
            <div v-else-if="!visibleItems.length" role="status" class="gy-empty">
              <h3>当前筛选没有资源</h3><p>可以清除筛选，查看其他影视条目与来源。</p>
              <button type="button" class="gy-text-button" @click="resetFilters">清除筛选</button>
            </div>
            <div v-else class="gy-results">
              <article v-for="item in displayedItems" :key="item.id" class="gy-film" :class="{ 'gy-film-single': singleItemSelected }" :data-film-id="item.id" :aria-label="item.title + '资源'">
                <header v-if="!singleItemSelected" class="gy-film-heading">
                  <h3>{{ item.title }}<span v-if="item.year">({{ item.year }})</span></h3>
                  <div><span class="gy-badge gy-neutral">{{ mediaLabels[item.mediaType] }}</span><span>{{ item.links.length < item.totalLinks ? '显示 ' + item.links.length + ' / ' : '' }}{{ item.totalLinks }} 条链接</span></div>
                </header>
                <details v-if="item.description" class="gy-film-details">
                  <summary><svg aria-hidden="true" class="gy-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 5 7 7-7 7" /></svg>影视详情</summary>
                  <p>{{ item.description }}</p>
                </details>
                <ul class="gy-resource-list">
                  <li v-for="(link, index) in item.links" :key="link.type + ':' + link.url + ':' + index" class="gy-resource" data-resource-row>
                    <div class="gy-resource-main">
                      <div class="gy-resource-info">
                        <div class="gy-resource-title">
                          <h4>{{ link.workTitle || link.typeLabel + '资源' }}</h4>
                          <button type="button" data-gy-action="copy" class="gy-copy-button" @click="copyLink(link)">
                            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V4H4v12h4" /></svg>
                            {{ copied === link.url ? '已复制' : link.category === 'torrent' ? '复制磁力' : '复制链接' }}<span class="sr-only">{{ link.password ? '及提取码' : '' }}</span>
                          </button>
                        </div>
                        <div class="gy-resource-meta">
                          <div class="gy-resource-tags">
                            <span class="gy-badge gy-info">{{ link.typeLabel }}</span>
                            <span v-if="link.subTypeLabel" class="gy-badge gy-neutral">{{ link.subTypeLabel }}</span>
                            <span v-else-if="link.type === 'magnet'" class="gy-badge gy-neutral">分类未知</span>
                            <span v-if="link.size">{{ link.size }}</span><span v-if="link.seeds !== undefined">做种 {{ link.seeds }}</span>
                            <span v-if="link.updatedAt">{{ link.updatedAt }}</span><span v-if="link.password">提取码 {{ link.password }}</span>
                          </div>
                        </div>
                      </div>
                      <div class="gy-actions">
                        <a :href="link.url" target="_blank" rel="noopener noreferrer" data-gy-action="open" class="gy-button gy-outlined-button">
                          <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6M20 4 10 14M10 4H4v16h16v-6" /></svg>
                          {{ link.category === 'torrent' ? '打开' : '打开分享' }}
                        </a>
                        <button v-if="canCloudDownload(link)" type="button" data-gy-action="cloud" :disabled="phase !== '' || cloudSubmitting[cloudResourceID(item, link)] || cloudStatuses[cloudResourceID(item, link)]?.success" class="gy-button gy-primary-button" @click="cloudDownload(item, link)">
                          <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M7 18H6a4 4 0 0 1-.7-7.94 7 7 0 0 1 13.4 0A4 4 0 0 1 18 18h-1M12 12v9m-4-4 4 4 4-4" /></svg>
                          {{ cloudSubmitting[cloudResourceID(item, link)] ? '提交中…' : cloudStatuses[cloudResourceID(item, link)]?.success ? '已提交' : '115 云下载' }}
                        </button>
                        <button v-if="canShareSTRM(link)" type="button" data-gy-action="strm" :disabled="phase !== '' || shareRuns.pending.value[shareResourceID(item, link)] || shareRuns.statuses.value[shareResourceID(item, link)]?.success" class="gy-button gy-primary-button" @click="shareSTRM(item, link)">
                          <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Zm0 0v6h6" /><path d="m10 12 5 3-5 3Z" /></svg>
                          {{ shareRuns.pending.value[shareResourceID(item, link)] ? '已加入任务…' : shareRuns.statuses.value[shareResourceID(item, link)]?.success ? '已生成' : '分享 STRM' }}
                        </button>
                      </div>
                    </div>
                    <div v-if="copyError === link.url" role="alert" class="gy-notice gy-warning">
                      <p>浏览器未允许复制，请手动复制：</p>
                      <textarea :value="link.url + (link.password ? '  提取码：' + link.password : '')" readonly aria-label="手动复制资源链接" @focus="($event.target as HTMLTextAreaElement).select()" />
                    </div>
                    <p v-if="copied === link.url" role="status" class="sr-only">链接{{ link.password ? '及提取码' : '' }}已复制</p>
                    <p v-if="cloudStatuses[cloudResourceID(item, link)]" role="status" class="gy-notice gy-task-status" :class="cloudStatuses[cloudResourceID(item, link)]?.success ? 'gy-success' : 'gy-error'">{{ cloudStatuses[cloudResourceID(item, link)]?.message }}<RouterLink v-if="cloudStatuses[cloudResourceID(item, link)]?.needsConfig" to="/115/cloud-download" @click="emit('close')">前往配置</RouterLink></p>
                    <p v-if="shareRuns.statuses.value[shareResourceID(item, link)]" role="status" class="gy-notice gy-task-status" :class="shareRuns.statuses.value[shareResourceID(item, link)]?.success ? 'gy-success' : 'gy-info'">{{ shareRuns.statuses.value[shareResourceID(item, link)]?.message }}</p>
                  </li>
                </ul>
              </article>
              <div v-if="displayedLinkCount < filteredLinkCount" class="gy-list-end">
                <button type="button" class="gy-button gy-outlined-button" @click="showMore">显示更多资源</button>
                <span>还有 {{ filteredLinkCount - displayedLinkCount }} 条链接，筛选包含全部结果</span>
              </div>
              <p v-else class="gy-all-loaded">已展示当前筛选的全部资源</p>
            </div>
          </template>
        </div>
        <footer class="gy-footer">115 分享可生成 STRM；磁力使用“115 云下载”中已保存的下载目录。</footer>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.gy-dialog {
  --gy-max-height: 90dvh;
  --gy-container: var(--search-dialog-container);
  --gy-card: var(--search-dialog-card);
  --gy-text: var(--search-dialog-text);
  --gy-muted: var(--search-dialog-muted);
  --gy-outline: var(--search-dialog-outline);
  --gy-primary: var(--search-dialog-primary);
  --gy-tonal: var(--search-dialog-tonal);
  --gy-on-tonal: var(--search-dialog-on-tonal);
  --gy-success: #246044; --gy-success-bg: #e6f2e9;
  --gy-warning: #765500; --gy-warning-bg: #fff1cd;
  --gy-error: #a12b2b; --gy-error-bg: #ffe9e7;
  margin: auto; padding: 0; width: min(1080px, calc(100% - 48px)); max-width: none; max-height: var(--gy-max-height);
  border: 0; border-radius: 28px; background: color-mix(in srgb, var(--gy-container) 94%, transparent);
  -webkit-backdrop-filter: blur(16px); backdrop-filter: blur(16px); color: var(--gy-text); color-scheme: light;
  overflow: hidden; box-shadow: 0 16px 48px rgb(0 0 0 / 20%);
}
.dark .gy-dialog {
  --gy-container: var(--search-dialog-container);
  --gy-card: var(--search-dialog-card);
  --gy-text: var(--search-dialog-text);
  --gy-muted: var(--search-dialog-muted);
  --gy-outline: var(--search-dialog-outline);
  --gy-primary: var(--search-dialog-primary);
  --gy-tonal: var(--search-dialog-tonal);
  --gy-on-tonal: var(--search-dialog-on-tonal);
  --gy-success: #a5d8b8; --gy-success-bg: #223d31;
  --gy-warning: #eed085; --gy-warning-bg: #42371e;
  --gy-error: #ffb4ab; --gy-error-bg: #502c2b;
  color-scheme: dark;
}
.gy-dialog::backdrop { background: rgb(2 6 23 / 32%); }
.gy-layout { display: flex; flex-direction: column; max-height: var(--gy-max-height); }
.gy-header { display: flex; flex-shrink: 0; align-items: center; gap: 14px; padding: 24px 24px 12px; }
.gy-brand { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border-radius: 14px; background: var(--gy-card); }
.gy-heading { flex: 1; min-width: 0; }
.gy-heading h2 { margin: 0; font-size: 20px; line-height: 28px; font-weight: 650; }
.gy-heading p { margin: 4px 0 0; color: var(--gy-muted); font-size: 13px; line-height: 20px; overflow-wrap: anywhere; }
.gy-heading-meta { display: flex; align-items: center; gap: 8px; }
.gy-heading-meta p { flex: 1; min-width: 0; }
.gy-search-caption { flex-shrink: 0; margin-top: 4px; color: var(--gy-muted); font-size: 10px; line-height: 18px; }
.gy-cached-caption { color: var(--gy-primary); }
.gy-icon-button { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border: 0; border-radius: 50%; background: transparent; color: var(--gy-muted); }
.gy-icon-button svg { width: 24px; height: 24px; }
.gy-tools { display: flex; flex-shrink: 0; justify-content: space-between; align-items: center; gap: 12px; padding: 0 24px 8px; }
.gy-tools > span { color: var(--gy-muted); font-size: 12px; }
.gy-tools > div { display: flex; gap: 4px; }
.gy-search-query { display: flex; align-items: center; flex: 1; min-width: 0; gap: 6px; }
.gy-query-method { flex-shrink: 0; color: var(--gy-primary); font-size: 10px; font-weight: 600; }
.gy-query-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
.gy-tool-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 48px; padding: 0 12px; border: 0; border-radius: 24px; background: transparent; color: var(--gy-muted); font-size: 12px; font-weight: 550; text-decoration: none; white-space: nowrap; }
.gy-tool-button svg { width: 18px; height: 18px; }
.gy-body { position: relative; min-height: 0; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.gy-search-process { margin-bottom: 12px; color: var(--gy-muted); font-size: 12px; }
.gy-process-heading { display: flex; align-items: center; gap: 8px; padding: 8px 0; font-size: 11px; line-height: 20px; }
.gy-cache { margin-left: auto; color: var(--gy-primary); font-size: 11px; }
.gy-search-process ul { display: grid; gap: 8px; margin: 0 0 12px; padding: 12px 14px; border-radius: 14px; background: var(--gy-card); list-style: none; }
.gy-search-process li { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; line-height: 20px; overflow-wrap: anywhere; }
.gy-search-process code { color: var(--gy-text); }
.gy-picker-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 4px 12px; margin-bottom: 10px; }
.gy-picker-heading h3 { margin: 0; font-size: 13px; line-height: 20px; font-weight: 600; }
.gy-picker-heading > span { font-size: 11px; color: var(--gy-muted); }
.gy-item-strip { position: relative; isolation: isolate; display: flex; gap: 10px; padding: 4px 2px 12px; overflow-x: auto; }
.gy-selection-indicator { position: absolute; z-index: 1; background: var(--search-dialog-action-background); pointer-events: none; }
.gy-item-indicator { border-radius: 16px; }
.gy-chip-indicator { border-radius: 10px; }
.gy-item-card { position: relative; display: flex; flex-shrink: 0; align-items: center; gap: 10px; width: 220px; min-height: 92px; padding: 8px; border: 2px solid transparent; border-radius: 16px; background: var(--gy-card); color: var(--gy-text); text-align: left; }
.gy-item-card > svg, .gy-item-card > span { position: relative; z-index: 2; }
.gy-item-card[aria-pressed="true"] { color: var(--gy-on-tonal); }
.gy-cover { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 70px; overflow: hidden; border-radius: 8px; background: color-mix(in srgb, var(--gy-outline) 20%, transparent); color: var(--gy-muted); }
.gy-cover img { width: 100%; height: 100%; object-fit: cover; }
.gy-cover svg { width: 24px; height: 24px; }
.gy-item-name { min-width: 0; padding-right: 4px; }
.gy-item-card strong { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; overflow: hidden; font-size: 12px; line-height: 18px; font-weight: 600; overflow-wrap: anywhere; }
.gy-item-card small { display: block; margin-top: 4px; color: var(--gy-muted); font-size: 10px; line-height: 16px; }
.gy-item-card small span { margin-left: 8px; }
.gy-all-items { width: 160px; padding: 12px; }
.gy-all-items > svg { width: 26px; height: 26px; flex-shrink: 0; }
.gy-filters { position: sticky; top: 0; z-index: 2; margin: 0 -24px 8px; padding: 2px 24px 4px; background: var(--gy-container); border-bottom: 1px solid var(--gy-outline); }
.gy-filter-context { display: flex; min-height: 28px; align-items: center; justify-content: space-between; gap: 8px; }
.gy-filter-context > span { flex-shrink: 0; font-size: 11px; line-height: 18px; color: var(--gy-muted); }
.gy-filter-title { margin: 0; font-size: 12px; line-height: 20px; font-weight: 600; }
.gy-short-viewport .gy-filters { position: static; }
.gy-source-strip { position: relative; isolation: isolate; display: flex; gap: 6px; padding: 2px; overflow-x: auto; }
.gy-chip { position: relative; display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; gap: 4px; min-width: 44px; min-height: 44px; padding: 6px 10px; border: 0; border-radius: 10px; background: transparent; color: var(--gy-muted); font-size: 11px; line-height: 18px; }
.gy-chip::before { position: absolute; z-index: 0; inset: 6px 0; border-radius: inherit; background: var(--gy-card); pointer-events: none; content: ''; }
.gy-chip > span { position: relative; z-index: 2; }
.gy-chip[aria-pressed="true"] { color: var(--gy-on-tonal); font-weight: 600; }
.gy-chip-count { opacity: .75; font-size: 10px; }
.gy-film + .gy-film { margin-top: 28px; }
.gy-film-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 12px; }
.gy-film-heading h3 { margin: 0; min-width: 0; font-size: 16px; line-height: 24px; font-weight: 650; overflow-wrap: anywhere; }
.gy-film-heading h3 > span { display: inline-block; margin-left: 8px; color: var(--gy-muted); font-size: 13px; font-weight: 400; }
.gy-film-heading > div { display: flex; align-items: center; gap: 8px; font-size: 11px; color: var(--gy-muted); }
.gy-film-details { margin: 4px 0 8px; color: var(--gy-muted); font-size: 11px; }
.gy-film-details summary { display: inline-flex; align-items: center; gap: 5px; min-height: 40px; }
.gy-film-details p { margin: 0 0 12px; font-size: 12px; line-height: 22px; overflow-wrap: anywhere; }
.gy-film-single .gy-film-details { margin: -8px 0 0; }
.gy-film-single .gy-resource-list { margin-top: 0; }
.gy-resource-list { display: grid; gap: 10px; margin: 12px 0 0; padding: 0; list-style: none; }
.gy-resource { min-width: 0; padding: 16px 20px; border: 1px solid color-mix(in srgb, var(--gy-outline) 35%, transparent); border-radius: 20px; background: var(--gy-card); }
.gy-resource-main { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 10px 20px; }
.gy-resource-info { display: contents; }
.gy-resource-title { grid-column: 1 / -1; min-width: 0; line-height: 23px; }
.gy-resource-info h4 { display: inline; margin: 0; font-size: 14px; line-height: 23px; font-weight: 600; overflow-wrap: anywhere; }
.gy-resource-meta { display: flex; grid-column: 1; grid-row: 2; min-width: 0; align-items: center; gap: 8px; color: var(--gy-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.gy-resource-tags { display: flex; flex: 1; min-width: 0; flex-wrap: wrap; align-items: center; gap: 6px 12px; }
.gy-badge { display: inline-flex; align-items: center; padding: 3px 8px; border-radius: 8px; font-size: 11px; line-height: 18px; font-weight: 550; }
.gy-neutral { background: color-mix(in srgb, var(--gy-outline) 18%, transparent); color: var(--gy-muted); }
.gy-info { color: var(--gy-on-tonal); background: var(--gy-tonal); }
.gy-success { color: var(--gy-success); background: var(--gy-success-bg); }
.gy-warning { color: var(--gy-warning); background: var(--gy-warning-bg); }
.gy-error { color: var(--gy-error); background: var(--gy-error-bg); }
.gy-actions { display: flex; grid-column: 2; grid-row: 2; align-items: center; gap: 8px; }
.gy-copy-button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 40px; margin-left: 8px; padding: 6px 10px; border: 0; border-radius: 20px; background: transparent; color: var(--gy-primary); font-size: 11px; vertical-align: middle; }
.gy-copy-button svg { width: 14px; height: 14px; }
.gy-notice { margin: 0 0 14px; padding: 12px 14px; border-radius: 12px; font-size: 12px; line-height: 22px; overflow-wrap: anywhere; }
.gy-notice.gy-info { border: 1px solid color-mix(in srgb, var(--gy-outline) 45%, transparent); background: var(--gy-card); color: var(--gy-text); }
.gy-notice.gy-info > span, .gy-task-notice.gy-info > div > span { color: var(--gy-muted); }
.gy-notice > span, .gy-task-notice > div > span { display: block; font-size: 11px; }
.gy-notice strong { font-weight: 600; }
.gy-notice p { margin: 0; }
.gy-task-notice { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; }
.gy-resource .gy-notice { margin: 8px 0 8px; }
.gy-task-status { white-space: pre-wrap; }
.gy-task-status a { margin-left: 8px; color: inherit; text-decoration: underline; text-underline-offset: 3px; }
.gy-notice textarea { display: block; width: 100%; margin-top: 8px; padding: 8px; border: 1px solid var(--gy-outline); border-radius: 8px; background: var(--gy-card); color: var(--gy-text); font-size: 16px; }
.gy-text-button { min-height: 48px; padding: 10px 12px; border: 0; border-radius: 24px; background: transparent; color: var(--gy-primary); font-size: 12px; line-height: 20px; font-weight: 600; }
.gy-list-end { display: flex; flex-direction: column; align-items: center; gap: 8px; margin-top: 20px; }
.gy-list-end > span, .gy-all-loaded { color: var(--gy-muted); font-size: 11px; line-height: 18px; }
.gy-all-loaded { margin: 20px 0 0; text-align: center; }
.gy-footer { flex-shrink: 0; padding: 12px 24px 16px; color: var(--gy-muted); font-size: 11px; line-height: 18px; }
.gy-empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 36px 16px; color: var(--gy-muted); text-align: center; }
.gy-empty svg { width: 36px; height: 36px; margin-bottom: 8px; }
.gy-empty h3 { margin: 0; font-size: 14px; line-height: 22px; color: var(--gy-text); font-weight: 600; }
.gy-empty p { margin: 0; font-size: 12px; line-height: 22px; }
.gy-empty > span { font-size: 11px; line-height: 18px; }
.gy-setup { padding: 24px; border-radius: 20px; background: var(--gy-card); }
.gy-setup h3 { margin: 0; font-size: 15px; font-weight: 600; }
.gy-setup p { margin: 10px 0 16px; color: var(--gy-muted); font-size: 13px; line-height: 22px; }
.gy-dialog summary { list-style: none; cursor: pointer; }
.gy-dialog summary::-webkit-details-marker { display: none; }
.gy-chevron { width: 14px; height: 14px; flex-shrink: 0; }
details[open] > summary > .gy-chevron { transform: rotate(90deg); }
.gy-dialog button { cursor: pointer; }
.gy-dialog button:disabled { cursor: default; opacity: .45; }
.gy-dialog button:focus-visible, .gy-dialog a:focus-visible, .gy-dialog summary:focus-visible { outline: 2px solid var(--gy-primary); outline-offset: 3px; }
.gy-scroll { scrollbar-color: var(--gy-outline) transparent; scrollbar-width: thin; }
.gy-scroll::-webkit-scrollbar { height: 4px; }
.gy-scroll::-webkit-scrollbar-thumb { border-radius: 999px; background: var(--gy-outline); }
.gy-spinning { animation: gy-spin 1s linear infinite; }
@keyframes gy-spin { to { transform: rotate(360deg); } }
@media (hover: hover) {
  .gy-icon-button:hover, .gy-tool-button:hover:not(:disabled), .gy-copy-button:hover, .gy-text-button:hover:not(:disabled) { background: color-mix(in srgb, var(--gy-primary) 8%, transparent); }
  .gy-item-card:hover:not([aria-pressed="true"]), .gy-chip:hover:not([aria-pressed="true"])::before { background: var(--search-dialog-secondary-hover); }
  .gy-item-strip:has(.gy-item-card[aria-pressed="true"]:hover) > .gy-selection-indicator,
  .gy-source-strip:has(.gy-chip[aria-pressed="true"]:hover) > .gy-selection-indicator { background: var(--search-dialog-action-hover); }
}
@media (max-width: 799px) {
  .gy-resource-main { grid-template-columns: minmax(0, 1fr); gap: 14px; }
  .gy-resource-info { display: grid; grid-column: 1; grid-row: 1; grid-template-columns: minmax(0, 1fr) auto; align-items: center; column-gap: 8px; min-width: 0; }
  .gy-resource-title { display: contents; }
  .gy-resource-info h4 { display: block; grid-column: 1 / -1; }
  .gy-resource-meta { margin-top: 10px; }
  .gy-copy-button { grid-column: 2; grid-row: 2; margin: 10px 0 0; }
  .gy-actions { grid-column: 1; }
}
@media (max-width: 639px) {
  .gy-dialog { --gy-max-height: calc(min(100dvh, var(--gy-visible-height, 100dvh)) - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 24px); inset-block: calc(var(--gy-visible-top, 0px) + env(safe-area-inset-top)) calc(var(--gy-visible-bottom, 0px) + env(safe-area-inset-bottom)); width: calc(100% - 24px); border-radius: 24px; }
  .gy-header { gap: 10px; padding: 16px 16px 8px; }
  .gy-heading h2 { font-size: 18px; line-height: 26px; }
  .gy-heading p { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; font-size: 12px; line-height: 18px; }
  .gy-tools { justify-content: flex-end; padding: 0 16px 4px; }
  .gy-tools > span { display: none; }
  .gy-tools > .gy-search-query { display: flex; }
  .gy-tool-button { padding-inline: 8px; }
  .gy-body { padding: 0 16px 16px; scrollbar-gutter: auto; }
  .gy-item-picker { margin-bottom: 8px; }
  .gy-filters { margin: 0 -16px 8px; padding: 2px 16px 4px; }
  .gy-picker-heading { flex-wrap: nowrap; justify-content: flex-start; gap: 8px; margin-bottom: 8px; }
  .gy-picker-heading h3, .gy-picker-heading > span { flex-shrink: 0; }
  .gy-picker-heading > span { font-size: 10px; }
  .gy-picker-hint { display: none; }
  .gy-item-strip { padding-top: 0; }
  .gy-item-card { width: 176px; min-height: 80px; gap: 8px; }
  .gy-cover { width: 40px; height: 58px; }
  .gy-all-items { flex-direction: column; justify-content: center; gap: 4px; width: 112px; padding: 8px; text-align: center; }
  .gy-all-items > svg { width: 20px; height: 20px; }
  .gy-all-items small { margin-top: 0; }
  .gy-filter-context > span { font-size: 10px; }
  .gy-film-heading h3 { font-size: 15px; }
  .gy-film-details summary { min-height: 48px; }
  .gy-resource { padding: 16px; border-radius: 18px; }
  .gy-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .gy-actions > :only-child { grid-column: 1 / -1; }
  .gy-copy-button { min-height: 48px; }
  .gy-footer { padding: 8px 16px 12px; font-size: 10px; line-height: 16px; }
}
@media (max-width: 359px) { .gy-actions { grid-template-columns: minmax(0, 1fr); } }
@media (prefers-reduced-motion: reduce) {
  .gy-selection-indicator { transition: none !important; }
  .gy-spinning { animation: none; }
}
@media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
  .gy-dialog { background: var(--gy-container); -webkit-backdrop-filter: none; backdrop-filter: none; }
}
</style>
