<script setup lang="ts">
import { catalogAPI, isPortalAdmin } from '../composables/usePortalAuth'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import BotSearchDialog from '../components/BotSearchDialog.vue'
import GyingSearchDialog from '../components/GyingSearchDialog.vue'
import MikanSearchDialog from '../components/MikanSearchDialog.vue'
import RE0SearchDialog from '../components/RE0SearchDialog.vue'
import FollowButton from '../components/FollowButton.vue'
import SearchPill from '../components/SearchPill.vue'
import MediaRecommendations from '../components/MediaRecommendations.vue'
import EmbyMediaInfo from '../components/EmbyMediaInfo.vue'
import EmbyMediaVersions from '../components/EmbyMediaVersions.vue'
import SeasonLibraryProgress from '../components/SeasonLibraryProgress.vue'
import InfiniteLoadMore from '../components/InfiniteLoadMore.vue'
import HistoryBackButton from '../components/HistoryBackButton.vue'
import { useEmbyMedia } from '../composables/useEmbyMedia'
import { useMediaColors } from '../composables/useMediaColors'
import { useTMDBSeasons, type CacheInfo, type TMDBEpisode as Episode } from '../composables/useTMDBSeasons'
import { episodeHasAired, formatEpisodeRanges, localDateKey, seasonLibraryProgress } from '../types/seasonLibrary'
import { copyText } from '../utils/clipboard'
import { formatScrapeName } from '../utils/mediaNaming'
import { tmdbImageURL } from '../utils/tmdbImage'
import tmdbLogo from '../assets/tmdb.svg'
import mikanMascot from '../assets/mikan/mikan-pic.svg?no-inline'
import mikanWordmark from '../assets/mikan/mikan-text.svg?no-inline'
import re0Logo from '../assets/re0/logo-re0-square.webp'
import gyingLogo from '../assets/gying/logo-gying.png'
import telegramIcon from '../assets/telegram/telegram.svg'
import { nanShareRequest, type NanShareConfigView } from '../types/nanshare'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'
import { gyingRequest, type GyingConfigView } from '../types/gying'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const botSearchOpen = ref(false)
const gyingSearchOpen = ref(false)
const mikanSearchOpen = ref(false)
const re0SearchOpen = ref(false)
const searchConfigured = ref({ re0: false, bot: false, gying: false })
watch(() => [props.token, isPortalAdmin.value] as const, ([token, admin], _, onCleanup) => {
  searchConfigured.value = { re0: false, bot: false, gying: false }
  re0SearchOpen.value = false; botSearchOpen.value = false; gyingSearchOpen.value = false
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
}, { immediate: true })
const route = useRoute(), router = useRouter()
watch(() => [props.kind, props.tmdbId], () => { botSearchOpen.value = false; gyingSearchOpen.value = false; mikanSearchOpen.value = false; re0SearchOpen.value = false })
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
const { media: embyMedia, episodes: embyEpisodes, error: embyError } = useEmbyMedia(() => props.token, () => props.kind, () => props.tmdbId)
interface CastMember { id: number; name: string; character: string; profileUrl: string }
interface SeasonSummary { id: number; number: number; name: string; episodeCount: number; date: string }
interface Detail extends CacheInfo {
  id: number; kind: 'movie' | 'tv'; title: string; originalTitle: string; overview: string; posterUrl: string
  backdropUrl: string; date: string; rating: number; votes: number; tagline: string; status: string; runtime: number
  episodeCount: number; genres: { id: number; name: string }[]; countries: string[]; cast: CastMember[]
  seasons: SeasonSummary[]; collection: { id: number; name: string } | null; imdbId: string; imdbUrl: string; tmdbUrl: string
}
const detail = ref<Detail | null>(null)
const { mediaColorStyle, readPosterColors } = useMediaColors(() => tmdbImageURL(detail.value?.posterUrl || ''))
// 动画类型包含日漫、国漫及动画电影，不按国家限制入口。
const canSearchMikan = computed(() => isPortalAdmin.value && !!detail.value?.genres?.some(genre => genre.id === 16))
watch(canSearchMikan, allowed => { if (!allowed) mikanSearchOpen.value = false })
const loading = ref(true)
const error = ref('')
const failedImages = ref(new Set<string>())
const openSeasons = ref(new Set<number>())
const visitedSeasons = ref(new Set<number>())
const { seasons, schedules, reset: resetSeasons, prime: primeSchedules, loadSeason, loadSchedule } = useTMDBSeasons(() => props.token, () => props.tmdbId)
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
  void refreshDetail()
  for (const season of detail.value?.seasons || []) {
    loadSeasonSchedule(season.number, true)
    if (visitedSeasons.value.has(season.number)) void loadSeason(season.number, true)
  }
}
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
function seasonName(season: SeasonSummary) { return season.number === 0 ? '特别篇' : `第 ${season.number} 季` }
function isEpisodeAvailable(season: number, episode: Episode) {
  // Emby 已入库是独立事实，不能被 TMDB 缺失或未来的播出日期覆盖。
  return (embyMedia.value?.state === 'present' || embyMedia.value?.state === 'stale') && !!embyEpisodes.value[season]?.has(episode.number)
}
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
    <BotSearchDialog v-if="isPortalAdmin && searchConfigured.bot && botSearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :theme-colors="mediaColorStyle" @close="botSearchOpen = false" />
    <RE0SearchDialog v-if="isPortalAdmin && searchConfigured.re0 && re0SearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :theme-colors="mediaColorStyle" @close="re0SearchOpen = false" />
    <GyingSearchDialog v-if="isPortalAdmin && searchConfigured.gying && gyingSearchOpen && detail" :token="token" :kind="kind" :tmdb-id="tmdbId" :title="detail.title" :original-title="detail.originalTitle" :imdb-id="detail.imdbId" @close="gyingSearchOpen = false" />
    <MikanSearchDialog v-if="canSearchMikan && mikanSearchOpen && detail" :token="token" :title="detail.title" :original-title="detail.originalTitle" @close="mikanSearchOpen = false" />
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
          <h1 class="text-xl font-semibold text-slate-900 dark:text-slate-100">暂时无法加载详情</h1><p class="mt-3 text-sm text-red-600">{{ error }}</p>
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
                <h1 class="min-w-0 break-words text-3xl font-bold leading-tight tracking-tight detail-on-surface lg:text-5xl"><button type="button" class="cursor-copy text-left decoration-slate-300 underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-500" :title="`点击复制：${scrapeName}`" :aria-label="`复制刮削命名：${scrapeName}`" @click="copyScrapeName">{{ detail.title }}</button></h1>
              </div>
              <p v-if="namingCopied || namingCopyError" class="mt-2 text-xs" :class="namingCopied ? 'text-emerald-600' : 'text-amber-600'" role="status">{{ namingCopied ? `已复制：${scrapeName}` : '复制失败，请再次点击标题重试' }}</p>
              <p v-if="detail.originalTitle && detail.originalTitle !== detail.title" class="mt-2 text-sm detail-muted">{{ detail.originalTitle }}</p>
              <RouterLink v-if="kind === 'movie' && detail.collection" :to="`/collection/${detail.collection.id}`" class="detail-collection mt-4 inline-flex max-w-full flex-wrap items-center gap-2 rounded-xl px-3 py-2 text-xs"><span aria-hidden="true" class="detail-primary"><svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="7" width="18" height="14" rx="2" /><path d="M7 3h10" /></svg></span><span>{{ detail.collection.name }}</span><span class="detail-primary">查看系列 <svg class="inline h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></span></RouterLink>
              <div class="mt-4 flex flex-wrap items-center gap-4 text-xs">
                <a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="flex items-baseline gap-2 text-cyan-600 hover:text-cyan-700"><img :src="tmdbLogo" alt="TMDB" width="161" height="35.52" class="h-auto w-11 shrink-0" /><span class="text-base font-bold">{{ detail.votes > 0 ? detail.rating.toFixed(1) : '暂无评分' }}<span v-if="detail.votes > 0" class="ml-0.5 text-xs detail-muted">/10</span></span></a>
                <span v-if="detail.votes > 0" class="detail-muted">{{ detail.votes.toLocaleString() }} 人评价</span>
                <a v-if="detail.imdbUrl" :href="detail.imdbUrl" target="_blank" rel="noopener noreferrer" aria-label="在 IMDb 查看条目" class="rounded bg-yellow-400 px-1.5 py-0.5 font-black text-black hover:bg-yellow-300">IMDb</a>
              </div>
              <div class="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-xs detail-muted"><span v-if="statusLabel">{{ statusLabel }}</span><span v-if="detail.runtime">{{ kind === 'tv' ? '每集约 ' : '' }}{{ detail.runtime }} 分钟</span><span v-if="kind === 'tv' && detail.episodeCount">共 {{ detail.episodeCount }} 集</span><span v-if="detail.countries.length">{{ detail.countries.join(' / ') }}</span></div>
              <div class="mt-6">
                <FollowButton :token="token" :kind="kind" :tmdb-id="tmdbId" />
              </div>
              <div class="mt-3 flex flex-wrap items-center gap-3">
                <SearchPill v-if="isPortalAdmin && searchConfigured.re0" @click="re0SearchOpen = true">
                  <img :src="re0Logo" alt="" class="h-4 w-4 shrink-0 rounded-[4px] object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-black tracking-wider re0-spectrum">RE0</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="isPortalAdmin && searchConfigured.bot" @click="botSearchOpen = true">
                  <img :src="telegramIcon" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-bold font-sans text-[#2AABEE]">Bot</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="isPortalAdmin && searchConfigured.gying" @click="gyingSearchOpen = true">
                  <img :src="gyingLogo" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <span class="font-bold text-violet-600 dark:text-violet-500">观影</span>
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
                <SearchPill v-if="canSearchMikan" @click="mikanSearchOpen = true">
                  <img :src="mikanMascot" alt="" class="h-4 w-4 shrink-0 object-contain" draggable="false" />
                  <span class="flex items-center gap-0.5">
                    <img :src="mikanWordmark" alt="Mikan" class="h-2.5 w-auto shrink-0 object-contain" draggable="false" />
                    <span class="detail-muted">搜索</span>
                  </span>
                </SearchPill>
              </div>
              <div class="mt-7"><h2 class="text-sm font-semibold detail-muted">剧情大纲</h2><p v-if="detail.tagline" class="mt-3 text-sm italic detail-muted">{{ detail.tagline }}</p><p class="detail-on-surface mt-3 whitespace-pre-line break-words text-sm leading-7">{{ detail.overview || '暂无剧情简介。' }}</p></div>
            </div>
          </section>

          <p v-if="embyError" role="status" class="mt-6 text-xs text-amber-700">{{ embyError }}；{{ embyMedia ? '当前显示上次入库查询结果。' : '暂时无法确认入库状态。' }}</p>
          <EmbyMediaInfo v-if="isPortalAdmin && kind === 'movie'" :token="token" :tmdb-id="tmdbId" :media="embyMedia" />
          <section v-if="kind === 'tv'" class="mt-12" aria-label="剧集季列表">
            <div class="mb-5 flex flex-wrap items-baseline gap-3"><h2 class="text-lg font-bold text-slate-900 dark:text-slate-100">季</h2><span class="text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400">{{ detail.seasons.filter(item => item.number > 0).length }} 季<span v-if="detail.seasons.some(item => item.number === 0)"> · 含特别篇</span></span><button v-if="isPortalAdmin" class="ml-auto text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:text-blue-600 disabled:opacity-50" :disabled="metadataBusy" @click="refreshMetadata">{{ metadataBusy ? '更新播出信息…' : '刷新播出信息' }}</button></div>
            <p v-if="metadataError" class="mb-3 text-xs text-amber-600">{{ metadataError }}，当前保留缓存数据。</p>
            <p v-if="!detail.seasons.length" class="rounded-xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 dark:bg-slate-900/80 p-5 text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400">暂无季信息</p>
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
                  <p v-if="seasons[season.number]?.loading && !seasons[season.number]?.data" role="status" class="py-6 text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400">正在加载分集…</p>
                  <div v-if="seasons[season.number]?.error" role="alert" class="py-5 text-sm text-amber-600">{{ seasons[season.number]?.error }}<button class="ml-3 underline" @click="loadSeason(season.number, true)">重试</button></div>
                  <template v-if="seasons[season.number]?.data">
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.missing.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-amber-300" aria-hidden="true"></span><span class="min-w-0 break-words">缺失集数：{{ seasonProgress[season.number]?.missingLabel }}</span></p>
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.numberingMode && seasonProgress[season.number]?.displayMissing.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-amber-300" aria-hidden="true"></span><span class="min-w-0 break-words">中间缺集：{{ seasonProgress[season.number]?.displayMissingLabel }}</span></p>
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.unlistedOwned.length" class="detail-season-note mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 dark:text-slate-300 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-sky-300" aria-hidden="true"></span><span class="min-w-0 break-words">额外入库集数：{{ formatEpisodeRanges(seasonProgress[season.number]?.unlistedOwned || []) }}（TMDB 本季未收录）</span></p>
                    <p v-if="seasons[season.number]?.data?.overview" class="py-4 text-sm leading-7 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ seasons[season.number]?.data?.overview }}</p>
                    <p v-if="!seasons[season.number]?.data?.episodes.length" class="py-5 text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400">暂无分集信息</p>
                    <div class="divide-y divide-slate-200 dark:divide-slate-700/80">
                      <article v-for="episode in seasons[season.number]?.data?.episodes.slice(0, episodeLimits[season.number] || 20)" :key="episode.id" class="min-w-0 py-6">
                        <div class="flex flex-wrap items-start justify-between gap-3"><h3 class="min-w-0 text-base font-semibold text-slate-800 dark:text-slate-200"><span class="mr-2 text-slate-400 dark:text-slate-500 dark:text-slate-400">{{ String(episode.number).padStart(2, '0') }}</span>{{ episode.name || `第 ${episode.number} 集` }}<span v-if="isEpisodeAvailable(season.number, episode)" class="ml-2 rounded bg-emerald-50 px-2 py-0.5 text-[10px] font-normal text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300">{{ embyMedia?.state === 'stale' ? '已入库 · 待更新' : '已入库' }}</span><span v-else-if="!episodeHasAired(episode, today)" class="ml-2 rounded bg-slate-100 dark:bg-slate-800 px-2 py-0.5 text-[10px] font-normal text-slate-400 dark:text-slate-500 dark:text-slate-400">{{ episode.date ? '未播出' : '播出日期待定' }}</span><span v-else-if="reliableLibrary" class="ml-2 rounded bg-amber-50 px-2 py-0.5 text-[10px] font-normal text-amber-600 dark:bg-amber-500/10 dark:text-amber-500">未入库</span></h3><span v-if="isEpisodeAvailable(season.number, episode)" class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-emerald-500 text-xs text-white" aria-hidden="true"><svg class="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m5 12 4 4L19 6" /></svg></span></div>
                        <p class="mt-2 flex flex-wrap gap-3 text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400"><span>{{ episode.date || '播出日期待定' }}</span><span v-if="episode.runtime">{{ episode.runtime }} 分钟</span><span v-if="episode.votes > 0" class="text-amber-600"><svg class="inline h-3 w-3" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="m12 3 2.8 5.7 6.3.9-4.6 4.5 1.1 6.3-5.6-3-5.6 3 1.1-6.3L2.9 9.6l6.3-.9Z" /></svg> {{ episode.rating.toFixed(1) }}</span></p>
                        <p class="mt-3 text-sm leading-7 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ episode.overview || '暂无分集简介。' }}</p>
                        <EmbyMediaVersions v-if="isPortalAdmin && isEpisodeAvailable(season.number, episode)" :token="token" kind="tv" :tmdb-id="tmdbId" :season="season.number" :episode="episode.number" />
                        <div v-if="hasImage(episode.stillUrl)" class="mx-auto mt-5 aspect-video w-full max-w-xs overflow-hidden rounded-xl bg-slate-100 dark:bg-slate-800"><img :src="tmdbImageURL(episode.stillUrl)" :alt="episode.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(episode.stillUrl)" /></div>
                      </article>
                    </div>
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
            <h2 class="mb-5 text-lg font-bold text-slate-900 dark:text-slate-100">领衔主演</h2>
            <p v-if="!detail.cast.length" class="text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400">暂无演员信息</p>
            <div v-else class="flex gap-5 overflow-x-auto pb-5 sm:gap-7">
              <RouterLink v-for="member in detail.cast" :key="member.id" :to="{ name: 'person', params: { tmdbId: member.id } }" class="group w-20 shrink-0 text-center sm:w-24" :title="`${member.name}${member.character ? ' · ' + member.character : ''}`">
                <div class="mx-auto h-20 w-20 overflow-hidden rounded-full border border-slate-200 dark:border-slate-700 dark:border-slate-800 bg-white dark:bg-slate-900 shadow-sm transition group-hover:ring-2 group-hover:ring-amber-300"><img v-if="hasImage(member.profileUrl)" :src="tmdbImageURL(member.profileUrl)" :alt="member.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover object-[center_25%]" @error="failedImages.add(member.profileUrl)" /><div v-else class="flex h-full items-center justify-center bg-slate-100 dark:bg-slate-800 text-xl text-slate-400 dark:text-slate-500 dark:text-slate-400">{{ member.name.slice(0, 1) }}</div></div><h3 class="mt-3 truncate text-xs font-semibold text-slate-800 dark:text-slate-200 group-hover:text-amber-600">{{ member.name }}</h3><p class="mt-1 truncate text-[11px] text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ member.character || '角色未提供' }}</p>
              </RouterLink>
            </div>
          </section>
          <MediaRecommendations :token="token" :kind="kind" :tmdb-id="tmdbId" />
          <footer class="mt-12 border-t border-slate-200 dark:border-slate-700 dark:border-slate-800 pt-5 text-[11px] leading-6 text-slate-400 dark:text-slate-500 dark:text-slate-400"><a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="hover:text-cyan-600">在 TMDB 查看完整资料 <svg class="inline h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></a><p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
        </template>
      </div>
    </main>
  </div>
</template>

<style scoped>
.media-detail {
  --md-sys-color-surface: var(--detail-light-surface);
  --md-sys-color-on-surface: var(--detail-light-on-surface);
  --md-sys-color-on-surface-variant: var(--detail-light-on-surface-variant);
  --md-sys-color-surface-container-high: var(--detail-light-surface-container-high);
  --md-sys-color-surface-container-highest: var(--detail-light-surface-container-highest);
  --md-sys-color-outline-variant: var(--detail-light-outline-variant);
  --md-sys-color-primary: var(--detail-light-primary);
  --md-sys-color-primary-container: var(--detail-light-primary-container);
  --md-sys-color-on-primary-container: var(--detail-light-on-primary-container);
  background: var(--md-sys-color-surface);
  color: var(--md-sys-color-on-surface);
}
.dark .media-detail {
  --md-sys-color-surface: var(--detail-dark-surface);
  --md-sys-color-on-surface: var(--detail-dark-on-surface);
  --md-sys-color-on-surface-variant: var(--detail-dark-on-surface-variant);
  --md-sys-color-surface-container-high: var(--detail-dark-surface-container-high);
  --md-sys-color-surface-container-highest: var(--detail-dark-surface-container-highest);
  --md-sys-color-outline-variant: var(--detail-dark-outline-variant);
  --md-sys-color-primary: var(--detail-dark-primary);
  --md-sys-color-primary-container: var(--detail-dark-primary-container);
  --md-sys-color-on-primary-container: var(--detail-dark-on-primary-container);
}
.detail-page { background: var(--md-sys-color-surface); }
.detail-content { padding-top: calc(var(--workbench-header-height) + 24px); }
.detail-back-button {
  background: var(--md-sys-color-surface-container-high);
  color: var(--md-sys-color-on-surface);
}
.detail-back-button:hover { background: var(--md-sys-color-surface-container-highest); }
.detail-backdrop {
  --backdrop-surface-start: 14%;
  --backdrop-surface-middle: 32%;
  height: 75vh;
  max-height: 800px;
  overflow: hidden;
  isolation: isolate;
}
.dark .detail-backdrop {
  --backdrop-surface-start: 34%;
  --backdrop-surface-middle: 48%;
}
.detail-backdrop img {
  /* Overscan keeps the blurred image edges outside the visible background. */
  position: absolute;
  inset: -12px;
  width: calc(100% + 24px);
  height: calc(100% + 24px);
  filter: blur(3px);
}
.detail-backdrop::before,
.detail-backdrop::after {
  content: '';
  position: absolute;
  inset: 0;
}
.detail-backdrop::before {
  z-index: 1;
  /* A broad falloff protects the reading area without outlining a panel. */
  background: radial-gradient(ellipse 70% 80% at 62% 32%,
    color-mix(in srgb, var(--md-sys-color-surface) 50%, transparent) 0%,
    color-mix(in srgb, var(--md-sys-color-surface) 44%, transparent) 32%,
    color-mix(in srgb, var(--md-sys-color-surface) 20%, transparent) 65%,
    transparent 100%);
}
.detail-backdrop::after {
  z-index: 2;
  background: linear-gradient(to bottom,
    color-mix(in srgb, var(--md-sys-color-surface) var(--backdrop-surface-start), transparent) 0%,
    color-mix(in srgb, var(--md-sys-color-surface) var(--backdrop-surface-start), transparent) 20%,
    color-mix(in srgb, var(--md-sys-color-surface) var(--backdrop-surface-middle), transparent) 40%,
    color-mix(in srgb, var(--md-sys-color-surface) 58%, transparent) 60%,
    color-mix(in srgb, var(--md-sys-color-surface) 85%, transparent) 80%,
    var(--md-sys-color-surface) 100%);
}
@media (max-width: 639px) {
  .detail-backdrop {
    height: calc(var(--workbench-header-height) + 340px);
    max-height: none;
  }
  .detail-backdrop img { filter: blur(2px); }
  .detail-backdrop::before {
    background: color-mix(in srgb, var(--md-sys-color-surface) 22%, transparent);
  }
}
.detail-poster {
  border-color: var(--md-sys-color-outline-variant);
  background: var(--md-sys-color-surface-container-high);
}
.detail-on-surface { color: var(--md-sys-color-on-surface); }
.detail-muted { color: var(--md-sys-color-on-surface-variant); }
.detail-primary { color: var(--md-sys-color-primary); }
.detail-divider { border-color: var(--md-sys-color-outline-variant); }
.detail-season {
  background: color-mix(in srgb, var(--md-sys-color-surface) 65%, var(--md-sys-color-surface-container-high));
  border-color: color-mix(in srgb, var(--md-sys-color-outline-variant) 45%, var(--md-sys-color-surface));
  color: var(--md-sys-color-on-surface);
}
.dark .detail-season {
  background: color-mix(in srgb, var(--md-sys-color-surface) 40%, var(--md-sys-color-surface-container-high));
}
.detail-season.season-selected {
  border-color: var(--md-sys-color-primary);
}
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
.detail-kind {
  background: var(--md-sys-color-primary-container);
  color: var(--md-sys-color-on-primary-container);
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
.re0-spectrum {
  background: linear-gradient(100deg, #00A76F, #FFAB00 38%, #00A76F 70%, #FFAB00);
  background-size: 260% 100%;
  background-clip: text;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  animation: re0-brand-spectrum 8s linear infinite;
}

@keyframes re0-brand-spectrum {
  from { background-position: 0 center; }
  to { background-position: -260% center; }
}

@media (prefers-reduced-motion: reduce) {
  .animate-pulse, .re0-spectrum { animation: none; }
  .transition, .transition-transform, .transition-colors { transition: none; }
  .detail-collection { transition: none; }
  .re0-spectrum { background-position: 50% center; }
}
</style>
