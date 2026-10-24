<script setup lang="ts">
import { catalogAPI, isPortalAdmin } from '../composables/usePortalAuth'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import BotSearchDialog from '../components/BotSearchDialog.vue'
import GyingSearchDialog from '../components/GyingSearchDialog.vue'
import WoniuSearchDialog from '../components/WoniuSearchDialog.vue'
import AnimeResourceSearchDialog from '../components/AnimeResourceSearchDialog.vue'
import RE0SearchDialog from '../components/RE0SearchDialog.vue'
import FollowButton from '../components/FollowButton.vue'
import SearchPill from '../components/SearchPill.vue'
import MediaRecommendations from '../components/MediaRecommendations.vue'
import EmbyMediaInfo from '../components/EmbyMediaInfo.vue'
import SeasonEpisodeList from '../components/SeasonEpisodeList.vue'
import SeasonLibraryProgress from '../components/SeasonLibraryProgress.vue'
import TMDBEpisodeGroupDialog from '../components/TMDBEpisodeGroupDialog.vue'
import { numberingLabel, type NumberingInfo } from '../types/tvNumbering'
import InfiniteLoadMore from '../components/InfiniteLoadMore.vue'
import HistoryBackButton from '../components/HistoryBackButton.vue'
import { useEmbyMedia } from '../composables/useEmbyMedia'
import { useMediaColors } from '../composables/useMediaColors'
import { useTMDBSeasons, type CacheInfo } from '../composables/useTMDBSeasons'
import { provideEmbyVersionCache } from '../composables/useEmbyVersionCache'
import { formatEpisodeRanges, localDateKey, seasonLibraryProgress } from '../types/seasonLibrary'
import { copyText } from '../utils/clipboard'
import { formatScrapeName } from '../utils/mediaNaming'
import { tmdbImageURL } from '../utils/tmdbImage'
import tmdbLogo from '../assets/tmdb.svg'
import mikanMascot from '../assets/mikan/mikan-pic.svg?no-inline'
import gardenLogo from '../assets/anime-providers/garden.svg'
import re0Logo from '../assets/re0/logo-re0-square.webp'
import gyingLogo from '../assets/gying/logo-gying.png'
import woniuLogo from '../assets/woniu.svg'
import telegramIcon from '../assets/telegram/telegram.svg'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'
import { gyingRequest, type GyingConfigView } from '../types/gying'
import { woniuRequest, type WoniuConfigView } from '../types/woniu'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const versionCache = provideEmbyVersionCache()
const botSearchOpen = ref(false)
const gyingSearchOpen = ref(false)
const woniuSearchOpen = ref(false)
const mikanSearchOpen = ref(false)
const gardenSearchOpen = ref(false)
const re0SearchOpen = ref(false)
const numberingOpen = ref(false)
const numberingNotice = ref('')
const searchConfigured = ref({ re0: false, bot: false, gying: false, woniu: false })
watch(() => [props.token, isPortalAdmin.value] as const, ([token, admin], _, onCleanup) => {
  searchConfigured.value = { re0: false, bot: false, gying: false, woniu: false }
  re0SearchOpen.value = false; botSearchOpen.value = false; gyingSearchOpen.value = false
  woniuSearchOpen.value = false
  mikanSearchOpen.value = false; gardenSearchOpen.value = false
  if (!admin || !token) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(10000)])
  async function check(key: keyof typeof searchConfigured.value, request: Promise<{ configured: boolean }>) {
    try {
      const config = await request
      if (!signal.aborted) searchConfigured.value[key] = config.configured === true
    } catch {
      // 无法确认配置时隐藏对应入口，不影响其他搜索服务。
    }
  }
  void check('re0', nanShareRequest<NanShareConfigView>(token, 'config', signal))
  void check('bot', telegramUserRequest<TelegramUserStatus>(token, 'config', signal))
  void check('gying', gyingRequest<GyingConfigView>(token, 'config', signal))
  void check('woniu', woniuRequest<WoniuConfigView>(token, 'config', signal))
}, { immediate: true })
const route = useRoute(), router = useRouter()
watch(() => [props.kind, props.tmdbId], () => { botSearchOpen.value = false; gyingSearchOpen.value = false; woniuSearchOpen.value = false; mikanSearchOpen.value = false; gardenSearchOpen.value = false; re0SearchOpen.value = false })
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
const { media: embyMedia, episodes: embyEpisodes, error: embyError, refresh: refreshEmbyMedia, lastReadSucceeded: embyReadSucceeded } = useEmbyMedia(() => props.token, () => props.kind, () => props.tmdbId)
watch(() => `${embyMedia.value?.state || ''}|${embyMedia.value?.items.map(item => [item.id, item.season, item.episode, item.endEpisode].join(':')).sort().join('|') || ''}`, () => versionCache.clear())
interface CastMember { id: number; name: string; character: string; profileUrl: string }
interface SeasonSummary { id: number; number: number; name: string; episodeCount: number; date: string }
interface Detail extends CacheInfo {
  numbering?: NumberingInfo
  id: number; kind: 'movie' | 'tv'; title: string; originalTitle: string; overview: string; posterUrl: string
  backdropUrl: string; date: string; rating: number; votes: number; tagline: string; status: string; runtime: number
  episodeCount: number; genres: { id: number; name: string }[]; countries: string[]; cast: CastMember[]
  seasons: SeasonSummary[]; collection: { id: number; name: string } | null; imdbId: string; imdbUrl: string; tmdbUrl: string
}
const detail = ref<Detail | null>(null)
const { mediaColorStyle, readPosterColors } = useMediaColors(() => tmdbImageURL(detail.value?.posterUrl || ''))
// 动画类型包含日漫、国漫及动画电影，不按国家限制入口。
const canSearchAnimeResources = computed(() => isPortalAdmin.value && !!detail.value?.genres?.some(genre => genre.id === 16))
watch(canSearchAnimeResources, allowed => { if (!allowed) { mikanSearchOpen.value = false; gardenSearchOpen.value = false } })
const loading = ref(true)
const error = ref('')
const failedImages = ref(new Set<string>())
const openSeasons = ref(new Set<number>())
const visitedSeasons = ref(new Set<number>())
const { seasons, schedules, reset: resetSeasons, prime: primeSchedules, loadSeason, loadSchedule } = useTMDBSeasons(() => props.token, () => props.tmdbId, () => detail.value?.numbering?.revision || 0)
const episodeLimits = ref<Record<number, number>>({})
const metadataRefreshing = ref(false)
const metadataError = ref('')
const namingCopied = ref(false)
const namingCopyError = ref(false)
let namingNoticeTimer: number | undefined
const metadataBusy = computed(() => metadataRefreshing.value || Object.values(schedules.value).some(state => state.loading) || Object.values(seasons.value).some(state => state.loading))
const today = ref(localDateKey())
const dateTimer = window.setInterval(() => { today.value = localDateKey() }, 60000)
const seasonProgress = computed(() => Object.fromEntries((detail.value?.seasons || []).flatMap(season => {
  if (seasonState(season.number) !== 'present') return []
  const episodes = schedules.value[season.number]?.data?.episodes || seasons.value[season.number]?.data?.episodes
  return episodes ? [[season.number, seasonLibraryProgress(episodes, embyEpisodes.value[season.number] || new Set<number>(), season.episodeCount, today.value)]] : []
})))
const reliableLibrary = computed(() => embyMedia.value?.state === 'present' || embyMedia.value?.state === 'absent')
const hasIndexedEpisodes = computed(() => embyMedia.value?.state === 'present' && Object.values(embyEpisodes.value).some(episodes => episodes.size > 0))
// 先用本地入库集号判断该季是否有媒体，无媒体时不读取或计算播出进度。
function seasonState(number: number) {
  if (!reliableLibrary.value) return embyMedia.value?.state
  return embyMedia.value?.state === 'present' && embyEpisodes.value[number]?.size ? 'present' : 'absent'
}
function loadSeasonSchedule(number: number, force = false) {
  if (seasonState(number) === 'present') void loadSchedule(number, force)
}
const mediaLabel = computed(() => props.kind === 'movie' ? '电影' : '剧集')
const statuses: Record<string, string> = { Released: '已上映', 'In Production': '制作中', 'Post Production': '后期制作', Planned: '计划中', Rumored: '待确认', Canceled: '已取消', 'Returning Series': '连载中', Ended: '已完结', Pilot: '试播' }
const statusLabel = computed(() => detail.value ? statuses[detail.value.status] ?? detail.value.status : '')
let detailController: AbortController | undefined
// 页面保持打开时也能获取全局预热后的新播出数据，仅查询本地缓存。
const scheduleTimer = window.setInterval(() => {
  if (!document.hidden && props.kind === 'tv' && hasIndexedEpisodes.value && detail.value && detailController && !metadataBusy.value) void primeSchedules(detailController.signal)
}, 300000)
const previousTitle = document.title
const scrapeName = computed(() => formatScrapeName(detail.value?.title || '', detail.value?.date || '', props.tmdbId))
async function copyScrapeName() {
  if (!detail.value) return
  namingCopied.value = false
  namingCopyError.value = false
  try {
    await copyText(scrapeName.value)
    namingCopied.value = true
  } catch {
    namingCopyError.value = true
  }
  window.clearTimeout(namingNoticeTimer)
  namingNoticeTimer = window.setTimeout(() => { namingCopied.value = false; namingCopyError.value = false }, 2400)
}

async function request(path: string, signal: AbortSignal) {
  const response = await fetch(catalogAPI(`tmdb/${path}`), { signal, headers: { Authorization: `Bearer ${props.token}` } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error ?? `加载失败（HTTP ${response.status}）`)
  return data
}
function cancelRequests() {
  detailController?.abort()
  resetSeasons()
}
async function loadDetail() {
  versionCache.clear()
  cancelRequests()
  const current = new AbortController(); detailController = current
  detail.value = null; error.value = ''; loading.value = true
  openSeasons.value = new Set(); visitedSeasons.value = new Set(); failedImages.value = new Set(); episodeLimits.value = {}
  metadataRefreshing.value = false; metadataError.value = ''
  document.title = `${mediaLabel.value}详情 · Cinema115`
  if (!/^[1-9]\d*$/.test(props.tmdbId) || !Number.isSafeInteger(Number(props.tmdbId))) {
    error.value = 'TMDB ID 无效，请从看板选择影视'; loading.value = false; return
  }
  try {
    const data = await request(`detail/${props.kind}/${props.tmdbId}`, current.signal)
    if (current.signal.aborted) return
    if (data.id !== Number(props.tmdbId) || data.kind !== props.kind || !Array.isArray(data.cast) || !Array.isArray(data.seasons)) throw new Error('影视详情数据格式不正确')
    detail.value = data
    document.title = `${data.title} · Cinema115`
    if (data.cacheStale) void refreshDetail()
  } catch (e) {
    if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '详情加载失败'
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}
async function refreshDetail() {
  const current = detailController
  if (!current || metadataRefreshing.value) return
  metadataRefreshing.value = true; metadataError.value = ''
  try {
    const data = await request(`detail/${props.kind}/${props.tmdbId}?refresh=1`, AbortSignal.any([current.signal, AbortSignal.timeout(25000)]))
    if (current.signal.aborted) return
    if (data.id !== Number(props.tmdbId) || data.kind !== props.kind || !Array.isArray(data.seasons)) throw new Error('影视详情数据格式不正确')
    detail.value = data
    document.title = `${data.title} · Cinema115`
  } catch (e) {
    if (!current.signal.aborted) metadataError.value = e instanceof Error ? e.message : '元数据刷新失败，保留缓存'
  } finally {
    if (!current.signal.aborted) metadataRefreshing.value = false
  }
}
function refreshMetadata() {
  versionCache.clear()
  void refreshDetail()
  for (const season of detail.value?.seasons || []) {
    loadSeasonSchedule(season.number, true)
    if (visitedSeasons.value.has(season.number)) void loadSeason(season.number, true)
  }
}
async function numberingSaved(value: NumberingInfo) {
  numberingOpen.value = false
  numberingNotice.value = `已保存：${numberingLabel(value)}。追更状态正在重新计算。`
  await loadDetail()
}
watch(() => [props.kind, props.tmdbId, props.token], () => { numberingOpen.value = false; numberingNotice.value = '' })
watch(() => detail.value?.numbering?.revision, (value, previous) => {
  if (value === previous) return
  versionCache.clear()
  resetSeasons(); openSeasons.value = new Set(); visitedSeasons.value = new Set(); episodeLimits.value = {}
  if (detail.value && hasIndexedEpisodes.value && detailController) void primeSchedules(detailController.signal)
})
function toggleSeason(number: number) {
  if (openSeasons.value.has(number)) { void closeSeason(number); return }
  openSeasons.value = new Set([number])
  visitedSeasons.value.add(number)
  episodeLimits.value[number] ||= 20
  void loadSeason(number)
  loadSeasonSchedule(number)
}
async function closeSeason(number: number) {
  const button = document.getElementById(`season-button-${number}`)
  const headerBottom = document.querySelector('.workbench-header')?.getBoundingClientRect().bottom || 0
  const previousTop = button?.getBoundingClientRect().top ?? headerBottom + 24
  openSeasons.value.clear()
  await nextTick()
  if (!button?.isConnected) return
  button.focus({ preventScroll: true })
  // Preserve the card position when visible; closing from deep in the list returns to it.
  const targetTop = Math.max(headerBottom + 24, Math.min(previousTop, window.innerHeight - button.offsetHeight))
  window.scrollBy({ top: button.getBoundingClientRect().top - targetTop, behavior: 'instant' })
}
function hasImage(url: string) { return !!url && !failedImages.value.has(url) }
function seasonName(season: SeasonSummary) { return detail.value?.numbering?.groupId && season.name ? season.name : season.number === 0 ? '特别篇' : `第 ${season.number} 季` }
// 同一个组件会复用于不同影视，路由变化时取消旧请求并清空季缓存。
watch(() => [props.kind, props.tmdbId, props.token], () => { namingCopied.value = false; namingCopyError.value = false; void loadDetail() }, { immediate: true })
// 首次打开和入库状态更新后，仅为已有分集的剧集预读播出缓存，不阻塞详情显示。
watch(() => [detail.value?.id, hasIndexedEpisodes.value], () => {
  if (props.kind === 'tv' && detail.value?.id === Number(props.tmdbId) && hasIndexedEpisodes.value && detailController) void primeSchedules(detailController.signal)
})
onBeforeUnmount(() => { cancelRequests(); window.clearInterval(dateTimer); window.clearInterval(scheduleTimer); window.clearTimeout(namingNoticeTimer); document.title = previousTitle })
// 管理员从追更管理点搜索后打开已有资源浮窗，只消费一次跳转参数。
watch(() => [detail.value?.id, props.kind, route.query.botSearch, isPortalAdmin.value], () => {
  if (isPortalAdmin.value && detail.value?.id === Number(props.tmdbId) && detail.value?.kind === props.kind && route.query.botSearch === '1') {
    botSearchOpen.value = true
    const query = { ...route.query }; delete query.botSearch
    void router.replace({ path: route.path, query })
  }
})
</script>

<template>
  <div class="media-detail flex min-h-screen flex-col" :style="mediaColorStyle">
    <TMDBEpisodeGroupDialog v-if="isPortalAdmin && kind === 'tv' && numberingOpen && detail" :token="token" :tmdb-id="tmdbId" :title="detail.title" @close="numberingOpen = false" @saved="numberingSaved" />
    <BotSearchDialog v-if="isPortalAdmin && searchConfigured.bot && botSearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :theme-colors="mediaColorStyle" @close="botSearchOpen = false" />
    <RE0SearchDialog v-if="isPortalAdmin && searchConfigured.re0 && re0SearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :theme-colors="mediaColorStyle" @close="re0SearchOpen = false" />
    <GyingSearchDialog v-if="isPortalAdmin && searchConfigured.gying && gyingSearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :original-title="detail.originalTitle" :imdb-id="detail.imdbId" :date="detail.date" :seasons="detail.seasons" :theme-colors="mediaColorStyle" @close="gyingSearchOpen = false" />
    <WoniuSearchDialog v-if="isPortalAdmin && searchConfigured.woniu && woniuSearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :original-title="detail.originalTitle" :theme-colors="mediaColorStyle" @close="woniuSearchOpen = false" />
    <AnimeResourceSearchDialog v-if="canSearchAnimeResources && (mikanSearchOpen || gardenSearchOpen) && detail" :key="gardenSearchOpen ? 'garden' : 'mikan'" :provider="gardenSearchOpen ? 'garden' : 'mikan'" :token="token" :title="detail.title" :original-title="detail.originalTitle" :tmdb-id="detail.id" :kind="detail.kind" :date="detail.date" :seasons="detail.seasons" :theme-colors="mediaColorStyle" @close="mikanSearchOpen = false; gardenSearchOpen = false" />
    <WorkbenchHeader tonal-surface :over-backdrop="!!detail?.backdropUrl && hasImage(detail.backdropUrl)" @logout="emit('logout')" />
    <main class="detail-page relative min-w-0 flex-1">
      <div v-if="detail?.backdropUrl && hasImage(detail.backdropUrl)" class="detail-backdrop pointer-events-none absolute inset-x-0 top-0" aria-hidden="true">
        <img :src="tmdbImageURL(detail.backdropUrl)" alt="" class="h-full w-full object-cover object-top" referrerpolicy="no-referrer" @error="failedImages.add(detail.backdropUrl)" />
      </div>
      <div class="detail-content workbench-content relative mx-auto max-w-[1440px] px-5 pb-8 md:px-8 lg:px-10">
        <div v-if="loading" aria-label="正在加载影视详情" aria-busy="true" class="animate-pulse">
          <div class="grid gap-8 sm:grid-cols-[220px_minmax(0,1fr)]"><div class="mx-auto aspect-[2/3] w-48 rounded-2xl bg-slate-200 dark:bg-slate-700 sm:w-full"></div><div class="space-y-6 py-4"><div class="h-5 w-1/3 rounded bg-slate-200 dark:bg-slate-700"></div><div class="h-12 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div><div class="h-5 w-1/2 rounded bg-slate-200 dark:bg-slate-700"></div><div class="h-32 rounded-xl bg-slate-200 dark:bg-slate-700/60"></div></div></div>
          <div class="mt-12 h-24 rounded-xl bg-slate-200 dark:bg-slate-700/60"></div>
        </div>
        <section v-else-if="error" role="alert" class="rounded-2xl border border-red-100 bg-white dark:bg-slate-900 dark:bg-slate-900/90 px-6 py-12 text-center">
          <HistoryBackButton :fallback="`/${kind}`" class="mb-4" />
          <h1 class="text-xl font-semibold detail-on-surface">暂时无法加载详情</h1><p class="mt-3 text-sm text-red-600">{{ error }}</p>
          <button class="mt-6 rounded-lg bg-slate-900 px-5 py-2.5 text-sm text-white" @click="loadDetail">重新加载</button>
        </section>
        <template v-else-if="detail">
          <section aria-label="影视信息" class="grid items-start gap-7 sm:grid-cols-[200px_minmax(0,1fr)] lg:grid-cols-[255px_minmax(0,1fr)] lg:gap-12">
            <div class="detail-poster mx-auto aspect-[2/3] w-48 overflow-hidden rounded-2xl border sm:w-full">
              <img v-if="hasImage(detail.posterUrl)" :src="tmdbImageURL(detail.posterUrl)" :alt="detail.title" referrerpolicy="no-referrer" class="h-full w-full object-cover" @load="readPosterColors" @error="failedImages.add(detail.posterUrl)" />
              <div v-else class="flex h-full items-center justify-center text-sm detail-muted">暂无海报</div>
            </div>
            <div class="min-w-0 py-1">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs detail-muted"><span class="rounded-full detail-kind px-3 py-1 text-[11px] font-bold">{{ mediaLabel }}</span><span>{{ detail.date || '日期待定' }}</span><span v-if="detail.genres.length" class="detail-divider border-l pl-3 font-semibold detail-muted">{{ detail.genres.map(item => item.name).join(' / ') }}</span></div>
              <div class="mt-4 flex items-start gap-3">
                <HistoryBackButton :fallback="`/${kind}`" icon-only class="detail-back-button lg:mt-2" />
                <h1 class="min-w-0 break-words text-3xl font-bold leading-tight tracking-tight detail-on-surface lg:text-5xl"><button type="button" class="cursor-copy text-left decoration-slate-300 underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4" :title="`点击复制：${scrapeName}`" :aria-label="`复制刮削命名：${scrapeName}`" @click="copyScrapeName">{{ detail.title }}</button></h1>
              </div>
              <p v-if="namingCopied || namingCopyError" class="mt-2 text-xs" :class="namingCopied ? 'text-emerald-600' : 'text-amber-600'" role="status">{{ namingCopied ? `已复制：${scrapeName}` : '复制失败，请再次点击标题重试' }}</p>
              <p v-if="detail.originalTitle && detail.originalTitle !== detail.title" class="mt-2 text-sm detail-muted">{{ detail.originalTitle }}</p>
              <RouterLink v-if="kind === 'movie' && detail.collection" :to="`/collection/${detail.collection.id}`" class="detail-collection mt-4 inline-flex max-w-full flex-wrap items-center gap-2 rounded-xl px-3 py-2 text-xs"><span aria-hidden="true" class="detail-primary"><svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="7" width="18" height="14" rx="2" /><path d="M7 3h10" /></svg></span><span>{{ detail.collection.name }}</span><span class="detail-primary">查看系列 <svg class="inline h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></span></RouterLink>
              <div class="mt-4 flex flex-wrap items-center gap-4 text-xs">
                <a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="detail-rating flex items-baseline gap-2"><img :src="tmdbLogo" alt="TMDB" width="161" height="35.52" class="h-auto w-11 shrink-0" /><span class="text-base font-bold">{{ detail.votes > 0 ? detail.rating.toFixed(1) : '暂无评分' }}<span v-if="detail.votes > 0" class="ml-0.5 text-xs detail-muted">/10</span></span></a>
                <span v-if="detail.votes > 0" class="detail-muted">{{ detail.votes.toLocaleString() }} 人评价</span>
                <a v-if="detail.imdbUrl" :href="detail.imdbUrl" target="_blank" rel="noopener noreferrer" aria-label="在 IMDb 查看条目" class="rounded bg-yellow-400 px-1.5 py-0.5 font-black text-black hover:bg-yellow-300">IMDb</a>
              </div>
              <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-xs detail-muted"><span v-if="statusLabel">{{ statusLabel }}</span><span v-if="detail.runtime">{{ kind === 'tv' ? '每集约 ' : '' }}{{ detail.runtime }} 分钟</span><span v-if="kind === 'tv' && detail.episodeCount">共 {{ detail.episodeCount }} 集</span><span v-if="detail.countries.length">{{ detail.countries.join(' / ') }}</span></div>
              <div class="mt-6">
                <FollowButton :token="token" :kind="kind" :tmdb-id="tmdbId" :library-state="kind === 'movie' ? embyReadSucceeded ? embyMedia?.state || 'unknown' : 'unknown' : undefined" @refresh-library="refreshEmbyMedia" />
              </div>
              <div class="mt-3 flex flex-wrap items-center gap-3">
                <SearchPill v-if="isPortalAdmin && searchConfigured.re0" @click="re0SearchOpen = true">
                  <img :src="re0Logo" alt="" class="h-4 w-4 shrink-0 rounded-[4px] object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-semibold tracking-wide">RE0</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="isPortalAdmin && searchConfigured.bot" @click="botSearchOpen = true">
                  <img :src="telegramIcon" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-semibold">Bot</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="isPortalAdmin && searchConfigured.gying" @click="gyingSearchOpen = true">
                  <img :src="gyingLogo" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-semibold">观影</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="canSearchAnimeResources" @click="mikanSearchOpen = true">
                  <img :src="mikanMascot" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-semibold">Mikan</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="canSearchAnimeResources" @click="gardenSearchOpen = true">
                  <img :src="gardenLogo" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span>Garden 搜索</span>
                </SearchPill>
                <SearchPill v-if="isPortalAdmin && searchConfigured.woniu" @click="woniuSearchOpen = true">
                  <img :src="woniuLogo" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5"><span class="font-semibold">蜗牛</span><span class="detail-muted">搜索</span></span>
                </SearchPill>
              </div>
              <div class="detail-overview mt-7"><h2 class="text-sm font-semibold detail-muted">剧情大纲</h2><p v-if="detail.tagline" class="mt-3 text-sm italic detail-muted">{{ detail.tagline }}</p><p class="detail-on-surface mt-3 whitespace-pre-line break-words text-sm leading-7">{{ detail.overview || '暂无剧情简介。' }}</p></div>
            </div>
          </section>

          <p v-if="embyError" role="status" class="mt-6 text-xs text-amber-700">{{ embyError }}；{{ embyMedia ? '当前显示上次入库查询结果。' : '暂时无法确认入库状态。' }}</p>
          <EmbyMediaInfo v-if="isPortalAdmin && kind === 'movie'" :token="token" :tmdb-id="tmdbId" :media="embyMedia" />
          <section v-if="kind === 'tv'" class="mt-12" aria-label="剧集季列表">
            <div class="mb-3 flex flex-wrap items-center gap-3"><h2 class="text-lg font-bold detail-on-surface">季</h2><span class="text-xs detail-muted">{{ detail.seasons.filter(item => item.number > 0).length }} 季<span v-if="detail.seasons.some(item => item.number === 0)"> · 含特别篇</span></span><div v-if="isPortalAdmin" class="ml-auto flex flex-wrap items-center gap-4"><button type="button" class="inline-flex items-center gap-1.5 detail-link text-xs disabled:opacity-50" :disabled="metadataBusy" @click="numberingOpen = true"><svg aria-hidden="true" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M4 6h16M4 12h16M4 18h16" /><circle cx="8" cy="6" r="2" fill="currentColor" /><circle cx="16" cy="12" r="2" fill="currentColor" /><circle cx="10" cy="18" r="2" fill="currentColor" /></svg>设置季集方案</button><button type="button" class="detail-secondary-action text-xs disabled:opacity-50" :disabled="metadataBusy" @click="refreshMetadata">{{ metadataBusy ? '更新播出信息…' : '刷新播出信息' }}</button></div></div>
            <p v-if="detail.numbering?.groupId" class="mb-4 text-xs detail-muted">季集方案：{{ numberingLabel(detail.numbering) }}</p>
            <p v-if="numberingNotice" role="status" class="mb-4 detail-notice rounded-xl px-3 py-2 text-xs leading-5">{{ numberingNotice }}</p>
            <p v-if="metadataError" class="mb-3 text-xs text-amber-600">{{ metadataError }}，当前保留缓存数据。</p>
            <p v-if="!detail.seasons.length" class="rounded-xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 dark:bg-slate-900/80 p-5 text-sm detail-muted">暂无季信息</p>
            <div class="season-list space-y-3">
              <section v-for="season in detail.seasons" :key="season.number" class="detail-season min-w-0 rounded-xl border" :class="{ 'season-selected': openSeasons.has(season.number) }">
              <button :id="`season-button-${season.number}`" type="button" class="detail-season-toggle group/season flex min-h-14 w-full items-center gap-3 rounded-xl px-4 py-3 text-left transition-colors sm:px-5" :aria-expanded="openSeasons.has(season.number)" :aria-controls="`season-${season.number}`" @click="toggleSeason(season.number)">
                  <SeasonLibraryProgress :key="`${tmdbId}-${season.number}`" :state="seasonState(season.number)" :owned-episodes="[...(embyEpisodes[season.number] || [])]" :progress="seasonProgress[season.number]" :loading="!!schedules[season.number]?.loading" :error="schedules[season.number]?.error || ''" :stale="schedules[season.number]?.data?.cacheStale" :has-undated="seasonState(season.number) === 'present' && !!schedules[season.number]?.data?.episodes.some(episode => !episode.date)" @load="loadSeasonSchedule(season.number)" @refresh="loadSeasonSchedule(season.number, true)">
                    <span class="inline-flex shrink-0 items-center gap-3">
                      <span class="detail-on-surface text-sm font-semibold">{{ seasonName(season) }}</span>
                      <span class="detail-season-count rounded-full px-2 py-0.5 text-[11px]">{{ season.episodeCount }} 集</span>
                    </span>
                  </SeasonLibraryProgress>
                  <svg class="detail-muted h-4 w-4 shrink-0 transition-transform" :class="{ 'rotate-180': openSeasons.has(season.number) }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m5 8 5 5 5-5" /></svg>
              </button>
            <div v-if="openSeasons.has(season.number)" :id="`season-${season.number}`" role="region" :aria-labelledby="`season-button-${season.number}`" class="detail-divider border-t px-4 pb-4 sm:px-5">
                  <p v-if="seasons[season.number]?.loading && !seasons[season.number]?.data" role="status" class="py-6 text-sm detail-muted">正在加载分集…</p>
                  <div v-if="seasons[season.number]?.error" role="alert" class="py-5 text-sm text-amber-600">{{ seasons[season.number]?.error }}<button class="ml-3 underline" @click="loadSeason(season.number, true)">重试</button></div>
                  <template v-if="seasons[season.number]?.data">
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.missing.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-amber-300" aria-hidden="true"></span><span class="min-w-0 break-words">缺失集数：{{ seasonProgress[season.number]?.missingLabel }}</span></p>
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.numberingMode && seasonProgress[season.number]?.displayMissing.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-amber-300" aria-hidden="true"></span><span class="min-w-0 break-words">中间缺集：{{ seasonProgress[season.number]?.displayMissingLabel }}</span></p>
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.unlistedOwned.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-sky-300" aria-hidden="true"></span><span class="min-w-0 break-words">额外入库集数：{{ formatEpisodeRanges(seasonProgress[season.number]?.unlistedOwned || []) }}（TMDB 本季未收录）</span></p>
                    <p v-if="seasons[season.number]?.data?.overview" class="py-4 text-sm leading-7 detail-muted">{{ seasons[season.number]?.data?.overview }}</p>
                    <p v-if="!seasons[season.number]?.data?.episodes.length" class="py-5 text-sm detail-muted">暂无分集信息</p>
                    <SeasonEpisodeList :key="`${tmdbId}-${season.number}-${detail.numbering?.revision || 0}`" :token="token" :tmdb-id="tmdbId" :season="season.number" :episodes="seasons[season.number]?.data?.episodes.slice(0, episodeLimits[season.number] || 20) || []" :media-state="embyMedia?.state" :owned-episodes="embyEpisodes[season.number]" :admin="isPortalAdmin" :today="today" />
                    <InfiniteLoadMore v-if="seasons[season.number]?.data?.episodes.length" :key="`${tmdbId}-${season.number}`" :active="openSeasons.has(season.number)" :count="Math.min(episodeLimits[season.number] || 20, seasons[season.number]?.data?.episodes.length || 0)" :total="seasons[season.number]?.data?.episodes.length || 0" @load="episodeLimits[season.number] = Math.min((episodeLimits[season.number] || 20) + 20, seasons[season.number]?.data?.episodes.length || 0)" />
                  </template>
              <div class="detail-divider border-t pt-3 text-right">
                <button type="button" class="season-close rounded-lg px-3 py-2 text-xs" @click="closeSeason(season.number)">收起列表</button>
              </div>
            </div>
              </section>
            </div>
          </section>

          <section class="mt-12" aria-label="领衔主演">
            <h2 class="mb-5 text-lg font-bold detail-on-surface">领衔主演</h2>
            <p v-if="!detail.cast.length" class="text-sm detail-muted">暂无演员信息</p>
            <div v-else class="flex gap-5 overflow-x-auto pb-5 sm:gap-7">
              <RouterLink v-for="member in detail.cast" :key="member.id" :to="{ name: 'person', params: { tmdbId: member.id } }" class="group w-20 shrink-0 text-center sm:w-24" :title="`${member.name}${member.character ? ' · ' + member.character : ''}`">
                <div class="mx-auto h-20 w-20 overflow-hidden rounded-full border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 shadow-sm transition group-hover:ring-2 group-hover:ring-amber-300"><img v-if="hasImage(member.profileUrl)" :src="tmdbImageURL(member.profileUrl)" :alt="member.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover object-[center_25%]" @error="failedImages.add(member.profileUrl)" /><div v-else class="flex h-full items-center justify-center bg-slate-100 dark:bg-slate-800 text-xl detail-muted">{{ member.name.slice(0, 1) }}</div></div><h3 class="mt-3 truncate text-xs font-semibold detail-on-surface group-hover:text-amber-600">{{ member.name }}</h3><p class="mt-1 truncate text-[11px] detail-muted">{{ member.character || '角色未提供' }}</p>
              </RouterLink>
            </div>
          </section>
          <MediaRecommendations :token="token" :kind="kind" :tmdb-id="tmdbId" />
          <footer class="mt-12 border-t border-slate-200 dark:border-slate-700 dark:border-slate-800 pt-5 text-[11px] leading-6 detail-muted"><a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="hover:text-cyan-600">在 TMDB 查看完整资料 <svg class="inline h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></a><p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
        </template>
      </div>
    </main>
  </div>
</template>

<style scoped>
.media-detail {
  /* MCU surfaces already have restrained chroma; retain the artwork hue. */
  --md-sys-color-surface: color-mix(in srgb, rgb(248 250 252) 90%, var(--detail-light-surface) 10%);
  --md-sys-color-on-surface: rgb(15 23 42);
  --md-sys-color-on-surface-variant: rgb(71 85 105);
  --md-sys-color-surface-container-low: rgb(241 245 249);
  --md-sys-color-surface-container: rgb(241 245 249);
  --md-sys-color-surface-container-high: rgb(226 232 240);
  --md-sys-color-surface-container-highest: rgb(226 232 240);
  --md-sys-color-outline-variant: rgb(226 232 240);
  --md-sys-color-primary: var(--detail-light-primary);
  --md-sys-color-on-primary: var(--detail-light-on-primary);
  --md-sys-color-primary-container: var(--detail-light-primary-container);
  --md-sys-color-on-primary-container: var(--detail-light-on-primary-container);
  --md-sys-color-secondary-container: rgb(241 245 249);
  --md-sys-color-on-secondary-container: rgb(51 65 85);
  --md-sys-color-tertiary-container: color-mix(in srgb, rgb(241 245 249) 88%, var(--detail-light-primary) 12%);
  --md-sys-color-on-tertiary-container: rgb(51 65 85);
  --detail-media-surface: rgb(241 245 249);
  --detail-media-inset: rgb(241 245 249);
  --detail-media-chip: rgb(226 232 240);
  --detail-media-outline: rgb(226 232 240);
  --detail-media-muted: rgb(100 116 139);
  background: var(--md-sys-color-surface);
  color: var(--md-sys-color-on-surface);
}
.dark .media-detail {
  --md-sys-color-surface: color-mix(in srgb, rgb(24 25 28) 90%, var(--detail-dark-surface) 10%);
  --md-sys-color-on-surface: rgb(225 227 229);
  --md-sys-color-on-surface-variant: rgb(156 163 170);
  --md-sys-color-surface-container-low: rgb(33 37 41);
  --md-sys-color-surface-container: rgb(33 37 41);
  --md-sys-color-surface-container-high: rgb(45 49 54);
  --md-sys-color-surface-container-highest: rgb(45 49 54);
  --md-sys-color-outline-variant: rgb(54 59 65);
  --md-sys-color-primary: var(--detail-dark-primary);
  --md-sys-color-on-primary: var(--detail-dark-on-primary);
  --md-sys-color-primary-container: var(--detail-dark-primary-container);
  --md-sys-color-on-primary-container: var(--detail-dark-on-primary-container);
  --md-sys-color-secondary-container: rgb(45 49 54);
  --md-sys-color-on-secondary-container: rgb(225 227 229);
  --md-sys-color-tertiary-container: color-mix(in srgb, rgb(45 49 54) 88%, var(--detail-dark-primary) 12%);
  --md-sys-color-on-tertiary-container: rgb(225 227 229);
  --detail-media-surface: rgb(33 37 41);
  --detail-media-inset: rgb(33 37 41);
  --detail-media-chip: rgb(45 49 54);
  --detail-media-outline: rgb(54 59 65);
  --detail-media-muted: rgb(156 163 170);
}
.detail-page { background: var(--md-sys-color-surface); }
.detail-content { padding-top: calc(var(--workbench-header-height) + 24px); }
.detail-back-button {
  background: var(--md-sys-color-surface-container-high);
  color: var(--md-sys-color-on-surface);
}
.detail-back-button:hover { background: var(--md-sys-color-surface-container-highest); }
.detail-backdrop {
  height: clamp(36rem, 78vh, 52rem);
  overflow: hidden;
  isolation: isolate;
}
.detail-backdrop img {
  /* Overscan keeps the blurred image edges outside the visible background. */
  position: absolute;
  inset: -12px;
  width: calc(100% + 24px);
  /* Override Preflight's max-width: 100% so overscan covers both edges. */
  max-width: none;
  height: calc(100% + 24px);
  filter: blur(5px);
}
.detail-backdrop::before,
.detail-backdrop::after {
  content: '';
  position: absolute;
  inset: 0;
}
.detail-backdrop::before {
  z-index: 1;
  /* The reading column extends across the image, including the right synopsis. */
  background: linear-gradient(90deg,
    color-mix(in srgb, var(--md-sys-color-surface) 64%, transparent) 0%,
    color-mix(in srgb, var(--md-sys-color-surface) 80%, transparent) 18%,
    color-mix(in srgb, var(--md-sys-color-surface) 86%, transparent) 42%,
    color-mix(in srgb, var(--md-sys-color-surface) 82%, transparent) 82%,
    color-mix(in srgb, var(--md-sys-color-surface) 42%, transparent) 100%);
}
.detail-backdrop::after {
  z-index: 2;
  background: linear-gradient(to bottom,
    color-mix(in srgb, var(--md-sys-color-surface) 24%, transparent) 0%,
    color-mix(in srgb, var(--md-sys-color-surface) 20%, transparent) 25%,
    color-mix(in srgb, var(--md-sys-color-surface) 56%, transparent) 48%,
    color-mix(in srgb, var(--md-sys-color-surface) 86%, transparent) 72%,
    color-mix(in srgb, var(--md-sys-color-surface) 98%, transparent) 92%,
    var(--md-sys-color-surface) 96%);
}
@media (max-width: 639px) {
  .detail-backdrop {
    height: calc(var(--workbench-header-height) + 350px);
  }
  .detail-backdrop img { filter: blur(4px); }
  .detail-backdrop::before {
    background: color-mix(in srgb, var(--md-sys-color-surface) 30%, transparent);
  }
}
.detail-poster {
  border-color: var(--md-sys-color-outline-variant);
  background: var(--md-sys-color-surface-container-high);
}
.detail-on-surface { color: var(--md-sys-color-on-surface); }
.detail-muted { color: var(--md-sys-color-on-surface-variant); }
.detail-primary { color: var(--md-sys-color-primary); }
.detail-link { color: var(--md-sys-color-primary); }
.detail-link:hover { text-decoration: underline; text-underline-offset: 3px; }
.detail-secondary-action { color: var(--md-sys-color-on-surface-variant); }
.detail-secondary-action:hover { color: var(--md-sys-color-primary); }
.detail-notice { background: var(--md-sys-color-primary-container); color: var(--md-sys-color-on-primary-container); }
.detail-rating { color: var(--md-sys-color-on-surface); }
.detail-rating:hover { color: var(--md-sys-color-primary); }
.media-detail :is(a, button):focus-visible { outline-color: var(--md-sys-color-primary); }
.media-detail :deep(.brand-caption) { color: var(--md-sys-color-on-surface-variant); }
.media-detail :deep(.follow-button[data-state='idle']) {
  background: var(--md-sys-color-primary-container);
  color: var(--md-sys-color-on-primary-container);
  box-shadow: none;
}
.media-detail :deep(.follow-button[data-state='idle']:hover),
.media-detail :deep(.follow-button[data-state='subscribed']) {
  background: var(--md-sys-color-primary);
  color: var(--md-sys-color-on-primary);
}
.media-detail :deep(.follow-button:focus-visible) { outline-color: var(--md-sys-color-primary); }
.media-detail :deep(.search-pill) {
  border: 1px solid var(--search-dialog-edge);
  background: var(--search-pill-background);
  color: var(--search-dialog-text);
  box-shadow: 0 2px 8px rgb(15 23 42 / 6%), inset 0 1px 0 rgb(255 255 255 / 10%);
}
.media-detail :deep(.search-pill:hover) {
  background: var(--search-pill-hover);
  color: var(--search-dialog-text);
  box-shadow: 0 4px 12px rgb(15 23 42 / 10%);
}
.media-detail :deep(.search-pill:focus-visible) { outline-color: var(--search-dialog-primary); }
@supports ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .media-detail :deep(.search-pill) {
    -webkit-backdrop-filter: blur(12px) saturate(1.05);
    backdrop-filter: blur(12px) saturate(1.05);
  }
}
@media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
  .media-detail :deep(.search-pill) {
    -webkit-backdrop-filter: none;
    backdrop-filter: none;
  }
}
.detail-divider { border-color: var(--md-sys-color-outline-variant); }
.detail-season {
  background: rgb(241 245 249);
  border-color: color-mix(in srgb, var(--md-sys-color-outline-variant) 45%, var(--md-sys-color-surface));
  color: var(--md-sys-color-on-surface);
}
.dark .media-detail .detail-season { background: rgb(33 37 41); }
.detail-season.season-selected {
  border-color: var(--md-sys-color-primary);
  background: var(--md-sys-color-surface-container);
}
.dark .media-detail .detail-season.season-selected { background: rgb(45 49 54); }
.season-close {
  color: var(--md-sys-color-primary);
  background: var(--md-sys-color-surface-container-highest);
}
.season-close:focus-visible {
  outline: 2px solid var(--md-sys-color-primary);
  outline-offset: 2px;
}
.detail-season-toggle:hover {
  background: color-mix(in srgb, var(--md-sys-color-surface-container-high) 60%, transparent);
}
.detail-season-toggle:focus-visible {
  outline: 2px solid var(--md-sys-color-primary);
  outline-offset: -2px;
}
.detail-season-count {
  background: var(--md-sys-color-surface-container-high);
  color: var(--md-sys-color-on-surface-variant);
}
.media-detail .detail-season-note,
.media-detail :deep(.media-tonal-panel) {
  background: var(--md-sys-color-surface);
  border-color: var(--md-sys-color-outline-variant);
  color: var(--md-sys-color-on-surface);
}
.media-detail :deep(.media-tonal-inset),
.media-detail :deep(.media-tonal-chip) {
  background: var(--md-sys-color-surface-container-highest);
  border-color: var(--md-sys-color-outline-variant);
  color: var(--md-sys-color-on-surface-variant);
}
.media-detail :deep(.media-tonal-toggle:hover) {
  background: var(--md-sys-color-surface-container-high);
}
.media-detail :deep(.media-tonal-toggle:focus-visible) {
  outline-color: var(--md-sys-color-primary);
}
.media-detail :deep(.media-tonal-divider),
.media-detail :deep(.media-tonal-rows > :not([hidden]) ~ :not([hidden])) {
  border-color: var(--md-sys-color-outline-variant);
}
.media-detail :deep(.media-version-panel) {
  background: var(--detail-media-surface);
  border-color: var(--detail-media-outline);
}
.media-detail :deep(.media-version-panel .media-tonal-inset) {
  background: var(--detail-media-inset);
  border-color: var(--detail-media-outline);
}
.media-detail :deep(.media-version-panel .media-tonal-chip) {
  background: var(--detail-media-chip);
  border-color: var(--detail-media-outline);
  color: var(--detail-media-muted);
}
.media-detail :deep(.media-version-panel .media-tonal-divider),
.media-detail :deep(.media-version-panel .media-tonal-rows > :not([hidden]) ~ :not([hidden])) {
  border-color: var(--detail-media-outline);
}
.media-detail :deep(.media-version-panel .media-tonal-rows dt) {
  color: var(--detail-media-muted);
}
.media-detail :deep(.media-version-panel .media-tonal-toggle:hover) {
  background: var(--detail-media-inset);
}
@media (hover: hover) {
  .media-detail :deep(.media-version-movie .movie-version-toggle:hover) {
    background: color-mix(in srgb, var(--md-sys-color-on-surface) 4%, var(--detail-media-surface));
  }
}
.detail-kind {
  background: var(--md-sys-color-tertiary-container);
  color: var(--md-sys-color-on-tertiary-container);
}
.detail-collection {
  background: var(--md-sys-color-surface-container-high);
  color: var(--md-sys-color-on-surface);
  transition: background-color 200ms;
}
.detail-collection:hover {
  background: var(--md-sys-color-surface-container-highest);
}
.detail-collection:focus-visible {
  outline: 2px solid var(--md-sys-color-primary);
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .animate-pulse { animation: none; }
  .transition, .transition-transform, .transition-colors { transition: none; }
  .detail-collection { transition: none; }
}
</style>
