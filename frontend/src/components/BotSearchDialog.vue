<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { is115Share } from '../utils/shareLink'
import { cancelShareRun, newShareRunID, savedSTRMSettings, ShareRunError, submitShareRun, waitForShareRun, type ShareRun } from '../utils/shareGeneration'
import { telegramUserRequest, type TelegramUserStatus, type BotSearchResult } from '../types/telegramUser'
const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const state = ref<TelegramUserStatus>()
const result = ref<BotSearchResult>()
const busy = ref(false)
const checking = ref(true)
const error = ref('')
const notice = ref('')
const generating = ref(false)
const activeResource = ref('')
const generationStatus = ref<Record<string, { message: string; success: boolean }>>({})
const activeRunID = ref('')
const stopping = ref(false)
const runStorageKey = 'cinema115.bot-strm-runs.v1'
type SavedRun = { key: string; id: string; time: number }
function savedRuns(): SavedRun[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(runStorageKey) || '[]')
    return Array.isArray(value) ? value.filter((entry): entry is SavedRun => typeof entry?.key === 'string' && /^[a-f0-9]{32}$/.test(entry?.id) && Date.now() - entry.time < 86400000).slice(-100) : []
  } catch { return [] }
}
function runKey(item: (typeof resources.value)[number]) {
  // 仅保存任务编号和资源标识，不把链接、Cookie 或令牌写入浏览器。
  return `${props.kind}/${props.tmdbId}/${item.direct ? item.title : item.id}`
}
function rememberRun(key: string, id?: string) {
  const entries = savedRuns().filter(entry => entry.key !== key)
  if (id) entries.push({ key, id, time: Date.now() })
  try { localStorage.setItem(runStorageKey, JSON.stringify(entries.slice(-100))) } catch { /* 禁用存储时仍可在分享日志查看任务。 */ }
}
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const resources = computed(() => {
  const items = result.value?.items.filter(item => item.kind === 'resource') || []
  if (items.length) return items.map(item => ({ ...item, direct: '' }))
  return (result.value?.links.filter(is115Share) || []).map((link, index) => ({ id: `link:${link}`, title: `${props.title} · 资源 ${index + 1}`, kind: 'resource', tmdbId: '', size: '', direct: link }))
})
const pages = computed(() => result.value?.items.filter(item => item.kind === 'page') || [])
async function request(id?: string, refresh = false) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const data = await telegramUserRequest<BotSearchResult>(props.token, id ? 'select' : 'search', lifecycle.signal, id ? { id } : { kind: props.kind, tmdbId: props.tmdbId, ...(refresh ? { refresh: true } : {}) })
    if (!lifecycle.signal.aborted) {
      result.value = data
    }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '搜索失败' }
  finally { busy.value = false }
  if (!lifecycle.signal.aborted) await restoreRuns()
}
function showRun(item: (typeof resources.value)[number], run: ShareRun) {
  let written = 0, metadata = 0
  for (const entry of run.items) { written += entry.result?.count ?? 0; metadata += entry.result?.metadataCount ?? 0 }
  const current = run.items.findIndex(entry => entry.status === 'running')
  const failures = run.items.flatMap((entry, index) => entry.error ? [`链接 ${index + 1}：${entry.error}`] : [])
  let message = current >= 0 ? `${current + 1}/${run.items.length} · ${run.items[current]?.message}` : '已提交后台，等待执行'
  if (run.status === 'success') message = `生成成功：新增 ${written} 个 STRM${metadata ? `，元数据 ${metadata} 个` : ''} · ${run.items[0]?.result?.directory ?? ''}`
  else if (run.status === 'failed') message = `新增 ${written} 个 STRM；${failures.join('；')}`
  else if (run.status === 'cancelled') message = `后台任务已停止，新增 ${written} 个 STRM；请在分享日志核对已转存目录。`
  else if (run.status === 'cancelling') message = '正在停止后台任务，等待保存处理结果…'
  generationStatus.value[item.id] = { message, success: run.status === 'success' }
}
async function followRun(item: (typeof resources.value)[number], id: string) {
  busy.value = true; generating.value = true; activeResource.value = item.id; activeRunID.value = id
  try {
    await waitForShareRun(props.token, id, lifecycle.signal, run => showRun(item, run))
    rememberRun(runKey(item))
  } catch (e) {
    if (!lifecycle.signal.aborted) {
      if (e instanceof ShareRunError && e.status === 404) rememberRun(runKey(item))
      generationStatus.value[item.id] = { message: `暂时无法查询进度：${e instanceof Error ? e.message : '网络异常'}。后台任务不会因页面断开而取消，请查看分享日志或 Telegram 通知。`, success: false }
    }
  } finally { busy.value = false; generating.value = false; activeResource.value = ''; activeRunID.value = ''; stopping.value = false }
}
async function restoreRuns() {
  for (const item of resources.value) {
    if (lifecycle.signal.aborted || generating.value) return
    const existing = savedRuns().find(entry => entry.key === runKey(item))
    if (existing) await followRun(item, existing.id)
  }
}
async function stopRun() {
  if (!activeRunID.value || stopping.value) return
  stopping.value = true
  try { await cancelShareRun(props.token, activeRunID.value) }
  catch (e) { error.value = e instanceof Error ? e.message : '停止任务失败'; stopping.value = false }
}
async function shareSTRM(item: (typeof resources.value)[number]) {
  if (busy.value || generationStatus.value[item.id]?.success) return
  const existing = savedRuns().find(entry => entry.key === runKey(item))
  if (existing) { await followRun(item, existing.id); return }
  busy.value = true; generating.value = true; activeResource.value = item.id; error.value = ''; notice.value = ''
  const signal = lifecycle.signal
  const update = (message: string, success = false) => { generationStatus.value[item.id] = { message, success } }
  update('正在读取生成配置…')
  try {
    const settings = await savedSTRMSettings(props.token, signal)
    update('正在获取资源链接…')
    const selection = item.direct ? { links: [item.direct] } : await telegramUserRequest<BotSearchResult>(props.token, 'select', signal, { id: item.id })
    const links = [...new Set(selection.links.filter(is115Share))]
    if (!links.length) throw new Error('Bot 未返回可用的 115 链接，请查看原始回复或刷新搜索')
    signal.throwIfAborted()
    const id = newShareRunID()
    rememberRun(runKey(item), id)
    update('正在提交全部链接到后台…')
    // 一次交给后端排队，关闭页面后其余链接仍会执行；模式与账号由后端固定。
    const run = await submitShareRun(props.token, id, links.map(url => ({ ...settings, mode: '', url, password: '', cid: '0' })))
    if (!signal.aborted) { showRun(item, run); await followRun(item, id) }
  } catch (e) {
    if (!lifecycle.signal.aborted) update(`${e instanceof Error ? e.message : '提交失败'}；若未收到确认，请重新打开弹窗查询原任务或查看分享日志。`)
  } finally { busy.value = false; generating.value = false; activeResource.value = '' }
}
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
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 p-5"><div class="min-w-0"><h2 id="bot-search-title" class="text-lg font-semibold text-slate-900">Bot 搜索</h2><p class="mt-1 break-words text-sm text-slate-500">{{ title }} · @ayclub_bot</p></div><button autofocus type="button" aria-label="关闭资源搜索" class="rounded-lg px-2 py-1 text-xl text-slate-400 hover:bg-slate-100" @click="emit('close')">×</button></header>
        <div class="min-h-0 overflow-y-auto p-5">
          <p v-if="checking" role="status" class="py-12 text-center text-sm text-slate-500">读取 Telegram 登录状态…</p>
          <div v-else-if="!state?.configured || !state.hasSession" class="rounded-xl bg-slate-50 p-6 text-sm leading-7 text-slate-600"><p>请先登录已获资源 Bot 认证的 Telegram 用户账号。</p><RouterLink to="/telegram/user" class="mt-3 inline-block font-medium text-blue-600" @click="emit('close')">配置并登录 Telegram →</RouterLink></div>
          <p v-if="busy && !generating" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">正在等待 Bot 回复并收集资源…<span class="block text-xs">通常数秒，最多等待约一分钟。</span></p>
          <p v-if="generating" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm leading-6 text-blue-700">{{ activeRunID ? '任务在后台执行，关闭弹窗或页面后仍会继续；生成成功后按配置发送 Telegram 通知。' : '正在准备并提交任务，提交成功后可关闭页面。' }}<button v-if="activeRunID" type="button" :disabled="stopping" class="ml-3 underline disabled:opacity-50" @click="stopRun">{{ stopping ? '正在停止…' : '停止任务' }}</button></p>
          <p v-if="error" role="alert" class="mb-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <p v-if="notice" role="status" class="mb-4 text-xs text-emerald-700">{{ notice }}</p>
          <template v-if="result">
            <div class="mb-4 flex flex-wrap gap-3 text-xs text-slate-400"><span v-if="result.cached" class="text-emerald-600">已复用缓存 · 未发送 Bot 查询</span><span v-if="result.remaining !== undefined">本月剩余次数：{{ result.remaining }}{{ result.cached ? '（查询时）' : '' }}</span><span v-if="result.page !== undefined">第 {{ result.page }} 页</span></div>
            <p v-if="result.empty" class="py-8 text-center text-sm text-slate-500">没有找到资源。</p>
            <ul v-if="resources.length" class="space-y-3"><li v-for="item in resources" :key="item.id" class="rounded-xl border border-slate-200 p-4"><p class="break-words text-sm leading-6 text-slate-700">{{ item.title }}</p><div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-slate-400"><span v-if="item.tmdbId === tmdbId" class="text-emerald-600">TMDB 匹配</span><span v-if="item.size">{{ item.size }}</span><button type="button" :disabled="busy || generationStatus[item.id]?.success" class="ml-auto rounded-lg px-3 py-2 font-medium text-blue-600 hover:bg-blue-50 disabled:opacity-50" @click="shareSTRM(item)">{{ activeResource === item.id ? '处理中…' : generationStatus[item.id]?.success ? '已生成' : '分享 STRM →' }}</button></div><p v-if="generationStatus[item.id]" role="status" class="mt-3 whitespace-pre-wrap break-words text-xs leading-6" :class="generationStatus[item.id]?.success ? 'text-emerald-600' : 'text-slate-600'">{{ generationStatus[item.id]?.message }}</p></li></ul>
            <div v-if="pages.length" class="mt-5 flex flex-wrap justify-center gap-3"><button v-for="item in pages" :key="item.id" :disabled="busy" class="rounded-lg border border-slate-200 px-4 py-2 text-sm disabled:opacity-40" @click="request(item.id)">{{ item.title }}</button></div>
            <details v-if="result.text" :open="!resources.length && !result.empty" class="mt-5 rounded-xl bg-slate-50 p-4 text-xs text-slate-500"><summary class="cursor-pointer">Bot 原始回复</summary><p class="mt-3 whitespace-pre-wrap break-words leading-6">{{ result.text }}</p></details>
          </template>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-slate-100 p-5"><div class="text-xs text-slate-500"><RouterLink to="/telegram/user" @click="emit('close')">Telegram 用户配置</RouterLink><p class="mt-1">结果缓存 24 小时；刷新搜索会重新查询 Bot。</p></div><button v-if="state?.configured && state.hasSession" :disabled="busy || checking" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40" @click="request(undefined, true)">刷新搜索</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>
