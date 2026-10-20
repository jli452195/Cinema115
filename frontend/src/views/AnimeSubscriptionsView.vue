<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconCalendar, IconChevronLeft, IconChevronRight, IconRefresh, IconSearch } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeCard from '../components/AnimeCard.vue'
import AnimeSubscriptionDialog from '../components/AnimeSubscriptionDialog.vue'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { animeSubscriptionsVersion, animeReturnPath, cachedSubscription, readSubscription, rememberSubscriptionQuery, rememberSubscriptionState, subscriptionState } from '../composables/useAnimeSubscriptionBrowser'
import { useAnimeLocalPolling } from '../composables/useAnimeLocalPolling'
import { animeConfigMessage } from '../lib/anime-config-api'
import { animeLocalTime, subscriptionCard, subscriptionStatus, subscriptionSummary } from '../utils/animeSubscriptionPresentation'
import type { AnimeSubscriptionPage } from '../types/animeSubscription'
import '../styles/animeDiscovery.css'
import '../styles/animeSubscriptions.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const status = computed(() => ['paused', 'enabled', 'archived', 'removed'].includes(String(route.query.status)) ? String(route.query.status) : '')
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const bangumiId = computed(() => /^[1-9]\d{0,11}$/.test(String(route.query.bangumiId)) ? String(route.query.bangumiId) : '')
const returnTo = computed(() => animeReturnPath(route.query.returnTo))
const page = computed(() => /^\d+$/.test(String(route.query.page)) ? Math.min(1000000, Math.max(1, Number(route.query.page))) : 1)
const detailID = computed(() => String(route.params.id || ''))
const input = ref(keyword.value), inputError = ref('')
const dataKey = computed(() => `subscriptions?page=${page.value}&pageSize=20${status.value ? `&status=${status.value}` : ''}${keyword.value ? `&keyword=${encodeURIComponent(keyword.value)}` : ''}${bangumiId.value ? `&bangumiId=${bangumiId.value}` : ''}`)
const result = ref<AnimeSubscriptionPage>(), loading = ref(false), error = ref(''), fetchedAt = ref(0)
const totalPages = computed(() => Math.max(1, Math.ceil((result.value?.total || 0) / 20)))
const selected = computed(() => result.value?.items.find(item => item.id === detailID.value))
let controller: AbortController | undefined, disposed = false, refreshing = false, frame: number | undefined, interaction = 0
function interacted() { interaction++ }
function query(overrides: Record<string, string | undefined> = {}) {
  return { ...(status.value ? { status: status.value } : {}), ...(keyword.value ? { q: keyword.value } : {}), ...(bangumiId.value ? { bangumiId: bangumiId.value } : {}), ...(returnTo.value ? { returnTo: returnTo.value } : {}), ...(page.value > 1 ? { page: String(page.value) } : {}), ...overrides }
}
function savePosition() { rememberSubscriptionState(`list:${dataKey.value}`, { top: window.scrollY, expanded: [] }) }
async function restorePosition(key: string) {
  await nextTick(); if (frame !== undefined) cancelAnimationFrame(frame)
  frame = requestAnimationFrame(() => { frame = undefined; if (!disposed && key === dataKey.value) window.scrollTo({ top: subscriptionState(`list:${key}`).top, behavior: 'instant' }) })
}
function filter(value: string) { void router.push({ path: '/anime/subscriptions', query: query({ status: value || undefined, page: undefined }) }) }
function changePage(value: number) { void router.push({ path: '/anime/subscriptions', query: query({ page: value > 1 ? String(value) : undefined }) }) }
function validKeyword(value: string) { return Array.from(value).length <= 128 && !/[\u0000-\u001f\u007f]/.test(value) }
function submitSearch() {
  const value = input.value.trim(); inputError.value = validKeyword(value) ? '' : '名称最多 128 个字符，不能含控制字符'
  if (inputError.value) return
  if (value === keyword.value && page.value === 1) { void load(true); return }
  void router.push({ path: '/anime/subscriptions', query: query({ q: value || undefined, page: undefined }) })
}
function openDetail(id: string) { void router.push({ path: `/anime/subscriptions/${encodeURIComponent(id)}`, query: query() }) }
async function closeDetail() {
  if (returnTo.value && window.history.state?.animeCatalogDirect === true) { await router.replace(returnTo.value); return }
  const id = detailID.value
  await router.replace({ path: '/anime/subscriptions', query: query() }); await nextTick()
  const cards = Array.from(document.querySelectorAll<HTMLElement>('[data-subscription-id]'))
  const target = cards.find(card => card.dataset.subscriptionId === id) || document.getElementById('subscription-keyword')
  target?.focus({ preventScroll: true })
}
async function load(force = false, silent = false) {
  controller?.abort(); const active = new AbortController(); controller = active
  const key = dataKey.value, interactionBefore = interaction
  if (!silent) { error.value = ''; loading.value = false }
  if (!props.token || portalUser.value?.role !== 'admin') { result.value = undefined; return }
  if (!validKeyword(keyword.value)) { result.value = undefined; error.value = '搜索名称最多 128 个字符，不能含控制字符'; return }
  const cached = cachedSubscription<AnimeSubscriptionPage>(key)
  if (!force) { result.value = cached?.data; fetchedAt.value = cached?.fetchedAt || 0; void restorePosition(key) }
  if (cached?.fresh && !force) return
  refreshing = true; if (!silent) loading.value = true
  try {
    const data = await readSubscription<AnimeSubscriptionPage>(key, props.token, active.signal)
    if (disposed || active.signal.aborted || controller !== active) return
    result.value = data; fetchedAt.value = Date.now(); error.value = ''
    const last = Math.max(1, Math.ceil(data.total / 20))
    if (page.value > last) { void router.replace({ path: detailID.value ? `/anime/subscriptions/${encodeURIComponent(detailID.value)}` : '/anime/subscriptions', query: query({ page: last > 1 ? String(last) : undefined }) }); return }
    if (!cached && !force && interactionBefore === interaction) void restorePosition(key)
  } catch (reason) { if (!disposed && !active.signal.aborted && controller === active && !(reason instanceof DOMException && reason.name === 'AbortError')) error.value = animeConfigMessage(reason) }
  finally { if (!disposed && controller === active) { loading.value = false; refreshing = false } }
}
watch(keyword, value => { input.value = value; inputError.value = '' })
watch(animeSubscriptionsVersion, () => { if (props.token && portalUser.value?.role === 'admin') void load(true, true) })
useAnimeLocalPolling(() => load(true, true), () => controller?.abort(), () => !refreshing && !detailID.value)
watch([dataKey, () => props.token, () => portalUser.value?.id, () => portalUser.value?.role], () => { void load() }, { immediate: true })
watch(() => route.fullPath, () => {
  if (route.name === 'anime-subscriptions') rememberSubscriptionQuery(Object.fromEntries(Object.entries(query()).filter((entry): entry is [string, string] => typeof entry[1] === 'string')), detailID.value)
}, { immediate: true })
onBeforeRouteUpdate(() => savePosition())
onBeforeRouteLeave(() => savePosition())
onMounted(() => { for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.addEventListener(event, interacted, { passive: true }) })
onBeforeUnmount(() => { disposed = true; controller?.abort(); if (frame !== undefined) cancelAnimationFrame(frame); for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.removeEventListener(event, interacted) })
</script>

<template>
  <ManagementLayout title="我的动漫订阅" description="管理订阅、下载进度与资源执行记录。" @logout="$emit('logout')">
    <template #actions><RouterLink :to="returnTo || '/anime'" class="as-link"><IconCalendar :size="17" aria-hidden="true" />返回选番</RouterLink><button type="button" class="anime-refresh" aria-label="刷新订阅列表" :disabled="loading" @click="load(true)"><IconRefresh :size="18" aria-hidden="true" :class="{ 'animate-spin motion-reduce:animate-none': loading }" /><span class="hidden sm:inline">刷新</span></button></template>
    <div class="anime-browser">
      <p v-if="bangumiId" class="anime-context">当前篇章的订阅配置（Bangumi {{ bangumiId }}）<RouterLink :to="{ path: '/anime/subscriptions', query: query({ bangumiId: undefined, page: undefined }) }" class="as-link">查看全部</RouterLink></p>
      <div class="as-toolbar">
        <div class="as-filters" role="group" aria-label="订阅状态"><button v-for="option in [{ value: '', label: '全部' }, { value: 'enabled', label: '已启用' }, { value: 'paused', label: '已暂停' }, { value: 'archived', label: '已归档' }, { value: 'removed', label: '已移除' }]" :key="option.value" type="button" :aria-pressed="status === option.value" @click="filter(option.value)">{{ option.label }}</button></div>
        <form class="as-search" role="search" aria-label="搜索本地订阅" @submit.prevent="submitSearch"><label class="sr-only" for="subscription-keyword">订阅名称</label><input id="subscription-keyword" v-model="input" type="search" placeholder="搜索订阅名称" :aria-invalid="!!inputError" :aria-describedby="inputError ? 'subscription-search-error' : undefined" /><button type="submit"><IconSearch :size="16" aria-hidden="true" />搜索</button></form>
      </div>
      <p v-if="inputError" id="subscription-search-error" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ inputError }}</p>
      <div class="anime-results-heading"><h2>已保存订阅<span v-if="result" class="ml-2 text-xs font-normal text-slate-500 dark:text-slate-400">共 {{ result.total }} 部</span></h2><span v-if="fetchedAt" class="text-[11px] text-slate-500 dark:text-slate-400">读取于 {{ animeLocalTime(fetchedAt) }}</span></div>
      <div class="anime-feedback" role="status" aria-live="polite"><template v-if="error"><span>{{ error }}{{ result ? '，已保留上次结果。' : '' }}</span><button type="button" :disabled="loading" @click="load(true)">重试</button></template><span v-else-if="loading && result">正在核对配置，当前内容仍可查看。</span></div>
      <div v-if="loading && !result" class="anime-grid" aria-label="正在读取订阅" aria-busy="true"><div v-for="index in 8" :key="index" class="anime-skeleton" aria-hidden="true"><div></div><span></span><span></span></div></div>
      <div v-else-if="result?.items.length" class="anime-grid" aria-label="订阅列表" :aria-busy="loading">
        <AnimeCard v-for="item in result.items" :key="item.id" :item="subscriptionCard(item)" :data-subscription-id="item.id" action-label="查看订阅配置" @select="openDetail(item.id)"><template #badge><span class="as-badge" :data-status="item.status">{{ subscriptionStatus(item.status) }}</span></template><template #meta><span class="as-card-meta" :title="subscriptionSummary(item)">{{ subscriptionSummary(item) }}</span></template></AnimeCard>
      </div>
      <div v-else-if="!loading" class="anime-empty"><IconCalendar :size="32" aria-hidden="true" /><h3>{{ error ? '暂时无法读取订阅' : keyword || status ? '没有符合条件的订阅' : '从一部想追的番剧开始' }}</h3><p>{{ keyword || status ? '可以调整名称或订阅状态。' : '从 Bangumi 选择篇章，保存主 RSS 与备用来源。' }}</p><RouterLink v-if="!error" to="/anime" class="as-link">前往选番</RouterLink></div>
      <nav v-if="result && result.total > 0" class="anime-pagination" aria-label="订阅分页"><button type="button" :disabled="page <= 1 || loading" aria-label="订阅上一页" @click="changePage(page - 1)"><IconChevronLeft :size="18" aria-hidden="true" />上一页</button><span aria-live="polite">{{ page }} / {{ totalPages }}</span><button type="button" :disabled="page >= totalPages || loading" aria-label="订阅下一页" @click="changePage(page + 1)">下一页<IconChevronRight :size="18" aria-hidden="true" /></button></nav>
    </div>
    <AnimeSubscriptionDialog v-if="detailID && token === portalToken && portalUser?.role === 'admin'" :key="`${portalUser.id}:${detailID}`" :token="token" :id="detailID" :initial="selected" :return-path="route.fullPath" @close="closeDetail" />
  </ManagementLayout>
</template>
