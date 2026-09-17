<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { gyingRequest, type GyingConfigView, type GyingResourceLink, type GyingSearchItem, type GyingSearchResponse } from '../types/gying'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; originalTitle: string; imdbId: string }>()
const emit = defineEmits<{ close: [] }>()
type Tab = 'all' | 'movie' | 'tv' | 'anime' | 'torrent' | 'pan'
const dialog = ref<HTMLDialogElement>()
const config = ref<GyingConfigView>()
const result = ref<GyingSearchResponse>()
const activeTab = ref<Tab>('all')
const busy = ref(true)
const error = ref('')
const copied = ref('')
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const labels: Record<Tab, string> = { all: '全部', movie: '电影', tv: '剧集', anime: '动漫', torrent: '种子', pan: '网盘' }

const counts = computed<Record<Tab, number>>(() => {
  const items = result.value?.items || []
  return {
    all: items.length,
    movie: items.filter(item => item.mediaType === 'movie').length,
    tv: items.filter(item => item.mediaType === 'tv').length,
    anime: items.filter(item => item.mediaType === 'anime').length,
    torrent: items.reduce((sum, item) => sum + item.links.filter(link => link.category === 'torrent').length, 0),
    pan: items.reduce((sum, item) => sum + item.links.filter(link => link.category === 'pan').length, 0),
  }
})
const tabs = computed(() => (Object.keys(labels) as Tab[]).map(id => ({ id, label: labels[id], count: counts.value[id] })))
const visibleItems = computed(() => {
  const items = result.value?.items || []
  if (['movie', 'tv', 'anime'].includes(activeTab.value)) return items.filter(item => item.mediaType === activeTab.value)
  if (activeTab.value === 'torrent' || activeTab.value === 'pan') return items.flatMap(item => {
    const links = item.links.filter(link => link.category === activeTab.value)
    return links.length ? [{ ...item, links }] : []
  })
  return items
})
const mediaLabels: Record<string, string> = { movie: '电影', tv: '剧集', anime: '动漫', other: '其他' }

async function search() {
  if (busy.value && result.value) return
  busy.value = true; error.value = ''; copied.value = ''
  try {
    result.value = await gyingRequest<GyingSearchResponse>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: props.tmdbId })
    activeTab.value = 'all'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '观影搜索失败' }
  finally { busy.value = false }
}

async function copyLink(link: GyingResourceLink) {
  const text = `${link.url}${link.password ? `  提取码：${link.password}` : ''}`
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(text)
    else {
      const input = document.createElement('textarea')
      input.value = text; input.style.position = 'fixed'; input.style.opacity = '0'
      document.body.appendChild(input); input.select()
      const ok = document.execCommand('copy'); input.remove()
      if (!ok) throw new Error('copy denied')
    }
    copied.value = link.url; error.value = ''
  }
  catch { error.value = '浏览器未允许复制，请手动选择链接。' }
}

function linkCount(item: GyingSearchItem, category: 'pan' | 'torrent') { return item.links.filter(link => link.category === category).length }

onMounted(async () => {
  document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  try {
    config.value = await gyingRequest<GyingConfigView>(props.token, 'config', lifecycle.signal)
    if (config.value.configured) await search()
    else busy.value = false
  } catch (e) { if (!lifecycle.signal.aborted) { error.value = e instanceof Error ? e.message : '读取观影配置失败'; busy.value = false } }
})
onBeforeUnmount(() => { lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="gying-search-title" class="m-auto max-h-[90dvh] w-[calc(100%-2rem)] max-w-5xl overflow-hidden rounded-2xl border border-slate-200 bg-white p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="emit('close')">
      <div class="flex max-h-[90dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 p-5"><div class="min-w-0"><h2 id="gying-search-title" class="text-lg font-semibold text-slate-900">观影搜索</h2><p class="mt-1 break-words text-sm text-slate-500">{{ title }}<span v-if="originalTitle && originalTitle !== title"> · {{ originalTitle }}</span><span v-if="imdbId"> · {{ imdbId }}</span></p></div><button autofocus type="button" aria-label="关闭观影搜索" class="rounded-lg px-2 py-1 text-xl text-slate-400 hover:bg-slate-100" @click="emit('close')">×</button></header>
        <div class="min-h-0 overflow-y-auto p-5">
          <div v-if="config && !config.configured" class="rounded-xl bg-slate-50 p-6 text-sm leading-7 text-slate-600"><p>请先配置并登录观影站账号。</p><RouterLink to="/gying" class="mt-3 inline-block font-medium text-blue-600" @click="emit('close')">前往观影搜索配置 →</RouterLink></div>
          <p v-if="busy" role="status" class="rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">正在使用影视原名搜索并读取全部资源详情…<span class="block text-xs">如果原名没有有效资源，将自动使用 IMDb 编号重试。</span></p>
          <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <template v-if="result">
            <p class="mb-4 rounded-xl bg-slate-50 p-4 text-sm text-slate-600"><template v-if="result.matchedBy === 'imdb'">原名没有找到有效资源，已使用 IMDb <strong>{{ result.query }}</strong> 搜索。</template><template v-else>使用原名 <strong>{{ result.query }}</strong> 搜索。</template></p>
            <p v-if="result.truncated" role="status" class="mb-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-700">站点返回 {{ result.total }} 个条目，本次为保护服务稳定性仅读取前 100 个。</p>
            <nav aria-label="资源分类" class="mb-5 flex gap-2 overflow-x-auto rounded-xl border border-slate-200 bg-slate-50 p-2"><button v-for="tab in tabs" :key="tab.id" type="button" class="shrink-0 rounded-lg px-3 py-2 text-sm" :class="activeTab === tab.id ? 'bg-blue-600 text-white' : 'text-slate-600 hover:bg-white'" @click="activeTab = tab.id">{{ tab.label }} <span class="opacity-70">{{ tab.count }}</span></button></nav>
            <p v-if="!result.items.length" class="py-12 text-center text-sm text-slate-500">原名和 IMDb 均没有找到有效资源。</p>
            <p v-else-if="!visibleItems.length" class="py-12 text-center text-sm text-slate-500">当前分类没有资源。</p>
            <div v-else class="space-y-4">
              <article v-for="item in visibleItems" :key="item.id" class="rounded-xl border border-slate-200 p-4 sm:p-5">
                <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><h3 class="break-words font-semibold leading-6 text-slate-900">{{ item.title }} <span v-if="item.year" class="font-normal text-slate-400">({{ item.year }})</span></h3><div class="mt-2 flex flex-wrap gap-2 text-[11px]"><span class="rounded bg-amber-50 px-2 py-1 text-amber-700">{{ mediaLabels[item.mediaType] }}</span><span v-if="linkCount(item, 'pan')" class="rounded bg-blue-50 px-2 py-1 text-blue-700">网盘 {{ linkCount(item, 'pan') }}</span><span v-if="linkCount(item, 'torrent')" class="rounded bg-violet-50 px-2 py-1 text-violet-700">种子 {{ linkCount(item, 'torrent') }}</span></div></div></div>
                <p v-if="item.description" class="mt-3 break-words text-xs leading-6 text-slate-500">{{ item.description }}</p>
                <ul class="mt-4 space-y-2"><li v-for="(link, index) in item.links" :key="`${link.type}:${link.url}:${index}`" class="rounded-lg bg-slate-50 p-3 text-xs"><div class="flex flex-wrap items-center gap-2"><span class="rounded bg-white px-2 py-1 font-medium text-slate-700 ring-1 ring-slate-200">{{ link.typeLabel }}</span><span v-if="link.size" class="text-slate-400">{{ link.size }}</span><span v-if="link.updatedAt" class="text-slate-400">{{ link.updatedAt }}</span><span v-if="link.password" class="text-amber-700">提取码 {{ link.password }}</span><span class="ml-auto flex gap-3"><button type="button" class="text-blue-600" @click="copyLink(link)">{{ copied === link.url ? '已复制' : '复制' }}</button><a :href="link.url" target="_blank" rel="noopener noreferrer" class="text-blue-600">打开 ↗</a></span></div><p v-if="link.workTitle" class="mt-2 break-words text-slate-600">{{ link.workTitle }}</p><p class="mt-2 break-all text-slate-400">{{ link.url }}</p></li></ul>
              </article>
            </div>
          </template>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-slate-100 p-5"><p class="text-xs text-slate-500">只展示和打开资源，本阶段不接入 115、转存或 STRM。</p><button v-if="config?.configured" :disabled="busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="search">重新搜索</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>
