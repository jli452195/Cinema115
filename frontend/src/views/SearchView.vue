<script setup lang="ts">
import { catalogAPI } from '../composables/usePortalAuth'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import HistoryBackButton from '../components/HistoryBackButton.vue'
import NavigationIcon from '../components/NavigationIcon.vue'
import MediaCard from '../components/MediaCard.vue'
import PersonCard from '../components/PersonCard.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import type { PersonItem } from '../types/person'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import type { MediaCardItem } from '../types/media'
import { rememberedDashboardHome } from '../utils/dashboardPreferences'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const router = useRouter()
const compactViewport = window.matchMedia('(max-width: 639px)')
const mobile = ref(compactViewport.matches)
const searchInput = ref<HTMLInputElement>()
const scrolled = ref(false)
const scopes = [{ value: 'multi', label: '聚合' }, { value: 'movie', label: '电影' }, { value: 'tv', label: '剧集' }, { value: 'person', label: '演员' }] as const
const mobileScopes = [{ value: 'multi', label: '影视' }, ...scopes.slice(1)] as const
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
function navigate(nextScope = scope.value, nextPage = 1, q = query.value) {
  const target = location(nextScope, nextPage, q)
  // 手机端的关键词、范围和分页共用一条历史记录，返回直接回到搜索入口。
  return mobile.value ? router.replace(target) : router.push(target)
}
function submit() {
  const q = input.value.trim()
  if (mobile.value) searchInput.value?.blur()
  if (q === query.value && page.value === 1) { void load(); return }
  void navigate(scope.value, 1, q)
}
function clear() {
  input.value = ''
  if (mobile.value) searchInput.value?.focus({ preventScroll: true })
  void navigate(scope.value, 1, '')
}
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
function updateScrolled() { scrolled.value = window.scrollY > 5 }
function updateViewport() { mobile.value = compactViewport.matches }
onMounted(() => {
  compactViewport.addEventListener('change', updateViewport)
  window.addEventListener('scroll', updateScrolled, { passive: true })
  updateScrolled()
  if (mobile.value && !query.value) searchInput.value?.focus({ preventScroll: true })
})
onBeforeUnmount(() => {
  compactViewport.removeEventListener('change', updateViewport)
  window.removeEventListener('scroll', updateScrolled)
  controller?.abort()
})
</script>

<template>
  <div class="search-page flex min-h-screen flex-col">
    <WorkbenchHeader v-if="!mobile" @logout="$emit('logout')" />
    <main class="search-content workbench-content mx-auto w-full max-w-[1600px] min-w-0 flex-1 px-4 pb-7 sm:px-8 lg:px-10 lg:pb-10">
      <header class="search-header mx-auto max-w-2xl text-center" :class="{ 'is-compact': query, 'is-scrolled': mobile && scrolled }">
        <h1 :class="mobile ? 'sr-only' : 'text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100'">搜索影视</h1>
        <p v-if="!mobile && !query" class="mt-2 text-sm text-slate-500 dark:text-slate-400">发现电影、剧集与喜爱的演员</p>
        <div class="search-fields">
          <HistoryBackButton v-if="mobile" :fallback="rememberedDashboardHome" icon-only class="mobile-search-back" />
          <form role="search" class="search-form flex gap-2" @submit.prevent="submit">
            <NavigationIcon v-if="mobile" name="search" :size="18" class="mobile-search-icon" />
            <input ref="searchInput" v-model="input" type="search" :autocapitalize="mobile ? 'off' : undefined" :aria-label="scope === 'person' ? '搜索演员姓名' : '搜索片名'" maxlength="100" :enterkeyhint="mobile ? 'search' : undefined" :placeholder="scope === 'person' ? '输入演员姓名…' : mobile ? '搜索电影、剧集' : '输入电影或剧集名称…'" class="search-input min-w-0 flex-1 bg-transparent py-2 text-sm text-slate-900 outline-none placeholder:text-slate-400 dark:text-slate-100" />
            <button v-if="mobile && (input || query)" type="button" class="mobile-search-clear" aria-label="清空搜索" @click="clear"><NavigationIcon name="close" :size="17" /></button>
            <button v-if="!mobile || input.trim()" type="submit" class="search-submit shrink-0 px-5 py-2 text-sm font-medium">搜索</button>
          </form>
        </div>
        <div class="search-scopes"><SegmentedControl :model-value="scope" :options="mobile ? mobileScopes : scopes" label="搜索范围" density="compact" :appearance="mobile ? 'pwa-pill' : 'neutral'" @change="navigate($event)" /></div>
      </header>
      <div v-if="query" class="search-summary search-secondary mb-6 flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 dark:border-slate-800 pb-4 text-xs" role="status" aria-live="polite">
        <span class="min-w-0 break-words">{{ loading ? '正在搜索…' : error ? '暂时无法搜索' : scope === 'person' ? `「${query}」· 本页 ${people.length} 位演员` : scope === 'multi' ? `「${query}」· 本页 ${items.length} 部影视` : `「${query}」· 找到 ${total.toLocaleString()} 部${scope === 'movie' ? '电影' : '剧集'}` }}</span>
        <button v-if="!mobile" type="button" class="search-clear shrink-0 text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200" @click="clear">清空搜索</button>
      </div>
      <p v-if="!query" class="search-empty search-secondary py-24 text-center text-sm">
        {{ scope === 'person' ? '输入演员姓名，查看代表作和参演影视。' : mobile ? '输入电影或剧集名称开始搜索。' : '输入片名开始搜索，聚合范围同时展示电影与剧集。' }}
      </p>
      <div v-else-if="error" role="alert" class="rounded-xl border border-red-100 bg-red-50 p-6 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300"><p>{{ error }}</p><button type="button" class="mt-4 rounded-lg border border-red-200 px-4 py-2 dark:border-red-800" @click="load">重新搜索</button></div>
      <div v-else-if="loading" aria-label="正在加载搜索结果" class="search-grid grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><div v-for="i in 10" :key="i" class="animate-pulse motion-reduce:animate-none"><div class="aspect-[2/3] rounded-xl bg-slate-200 dark:bg-slate-700"></div><div class="mt-3 h-4 w-3/4 rounded bg-slate-200 dark:bg-slate-700"></div></div></div>
      <div v-else-if="scope === 'person' && !people.length" class="search-secondary py-20 text-center text-sm">{{ totalPages > page ? '本页没有演员结果，可尝试下一页。' : '没有找到匹配的演员，试试其他姓名。' }}</div>
      <div v-else-if="scope === 'person'" class="search-grid grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><PersonCard v-for="person in people" :key="person.id" :person="person" /></div>
      <div v-else-if="!items.length" class="search-secondary py-20 text-center text-sm">{{ totalPages > page ? '本页没有电影或剧集，可尝试下一页或切换搜索范围。' : '没有找到匹配的影视，试试其他片名或搜索范围。' }}</div>
      <div v-else class="search-grid grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6">
        <MediaCard v-for="item in items" :key="`${item.kind}:${item.id}`" :item="item" :library="matches[`${item.kind}:${item.id}`]" />
      </div>
      <nav v-if="!loading && !error && query && (totalPages > 1 || page > 1)" aria-label="搜索分页" class="search-pagination mt-9 flex items-center justify-center gap-4 text-sm">
        <button type="button" :disabled="page <= 1" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 disabled:opacity-40" @click="navigate(scope, page - 1)">上一页</button>
        <span class="search-secondary text-xs">{{ page }} / {{ totalPages }}</span>
        <button type="button" :disabled="page >= totalPages" class="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-2 disabled:opacity-40" @click="navigate(scope, page + 1)">下一页</button>
      </nav>
      <footer class="search-attribution search-secondary mt-10 border-t border-slate-200 pt-5 text-xs leading-6 dark:border-slate-800" :class="{ 'is-empty': !query }">
        数据与相关度排序来自 <a href="https://www.themoviedb.org/" target="_blank" rel="noopener noreferrer">TMDB</a>。<template v-if="!mobile">聚合展示电影与剧集；演员范围展示表演部门人物（含配音），过滤其他工作人员，部分页面结果可能较少。</template>
        <p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
      </footer>
    </main>
  </div>
</template>

<style scoped>
.search-header { padding-top: .25rem; margin-bottom: 1.5rem; }
.search-header.is-compact h1 { font-size: 1.25rem; opacity: .9; }
.search-form { margin-top: 1.5rem; padding: 5px 7px 5px 14px; border: 1px solid var(--glass-edge); border-radius: 14px; background: var(--glass-panel-background); box-shadow: var(--glass-card-shadow); -webkit-backdrop-filter: var(--glass-panel-filter); backdrop-filter: var(--glass-panel-filter); transition: border-color .2s ease, box-shadow .2s ease, background-color .2s ease; }
.is-compact .search-form { margin-top: .85rem; }
.search-form:focus-within { border-color: #64748b; box-shadow: 0 0 0 3px rgb(148 163 184 / .22), var(--glass-card-shadow); }
.dark .search-form:focus-within { border-color: #94a3b8; box-shadow: 0 0 0 3px rgb(148 163 184 / .16), var(--glass-card-shadow); }
.search-submit { min-height: 40px; border-radius: 10px; color: #fff; background: #0f172a; transition: background-color .2s ease; }
.dark .search-submit { color: #0f172a; background: #f1f5f9; }
.search-submit:focus-visible, .search-clear:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
.search-scopes { margin-top: .9rem; }
.search-clear { min-height: 36px; }
@media (hover: hover) { .search-submit:hover { background: #334155; }.dark .search-submit:hover { background: #cbd5e1; } }
.search-secondary { color: rgb(var(--content-secondary)); }
.search-attribution a { text-decoration: underline; text-underline-offset: 2px; }
@media (max-width: 639px) {
  .search-page { min-height: 100dvh; }
  .search-content {
    --search-start: max(16px, var(--app-safe-left));
    --search-end: max(16px, var(--app-safe-right));
    display: flex;
    flex-direction: column;
    padding: 0 var(--search-end) calc(20px + var(--app-safe-bottom)) var(--search-start);
  }
  .search-header {
    position: sticky;
    top: 0;
    z-index: 20;
    max-width: none;
    margin: 0 calc(-1 * var(--search-end)) 16px calc(-1 * var(--search-start));
    padding: calc(var(--app-safe-top) + 8px) var(--search-end) 12px var(--search-start);
    background: rgb(var(--surface-page));
  }
  .search-header.is-scrolled { box-shadow: 0 8px 18px -14px rgb(15 23 42 / .38); }
  .dark .search-header.is-scrolled { box-shadow: 0 8px 18px -12px rgb(0 0 0 / .75); }
  .search-fields { display: flex; align-items: center; gap: 4px; }
  .mobile-search-back { width: 44px; height: 44px; margin-inline-start: -8px; border-radius: 50%; color: rgb(var(--content-primary)); }
  .search-form, .is-compact .search-form {
    flex: 1;
    min-width: 0;
    align-items: center;
    gap: 4px;
    margin-top: 0;
    padding: 0 4px 0 12px;
    border: 0;
    border-radius: 999px;
    background: rgb(var(--surface-elevated));
    box-shadow: 0 1px 3px rgb(0 0 0 / .12);
    -webkit-backdrop-filter: none;
    backdrop-filter: none;
  }
  .search-form:focus-within, .dark .search-form:focus-within { box-shadow: inset 0 0 0 1px rgb(var(--line-control) / .55), 0 1px 3px rgb(0 0 0 / .12); }
  .search-input { height: 44px; font-size: 16px; color: rgb(var(--content-primary)); }
  .search-input::placeholder { color: rgb(var(--content-secondary)); opacity: 1; }
  .search-input::-webkit-search-cancel-button { -webkit-appearance: none; }
  .mobile-search-icon { flex: none; color: rgb(var(--content-secondary)); }
  .mobile-search-clear { display: grid; flex: none; width: 44px; height: 44px; place-items: center; border-radius: 50%; color: rgb(var(--content-secondary)); }
  .search-submit, .dark .search-submit { min-width: 48px; min-height: 44px; padding: 0; border-radius: 999px; background: transparent; color: rgb(var(--action-link)); }
  .search-scopes { margin-top: 10px; }
  .search-scopes :deep(.segmented-control) { display: flex; width: 100%; }
  .search-scopes :deep(.segmented-control-option) { flex: 1 1 0; }
  .search-summary { margin-bottom: 12px; padding-bottom: 0; border: 0; overflow-wrap: anywhere; }
  .search-grid { column-gap: 14px; row-gap: 24px; }
  .search-empty { padding: clamp(32px, 8dvh, 72px) 0 24px; }
  .search-pagination button { min-height: 44px; border: 0; border-radius: 999px; background: rgb(var(--surface-elevated)); color: rgb(var(--content-primary)); }
  .search-attribution { border: 0; }
  .search-attribution.is-empty { margin-top: auto; }
  .mobile-search-back:focus-visible, .search-submit:focus-visible, .mobile-search-clear:focus-visible,
  .search-pagination button:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: -2px; }
  .mobile-search-back:active, .mobile-search-clear:active, .search-pagination button:not(:disabled):active { background: rgb(var(--surface-hover)); }
  .search-submit:active, .dark .search-submit:active { background: rgb(var(--selection-surface)); }
}
@media (hover: hover) and (max-width: 639px) {
  .mobile-search-back:hover, .mobile-search-clear:hover, .search-pagination button:not(:disabled):hover { background: rgb(var(--surface-hover)); }
  .search-submit:hover, .dark .search-submit:hover { background: rgb(var(--selection-surface)); }
}
@media (prefers-reduced-motion: no-preference) and (max-width: 639px) {
  .search-fields { animation: search-toolbar-in 180ms cubic-bezier(.2, 0, 0, 1); }
}
@keyframes search-toolbar-in { from { opacity: 0; transform: translateY(-4px) scale(.98); } }
@media (prefers-reduced-motion: reduce) { .search-form, .search-submit, .mobile-search-back { transition: none; } }
</style>
