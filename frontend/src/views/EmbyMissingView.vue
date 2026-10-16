<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import EmbyMissingCard from '../components/EmbyMissingCard.vue'
import EmbyMissingDetails from '../components/EmbyMissingDetails.vue'
import EmbyMissingStatus from '../components/EmbyMissingStatus.vue'
import EmbyMissingPagination from '../components/EmbyMissingPagination.vue'
import { getEmbyMissingSession, MISSING_PAGE_SIZE, type MissingMode } from '../composables/useEmbyMissingSession'
import { providePosterCache } from '../composables/useLazyPoster'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { embyRequest, type EmbyMissingPage, type EmbyMissingSeries, type EmbyMissingExclusion, type EmbyMissingExcludedPage } from '../types/emby'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const page = ref<EmbyMissingPage>(), query = ref(''), resultQuery = ref('')
const loading = ref(false), error = ref('')
const excludedMode = computed(() => route.query.view === 'excluded')
const mode = computed<MissingMode>(() => excludedMode.value ? 'excluded' : 'results')
const session = shallowRef(getEmbyMissingSession(props.token))
session.value.activate()
const excluded = ref<EmbyMissingExcludedPage>(), selected = ref<EmbyMissingSeries>()
const restoring = ref(''), notice = ref(''), noticeElement = ref<HTMLElement>()
const currentPage = computed(() => excludedMode.value ? excluded.value : page.value)
const excludedCount = computed(() => session.value.excludedCount.value ?? 0)
const pageNumber = computed(() => Math.floor((currentPage.value?.offset || 0) / MISSING_PAGE_SIZE) + 1)
const pageCount = computed(() => Math.max(1, Math.ceil((currentPage.value?.total || 0) / MISSING_PAGE_SIZE)))
const viewNav = ref<HTMLElement>()
const { indicatorStyle } = useSlidingIndicator(viewNav, '[aria-pressed="true"]', [excludedMode, excludedCount])
const detailNotices = computed(() => page.value?.notices.filter(item => item.level === 'info' || item.code === 'episodes_incomplete') || [])
const warningNotices = computed(() => page.value?.notices.filter(item => item.level === 'warning' && item.code !== 'episodes_incomplete') || [])
const lifecycle = new AbortController()
const grid = ref<HTMLElement>()
const posters = providePosterCache(() => props.token, session.value.posters)
let reader: AbortController | undefined, writer: AbortController | undefined
let displayed: { mode: MissingMode; search: string; offset: number } | undefined
let active: typeof displayed
let navigation: { top: number; draft: string } | undefined
function location(targetMode: MissingMode, search: string, offset: number) {
  return { path: '/emby/missing', query: { ...(targetMode === 'excluded' ? { view: 'excluded' } : {}), ...(search ? { q: search } : {}), ...(offset ? { offset: String(offset) } : {}) } }
}
function remember() {
  if (active) session.value.remember(active.mode, active.search, active.offset, query.value, window.scrollY)
}
function install(data: EmbyMissingPage | EmbyMissingExcludedPage, targetMode: MissingMode, search: string) {
  if (targetMode === 'excluded') excluded.value = data as EmbyMissingExcludedPage
  else page.value = data as EmbyMissingPage
  resultQuery.value = search
  displayed = { mode: targetMode, search, offset: data.offset }
  active = displayed
}
function routeQuery() { return typeof route.query.q === 'string' ? route.query.q.slice(0, 100).trim() : '' }
function routeOffset() {
  const offset = Number(route.query.offset || 0)
  return Number.isInteger(offset) && offset >= 0 && offset <= 1_000_000 ? offset : 0
}
function time(value: string) { return new Date(value).toLocaleString() }
function visiblePosters(items: EmbyMissingSeries[], targetTop = window.scrollY) {
  const element = grid.value, card = element?.querySelector<HTMLElement>('[data-missing-card]')
  if (!element || !card) return []
  const style = getComputedStyle(element), columns = style.gridTemplateColumns.split(' ').length
  const gap = parseFloat(style.rowGap) || 0, height = card.offsetHeight, top = element.getBoundingClientRect().top + window.scrollY - targetTop
  return items.filter((_item, index) => {
    const rowTop = top + Math.floor(index / columns) * (height + gap)
    return rowTop < window.innerHeight + 100 && rowTop + height > -100
  }).map(item => item.posterUrl)
}
async function load(force = false, top?: number) {
  reader?.abort()
  const current = new AbortController()
  reader = current; error.value = ''
  const search = routeQuery(), targetMode = mode.value, offset = routeOffset()
  const cached = force ? undefined : session.value.read(targetMode, search, offset)
  loading.value = !cached?.fresh
  if (cached) install(cached.data, targetMode, search)
  else if (displayed?.mode !== targetMode) {
    if (targetMode === 'excluded') excluded.value = undefined
    else page.value = undefined
  }
  const isCurrent = () => reader === current && !current.signal.aborted && !lifecycle.signal.aborted
  async function restoreScroll() {
    await nextTick()
    if (isCurrent() && top !== undefined) window.scrollTo({ top, behavior: 'instant' })
  }
  if (cached) {
    await restoreScroll()
    if (!isCurrent()) return
    session.value.remember(targetMode, search, cached.data.offset, query.value, top ?? window.scrollY)
    if (cached.fresh) return
  }
  const params = new URLSearchParams({ q: search, offset: String(offset) })
  try {
    const data: EmbyMissingPage | EmbyMissingExcludedPage = await embyRequest(props.token, `${targetMode === 'excluded' ? 'missing/exclusions' : 'missing'}?${params}`, AbortSignal.any([lifecycle.signal, current.signal]))
    if (targetMode === 'results' && isCurrent()) {
      // Keep the current page steady while only the incoming visible thumbnails decode.
      await posters.prepare(visiblePosters((data as EmbyMissingPage).items, cached ? window.scrollY : top), AbortSignal.any([lifecycle.signal, current.signal]))
    }
    if (isCurrent()) {
      session.value.write(targetMode, search, data)
      install(data, targetMode, search)
      if (!cached) await restoreScroll()
      if (!isCurrent()) return
      session.value.remember(targetMode, search, data.offset, query.value, window.scrollY)
      if (data.offset !== offset) void router.replace(location(targetMode, search, data.offset))
    }
  } catch (reason) {
    if (!current.signal.aborted && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取检测结果失败'
  } finally { if (reader === current) loading.value = false }
}
function navigate(targetMode: MissingMode, search: string, offset: number, draft: string, top: number) {
  if (restoring.value) return
  const target = location(targetMode, search, offset)
  if (router.resolve(target).fullPath === route.fullPath) return
  remember(); navigation = { top, draft }
  void router.push(target)
}
function filter(offset = 0, search = query.value) {
  const value = search.trim().slice(0, 100)
  navigate(mode.value, value, offset, value, 0)
}
function switchView(targetMode: MissingMode) {
  if (targetMode === mode.value) return
  const saved = session.value.views[targetMode]
  navigate(targetMode, saved.search, saved.offset, saved.draft, saved.top)
}
function refresh() { session.value.invalidate(); void load(true) }
function changePage(value: number) {
  if (!loading.value && value !== pageNumber.value) filter((value - 1) * MISSING_PAGE_SIZE, resultQuery.value)
}
function clearSearch() { if (!restoring.value) { query.value = ''; filter(0, '') } }
function nativeSearch() { if (!query.value && routeQuery()) clearSearch() }
async function announce(message: string) {
  notice.value = message
  await nextTick(); noticeElement.value?.focus()
}
async function removed(count: number) {
  selected.value = undefined
  await nextTick()
  await announce(`已从缺集检测排除 ${count} 个来源条目，可在“已排除”中恢复。`)
  session.value.invalidate(); excluded.value = undefined
  await load(true)
}
async function restore(item: EmbyMissingExclusion) {
  if (!excluded.value || restoring.value || loading.value) return
  const current = new AbortController(); writer = current; restoring.value = item.id; error.value = ''; notice.value = ''
  try {
    await embyRequest(props.token, 'missing/restore', AbortSignal.any([lifecycle.signal, current.signal]), { service: excluded.value.service, id: item.id })
    if (!current.signal.aborted && !lifecycle.signal.aborted) {
      session.value.invalidate(); page.value = undefined
      await announce(`已恢复“${item.title}”的检测；是否显示缺集以当前索引为准。`)
      if (excluded.value && excluded.value.items.length === 1 && excluded.value.offset > 0) {
        const offset = Math.max(0, excluded.value.offset - MISSING_PAGE_SIZE)
        await router.replace({ path: '/emby/missing', query: { view: 'excluded', ...(routeQuery() ? { q: routeQuery() } : {}), ...(offset ? { offset: String(offset) } : {}) } })
      } else await load(true)
    }
  } catch (reason) {
    if (!current.signal.aborted && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '恢复失败，请重试'
  } finally { if (writer === current) restoring.value = '' }
}
let firstRoute = true
watch(() => [props.token, route.fullPath], (values, previous) => {
  if (route.name !== 'emby-missing') return
  if (previous && values[0] !== previous[0]) {
    writer?.abort(); restoring.value = ''; notice.value = ''; displayed = undefined
    page.value = undefined; excluded.value = undefined
    active = undefined
    session.value = getEmbyMissingSession(props.token); session.value.activate()
  } else remember()
  if (firstRoute) {
    firstRoute = false
    const saved = session.value.views[session.value.lastMode]
    if (!Object.keys(route.query).length && saved.initialized) {
      const target = location(session.value.lastMode, saved.search, saved.offset)
      if (router.resolve(target).fullPath !== route.fullPath) { void router.replace(target); return }
    }
  }
  const search = routeQuery(), offset = routeOffset(), saved = session.value.views[mode.value]
  const same = saved.initialized && saved.search === search && saved.offset === offset
  const target = navigation
  navigation = undefined
  const cached = session.value.read(mode.value, search, offset)
  active = { mode: mode.value, search, offset }
  query.value = target?.draft ?? (same ? saved.draft : search)
  selected.value = undefined; notice.value = ''
  void load(false, target?.top ?? cached?.top ?? (same ? saved.top : 0))
}, { immediate: true })
onBeforeRouteLeave(() => { remember() })
onBeforeUnmount(() => { lifecycle.abort(); reader?.abort(); writer?.abort(); session.value.release() })
</script>

<template>
  <ManagementLayout class="missing-page" title="缺集检测" description="按季检查第 1 集至已入库最后一集；合并所选媒体库，不含特别篇。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <RouterLink to="/emby/library" class="inline-flex min-h-11 items-center whitespace-nowrap text-sm text-blue-600 dark:text-blue-400">媒体库同步</RouterLink>
        <button type="button" :disabled="loading || !!restoring" aria-label="刷新检测结果" class="inline-flex min-h-11 shrink-0 items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 hover:border-slate-400 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300" @click="refresh">
          <svg aria-hidden="true" class="h-4 w-4" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7A7 7 0 0 1 18 5l2 2M4 17l2 2a7 7 0 0 0 11.9-2"/></svg>
          {{ loading ? '读取中' : '刷新' }}
        </button>
    </template>
    <div class="space-y-4">
      <p v-if="notice" ref="noticeElement" role="status" tabindex="-1" class="break-words rounded-xl bg-emerald-50 px-4 py-3 text-sm leading-6 text-emerald-800 outline-none focus:ring-2 focus:ring-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200">{{ notice }}</p>
      <div v-if="error" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-red-50 p-4 text-sm leading-6 text-red-700 dark:bg-red-500/10 dark:text-red-300">
        <p>{{ currentPage ? '操作或读取失败，当前保留上次结果：' : '读取失败：' }}{{ error }}</p><button type="button" :disabled="loading || !!restoring" class="min-h-9 underline disabled:opacity-50" @click="refresh">重新读取列表</button>
      </div>
      <div class="flex min-w-0 flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
        <nav ref="viewNav" class="relative flex w-max max-w-full gap-1 rounded-xl bg-slate-100 p-1 dark:bg-slate-800/60" aria-label="检测视图">
          <div aria-hidden="true" class="pointer-events-none absolute rounded-lg bg-white shadow-[0_2px_8px_rgba(0,0,0,0.08)] dark:bg-slate-700" :style="indicatorStyle"></div>
          <button type="button" :aria-pressed="!excludedMode" :disabled="!!restoring" :class="!excludedMode ? 'font-medium text-slate-900 dark:text-white' : 'text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200'" class="relative z-10 min-h-11 rounded-lg px-4 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 focus-visible:ring-inset disabled:opacity-50" @click="switchView('results')">缺集结果</button>
          <button type="button" :aria-pressed="excludedMode" :disabled="!!restoring" :class="excludedMode ? 'font-medium text-slate-900 dark:text-white' : 'text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200'" class="relative z-10 min-h-11 rounded-lg px-4 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 focus-visible:ring-inset disabled:opacity-50" @click="switchView('excluded')">已排除 <span class="ml-1 text-xs tabular-nums opacity-60">{{ excludedCount }} 项</span></button>
        </nav>
          <form role="search" class="flex w-full min-w-0 gap-2 sm:w-80" @submit.prevent="filter()">
            <div class="relative min-w-0 flex-1">
              <input v-model="query" type="search" :aria-label="excludedMode ? '搜索已排除条目' : '搜索缺集剧集'" :disabled="!!restoring" maxlength="100" placeholder="剧名或 TMDB ID" class="min-h-11 w-full min-w-0 rounded-lg border border-slate-200 bg-white py-2.5 pl-3 pr-10 text-sm text-slate-900 outline-none focus:border-slate-400 focus:ring-2 focus:ring-slate-400/20 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 [&::-webkit-search-cancel-button]:appearance-none" @search="nativeSearch" />
              <button v-if="query" type="button" aria-label="清除搜索" class="absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-r-lg text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100" @click="clearSearch"><svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M6 18 18 6"/></svg></button>
            </div>
            <button :disabled="!!restoring" class="min-h-11 shrink-0 rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900">搜索</button>
          </form>
        </div>
      <template v-if="page?.ready || excludedMode">
        <div v-if="!excludedMode && page" class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs leading-5 text-slate-500 dark:text-slate-400" data-missing-summary>
          <div class="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-1">
            <dl class="flex flex-wrap gap-x-4 gap-y-1" aria-label="缺集统计">
              <div class="flex items-baseline gap-1.5"><dt>缺集剧</dt><dd class="font-medium tabular-nums text-slate-800 dark:text-slate-200">{{ page.summary.series.toLocaleString() }} 部</dd></div>
              <div class="flex items-baseline gap-1.5"><dt>涉及</dt><dd class="font-medium tabular-nums text-slate-800 dark:text-slate-200">{{ page.summary.seasons.toLocaleString() }} 季</dd></div>
              <div class="flex items-baseline gap-1.5"><dt>共缺</dt><dd class="font-medium tabular-nums text-slate-800 dark:text-slate-200">{{ page.summary.episodes.toLocaleString() }} 集</dd></div>
            </dl>
            <p>已检查 {{ page.checkedSeries.toLocaleString() }} 部<span class="sr-only">有正式分集的剧集</span></p>
          </div>
          <div class="flex max-w-full flex-wrap items-center gap-x-2">
            <p v-if="page.syncedAt">最近全量同步 <time :datetime="page.syncedAt">{{ time(page.syncedAt) }}</time></p>
            <EmbyMissingStatus v-if="detailNotices.length" :notices="detailNotices" />
          </div>
          <p v-if="resultQuery" class="w-full break-words">当前结果：{{ resultQuery }}</p>
        </div>
        <div v-if="!excludedMode && warningNotices.length" role="status" class="flex items-start gap-2 rounded-lg border border-slate-200 px-3 py-2 text-xs leading-6 text-slate-600 dark:border-slate-700 dark:text-slate-300">
          <svg aria-hidden="true" class="mt-1 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v4m0 4v.1"/></svg>
          <div class="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-x-3"><div><p v-for="item in warningNotices" :key="item.code">{{ item.message }}</p></div><RouterLink to="/emby/library" class="inline-flex min-h-11 shrink-0 items-center text-blue-600 dark:text-blue-400">查看媒体库同步</RouterLink></div>
        </div>
      </template>
      <template v-if="excludedMode">
        <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">只影响本地缺集检测，不修改 Emby 和网盘文件。修正 TMDB 编号并更新索引后，新绑定正常检测。<span v-if="resultQuery">当前搜索：{{ resultQuery }}</span></p>
        <p v-if="loading" role="status" class="sr-only">正在读取已排除条目</p>
        <ul v-if="excluded?.items.length" class="space-y-3" :aria-busy="loading || !!restoring" aria-label="已排除来源">
          <li v-for="item in excluded.items" :key="item.id" class="flex min-w-0 flex-col gap-3 rounded-xl border border-slate-200 bg-white p-4 sm:flex-row sm:items-center sm:justify-between dark:border-slate-700 dark:bg-slate-900" data-excluded-item>
            <div class="min-w-0 space-y-1 break-all text-xs leading-6 text-slate-500 dark:text-slate-400">
              <h2 class="text-sm font-semibold text-slate-900 dark:text-slate-100">{{ item.title }}</h2>
              <p>{{ item.tmdbId ? `TMDB ${item.tmdbId}` : '未关联 TMDB' }}，Emby Series ID：{{ item.seriesId }}</p>
              <p>来源媒体库 ID：{{ item.libraryId || '索引未记录' }}，排除时已入库 {{ item.episodeCount.toLocaleString() }} 条正式分集记录</p>
              <p>排除于 {{ time(item.excludedAt) }}</p>
            </div>
            <button type="button" :disabled="loading || !!restoring" class="min-h-11 shrink-0 rounded-lg border border-slate-200 px-4 text-sm text-blue-600 disabled:opacity-50 dark:border-slate-700 dark:text-blue-400" :aria-label="`恢复${item.title}的检测`" @click="restore(item)">{{ restoring === item.id ? '正在恢复' : '恢复检测' }}</button>
          </li>
        </ul>
        <section v-else-if="excluded && !loading" class="rounded-2xl border border-slate-200 bg-white px-5 py-14 text-center dark:border-slate-700 dark:bg-slate-900">
          <h2 class="text-sm font-medium text-slate-900 dark:text-slate-100">{{ resultQuery ? '未找到匹配的排除记录' : '暂无手动排除记录' }}</h2>
          <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ resultQuery ? `没有与“${resultQuery}”匹配的剧名或 TMDB ID。` : '需要时可从缺集明细中手动排除误刮削或残留条目。' }}</p>
          <button v-if="resultQuery" type="button" class="mt-4 min-h-11 px-3 text-sm text-blue-600 dark:text-blue-400" @click="clearSearch">清除搜索条件</button>
          <button type="button" class="mt-4 min-h-11 px-3 text-sm text-blue-600 dark:text-blue-400" @click="switchView('results')">返回缺集检测</button>
        </section>
      </template>
      <p v-else-if="loading && !page" role="status" class="py-16 text-center text-sm text-slate-500 dark:text-slate-400">正在读取媒体库索引</p>
      <section v-else-if="page && !page.ready" class="rounded-2xl border border-slate-200 bg-white px-5 py-14 text-center dark:border-slate-700 dark:bg-slate-900">
        <h2 class="text-sm font-medium text-slate-900 dark:text-slate-100">{{ page.configured ? '当前媒体库索引尚未建立' : '尚未配置需要同步的媒体库' }}</h2>
        <p class="mx-auto mt-2 max-w-lg text-sm leading-6 text-slate-500 dark:text-slate-400">{{ page.configured ? '请完成当前所选媒体库的全量同步，再查看缺集结果。' : '请先连接 Emby 并选择需要同步的媒体库。' }}</p>
        <RouterLink to="/emby/library" class="mt-5 inline-flex min-h-10 items-center text-sm text-blue-600 dark:text-blue-400">{{ page.configured ? '查看同步状态' : '配置媒体库同步' }}</RouterLink>
      </section>
      <template v-else-if="page?.ready">
        <p role="status" class="sr-only">{{ loading ? '正在刷新检测结果' : '' }}</p>
        <div v-if="page.items.length" ref="grid" class="grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3" :aria-busy="loading" data-missing-grid>
          <EmbyMissingCard v-for="item in page.items" :key="item.key" :item="item" :token="token" @details="selected = item" />
        </div>
        <section v-else class="rounded-2xl border border-slate-200 bg-white px-5 py-14 text-center dark:border-slate-700 dark:bg-slate-900">
          <h2 class="text-sm font-medium text-slate-900 dark:text-slate-100">{{ resultQuery ? '未找到匹配的缺集剧集' : page.checkedSeries ? '当前索引未检测到缺集' : '当前索引没有可检测的正式分集' }}</h2>
          <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ resultQuery ? `没有与“${resultQuery}”匹配的剧名或 TMDB ID。` : page.checkedSeries ? '检测范围为各季第 1 集至已入库最后一集。' : '需有有效季号和集号的已入库分集才能检测。' }}</p>
          <button v-if="resultQuery" type="button" class="mt-4 min-h-10 text-sm text-blue-600 dark:text-blue-400" @click="clearSearch">清除搜索条件</button>
        </section>
      </template>
      <EmbyMissingPagination v-if="currentPage && pageCount > 1" :page="pageNumber" :pages="pageCount" :disabled="loading || !!restoring" :label="excludedMode ? '排除记录分页' : '缺集结果分页'" @change="changePage" />
    </div>
    <EmbyMissingDetails v-if="selected" :key="selected.key" :item="selected" :token="token" @close="selected = undefined" @removed="removed" />
  </ManagementLayout>
</template>

<style scoped>
.missing-page :deep(.management-heading) { align-items: center; margin-bottom: 20px; }
.missing-page :deep(.management-heading > p) { max-width: none; }
@media (max-width: 639px) {
  .missing-page :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); gap: 8px; }
  .missing-page :deep(.management-heading > p) { grid-column: 1; grid-row: 2; font-size: 12px; }
  .missing-page :deep(.management-heading > div) { grid-row: 3; }
}
</style>
