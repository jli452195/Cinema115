<script setup lang="ts">
import { catalogAPI } from '../composables/useWebsiteAuth'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import MediaCard from '../components/MediaCard.vue'
import HistoryBackButton from '../components/HistoryBackButton.vue'
import type { PersonItem } from '../types/person'
import type { MediaCardItem } from '../types/media'
import { useEmbyLibrary } from '../composables/useEmbyLibrary'
import { tmdbImageURL } from '../utils/tmdbImage'
const props = defineProps<{ token: string; tmdbId: string }>()
defineEmits<{ logout: [] }>()
interface PersonDetail extends PersonItem { biography: string; birthday: string; deathday: string; placeOfBirth: string; items: MediaCardItem[] }
const person = ref<PersonDetail>()
const loading = ref(true)
const error = ref('')
const failedImage = ref(false)
const expanded = ref(false)
const kind = ref('all')
const limit = ref(30)
const filtered = computed(() => person.value?.items.filter(item => kind.value === 'all' || kind.value === item.kind) ?? [])
const visibleItems = computed(() => filtered.value.slice(0, limit.value))
const matches = useEmbyLibrary(() => props.token, visibleItems)
let controller: AbortController | undefined
const previousTitle = document.title
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current
  person.value = undefined; loading.value = true; error.value = ''; failedImage.value = false; expanded.value = false; kind.value = 'all'; limit.value = 30
  document.title = '演员 · Cinema115'
  try {
    if (!/^[1-9]\d*$/.test(props.tmdbId) || !Number.isSafeInteger(Number(props.tmdbId))) throw new Error('演员 ID 无效')
    const response = await fetch(catalogAPI(`tmdb/person/${props.tmdbId}`), { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([current.signal, AbortSignal.timeout(25000)]) })
    const data = await response.json().catch(() => null)
    if (!response.ok || !Array.isArray(data?.items)) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : response.status === 404 ? '未找到该演员' : data?.error || '演员信息加载失败')
    if (current.signal.aborted) return
    person.value = data; document.title = `${data.name} · Cinema115`
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '演员信息加载失败' }
  finally { if (controller === current) loading.value = false }
}
watch(kind, () => { limit.value = 30 })
watch(() => [props.tmdbId, props.token], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); document.title = previousTitle })
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <main class="mx-auto w-full max-w-[1440px] min-w-0 flex-1 px-4 py-7 sm:px-8 lg:px-10">
      <HistoryBackButton fallback="/search?scope=person" class="mb-8 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500" />
      <div v-if="loading" role="status" class="rounded-xl bg-slate-200 dark:bg-slate-700 p-16 text-center text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 motion-safe:animate-pulse">正在加载演员信息…</div>
      <div v-else-if="error" role="alert" class="rounded-xl border border-red-100 bg-white dark:bg-slate-900 p-8 text-center"><p class="text-sm text-red-600">{{ error }}</p><button class="mt-4 rounded-lg bg-slate-900 px-5 py-2 text-sm text-white" @click="load">重新加载</button></div>
      <template v-else-if="person">
        <section class="grid items-start gap-8 sm:grid-cols-[200px_minmax(0,1fr)]">
          <div class="mx-auto aspect-[2/3] w-44 overflow-hidden rounded-2xl bg-slate-200 dark:bg-slate-700 sm:w-full"><img v-if="person.profileUrl && !failedImage" :src="tmdbImageURL(person.profileUrl)" :alt="person.name" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImage = true"/><div v-else class="flex h-full items-center justify-center text-sm text-slate-400 dark:text-slate-500">暂无照片</div></div>
          <div class="min-w-0"><span class="text-xs font-medium text-amber-600">演员</span><h1 class="mt-3 break-words text-3xl font-bold text-slate-900 dark:text-slate-100">{{ person.name }}</h1><div class="mt-4 flex flex-wrap gap-x-5 gap-y-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><span v-if="person.birthday">出生：{{ person.birthday }}</span><span v-if="person.deathday">逝世：{{ person.deathday }}</span><span v-if="person.placeOfBirth">{{ person.placeOfBirth }}</span></div><p class="mt-6 whitespace-pre-line text-sm leading-7 text-slate-600 dark:text-slate-400 dark:text-slate-500" :class="{ 'line-clamp-6': !expanded }">{{ person.biography || 'TMDB 暂未提供中文简介。' }}</p><button v-if="person.biography.length > 200" class="mt-3 text-xs text-amber-600" :aria-expanded="expanded" @click="expanded = !expanded">{{ expanded ? '收起简介' : '展开简介' }}</button></div>
        </section>
        <section class="mt-10" aria-label="参演作品"><div class="mb-6 flex flex-wrap items-center justify-between gap-4"><h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">参演作品 <span class="ml-2 text-sm font-normal text-slate-400 dark:text-slate-500">{{ filtered.length }}</span></h2><div class="flex gap-1" role="group" aria-label="作品类型"><button v-for="tab in [{ id: 'all', label: '全部' }, { id: 'movie', label: '电影' }, { id: 'tv', label: '剧集' }]" :key="tab.id" :aria-pressed="kind === tab.id" class="rounded-lg px-4 py-2 text-sm" :class="kind === tab.id ? 'bg-amber-50 text-amber-700' : 'text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800'" @click="kind = tab.id">{{ tab.label }}</button></div></div>
          <p v-if="!filtered.length" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">暂无此类参演作品。</p>
          <div v-else class="grid grid-cols-2 gap-x-5 gap-y-7 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6"><MediaCard v-for="item in visibleItems" :key="`${item.kind}:${item.id}`" :item="item" :library="matches[`${item.kind}:${item.id}`]" /></div>
          <div v-if="limit < filtered.length" class="mt-8 text-center"><button class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-5 py-2 text-sm" @click="limit += 30">加载更多（剩余 {{ filtered.length - limit }} 部）</button></div>
        </section>
        <footer class="mt-10 border-t border-slate-200 dark:border-slate-700 pt-5 text-xs leading-6 text-slate-400 dark:text-slate-500">演员资料与参演作品来自 <a :href="`https://www.themoviedb.org/person/${person.id}?language=zh-CN`" target="_blank" rel="noopener noreferrer" class="text-slate-500 dark:text-slate-400 dark:text-slate-500">TMDB</a>，作品按发行日期从新到旧排列。<p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p></footer>
      </template>
    </main>
  </div>
</template>
