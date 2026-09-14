<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import MediaRecommendations from '../components/MediaRecommendations.vue'
import EmbyMediaInfo from '../components/EmbyMediaInfo.vue'
import EmbyMediaVersions from '../components/EmbyMediaVersions.vue'
import SeasonLibraryProgress from '../components/SeasonLibraryProgress.vue'
import { useEmbyMedia } from '../composables/useEmbyMedia'
import { useTMDBSeasons, type CacheInfo, type TMDBEpisode as Episode } from '../composables/useTMDBSeasons'
import { episodeHasAired, localDateKey, seasonLibraryProgress } from '../types/seasonLibrary'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
const { media: embyMedia, episodes: embyEpisodes } = useEmbyMedia(() => props.token, () => props.kind, () => props.tmdbId)
interface CastMember { id: number; name: string; character: string; profileUrl: string }
interface SeasonSummary { id: number; number: number; name: string; episodeCount: number; date: string }
interface Detail extends CacheInfo {
  id: number; kind: 'movie' | 'tv'; title: string; originalTitle: string; overview: string; posterUrl: string
  backdropUrl: string; date: string; rating: number; votes: number; tagline: string; status: string; runtime: number
  episodeCount: number; genres: { id: number; name: string }[]; countries: string[]; cast: CastMember[]
  seasons: SeasonSummary[]; collection: { id: number; name: string } | null; imdbUrl: string; tmdbUrl: string
}
const detail = ref<Detail | null>(null)
const loading = ref(true)
const error = ref('')
const failedImages = ref(new Set<string>())
const openSeasons = ref(new Set<number>())
const visitedSeasons = ref(new Set<number>())
const { seasons, schedules, reset: resetSeasons, prime: primeSchedules, loadSeason, loadSchedule } = useTMDBSeasons(() => props.token, () => props.tmdbId)
const episodeLimits = ref<Record<number, number>>({})
const metadataRefreshing = ref(false)
const metadataError = ref('')
const metadataBusy = computed(() => metadataRefreshing.value || Object.values(schedules.value).some(state => state.loading) || Object.values(seasons.value).some(state => state.loading))
const today = ref(localDateKey())
const dateTimer = window.setInterval(() => { today.value = localDateKey() }, 60000)
const seasonProgress = computed(() => Object.fromEntries((detail.value?.seasons || []).flatMap(season => {
  const episodes = schedules.value[season.number]?.data?.episodes || seasons.value[season.number]?.data?.episodes
  return episodes ? [[season.number, seasonLibraryProgress(episodes, embyEpisodes.value[season.number] || new Set<number>(), season.episodeCount, today.value)]] : []
})))
const reliableLibrary = computed(() => embyMedia.value?.state === 'present' || embyMedia.value?.state === 'absent')
const mediaLabel = computed(() => props.kind === 'movie' ? '电影' : '剧集')
const statuses: Record<string, string> = { Released: '已上映', 'In Production': '制作中', 'Post Production': '后期制作', Planned: '计划中', Rumored: '待确认', Canceled: '已取消', 'Returning Series': '连载中', Ended: '已完结', Pilot: '试播' }
const statusLabel = computed(() => detail.value ? statuses[detail.value.status] ?? detail.value.status : '')
let detailController: AbortController | undefined
// 页面保持打开时也能获取全局预热后的新播出数据，仅查询本地缓存。
const scheduleTimer = window.setInterval(() => {
  if (!document.hidden && props.kind === 'tv' && detail.value && detailController && !metadataBusy.value) void primeSchedules(detailController.signal)
}, 300000)
const previousTitle = document.title

async function request(path: string, signal: AbortSignal) {
  const response = await fetch(`/api/admin/tmdb/${path}`, { signal, headers: { Authorization: `Bearer ${props.token}` } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `加载失败（HTTP ${response.status}）`)
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
    const [data] = await Promise.all([request(`detail/${props.kind}/${props.tmdbId}`, current.signal), props.kind === 'tv' ? primeSchedules(current.signal) : Promise.resolve()])
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
    void loadSchedule(season.number, true)
    if (visitedSeasons.value.has(season.number)) void loadSeason(season.number, true)
  }
}
function toggleSeason(number: number) {
  if (openSeasons.value.has(number)) { openSeasons.value.delete(number); return }
  openSeasons.value.add(number)
  visitedSeasons.value.add(number)
  episodeLimits.value[number] ||= 20
  void loadSeason(number)
  void loadSchedule(number)
}
function hasImage(url: string) { return !!url && !failedImages.value.has(url) }
function seasonName(season: SeasonSummary) { return season.number === 0 ? '特别篇' : `第 ${season.number} 季` }
function isEpisodeAvailable(season: number, episode: Episode) {
  return episodeHasAired(episode, today.value) && embyEpisodes.value[season]?.has(episode.number)
}
// 同一个组件会复用于不同影视，路由变化时取消旧请求并清空季缓存。
watch(() => [props.kind, props.tmdbId, props.token], () => { void loadDetail() }, { immediate: true })
onBeforeUnmount(() => { cancelRequests(); window.clearInterval(dateTimer); window.clearInterval(scheduleTimer); document.title = previousTitle })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="detail-page relative min-w-0 flex-1 overflow-hidden">
      <div v-if="detail?.backdropUrl && hasImage(detail.backdropUrl)" class="detail-backdrop pointer-events-none absolute inset-x-0 top-0" aria-hidden="true">
        <img :src="detail.backdropUrl" alt="" class="h-full w-full object-cover object-top" referrerpolicy="no-referrer" @error="failedImages.add(detail.backdropUrl)" />
      </div>
      <div class="relative mx-auto max-w-[1440px] px-5 pb-8 pt-6 md:px-8 lg:px-10">
        <nav aria-label="详情导航" class="mb-8 flex items-center justify-between text-sm text-slate-500">
          <RouterLink :to="`/${kind}`" class="inline-flex items-center gap-2 rounded-lg py-2 transition hover:text-amber-600"><span aria-hidden="true">←</span> 返回{{ mediaLabel }}看板</RouterLink>
          <span class="text-[10px] tracking-[0.2em] text-slate-400">CINEMA115 / DETAILS</span>
        </nav>

        <div v-if="loading" aria-label="正在加载影视详情" aria-busy="true" class="animate-pulse">
          <div class="grid gap-8 sm:grid-cols-[220px_minmax(0,1fr)]"><div class="mx-auto aspect-[2/3] w-48 rounded-2xl bg-slate-200 sm:w-full"></div><div class="space-y-6 py-4"><div class="h-5 w-1/3 rounded bg-slate-200"></div><div class="h-12 w-3/4 rounded bg-slate-200"></div><div class="h-5 w-1/2 rounded bg-slate-200"></div><div class="h-32 rounded-xl bg-slate-200/60"></div></div></div>
          <div class="mt-12 h-24 rounded-xl bg-slate-200/60"></div>
        </div>
        <section v-else-if="error" role="alert" class="rounded-2xl border border-red-100 bg-white/90 px-6 py-12 text-center">
          <h1 class="text-xl font-semibold text-slate-900">暂时无法加载详情</h1><p class="mt-3 text-sm text-red-600">{{ error }}</p>
          <button class="mt-6 rounded-lg bg-slate-900 px-5 py-2.5 text-sm text-white" @click="loadDetail">重新加载</button>
        </section>
        <template v-else-if="detail">
          <section aria-label="影视信息" class="grid items-start gap-7 sm:grid-cols-[200px_minmax(0,1fr)] lg:grid-cols-[255px_minmax(0,1fr)] lg:gap-12">
            <div class="mx-auto aspect-[2/3] w-48 overflow-hidden rounded-2xl border border-slate-200/80 bg-slate-100 shadow-[0_20px_40px_-15px_rgba(15,23,42,0.4)] sm:w-full">
              <img v-if="hasImage(detail.posterUrl)" :src="detail.posterUrl" :alt="detail.title" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(detail.posterUrl)" />
              <div v-else class="flex h-full items-center justify-center text-sm text-slate-400">暂无海报</div>
            </div>
            <div class="min-w-0 py-1">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs text-slate-600"><span class="rounded-full bg-amber-400 px-3 py-1 text-[11px] font-bold text-slate-950">{{ mediaLabel }}</span><span>{{ detail.date || '日期待定' }}</span><span v-if="detail.genres.length" class="border-l border-slate-300 pl-3 font-semibold text-slate-700">{{ detail.genres.map(item => item.name).join(' / ') }}</span></div>
              <h1 class="mt-4 break-words text-3xl font-bold leading-tight tracking-tight text-slate-950 lg:text-5xl">{{ detail.title }}</h1>
              <p v-if="detail.originalTitle && detail.originalTitle !== detail.title" class="mt-2 text-sm text-slate-400">{{ detail.originalTitle }}</p>
              <RouterLink v-if="kind === 'movie' && detail.collection" :to="`/collection/${detail.collection.id}`" class="mt-4 inline-flex max-w-full flex-wrap items-center gap-2 rounded-lg border border-sky-200 bg-white/80 px-3 py-2 text-xs text-slate-600 hover:border-sky-400"><span aria-hidden="true" class="text-sky-500">▱</span><span>{{ detail.collection.name }}</span><span class="text-sky-600">查看系列 →</span></RouterLink>
              <div class="mt-6 flex flex-wrap items-center gap-4 text-xs">
                <a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="flex items-baseline gap-2 text-cyan-600 hover:text-cyan-700"><span class="text-[10px] font-extrabold tracking-wider">TMDB</span><span class="text-base font-bold">{{ detail.votes > 0 ? detail.rating.toFixed(1) : '暂无评分' }}<span v-if="detail.votes > 0" class="ml-0.5 text-xs text-slate-400">/10</span></span></a>
                <span v-if="detail.votes > 0" class="text-slate-400">{{ detail.votes.toLocaleString() }} 人评价</span>
                <a v-if="detail.imdbUrl" :href="detail.imdbUrl" target="_blank" rel="noopener noreferrer" aria-label="在 IMDb 查看条目" class="rounded bg-yellow-400 px-1.5 py-0.5 font-black text-black hover:bg-yellow-300">IMDb ↗</a>
              </div>
              <div class="mt-4 flex flex-wrap gap-x-4 gap-y-2 text-xs text-slate-500"><span v-if="statusLabel">{{ statusLabel }}</span><span v-if="detail.runtime">{{ kind === 'tv' ? '每集约 ' : '' }}{{ detail.runtime }} 分钟</span><span v-if="kind === 'tv' && detail.episodeCount">共 {{ detail.episodeCount }} 集</span><span v-if="detail.countries.length">{{ detail.countries.join(' / ') }}</span></div>
              <div class="mt-7"><h2 class="text-sm font-semibold text-slate-500">剧情大纲</h2><p v-if="detail.tagline" class="mt-3 text-sm italic text-slate-500">{{ detail.tagline }}</p><p class="mt-3 whitespace-pre-line break-words text-sm leading-7 text-slate-600">{{ detail.overview || '暂无剧情简介。' }}</p></div>
            </div>
          </section>

          <EmbyMediaInfo v-if="kind === 'movie'" :token="token" :tmdb-id="tmdbId" :media="embyMedia" />
          <section v-if="kind === 'tv'" class="mt-12" aria-label="剧集季列表">
            <div class="mb-5 flex flex-wrap items-baseline gap-3"><h2 class="text-lg font-bold text-slate-900">季</h2><span class="text-xs text-slate-400">{{ detail.seasons.filter(item => item.number > 0).length }} 季<span v-if="detail.seasons.some(item => item.number === 0)"> · 含特别篇</span></span><button class="ml-auto text-xs text-slate-500 hover:text-blue-600 disabled:opacity-50" :disabled="metadataBusy" @click="refreshMetadata">{{ metadataBusy ? '更新播出信息…' : '刷新播出信息' }}</button></div>
            <p v-if="metadataError" class="mb-3 text-xs text-amber-600">{{ metadataError }}，当前保留缓存数据。</p>
            <p v-if="!detail.seasons.length" class="rounded-xl border border-slate-200 bg-white/80 p-5 text-sm text-slate-400">暂无季信息</p>
            <div class="space-y-3">
              <section v-for="season in detail.seasons" :key="season.number" class="overflow-hidden rounded-xl border border-slate-200 bg-white/90 shadow-sm">
                <button :id="`season-button-${season.number}`" class="group/season flex w-full items-center gap-3 px-4 py-4 text-left transition hover:bg-slate-50 focus-visible:outline-blue-500 sm:px-5" :aria-expanded="openSeasons.has(season.number)" :aria-controls="`season-${season.number}`" @click="toggleSeason(season.number)">
                  <span class="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-3">
                    <span class="inline-flex shrink-0 items-center gap-3"><span class="text-sm font-semibold text-slate-900">{{ seasonName(season) }}</span><span class="rounded-full border border-slate-200 bg-slate-100 px-2.5 py-0.5 text-[11px] font-medium text-slate-600">{{ season.episodeCount }} 集</span></span>
                    <SeasonLibraryProgress :key="`${tmdbId}-${season.number}`" :state="embyMedia?.state" :progress="seasonProgress[season.number]" :loading="!!schedules[season.number]?.loading" :error="schedules[season.number]?.error || ''" :stale="schedules[season.number]?.data?.cacheStale" :has-undated="!!schedules[season.number]?.data?.episodes.some(episode => !episode.date)" @load="loadSchedule(season.number)" @refresh="loadSchedule(season.number, true)" />
                  </span>
                  <svg class="h-4 w-4 shrink-0 text-slate-400 transition-transform" :class="{ 'rotate-180': openSeasons.has(season.number) }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m5 8 5 5 5-5" /></svg>
                </button>
                <div v-if="visitedSeasons.has(season.number)" v-show="openSeasons.has(season.number)" :id="`season-${season.number}`" role="region" :aria-labelledby="`season-button-${season.number}`" class="border-t border-slate-100 px-4 pb-4 sm:px-5">
                  <p v-if="seasons[season.number]?.loading && !seasons[season.number]?.data" role="status" class="py-6 text-sm text-slate-400">正在加载分集…</p>
                  <div v-if="seasons[season.number]?.error" role="alert" class="py-5 text-sm text-amber-600">{{ seasons[season.number]?.error }}<button class="ml-3 underline" @click="loadSeason(season.number, true)">重试</button></div>
                  <template v-if="seasons[season.number]?.data">
                    <p v-if="reliableLibrary && seasonProgress[season.number]?.missing.length" class="mt-4 inline-flex max-w-full items-start gap-2 rounded-2xl border border-slate-200 bg-white px-3 py-2 text-[11px] font-medium leading-5 text-slate-600 shadow-sm"><span class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-amber-300" aria-hidden="true"></span><span class="min-w-0 break-words">缺失集数：{{ seasonProgress[season.number]?.missingLabel }}</span></p>
                    <p v-if="seasons[season.number]?.data?.overview" class="py-4 text-sm leading-7 text-slate-500">{{ seasons[season.number]?.data?.overview }}</p>
                    <p v-if="!seasons[season.number]?.data?.episodes.length" class="py-5 text-sm text-slate-400">暂无分集信息</p>
                    <div class="divide-y divide-slate-200/80">
                      <article v-for="episode in seasons[season.number]?.data?.episodes.slice(0, episodeLimits[season.number] || 20)" :key="episode.id" class="min-w-0 py-6">
                        <div class="flex flex-wrap items-start justify-between gap-3"><h3 class="min-w-0 text-base font-semibold text-slate-800"><span class="mr-2 text-slate-400">{{ String(episode.number).padStart(2, '0') }}</span>{{ episode.name || `第 ${episode.number} 集` }}<span v-if="!episodeHasAired(episode, today)" class="ml-2 rounded bg-slate-100 px-2 py-0.5 text-[10px] font-normal text-slate-400">{{ episode.date ? '未播出' : '播出日期待定' }}</span><span v-else-if="isEpisodeAvailable(season.number, episode)" class="ml-2 rounded bg-emerald-50 px-2 py-0.5 text-[10px] font-normal text-emerald-700">{{ embyMedia?.state === 'stale' ? '已入库 · 待更新' : '已入库' }}</span><span v-else-if="reliableLibrary" class="ml-2 rounded bg-amber-50 px-2 py-0.5 text-[10px] font-normal text-amber-600">未入库</span></h3><span v-if="isEpisodeAvailable(season.number, episode)" class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-emerald-500 text-xs text-white" aria-hidden="true">✓</span></div>
                        <p class="mt-2 flex flex-wrap gap-3 text-xs text-slate-400"><span>{{ episode.date || '播出日期待定' }}</span><span v-if="episode.runtime">{{ episode.runtime }} 分钟</span><span v-if="episode.votes > 0" class="text-amber-600">★ {{ episode.rating.toFixed(1) }}</span></p>
                        <p class="mt-3 text-sm leading-7 text-slate-500">{{ episode.overview || '暂无分集简介。' }}</p>
                        <EmbyMediaVersions v-if="isEpisodeAvailable(season.number, episode)" :token="token" kind="tv" :tmdb-id="tmdbId" :season="season.number" :episode="episode.number" />
                        <div v-if="hasImage(episode.stillUrl)" class="mx-auto mt-5 aspect-video w-full max-w-xs overflow-hidden rounded-xl bg-slate-100"><img :src="episode.stillUrl" :alt="episode.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(episode.stillUrl)" /></div>
                      </article>
                    </div>
                    <button v-if="(seasons[season.number]?.data?.episodes.length || 0) > (episodeLimits[season.number] || 20)" class="mt-3 w-full rounded-lg border border-slate-200 py-3 text-xs text-blue-600 hover:bg-slate-50" @click="episodeLimits[season.number] = (episodeLimits[season.number] || 20) + 20">显示更多分集（已显示 {{ episodeLimits[season.number] || 20 }} / {{ seasons[season.number]?.data?.episodes.length }}）</button>
                  </template>
                </div>
              </section>
            </div>
          </section>

          <section class="mt-12" aria-label="领衔主演">
            <h2 class="mb-5 text-lg font-bold text-slate-900">领衔主演</h2>
            <p v-if="!detail.cast.length" class="text-sm text-slate-400">暂无演员信息</p>
            <div v-else class="flex gap-5 overflow-x-auto pb-5 sm:gap-7">
              <a v-for="member in detail.cast" :key="member.id" :href="`https://www.themoviedb.org/person/${member.id}?language=zh-CN`" target="_blank" rel="noopener noreferrer" class="group w-20 shrink-0 text-center sm:w-24" :title="`${member.name}${member.character ? ' · ' + member.character : ''}`">
                <div class="mx-auto h-20 w-20 overflow-hidden rounded-full border border-slate-200 bg-white shadow-sm transition group-hover:ring-2 group-hover:ring-amber-300"><img v-if="hasImage(member.profileUrl)" :src="member.profileUrl" :alt="member.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover object-[center_25%]" @error="failedImages.add(member.profileUrl)" /><div v-else class="flex h-full items-center justify-center bg-slate-100 text-xl text-slate-400">{{ member.name.slice(0, 1) }}</div></div><h3 class="mt-3 truncate text-xs font-semibold text-slate-800 group-hover:text-amber-600">{{ member.name }}</h3><p class="mt-1 truncate text-[11px] text-slate-500">{{ member.character || '角色未提供' }}</p>
              </a>
            </div>
          </section>
          <MediaRecommendations :token="token" :kind="kind" :tmdb-id="tmdbId" />
          <footer class="mt-12 border-t border-slate-200 pt-5 text-[11px] leading-6 text-slate-400"><a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="hover:text-cyan-600">在 TMDB 查看完整资料 ↗</a><p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
        </template>
      </div>
    </main>
  </div>
</template>

<style scoped>
.detail-page { background: #f8fafc; }
.detail-backdrop { height: 800px; }
.detail-backdrop img { opacity: 0.12; }
.detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }
@media (prefers-reduced-motion: reduce) {
  .animate-pulse { animation: none; }
  .transition, .transition-transform { transition: none; }
}
</style>
