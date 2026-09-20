<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import { gyingRequest, type GyingConfigView, type GyingResourceLink, type GyingSearchAttempt, type GyingSearchItem, type GyingSearchResponse } from '../types/gying'
import { normalize115Share } from '../utils/shareLink'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; originalTitle: string; imdbId: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
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
const resourceColors: Record<string, string> = {
  magnet: '#94a3b8', aliyun: '#60a5fa', quark: '#c084fc', baidu: '#38bdf8',
  xunlei: '#818cf8', mobile: '#2dd4bf', '115': '#f97316', '123': '#22c55e',
  guangya: '#ec4899', uc: '#f59e0b', tianyi: '#ef4444', other: '#94a3b8',
}
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
    if (!config.accountId) {
      throw Object.assign(new Error('请先在“115 云下载”页面选择账号并保存配置'), { needsConfig: true })
    }
    const receipt = await submitCloudDownload(props.token, config.accountId, link.url, config.targetCID || '0', lifecycle.signal)
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
const visibleItems = computed(() => {
  return selectedItems.value.flatMap(item => {
    const resourceLinks = activeResourceType.value === 'all' ? item.links : item.links.filter(link => link.type === activeResourceType.value)
    const links = sortResourceLinks(resourceLinks.filter(link => activeResourceType.value !== 'magnet' || activeMagnetSubType.value === 'all' || magnetSubType(link) === activeMagnetSubType.value))
    return links.length ? [{ ...item, links }] : []
  })
})
const searchAttempts = computed(() => result.value?.attempts || [])
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
}

function selectResourceType(type: string) {
  activeResourceType.value = type
  activeMagnetSubType.value = 'all'
}

async function search(refresh = false) {
  if (busy.value) return
  beginPhase('search'); error.value = ''; copied.value = ''
  try {
    result.value = await gyingRequest<GyingSearchResponse>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: props.tmdbId, refresh })
    coverErrors.value = {}
    activeItemId.value = 'all'; activeResourceType.value = 'all'; activeMagnetSubType.value = 'all'
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

function linkCount(item: GyingSearchItem, category: 'pan' | 'torrent') { return item.links.filter(link => link.category === category).length }

onMounted(async () => {
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
  lifecycle.abort()
  finishPhase()
  if (dialog.value?.open) dialog.value.close()
  document.body.style.overflow = previousOverflow
  previousFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="gying-search-title" class="m-auto max-h-[90dvh] w-[calc(100%-2rem)] max-w-5xl overflow-hidden rounded-2xl border border-slate-200 bg-white p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="emit('close')">
      <div class="flex max-h-[90dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 p-5"><div class="min-w-0"><h2 id="gying-search-title" class="text-lg font-semibold text-slate-900">观影搜索</h2><p class="mt-1 break-words text-sm text-slate-500">{{ title }}<span v-if="originalTitle && originalTitle !== title"> · {{ originalTitle }}</span><span v-if="imdbId"> · {{ imdbId }}</span></p></div><button autofocus type="button" aria-label="关闭观影搜索" class="rounded-lg px-2 py-1 text-xl text-slate-400 hover:bg-slate-100" @click="emit('close')">×</button></header>
        <div class="min-h-0 overflow-y-auto p-5">
          <div v-if="config && !config.configured" class="rounded-xl bg-slate-50 p-6 text-sm leading-7 text-slate-600"><p>请先配置并登录观影站账号。</p><RouterLink to="/gying" class="mt-3 inline-block font-medium text-blue-600" @click="emit('close')">前往观影搜索配置 →</RouterLink></div>
          <p v-if="phase === 'config'" role="status" class="rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">正在读取观影搜索配置…<span v-if="elapsedSeconds >= 10" class="block text-xs">已等待 {{ elapsedSeconds }} 秒，请检查后端连接状态。</span></p>
          <p v-else-if="phase === 'search'" role="status" class="rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">正在依次搜索影视原名并读取资源详情…<span class="block text-xs"><template v-if="elapsedSeconds < 10">原名没有有效资源时会自动尝试 IMDb 编号；通常数秒，匹配条目较多时会更久。</template><template v-else>正在读取较多资源详情，已等待 {{ elapsedSeconds }} 秒；最多等待约 2 分钟。</template></span></p>
          <p v-if="shareRuns.busy.value" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">{{ shareRuns.activeRunID.value ? 'STRM 任务正在后台执行，可继续选择其他资源；关闭页面不影响已提交任务。' : '正在准备并提交 STRM 任务…' }}<button v-if="shareRuns.activeRunID.value" type="button" :disabled="shareRuns.stopping.value" class="ml-3 underline disabled:opacity-50" @click="stopShareRun">{{ shareRuns.stopping.value ? '正在停止…' : '停止已提交批次' }}</button></p>
          <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <template v-if="result">
            <section class="mb-4 rounded-xl bg-slate-50 p-4 text-sm text-slate-600" aria-label="搜索过程">
              <div class="flex flex-wrap items-center justify-between gap-2"><strong class="font-medium text-slate-700">搜索过程</strong><span v-if="result.cached" class="text-xs text-emerald-700">已使用短期缓存结果，可重新搜索更新</span></div>
              <ul class="mt-2 space-y-1.5 text-xs leading-5"><li v-for="attempt in searchAttempts" :key="`${attempt.method}:${attempt.query}`" class="flex flex-wrap gap-x-2"><span class="font-medium" :class="attempt.method === 'imdb' ? 'text-violet-700' : 'text-blue-700'">{{ attempt.method === 'imdb' ? 'IMDb' : '原名' }}</span><code class="break-all text-slate-700">{{ attempt.query }}</code><span class="text-slate-500">{{ attemptDescription(attempt) }}</span></li></ul>
            </section>
            <p v-if="result.partial" role="status" class="mb-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-700">有 {{ result.failedDetails }} 个资源详情读取失败，当前展示其余成功结果；本次不写入搜索缓存。</p>
            <p v-for="attempt in truncatedAttempts" :key="`truncated:${attempt.method}`" role="status" class="mb-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-700">{{ attempt.method === 'imdb' ? 'IMDb' : '原名' }}搜索返回 {{ attempt.total }} 个条目，本次为保护服务稳定性仅读取前 100 个。</p>
            <section v-if="resultItems.length" class="mb-5 overflow-hidden rounded-xl border border-slate-200 bg-white" aria-label="观影结果筛选">
              <div class="flex items-center justify-between gap-3 px-3 pt-3">
                <span class="text-xs text-slate-500">选择影视条目</span>
                <button type="button" :aria-pressed="activeItemId === 'all'" class="rounded-lg border px-3 py-1.5 text-xs font-medium transition" :class="activeItemId === 'all' ? 'border-amber-400 bg-amber-50 text-amber-800' : 'border-slate-200 text-slate-600 hover:border-slate-300'" @click="selectItem('all')">全部条目 <span class="opacity-60">{{ resultItems.length }}</span></button>
              </div>
              <nav aria-label="影视条目" class="filter-scroll flex gap-3 overflow-x-auto p-3">
                <button v-for="item in resultItems" :key="item.id" type="button" :aria-label="`${item.title}${item.year ? ` (${item.year})` : ''}`" :aria-pressed="activeItemId === item.id" class="relative aspect-[5/7] w-28 shrink-0 overflow-hidden rounded-xl border-2 bg-slate-100 text-left transition focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-amber-500 sm:w-32" :class="activeItemId === item.id ? 'border-amber-500 ring-2 ring-amber-200' : 'border-transparent hover:border-amber-300'" :title="`${item.title}${item.year ? ` (${item.year})` : ''}`" @click="selectItem(item.id)">
                  <img v-if="coverSource(item) && !coverErrors[item.id]" :src="coverSource(item)" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" class="absolute inset-0 h-full w-full object-cover" @error="coverErrors[item.id] = true">
                  <span v-else class="absolute inset-0 flex items-center justify-center pb-12 text-xs text-slate-400">{{ coverErrors[item.id] ? '封面暂不可用' : '暂无封面' }}</span>
                  <span class="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/65 to-transparent px-2 pb-2 pt-10 text-white"><span class="line-clamp-3 text-xs font-medium leading-5">{{ item.title }}</span><span v-if="item.year" class="mt-0.5 block text-[11px] text-white/75">{{ item.year }}</span></span>
                  <span v-if="activeItemId === item.id" aria-hidden="true" class="absolute right-1.5 top-1.5 flex h-5 w-5 items-center justify-center rounded-full bg-amber-500 text-xs text-white">✓</span>
                </button>
              </nav>
              <nav aria-label="资源网盘" class="filter-scroll mt-1 flex gap-2 overflow-x-auto border-t border-slate-100 bg-slate-50/80 p-3">
                <button v-for="option in resourceOptions" :key="option.type" type="button" class="inline-flex shrink-0 items-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition" :class="activeResourceType === option.type ? 'border-slate-800 bg-slate-800 text-white' : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300'" @click="selectResourceType(option.type)">
                  <span v-if="option.type !== 'all'" class="h-2 w-2 rounded-full" :style="{ backgroundColor: resourceColors[option.type] || resourceColors.other }" aria-hidden="true"></span>{{ option.label }} <span class="opacity-60">{{ option.count }}</span>
                </button>
              </nav>
              <nav v-if="activeResourceType === 'magnet' && magnetSubTypeOptions.length > 1" aria-label="磁力清晰度分类" class="filter-scroll flex gap-2 overflow-x-auto border-t border-slate-100 bg-white p-3">
                <button v-for="option in magnetSubTypeOptions" :key="option.type" type="button" :aria-pressed="activeMagnetSubType === option.type" class="inline-flex shrink-0 items-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition" :class="activeMagnetSubType === option.type ? 'border-violet-500 bg-violet-50 text-violet-700' : 'border-slate-200 bg-white text-slate-600 hover:border-violet-300 hover:text-violet-700'" @click="activeMagnetSubType = option.type">
                  {{ option.label }} <span class="opacity-60">{{ option.count }}</span>
                </button>
              </nav>
            </section>
            <div v-if="!resultItems.length" class="py-10 text-center"><p class="font-medium text-slate-700">暂未找到可用资源</p><p class="mt-2 text-sm leading-6 text-slate-500">{{ emptySummary }}</p><p v-if="!result.partial" class="mt-1 text-xs text-slate-400">空结果缓存 2 分钟，也可以立即重新搜索。</p></div>
            <p v-else-if="!visibleItems.length" class="py-12 text-center text-sm text-slate-500">当前分类没有资源。</p>
            <div v-else class="space-y-4">
              <article v-for="item in visibleItems" :key="item.id" class="rounded-xl border border-slate-200 p-4 sm:p-5">
                <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><h3 class="break-words font-semibold leading-6 text-slate-900">{{ item.title }} <span v-if="item.year" class="font-normal text-slate-400">({{ item.year }})</span></h3><div class="mt-2 flex flex-wrap gap-2 text-[11px]"><span class="rounded bg-amber-50 px-2 py-1 text-amber-700">{{ mediaLabels[item.mediaType] }}</span><span v-if="linkCount(item, 'pan')" class="rounded bg-blue-50 px-2 py-1 text-blue-700">网盘 {{ linkCount(item, 'pan') }}</span><span v-if="linkCount(item, 'torrent')" class="rounded bg-violet-50 px-2 py-1 text-violet-700">种子 {{ linkCount(item, 'torrent') }}</span></div></div></div>
                <p v-if="item.description" class="mt-3 break-words text-xs leading-6 text-slate-500">{{ item.description }}</p>
                <ul class="mt-4 space-y-2"><li v-for="(link, index) in item.links" :key="`${link.type}:${link.url}:${index}`" class="rounded-lg bg-slate-50 p-3 text-xs"><div class="flex flex-wrap items-center gap-2"><span class="rounded bg-white px-2 py-1 font-medium text-slate-700 ring-1 ring-slate-200">{{ link.typeLabel }}</span><span v-if="link.subTypeLabel" class="rounded bg-violet-50 px-2 py-1 font-medium text-violet-700">{{ link.subTypeLabel }}</span><span v-if="link.size" class="text-slate-400">{{ link.size }}</span><span v-if="link.seeds !== undefined" class="text-slate-400">做种 {{ link.seeds }}</span><span v-if="link.updatedAt" class="text-slate-400">{{ link.updatedAt }}</span><span v-if="link.password" class="text-amber-700">提取码 {{ link.password }}</span><span class="ml-auto flex flex-wrap gap-3"><button v-if="canCloudDownload(link)" type="button" :disabled="phase !== '' || cloudSubmitting[cloudResourceID(item, link)] || cloudStatuses[cloudResourceID(item, link)]?.success" class="font-medium text-orange-600 hover:text-orange-700 disabled:opacity-50" @click="cloudDownload(item, link)">{{ cloudSubmitting[cloudResourceID(item, link)] ? '提交中…' : cloudStatuses[cloudResourceID(item, link)]?.success ? '已提交' : '115 转存' }}</button><button v-if="canShareSTRM(link)" type="button" :disabled="phase !== '' || shareRuns.pending.value[shareResourceID(item, link)] || shareRuns.statuses.value[shareResourceID(item, link)]?.success" class="font-medium text-emerald-700 hover:text-emerald-800 disabled:opacity-50" @click="shareSTRM(item, link)">{{ shareRuns.pending.value[shareResourceID(item, link)] ? '已加入任务…' : shareRuns.statuses.value[shareResourceID(item, link)]?.success ? '已生成' : '分享 STRM →' }}</button><button type="button" class="text-blue-600" @click="copyLink(link)">{{ copied === link.url ? '已复制' : '复制' }}</button><a :href="link.url" target="_blank" rel="noopener noreferrer" class="text-blue-600">打开 ↗</a></span></div><div v-if="copyError === link.url" role="alert" class="mt-2 text-amber-700"><p>浏览器未允许复制，请手动复制下方链接：</p><textarea :value="`${link.url}${link.password ? `  提取码：${link.password}` : ''}`" readonly aria-label="手动复制资源链接" class="mt-2 w-full rounded border border-amber-200 bg-white p-2 text-slate-700" @focus="($event.target as HTMLTextAreaElement).select()" /></div><p v-if="link.workTitle" class="mt-2 break-words text-slate-600">{{ link.workTitle }}</p><p class="mt-2 break-all text-slate-400">{{ link.url }}</p><p v-if="cloudStatuses[cloudResourceID(item, link)]" role="status" class="mt-3 whitespace-pre-wrap break-words leading-6" :class="cloudStatuses[cloudResourceID(item, link)]?.success ? 'text-emerald-600' : 'text-red-600'">{{ cloudStatuses[cloudResourceID(item, link)]?.message }}<RouterLink v-if="cloudStatuses[cloudResourceID(item, link)]?.needsConfig" to="/115/cloud-download" class="ml-2 text-blue-600 hover:underline" @click="emit('close')">前往配置</RouterLink></p><p v-if="shareRuns.statuses.value[shareResourceID(item, link)]" role="status" class="mt-3 whitespace-pre-wrap break-words leading-6" :class="shareRuns.statuses.value[shareResourceID(item, link)]?.success ? 'text-emerald-600' : 'text-slate-600'">{{ shareRuns.statuses.value[shareResourceID(item, link)]?.message }}</p></li></ul>
              </article>
            </div>
          </template>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-slate-100 p-5"><p class="text-xs text-slate-500">115 分享资源可复用“115分享STRM”的账号；磁力可直接提交到“115 云下载”配置的账号和目录。</p><button v-if="config?.configured" :disabled="busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="search(true)">{{ phase === 'search' ? `搜索中（${elapsedSeconds} 秒）` : '重新搜索' }}</button><button v-else-if="!config && error" :disabled="busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="initialize">{{ phase === 'config' ? '读取中…' : '重试' }}</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.filter-scroll {
  scrollbar-color: #cbd5e1 transparent;
  scrollbar-width: thin;
}

.filter-scroll::-webkit-scrollbar {
  height: 4px;
}

.filter-scroll::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: #cbd5e1;
}
</style>
