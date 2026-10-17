<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { memberRequest, type MemberPlayback } from '../types/memberPlayback'
import { playbackTargetName, playbackTargetSource, type UserPlaybackSession as Session, type PlaybackSessionsSnapshot } from '../types/userPlayback'

const props = defineProps<{ token: string; members: MemberPlayback[] }>()
const emit = defineEmits<{ snapshot: [value: PlaybackSessionsSnapshot] }>()
const sessions = ref<Session[]>([]), readError = ref(''), releaseError = ref(''), notice = ref('')
const reading = ref(false), releasing = ref(false), loaded = ref(false), selected = ref<Session>()
const policy = ref<{ mode: string; silenceSeconds: number }>()
const dialog = ref<HTMLDialogElement>(), trigger = ref<HTMLButtonElement>(), closeButton = ref<HTMLButtonElement>(), refreshButton = ref<HTMLButtonElement>()
const opened = ref(false), titleID = useId(), dialogID = useId()
const userFilter = ref(''), now = ref(Date.now()), checkedAt = ref(0)
const stale = computed(() => !!readError.value || loaded.value && now.value - checkedAt.value > 30000)
const filteredSessions = computed(() => sessions.value.filter(session => !userFilter.value || session.userId === userFilter.value))
const filteredName = computed(() => props.members.find(member => member.userId === userFilter.value)?.embyName || 'Emby 用户')
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined, previousOverflow = '', backdropStarted = false
let clockTimer: ReturnType<typeof setInterval> | undefined, returnFocus: HTMLElement | null = null
let readController: AbortController | undefined, readVersion = 0
let releaseTrigger: HTMLElement | null = null
let clockOffset = 0
function syncClock(serverTime: unknown) {
 if (typeof serverTime === 'number' && Number.isFinite(serverTime) && serverTime > 0) clockOffset = serverTime - Date.now()
 now.value = Date.now() + clockOffset
 checkedAt.value = now.value
}
const busy = computed(() => reading.value || releasing.value)
const countLabel = computed(() => loaded.value ? String(sessions.value.length) : readError.value ? '待核实' : '读取中')
watch([sessions, loaded, stale, now], () => emit('snapshot', { sessions: sessions.value, loaded: loaded.value, stale: stale.value, now: now.value }), { immediate: true })
const policyLabel = computed(() => policy.value?.mode === 'heartbeat_takeover' ? `单设备心跳熔断 · ${policy.value.silenceSeconds} 秒` : policy.value?.mode === 'parallel' ? '多设备并行' : '设备策略待核实')
const policyDescription = computed(() => {
 if (policy.value?.mode === 'heartbeat_takeover') return `原设备静默 ${policy.value.silenceSeconds} 秒后，其他设备可尝试接替；成功后转交原名额，旧播放不再占容量。缺少可信设备信息时需通过反代重新登录。`
 if (policy.value?.mode === 'parallel') return '同一用户可在多个设备播放，各设备独立计数，停止或换号后可能保留最多两分钟残余占用。'
 return '设备策略尚未读取，不能据此判断是否允许多设备播放。'
})
function sessionName(session: Session) { return props.members.find(member => member.userId === session.userId)?.embyName || 'Emby 用户' }
function formatTime(value: number) {
 const date = new Date(value)
 return Number.isNaN(date.getTime()) ? '时间待核实' : date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
function focusList() { void nextTick(() => refreshButton.value?.focus({ preventScroll: true })) }
function applySessions(next: Session[]) {
 const active = document.activeElement
 const focusedKey = active instanceof HTMLElement ? active.closest<HTMLElement>('[data-session-key]')?.dataset.sessionKey : undefined
 // Retain the confirmation snapshot. A new generation requires a new choice.
 if (selected.value && !next.some(session => session.key === selected.value?.key && session.generation === selected.value.generation)) {
  selected.value = undefined; releaseError.value = ''; notice.value = '所选会话已更新或结束，请重新选择。'
  if (active instanceof HTMLElement && active.closest('.playback-release-confirm')) focusList()
 }
 sessions.value = next
 if (focusedKey && !next.some(session => session.key === focusedKey)) focusList()
}
async function load() {
 if (busy.value || controller.signal.aborted) return
 const version = ++readVersion, request = new AbortController()
 readController = request; reading.value = true
 try {
  const data = await memberRequest(props.token, AbortSignal.any([controller.signal, request.signal, AbortSignal.timeout(15000)]), '/playback-sessions')
  if (controller.signal.aborted || version !== readVersion) return
  syncClock(data.serverTime)
  applySessions((data.sessions || []).filter((session: Session) => session.expiresAt > now.value)); policy.value = data.devicePolicy; readError.value = ''; loaded.value = true
 } catch (e) {
  if (!controller.signal.aborted && !request.signal.aborted && version === readVersion) {
   const active = document.activeElement
   readError.value = e instanceof Error ? e.message : '读取播放会话失败'
   if (active instanceof HTMLElement && active.matches('.playback-release-button, .playback-button-danger')) focusList()
  }
 }
 finally { if (version === readVersion) { reading.value = false; readController = undefined } }
}
async function poll() {
 if (!document.hidden) await load()
 if (!controller.signal.aborted) timer = setTimeout(poll, 10000)
}
async function openForUser(userId = '', origin?: HTMLElement) {
 if (opened.value) return
 userFilter.value = userId
 returnFocus = origin || trigger.value || null
 previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; opened.value = true
 await nextTick()
 if (!controller.signal.aborted) { dialog.value?.showModal(); closeButton.value?.focus({ preventScroll: true }) }
 if (stale.value || !loaded.value) void load()
}
function openDialog() { void openForUser() }
function showAll() { if (!releasing.value) { userFilter.value = ''; cancelRelease(); focusList() } }
defineExpose({ openForUser })
function closeDialog() {
 if (releasing.value) return
 dialog.value?.close(); opened.value = false; selected.value = undefined; releaseError.value = ''; notice.value = ''
 document.body.style.overflow = previousOverflow
 if (returnFocus?.isConnected && returnFocus.getClientRects().length) returnFocus.focus({ preventScroll: true })
 else trigger.value?.focus({ preventScroll: true })
 returnFocus = null
}
function outsideDialog(event: MouseEvent) {
 if (event.target !== dialog.value || !dialog.value) return false
 const bounds = dialog.value.getBoundingClientRect()
 return event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom
}
function backdropClick(event: MouseEvent) { if (backdropStarted && outsideDialog(event)) closeDialog(); backdropStarted = false }
async function selectSession(session: Session, event: MouseEvent) {
 if (releasing.value || stale.value) return
 releaseTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
 selected.value = { ...session }; releaseError.value = ''; notice.value = ''
 await nextTick()
 const cancel = dialog.value?.querySelector<HTMLButtonElement>('.playback-release-confirm .playback-button-secondary')
 if (cancel?.isConnected) cancel.focus({ preventScroll: true })
 else if (opened.value) focusList()
}
function cancelRelease() {
 if (releasing.value) return
 selected.value = undefined; releaseError.value = ''
 void nextTick(() => releaseTrigger?.isConnected ? releaseTrigger.focus({ preventScroll: true }) : refreshButton.value?.focus({ preventScroll: true }))
}
function requestClose() { if (selected.value && !releasing.value) cancelRelease(); else closeDialog() }
async function release() {
 const session = selected.value
 if (!session || releasing.value || stale.value || !sessions.value.some(current => current.key === session.key && current.generation === session.generation && current.expiresAt > Date.now() + clockOffset)) return
 // A pending read cannot overwrite the release result or disable focused controls.
 readVersion++; readController?.abort(); readController = undefined; reading.value = false
 releasing.value = true; releaseError.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, controller.signal, `/playback-sessions/${encodeURIComponent(session.key)}/release`, { generation: session.generation })
  if (controller.signal.aborted) return
  syncClock(data.serverTime)
  selected.value = undefined; applySessions((data.sessions || []).filter((session: Session) => session.expiresAt > now.value)); loaded.value = true
  notice.value = '已释放此会话的占用。已签发直链可能仍有效。'
  focusList()
 } catch (e) {
  if (!controller.signal.aborted) {
   releaseError.value = e instanceof Error ? e.message : '释放结果未确认，请重新读取后核实。'
   checkedAt.value = 0
   focusList()
  }
 }
 finally { releasing.value = false }
}
function tick() {
 now.value = Date.now() + clockOffset
 if (sessions.value.some(session => session.expiresAt <= now.value)) applySessions(sessions.value.filter(session => session.expiresAt > now.value))
}
function onVisible() { tick(); if (!document.hidden) void load() }
onMounted(() => { void poll(); clockTimer = setInterval(tick, 5000); document.addEventListener('visibilitychange', onVisible) })
onBeforeUnmount(() => { controller.abort(); readController?.abort(); clearTimeout(timer); clearInterval(clockTimer); document.removeEventListener('visibilitychange', onVisible); if (opened.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow } })
</script>

<template>
 <section class="playback-summary playback-ui" aria-label="播放会话摘要">
  <svg class="playback-summary-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3" y="4" width="18" height="13" rx="3" /><path d="M8 21h8M12 17v4m-2-13 5 3-5 3z" stroke-linejoin="round" /></svg>
  <div class="playback-summary-copy"><strong>播放会话 <span class="playback-count">{{ countLabel }}</span><span v-if="stale && loaded" class="playback-stale">待核实</span></strong><span class="playback-policy-summary" :title="policyLabel">{{ policyLabel }}</span></div>
  <button ref="trigger" type="button" class="playback-button playback-button-text" aria-haspopup="dialog" :aria-expanded="opened" :aria-controls="opened ? dialogID : undefined" @click="openDialog">查看会话</button>
 </section>
 <Teleport to="body">
  <dialog v-if="opened" :id="dialogID" ref="dialog" class="playback-dialog playback-ui" :aria-labelledby="titleID" @cancel.prevent="requestClose" @pointerdown="backdropStarted = outsideDialog($event)" @click="backdropClick">
   <div class="playback-dialog-panel">
    <header class="playback-dialog-heading"><div><h2 :id="titleID">播放会话 <span class="playback-count">{{ userFilter && loaded ? filteredSessions.length : countLabel }}</span></h2><p>{{ policyLabel }}</p></div><button ref="closeButton" type="button" class="playback-button playback-icon-button" aria-label="关闭播放会话" :disabled="releasing" @click="closeDialog"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m6 6 12 12M6 18 18 6" /></svg></button></header>
    <div class="playback-dialog-body">
     <div v-if="userFilter" class="playback-user-filter"><span :title="filteredName">{{ filteredName }}</span><button type="button" class="playback-button playback-button-text" :disabled="releasing" @click="showAll">查看全部会话</button></div>
     <details class="playback-explanation"><summary>策略与会话说明</summary><p>{{ policyDescription }}</p><p>这里列出播放会话，同设备切集与预加载可共享一个 UID 名额。池的占用、活跃、准备与残余是逻辑容量统计，不是用户数或真实视频连接数。“已签发直链”不代表当前正在播放。</p><p>取链配置记录本会话最近一次成功签发时实际使用的配置，名称保留签发时的记录。准备中不会提前显示成功配置；停止后的池残余不列为当前会话。</p></details>
     <p v-if="readError" role="alert" class="playback-message is-error">{{ readError }}{{ loaded ? '；以下保留上次读取结果，释放前请重新读取。' : '；当前会话数尚未确认。' }}</p>
     <p v-else-if="stale" role="status" class="playback-message">取链状态待核实，以下为上次读取结果，请重新读取。</p>
     <p v-if="!loaded" role="status" class="playback-empty">{{ reading ? '正在读取播放会话…' : '会话状态待核实，请重新读取。' }}</p>
     <p v-else-if="!filteredSessions.length && !stale" class="playback-empty">{{ userFilter ? '此用户暂无播放会话' : '暂无播放会话' }}</p>
     <div v-if="filteredSessions.length" class="playback-session-list">
      <article v-for="session in filteredSessions" :key="session.key" :data-session-key="session.key" class="playback-session-row">
       <div class="playback-session-content"><div class="playback-session-title"><strong :title="sessionName(session)">{{ sessionName(session) }}</strong><span class="playback-session-badge" :class="session.issued ? 'is-issued' : 'is-preparing'">{{ session.issued ? '已签发直链' : '准备中' }}</span></div><p class="playback-item">条目 {{ session.item }}</p><div v-if="session.issued && session.target?.accountId" class="playback-target"><p><span>{{ stale ? '上次读取的取链配置' : '本次取链' }}</span><strong>{{ playbackTargetName(session.target) }}</strong></p><p class="playback-target-meta">{{ playbackTargetSource(session.target.source) }}<span>配置编号 {{ session.target.accountId }}</span></p><p v-if="session.target.issuedAt" class="playback-target-meta">取链时间 {{ formatTime(session.target.issuedAt) }}</p></div><p v-else class="playback-item">{{ session.issued ? '本会话尚无成功取链配置记录' : '准备取链，成功后显示实际配置' }}</p><p class="playback-session-times"><span>最近活动 {{ formatTime(session.updatedAt) }}</span><span>预计到期 {{ formatTime(session.expiresAt) }}</span></p></div>
       <button type="button" class="playback-button playback-button-text playback-release-button" :disabled="releasing || stale" @click="selected?.key === session.key ? cancelRelease() : selectSession(session, $event)">{{ selected?.key === session.key ? '取消释放' : '释放占用' }}</button>
       <div v-if="selected?.key === session.key" class="playback-release-confirm"><p>释放后会拒绝旧会话的新取链，但不能立即中断已签发的 CDN 直链。确认释放 {{ sessionName(selected) }} 的条目 {{ selected.item }}？</p><p v-if="releaseError" role="alert" class="playback-release-error">{{ releaseError }}</p><div><button type="button" class="playback-button playback-button-secondary" :disabled="releasing" @click="cancelRelease">取消</button><button type="button" class="playback-button playback-button-danger" :disabled="releasing || stale" @click="release">{{ releasing ? '正在释放…' : '确认释放' }}</button></div></div>
      </article>
     </div>
     <p v-if="notice" role="status" class="playback-message">{{ notice }}</p>
    </div>
    <footer class="playback-dialog-footer"><span role="status">{{ reading ? '正在更新会话' : '每 10 秒自动更新' }}</span><button ref="refreshButton" type="button" class="playback-button playback-button-secondary" :disabled="releasing" :aria-disabled="reading || releasing" @click="load">重新读取</button></footer>
   </div>
  </dialog>
 </Teleport>
</template>

<style scoped>
.playback-ui { --ps-bg: #fff; --ps-soft: #f5f7fb; --ps-text: #192b47; --ps-muted: #61718a; --ps-outline: #e5eaf2; --ps-blue: #245bc7; --ps-blue-soft: #eaf1ff; --ps-error: #aa3446; --ps-error-bg: #fff0f2; color: var(--ps-text); }
.dark .playback-ui { --ps-bg: rgb(var(--color-slate-900)); --ps-soft: rgb(var(--color-slate-800)); --ps-text: rgb(var(--color-slate-100)); --ps-muted: rgb(var(--color-slate-400)); --ps-outline: rgb(var(--color-slate-700)); --ps-blue: #8daee8; --ps-blue-soft: color-mix(in srgb, var(--ps-soft) 90%, var(--ps-blue)); --ps-error: #e8788a; --ps-error-bg: #301a20; color-scheme: dark; }
.playback-summary { display: flex; align-items: center; gap: 10px; min-height: 48px; margin-top: 12px; padding: 4px 12px; border-radius: 12px; background: var(--ps-soft); }
.playback-summary-icon { width: 18px; height: 18px; flex-shrink: 0; color: var(--ps-blue); }
.playback-summary-copy { display: flex; flex: 1; min-width: 0; align-items: baseline; gap: 6px 16px; }
.playback-summary-copy strong { flex-shrink: 0; font-size: 13px; font-weight: 600; }
.playback-count { margin-left: 6px; color: var(--ps-blue); font-variant-numeric: tabular-nums; }
.playback-stale { margin-left: 8px; font-size: 11px; color: var(--ps-error); }
.playback-policy-summary { min-width: 0; overflow: hidden; color: var(--ps-muted); font-size: 12px; white-space: nowrap; text-overflow: ellipsis; }
.playback-button { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; min-height: 40px; padding: 0 12px; border-radius: 9px; font-size: 12px; font-weight: 600; cursor: pointer; transition: background 150ms ease, transform 150ms ease; }
.playback-button-text { background: transparent; color: var(--ps-blue); }.playback-button-secondary { background: var(--ps-soft); color: var(--ps-text); }.playback-button-danger { background: var(--ps-error-bg); color: var(--ps-error); }
.playback-button:disabled { cursor: default; opacity: .45; }.playback-button:not(:disabled):active { transform: scale(.97); }
.playback-ui button:focus-visible, .playback-ui summary:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }
.playback-dialog { width: min(680px, calc(100% - 24px)); max-width: none; height: min(640px, calc(100dvh - 32px - env(safe-area-inset-top) - env(safe-area-inset-bottom))); max-height: none; margin: auto; padding: 0; overflow: hidden; border: 1px solid var(--ps-outline); border-radius: 22px; background: var(--ps-bg); box-shadow: 0 24px 90px #02061735; }
.playback-dialog::backdrop { background: #0b15275c; backdrop-filter: blur(3px); }.playback-dialog[open] { animation: playback-dialog-enter 200ms cubic-bezier(.2,0,0,1); }
.playback-dialog-panel { display: flex; flex-direction: column; height: 100%; }.playback-dialog-heading { display: flex; align-items: flex-start; justify-content: space-between; flex-shrink: 0; gap: 16px; padding: 20px 18px 16px 24px; }.playback-dialog-heading > div { min-width: 0; }.playback-dialog-heading h2 { font-size: 18px; font-weight: 650; }.playback-dialog-heading p { margin-top: 5px; color: var(--ps-muted); font-size: 12px; }
.playback-icon-button { width: 40px; padding: 0; color: var(--ps-muted); }.playback-icon-button svg { width: 20px; height: 20px; }
.playback-dialog-body { min-height: 0; flex: 1; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }.playback-explanation { padding: 10px 12px; border-radius: 12px; color: var(--ps-muted); background: var(--ps-soft); font-size: 12px; line-height: 1.7; }.playback-explanation summary { width: fit-content; cursor: pointer; }.playback-explanation p { margin-top: 8px; }
.playback-user-filter { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 10px; font-size: 13px; }.playback-user-filter > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.playback-target { margin: 9px 0; font-size: 12px; overflow-wrap: anywhere; }.playback-target > p:first-child { display: flex; flex-wrap: wrap; gap: 2px 8px; color: var(--ps-muted); }.playback-target strong { color: var(--ps-blue); font-weight: 600; }.playback-target-meta { display: flex; flex-wrap: wrap; gap: 2px 10px; margin-top: 3px; color: var(--ps-muted); font-size: 11px; }
.playback-message { margin-top: 12px; padding: 10px 12px; border-radius: 10px; background: var(--ps-soft); color: var(--ps-muted); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.playback-message.is-error { color: var(--ps-error); background: var(--ps-error-bg); }.playback-empty { padding: 60px 12px; color: var(--ps-muted); font-size: 13px; text-align: center; }
.playback-session-list { margin-top: 12px; }.playback-session-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px 12px; padding: 14px 0; border-bottom: 1px solid var(--ps-outline); }.playback-session-content { min-width: 0; }.playback-session-title { display: flex; align-items: center; gap: 8px; font-size: 13px; }.playback-session-title strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.playback-item { margin-top: 4px; color: var(--ps-muted); font-size: 12px; overflow-wrap: anywhere; }.playback-session-times { display: flex; flex-wrap: wrap; gap: 2px 12px; margin-top: 4px; color: var(--ps-muted); font-size: 11px; line-height: 1.6; font-variant-numeric: tabular-nums; }.playback-session-badge { flex-shrink: 0; border-radius: 6px; padding: 3px 6px; font-size: 11px; }.playback-session-badge.is-issued { color: var(--ps-blue); background: var(--ps-blue-soft); }.playback-session-badge.is-preparing { color: #94601b; background: #fff3d8; }.dark .playback-session-badge.is-preparing { color: #f0ce8d; background: #45331e; }.playback-release-button { align-self: center; }
.playback-release-confirm { grid-column: 1 / -1; padding: 12px; border-radius: 12px; color: var(--ps-text); background: var(--ps-soft); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.playback-release-confirm > div { display: flex; justify-content: flex-end; gap: 8px; margin-top: 10px; }.playback-release-error { margin-top: 8px; color: var(--ps-error); }
.playback-dialog-footer { display: flex; align-items: center; justify-content: space-between; flex-shrink: 0; gap: 12px; padding: 14px 24px max(14px, env(safe-area-inset-bottom)); border-top: 1px solid var(--ps-outline); color: var(--ps-muted); font-size: 11px; }
@media (hover: hover) { .playback-button-text:not(:disabled):hover, .playback-button-secondary:not(:disabled):hover, .playback-icon-button:not(:disabled):hover { background: var(--ps-blue-soft); }.playback-button-danger:not(:disabled):hover { background: color-mix(in srgb, var(--ps-error) 12%, var(--ps-error-bg)); } }
@media (max-width: 639px) { .playback-summary { min-height: 64px; padding: 6px 10px; gap: 8px; }.playback-summary-copy { flex-direction: column; gap: 2px; align-items: flex-start; }.playback-summary-copy strong { font-size: 12px; }.playback-policy-summary { width: 100%; font-size: 11px; }.playback-button { min-height: 44px; }.playback-summary .playback-button { padding: 0 6px; }.playback-dialog { height: min(640px, calc(100dvh - 24px - env(safe-area-inset-top) - env(safe-area-inset-bottom))); border-radius: 20px; }.playback-dialog-heading { padding: 18px 12px 14px 18px; }.playback-dialog-body { padding: 0 18px 18px; scrollbar-gutter: auto; }.playback-dialog-footer { padding: 12px 18px max(12px, env(safe-area-inset-bottom)); }.playback-session-row { grid-template-columns: minmax(0, 1fr); gap: 4px; }.playback-release-button { justify-self: end; grid-row: 2; margin-top: -2px; }.playback-release-confirm { grid-row: 3; }.playback-session-times { display: grid; gap: 2px; } }
@keyframes playback-dialog-enter { from { opacity: 0; transform: translateY(10px) scale(.98); } to { opacity: 1; transform: translateY(0) scale(1); } }
@media (prefers-reduced-motion: reduce) { .playback-button { transition: none; }.playback-button:not(:disabled):active { transform: none; }.playback-dialog[open] { animation: none; } }
@media (max-height: 480px) { .playback-dialog-heading { padding-block: 10px; }.playback-dialog-body { padding-bottom: 10px; }.playback-dialog-footer { padding-block: 8px; } }
</style>
