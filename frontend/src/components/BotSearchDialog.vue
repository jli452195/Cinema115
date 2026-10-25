<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useShareSTRMRun, type ShareSTRMResource } from '../composables/useShareSTRMRun'
import { is115Share, normalize115Share } from '../utils/shareLink'
import { telegramUserRequest, telegramUserSearchStream, TelegramSearchError, type TelegramUserStatus, type BotSearchResult, type BotSearchProgress } from '../types/telegramUser'
import telegramLogo from '../assets/telegram/telegram.svg'
const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; themeColors?: Record<string, string> }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const viewportStyle = ref<Record<string, string>>({})
const dialogStyle = computed(() => ({ ...props.themeColors, ...viewportStyle.value }))
const query = ref('')
const lockedResourceOrder = ref<string[]>([])
const state = ref<TelegramUserStatus>()
const result = ref<BotSearchResult>()
const searchBusy = ref(false)
const loadingMore = ref(false)
const checking = ref(true)
const error = ref('')
const searchStage = ref<BotSearchProgress['stage']>('waiting')
const queuePosition = ref(0)
const retryAt = ref(0)
const clockNow = ref(Date.now())
const retrySeconds = computed(() => Math.max(0, Math.ceil((retryAt.value - clockNow.value) / 1000)))
let countdown: ReturnType<typeof setInterval> | undefined
function showError(e: unknown): string {
  if (e instanceof TelegramSearchError && e.retryAt) {
    const deadline = Date.parse(e.retryAt)
    if (Number.isFinite(deadline)) { retryAt.value = deadline; clockNow.value = Date.now() }
  }
  return e instanceof Error ? e.message : '搜索失败'
}
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
const orderedResources = computed(() => {
  const positions = new Map(lockedResourceOrder.value.map((id, index) => [id, index]))
  return [...resources.value].sort((a, b) => {
    const position = (positions.get(a.id) ?? Infinity) - (positions.get(b.id) ?? Infinity)
    return position || Number(matchesCurrent(b)) - Number(matchesCurrent(a))
  })
})
function lockResourceOrder() {
  // Append later batches once the user interacts, keeping actions in place.
  if (searchBusy.value && !lockedResourceOrder.value.length) lockedResourceOrder.value = orderedResources.value.map(item => item.id)
}
const filteredResources = computed(() => {
  const filter = query.value.trim().toLowerCase()
  return orderedResources.value.filter(item => `${item.title} ${item.size || ''} ${item.tmdbId || ''}`.toLowerCase().includes(filter))
})
function matchesCurrent(item: (typeof resources.value)[number]) {
  return item.tmdbId === props.tmdbId
}
function clearQuery() {
  query.value = ''
  dialog.value?.querySelector<HTMLInputElement>('input[type="search"]')?.focus({ preventScroll: true })
}
async function revealLinks(id: string) {
  await nextTick()
  const resource = Array.from(dialog.value?.querySelectorAll<HTMLElement>('[data-resource-id]') || []).find(element => element.dataset.resourceId === id)
  const links = resource?.querySelector<HTMLElement>('.bot-share-links')
  const body = dialog.value?.querySelector<HTMLElement>('.bot-body')
  if (links && body) {
    const target = links.getBoundingClientRect()
    const viewport = body.getBoundingClientRect()
    const top = target.top < viewport.top ? target.top - viewport.top : Math.max(0, Math.min(target.bottom - viewport.bottom, target.top - viewport.top))
    body.scrollBy({ top, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })
  }
  links?.querySelector<HTMLAnchorElement>('a')?.focus({ preventScroll: true })
}
// 打开分享与 STRM 生成共用取链队列，避免同时点击导致 Bot 回复错配。
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
  }).catch(e => { if (!lifecycle.signal.aborted) showError(e); throw e }).finally(() => { delete linkPending.value[item.id]; linkRequests.delete(item.id) })
  linkRequests.set(item.id, task)
  linkQueue = task.catch(() => {})
  return task
}
async function openResource(item: (typeof resources.value)[number]) {
  if (linkPending.value[item.id]) return
  if ((resolvedLinks.value[item.id]?.length || 0) > 1) { await revealLinks(item.id); return }
  const trigger = document.activeElement as HTMLElement | null
  const returnFocus = trigger?.dataset.botAction === 'open'
  linkErrors.value[item.id] = ''
  // 在点击事件中预开窗口，避免异步取链完成后被浏览器拦截。
  const tab = window.open('about:blank', '_blank')
  if (tab) { tab.opener = null; tab.document.title = '正在获取 115 分享链接…' }
  try {
    const links = await loadResourceLinks(item)
    if (links.length === 1 && tab && !tab.closed) tab.location.replace(links[0]!)
    else {
      tab?.close()
      if (links.length > 1) await revealLinks(item.id)
      else linkErrors.value[item.id] = '浏览器未打开新窗口，请再次点击“打开分享”。'
    }
  } catch (e) {
    tab?.close()
    if (!lifecycle.signal.aborted) linkErrors.value[item.id] = showError(e)
  } finally {
    await nextTick()
    if (returnFocus && dialog.value?.open && (document.activeElement === document.body || document.activeElement === dialog.value)) {
      const resource = Array.from(dialog.value.querySelectorAll<HTMLElement>('[data-resource-id]')).find(element => element.dataset.resourceId === item.id)
      resource?.querySelector<HTMLElement>('[data-bot-action="open"]')?.focus({ preventScroll: true })
    }
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
  if (busy.value || retrySeconds.value > 0) return
  lockedResourceOrder.value = []
  if (refresh) { resolvedLinks.value = {}; linkErrors.value = {} }
  let finishSearch!: () => void
  searchFinished = new Promise<void>(resolve => { finishSearch = resolve })
  searchBusy.value = true; loadingMore.value = !!id; error.value = ''; searchStage.value = 'waiting'; queuePosition.value = 0; retryAt.value = 0
  try {
    if (id) {
      const data = await telegramUserRequest<BotSearchResult>(props.token, 'select', lifecycle.signal, { id })
      if (!lifecycle.signal.aborted) result.value = data
    } else {
      await telegramUserSearchStream(props.token, lifecycle.signal, { kind: props.kind, tmdbId: props.tmdbId, ...(refresh ? { refresh: true } : {}) }, event => {
        if (lifecycle.signal.aborted) return
        result.value = event.result
        if (!event.complete) searchStage.value = 'collecting'
      }, progress => { if (!lifecycle.signal.aborted) { searchStage.value = progress.stage; queuePosition.value = progress.position || 0 } })
    }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = showError(e) }
  finally { searchBusy.value = false; loadingMore.value = false; searchStage.value = 'waiting'; finishSearch() }
  if (!lifecycle.signal.aborted) await shareRuns.restore(resources.value.map(shareResource))
}
function stopRun() { void shareRuns.stop() }
function shareSTRM(item: (typeof resources.value)[number]) { void shareRuns.submit(shareResource(item)) }
function syncVisualViewport() {
  const viewport = window.visualViewport
  viewportStyle.value = {
    '--bot-visible-height': `${viewport?.height ?? window.innerHeight}px`,
    '--bot-visible-top': `${viewport?.offsetTop ?? 0}px`,
    '--bot-visible-bottom': `${viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0}px`,
  }
}
onMounted(async () => {
  syncVisualViewport()
  window.visualViewport?.addEventListener('resize', syncVisualViewport)
  window.visualViewport?.addEventListener('scroll', syncVisualViewport)
  countdown = setInterval(() => { clockNow.value = Date.now() }, 1000)
  document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  try { state.value = await telegramUserRequest<TelegramUserStatus>(props.token, 'config', lifecycle.signal) }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '配置读取失败' }
  finally { checking.value = false }
  if (!lifecycle.signal.aborted && state.value?.configured && state.value.hasSession) await request()
})
onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', syncVisualViewport)
  window.visualViewport?.removeEventListener('scroll', syncVisualViewport)
  clearInterval(countdown); lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus()
})
</script>
<template>
  <Teleport to="body">
    <dialog ref="dialog" :style="dialogStyle" aria-labelledby="bot-search-title" aria-describedby="bot-work-title" class="bot-dialog media-search-dialog" @cancel.prevent="emit('close')">
      <div class="bot-layout">
        <header class="bot-header">
          <div class="bot-brand"><img :src="telegramLogo" alt="" width="36" height="36" /></div>
          <div class="bot-heading">
            <h2 id="bot-search-title">Telegram 资源搜索</h2>
            <p id="bot-work-title" :title="title">{{ title }}<span v-if="state?.bot" class="bot-identity">@{{ state.bot }}</span></p>
          </div>
          <button autofocus type="button" aria-label="关闭资源搜索" class="bot-icon-button" @click="emit('close')">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        <div class="bot-toolbar">
          <label class="bot-search-field">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></svg>
            <span class="sr-only">筛选当前结果</span>
            <input v-model="query" type="search" placeholder="筛选资源名称、大小或 TMDB ID" :disabled="!resources.length" />
          </label>
          <div class="bot-tools">
            <button type="button" :disabled="!state?.configured || !state.hasSession || busy || checking || retrySeconds > 0" class="bot-tool-button" @click="request(undefined, true)">
              <svg aria-hidden="true" :class="{ 'bot-spinning': searchBusy }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg>
              刷新搜索
            </button>
            <RouterLink to="/telegram/user" class="bot-tool-button" @click="emit('close')">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="3" fill="var(--bot-container)" /><circle cx="15" cy="17" r="3" fill="var(--bot-container)" /></svg>
              Telegram 配置
            </RouterLink>
          </div>
        </div>

        <div class="bot-body">
          <p v-if="checking" role="status" class="bot-notice bot-info">读取 Telegram 登录状态…</p>
          <div v-else-if="!state?.configured || !state.hasSession" class="bot-setup">
            <h3>登录 Telegram 后搜索资源</h3>
            <p>请使用已获资源 Bot 认证的 Telegram 用户账号。</p>
            <RouterLink to="/telegram/user" class="bot-button bot-primary-button" @click="emit('close')">配置并登录 Telegram</RouterLink>
          </div>

          <div v-if="searchBusy" role="status" class="bot-notice bot-info">
            <template v-if="loadingMore"><strong>正在加载更多资源…</strong><span>已展示的结果仍可查看，等待 Bot 返回后续资源。</span></template>
            <template v-else-if="searchStage === 'queued'"><strong>搜索排队中<span v-if="queuePosition">，第 {{ queuePosition }} 位</span></strong><span>尚未发送查询，关闭窗口可取消排队。</span></template>
            <template v-else-if="searchStage === 'collecting'"><strong>已收到首批结果，继续收集回复…</strong><span>关闭窗口后继续收尾并缓存，重新打开可查看。</span></template>
            <template v-else><strong>正在等待 Bot 回复…</strong><span>单次查询最多等待 45 秒，已开始的查询会在后台收尾。</span></template>
          </div>
          <div v-if="generating" role="status" class="bot-notice bot-info bot-task-notice">
            <div><strong>{{ activeRunID ? 'STRM 任务在后台执行' : '正在准备 STRM 任务' }}</strong><span>{{ activeRunID ? '可以继续选择其他资源，已提交任务不受窗口关闭影响。' : '提交成功后可关闭窗口。' }}</span></div>
            <button v-if="activeRunID" type="button" :disabled="stopping" class="bot-text-button" @click="stopRun">{{ stopping ? '正在停止…' : '停止已提交批次' }}</button>
          </div>
          <p v-if="error" role="alert" class="bot-notice bot-error">{{ error }}</p>
          <p v-if="retryAt" role="status" class="bot-notice bot-warning">{{ retrySeconds > 0 ? '正在隔离迟到回复，' + retrySeconds + ' 秒后可重试。' : '等待时间已结束，可以重新搜索。' }}</p>

          <template v-if="result">
            <p v-if="result.partial" role="status" class="bot-notice bot-warning">{{ result.warning || '当前为部分结果，稍后可刷新补充。' }}</p>
            <div class="bot-results-heading">
              <h3>115 资源</h3>
              <span role="status">{{ query.trim() ? '显示 ' + filteredResources.length + ' / ' + resources.length + ' 个资源' : resources.length + ' 个资源' }}</span>
            </div>
            <div v-if="result.remaining !== undefined || (result.cached && !searchBusy)" class="bot-search-meta">
              <span v-if="result.remaining !== undefined">本月剩余 {{ result.remaining }} 次{{ result.cached ? '（查询时）' : '' }}</span>
              <span v-if="result.cached && !searchBusy" class="bot-cache-status">
                <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg>
                已复用缓存，未发送 Bot 查询
              </span>
            </div>
            <div v-if="!resources.length && result.empty && !searchBusy" role="status" class="bot-empty">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4M8 10.5h5" /></svg>
              <p>Bot 暂未找到该影视的资源</p>
              <span>可以稍后刷新搜索</span>
            </div>
            <div v-else-if="resources.length && !filteredResources.length" role="status" class="bot-empty">
              <p>没有符合筛选条件的资源</p>
              <button type="button" class="bot-text-button" @click="clearQuery">清除筛选</button>
            </div>

            <ul v-if="filteredResources.length" aria-label="Bot 资源列表" class="bot-resource-list" @pointerenter="lockResourceOrder" @focusin="lockResourceOrder">
              <li v-for="item in filteredResources" :key="item.id" :data-resource-id="item.id" class="bot-resource">
                <div class="bot-resource-main">
                  <div class="bot-resource-info">
                    <h3>{{ item.title }}</h3>
                    <div class="bot-resource-meta">
                      <span v-if="matchesCurrent(item)" class="bot-badge bot-info" data-current-match>
                        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 4 4L19 6" /></svg>
                        匹配当前条目
                      </span>
                      <span v-else-if="item.tmdbId" class="bot-badge bot-neutral">相关资源</span>
                      <span v-else class="bot-badge bot-neutral">未标注 TMDB</span>
                      <span v-if="item.size">大小 {{ item.size }}</span>
                      <span v-if="item.tmdbId">TMDB {{ item.tmdbId }}</span>
                    </div>
                  </div>
                  <div class="bot-actions">
                    <a v-if="(resolvedLinks[item.id]?.length || 0) === 1 || item.direct" :href="resolvedLinks[item.id]?.[0] || normalize115Share(item.direct)" target="_blank" rel="noopener noreferrer" data-bot-action="open" class="bot-button bot-outlined-button">
                      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6M20 4 10 14M10 4H4v16h16v-6" /></svg>
                      打开分享
                    </a>
                    <button v-else type="button" data-bot-action="open" :disabled="!!linkPending[item.id] || retrySeconds > 0" class="bot-button bot-outlined-button" @click="openResource(item)">
                      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6M20 4 10 14M10 4H4v16h16v-6" /></svg>
                      {{ linkPending[item.id] ? '获取链接…' : (resolvedLinks[item.id]?.length || 0) > 1 ? '选择分享' : '打开分享' }}
                    </button>
                    <button type="button" data-bot-action="strm" :disabled="shareRuns.pending.value[item.id] || generationStatus[item.id]?.success || retrySeconds > 0" class="bot-button bot-primary-button" @click="shareSTRM(item)">
                      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Zm0 0v6h6" /><path d="m10 12 5 3-5 3Z" /></svg>
                      {{ shareRuns.pending.value[item.id] ? '已加入任务…' : generationStatus[item.id]?.success ? '已生成' : '分享 STRM' }}
                    </button>
                  </div>
                </div>
                <p v-if="linkPending[item.id]" role="status" class="bot-notice bot-info">{{ searchBusy ? '等待搜索结束后获取链接…' : '正在获取分享链接…' }}</p>
                <p v-if="linkErrors[item.id]" role="status" class="bot-notice bot-warning">{{ linkErrors[item.id] }}</p>
                <div v-if="(resolvedLinks[item.id]?.length || 0) > 1" class="bot-share-links">
                  <p>该资源包含 {{ resolvedLinks[item.id]?.length }} 个分享，请选择打开。</p>
                  <div>
                    <a v-for="(link, index) in resolvedLinks[item.id]" :key="link" :href="link" target="_blank" rel="noopener noreferrer" class="bot-button bot-outlined-button">
                      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6M20 4 10 14M10 4H4v16h16v-6" /></svg>
                      打开分享 {{ index + 1 }}
                    </a>
                  </div>
                </div>
                <p v-if="generationStatus[item.id]" role="status" class="bot-notice bot-generation-status" :class="generationStatus[item.id]?.success ? 'bot-success' : 'bot-info'">{{ generationStatus[item.id]?.message }}</p>
              </li>
            </ul>

            <div v-if="nextPage" class="bot-list-end">
              <button type="button" :disabled="busy || retrySeconds > 0" class="bot-button bot-outlined-button" @click="request(nextPage.id)">{{ loadingMore ? '正在加载更多…' : '加载更多资源' }}</button>
            </div>
            <p v-else-if="result.allLoaded && resources.length && !busy" role="status" class="bot-all-loaded">已加载全部资源</p>
            <details v-if="result.text" :open="!resources.length" class="bot-raw-reply">
              <summary><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 5 7 7-7 7" /></svg>Bot 原始回复</summary>
              <p>{{ result.text }}</p>
            </details>
          </template>
        </div>
        <footer class="search-log-footer"><span v-if="state?.configured && state.hasSession">结果缓存 24 小时；刷新会重新查询 Bot 并追加结果。</span><RouterLink :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click="emit('close')">查看日志</RouterLink></footer>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.bot-dialog {
  --bot-max-height: 90dvh;
  --bot-container: var(--search-dialog-container);
  --bot-card: var(--search-dialog-card);
  --bot-text: var(--search-dialog-text);
  --bot-muted: var(--search-dialog-muted);
  --bot-outline: var(--search-dialog-outline);
  --bot-primary: var(--search-dialog-primary);
  --bot-tonal: var(--search-dialog-tonal);
  --bot-on-tonal: var(--search-dialog-on-tonal);
  --bot-success: #246044;
  --bot-success-bg: #e6f2e9;
  --bot-warning: #765500;
  --bot-warning-bg: #fff1cd;
  --bot-error: #a12b2b;
  --bot-error-bg: #ffe9e7;
  margin: auto;
  padding: 0;
  width: min(960px, calc(100% - 48px));
  max-width: none;
  max-height: var(--bot-max-height);
  border-radius: 28px;
  color: var(--bot-text);
  color-scheme: light;
  overflow: hidden;
}
.dark .bot-dialog {
  --bot-container: var(--search-dialog-container);
  --bot-card: var(--search-dialog-card);
  --bot-text: var(--search-dialog-text);
  --bot-muted: var(--search-dialog-muted);
  --bot-outline: var(--search-dialog-outline);
  --bot-primary: var(--search-dialog-primary);
  --bot-tonal: var(--search-dialog-tonal);
  --bot-on-tonal: var(--search-dialog-on-tonal);
  --bot-success: #a5d8b8;
  --bot-success-bg: #223d31;
  --bot-warning: #eed085;
  --bot-warning-bg: #42371e;
  --bot-error: #ffb4ab;
  --bot-error-bg: #502c2b;
  color-scheme: dark;
}
.bot-layout { display: flex; flex-direction: column; max-height: var(--bot-max-height); }
.bot-header { display: flex; flex-shrink: 0; align-items: center; gap: 14px; padding: 24px 24px 16px; }
.bot-brand { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border-radius: 14px; background: var(--bot-card); }
.bot-heading { flex: 1; min-width: 0; }
.bot-heading h2 { margin: 0; font-size: 20px; line-height: 28px; font-weight: 650; }
.bot-heading p { margin: 4px 0 0; color: var(--bot-muted); font-size: 13px; line-height: 20px; overflow-wrap: anywhere; }
.bot-identity { display: inline-block; margin-left: 10px; font-size: 12px; }
.bot-icon-button { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border: 0; border-radius: 50%; background: transparent; color: var(--bot-muted); }
.bot-icon-button svg { width: 24px; height: 24px; }
.bot-toolbar { display: flex; flex-shrink: 0; align-items: center; gap: 12px; padding: 0 24px 20px; }
.bot-search-field { display: flex; align-items: center; flex: 1; min-width: 0; gap: 10px; min-height: 48px; padding: 0 14px; border: 1px solid transparent; border-radius: 14px; background: var(--bot-card); color: var(--bot-muted); }
.bot-search-field:focus-within { border-color: var(--bot-primary); }
.bot-search-field svg { width: 20px; height: 20px; flex-shrink: 0; }
.bot-search-field input { width: 100%; min-width: 0; height: 46px; border: 0; padding: 0; outline: none; background: transparent; color: var(--bot-text); font-size: 13px; }
.bot-search-field input::placeholder { color: var(--bot-muted); opacity: .8; }
.bot-tools { display: flex; gap: 4px; flex-shrink: 0; }
.bot-tool-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 48px; padding: 0 12px; border: 0; border-radius: 24px; background: transparent; color: var(--bot-muted); font-size: 12px; font-weight: 550; text-decoration: none; white-space: nowrap; }
.bot-tool-button svg { width: 18px; height: 18px; }
.bot-body { min-height: 0; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.bot-results-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.bot-results-heading h3 { margin: 0; font-size: 13px; line-height: 20px; font-weight: 600; }
.bot-results-heading span { color: var(--bot-muted); font-size: 12px; }
.bot-search-meta { display: flex; flex-wrap: wrap; gap: 6px 16px; margin-bottom: 14px; color: var(--bot-muted); font-size: 11px; line-height: 18px; }
.bot-cache-status { display: inline-flex; align-items: center; gap: 5px; }
.bot-cache-status svg { width: 14px; height: 14px; }
.bot-resource-list { display: grid; gap: 12px; margin: 0; padding: 0; list-style: none; }
.bot-resource { min-width: 0; padding: 20px; border: 1px solid var(--search-dialog-card-border); border-radius: 20px; background: var(--bot-card); }
.bot-resource-main { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 20px; }
.bot-resource-info { min-width: 0; }
.bot-resource-info h3 { margin: 0; font-size: 15px; line-height: 24px; font-weight: 600; overflow-wrap: anywhere; }
.bot-resource-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px; margin-top: 12px; color: var(--bot-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.bot-badge { display: inline-flex; align-items: center; gap: 4px; padding: 3px 8px; border-radius: 8px; font-size: 11px; line-height: 18px; font-weight: 550; }
.bot-badge svg { width: 14px; height: 14px; flex-shrink: 0; }
.bot-neutral { background: color-mix(in srgb, var(--bot-outline) 18%, transparent); color: var(--bot-muted); }
.bot-actions { display: flex; align-items: center; gap: 8px; }
.bot-info { color: var(--bot-on-tonal); background: var(--bot-tonal); }
.bot-success { color: var(--bot-success); background: var(--bot-success-bg); }
.bot-warning { color: var(--bot-warning); background: var(--bot-warning-bg); }
.bot-error { color: var(--bot-error); background: var(--bot-error-bg); }
.bot-notice { margin: 0 0 14px; padding: 12px 14px; border-radius: 12px; font-size: 12px; line-height: 22px; overflow-wrap: anywhere; }
.bot-notice.bot-info { border: 1px solid color-mix(in srgb, var(--bot-outline) 45%, transparent); background: var(--bot-card); color: var(--bot-text); }
.bot-notice strong { font-weight: 600; }
.bot-notice > span, .bot-task-notice > div > span { display: block; font-size: 11px; }
.bot-resource .bot-notice { margin: 12px 0 0; }
.bot-generation-status { white-space: pre-wrap; }
.bot-task-notice { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; }
.bot-text-button { min-height: 48px; padding: 10px 12px; border: 0; border-radius: 24px; background: transparent; color: var(--bot-primary); font-size: 12px; line-height: 20px; font-weight: 600; }
.bot-share-links { margin-top: 14px; padding-top: 14px; border-top: 1px solid var(--bot-outline); }
.bot-share-links p { margin: 0 0 8px; color: var(--bot-muted); font-size: 12px; line-height: 20px; }
.bot-share-links > div { display: flex; flex-wrap: wrap; gap: 8px; }
.bot-list-end { display: flex; justify-content: center; margin-top: 16px; }
.bot-all-loaded { margin: 18px 0 0; color: var(--bot-muted); text-align: center; font-size: 11px; line-height: 18px; }
.bot-raw-reply { margin-top: 16px; border-radius: 14px; background: color-mix(in srgb, var(--bot-card) 65%, transparent); color: var(--bot-muted); font-size: 12px; }
.bot-raw-reply summary { display: flex; align-items: center; gap: 8px; min-height: 48px; padding: 12px 14px; border-radius: inherit; list-style: none; cursor: pointer; }
.bot-raw-reply summary::-webkit-details-marker { display: none; }
.bot-raw-reply summary svg { width: 14px; height: 14px; flex-shrink: 0; }
.bot-raw-reply[open] summary svg { transform: rotate(90deg); }
.bot-raw-reply p { margin: 0; padding: 0 14px 14px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 22px; }
.bot-empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 32px 16px; color: var(--bot-muted); text-align: center; }
.bot-empty svg { width: 36px; height: 36px; margin-bottom: 8px; }
.bot-empty p { margin: 0; font-size: 14px; line-height: 22px; }
.bot-empty span { font-size: 12px; }
.bot-setup { padding: 24px; border-radius: 20px; background: var(--bot-card); }
.bot-setup h3 { margin: 0; font-size: 15px; font-weight: 600; }
.bot-setup p { margin: 10px 0 16px; color: var(--bot-muted); font-size: 13px; line-height: 22px; }
.bot-dialog button { cursor: pointer; }
.bot-dialog button:disabled { cursor: default; opacity: .45; }
.bot-dialog input:disabled { opacity: .6; }
.bot-dialog button:focus-visible, .bot-dialog a:focus-visible, .bot-dialog summary:focus-visible { outline: 2px solid var(--bot-primary); outline-offset: 3px; }
.bot-spinning { animation: bot-spin 1s linear infinite; }
@keyframes bot-spin { to { transform: rotate(360deg); } }
@media (hover: hover) {
  .bot-icon-button:hover, .bot-tool-button:hover:not(:disabled), .bot-text-button:hover:not(:disabled) { background: color-mix(in srgb, var(--bot-primary) 8%, transparent); }
}
@media (max-width: 799px) {
  .bot-resource-main { grid-template-columns: minmax(0, 1fr); gap: 16px; }
}
@media (max-width: 639px) {
  .bot-dialog { --bot-max-height: calc(min(100dvh, var(--bot-visible-height, 100dvh)) - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 24px); inset-block: calc(var(--bot-visible-top, 0px) + env(safe-area-inset-top)) calc(var(--bot-visible-bottom, 0px) + env(safe-area-inset-bottom)); width: calc(100% - 24px); border-radius: 24px; }
  .bot-header { gap: 10px; padding: 16px 16px 12px; }
  .bot-heading h2 { font-size: 18px; line-height: 26px; }
  .bot-heading p { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
  .bot-toolbar { flex-wrap: wrap; gap: 4px; padding: 0 16px 12px; }
  .bot-search-field { flex-basis: 100%; }
  .bot-search-field input { font-size: 16px; }
  .bot-tools { justify-content: flex-end; width: 100%; }
  .bot-body { padding: 0 16px 16px; scrollbar-gutter: auto; }
  .bot-resource { padding: 16px; border-radius: 18px; }
  .bot-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 359px) {
  .bot-actions { grid-template-columns: minmax(0, 1fr); }
}
@media (prefers-reduced-motion: reduce) {
  .bot-spinning { animation: none; }
}
</style>
