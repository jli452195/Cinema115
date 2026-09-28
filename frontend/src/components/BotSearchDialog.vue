<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { is115Share, normalize115Share } from '../utils/shareLink'
import { telegramUserRequest, telegramUserSearchStream, type TelegramUserStatus, type BotSearchResult } from '../types/telegramUser'
const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const state = ref<TelegramUserStatus>()
const result = ref<BotSearchResult>()
const searchBusy = ref(false)
const loadingMore = ref(false)
const checking = ref(true)
const error = ref('')
const searchStage = ref<'waiting' | 'collecting'>('waiting')
const lifecycle = new AbortController()
let searchFinished: Promise<void> = Promise.resolve()
const shareRuns = useShareSTRMRun(props.token, lifecycle.signal, { scope: `bot/${props.kind}/${props.tmdbId}`, beforeLoad: () => searchFinished })
const generating = shareRuns.busy
const generationStatus = shareRuns.statuses
const activeRunID = shareRuns.activeRunID
const stopping = shareRuns.stopping
const linkPending = ref<Record<string, boolean>>({})
const resolvedLinks = ref<Record<string, string[]>>({})
const linkErrors = ref<Record<string, string>>({})
const linkRequests = new Map<string, Promise<string[]>>()
let linkQueue: Promise<unknown> = Promise.resolve()
const busy = computed(() => searchBusy.value || shareRuns.preparing.value > 0 || Object.values(linkPending.value).some(Boolean))
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const resources = computed(() => {
  const items = result.value?.items.filter(item => item.kind === 'resource') || []
  if (items.length) return items.map(item => ({ ...item, direct: '' }))
  return (result.value?.links.filter(is115Share) || []).map((link, index) => ({ id: `link:${index}:${link}`, title: `${props.title} · 资源 ${index + 1}`, kind: 'resource' as const, tmdbId: '', size: '', direct: link }))
})
// 标题打开与 STRM 生成共用取链队列，避免同时点击导致 Bot 回复错配。
function loadResourceLinks(item: (typeof resources.value)[number]): Promise<string[]> {
  const cached = resolvedLinks.value[item.id]
  if (cached) return Promise.resolve(cached)
  const pending = linkRequests.get(item.id)
  if (pending) return pending
  linkPending.value[item.id] = true
  const task = linkQueue.then(async () => {
    await searchFinished
    lifecycle.signal.throwIfAborted()
    const selection = item.direct ? { links: [item.direct] } : await telegramUserRequest<BotSearchResult>(props.token, 'select', lifecycle.signal, { id: item.id })
    lifecycle.signal.throwIfAborted()
    const links = [...new Set(selection.links.map(normalize115Share).filter(Boolean))]
    if (!links.length) throw new Error('该资源没有返回可打开的 115 分享链接')
    resolvedLinks.value[item.id] = links
    return links
  }).finally(() => { delete linkPending.value[item.id]; linkRequests.delete(item.id) })
  linkRequests.set(item.id, task)
  linkQueue = task.catch(() => {})
  return task
}
async function openResource(item: (typeof resources.value)[number]) {
  if (linkPending.value[item.id]) return
  linkErrors.value[item.id] = ''
  // 在点击事件中预开窗口，避免异步取链完成后被浏览器拦截。
  const tab = window.open('about:blank', '_blank')
  if (tab) { tab.opener = null; tab.document.title = '正在获取 115 分享链接…' }
  try {
    const links = await loadResourceLinks(item)
    if (links.length === 1 && tab && !tab.closed) tab.location.replace(links[0]!)
    else {
      tab?.close()
      linkErrors.value[item.id] = links.length > 1 ? '该资源包含多个分享，请选择下方链接。' : '浏览器未打开新窗口，请再次点击标题打开。'
    }
  } catch (e) {
    tab?.close()
    if (!lifecycle.signal.aborted) linkErrors.value[item.id] = e instanceof Error ? e.message : '获取分享链接失败，请重试'
  }
}
function shareResource(item: (typeof resources.value)[number]): ShareSTRMResource {
  const identity = item.direct ? `bot/${props.kind}/${props.tmdbId}/direct/${normalize115Share(item.direct)}` : `bot/${props.kind}/${props.tmdbId}/item/${item.id}`
  return {
    id: item.id,
    identity,
    load: async () => {
      return (await loadResourceLinks(item)).map(url => ({ url }))
    },
  }
}
const nextPage = computed(() => result.value?.items.find(item => item.kind === 'page' && item.title.includes('下一页')))
async function request(id?: string, refresh = false) {
  if (busy.value) return
  if (refresh) { resolvedLinks.value = {}; linkErrors.value = {} }
  let finishSearch!: () => void
  searchFinished = new Promise<void>(resolve => { finishSearch = resolve })
  searchBusy.value = true; loadingMore.value = !!id; error.value = ''; searchStage.value = 'waiting'
  try {
    if (id) {
      const data = await telegramUserRequest<BotSearchResult>(props.token, 'select', lifecycle.signal, { id })
      if (!lifecycle.signal.aborted) result.value = data
    } else {
      await telegramUserSearchStream(props.token, lifecycle.signal, { kind: props.kind, tmdbId: props.tmdbId, ...(refresh ? { refresh: true } : {}) }, event => {
        if (lifecycle.signal.aborted) return
        result.value = event.result
        if (!event.complete) searchStage.value = 'collecting'
      })
    }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '搜索失败' }
  finally { searchBusy.value = false; loadingMore.value = false; searchStage.value = 'waiting'; finishSearch() }
  if (!lifecycle.signal.aborted) await shareRuns.restore(resources.value.map(shareResource))
}
function stopRun() { void shareRuns.stop() }
function shareSTRM(item: (typeof resources.value)[number]) { void shareRuns.submit(shareResource(item)) }
onMounted(async () => {
  document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  try { state.value = await telegramUserRequest<TelegramUserStatus>(props.token, 'config', lifecycle.signal) }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '配置读取失败' }
  finally { checking.value = false }
  if (!lifecycle.signal.aborted && state.value?.configured && state.value.hasSession) await request()
})
onBeforeUnmount(() => { lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus() })
</script>
<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="bot-search-title" class="m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-3xl overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="emit('close')">
      <div class="flex max-h-[85dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 dark:border-slate-800 p-5"><div class="min-w-0"><h2 id="bot-search-title" class="text-lg font-semibold text-slate-900 dark:text-slate-100">Bot 搜索</h2><p class="mt-1 break-words text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ title }} · @{{ state?.bot || '资源 Bot' }}</p></div><button autofocus type="button" aria-label="关闭资源搜索" class="rounded-lg px-2 py-1 text-xl text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800" @click="emit('close')">×</button></header>
        <div class="min-h-0 overflow-y-auto p-5">
          <p v-if="checking" role="status" class="py-12 text-center text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">读取 Telegram 登录状态…</p>
          <div v-else-if="!state?.configured || !state.hasSession" class="rounded-xl bg-slate-50 dark:bg-slate-950 p-6 text-sm leading-7 text-slate-600 dark:text-slate-400 dark:text-slate-500"><p>请先登录已获资源 Bot 认证的 Telegram 用户账号。</p><RouterLink to="/telegram/user" class="mt-3 inline-block font-medium text-blue-600" @click="emit('close')">配置并登录 Telegram →</RouterLink></div>
          <p v-if="searchBusy" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700"><template v-if="searchStage === 'collecting'">已收到 Bot 首批结果，正在收集后续回复…<span class="block text-xs">可先选择资源；搜索结束后按选择顺序获取链接。</span></template><template v-else>正在等待 Bot 回复并收集资源…<span class="block text-xs">通常数秒，最多等待约一分钟。</span></template></p>
          <p v-if="generating" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">{{ activeRunID ? '任务在后台执行，可继续选择其他资源；关闭页面不影响已提交的任务。' : '正在准备并提交任务，提交成功后可关闭页面。' }}<button v-if="activeRunID" type="button" :disabled="stopping" class="ml-3 underline disabled:opacity-50" @click="stopRun">{{ stopping ? '正在停止…' : '停止已提交批次' }}</button></p>
          <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <template v-if="result">
            <div class="mb-4 flex flex-wrap gap-3 text-xs text-slate-400 dark:text-slate-500"><span v-if="result.cached" class="text-emerald-600">已复用缓存 · 未发送 Bot 查询</span><span v-if="result.remaining !== undefined">本月剩余次数：{{ result.remaining }}{{ result.cached ? '（查询时）' : '' }}</span></div>
            <p v-if="result.empty" class="py-8 text-center text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">没有找到资源。</p>
            <ul v-if="resources.length" class="space-y-3"><li v-for="item in resources" :key="item.id" class="rounded-xl border border-slate-200 dark:border-slate-700 p-4"><div class="break-words text-sm leading-6"><a v-if="(resolvedLinks[item.id]?.length || 0) === 1 || item.direct" :href="resolvedLinks[item.id]?.[0] || normalize115Share(item.direct)" target="_blank" rel="noopener noreferrer" class="rounded text-blue-600 underline decoration-blue-200 underline-offset-4 hover:text-blue-800 hover:decoration-blue-500 focus-visible:outline-blue-500">{{ item.title }} <span aria-hidden="true">↗</span></a><button v-else type="button" :disabled="!!linkPending[item.id]" class="rounded text-left text-blue-600 underline decoration-blue-200 underline-offset-4 hover:text-blue-800 hover:decoration-blue-500 focus-visible:outline-blue-500 disabled:cursor-wait disabled:opacity-60" @click="openResource(item)">{{ item.title }} <span aria-hidden="true">↗</span></button></div><p v-if="linkPending[item.id]" role="status" class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ searchBusy ? '等待搜索结束后获取链接…' : '正在获取分享链接…' }}</p><p v-if="linkErrors[item.id]" role="status" class="mt-2 text-xs text-amber-700">{{ linkErrors[item.id] }}</p><div v-if="(resolvedLinks[item.id]?.length || 0) > 1" class="mt-2 flex flex-wrap gap-3"><a v-for="(link, index) in resolvedLinks[item.id]" :key="link" :href="link" target="_blank" rel="noopener noreferrer" class="text-xs text-blue-600 underline">打开分享 {{ index + 1 }} ↗</a></div><div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-slate-400 dark:text-slate-500"><span v-if="item.tmdbId === tmdbId" class="text-emerald-600">TMDB 匹配</span><span v-if="item.size">{{ item.size }}</span><button type="button" :disabled="shareRuns.pending.value[item.id] || generationStatus[item.id]?.success" class="ml-auto rounded-lg px-3 py-2 font-medium text-blue-600 hover:bg-blue-50 disabled:opacity-50" @click="shareSTRM(item)">{{ shareRuns.pending.value[item.id] ? '已加入任务…' : generationStatus[item.id]?.success ? '已生成' : '分享 STRM →' }}</button></div><p v-if="generationStatus[item.id]" role="status" class="mt-3 whitespace-pre-wrap break-words text-xs leading-6" :class="generationStatus[item.id]?.success ? 'text-emerald-600' : 'text-slate-600 dark:text-slate-400 dark:text-slate-500'">{{ generationStatus[item.id]?.message }}</p></li></ul>
            <div v-if="nextPage" class="mt-5 flex justify-center"><button type="button" :disabled="busy" class="rounded-lg border border-slate-200 dark:border-slate-700 px-5 py-2.5 text-sm text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 disabled:opacity-40" @click="request(nextPage.id)">{{ loadingMore ? '正在加载更多…' : '加载更多资源' }}</button></div>
            <p v-else-if="result.allLoaded && resources.length && !busy" role="status" class="mt-5 text-center text-xs text-slate-400 dark:text-slate-500">已加载全部资源</p>
            <details v-if="result.text" :open="!resources.length && !result.empty" class="mt-5 rounded-xl bg-slate-50 dark:bg-slate-950 p-4 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><summary class="cursor-pointer">Bot 原始回复</summary><p class="mt-3 whitespace-pre-wrap break-words leading-6">{{ result.text }}</p></details>
          </template>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-slate-100 dark:border-slate-800 p-5"><div class="text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><RouterLink to="/telegram/user" @click="emit('close')">Telegram 用户配置</RouterLink><p class="mt-1">结果缓存 24 小时；刷新搜索会重新查询 Bot 并追加结果。</p></div><button v-if="state?.configured && state.hasSession" :disabled="busy || checking" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="request(undefined, true)">刷新搜索</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>
