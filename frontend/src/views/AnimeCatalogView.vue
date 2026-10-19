<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconBookmark, IconCalendar, IconChevronLeft, IconChevronRight, IconRefresh, IconSearch, IconX } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import MikanAnimeCard from '../components/MikanAnimeCard.vue'
import MikanCatalogDialog from '../components/MikanCatalogDialog.vue'
import AnimeProviderButton from '../components/AnimeProviderButton.vue'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { animeScroll, rememberAnimeQuery, rememberAnimeScroll } from '../composables/useAnimeDiscovery'
import { catalogDraft, catalogQuery, catalogSnapshot, rememberCatalogDraft, rememberCatalogQuery, requestMikanCatalog } from '../composables/useAnimeCatalog'
import { animeConfigMessage } from '../lib/anime-config-api'
import { animeProviders, type AnimeProviderID, type MikanCatalog, type MikanCatalogItem } from '../types/animeCatalog'
import '../styles/animeDiscovery.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const now = new Date(), today = now.getDay() || 7
const currentSeason = `${now.getFullYear()}-${String(Math.floor(now.getMonth() / 3) * 3 + 1).padStart(2, '0')}`
const labels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日', '剧场版', 'OVA']
const provider = computed(() => typeof route.query.source === 'string' ? route.query.source : 'mikan')
const available = computed(() => provider.value === 'mikan')
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const mode = computed(() => route.query.view === 'search' || route.query.view !== 'calendar' && !!keyword.value ? 'search' : 'calendar')
const season = computed(() => /^(20\d{2}|2100)-(01|04|07|10)$/.test(String(route.query.season)) ? String(route.query.season) : currentSeason)
const day = computed(() => /^[1-9]$/.test(String(route.query.day)) ? Number(route.query.day) : today)
const page = computed(() => /^\d{1,3}$/.test(String(route.query.page)) ? Math.max(1, Number(route.query.page)) : 1)
const input = ref(catalogDraft('mikan') ?? keyword.value), inputError = ref(''), inputElement = ref<HTMLInputElement>()
const result = ref<MikanCatalog>(), loading = ref(false), error = ref('')
const key = computed(() => `${provider.value}:${mode.value}:${mode.value === 'calendar' ? season.value : keyword.value}`)
const scrollKey = computed(() => `catalog:${key.value}:${mode.value === 'calendar' ? day.value : page.value}`)
const allItems = computed(() => mode.value === 'calendar' ? result.value?.days.find(group => group.day === day.value)?.items || [] : result.value?.items || [])
const items = computed(() => mode.value === 'search' ? allItems.value.slice((page.value - 1) * 20, page.value * 20) : allItems.value)
const pages = computed(() => Math.max(1, Math.ceil(allItems.value.length / 20)))
const selected = computed<MikanCatalogItem | undefined>(() => {
  if (!available.value || !props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') return
  const id = typeof route.query.item === 'string' && /^[1-9]\d{0,11}$/.test(route.query.item) ? route.query.item : ''
  if (id) return allItems.value.find(item => item.id === id) || { id, title: `蜜柑条目 ${id}`, url: '', coverUrl: '', updatedDate: '' }
})
const seasons = computed(() => {
  const list = []
  for (let year = Math.max(now.getFullYear() + 1, Number(season.value.slice(0, 4))); year >= 2000; year--) {
    for (const [month, name] of [['10', '秋'], ['07', '夏'], ['04', '春'], ['01', '冬']]) list.push({ value: `${year}-${month}`, name: `${year} ${name}季` })
  }
  return list
})
let controller: AbortController | undefined, disposed = false, restoreFrame = 0, interactions = 0
const interact = () => { interactions++ }
function query(overrides: Record<string, string | undefined> = {}) {
  return { source: provider.value, view: mode.value, season: season.value, day: String(day.value), ...(keyword.value ? { q: keyword.value } : {}), ...(page.value > 1 ? { page: String(page.value) } : {}), ...(selected.value ? { item: selected.value.id } : {}), ...overrides }
}
function navigate(overrides: Record<string, string | undefined>) { void router.push({ path: '/anime', query: query(overrides) }) }
function switchProvider(id: AnimeProviderID) {
  if (!animeProviders.find(item => item.id === id)?.available || provider.value === id) return
  void router.push({ path: '/anime', query: { ...catalogQuery(id), source: id } })
}
function submit() {
  const value = input.value.trim()
  inputError.value = !value ? '请输入番剧名称' : Array.from(value).length > 128 || /[\u0000-\u001f\u007f]/.test(value) ? '名称最多 128 个字符，不能含控制字符' : ''
  if (inputError.value) return
  if (mode.value === 'search' && keyword.value === value) { void load(true); return }
  navigate({ view: 'search', q: value, page: undefined, item: undefined })
}
function clearSearch() { input.value = ''; inputError.value = ''; navigate({ view: 'calendar', q: undefined, page: undefined, item: undefined }); inputElement.value?.focus() }
function savePosition() { rememberAnimeScroll(scrollKey.value, window.scrollY) }
async function restorePosition(target: string) {
  await nextTick(); cancelAnimationFrame(restoreFrame)
  restoreFrame = requestAnimationFrame(() => { if (!disposed && target === scrollKey.value) window.scrollTo({ top: animeScroll(target), behavior: 'instant' }) })
}
async function load(refresh = false) {
  controller?.abort(); const active = new AbortController(); controller = active
  const requestedKey = key.value, position = scrollKey.value, interaction = interactions
  error.value = ''; loading.value = false
  if (!available.value || !props.token || props.token !== portalToken.value || portalUser.value?.role !== 'admin') { result.value = undefined; return }
  result.value = catalogSnapshot(requestedKey)
  if (mode.value === 'search' && (!keyword.value || Array.from(keyword.value).length > 128 || /[\u0000-\u001f\u007f]/.test(keyword.value))) { result.value = undefined; if (keyword.value) error.value = '搜索名称格式不正确'; return }
  if (!refresh) void restorePosition(position)
  if (result.value && result.value.expiresAt > Date.now() && !refresh) return
  loading.value = true
  try {
    const body = mode.value === 'calendar' ? { mode: 'calendar', season: season.value, refresh } : { mode: 'search', keyword: keyword.value, refresh }
    const data = await requestMikanCatalog(props.token, requestedKey, body, active.signal)
    if (disposed || controller !== active || active.signal.aborted) return
    result.value = data
    if (!refresh && interactions === interaction) void restorePosition(position)
    if (mode.value === 'search' && page.value > pages.value) void router.replace({ path: '/anime', query: query({ page: pages.value > 1 ? String(pages.value) : undefined }) })
  } catch (reason) { if (!active.signal.aborted && !disposed && controller === active) error.value = animeConfigMessage(reason) }
  finally { if (!disposed && controller === active) loading.value = false }
}
watch([key, () => props.token, portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { void load() }, { immediate: true })
watch(keyword, value => { input.value = value; inputError.value = '' })
watch(provider, value => { input.value = catalogDraft(value as AnimeProviderID) ?? keyword.value; inputError.value = '' })
watch(input, value => { if (available.value) rememberCatalogDraft(provider.value as AnimeProviderID, value) })
watch(scrollKey, value => { void restorePosition(value) })
watch(() => route.fullPath, () => {
  if (route.name !== 'anime-discovery' || !available.value) return
  const canonical = Object.fromEntries(Object.entries(query()).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
  rememberAnimeQuery(canonical); rememberCatalogQuery(provider.value as AnimeProviderID, canonical)
}, { immediate: true })
onBeforeRouteUpdate(savePosition); onBeforeRouteLeave(savePosition)
onMounted(() => { for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.addEventListener(event, interact, { passive: true }) })
onBeforeUnmount(() => { disposed = true; controller?.abort(); cancelAnimationFrame(restoreFrame); for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.removeEventListener(event, interact) })
</script>

<template>
  <ManagementLayout title="动漫订阅" description="按站点找番，选择字幕组与订阅来源。" @logout="$emit('logout')">
    <template #actions><RouterLink to="/anime/subscriptions" class="anime-refresh"><IconBookmark :size="18" aria-hidden="true" />我的订阅</RouterLink><button type="button" class="anime-refresh" :disabled="!available || loading || mode === 'search' && !keyword" aria-label="刷新当前站点" @click="load(true)"><IconRefresh :size="18" aria-hidden="true" :class="{ 'animate-spin motion-reduce:animate-none': loading }" /><span class="hidden sm:inline">刷新</span></button></template>
    <div class="anime-browser">
      <div class="catalog-providers" role="group" aria-label="选择订阅站点"><AnimeProviderButton v-for="site in animeProviders" :key="site.id" :provider="site.id" :name="site.name" :available="site.available" :selected="provider === site.id" @select="switchProvider(site.id)" /></div>
      <template v-if="available">
        <form class="anime-search-form catalog-search" role="search" aria-label="搜索蜜柑番剧" @submit.prevent="submit"><label for="anime-keyword" class="sr-only">蜜柑番剧名称</label><div class="anime-search-input"><IconSearch :size="19" aria-hidden="true" /><input id="anime-keyword" ref="inputElement" v-model="input" type="search" placeholder="在蜜柑搜索番剧" autocomplete="off" :aria-invalid="!!inputError" :aria-describedby="inputError ? 'catalog-input-error' : undefined" /><button v-if="input || mode === 'search'" type="button" class="anime-input-clear" aria-label="清空搜索并返回日历" @click="clearSearch"><IconX :size="17" aria-hidden="true" /></button></div><button type="submit" class="anime-search-submit">搜索</button></form>
        <p v-if="inputError" id="catalog-input-error" role="alert" class="anime-context text-red-600">{{ inputError }}</p>
        <div class="catalog-controls"><button type="button" class="catalog-calendar-toggle" :aria-pressed="mode === 'calendar'" @click="navigate({ view: 'calendar', page: undefined, item: undefined })"><IconCalendar :size="17" aria-hidden="true" />新番日历</button><label class="catalog-season"><span class="sr-only">蜜柑季度</span><select :value="season" aria-label="蜜柑季度" @change="navigate({ view: 'calendar', season: ($event.target as HTMLSelectElement).value, page: undefined, item: undefined })"><option v-for="entry in seasons" :key="entry.value" :value="entry.value">{{ entry.name }}</option></select></label><span v-if="mode === 'search'" class="anime-context catalog-scope">蜜柑搜索结果</span></div>
        <div v-if="mode === 'calendar'" class="catalog-days" role="group" aria-label="蜜柑日历分类"><button v-for="(label, index) in labels" :key="label" type="button" :aria-pressed="day === index + 1" :aria-label="`${label}${today === index + 1 ? '，今天' : ''}`" @click="navigate({ day: String(index + 1), item: undefined })">{{ label }}<span v-if="today === index + 1" class="anime-today-dot" aria-hidden="true"></span></button></div>
        <div class="anime-results-heading"><h2>{{ mode === 'calendar' ? labels[day - 1] : keyword ? `“${keyword}”的搜索结果` : '搜索蜜柑番剧' }}<span v-if="result" class="catalog-count">{{ allItems.length }} 部</span></h2><span v-if="result" class="catalog-updated">读取于 {{ new Date(result.fetchedAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) }}</span></div>
        <div class="anime-feedback" role="status" aria-live="polite"><template v-if="error"><span>{{ error }}{{ result ? '，已保留上次结果。' : '' }}</span><button type="button" :disabled="loading" @click="load(true)">重试</button></template><span v-else-if="loading && result">正在更新蜜柑资料…</span><span v-else-if="result && result.expiresAt <= Date.now()">当前为缓存资料。</span></div>
        <div v-if="loading && !result" class="anime-grid" aria-label="正在读取蜜柑番剧" aria-busy="true"><div v-for="index in 8" :key="index" class="anime-skeleton"><div></div><span></span></div></div>
        <div v-else-if="items.length" class="anime-grid" aria-label="蜜柑番剧列表" :aria-busy="loading"><MikanAnimeCard v-for="item in items" :key="item.id" :item="item" @select="navigate({ item: $event.id })" /></div>
        <div v-else-if="!error" class="anime-empty"><component :is="mode === 'calendar' ? IconCalendar : IconSearch" :size="30" aria-hidden="true" /><h3>{{ mode === 'calendar' ? '当前分类暂无条目' : keyword ? '蜜柑未找到相关番剧' : '在蜜柑查找想追的番剧' }}</h3><p>{{ mode === 'calendar' ? '可以切换分类、季度，或搜索番剧名称。' : '试试原名、简称或更短的关键词。' }}</p></div>
        <nav v-if="mode === 'search' && pages > 1" class="anime-pagination" aria-label="蜜柑搜索分页"><button type="button" :disabled="page <= 1" aria-label="搜索上一页" @click="navigate({ page: page > 2 ? String(page - 1) : undefined, item: undefined })"><IconChevronLeft :size="18" aria-hidden="true" />上一页</button><span>{{ page }} / {{ pages }}</span><button type="button" :disabled="page >= pages" aria-label="搜索下一页" @click="navigate({ page: String(page + 1), item: undefined })">下一页<IconChevronRight :size="18" aria-hidden="true" /></button></nav>
      </template>
      <div v-else class="anime-empty"><h3>该站点暂未接入</h3><p>请在上方明确选择可用站点。</p></div>
    </div>
    <MikanCatalogDialog v-if="selected" :key="`${portalUser?.id}:${selected.id}`" :token="token" :item="selected" @close="router.replace({ path: '/anime', query: query({ item: undefined }) })" />
  </ManagementLayout>
</template>

<style scoped>
.catalog-providers { display: flex; gap: 10px; flex-wrap: wrap; margin-bottom: 22px; }
.catalog-search { max-width: 740px; }
.catalog-controls { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; margin-top: 22px; }
.catalog-calendar-toggle, .catalog-season select { display: inline-flex; min-height: 42px; align-items: center; gap: 7px; border-radius: 10px; padding: 0 12px; background: rgb(148 163 184 / .08); color: rgb(var(--color-slate-600)); font-size: 12px; }
.catalog-calendar-toggle[aria-pressed="true"] { color: #2563eb; font-weight: 500; }
.catalog-season select { cursor: pointer; padding-right: 24px; }
.catalog-days { display: flex; gap: 4px; overflow-x: auto; padding-block: 10px 4px; scrollbar-width: thin; }
.catalog-days button { display: inline-flex; flex: none; gap: 5px; align-items: center; justify-content: center; min-width: 48px; min-height: 44px; padding: 0 11px; border-radius: 11px; color: rgb(var(--color-slate-500)); font-size: 12px; }
.catalog-days button[aria-pressed="true"] { background: rgb(59 130 246 / .09); color: #2563eb; font-weight: 600; }
.catalog-count { margin-left: 10px; font-size: 12px; color: rgb(var(--color-slate-500)); font-weight: 400; }
.catalog-updated { font-size: 11px; color: rgb(var(--color-slate-500)); }
.catalog-scope { margin: 0; }
.dark .catalog-days button[aria-pressed="true"], .dark .catalog-calendar-toggle[aria-pressed="true"] { color: #93c5fd; }
.dark .catalog-season select { color: rgb(var(--color-slate-300)); }
.dark .catalog-season option { background: rgb(var(--color-slate-900)); }
.catalog-season select:focus-visible { outline: 2px solid #60a5fa; outline-offset: 3px; }
@media (max-width: 639px) { .catalog-providers { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 7px; } }
@media (max-width: 480px) { .catalog-days { margin-right: -8px; } }
</style>
