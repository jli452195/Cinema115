<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { IconChevronDown, IconClock, IconHeart, IconHeartFilled, IconLoader2, IconRefresh, IconX } from '@tabler/icons-vue'
import { followRequest, type FollowState } from '../types/follows'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; libraryState?: FollowState['libraryState'] }>()
const emit = defineEmits<{ refreshLibrary: [] }>()
const state = ref<FollowState>()
const error = ref('')
const busy = ref(false)
const loading = ref(true)
const coolingDown = ref(false)
let cooldownTimer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let readEpoch = 0
const movieLibrary = computed(() => props.libraryState ?? state.value?.libraryState ?? 'unknown')
const available = computed(() => props.kind === 'movie' && movieLibrary.value === 'present')
const libraryKnown = computed(() => movieLibrary.value === 'present' || movieLibrary.value === 'absent')
const closed = computed(() => state.value?.request?.status === 'closed')
const movieLabel = computed(() => state.value?.subscribed ? closed.value ? '申请已关闭' : '已申请' : '申请入库')
const requestStatus = computed(() => {
  if (closed.value) return '申请已关闭'
  if (!libraryKnown.value) return '入库状态待核实'
  if (state.value?.request?.status === 'completed') return '等待入库复核'
  return state.value?.request?.status === 'processing' ? '正在处理' : '等待处理'
})
const requestDescription = computed(() => {
  if (closed.value) return '这条申请已停止资源搜索，可在下方查看处理说明。'
  if (!libraryKnown.value) return '暂时无法确认媒体库状态，已保留你的申请。'
  if (state.value?.request?.status === 'completed') return '先前已确认入库，当前未查到资源，等待后台重新核对。'
  return state.value?.request?.status === 'processing' ? '申请正在处理中，确认入库后会更新状态。' : '申请已保存，等待后续处理和入库确认。'
})
const panelID = useId(), movieTrigger = ref<HTMLElement>(), panel = ref<HTMLElement>(), panelClose = ref<HTMLButtonElement>()
const panelOpen = ref(false), refreshing = ref(false), position = ref<Record<string, string>>({})
let listening = false

function listen(active: boolean) {
  if (active === listening) return
  listening = active
  const method = active ? 'addEventListener' : 'removeEventListener'
  window[method]('resize', place)
  window[method]('scroll', onScroll, true)
  window.visualViewport?.[method]('resize', place)
  window.visualViewport?.[method]('scroll', place)
}
function closePanel(restoreFocus = false) {
  panel.value?.hidePopover()
  panelOpen.value = false
  listen(false)
  if (restoreFocus) movieTrigger.value?.focus({ preventScroll: true })
}
function place() {
  if (!panelOpen.value || !movieTrigger.value || !panel.value) return
  const anchor = movieTrigger.value.getBoundingClientRect(), viewport = window.visualViewport, margin = 12
  const leftEdge = (viewport?.offsetLeft || 0) + margin, topEdge = (viewport?.offsetTop || 0) + margin
  const rightEdge = (viewport?.offsetLeft || 0) + Math.min(document.documentElement.clientWidth, viewport?.width || innerWidth) - margin
  const bottomEdge = (viewport?.offsetTop || 0) + (viewport?.height || innerHeight) - margin
  const width = Math.max(0, Math.min(336, rightEdge - leftEdge)), maxHeight = Math.max(0, bottomEdge - topEdge)
  panel.value.style.width = `${width}px`
  panel.value.style.maxHeight = `${maxHeight}px`
  const height = panel.value.offsetHeight
  const top = anchor.bottom + height + 8 <= bottomEdge ? anchor.bottom + 8 : Math.max(topEdge, anchor.top - height - 8)
  position.value = { left: `${Math.max(leftEdge, Math.min(anchor.left, rightEdge - width))}px`, top: `${Math.min(top, bottomEdge - height)}px`, width: `${width}px`, maxHeight: `${maxHeight}px` }
}
function onScroll(event: Event) {
  if (event.target instanceof Node && panel.value?.contains(event.target)) return
  closePanel(!!panel.value?.contains(document.activeElement))
}
function onFocusOut(event: FocusEvent) {
  // Touch browsers can leave relatedTarget null for a tap inside the popover.
  // Native light-dismiss handles outside taps; only close on a known focus exit.
  if (event.relatedTarget instanceof Node && !panel.value?.contains(event.relatedTarget) && event.relatedTarget !== movieTrigger.value) closePanel()
}
function onToggle(event: Event) {
  panelOpen.value = (event as ToggleEvent).newState === 'open'
  listen(panelOpen.value)
}
async function refreshStatus() {
  const current = controller
  if (!current || current.signal.aborted || busy.value || refreshing.value) return
  const epoch = ++readEpoch
  refreshing.value = true
  try {
    const data = await followRequest(props.token, `/${props.kind}/${props.tmdbId}`, current.signal)
    if (!current.signal.aborted && epoch === readEpoch) { state.value = data; error.value = '' }
  } catch (e) {
    if (!current.signal.aborted && epoch === readEpoch) error.value = e instanceof Error ? e.message : '申请状态读取失败'
  } finally {
    if (!current.signal.aborted && epoch === readEpoch) { refreshing.value = false; await nextTick(); place() }
  }
}
async function togglePanel() {
  if (busy.value || coolingDown.value) return
  if (panelOpen.value) { closePanel(); return }
  panel.value?.showPopover()
  panelOpen.value = true
  await nextTick()
  place()
  panelClose.value?.focus({ preventScroll: true })
  void refreshStatus()
}
function retry() {
  emit('refreshLibrary')
  void load()
}
async function load() {
  closePanel()
  controller?.abort(); const current = new AbortController(); controller = current
  readEpoch++; refreshing.value = false
  clearTimeout(cooldownTimer); coolingDown.value = false
  state.value = undefined; error.value = ''; loading.value = true; busy.value = false
  try { const data = await followRequest(props.token, `/${props.kind}/${props.tmdbId}`, current.signal); if (!current.signal.aborted) state.value = data }
  catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '追更状态读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
async function change(cancel = false) {
  const current = controller
  if (!current || current.signal.aborted || busy.value || coolingDown.value || loading.value || !state.value || (!cancel && available.value)) return
  if (props.kind === 'movie' && (cancel ? !state.value.subscribed || available.value : state.value.subscribed || movieLibrary.value !== 'absent')) return
  readEpoch++; refreshing.value = false
  let restoreMovieFocus = false
  busy.value = true; error.value = ''
  // 提交时固定影视编号，切换路由不能把响应写入另一部影视。
  const path = `/${props.kind}/${props.tmdbId}`
  try {
    const data = await followRequest(props.token, `${path}${cancel ? '/cancel' : ''}`, current.signal, 'POST')
    if (current.signal.aborted) return
    state.value = cancel ? { ...state.value, subscribed: false, request: null } : data
    if (props.kind === 'movie') {
      if (cancel) { restoreMovieFocus = panelOpen.value; closePanel() }
      emit('refreshLibrary')
    }
  } catch (e) {
    if (!current.signal.aborted) {
      error.value = e instanceof Error ? e.message : '追更操作失败'
      // 超时可能发生在提交之后，读取服务器状态，避免误认为没有保存。
      try { const data = await followRequest(props.token, path, current.signal); if (!current.signal.aborted) state.value = data } catch { /* 保留原错误供用户重试。 */ }
    }
  } finally {
    if (!current.signal.aborted) {
      busy.value = false
      coolingDown.value = true
      cooldownTimer = setTimeout(() => { coolingDown.value = false }, 1000)
      if (restoreMovieFocus) { await nextTick(); movieTrigger.value?.focus({ preventScroll: true }) }
    }
  }
}
watch([available, () => state.value?.subscribed], async () => {
  if (!panelOpen.value || !available.value && state.value?.subscribed) return
  const restoreFocus = !!panel.value?.contains(document.activeElement)
  closePanel()
  await nextTick()
  if (restoreFocus) movieTrigger.value?.focus({ preventScroll: true })
})
watch([error, busy, state], async () => { await nextTick(); place() })
watch(() => [props.token, props.kind, props.tmdbId], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { closePanel(); controller?.abort(); clearTimeout(cooldownTimer) })
</script>

<template>
  <div class="space-y-2">
    <template v-if="kind === 'movie'">
      <div class="movie-follow-row" aria-live="polite" :aria-busy="loading || busy">
        <span v-if="available" ref="movieTrigger" tabindex="-1" class="movie-follow-status movie-follow-available" data-state="available"><IconHeartFilled aria-hidden="true" :size="16" />已入库</span>
        <span v-else-if="loading || !state || (!libraryKnown && !state.subscribed)" class="movie-follow-status movie-follow-muted" data-state="unverified">
          <IconLoader2 v-if="loading" aria-hidden="true" :size="18" class="animate-spin" />
          <IconClock v-else aria-hidden="true" :size="18" />
          {{ loading ? '读取中' : !state ? '状态读取失败' : '状态待核实' }}
        </span>
        <button v-else ref="movieTrigger" type="button" class="movie-follow-action" :data-state="state.subscribed ? 'requested' : 'idle'" :disabled="busy" :aria-disabled="coolingDown || undefined" :aria-label="state.subscribed ? `${movieLabel}，查看申请详情` : undefined" :aria-haspopup="state.subscribed ? 'dialog' : undefined" :aria-controls="state.subscribed ? panelID : undefined" :aria-expanded="state.subscribed ? panelOpen : undefined" @click="state.subscribed ? togglePanel() : change()">
          <IconLoader2 v-if="busy" aria-hidden="true" :size="18" class="animate-spin" />
          <IconHeartFilled v-else-if="state.subscribed" aria-hidden="true" :size="16" />
          <IconHeart v-else aria-hidden="true" :size="16" />
          <span>{{ busy ? state.subscribed ? '取消中' : '提交中' : movieLabel }}</span>
          <IconChevronDown v-if="state.subscribed && !busy" aria-hidden="true" :size="14" class="movie-follow-chevron" :class="{ 'is-open': panelOpen }" />
        </button>
        <span v-if="!loading && state?.subscribed && !libraryKnown && !available" class="movie-follow-hint">入库待核实</span>
        <button v-if="!loading && (!state || !libraryKnown) && !available" type="button" class="movie-follow-retry" @click="retry"><IconRefresh aria-hidden="true" :size="15" />重新核对</button>
      </div>
      <div :id="panelID" ref="panel" popover="auto" role="dialog" :aria-labelledby="`${panelID}-title`" class="movie-follow-panel floating-surface" :style="position" @toggle="onToggle" @keydown.esc.prevent.stop="closePanel(true)" @focusout="onFocusOut">
        <div class="movie-follow-heading">
          <h2 :id="`${panelID}-title`">电影申请</h2>
          <button ref="panelClose" type="button" class="movie-follow-close" aria-label="关闭申请详情" @click="closePanel(true)"><IconX aria-hidden="true" :size="18" /></button>
        </div>
        <p class="movie-follow-progress" role="status"><IconLoader2 v-if="refreshing" aria-hidden="true" :size="16" class="animate-spin" /><IconClock v-else aria-hidden="true" :size="16" />{{ requestStatus }}</p>
        <p class="movie-follow-description">{{ requestDescription }}</p>
        <div v-if="state?.request?.note" class="movie-follow-note"><p>处理说明</p><div>{{ state.request.note }}</div></div>
        <p v-if="error" role="alert" class="movie-follow-error">{{ error }}<button :disabled="refreshing || busy" class="movie-follow-retry" @click="refreshStatus">重试</button></p>
        <div class="movie-follow-footer"><p>取消仅移除你的订阅，不会删除已入库资源。</p><button type="button" class="movie-follow-cancel" :disabled="busy || coolingDown" @click="change(true)">{{ busy ? '取消中' : '取消申请' }}</button></div>
      </div>
    </template>
    <div v-else class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        :disabled="loading || busy || coolingDown || !state || (available && !state.subscribed)"
        :aria-label="state?.subscribed ? '取消追更' : undefined"
        class="follow-button group inline-flex items-center justify-center gap-2 px-5 py-2 text-sm font-medium rounded-full transition-colors duration-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 disabled:opacity-60 disabled:cursor-not-allowed"
        :data-state="state?.subscribed ? 'subscribed' : available ? 'available' : 'idle'"
        :class="[
          state?.subscribed
            ? 'bg-rose-50 text-rose-600 hover:bg-rose-100 hover:text-rose-700 dark:bg-rose-500/10 dark:text-rose-400 dark:hover:bg-rose-500/20'
            : available
              ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300'
              : 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700 shadow-sm'
        ]"
        @click="state?.subscribed ? change(true) : change()"
      >
        <svg v-if="loading || busy" aria-hidden="true" class="animate-spin h-4 w-4" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
        </svg>

        <svg v-else-if="!state?.subscribed && !available" aria-hidden="true" class="h-4 w-4 transition-transform duration-200 group-active:scale-75" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z" />
        </svg>

        <svg v-else aria-hidden="true" class="h-4 w-4 transition-transform duration-200 group-active:scale-75" :class="{ 'group-hover:hidden group-focus-visible:hidden': state?.subscribed }" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z" clip-rule="evenodd" />
        </svg>

        <svg v-if="state?.subscribed && !loading && !busy" aria-hidden="true" class="h-4 w-4 hidden group-hover:block group-focus-visible:block" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z" />
          <line x1="4" y1="4" x2="20" y2="20" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" />
        </svg>

        <span>
          <template v-if="loading">读取中</template>
          <template v-else-if="busy">处理中</template>
          <template v-else-if="state?.subscribed">
            <span class="group-hover:hidden group-focus-visible:hidden">已追更</span>
            <span class="hidden group-hover:inline group-focus-visible:inline">取消追更</span>
          </template>
          <template v-else>{{ available ? '已入库' : '追更' }}</template>
        </span>
      </button>
    </div>
    <p v-if="error && !panelOpen" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ error }}<button v-if="!state && kind !== 'movie'" :disabled="loading" class="ml-2 underline" @click="load">重试</button></p>
  </div>
</template>

<style scoped>
.movie-follow-row { display: flex; min-height: 36px; flex-wrap: wrap; align-items: center; gap: 4px 12px; }
.movie-follow-action, .movie-follow-status { display: inline-flex; min-height: 36px; align-items: center; justify-content: center; gap: 8px; padding: 8px 20px; border-radius: 999px; font-size: 14px; font-weight: 500; line-height: 20px; }
.movie-follow-action { position: relative; }
.movie-follow-action::before { content: ''; position: absolute; inset: -4px 0; border-radius: inherit; }
.movie-follow-action { color: var(--md-sys-color-on-primary-container, #1e40af); background: var(--md-sys-color-primary-container, #dbeafe); transition: background 160ms ease, color 160ms ease; }
.movie-follow-action:hover { background: var(--md-sys-color-primary, #1d4ed8); color: var(--md-sys-color-on-primary, #fff); }
.movie-follow-action[data-state='requested'], .movie-follow-action[data-state='requested']:hover { background: var(--md-sys-color-primary, #1d4ed8); color: var(--md-sys-color-on-primary, #fff); }
.movie-follow-action:disabled, .movie-follow-action[aria-disabled='true'], .movie-follow-cancel:disabled, .movie-follow-retry:disabled { cursor: default; opacity: .65; }
.movie-follow-action:focus-visible, .movie-follow-retry:focus-visible, .movie-follow-panel button:focus-visible { outline: 2px solid var(--md-sys-color-primary, #2563eb); outline-offset: 3px; }
.movie-follow-available { color: var(--md-sys-color-on-primary-container, #1e40af); background: var(--md-sys-color-primary-container, #dbeafe); }
.movie-follow-muted { color: var(--md-sys-color-on-surface-variant, #475569); background: var(--md-sys-color-surface-container, #f1f5f9); }
.movie-follow-hint, .movie-follow-description, .movie-follow-footer p { font-size: 12px; line-height: 1.7; color: var(--floating-surface-muted, var(--md-sys-color-on-surface-variant, #475569)); }
.movie-follow-retry { display: inline-flex; min-height: 44px; align-items: center; gap: 5px; color: var(--md-sys-color-primary, #2563eb); font-size: 12px; }
.movie-follow-chevron { transition: transform 160ms ease; }
.movie-follow-chevron.is-open { transform: rotate(180deg); }
.movie-follow-panel { position: fixed; inset: auto; width: 336px; max-width: calc(100vw - 24px); margin: 0; padding: 12px 16px 16px; border-radius: 20px; overflow: auto; color: var(--floating-surface-text); }
.movie-follow-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.movie-follow-heading h2 { font-size: 14px; font-weight: 600; }
.movie-follow-close { display: flex; width: 44px; height: 44px; flex-shrink: 0; align-items: center; justify-content: center; border-radius: 50%; color: var(--floating-surface-muted); }
.movie-follow-close:hover { background: rgb(100 116 139 / 10%); }
.movie-follow-progress { display: flex; align-items: center; gap: 7px; margin-top: 4px; font-size: 14px; font-weight: 500; }
.movie-follow-description { margin-top: 8px; }
.movie-follow-note { margin-top: 14px; padding: 12px; border-radius: 12px; background: rgb(100 116 139 / 7%); font-size: 13px; line-height: 1.7; }
.movie-follow-note p { margin-bottom: 4px; color: var(--floating-surface-muted); font-size: 11px; }
.movie-follow-note div { white-space: pre-wrap; overflow-wrap: anywhere; }
.movie-follow-footer { margin-top: 16px; }
.movie-follow-footer p { margin-bottom: 4px; }
.movie-follow-cancel { display: inline-flex; min-height: 44px; align-items: center; color: #be123c; font-size: 13px; }
:global(.dark) .movie-follow-cancel { color: #fda4af; }
.movie-follow-error { margin-top: 12px; color: #be123c; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
:global(.dark) .movie-follow-error { color: #fda4af; }
.movie-follow-panel:popover-open { animation: movie-follow-enter 140ms ease-out; }
@keyframes movie-follow-enter { from { opacity: 0; transform: translateY(3px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
  .transition-colors, .transition-transform, .movie-follow-action, .movie-follow-chevron { transition: none; }
  .movie-follow-panel:popover-open { animation: none; }
}
</style>
