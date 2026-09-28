<script setup lang="ts">
import { catalogAPI } from '../composables/useWebsiteAuth'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import MediaCard from '../components/MediaCard.vue'
import PersonCard from '../components/PersonCard.vue'
import type { PersonItem } from '../types/person'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import type { MediaCardItem } from '../types/media'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const router = useRouter()
const scopes = [{ id: 'multi', label: '聚合' }, { id: 'movie', label: '电影' }, { id: 'tv', label: '剧集' }, { id: 'person', label: '演员' }] as const
const scope = computed(() => route.query.scope === 'movie' || route.query.scope === 'tv' || route.query.scope === 'person' ? route.query.scope : 'multi')
const query = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const page = computed(() => {
  const value = typeof route.query.page === 'string' ? Number(route.query.page) : 1
  return Number.isInteger(value) && value >= 1 && value <= 500 ? value : 1
})
const input = ref(query.value)
const items = ref<MediaCardItem[]>([])
const people = ref<PersonItem[]>([])
const matches = useEmbyLibrary(() => props.token, items)
const loading = ref(false)
const error = ref('')
const totalPages = ref(0)
const total = ref(0)
let controller: AbortController | undefined

function location(nextScope = scope.value, nextPage = 1, q = query.value) {
  return { path: '/search', query: { ...(q ? { q } : {}), scope: nextScope, ...(nextPage > 1 ? { page: String(nextPage) } : {}) } }
}
function submit() {
  const q = input.value.trim()
  if (q === query.value && page.value === 1) { void load(); return }
  void router.push(location(scope.value, 1, q))
}
function clear() { input.value = ''; void router.push(location(scope.value, 1, '')) }
async function load() {
  controller?.abort()
  const current = new AbortController(); controller = current
  items.value = []; people.value = []; error.value = ''; totalPages.value = 0; total.value = 0; loading.value = false
  if (!query.value) return
  if ([...query.value].length > 100) { error.value = '搜索词最多 100 个字符，请缩短后重试。'; return }
  loading.value = true
  try {
    const params = new URLSearchParams({ kind: scope.value, query: query.value, page: String(page.value) })
    const response = await fetch(catalogAPI(`tmdb/${scope.value === 'person' ? 'people' : 'browse'}?${params}`), {
      headers: { Authorization: `Bearer ${props.token}` },
      signal: AbortSignal.any([current.signal, AbortSignal.timeout(25000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !Array.isArray(data?.items)) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || '搜索失败，请稍后重试。')
    if (current.signal.aborted) return
    if (scope.value === 'person') people.value = data.items
    else items.value = data.items
    totalPages.value = data.totalPages; total.value = data.total
  } catch (e) {
    if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '搜索失败，请稍后重试。'
  } finally { if (controller === current) loading.value = false }
}
watch(query, value => { input.value = value })
// 新搜索、范围切换和前进后退均由路由驱动，旧响应不能覆盖当前结果。
watch([query, scope, page, () => props.token], () => { void load() }, { immediate: true })
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <main class="workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-7 sm:px-8 lg:px-10 lg:pb-10">
      <header class="mx-auto max-w-2xl text-center">
        <h1 class="text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">搜索影视</h1>
        <p class="mt-2 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">发现电影、剧集与喜爱的演员</p>
        <form role="search" class="mt-6 flex gap-2 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-2 shadow-sm focus-within:border-amber-400" @submit.prevent="submit">
          <input v-model="input" type="search" :aria-label="scope === 'person' ? '搜索演员姓名' : '搜索片名'" maxlength="100" :placeholder="scope === 'person' ? '输入演员姓名…' : '输入电影或剧集名称…'" class="min-w-0 flex-1 bg-transparent px-2 py-2 text-sm outline-none sm:px-3" />
          <button type="submit" class="shrink-0 rounded-lg bg-slate-900 px-5 py-2 text-sm font-medium text-white hover:bg-slate-700">搜索</button>
        </form>
        <nav aria-label="搜索范围" class="mt-5 flex flex-wrap justify-center gap-1 sm:gap-2">
          <RouterLink v-for="item in scopes" :key="item.id" :to="location(item.id)" :aria-current="scope === item.id ? 'page' : undefined" class="rounded-lg border px-4 py-2 sm:px-6 text-sm font-medium transition-colors" :class="scope === item.id ? 'border-amber-300 bg-amber-50 text-amber-700' : 'border-transparent text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800'">{{ item.label }}</RouterLink>
        </nav>
      </header>
      <div v-if="query" class="mb-6 mt-8 flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 dark:border-slate-800 pb-4 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500" role="status" aria-live="polite">
        <span class="min-w-0 break-words">{{ loading ? '正在搜索…' : error ? '暂时无法搜索' : scope === 'person' ? `「${query}」· 本页 ${people.length} 位演员` : scope === 'multi' ? `「${query}」· 本页 ${items.length} 部影视` : `「${query}」· 找到 ${total.toLocaleString()} 部${scope === 'movie' ? '电影' : '剧集'}` }}</span>
        <button type="button" class="shrink-0 text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-300 dark:text-slate-300" @click="clear">清空搜索</button>
      </div>
      <div v-if="!query" class="py-24 text-center text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ scope === 'person' ? '输入演员姓名，查看照片、代表作和参演影视。' : '输入片名开始搜索，聚合范围同时展示电影与剧集。' }}</div>
      <div v-else-if="error" role="alert" class="rounded-xl border border-red-100 bg-red-50 p-6 text-sm text-red-700"><p>{{ error }}</p><button type="button" class="mt-4 rounded-lg border border-red-200 px-4 py-2" @click="load">重新搜索</button></div>
      <div v-else-if="loading" aria-label="正在加载搜索结果" class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><div v-for="i in 10" :key="i" class="animate-pulse motion-reduce:animate-none"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-3 h-4 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div></div></div>
      <div v-else-if="scope === 'person' && !people.length" class="py-20 text-center text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ totalPages > page ? '本页没有演员结果，可尝试下一页。' : '没有找到匹配的演员，试试其他姓名。' }}</div>
      <div v-else-if="scope === 'person'" class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><PersonCard v-for="person in people" :key="person.id" :person="person" /></div>
      <div v-else-if="!items.length" class="py-20 text-center text-sm text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ totalPages > page ? '本页没有电影或剧集，可尝试下一页或切换搜索范围。' : '没有找到匹配的影视，试试其他片名或搜索范围。' }}</div>
      <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <MediaCard v-for="item in items" :key="`${item.kind}:${item.id}`" :item="item" :library="matches[`${item.kind}:${item.id}`]" />
      </div>
      <nav v-if="!loading && !error && query && (totalPages > 1 || page > 1)" aria-label="搜索分页" class="mt-9 flex items-center justify-center gap-4 text-sm">
        <button type="button" :disabled="page <= 1" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 disabled:opacity-40" @click="router.push(location(scope, page - 1))">上一页</button>
        <span class="text-xs text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ page }} / {{ totalPages }}</span>
        <button type="button" :disabled="page >= totalPages" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 disabled:opacity-40" @click="router.push(location(scope, page + 1))">下一页</button>
      </nav>
      <footer class="mt-10 border-t border-slate-200 dark:border-slate-800 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">数据与相关度排序来自 <a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer" class="text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">TMDB</a>。聚合展示电影与剧集；演员范围展示表演部门人物（含配音），过滤其他工作人员，部分页面结果可能较少。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
    </main>
  </div>
</template>
