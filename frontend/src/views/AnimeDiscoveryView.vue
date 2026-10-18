<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconCalendar, IconChevronLeft, IconChevronRight, IconRefresh, IconSearch, IconX, IconBookmark } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeCard from '../components/AnimeCard.vue'
import AnimeDetailDialog from '../components/AnimeDetailDialog.vue'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { animeCooldownMessage, animeCooldownUntil, animeDetailState, animeFailure, animeScroll, cachedAnime, rememberAnimeQuery, rememberAnimeScroll, requestAnime } from '../composables/useAnimeDiscovery'
import type { AnimeCalendar, AnimeCardItem, AnimeSearch, AnimeSubjectResult } from '../types/anime'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const today = new Date().getDay() || 7
const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const mode = computed(() => route.query.view === 'calendar' ? 'calendar' : route.query.view === 'search' || keyword.value ? 'search' : 'calendar')
const weekday = computed(() => /^[1-7]$/.test(String(route.query.day)) ? Number(route.query.day) : today)
const page = computed(() => /^\d+$/.test(String(route.query.page)) ? Math.min(51, Math.max(1, Number(route.query.page))) : 1)
const input = ref(keyword.value), inputError = ref(''), keywordInput = ref<HTMLInputElement>()
const calendar = ref<AnimeCalendar>(), search = ref<AnimeSearch>()
const loading = ref(false), error = ref('')
const dataKey = computed(() => mode.value === 'calendar' ? 'calendar' : `search:${keyword.value}:${page.value}`)
const scrollKey = computed(() => mode.value === 'calendar' ? `calendar:${weekday.value}` : dataKey.value)
const result = computed(() => mode.value === 'calendar' ? calendar.value : search.value)
const items = computed(() => mode.value === 'calendar' ? calendar.value?.days.find(day => day.weekday === weekday.value)?.items || [] : search.value?.items || [])
const selected = computed<AnimeCardItem | undefined>(() => {
  if (!props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') return
  const id = typeof route.query.subject === 'string' && /^[1-9]\d*$/.test(route.query.subject) ? route.query.subject : ''
  if (!id) return
  return animeDetailState(id)?.current || cachedAnime<AnimeSubjectResult>(`subject:${id}`)?.data.subject || items.value.find(item => item.id === id) || { id, title: `Bangumi ${id}`, originalTitle: '', coverUrl: '', date: '', weekday: null, url: '' }
})
const sourceStale = computed(() => !!result.value && (result.value.stale || result.value.expiresAt <= Date.now()))
const totalPages = computed(() => Math.min(51, Math.max(1, Math.ceil((search.value?.total || 0) / 20))))
const weekContainer = ref<HTMLElement>()
const { indicatorStyle } = useSlidingIndicator(weekContainer, '[aria-pressed="true"]', [weekday])
let controller: AbortController | undefined, disposed = false
let restoreFrame: number | undefined
let interactionVersion = 0
function markInteraction() { interactionVersion++ }

function query(overrides: Record<string, string | undefined> = {}) {
  return { view: mode.value, day: String(weekday.value), ...(keyword.value ? { q: keyword.value } : {}), ...(page.value > 1 ? { page: String(page.value) } : {}), ...(selected.value ? { subject: selected.value.id } : {}), ...overrides }
}
function validKeyword(value: string) {
  return Array.from(value).length <= 128 && !/[\u0000-\u001f\u007f]/.test(value)
}
function submitSearch() {
  if (animeCooldownUntil.value > Date.now()) { inputError.value = animeCooldownMessage(); return }
  const value = input.value.trim()
  inputError.value = !value ? '请输入番剧名称' : !validKeyword(value) ? '名称最多 128 个字符，不能含控制字符' : ''
  if (inputError.value) return
  if (mode.value === 'search' && value === keyword.value && page.value === 1) { void load(true); return }
  void router.push({ path: '/anime', query: query({ view: 'search', q: value, page: undefined, subject: undefined }) })
}
function switchMode(value: 'calendar' | 'search') { void router.push({ path: '/anime', query: query({ view: value, subject: undefined }) }) }
function selectDay(day: number) { void router.push({ path: '/anime', query: query({ day: String(day), subject: undefined }) }) }
function changePage(value: number) { void router.push({ path: '/anime', query: query({ page: value > 1 ? String(value) : undefined, subject: undefined }) }) }
function openDetail(item: AnimeCardItem, replace = false) { void router[replace ? 'replace' : 'push']({ path: '/anime', query: query({ subject: item.id }) }) }
function closeDetail() { void router.replace({ path: '/anime', query: query({ subject: undefined }) }) }
function savePosition() { rememberAnimeScroll(scrollKey.value, window.scrollY) }
async function restorePosition(key: string) {
  await nextTick()
  if (restoreFrame !== undefined) cancelAnimationFrame(restoreFrame)
  restoreFrame = requestAnimationFrame(() => {
    restoreFrame = undefined
    if (!disposed && key === scrollKey.value) window.scrollTo({ top: animeScroll(key), behavior: 'instant' })
  })
}
async function load(force = false) {
  controller?.abort(); const active = new AbortController(); controller = active
  const key = dataKey.value, view = mode.value, q = keyword.value, offset = (page.value - 1) * 20
  loading.value = false; error.value = ''
  if (!props.token || portalUser.value?.role !== 'admin') { calendar.value = undefined; search.value = undefined; return }
  if (view === 'search' && (!q || !validKeyword(q))) {
    search.value = undefined
    if (q) error.value = '搜索名称最多 128 个字符，不能含控制字符'
    void restorePosition(scrollKey.value)
    return
  }
  const cached = cachedAnime<AnimeCalendar | AnimeSearch>(key)
  if (view === 'calendar') calendar.value = cached?.data as AnimeCalendar | undefined || (force ? calendar.value : undefined)
  else search.value = cached?.data as AnimeSearch | undefined || (force ? search.value : undefined)
  const requestedScrollKey = scrollKey.value, requestedTop = animeScroll(requestedScrollKey), interaction = interactionVersion
  if (!force) void restorePosition(requestedScrollKey)
  if (cached?.fresh && !force) return
  loading.value = true
  try {
    const data = await requestAnime<AnimeCalendar | AnimeSearch>(key, view === 'calendar' ? 'calendar' : 'search', props.token, active.signal, view === 'search' ? { keyword: q, sort: 'match', limit: 20, offset } : undefined)
    if (disposed || active.signal.aborted || controller !== active) return
    if (view === 'calendar') calendar.value = data as AnimeCalendar
    else search.value = data as AnimeSearch
    // Only finish an uncached saved-position restore if the user has not moved on.
    if (!cached && !force && requestedTop > 0 && interaction === interactionVersion) void restorePosition(requestedScrollKey)
    if (view === 'search') {
      const lastPage = Math.min(51, Math.max(1, Math.ceil((data as AnimeSearch).total / 20)))
      if (page.value > lastPage) void router.replace({ path: '/anime', query: query({ page: lastPage > 1 ? String(lastPage) : undefined }) })
    }
  } catch (e) {
    if (!disposed && !active.signal.aborted && controller === active) error.value = animeFailure(e)
  } finally { if (!disposed && controller === active) loading.value = false }
}

watch(keyword, value => { input.value = value; inputError.value = '' })
watch([dataKey, () => props.token, () => portalUser.value?.id, () => portalUser.value?.role], () => {
  void load()
}, { immediate: true })
watch(scrollKey, key => { void restorePosition(key) })
watch(() => route.fullPath, () => {
  if (route.name !== 'anime-discovery') return
  const canonical = Object.fromEntries(Object.entries(query()).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
  rememberAnimeQuery(canonical)
}, { immediate: true })
onBeforeRouteUpdate(() => { savePosition() })
onBeforeRouteLeave(() => { savePosition() })
onMounted(() => { for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.addEventListener(event, markInteraction, { passive: true }) })
onBeforeUnmount(() => {
  disposed = true; controller?.abort()
  for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.removeEventListener(event, markInteraction)
  if (restoreFrame !== undefined) cancelAnimationFrame(restoreFrame)
})
</script>

<template>
  <ManagementLayout title="动漫订阅" description="从 Bangumi 找到想追的番剧，先确认作品与篇章。" @logout="$emit('logout')">
    <template #actions><RouterLink to="/anime/subscriptions" class="anime-refresh"><IconBookmark :size="18" aria-hidden="true" />我的订阅</RouterLink><button type="button" class="anime-refresh" :disabled="loading || !!animeCooldownUntil || mode === 'search' && !keyword" :aria-label="mode === 'calendar' ? '刷新新番日历' : '刷新搜索结果'" @click="load(true)"><IconRefresh :size="18" aria-hidden="true" :class="{ 'animate-spin motion-reduce:animate-none': loading }" /><span class="hidden sm:inline">刷新</span></button></template>
    <div class="anime-browser">
      <div class="anime-browser-toolbar">
        <div class="anime-mode-switch" aria-label="找番方式">
          <button type="button" :aria-pressed="mode === 'calendar'" @click="switchMode('calendar')"><IconCalendar :size="17" aria-hidden="true" />新番日历</button>
          <button type="button" :aria-pressed="mode === 'search'" @click="switchMode('search')"><IconSearch :size="17" aria-hidden="true" />历史搜索</button>
        </div>
        <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">Bangumi 放送资料</p>
      </div>
      <template v-if="mode === 'calendar'">
        <div ref="weekContainer" class="anime-weekdays" aria-label="选择放送星期">
          <span class="anime-weekday-indicator" :style="indicatorStyle" aria-hidden="true"></span>
          <button v-for="(label, index) in weekdays" :key="label" type="button" :aria-pressed="weekday === index + 1" :aria-label="`${label}${today === index + 1 ? '，今天' : ''}`" @click="selectDay(index + 1)"><span>{{ label }}</span><span v-if="today === index + 1" class="anime-today-dot" aria-hidden="true"></span></button>
        </div>
        <p class="anime-context">按原始放送星期展示，不代表字幕组更新时间或资源可下载。</p>
      </template>
      <template v-else>
        <form class="anime-search-form" role="search" aria-label="搜索 Bangumi 番剧" @submit.prevent="submitSearch">
          <label for="anime-keyword" class="sr-only">番剧名称</label>
          <div class="anime-search-input"><IconSearch :size="19" aria-hidden="true" /><input id="anime-keyword" ref="keywordInput" v-model="input" type="search" placeholder="搜索历史番剧的中文名或原名" autocomplete="off" :aria-invalid="!!inputError" :aria-describedby="inputError ? 'anime-input-error' : undefined" /><button v-if="input" type="button" class="anime-input-clear" aria-label="清空搜索输入" @click="input = ''; inputError = ''; keywordInput?.focus()"><IconX :size="17" aria-hidden="true" /></button></div>
          <button type="submit" class="anime-search-submit" :disabled="!!animeCooldownUntil">搜索</button>
        </form>
        <p v-if="inputError" id="anime-input-error" role="alert" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ inputError }}</p>
        <p class="anime-context">按名称检索 Bangumi 已收录作品，可查找不在当前周历中的篇章。</p>
      </template>

      <div class="anime-results-heading">
        <h2>{{ mode === 'calendar' ? `${weekdays[weekday - 1]}放送` : keyword ? `“${keyword}”的搜索结果` : '查找历史番剧' }}<span v-if="result" class="ml-2 text-xs font-normal text-slate-500 dark:text-slate-400">{{ mode === 'calendar' ? `${items.length} 部` : `共 ${search?.total || 0} 部` }}</span></h2>
        <span v-if="result" class="text-[11px] text-slate-500 dark:text-slate-400">{{ sourceStale ? '缓存于' : '更新于' }} {{ new Date(result.fetchedAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) }}</span>
      </div>
      <div class="anime-feedback" role="status" aria-live="polite">
        <template v-if="error"><span>{{ error }}{{ result ? '，已保留上次结果。' : '' }}</span><button type="button" :disabled="loading || !!animeCooldownUntil" @click="load(true)">重试</button></template>
        <span v-else-if="loading && result">正在更新资料，当前内容仍可查看。</span>
        <span v-else-if="sourceStale">正在使用缓存资料，暂未取得最新内容。</span>
      </div>
      <div v-if="loading && !result" class="anime-grid" aria-label="正在读取番剧" aria-busy="true">
        <div v-for="index in 8" :key="index" class="anime-skeleton" aria-hidden="true"><div></div><span></span><span></span></div>
      </div>
      <div v-else-if="items.length" class="anime-grid" :aria-label="mode === 'calendar' ? '放送番剧列表' : '搜索结果列表'" :aria-busy="loading">
        <AnimeCard v-for="item in items" :key="item.id" :item="item" @select="openDetail($event)" />
      </div>
      <div v-else-if="!error" class="anime-empty">
        <component :is="mode === 'calendar' ? IconCalendar : IconSearch" :size="32" aria-hidden="true" />
        <h3>{{ mode === 'calendar' ? '这一天暂未收录放送作品' : keyword ? '没有找到相关作品' : '从名称开始，找到对应篇章' }}</h3>
        <p>{{ mode === 'calendar' ? '可以切换星期，或到历史搜索查找。' : keyword ? '试试原名、简称或更短的关键词。' : '支持中文名与原名，输入后点击搜索。' }}</p>
        <button v-if="mode === 'calendar'" type="button" @click="switchMode('search')">搜索历史番剧</button>
      </div>
      <div v-else-if="!result" class="anime-empty"><component :is="mode === 'calendar' ? IconCalendar : IconSearch" :size="32" aria-hidden="true" /><h3>暂时无法读取{{ mode === 'calendar' ? '新番日历' : '搜索结果' }}</h3><p>可以稍后重试，或切换找番方式。</p></div>
      <nav v-if="mode === 'search' && search && search.total > 0" class="anime-pagination" aria-label="番剧搜索分页">
        <button type="button" :disabled="page <= 1 || loading" aria-label="搜索上一页" @click="changePage(page - 1)"><IconChevronLeft :size="18" aria-hidden="true" /><span>上一页</span></button>
        <span aria-live="polite">{{ page }} / {{ totalPages }}</span>
        <button type="button" :disabled="page >= totalPages || loading" aria-label="搜索下一页" @click="changePage(page + 1)"><span>下一页</span><IconChevronRight :size="18" aria-hidden="true" /></button>
      </nav>
      <p v-if="mode === 'search' && (search?.total || 0) > 1020" class="anime-context text-center">当前可翻阅前 1020 条结果，建议补充作品名缩小范围。</p>
    </div>
    <AnimeDetailDialog v-if="selected" :key="portalUser?.id" :token="token" :item="selected" @close="closeDetail" @navigate="openDetail($event, true)" />
  </ManagementLayout>
</template>

<style src="../styles/animeDiscovery.css"></style>
