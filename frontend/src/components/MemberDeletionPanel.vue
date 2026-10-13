<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { memberRequest, type MemberEmbyState } from '../types/memberPlayback'

const props = defineProps<{ token: string; disabled?: boolean }>()
const emit = defineEmits<{ changed: []; busy: [value: boolean] }>()
interface Target { key: string; userId: string; embyId: string; name: string; ownerId?: string; completed: boolean; remoteDeleted: boolean }
interface Job { id: string; name: string; fingerprint: string; targets: Target[]; stage: string; message: string; createdAt: number }
interface Preview { key: string; name: string; fingerprint: string; targets: Target[]; children: number; personalDrives?: number; sessions?: number; embyStates?: Record<string, MemberEmbyState> }
const pending = ref<Target[]>([]), jobs = ref<Job[]>([]), preview = ref<Preview>()
const confirmation = ref(''), cascade = ref(false), opened = ref(false), mode = ref<'list' | 'preview'>('list')
const action = ref<'preview' | 'submit' | ''>(''), reading = ref(false), loaded = ref(false), uncertain = ref(false)
const listError = ref(''), error = ref(''), notice = ref(''), resuming = ref(false)
const dialog = ref<HTMLDialogElement>(), trigger = ref<HTMLButtonElement>(), closeButton = ref<HTMLButtonElement>(), refreshButton = ref<HTMLButtonElement>()
const titleID = useId(), dialogID = useId(), formID = useId()
const lifecycle = new AbortController()
let listController: AbortController | undefined, previewController: AbortController | undefined, listVersion = 0, previewVersion = 0
let returnFocus: HTMLElement | null = null, previousOverflow = '', backdropStarted = false
const submitting = computed(() => action.value === 'submit')
const sortedJobs = computed(() => [...jobs.value].sort((a, b) => b.createdAt - a.createdAt || b.id.localeCompare(a.id)))
const unfinished = computed(() => sortedJobs.value.filter(job => job.stage !== 'completed'))
const completed = computed(() => sortedJobs.value.filter(job => job.stage === 'completed'))
const attentionCount = computed(() => pending.value.length + unfinished.value.length)
const cleanupOnly = computed(() => !!preview.value?.targets.length && preview.value.targets.every(t => t.remoteDeleted || preview.value?.embyStates?.[t.key] === 'deleted'))
const title = computed(() => mode.value === 'list' ? '账号清理' : resuming.value ? '继续原清理任务' : cleanupOnly.value ? '清理关联预览' : '删除影响预览')
const canSubmit = computed(() => !!preview.value && !action.value && !props.disabled && !uncertain.value && confirmation.value === preview.value.name && (!preview.value.children || cascade.value))
const taskActionsDisabled = computed(() => !!action.value || props.disabled || !!listError.value || reading.value)
function targetStatus(t: Target) {
 if (t.completed) return '已清理'
 if (t.remoteDeleted || preview.value?.embyStates?.[t.key] === 'deleted') return 'Emby 已删除，将清理本地关联'
 const state = preview.value?.embyStates?.[t.key]
 if (state === 'active' || state === 'disabled') return 'Emby 账号仍存在，将永久删除账号及观看记录'
 return 'Emby 状态待核实；若账号存在，将永久删除账号及观看记录'
}
function formatTime(seconds: number) {
 if (!seconds) return '时间未记录'
 return new Date(seconds * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
function setAction(value: typeof action.value) { action.value = value; emit('busy', !!value) }
function focusClose() { void nextTick(() => closeButton.value?.focus({ preventScroll: true })) }
function invalidatePreview() { previewVersion++; previewController?.abort(); previewController = undefined; if (action.value === 'preview') setAction('') }
function invalidateList() { listVersion++; listController?.abort(); listController = undefined; reading.value = false }
async function read() {
 if (reading.value || submitting.value || lifecycle.signal.aborted) return
 const version = ++listVersion, request = new AbortController()
 listController = request; reading.value = true
 try {
  const data = await memberRequest(props.token, AbortSignal.any([lifecycle.signal, request.signal]), '/deletions')
  if (version !== listVersion || lifecycle.signal.aborted) return
  const active = document.activeElement
  const focusedKey = active instanceof HTMLElement ? active.closest<HTMLElement>('[data-cleanup-key]')?.dataset.cleanupKey : undefined
  pending.value = data.pending || []; jobs.value = data.jobs || []; listError.value = ''; loaded.value = true
  if (focusedKey) void nextTick(() => {
   if (opened.value && mode.value === 'list' && active instanceof HTMLElement && (!active.isConnected || (active instanceof HTMLButtonElement && active.disabled))) refreshButton.value?.focus({ preventScroll: true })
  })
 } catch (e) {
  if (version === listVersion && !request.signal.aborted && !lifecycle.signal.aborted) {
   listError.value = e instanceof Error ? e.message : '读取清理任务失败'
   if (document.activeElement instanceof HTMLElement && document.activeElement.closest('[data-cleanup-key]')) void nextTick(() => refreshButton.value?.focus({ preventScroll: true }))
  }
 } finally { if (version === listVersion) { reading.value = false; listController = undefined } }
}
async function openDialog() {
 if (!opened.value) {
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; opened.value = true
 }
 await nextTick()
 if (!lifecycle.signal.aborted && opened.value) { if (!dialog.value?.open) dialog.value?.showModal(); closeButton.value?.focus({ preventScroll: true }) }
}
function closeDialog() {
 if (submitting.value) return
 invalidatePreview(); dialog.value?.close(); opened.value = false; preview.value = undefined; confirmation.value = ''; cascade.value = false
 document.body.style.overflow = previousOverflow
 void nextTick(() => (returnFocus?.isConnected ? returnFocus : trigger.value)?.focus({ preventScroll: true }))
}
async function openTasks() {
 mode.value = 'list'; preview.value = undefined; error.value = ''; notice.value = ''; uncertain.value = false
 await openDialog(); void read()
}
function backToTasks() {
 if (submitting.value) return
 invalidatePreview(); mode.value = 'list'; preview.value = undefined; confirmation.value = ''; cascade.value = false; error.value = ''; uncertain.value = false
 focusClose(); void read()
}
async function inspect(key: string) {
 if (action.value || props.disabled || !key) return
 invalidatePreview(); mode.value = 'preview'; preview.value = undefined; confirmation.value = ''; cascade.value = false; error.value = ''; notice.value = ''; uncertain.value = false; resuming.value = false
 const version = previewVersion, request = new AbortController()
 previewController = request; setAction('preview'); await openDialog()
 try {
  const data = await memberRequest(props.token, AbortSignal.any([lifecycle.signal, request.signal]), '/deletions/preview', { key })
  if (version === previewVersion && opened.value && !lifecycle.signal.aborted) preview.value = data
 } catch (e) { if (version === previewVersion && !request.signal.aborted && !lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '无法核验删除目标' }
 finally { if (version === previewVersion) { previewController = undefined; setAction('') } }
}
function resume(job: Job) {
 if (taskActionsDisabled.value || job.stage === 'completed') return
 invalidatePreview(); mode.value = 'preview'; resuming.value = true; uncertain.value = false
 preview.value = { key: job.id, name: job.name, fingerprint: job.fingerprint, targets: job.targets.map(t => ({ ...t })), children: Math.max(0, job.targets.length - 1) }
 confirmation.value = ''; cascade.value = false; error.value = ''; notice.value = '继续原任务，已完成的步骤不会重复执行。'
 focusClose()
}
async function remove() {
 const target = preview.value
 if (!target || !canSubmit.value) return
 invalidateList(); setAction('submit'); error.value = ''; notice.value = ''
 let changed = false
 try {
  const data = await memberRequest(props.token, lifecycle.signal, '/deletions', { key: target.key, fingerprint: target.fingerprint, confirmName: confirmation.value, includeChildren: cascade.value })
  if (lifecycle.signal.aborted) return
  changed = true; notice.value = data.job.message; mode.value = 'list'; preview.value = undefined; confirmation.value = ''; cascade.value = false
  // Keep the returned job visible even if the subsequent directory read fails.
  jobs.value = [...jobs.value.filter(job => job.id !== data.job.id), data.job]
  pending.value = pending.value.filter(t => !data.job.targets.some((item: Target) => item.key === t.key))
 } catch (e) {
  if (!lifecycle.signal.aborted) { changed = true; uncertain.value = true; error.value = `${e instanceof Error ? e.message : '删除结果未确认'}；请返回任务列表核实后再继续。` }
 } finally {
  setAction('')
  if (!lifecycle.signal.aborted) { focusClose(); if (changed) { void read(); emit('changed') } }
 }
}
function outsideDialog(event: MouseEvent) {
 if (event.target !== dialog.value || !dialog.value) return false
 const rect = dialog.value.getBoundingClientRect()
 return event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom
}
function backdropClick(event: MouseEvent) { if (backdropStarted && outsideDialog(event)) closeDialog(); backdropStarted = false }
defineExpose({ inspect })
onMounted(() => { void read() })
onBeforeUnmount(() => { lifecycle.abort(); previewController?.abort(); listController?.abort(); if (opened.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }; emit('busy', false) })
</script>

<template>
 <button ref="trigger" type="button" class="cleanup-trigger cleanup-ui" :class="{ 'has-attention': loaded && attentionCount }" aria-haspopup="dialog" :aria-expanded="opened" :aria-controls="opened ? dialogID : undefined" @click="openTasks">
  <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M14 5H7a3 3 0 0 0-3 3v11a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-5M8 3h6v4H8zM8 11h3M8 15h5m3-9 2 2 4-4" stroke-linecap="round" stroke-linejoin="round" /></svg>
  <span>账号清理</span><span v-if="listError" class="cleanup-badge is-warning">待核实</span><span v-else-if="loaded && attentionCount" class="cleanup-badge is-warning" :aria-label="`${attentionCount} 项待处理`">{{ attentionCount }}</span>
 </button>
 <Teleport to="body">
  <dialog v-if="opened" :id="dialogID" ref="dialog" class="deletion-dialog cleanup-ui" :aria-labelledby="titleID" @cancel.prevent="closeDialog" @pointerdown="backdropStarted = outsideDialog($event)" @click="backdropClick">
   <div class="cleanup-panel">
    <header class="cleanup-heading">
     <button v-if="mode === 'preview'" type="button" class="cleanup-button cleanup-icon-button" aria-label="返回任务列表" :disabled="submitting" @click="backToTasks"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m14 6-6 6 6 6" stroke-linecap="round" stroke-linejoin="round" /></svg></button>
     <div><h2 :id="titleID">{{ title }}</h2><p>{{ mode === 'preview' ? preview?.name || '核验目标与关联范围' : listError ? '清理状态待核实' : loaded ? `${attentionCount} 项待处理，${completed.length} 条已完成记录` : '正在读取清理任务' }}</p></div>
     <button ref="closeButton" type="button" class="cleanup-button cleanup-icon-button cleanup-close" :aria-label="mode === 'preview' ? '关闭删除确认' : '关闭账号清理'" :disabled="submitting" @click="closeDialog"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m6 6 12 12M6 18 18 6" stroke-linecap="round" /></svg></button>
    </header>
    <div class="cleanup-body">
     <template v-if="mode === 'list'">
      <details class="cleanup-explanation"><summary><svg class="cleanup-chevron" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m9 5 7 7-7 7" /></svg>清理范围与说明</summary><p>删除会永久移除 Emby 账号及观看记录，并清理网站身份、个人网盘凭据、播放占用和子账号名额。不会删除 115 文件、管理员网盘或共享池账号；已签发的直链可能仍有效。</p><p>删除某位用户，请从用户卡片的“管理用户”进入影响预览。未完成创建需先核查，不会自动删除或重试。</p></details>
      <p v-if="listError" role="alert" class="cleanup-message is-error">{{ listError }}{{ loaded ? '；保留上次读取结果，请刷新后再处理。' : '；待处理数量尚未确认，请重新读取。' }}</p>
      <p v-if="notice" role="status" class="cleanup-message">{{ notice }}</p>
      <p v-if="disabled" class="cleanup-message">请先完成或放弃其他用户设置的修改，再进行清理。任务记录仍可查看。</p>
      <div v-if="!loaded && !listError" class="cleanup-empty" role="status">正在读取清理任务…</div>
      <section v-if="pending.length" class="cleanup-group" aria-label="未完成创建"><h3>未完成创建 <span>{{ pending.length }}</span></h3><article v-for="target in pending" :key="target.key" :data-cleanup-key="target.key" class="cleanup-task-row"><div><strong>{{ target.name }}</strong><p>{{ target.embyId ? '已确定 Emby 身份，关联尚未完成' : '外部身份待核查' }}</p></div><button type="button" class="cleanup-button cleanup-button-text" :disabled="taskActionsDisabled" @click="inspect(target.key)">核查并预览</button></article></section>
      <section v-if="unfinished.length" class="cleanup-group" aria-label="待继续清理"><h3>待继续清理 <span>{{ unfinished.length }}</span></h3><article v-for="job in unfinished" :key="job.id" :data-cleanup-key="job.id" class="cleanup-task-row"><div><strong>{{ job.name }}</strong><p>{{ job.message }}</p><time :datetime="new Date(job.createdAt * 1000).toISOString()">{{ formatTime(job.createdAt) }}</time></div><button type="button" class="cleanup-button cleanup-button-text" :disabled="taskActionsDisabled" @click="resume(job)">继续原任务</button></article></section>
      <div v-if="loaded && !attentionCount && !listError" class="cleanup-empty"><svg aria-hidden="true" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="12" y="9" width="24" height="32" rx="5" /><path d="M19 6h10v7H19zM19 25l4 4 7-8" stroke-linecap="round" stroke-linejoin="round" /></svg><strong>暂无待处理清理</strong><p>已完成的任务保留在下方记录中</p></div>
      <details v-if="completed.length" class="cleanup-history"><summary><svg class="cleanup-chevron" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m9 5 7 7-7 7" /></svg><span>已完成记录</span><span class="cleanup-history-count">{{ completed.length }}</span></summary><details v-for="job in completed" :key="job.id" class="cleanup-history-row"><summary><span class="cleanup-history-name" :title="job.name">{{ job.name }}</span><span class="cleanup-badge is-completed">已完成</span><time>{{ formatTime(job.createdAt) }}</time><svg class="cleanup-chevron" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m9 5 7 7-7 7" /></svg></summary><p>{{ job.message }}</p></details></details>
     </template>
     <template v-else>
      <p v-if="notice" role="status" class="cleanup-message">{{ notice }}</p>
      <div class="cleanup-warning"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M10.3 4.7 2.5 18.2A2 2 0 0 0 4.2 21h15.6a2 2 0 0 0 1.7-2.8L13.7 4.7a2 2 0 0 0-3.4 0Z" /><path d="M12 9v5m0 3v.5" stroke-linecap="round" /></svg><p>{{ cleanupOnly ? '本次核查中，所列 Emby 账号均已不存在，将清理本地关联和个人凭据。执行时会重新核验，若原 ID 再次出现，仍会删除该账号。' : '永久删除所列仍存在的 Emby 账号及观看记录，并清理网站关联和个人凭据。' }} 不删除 115 文件及管理员网盘；已签发的直链可能仍有效。</p></div>
      <p v-if="action === 'preview'" role="status" class="cleanup-empty">正在核验删除影响…</p>
      <p v-if="error" role="alert" class="cleanup-message is-error">{{ error }}</p>
      <p v-if="disabled" class="cleanup-message">请先完成或放弃其他用户设置的修改，再进行清理。</p>
      <form v-if="preview" :id="formID" class="cleanup-confirm-form" @submit.prevent="remove">
       <div class="cleanup-impact"><span>处理目标 <strong>{{ preview.targets.length }}</strong></span><span v-if="preview.personalDrives !== undefined">个人网盘配置 <strong>{{ preview.personalDrives }}</strong></span><span v-if="preview.sessions !== undefined">播放占用 <strong>{{ preview.sessions }}</strong></span></div>
       <ul class="cleanup-targets"><li v-for="target in preview.targets" :key="target.key"><strong>{{ target.name }}</strong><p>{{ targetStatus(target) }}</p><p class="cleanup-target-id">{{ target.embyId || '尚未确定外部账号身份' }}</p></li></ul>
       <label v-if="preview.children" class="cleanup-cascade"><input v-model="cascade" :disabled="submitting || uncertain" type="checkbox" /><span>连同 {{ preview.children }} 个子账号一起处理：仍存在的 Emby 账号及观看记录会永久删除，并清理本地关联。</span></label>
       <label class="cleanup-confirm-label">输入用户名 <strong>{{ preview.name }}</strong> 确认<input v-model="confirmation" :disabled="submitting || uncertain" required autocomplete="off" autocapitalize="off" autocorrect="off" :spellcheck="false" /></label>
       <p class="cleanup-hint">操作开始后无法撤销，请核对目标和关联范围。</p>
      </form>
     </template>
    </div>
    <footer class="cleanup-footer">
     <template v-if="mode === 'list'"><span role="status">{{ reading ? '正在更新任务' : '仅在确认后执行清理' }}</span><button ref="refreshButton" type="button" class="cleanup-button cleanup-button-secondary" :aria-disabled="reading" @click="read">刷新任务</button></template>
     <template v-else><button type="button" class="cleanup-button cleanup-button-secondary" :disabled="submitting" @click="closeDialog">取消</button><button v-if="preview" type="submit" :form="formID" class="cleanup-button cleanup-button-danger" :disabled="!canSubmit">{{ submitting ? '正在确认删除结果…' : resuming ? '确认继续清理' : cleanupOnly ? '确认清理关联' : '确认永久删除' }}</button></template>
    </footer>
   </div>
  </dialog>
 </Teleport>
</template>

<style scoped>
.cleanup-ui { --cl-bg: #fff; --cl-soft: #f5f7fb; --cl-text: #192b47; --cl-muted: #61718a; --cl-outline: #e5eaf2; --cl-blue: #245bc7; --cl-blue-soft: #eaf1ff; --cl-error: #aa3446; --cl-error-bg: #fff0f2; --cl-warning: #885617; --cl-warning-bg: #fff3d8; --cl-success: #187052; --cl-success-bg: #e5f5ee; color: var(--cl-text); }
.dark .cleanup-ui { --cl-bg: rgb(var(--color-slate-900)); --cl-soft: rgb(var(--color-slate-800)); --cl-text: rgb(var(--color-slate-100)); --cl-muted: rgb(var(--color-slate-400)); --cl-outline: rgb(var(--color-slate-700)); --cl-blue: #8daee8; --cl-blue-soft: color-mix(in srgb, var(--cl-soft) 90%, var(--cl-blue)); --cl-error: #e8788a; --cl-error-bg: #301a20; --cl-warning: #dcb068; --cl-warning-bg: #2d2516; --cl-success: #62c499; --cl-success-bg: #142921; color-scheme: dark; }
.cleanup-trigger { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 38px; padding: 0 11px; border: 1px solid var(--cl-outline); border-radius: 9px; background: var(--cl-bg); color: var(--cl-muted); font-size: 13px; white-space: nowrap; cursor: pointer; transition: background 150ms ease; }.cleanup-trigger > svg { width: 16px; height: 16px; flex-shrink: 0; }.cleanup-trigger.has-attention > svg { color: var(--cl-warning); }
.cleanup-badge { display: inline-flex; align-items: center; justify-content: center; min-width: 20px; padding: 2px 6px; border-radius: 6px; font-size: 11px; font-weight: 600; line-height: 1.5; font-variant-numeric: tabular-nums; }.cleanup-badge.is-warning { color: var(--cl-warning); background: var(--cl-warning-bg); }.cleanup-badge.is-completed { color: var(--cl-success); background: var(--cl-success-bg); }
.cleanup-ui button:focus-visible, .cleanup-ui summary:focus-visible, .cleanup-trigger:focus-visible, .cleanup-ui input:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }.cleanup-ui button:disabled { opacity: .45; cursor: default; }
.deletion-dialog { width: min(680px, calc(100% - 24px)); max-width: none; height: min(640px, calc(100dvh - 32px - env(safe-area-inset-top) - env(safe-area-inset-bottom))); max-height: none; margin: auto; padding: 0; overflow: hidden; border: 1px solid var(--cl-outline); border-radius: 22px; background: var(--cl-bg); box-shadow: 0 24px 90px #02061735; }.deletion-dialog::backdrop { background: #0b15275c; backdrop-filter: blur(3px); }.deletion-dialog[open] { animation: cleanup-enter 200ms cubic-bezier(.2,0,0,1); }
.cleanup-panel { display: flex; flex-direction: column; height: 100%; }.cleanup-heading { display: flex; align-items: flex-start; gap: 10px; flex-shrink: 0; padding: 20px 18px 16px 24px; }.cleanup-heading > div { flex: 1; min-width: 0; }.cleanup-heading h2 { font-size: 18px; font-weight: 650; }.cleanup-heading p { margin-top: 5px; overflow: hidden; color: var(--cl-muted); font-size: 12px; white-space: nowrap; text-overflow: ellipsis; }.cleanup-close { margin-left: auto; }
.cleanup-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 40px; flex-shrink: 0; padding: 0 12px; border-radius: 9px; font-size: 12px; font-weight: 600; cursor: pointer; transition: background 150ms ease, transform 150ms ease; }.cleanup-button:not(:disabled):active { transform: scale(.97); }.cleanup-button-text { color: var(--cl-blue); background: transparent; }.cleanup-button-secondary { color: var(--cl-text); background: var(--cl-soft); }.cleanup-button-danger { color: var(--cl-error); background: var(--cl-error-bg); }.cleanup-icon-button { width: 40px; padding: 0; color: var(--cl-muted); background: transparent; }.cleanup-icon-button svg { width: 20px; height: 20px; }
.cleanup-body { min-height: 0; flex: 1; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }.cleanup-explanation { padding: 10px 12px; border-radius: 12px; background: var(--cl-soft); color: var(--cl-muted); font-size: 12px; line-height: 1.7; }.cleanup-explanation summary, .cleanup-history summary { display: flex; align-items: center; gap: 7px; cursor: pointer; list-style: none; }.cleanup-ui summary::-webkit-details-marker { display: none; }.cleanup-chevron { width: 13px; height: 13px; flex-shrink: 0; transition: transform 150ms ease; }.cleanup-ui details[open] > summary > .cleanup-chevron { transform: rotate(90deg); }.cleanup-explanation p { margin-top: 8px; }
.cleanup-message { margin-top: 12px; padding: 10px 12px; border-radius: 10px; background: var(--cl-soft); color: var(--cl-muted); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.cleanup-message.is-error { background: var(--cl-error-bg); color: var(--cl-error); }
.cleanup-group { margin-top: 20px; }.cleanup-group h3 { display: flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 600; }.cleanup-group h3 span { color: var(--cl-warning); font-variant-numeric: tabular-nums; }.cleanup-task-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 0; border-bottom: 1px solid var(--cl-outline); }.cleanup-task-row > div { flex: 1; min-width: 0; }.cleanup-task-row strong { font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }.cleanup-task-row p { margin-top: 4px; color: var(--cl-muted); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }.cleanup-task-row time { display: block; margin-top: 4px; color: var(--cl-muted); font-size: 11px; font-variant-numeric: tabular-nums; }
.cleanup-empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 36px 12px; color: var(--cl-muted); font-size: 12px; text-align: center; }.cleanup-empty > svg { width: 44px; height: 44px; color: var(--cl-blue); opacity: .6; }.cleanup-empty strong { color: var(--cl-text); font-size: 13px; font-weight: 600; }
.cleanup-history { margin-top: 16px; }.cleanup-history > summary { min-height: 44px; padding: 0 10px; border-radius: 10px; background: var(--cl-soft); color: var(--cl-text); font-size: 13px; font-weight: 600; }.cleanup-history-count { margin-left: auto; color: var(--cl-muted); font-size: 12px; font-variant-numeric: tabular-nums; }.cleanup-history-row { border-bottom: 1px solid var(--cl-outline); }.cleanup-history-row > summary { min-height: 48px; padding: 8px 2px; font-size: 12px; }.cleanup-history-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.cleanup-history-row time { color: var(--cl-muted); font-size: 11px; white-space: nowrap; font-variant-numeric: tabular-nums; }.cleanup-history-row > p { padding: 0 4px 12px; color: var(--cl-muted); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.cleanup-warning { display: flex; align-items: flex-start; gap: 9px; padding: 12px; border-radius: 12px; color: var(--cl-error); background: var(--cl-error-bg); font-size: 12px; line-height: 1.7; }.cleanup-warning > svg { width: 18px; height: 18px; flex-shrink: 0; margin-top: 2px; }
.cleanup-confirm-form { margin-top: 16px; }.cleanup-impact { display: flex; flex-wrap: wrap; gap: 8px 16px; padding: 12px; border-radius: 10px; background: var(--cl-soft); font-size: 12px; color: var(--cl-muted); }.cleanup-impact strong { margin-left: 5px; color: var(--cl-text); font-variant-numeric: tabular-nums; }.cleanup-targets { margin-top: 14px; }.cleanup-targets li { padding: 10px 0; border-bottom: 1px solid var(--cl-outline); overflow-wrap: anywhere; }.cleanup-targets strong { font-size: 13px; font-weight: 600; }.cleanup-targets p { margin-top: 4px; color: var(--cl-muted); font-size: 12px; line-height: 1.6; }.cleanup-targets p.cleanup-target-id { font-size: 11px; user-select: all; }
.cleanup-cascade { display: flex; align-items: flex-start; gap: 10px; margin-top: 16px; font-size: 12px; line-height: 1.7; }.cleanup-cascade input { width: 18px; height: 18px; flex-shrink: 0; margin-top: 2px; accent-color: var(--cl-blue); }.cleanup-confirm-label { display: block; margin-top: 16px; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.cleanup-confirm-label input { display: block; width: 100%; min-height: 42px; margin-top: 8px; padding: 8px 10px; border: 1px solid var(--cl-outline); border-radius: 9px; background: var(--cl-soft); color: var(--cl-text); font-size: 14px; }.cleanup-hint { margin-top: 8px; color: var(--cl-muted); font-size: 11px; line-height: 1.7; }
.cleanup-footer { display: flex; align-items: center; justify-content: space-between; flex-shrink: 0; gap: 12px; padding: 14px 24px max(14px, env(safe-area-inset-bottom)); border-top: 1px solid var(--cl-outline); color: var(--cl-muted); font-size: 11px; }
@media (hover: hover) { .cleanup-trigger:hover, .cleanup-button-text:not(:disabled):hover, .cleanup-button-secondary:not(:disabled):hover, .cleanup-icon-button:not(:disabled):hover { background: var(--cl-blue-soft); }.cleanup-button-danger:not(:disabled):hover { background: color-mix(in srgb, var(--cl-error) 12%, var(--cl-error-bg)); } }
@media (max-width: 639px) { .cleanup-trigger { min-height: 40px; padding: 0 10px; }.cleanup-trigger > svg { display: none; }.cleanup-button { min-height: 44px; }.deletion-dialog { height: min(640px, calc(100dvh - 24px - env(safe-area-inset-top) - env(safe-area-inset-bottom))); border-radius: 20px; }.cleanup-heading { padding: 18px 12px 14px 18px; gap: 6px; }.cleanup-heading h2 { font-size: 17px; }.cleanup-body { padding: 0 18px 18px; scrollbar-gutter: auto; }.cleanup-footer { padding: 12px 18px max(12px, env(safe-area-inset-bottom)); }.cleanup-task-row { flex-wrap: wrap; gap: 6px; }.cleanup-task-row > div { flex-basis: 100%; }.cleanup-task-row > button { margin-left: auto; }.cleanup-history-row > summary { display: grid; grid-template-columns: minmax(0,1fr) auto auto; gap: 4px 8px; padding: 10px 2px; }.cleanup-history-row time { grid-row: 2; grid-column: 1 / 3; }.cleanup-history-row .cleanup-chevron { grid-column: 3; grid-row: 1 / 3; }.cleanup-history-row .cleanup-badge { grid-column: 2; grid-row: 1; }.cleanup-confirm-label input { font-size: 16px; } }
@media (max-height: 480px) { .cleanup-heading { padding-block: 10px; }.cleanup-body { padding-bottom: 10px; }.cleanup-footer { padding-block: 8px; } }
@keyframes cleanup-enter { from { opacity: 0; transform: translateY(10px) scale(.98); } to { opacity: 1; transform: translateY(0) scale(1); } }
@media (prefers-reduced-motion: reduce) { .cleanup-trigger, .cleanup-button, .cleanup-chevron { transition: none; }.cleanup-button:not(:disabled):active { transform: none; }.deletion-dialog[open] { animation: none; } }
</style>
