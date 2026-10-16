<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import EmbyMissingCard from '../components/EmbyMissingCard.vue'
import EmbyMissingDetails from '../components/EmbyMissingDetails.vue'
import { embyRequest, type EmbyMissingPage, type EmbyMissingSeries, type EmbyMissingExclusion, type EmbyMissingExcludedPage } from '../types/emby'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const page = ref<EmbyMissingPage>(), query = ref(''), resultQuery = ref('')
const loading = ref(false), error = ref('')
const excludedMode = computed(() => route.query.view === 'excluded')
const excluded = ref<EmbyMissingExcludedPage>(), selected = ref<EmbyMissingSeries>()
const restoring = ref(''), notice = ref(''), noticeElement = ref<HTMLElement>()
const currentPage = computed(() => excludedMode.value ? excluded.value : page.value)
const excludedCount = computed(() => excludedMode.value ? excluded.value?.count || 0 : page.value?.excludedCount || 0)
const lifecycle = new AbortController()
let reader: AbortController | undefined, writer: AbortController | undefined
function routeQuery() { return typeof route.query.q === 'string' ? route.query.q.slice(0, 100).trim() : '' }
function routeOffset() {
  const offset = Number(route.query.offset || 0)
  return Number.isInteger(offset) && offset >= 0 && offset <= 1_000_000 ? offset : 0
}
function time(value: string) { return new Date(value).toLocaleString() }
async function load() {
  reader?.abort()
  const current = new AbortController()
  reader = current; loading.value = true; error.value = ''
  const search = routeQuery(), mode = excludedMode.value
  const params = new URLSearchParams({ q: search, offset: String(routeOffset()) })
  try {
    const data = await embyRequest(props.token, `${mode ? 'missing/exclusions' : 'missing'}?${params}`, AbortSignal.any([lifecycle.signal, current.signal]))
    if (!current.signal.aborted && !lifecycle.signal.aborted) {
      if (mode) excluded.value = data as EmbyMissingExcludedPage
      else page.value = data as EmbyMissingPage
      resultQuery.value = search
    }
  } catch (reason) {
    if (!current.signal.aborted && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取检测结果失败'
  } finally { if (reader === current) loading.value = false }
}
function filter(offset = 0, search = query.value, mode = excludedMode.value) {
  if (restoring.value) return
  const target = { path: '/emby/missing', query: { ...(mode ? { view: 'excluded' } : {}), ...(search.trim() ? { q: search.trim() } : {}), ...(offset ? { offset: String(offset) } : {}) } }
  if (router.resolve(target).fullPath === route.fullPath) void load()
  else void router.push(target)
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
  await load()
}
async function restore(item: EmbyMissingExclusion) {
  if (!excluded.value || restoring.value || loading.value) return
  const current = new AbortController(); writer = current; restoring.value = item.id; error.value = ''; notice.value = ''
  try {
    await embyRequest(props.token, 'missing/restore', AbortSignal.any([lifecycle.signal, current.signal]), { service: excluded.value.service, id: item.id })
    if (!current.signal.aborted && !lifecycle.signal.aborted) {
      await announce(`已恢复“${item.title}”的检测；是否显示缺集以当前索引为准。`)
      if (excluded.value && excluded.value.items.length === 1 && excluded.value.offset > 0) {
        const offset = Math.max(0, excluded.value.offset - 24)
        await router.replace({ path: '/emby/missing', query: { view: 'excluded', ...(routeQuery() ? { q: routeQuery() } : {}), ...(offset ? { offset: String(offset) } : {}) } })
      } else await load()
    }
  } catch (reason) {
    if (!current.signal.aborted && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '恢复失败，请重试'
  } finally { if (writer === current) restoring.value = '' }
}
watch(() => [props.token, route.query.q, route.query.offset, excludedMode.value], (values, previous) => {
  if (previous && (values[0] !== previous[0] || values[3] !== previous[3])) { page.value = undefined; excluded.value = undefined }
  if (previous && values[0] !== previous[0]) { writer?.abort(); restoring.value = ''; notice.value = '' }
  selected.value = undefined
  query.value = routeQuery(); void load()
}, { immediate: true })
onBeforeUnmount(() => { lifecycle.abort(); reader?.abort(); writer?.abort() })
</script>

<template>
  <ManagementLayout title="缺集检测" description="按季检查第 1 集至已入库最后一集，列出其中缺失的集号。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions><RouterLink to="/emby/library" class="text-sm text-blue-600 dark:text-blue-400">媒体库同步</RouterLink></template>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">已选媒体库合并去重，特别篇不参与检测。</p>
        <button type="button" :disabled="loading || !!restoring" aria-label="刷新检测结果" class="inline-flex min-h-10 shrink-0 items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 text-sm text-slate-700 hover:border-amber-300 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300" @click="load">
          <svg aria-hidden="true" class="h-4 w-4" :class="loading ? 'animate-spin motion-reduce:animate-none' : ''" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 7A7 7 0 0 1 18 5l2 2M4 17l2 2a7 7 0 0 0 11.9-2"/></svg>
          {{ loading ? '读取中' : '刷新' }}
        </button>
      </div>
      <div class="flex flex-wrap items-center gap-4 text-sm" aria-label="检测视图">
        <button type="button" :aria-pressed="!excludedMode" :disabled="!!restoring" :class="!excludedMode ? 'font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-500 underline underline-offset-4 dark:text-slate-400'" class="min-h-11 disabled:opacity-50" @click="filter(0, routeQuery(), false)">缺集结果</button>
        <button type="button" :aria-pressed="excludedMode" :disabled="!!restoring" :class="excludedMode ? 'font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-500 underline underline-offset-4 dark:text-slate-400'" class="min-h-11 disabled:opacity-50" @click="filter(0, routeQuery(), true)">已排除 {{ excludedCount }} 项</button>
      </div>
      <p v-if="notice" ref="noticeElement" role="status" tabindex="-1" class="break-words rounded-xl bg-emerald-50 px-4 py-3 text-sm leading-6 text-emerald-800 outline-none focus:ring-2 focus:ring-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200">{{ notice }}</p>
      <div v-if="error" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-red-50 p-4 text-sm leading-6 text-red-700 dark:bg-red-500/10 dark:text-red-300">
        <p>{{ currentPage ? '操作或读取失败，当前保留上次结果：' : '读取失败：' }}{{ error }}</p><button type="button" :disabled="loading || !!restoring" class="min-h-9 underline disabled:opacity-50" @click="load">重新读取列表</button>
      </div>
      <div v-if="page?.warnings.length" role="status" class="space-y-1 rounded-xl bg-amber-50 px-4 py-3 text-sm leading-6 text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"><p v-for="warning in page.warnings" :key="warning">{{ warning }}</p></div>
      <template v-if="page?.ready || excludedMode">
        <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <dl v-if="!excludedMode && page" class="flex min-w-0 flex-wrap gap-x-5 gap-y-2 text-sm" aria-label="缺集统计">
            <div class="flex items-baseline gap-2"><dt class="text-slate-500 dark:text-slate-400">缺集剧</dt><dd class="font-semibold tabular-nums text-slate-900 dark:text-slate-100">{{ page.summary.series.toLocaleString() }} 部</dd></div>
            <div class="flex items-baseline gap-2"><dt class="text-slate-500 dark:text-slate-400">涉及</dt><dd class="font-semibold tabular-nums text-slate-900 dark:text-slate-100">{{ page.summary.seasons.toLocaleString() }} 季</dd></div>
            <div class="flex items-baseline gap-2"><dt class="text-slate-500 dark:text-slate-400">共缺</dt><dd class="font-semibold tabular-nums text-amber-800 dark:text-amber-300">{{ page.summary.episodes.toLocaleString() }} 集</dd></div>
          </dl>
          <p v-else class="text-sm leading-6 text-slate-600 dark:text-slate-300">当前 Emby 服务的手动排除记录，按具体来源计数。</p>
          <form role="search" class="flex min-w-0 gap-2 lg:w-80" @submit.prevent="filter()">
            <div class="relative min-w-0 flex-1">
              <input v-model="query" type="search" :aria-label="excludedMode ? '搜索已排除条目' : '搜索缺集剧集'" :disabled="!!restoring" maxlength="100" placeholder="剧名或 TMDB ID" class="w-full min-w-0 rounded-lg border border-slate-200 bg-white py-2.5 pl-3 pr-10 text-sm text-slate-900 outline-none focus:border-amber-400 focus:ring-2 focus:ring-amber-400/20 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 [&::-webkit-search-cancel-button]:appearance-none" @search="nativeSearch" />
              <button v-if="query" type="button" aria-label="清除搜索" class="absolute inset-y-0 right-0 flex w-10 items-center justify-center rounded-r-lg text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100" @click="clearSearch"><svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M6 18 18 6"/></svg></button>
            </div>
            <button :disabled="!!restoring" class="shrink-0 rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900">搜索</button>
          </form>
        </div>
        <div v-if="!excludedMode && page" class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs leading-5 text-slate-500 dark:text-slate-400">
          <p>已检查 {{ page.checkedSeries.toLocaleString() }} 部有正式分集的剧集<span v-if="resultQuery">，当前结果：{{ resultQuery }}</span></p>
          <p v-if="page.syncedAt">最近全量同步 {{ time(page.syncedAt) }}</p>
        </div>
      </template>
      <template v-if="excludedMode">
        <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">只影响本地缺集检测，不修改 Emby 和网盘文件。修正 TMDB 编号并更新索引后，新绑定正常检测。<span v-if="resultQuery">当前搜索：{{ resultQuery }}</span></p>
        <p v-if="loading" role="status" class="text-sm text-slate-500 dark:text-slate-400">正在读取已排除条目</p>
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
          <button type="button" class="mt-4 min-h-11 px-3 text-sm text-blue-600 dark:text-blue-400" @click="filter(0, routeQuery(), false)">返回缺集检测</button>
        </section>
      </template>
      <p v-else-if="loading && !page" role="status" class="py-16 text-center text-sm text-slate-500 dark:text-slate-400">正在读取媒体库索引</p>
      <section v-else-if="page && !page.ready" class="rounded-2xl border border-slate-200 bg-white px-5 py-14 text-center dark:border-slate-700 dark:bg-slate-900">
        <h2 class="text-sm font-medium text-slate-900 dark:text-slate-100">{{ page.configured ? '当前媒体库索引尚未建立' : '尚未配置需要同步的媒体库' }}</h2>
        <p class="mx-auto mt-2 max-w-lg text-sm leading-6 text-slate-500 dark:text-slate-400">{{ page.configured ? '请完成当前所选媒体库的全量同步，再查看缺集结果。' : '请先连接 Emby 并选择需要同步的媒体库。' }}</p>
        <RouterLink to="/emby/library" class="mt-5 inline-flex min-h-10 items-center text-sm text-blue-600 dark:text-blue-400">{{ page.configured ? '查看同步状态' : '配置媒体库同步' }}</RouterLink>
      </section>
      <template v-else-if="page?.ready">
        <p v-if="loading" role="status" class="text-xs text-slate-500 dark:text-slate-400">正在刷新检测结果</p>
        <div v-if="page.items.length" class="grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3" :aria-busy="loading" data-missing-grid>
          <EmbyMissingCard v-for="item in page.items" :key="item.key" :item="item" :token="token" @details="selected = item" />
        </div>
        <section v-else class="rounded-2xl border border-slate-200 bg-white px-5 py-14 text-center dark:border-slate-700 dark:bg-slate-900">
          <h2 class="text-sm font-medium text-slate-900 dark:text-slate-100">{{ resultQuery ? '未找到匹配的缺集剧集' : page.checkedSeries ? '当前索引未检测到缺集' : '当前索引没有可检测的正式分集' }}</h2>
          <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ resultQuery ? `没有与“${resultQuery}”匹配的剧名或 TMDB ID。` : page.checkedSeries ? '检测范围为各季第 1 集至已入库最后一集。' : '需有有效季号和集号的已入库分集才能检测。' }}</p>
          <button v-if="resultQuery" type="button" class="mt-4 min-h-10 text-sm text-blue-600 dark:text-blue-400" @click="clearSearch">清除搜索条件</button>
        </section>
      </template>
      <nav v-if="currentPage && (currentPage.offset || currentPage.hasMore)" :aria-label="excludedMode ? '排除记录分页' : '缺集结果分页'" class="flex items-center justify-center gap-5 text-sm">
        <button type="button" :disabled="!currentPage.offset || loading || !!restoring" class="min-h-11 disabled:opacity-30" @click="filter(Math.max(0, currentPage.offset - 24), resultQuery)">上一页</button>
        <span class="text-slate-500 dark:text-slate-400">第 {{ Math.floor(currentPage.offset / 24) + 1 }} 页</span>
        <button type="button" :disabled="!currentPage.hasMore || loading || !!restoring" class="min-h-11 disabled:opacity-30" @click="filter(currentPage.nextOffset, resultQuery)">下一页</button>
      </nav>
    </div>
    <EmbyMissingDetails v-if="selected" :key="selected.key" :item="selected" :token="token" @close="selected = undefined" @removed="removed" />
  </ManagementLayout>
</template>
