<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import WorkbenchSidebar from '../components/WorkbenchSidebar.vue'
import MediaRecommendations from '../components/MediaRecommendations.vue'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const emit = defineEmits<{ home: []; openP115: []; openTelegram: []; openLogs: []; logout: [] }>()
const router = useRouter()
interface CastMember { id: number; name: string; character: string; profileUrl: string }
interface SeasonSummary { id: number; number: number; name: string; episodeCount: number; date: string }
interface Episode { id: number; number: number; name: string; overview: string; date: string; runtime: number; rating: number; votes: number; stillUrl: string }
interface SeasonDetail { id: number; number: number; name: string; overview: string; episodes: Episode[] }
interface Detail {
  id: number; kind: 'movie' | 'tv'; title: string; originalTitle: string; overview: string; posterUrl: string
  backdropUrl: string; date: string; rating: number; votes: number; tagline: string; status: string; runtime: number
  episodeCount: number; genres: { id: number; name: string }[]; countries: string[]; cast: CastMember[]
  seasons: SeasonSummary[]; collection: { id: number; name: string } | null; imdbUrl: string; tmdbUrl: string
}
interface SeasonState { loading: boolean; error: string; data?: SeasonDetail }
const detail = ref<Detail | null>(null)
const loading = ref(true)
const error = ref('')
const failedImages = ref(new Set<string>())
const openSeasons = ref(new Set<number>())
const seasons = ref<Record<number, SeasonState>>({})
const mediaLabel = computed(() => props.kind === 'movie' ? '电影' : '剧集')
const statuses: Record<string, string> = { Released: '已上映', 'In Production': '制作中', 'Post Production': '后期制作', Planned: '计划中', Rumored: '待确认', Canceled: '已取消', 'Returning Series': '连载中', Ended: '已完结', Pilot: '试播' }
const statusLabel = computed(() => detail.value ? statuses[detail.value.status] ?? detail.value.status : '')
let detailController: AbortController | undefined
const seasonControllers = new Map<number, AbortController>()
const previousTitle = document.title

async function request(path: string, signal: AbortSignal) {
  const response = await fetch(`/api/admin/tmdb/${path}`, { signal, headers: { Authorization: `Bearer ${props.token}` } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `加载失败（HTTP ${response.status}）`)
  return data
}
function cancelRequests() {
  detailController?.abort()
  for (const controller of seasonControllers.values()) controller.abort()
  seasonControllers.clear()
}
async function loadDetail() {
  cancelRequests()
  const current = new AbortController(); detailController = current
  detail.value = null; error.value = ''; loading.value = true
  seasons.value = {}; openSeasons.value = new Set(); failedImages.value = new Set()
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
  } catch (e) {
    if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '详情加载失败'
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}
async function loadSeason(number: number) {
  if (seasons.value[number]?.loading || seasons.value[number]?.data) return
  const current = new AbortController(); seasonControllers.set(number, current)
  seasons.value[number] = { loading: true, error: '' }
  try {
    const data = await request(`tv/${props.tmdbId}/season/${number}`, current.signal)
    if (current.signal.aborted) return
    if (data.number !== number || !Array.isArray(data.episodes)) throw new Error('分集数据格式不正确')
    seasons.value[number] = { loading: false, error: '', data }
  } catch (e) {
    if (!current.signal.aborted) seasons.value[number] = { loading: false, error: e instanceof Error ? e.message : '分集加载失败' }
  } finally {
    if (seasonControllers.get(number) === current) seasonControllers.delete(number)
  }
}
function toggleSeason(number: number) {
  if (openSeasons.value.has(number)) { openSeasons.value.delete(number); return }
  openSeasons.value.add(number)
  void loadSeason(number)
}
function hasImage(url: string) { return !!url && !failedImages.value.has(url) }
function seasonName(season: SeasonSummary) { return season.number === 0 ? '特别篇' : `第 ${season.number} 季` }
// 同一个组件会复用于不同影视，路由变化时取消旧请求并清空季缓存。
watch(() => [props.kind, props.tmdbId, props.token], () => { void loadDetail() }, { immediate: true })
onBeforeUnmount(() => { cancelRequests(); document.title = previousTitle })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 md:flex-row">
    <WorkbenchSidebar active="dashboard" @home="emit('home')" @open-dashboard="router.push(`/${kind}`)" @open-p115="emit('openP115')" @open-telegram="emit('openTelegram')" @open-logs="emit('openLogs')" @logout="emit('logout')" />
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
              <a v-if="kind === 'movie' && detail.collection" :href="`https://www.themoviedb.org/collection/${detail.collection.id}?language=zh-CN`" target="_blank" rel="noopener noreferrer" class="mt-4 inline-flex max-w-full flex-wrap items-center gap-2 rounded-lg border border-sky-200 bg-white/80 px-3 py-2 text-xs text-slate-600 hover:border-sky-400"><span aria-hidden="true" class="text-sky-500">▱</span><span>{{ detail.collection.name }}</span><span class="text-sky-600">查看系列 ↗</span></a>
              <div class="mt-6 flex flex-wrap items-center gap-4 text-xs">
                <a :href="detail.tmdbUrl" target="_blank" rel="noopener noreferrer" class="flex items-baseline gap-2 text-cyan-600 hover:text-cyan-700"><span class="text-[10px] font-extrabold tracking-wider">TMDB</span><span class="text-base font-bold">{{ detail.votes > 0 ? detail.rating.toFixed(1) : '暂无评分' }}<span v-if="detail.votes > 0" class="ml-0.5 text-xs text-slate-400">/10</span></span></a>
                <span v-if="detail.votes > 0" class="text-slate-400">{{ detail.votes.toLocaleString() }} 人评价</span>
                <a v-if="detail.imdbUrl" :href="detail.imdbUrl" target="_blank" rel="noopener noreferrer" aria-label="在 IMDb 查看条目" class="rounded bg-yellow-400 px-1.5 py-0.5 font-black text-black hover:bg-yellow-300">IMDb ↗</a>
              </div>
              <div class="mt-4 flex flex-wrap gap-x-4 gap-y-2 text-xs text-slate-500"><span v-if="statusLabel">{{ statusLabel }}</span><span v-if="detail.runtime">{{ kind === 'tv' ? '每集约 ' : '' }}{{ detail.runtime }} 分钟</span><span v-if="kind === 'tv' && detail.episodeCount">共 {{ detail.episodeCount }} 集</span><span v-if="detail.countries.length">{{ detail.countries.join(' / ') }}</span></div>
              <div class="mt-7"><h2 class="text-sm font-semibold text-slate-500">剧情大纲</h2><p v-if="detail.tagline" class="mt-3 text-sm italic text-slate-500">{{ detail.tagline }}</p><p class="mt-3 whitespace-pre-line break-words text-sm leading-7 text-slate-600">{{ detail.overview || '暂无剧情简介。' }}</p></div>
            </div>
          </section>

          <section v-if="kind === 'tv'" class="mt-12" aria-label="剧集季列表">
            <div class="mb-5 flex items-baseline gap-3"><h2 class="text-lg font-bold text-slate-900">季</h2><span class="text-xs text-slate-400">{{ detail.seasons.filter(item => item.number > 0).length }} 季<span v-if="detail.seasons.some(item => item.number === 0)"> · 含特别篇</span></span></div>
            <p v-if="!detail.seasons.length" class="rounded-xl border border-slate-200 bg-white/80 p-5 text-sm text-slate-400">暂无季信息</p>
            <div class="space-y-3">
              <section v-for="season in detail.seasons" :key="season.number" class="overflow-hidden rounded-xl border border-slate-200 bg-white/90 shadow-sm">
                <button :id="`season-button-${season.number}`" class="flex w-full items-center gap-3 px-4 py-4 text-left transition hover:bg-slate-50 sm:px-5" :aria-expanded="openSeasons.has(season.number)" :aria-controls="`season-${season.number}`" @click="toggleSeason(season.number)">
                  <span class="text-sm font-semibold text-slate-900">{{ seasonName(season) }}</span><span class="rounded-full border border-slate-200 bg-slate-100 px-2.5 py-0.5 text-[11px] font-medium text-slate-600">{{ season.episodeCount }} 集</span><span v-if="season.date" class="hidden text-xs text-slate-400 sm:inline">{{ season.date }}</span><svg class="ml-auto h-4 w-4 shrink-0 text-slate-400 transition-transform" :class="{ 'rotate-180': openSeasons.has(season.number) }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m5 8 5 5 5-5" /></svg>
                </button>
                <div v-if="openSeasons.has(season.number)" :id="`season-${season.number}`" role="region" :aria-labelledby="`season-button-${season.number}`" class="border-t border-slate-100 px-4 pb-4 sm:px-5">
                  <p v-if="seasons[season.number]?.loading" role="status" class="py-6 text-sm text-slate-400">正在加载分集…</p>
                  <div v-else-if="seasons[season.number]?.error" role="alert" class="py-5 text-sm text-red-600">{{ seasons[season.number]?.error }}<button class="ml-3 underline" @click="loadSeason(season.number)">重试</button></div>
                  <template v-else-if="seasons[season.number]?.data">
                    <p v-if="seasons[season.number]?.data?.overview" class="py-4 text-sm leading-7 text-slate-500">{{ seasons[season.number]?.data?.overview }}</p>
                    <p v-if="!seasons[season.number]?.data?.episodes.length" class="py-5 text-sm text-slate-400">暂无分集信息</p>
                    <div class="max-h-[640px] overflow-y-auto overscroll-contain divide-y divide-slate-100">
                      <article v-for="episode in seasons[season.number]?.data?.episodes" :key="episode.id" class="flex gap-4 py-4">
                        <div class="hidden aspect-video w-36 shrink-0 self-start overflow-hidden rounded-lg bg-slate-100 sm:block"><img v-if="hasImage(episode.stillUrl)" :src="episode.stillUrl" :alt="episode.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(episode.stillUrl)" /><div v-else class="flex h-full items-center justify-center text-xs text-slate-400">暂无剧照</div></div>
                        <div class="min-w-0"><h3 class="text-sm font-semibold text-slate-800"><span class="mr-2 text-slate-400">{{ String(episode.number).padStart(2, '0') }}</span>{{ episode.name || `第 ${episode.number} 集` }}</h3><p class="mt-2 flex flex-wrap gap-3 text-xs text-slate-400"><span>{{ episode.date || '播出日期待定' }}</span><span v-if="episode.runtime">{{ episode.runtime }} 分钟</span><span v-if="episode.votes > 0" class="text-amber-600">★ {{ episode.rating.toFixed(1) }}</span></p><p class="mt-2 text-xs leading-6 text-slate-500">{{ episode.overview || '暂无分集简介。' }}</p></div>
                      </article>
                    </div>
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
