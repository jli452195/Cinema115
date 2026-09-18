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
const shareRuns = useShareSTRMRun(props.token, lifecycle.signal)
const generating = shareRuns.busy
const activeResource = shareRuns.activeResource
const generationStatus = shareRuns.statuses
const activeRunID = shareRuns.activeRunID
const stopping = shareRuns.stopping
const busy = computed(() => searchBusy.value || generating.value)
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const resources = computed(() => {
  const items = result.value?.items.filter(item => item.kind === 'resource') || []
  if (items.length) return items.map(item => ({ ...item, direct: '' }))
  return (result.value?.links.filter(is115Share) || []).map((link, index) => ({ id: `link:${index}:${link}`, title: `${props.title} · 资源 ${index + 1}`, kind: 'resource' as const, tmdbId: '', size: '', direct: link }))
})
function shareResource(item: (typeof resources.value)[number]): ShareSTRMResource {
  const identity = item.direct ? `bot/${props.kind}/${props.tmdbId}/direct/${normalize115Share(item.direct)}` : `bot/${props.kind}/${props.tmdbId}/item/${item.id}`
  return {
    id: item.id,
    identity,
    load: async () => {
      const selection = item.direct ? { links: [item.direct] } : await telegramUserRequest<BotSearchResult>(props.token, 'select', lifecycle.signal, { id: item.id })
      return selection.links.map(url => ({ url }))
    },
  }
}
const nextPage = computed(() => result.value?.items.find(item => item.kind === 'page' && item.title.includes('下一页')))
async function request(id?: string, refresh = false) {
  if (busy.value) return
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
  finally { searchBusy.value = false; loadingMore.value = false; searchStage.value = 'waiting' }
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
    <dialog ref="dialog" aria-labelledby="bot-search-title" class="m-auto max-h-[85dvh] w-[calc(100%-2rem)] max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-white p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="emit('close')">
      <div class="flex max-h-[85dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 p-5"><div class="min-w-0"><h2 id="bot-search-title" class="text-lg font-semibold text-slate-900">Bot 搜索</h2><p class="mt-1 break-words text-sm text-slate-500">{{ title }} · @{{ state?.bot || '资源 Bot' }}</p></div><button autofocus type="button" aria-label="关闭资源搜索" class="rounded-lg px-2 py-1 text-xl text-slate-400 hover:bg-slate-100" @click="emit('close')">×</button></header>
        <div class="min-h-0 overflow-y-auto p-5">
          <p v-if="checking" role="status" class="py-12 text-center text-sm text-slate-500">读取 Telegram 登录状态…</p>
          <div v-else-if="!state?.configured || !state.hasSession" class="rounded-xl bg-slate-50 p-6 text-sm leading-7 text-slate-600"><p>请先登录已获资源 Bot 认证的 Telegram 用户账号。</p><RouterLink to="/telegram/user" class="mt-3 inline-block font-medium text-blue-600" @click="emit('close')">配置并登录 Telegram →</RouterLink></div>
          <p v-if="busy && !generating" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700"><template v-if="searchStage === 'collecting'">已收到 Bot 首批结果，正在收集后续回复…<span class="block text-xs">资源可以先查看，完整结果收集结束后会自动更新。</span></template><template v-else>正在等待 Bot 回复并收集资源…<span class="block text-xs">通常数秒，最多等待约一分钟。</span></template></p>
          <p v-if="generating" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">{{ activeRunID ? '任务在后台执行，关闭弹窗或页面后仍会继续；生成成功后按配置发送 Telegram 通知。' : '正在准备并提交任务，提交成功后可关闭页面。' }}<button v-if="activeRunID" type="button" :disabled="stopping" class="ml-3 underline disabled:opacity-50" @click="stopRun">{{ stopping ? '正在停止…' : '停止任务' }}</button></p>
          <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <template v-if="result">
            <div class="mb-4 flex flex-wrap gap-3 text-xs text-slate-400"><span v-if="result.cached" class="text-emerald-600">已复用缓存 · 未发送 Bot 查询</span><span v-if="result.remaining !== undefined">本月剩余次数：{{ result.remaining }}{{ result.cached ? '（查询时）' : '' }}</span></div>
            <p v-if="result.empty" class="py-8 text-center text-sm text-slate-500">没有找到资源。</p>
            <ul v-if="resources.length" class="space-y-3"><li v-for="item in resources" :key="item.id" class="rounded-xl border border-slate-200 p-4"><p class="break-words text-sm leading-6 text-slate-700">{{ item.title }}</p><div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-slate-400"><span v-if="item.tmdbId === tmdbId" class="text-emerald-600">TMDB 匹配</span><span v-if="item.size">{{ item.size }}</span><button type="button" :disabled="busy || generationStatus[item.id]?.success" class="ml-auto rounded-lg px-3 py-2 font-medium text-blue-600 hover:bg-blue-50 disabled:opacity-50" @click="shareSTRM(item)">{{ activeResource === item.id ? '处理中…' : generationStatus[item.id]?.success ? '已生成' : '分享 STRM →' }}</button></div><p v-if="generationStatus[item.id]" role="status" class="mt-3 whitespace-pre-wrap break-words text-xs leading-6" :class="generationStatus[item.id]?.success ? 'text-emerald-600' : 'text-slate-600'">{{ generationStatus[item.id]?.message }}</p></li></ul>
            <div v-if="nextPage" class="mt-5 flex justify-center"><button type="button" :disabled="busy" class="rounded-lg border border-slate-200 px-5 py-2.5 text-sm text-slate-700 hover:bg-slate-50 disabled:opacity-40" @click="request(nextPage.id)">{{ loadingMore ? '正在加载更多…' : '加载更多资源' }}</button></div>
            <p v-else-if="result.allLoaded && resources.length && !busy" role="status" class="mt-5 text-center text-xs text-slate-400">已加载全部资源</p>
            <details v-if="result.text" :open="!resources.length && !result.empty" class="mt-5 rounded-xl bg-slate-50 p-4 text-xs text-slate-500"><summary class="cursor-pointer">Bot 原始回复</summary><p class="mt-3 whitespace-pre-wrap break-words leading-6">{{ result.text }}</p></details>
          </template>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-slate-100 p-5"><div class="text-xs text-slate-500"><RouterLink to="/telegram/user" @click="emit('close')">Telegram 用户配置</RouterLink><p class="mt-1">结果缓存 24 小时；刷新搜索会重新查询 Bot 并追加结果。</p></div><button v-if="state?.configured && state.hasSession" :disabled="busy || checking" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="request(undefined, true)">刷新搜索</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>
