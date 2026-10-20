<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import { IconBookmark, IconCalendar, IconChevronLeft, IconChevronRight, IconRefresh, IconSearch, IconSettings, IconX } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeStorageSettingsDialog from '../components/AnimeStorageSettingsDialog.vue'
import GardenAnimeCard from '../components/GardenAnimeCard.vue'
import AnibtAnimeCard from '../components/AnibtAnimeCard.vue'
import MikanAnimeCard from '../components/MikanAnimeCard.vue'
import AnimeProviderButton from '../components/AnimeProviderButton.vue'
import AnimeCatalogCategories from '../components/AnimeCatalogCategories.vue'
import SelectMenu from '../components/SelectMenu.vue'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { animeScroll, rememberAnimeQuery, rememberAnimeScroll } from '../composables/useAnimeDiscovery'
import { catalogDraft, catalogQuery, catalogSnapshot, catalogSeasonsSnapshot, rememberCatalogDraft, rememberCatalogQuery, requestCatalog, requestCatalogSeasons } from '../composables/useAnimeCatalog'
import { animeCatalogStatusSnapshot, requestAnimeCatalogStatuses } from '../composables/useAnimeCatalogStatus'
import { animeSubscriptionsVersion } from '../composables/useAnimeSubscriptionBrowser'
import { animeConfigMessage } from '../lib/anime-config-api'
import { animeProviders, type AnimeCatalogStatus, type AnimeProviderID, type AnimeCatalog, type AnimeCatalogSeasons, type GardenCatalogItem, type AnibtCatalogItem, type MikanCatalogItem } from '../types/animeCatalog'
import '../styles/animeDiscovery.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const settingsOpen = ref(false)
const browserElement = ref<HTMLElement>(), viewportMinHeight = ref(0)
const route = useRoute(), router = useRouter()
function shanghaiDate() { return new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date()) }
const calendarDate = ref(shanghaiDate())
const today = computed(() => new Date(`${calendarDate.value}T12:00:00Z`).getUTCDay() || 7)
const currentSeason = computed(() => `${calendarDate.value.slice(0, 4)}-${String(Math.floor((Number(calendarDate.value.slice(5, 7)) - 1) / 3) * 3 + 1).padStart(2, '0')}`)
let calendarTimer: ReturnType<typeof setTimeout> | undefined
function updateCalendarDate() {
  clearTimeout(calendarTimer)
  calendarDate.value = shanghaiDate()
  calendarTimer = setTimeout(updateCalendarDate, Date.parse(`${calendarDate.value}T00:00:00+08:00`) + 86400000 - Date.now() + 100)
}
const mikanLabels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日', '剧场版', 'OVA']
const labels = computed(() => provider.value === 'anibt' ? [...mikanLabels.slice(0, 7), '未排播'] : provider.value === 'garden' ? [...mikanLabels.slice(0, 7), '网络动画', '韩国动画', '短片', '动态漫画', '成人'] : mikanLabels)
const provider = computed(() => typeof route.query.source === 'string' ? route.query.source : 'mikan')
const available = computed(() => provider.value === 'mikan' || provider.value === 'anibt' || provider.value === 'garden')
const providerName = computed(() => provider.value === 'anibt' ? 'AniBT' : provider.value === 'garden' ? 'Anime Garden' : '蜜柑')
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const mode = computed(() => route.query.view === 'search' || route.query.view !== 'calendar' && !!keyword.value ? 'search' : 'calendar')
const season = computed(() => /^(20\d{2}|2100)-(01|04|07|10)$/.test(String(route.query.season)) ? String(route.query.season) : currentSeason.value)
const day = computed(() => route.query.day === 'all' ? 'all' : /^(?:[1-9]|1[0-2])$/.test(String(route.query.day)) && Number(route.query.day) <= labels.value.length ? Number(route.query.day) : season.value === currentSeason.value ? today.value : 'all')
const page = computed(() => /^\d{1,3}$/.test(String(route.query.page)) ? Math.max(1, Number(route.query.page)) : 1)
const input = ref(catalogDraft(provider.value as AnimeProviderID) ?? keyword.value), inputError = ref(''), inputElement = ref<HTMLInputElement>()
const result = ref<AnimeCatalog>(), loading = ref(false), error = ref('')
const seasonIndex = ref<AnimeCatalogSeasons>(), seasonsLoading = ref(false), seasonsError = ref('')
const key = computed(() => `${provider.value}:${mode.value}:${mode.value === 'calendar' ? season.value : keyword.value}`)
const scrollKey = computed(() => `catalog:${key.value}:${mode.value === 'calendar' ? day.value : 'search'}:${page.value}`)
const allItems = computed(() => {
  if (mode.value === 'search') return result.value?.items || []
  if (day.value !== 'all') return result.value?.days.find(group => group.day === day.value)?.items || []
  const unique = new Map<string, MikanCatalogItem | AnibtCatalogItem | GardenCatalogItem>()
  for (const group of result.value?.days || []) for (const item of group.items) if (!unique.has(item.id)) unique.set(item.id, item)
  return [...unique.values()]
})
const paginated = computed(() => mode.value === 'search' || day.value === 'all')
const items = computed(() => paginated.value ? allItems.value.slice((page.value - 1) * 20, page.value * 20) : allItems.value)
const categories = computed(() => {
  const weekdays = season.value === currentSeason.value ? Array.from({ length: 7 }, (_, offset) => {
    const weekday = (today.value - 1 + offset) % 7 + 1
    return { value: String(weekday), label: labels.value[weekday - 1]!, date: new Date(Date.parse(`${calendarDate.value}T12:00:00Z`) + offset * 86400000).toISOString().slice(0, 10) }
  }) : labels.value.slice(0, 7).map((label, index) => ({ value: String(index + 1), label }))
  const extra = labels.value.slice(7).map((label, index) => ({ value: String(index + 8), label })).filter(entry => provider.value !== 'garden' || String(day.value) === entry.value || result.value?.days.some(group => group.day === Number(entry.value) && group.items.length))
  const overview = { value: 'all', label: '本季收录' }
  return season.value === currentSeason.value ? [...weekdays, overview, ...extra] : [overview, ...weekdays, ...extra]
})
const categoryEntries = computed(() => {
  if (!result.value) return categories.value
  const counts = new Map<string, number>(), total = new Set<string>()
  for (const group of result.value.days) {
    counts.set(String(group.day), group.items.length)
    for (const item of group.items) total.add(item.id)
  }
  counts.set('all', total.size)
  return categories.value.map(entry => ({ ...entry, count: counts.get(entry.value) ?? 0 }))
})
const mikanItems = computed(() => result.value?.provider === 'mikan' ? items.value as MikanCatalogItem[] : [])
const gardenItems = computed(() => result.value?.provider === 'garden' ? items.value as GardenCatalogItem[] : [])
const anibtItems = computed(() => result.value?.provider === 'anibt' ? items.value as AnibtCatalogItem[] : [])
const pages = computed(() => Math.max(1, Math.ceil(allItems.value.length / 20)))
const subscriptionStates = ref<Record<string, AnimeCatalogStatus>>({}), statusError = ref('')
let statusController: AbortController | undefined, statusPending = false
async function loadStatuses(force = false) {
  statusController?.abort(); const active = new AbortController(); statusController = active
  statusPending = false
  subscriptionStates.value = animeCatalogStatusSnapshot(provider.value as AnimeProviderID, items.value.map(item => item.id)); statusError.value = ''
  if (!available.value || !items.value.length || !props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') return
  statusPending = true
  try {
    const states = await requestAnimeCatalogStatuses(props.token, provider.value as AnimeProviderID, items.value.map(item => item.id), active.signal, force)
    if (!active.signal.aborted && statusController === active) subscriptionStates.value = states
  } catch (reason) { if (!active.signal.aborted && statusController === active && !(reason instanceof DOMException && reason.name === 'AbortError')) statusError.value = '订阅状态暂时无法核对' }
  finally { if (statusController === active) statusPending = false }
}
function seasonLabel(value: string) { return `${value.slice(0, 4)} ${({ '01': '冬', '04': '春', '07': '夏', '10': '秋' } as Record<string, string>)[value.slice(5)]}季` }
const seasons = computed(() => {
  const values = new Set(seasonIndex.value?.seasons || [])
  // A historical bookmark may be readable even when absent from a site's recommended entries.
  if (result.value?.provider === provider.value && result.value.season === season.value && mode.value === 'calendar' && result.value.days.some(group => group.items.length)) values.add(season.value)
  return [...values].sort().reverse().map(value => ({ value, label: seasonLabel(value) }))
})
const previousSeason = computed(() => seasons.value.find(option => option.value < season.value)?.value)
const nextSeason = computed(() => [...seasons.value].reverse().find(option => option.value > season.value)?.value)
function selectSeason(value?: string) { if (value) navigate({ view: 'calendar', season: value, day: 'all', page: undefined, item: undefined }) }
let seasonsController: AbortController | undefined
async function loadSeasons(refresh = false) {
  seasonsController?.abort(); const active = new AbortController(); seasonsController = active
  seasonIndex.value = undefined; seasonsError.value = ''; seasonsLoading.value = false
  if (!available.value || mode.value !== 'calendar' || !props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') return
  const source = provider.value as AnimeProviderID
  seasonIndex.value = catalogSeasonsSnapshot(source)
  if (!refresh && seasonIndex.value && seasonIndex.value.expiresAt > Date.now()) return
  seasonsLoading.value = true
  try {
    const data = await requestCatalogSeasons(props.token, source, active.signal, refresh)
    if (!disposed && seasonsController === active && !active.signal.aborted) seasonIndex.value = data
  } catch (reason) { if (!disposed && seasonsController === active && !active.signal.aborted) seasonsError.value = animeConfigMessage(reason) }
  finally { if (!disposed && seasonsController === active) seasonsLoading.value = false }
}
let controller: AbortController | undefined, disposed = false, restoreFrame = 0, interactions = 0
let inPlaceNavigation: { fullPath: string } | undefined
const interact = () => { interactions++ }
function reserveViewport(top: number) {
  const el = browserElement.value
  if (el) viewportMinHeight.value = Math.max(0, top + innerHeight - (el.getBoundingClientRect().top + window.scrollY))
}
function pushInPlace(location: RouteLocationRaw) {
  const navigation = { fullPath: router.resolve(location).fullPath }
  if (navigation.fullPath === route.fullPath) return
  reserveViewport(window.scrollY)
  cancelAnimationFrame(restoreFrame)
  inPlaceNavigation = navigation
  void router.push(location).finally(() => { if (inPlaceNavigation === navigation) inPlaceNavigation = undefined })
}
function query(overrides: Record<string, string | undefined> = {}) {
  return { source: provider.value, view: mode.value, season: season.value, day: String(day.value), ...(keyword.value ? { q: keyword.value } : {}), ...(page.value > 1 ? { page: String(page.value) } : {}), ...overrides }
}
function navigate(overrides: Record<string, string | undefined>, keepViewport = true) {
  const location = { path: '/anime', query: query(overrides) }
  if (keepViewport) pushInPlace(location)
  else void router.push(location)
}
function switchProvider(id: AnimeProviderID) {
  if (!animeProviders.find(item => item.id === id)?.available || provider.value === id) return
  pushInPlace({ path: '/anime', query: { ...catalogQuery(id), source: id } })
}
function submit() {
  const value = input.value.trim()
  inputError.value = !value ? '请输入番剧名称' : Array.from(value).length > (provider.value === 'anibt' ? 120 : 128) || /[\u0000-\u001f\u007f]/.test(value) ? `名称最多 ${provider.value === 'anibt' ? 120 : 128} 个字符，不能含控制字符` : ''
  if (inputError.value) return
  if (mode.value === 'search' && keyword.value === value) { void load(true); return }
  navigate({ view: 'search', q: value, page: undefined, item: undefined })
}
function clearSearch() { input.value = ''; inputError.value = ''; navigate({ view: 'calendar', q: undefined, page: undefined, item: undefined }); inputElement.value?.focus() }
function savePosition() { rememberAnimeScroll(scrollKey.value, window.scrollY) }
async function restorePosition(target: string) {
  cancelAnimationFrame(restoreFrame)
  if (inPlaceNavigation?.fullPath === route.fullPath) return
  const top = animeScroll(target), interaction = interactions
  await nextTick()
  if (disposed || target !== scrollKey.value || interactions !== interaction) return
  reserveViewport(top)
  await nextTick()
  restoreFrame = requestAnimationFrame(() => { if (!disposed && target === scrollKey.value && interactions === interaction) window.scrollTo({ top, behavior: 'instant' }) })
}
async function load(refresh = false) {
  controller?.abort(); const active = new AbortController(); controller = active
  const requestedKey = key.value, position = scrollKey.value, interaction = interactions, keepViewport = inPlaceNavigation?.fullPath === route.fullPath
  error.value = ''; loading.value = false
  if (!available.value || !props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') { result.value = undefined; return }
  result.value = catalogSnapshot(requestedKey)
  if (mode.value === 'search' && (!keyword.value || Array.from(keyword.value).length > (provider.value === 'anibt' ? 120 : 128) || /[\u0000-\u001f\u007f]/.test(keyword.value))) { result.value = undefined; if (keyword.value) error.value = '搜索名称格式不正确'; return }
  if (!refresh && !keepViewport) void restorePosition(position)
  if (result.value && result.value.expiresAt > Date.now() && !refresh) return
  loading.value = true
  try {
    const body = mode.value === 'calendar' ? { mode: 'calendar', season: season.value, refresh } : { mode: 'search', keyword: keyword.value, refresh }
    const data = await requestCatalog(props.token, provider.value as AnimeProviderID, requestedKey, body, active.signal)
    if (disposed || controller !== active || active.signal.aborted) return
    result.value = data
    if (!refresh && !keepViewport && interactions === interaction) void restorePosition(position)
  } catch (reason) { if (!active.signal.aborted && !disposed && controller === active) error.value = animeConfigMessage(reason) }
  finally { if (!disposed && controller === active) loading.value = false }
}
watch([key, () => props.token, portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { void load() }, { immediate: true })
watch([provider, mode, () => props.token, portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { void loadSeasons() }, { immediate: true })
watch([page, pages, paginated, result], () => {
  if (result.value && paginated.value && page.value > pages.value) void router.replace({ path: '/anime', query: query({ page: pages.value > 1 ? String(pages.value) : undefined }) })
})
watch(keyword, value => { input.value = value; inputError.value = '' })
watch(provider, value => { input.value = catalogDraft(value as AnimeProviderID) ?? keyword.value; inputError.value = '' })
watch(input, value => { if (available.value) rememberCatalogDraft(provider.value as AnimeProviderID, value) })
watch(scrollKey, value => { void restorePosition(value) })
watch([result, () => items.value.map(item => item.id).join(','), provider, animeSubscriptionsVersion, () => props.token, portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { void loadStatuses() }, { immediate: true })
function recheckStatuses() { if (document.visibilityState === 'visible') { updateCalendarDate(); if (!statusPending) void loadStatuses() } }
watch(() => route.fullPath, () => {
  if (route.name !== 'anime-discovery' || !available.value) return
  const canonical = Object.fromEntries(Object.entries(query()).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
  rememberAnimeQuery(canonical); rememberCatalogQuery(provider.value as AnimeProviderID, canonical)
}, { immediate: true })
onBeforeRouteUpdate(savePosition); onBeforeRouteLeave(savePosition)
onMounted(() => { updateCalendarDate(); document.addEventListener('visibilitychange', recheckStatuses); window.addEventListener('focus', recheckStatuses); for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.addEventListener(event, interact, { passive: true }) })
onBeforeUnmount(() => { disposed = true; clearTimeout(calendarTimer); controller?.abort(); seasonsController?.abort(); statusController?.abort(); document.removeEventListener('visibilitychange', recheckStatuses); window.removeEventListener('focus', recheckStatuses); cancelAnimationFrame(restoreFrame); for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.removeEventListener(event, interact) })
</script>

<template>
  <ManagementLayout title="动漫订阅" description="按站点找番，选择字幕组与订阅来源。" @logout="$emit('logout')">
    <template #actions><button type="button" class="anime-refresh" aria-label="动漫存储设置" @click="settingsOpen = true"><IconSettings :size="18" aria-hidden="true" /><span class="hidden sm:inline">设置</span></button><RouterLink to="/anime/subscriptions" class="anime-refresh"><IconBookmark :size="18" aria-hidden="true" />我的订阅</RouterLink><button type="button" class="anime-refresh" :disabled="!available || loading || mode === 'search' && !keyword" aria-label="刷新当前站点" @click="load(true)"><IconRefresh :size="18" aria-hidden="true" :class="{ 'animate-spin motion-reduce:animate-none': loading }" /><span class="hidden sm:inline">刷新</span></button></template>
    <div ref="browserElement" class="anime-browser" :style="viewportMinHeight ? { minHeight: `${viewportMinHeight}px` } : undefined">
      <div class="catalog-providers" role="group" aria-label="选择订阅站点"><AnimeProviderButton v-for="site in animeProviders" :key="site.id" :provider="site.id" :name="site.name" :available="site.available" :selected="provider === site.id" @select="switchProvider(site.id)" /></div>
      <template v-if="available">
        <form class="anime-search-form catalog-search" role="search" :aria-label="`搜索${providerName}番剧`" @submit.prevent="submit"><label for="anime-keyword" class="sr-only">{{ providerName }}番剧名称</label><div class="anime-search-input"><IconSearch :size="19" aria-hidden="true" /><input id="anime-keyword" ref="inputElement" v-model="input" type="search" :placeholder="`在${providerName}搜索番剧`" autocomplete="off" :aria-invalid="!!inputError" :aria-describedby="inputError ? 'catalog-input-error' : undefined" /><button v-if="input || mode === 'search'" type="button" class="anime-input-clear" aria-label="清空搜索并返回日历" @click="clearSearch"><IconX :size="17" aria-hidden="true" /></button></div><button type="submit" class="anime-search-submit">搜索</button></form>
        <p v-if="inputError" id="catalog-input-error" role="alert" class="anime-context text-red-600">{{ inputError }}</p>
        <div class="catalog-controls">
          <div v-if="mode === 'calendar'" class="catalog-quarter-nav" role="group" :aria-label="`${providerName}季度浏览`">
            <button type="button" class="catalog-quarter-step" aria-label="上一季" :title="previousSeason ? `切换到 ${seasonLabel(previousSeason)}` : '没有更早的已收录季度'" :disabled="!previousSeason" @click="selectSeason(previousSeason)"><IconChevronLeft :size="20" aria-hidden="true" /></button>
            <SelectMenu class="catalog-season" variant="anime" :model-value="season" :options="seasons" :placeholder="seasonLabel(season)" :label="`${providerName}季度`" :disabled="!seasons.length" @change="selectSeason">
              <template #trigger="{ label: selectedSeasonLabel }"><span class="catalog-season-content"><IconCalendar :size="17" class="catalog-season-icon" aria-hidden="true" /><span class="catalog-season-label">{{ selectedSeasonLabel }}</span><span v-if="season === currentSeason" class="catalog-current-season">当季</span></span></template>
            </SelectMenu>
            <button type="button" class="catalog-quarter-step" aria-label="下一季" :title="nextSeason ? `切换到 ${seasonLabel(nextSeason)}` : '没有更晚的已收录季度'" :disabled="!nextSeason" @click="selectSeason(nextSeason)"><IconChevronRight :size="20" aria-hidden="true" /></button>
          </div>
          <template v-else><button type="button" class="catalog-calendar-toggle" @click="navigate({ view: 'calendar', q: undefined, page: undefined, item: undefined })"><IconCalendar :size="17" aria-hidden="true" />季度浏览</button><span class="anime-context catalog-scope">{{ providerName }}搜索结果</span></template>
        </div>
        <p v-if="mode === 'calendar'" class="catalog-range">{{ providerName }}季度收录，范围以原站为准。<span v-if="seasonsError">季度目录暂时无法更新。<button type="button" :disabled="seasonsLoading" @click="loadSeasons(true)">重试目录</button></span><span v-else-if="seasonsLoading">正在读取季度目录…</span></p>
        <AnimeCatalogCategories v-if="mode === 'calendar'" :entries="categoryEntries" :model-value="String(day)" :label="`${providerName}日历分类`" :today="season === currentSeason ? String(today) : undefined" @select="value => navigate({ day: value, page: undefined, item: undefined })" />
        <div class="anime-results-heading"><h2>{{ mode === 'calendar' ? day === 'all' ? `${seasonLabel(season)}收录` : labels[day - 1] : keyword ? `“${keyword}”的搜索结果` : `搜索${providerName}番剧` }}<span v-if="result" class="catalog-count">{{ allItems.length }} 部</span></h2><span v-if="result" class="catalog-updated">读取于 {{ new Date(result.fetchedAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) }}</span></div>
        <div class="anime-feedback" role="status" aria-live="polite"><template v-if="error"><span>{{ error }}{{ result ? '，已保留上次结果。' : '' }}</span><button type="button" :disabled="loading" @click="load(true)">重试</button></template><span v-else-if="loading && result">正在更新{{ providerName }}资料…</span><span v-else-if="result && result.expiresAt <= Date.now()">当前为缓存资料。</span></div>
        <div v-if="loading && !result" class="anime-grid" :aria-label="`正在读取${providerName}番剧`" aria-busy="true"><div v-for="index in 8" :key="index" class="anime-skeleton"><div></div><span></span></div></div>
        <div v-else-if="items.length" class="anime-grid" :aria-label="`${providerName}番剧列表`" :aria-busy="loading"><GardenAnimeCard v-for="item in gardenItems" :key="`garden:${item.id}`" :item="item" :return-to="route.fullPath" :subscription="subscriptionStates[item.id]" /><AnibtAnimeCard v-for="item in anibtItems" :key="`anibt:${item.id}`" :item="item" :return-to="route.fullPath" :subscription="subscriptionStates[item.id]" /><MikanAnimeCard v-for="item in mikanItems" :key="item.id" :item="item" :return-to="route.fullPath" :subscription="subscriptionStates[item.id]" /></div>
        <div v-else-if="!error" class="anime-empty"><component :is="mode === 'calendar' ? IconCalendar : IconSearch" :size="30" aria-hidden="true" /><h3>{{ mode === 'calendar' ? '当前分类暂无条目' : keyword ? `${providerName}未找到相关番剧` : `在${providerName}查找想追的番剧` }}</h3><p>{{ mode === 'calendar' ? '可以切换分类、季度，或搜索番剧名称。' : '试试原名、简称或更短的关键词。' }}</p></div>
        <p v-if="statusError" class="catalog-status-error" role="status">{{ statusError }}<button type="button" @click="loadStatuses(true)">重新核对</button></p>
        <p v-if="mode === 'search' && result?.provider === 'anibt' && result.truncated" class="anime-context">AniBT 最多返回 50 部搜索结果，请缩小关键词范围。</p>
        <p v-if="mode === 'search' && result?.provider === 'garden' && result.truncated" class="anime-context">Anime Garden 最多显示 100 部搜索结果，请缩小关键词范围。</p>
        <p v-if="mode === 'calendar' && result?.provider === 'anibt' && result.truncated" class="anime-context">原站仅返回本季部分条目，当前可浏览 {{ result.items.length }} / {{ result.total }} 部。</p>
        <nav v-if="paginated && pages > 1" class="anime-pagination" :aria-label="`${providerName}${mode === 'search' ? '搜索' : '季度'}分页`"><button type="button" :disabled="page <= 1" :aria-label="`${mode === 'search' ? '搜索' : '季度'}上一页`" @click="navigate({ page: page > 2 ? String(page - 1) : undefined, item: undefined }, false)"><IconChevronLeft :size="18" aria-hidden="true" />上一页</button><span>{{ page }} / {{ pages }}</span><button type="button" :disabled="page >= pages" :aria-label="`${mode === 'search' ? '搜索' : '季度'}下一页`" @click="navigate({ page: String(page + 1), item: undefined }, false)">下一页<IconChevronRight :size="18" aria-hidden="true" /></button></nav>
      </template>
      <div v-else class="anime-empty"><h3>该站点暂未接入</h3><p>请在上方明确选择可用站点。</p></div>
    </div>
    <AnimeStorageSettingsDialog v-if="settingsOpen" :token="token" @close="settingsOpen = false" />
  </ManagementLayout>
</template>

<style scoped>
.anime-browser { overflow-anchor: none; }
.catalog-providers { display: flex; gap: 10px; flex-wrap: wrap; margin-bottom: 22px; }
.catalog-search { max-width: 740px; }
.catalog-controls { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; margin-top: 22px; }
.catalog-quarter-nav { display: inline-flex; max-width: 100%; align-items: center; gap: 6px; }
.catalog-quarter-step, .catalog-season :deep(.select-menu-trigger) { border: 1px solid rgb(148 163 184 / .25); background: #fff; box-shadow: 0 2px 0 rgb(148 163 184 / .18), 0 2px 5px rgb(15 23 42 / .025); transition: transform .2s cubic-bezier(.34, 1.56, .64, 1), border-color .16s ease, box-shadow .16s ease; }
.catalog-quarter-step { display: inline-flex; flex: 0 0 44px; width: 44px; height: 44px; align-items: center; justify-content: center; border-radius: 10px; color: rgb(var(--color-slate-600)); }
.catalog-quarter-step:disabled { opacity: .35; box-shadow: none; cursor: default; }
.catalog-quarter-nav .catalog-season { flex: 1 1 180px; width: 180px; min-width: 0; }
.catalog-season :deep(.select-menu-trigger) { gap: 7px; min-height: 44px; padding: 8px 10px; }
.catalog-season-content { display: inline-flex; min-width: 0; align-items: center; gap: 7px; white-space: nowrap; }
.catalog-season-icon { flex: none; color: #3b82f6; }
.catalog-season-label { overflow: hidden; text-overflow: ellipsis; font-size: 13px; font-weight: 650; color: rgb(var(--color-slate-800)); font-variant-numeric: tabular-nums; }
.catalog-current-season { flex: none; border-radius: 5px; padding: 2px 5px; background: #eff6ff; color: #2563eb; font-size: 11px; font-weight: 600; line-height: 16px; }
@media (hover: hover) and (pointer: fine) { .catalog-quarter-step:hover:not(:disabled), .catalog-season :deep(.select-menu-trigger:hover:not(:disabled)) { transform: translateY(-1px); border-color: rgb(96 165 250 / .55); box-shadow: 0 3px 0 rgb(148 163 184 / .14), 0 4px 8px rgb(15 23 42 / .04); } }
.catalog-quarter-step:active:not(:disabled), .catalog-season :deep(.select-menu-trigger:active:not(:disabled)) { transform: translateY(1px); box-shadow: none; }
.catalog-quarter-step:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.catalog-calendar-toggle { display: inline-flex; min-height: 44px; align-items: center; gap: 7px; border-radius: 10px; padding: 0 12px; background: rgb(148 163 184 / .08); color: rgb(var(--color-slate-600)); font-size: 12px; }
.catalog-calendar-toggle[aria-pressed="true"] { color: #2563eb; font-weight: 500; }
.catalog-season { --ae-surface: rgb(var(--color-slate-50)); --ae-ink: rgb(var(--color-slate-700)); --ae-muted: rgb(var(--color-slate-600)); --ae-inset: rgb(var(--color-slate-100)); --ae-blue: #2563eb; }
.catalog-range { display: flex; flex-wrap: wrap; align-items: center; gap: 0 12px; margin-top: 8px; min-height: 24px; font-size: 11px; color: rgb(var(--color-slate-500)); }
.catalog-range button { padding: 6px; color: #2563eb; }
.catalog-count { margin-left: 10px; font-size: 12px; color: rgb(var(--color-slate-500)); font-weight: 400; }
.catalog-updated { font-size: 11px; color: rgb(var(--color-slate-500)); }
.catalog-scope { margin: 0; }
.catalog-status-error { display: flex; align-items: center; gap: 12px; margin-top: 12px; font-size: 12px; color: rgb(var(--color-slate-500)); }
.catalog-status-error button { min-height: 44px; color: #2563eb; }
.dark .catalog-status-error button { color: #93c5fd; }
.dark .catalog-calendar-toggle[aria-pressed="true"] { color: #93c5fd; }
.dark .catalog-season { --ae-surface: rgb(var(--color-slate-800)); --ae-ink: rgb(var(--color-slate-200)); --ae-muted: rgb(var(--color-slate-300)); --ae-inset: rgb(var(--color-slate-700)); --ae-blue: #93c5fd; }
.dark .catalog-quarter-step, .dark .catalog-season :deep(.select-menu-trigger) { background: rgb(var(--color-slate-800)); border-color: rgb(148 163 184 / .25); color: rgb(var(--color-slate-300)); box-shadow: 0 2px 0 rgb(0 0 0 / .2), 0 2px 5px rgb(0 0 0 / .08); }
.dark .catalog-quarter-step:disabled { box-shadow: none; }
.dark .catalog-season-label { color: rgb(var(--color-slate-100)); }
.dark .catalog-season-icon { color: #93c5fd; }
.dark .catalog-current-season { background: rgb(59 130 246 / .14); color: #93c5fd; }
.dark .catalog-range button { color: #93c5fd; }
.catalog-calendar-toggle:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
@media (max-width: 639px) { .catalog-providers { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 7px; } }
@media (prefers-reduced-motion: reduce) { .catalog-quarter-step, .catalog-season :deep(.select-menu-trigger) { transition: none; transform: none !important; } }
</style>
