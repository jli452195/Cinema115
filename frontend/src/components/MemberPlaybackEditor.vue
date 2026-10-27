<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import MemberDriveSourceIcon from './MemberDriveSourceIcon.vue'
import MemberProxyPlaybackSwitch from './MemberProxyPlaybackSwitch.vue'
import { vAmbientVisibility } from '../directives/ambientVisibility'
import { useToast } from '../composables/useToast'
import { memberRequest, memberStage, memberEmbyLabel, type MemberProxyPlaybackState, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { PlaybackActivity, PlaybackCardSummary } from '../types/userPlayback'

const props = defineProps<{ token: string; member: MemberPlayback; disabled: boolean; deleteDisabled?: boolean; playback?: PlaybackCardSummary; playbackStale?: boolean; playbackActivity?: PlaybackActivity }>()
const activity = computed(() => props.playbackActivity || 'idle')
const activityPhase = computed(() => {
 let hash = 0
 for (const char of props.member.userId) hash = (Math.imul(hash, 31) + char.charCodeAt(0)) >>> 0
 return { '--playback-delay-a': `-${hash % 7000}ms`, '--playback-delay-b': `-${(Math.imul(hash, 17) >>> 0) % 11000}ms` }
})
const emit = defineEmits<{ proxyPlayback: [state: MemberProxyPlaybackState]; updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean]; children: [owner: string]; delete: [id: string]; refresh: []; sessions: [userId: string, event: MouseEvent] }>()
interface Values { source: string; accountId: string; childLimit: number }
function values(m: MemberPlayback): Values { return { source: m.source || 'primary', accountId: '', childLimit: m.childLimit || 0 } }
const initial = ref(values(props.member)), source = ref(props.member.source || 'primary')
const childLimit = ref(props.member.childLimit || 0), action = ref(''), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const toast = useToast()
const accountBusy = ref(false)
function markAccountBusy(value: boolean) { accountBusy.value = value; emit('busy', value) }
const locked = computed(() => !!action.value || accountBusy.value || props.disabled)
const draft = computed<Values>(() => ({ source: source.value, accountId: '', childLimit: Number(childLimit.value) }))
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(initial.value))
const state = computed(() => props.member.childRevoked ? '共享已撤销' : props.member.childDisabled ? '所有者已停用登录' : props.member.childPaused ? '所有者已暂停播放' : props.member.driveStatus?.message || memberStage(props.member))
const identity = computed(() => props.member.playbackOnly ? '播放子账号' : props.member.administrator ? '管理员' : props.member.createdByCinema ? '邀请注册用户' : 'Emby 原有账号')
const available = computed(() => props.member.embyState === 'active' || props.member.embyState === 'disabled')
const canToggleProxy = computed(() => available.value && !props.member.administrator && !props.member.playbackOnly && props.member.provisioned !== false)
const embyDisabled = computed(() => props.member.embyState === 'disabled' || props.member.disabled)
const accountLabel = computed(() => ({ active: '账号正常', disabled: '已停用', deleted: '已删除', missing: '待核实', other_server: '其他服务', unknown: '待核实' })[props.member.embyState || 'unknown'])
const accountHint = computed(() => {
 switch (props.member.embyState) {
  case 'deleted': return 'Emby 用户已删除，本地关联待清理。'
  case 'missing': return '用户列表中未找到原用户 ID，尚未确认删除，请重新核实。'
  case 'other_server': return '此记录属于其他 Emby 服务，请切回原服务核实。'
  default: return '暂时无法核实 Emby 状态，原业务配置仍保留。'
 }
})
const savedSource = computed(() => props.member.playbackOnly ? 'personal_shared' : props.member.source || 'primary')
const sourceLabel = computed(() => ({ primary: '主网盘', assigned: '动态共享池', personal: '个人网盘', personal_shared: '所有者个人网盘' })[savedSource.value] || '待核实来源')
const poolScheduled = computed(() => savedSource.value === 'assigned' && props.member.driveStatus?.state === 'ready' && !props.member.driveStatus.checkedAt && !props.member.childRevoked && !props.member.childDisabled && !props.member.childPaused)
const statusText = computed(() => poolScheduled.value ? '播放时自动分配' : state.value)
const statusTone = computed(() => {
 if (props.member.childRevoked || props.member.childDisabled || props.member.childPaused) return 'warning'
 if (poolScheduled.value) return 'scheduled'
 if (props.member.driveStatus?.state === 'ready' && props.member.driveStatus.checkedAt) return 'success'
 if (['error', 'invalid_login', 'invalid_directory', 'unavailable'].includes(props.member.driveStatus?.state || '')) return 'error'
 return 'muted'
})
const usage = computed(() => props.member.childUsed === undefined ? '未读取' : String(props.member.childUsed))
const checkedTime = computed(() => {
 const value = props.member.driveStatus?.checkedAt
 if (!value) return ''
 const date = new Date(value)
 return Number.isNaN(date.getTime()) ? '' : date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
})
const sourceOptions = computed(() => [
 { value: 'primary', label: '主网盘', description: '使用管理员配置的主网盘' },
 { value: 'assigned', label: '动态共享池', description: '优先沿用原账号，新增名额不足或确认故障时切换' },
 ...(!props.member.administrator ? [{ value: 'personal', label: '个人网盘', description: '由本人在个人中心扫码绑定' }] : []),
])
const dialog = ref<HTMLDialogElement>(), trigger = ref<HTMLButtonElement>(), closeButton = ref<HTMLButtonElement>(), continueButton = ref<HTMLButtonElement>()
const opened = ref(false), confirmClose = ref(false), titleID = useId(), dialogID = useId()
let previousOverflow = '', backdropStarted = false
let lastField: HTMLElement | null = null

watch(changed, value => { emit('dirty', value); if (!value) confirmClose.value = false }, { flush: 'sync' })
watch(() => props.member, member => { if (!changed.value) reset(member) })
function reset(member: MemberPlayback) {
 initial.value = values(member); source.value = member.source || 'primary'; childLimit.value = member.childLimit || 0
}
function cancel() { reset(props.member); error.value = ''; notice.value = '' }
async function openDialog() {
 if (locked.value || opened.value) return
 cancel(); confirmClose.value = false; lastField = null; previousOverflow = document.body.style.overflow
 document.body.style.overflow = 'hidden'; opened.value = true
 await nextTick()
 if (lifecycle.signal.aborted) return
 dialog.value?.showModal(); closeButton.value?.focus({ preventScroll: true })
}
function closeDialog() {
 dialog.value?.close(); opened.value = false; confirmClose.value = false
 document.body.style.overflow = previousOverflow
 if (trigger.value?.isConnected) trigger.value.focus({ preventScroll: true })
}
async function requestClose() {
 if (action.value) return
 if (confirmClose.value) { continueEditing(); return }
 if (changed.value) { confirmClose.value = true; await nextTick(); continueButton.value?.focus({ preventScroll: true }); return }
 closeDialog()
}
function discardAndClose() { if (!action.value) { cancel(); closeDialog() } }
function rememberField(event: FocusEvent) { if (event.target instanceof HTMLInputElement) lastField = event.target }
function continueEditing() {
 confirmClose.value = false
 void nextTick(() => {
  const target = lastField?.isConnected ? lastField : dialog.value?.querySelector<HTMLElement>('input:checked:not(:disabled), input:not(:disabled)')
  ;(target || closeButton.value)?.focus({ preventScroll: true })
 })
}
function outsideDialog(event: MouseEvent) {
 if (event.target !== dialog.value || !dialog.value) return false
 const bounds = dialog.value.getBoundingClientRect()
 return event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom
}
function backdropClick(event: MouseEvent) { if (backdropStarted && outsideDialog(event)) void requestClose(); backdropStarted = false }
async function showChildren() {
 if (locked.value || changed.value) return
 closeDialog(); await nextTick(); emit('children', props.member.userId)
}
async function inspectDeletion() {
 if (locked.value || changed.value || props.deleteDisabled) return
 closeDialog(); await nextTick(); emit('delete', props.member.userId)
}
async function refreshAccount() {
 if (locked.value || changed.value) return
 if (opened.value) { closeDialog(); await nextTick() }
 emit('refresh')
}
async function mutate(suffix: string, body: object, save = false) {
 if (locked.value) return
 action.value = save ? 'save' : 'check'; emit('busy', true); error.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, `/${encodeURIComponent(props.member.userId)}${suffix}`, body)
  if (lifecycle.signal.aborted) return
  const updated = (data.members as MemberPlayback[]).find(m => m.userId === props.member.userId)
  if (!updated) throw new Error('状态未返回，请刷新核查；请勿重复提交')
  const merged = { ...props.member, ...updated, disabled: props.member.disabled, imageTag: props.member.imageTag, embyState: props.member.embyState, embyCheckedAt: props.member.embyCheckedAt }
  // Health checks retain both the draft and its original write baseline.
  if (save) reset(merged)
  emit('updated', merged, data.settings)
  notice.value = save ? '设置已保存。' : '已更新网盘检查结果。'
  if (!opened.value) toast.success(`${props.member.embyName}：${notice.value}`)
 } catch (e) { if (!lifecycle.signal.aborted) {
  error.value = e instanceof Error ? e.message : '操作失败，请重试'
  if (!opened.value) toast.error(`${props.member.embyName}：${error.value}`)
 } }
 finally { action.value = ''; emit('busy', false) }
}
function save() { if (available.value && !props.member.playbackOnly && changed.value && !confirmClose.value) void mutate('/settings', { ...draft.value, expected: initial.value }, true) }
onBeforeUnmount(() => {
 lifecycle.abort()
 if (opened.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }
 emit('dirty', false); emit('busy', false)
})
</script>

<template>
 <article v-ambient-visibility class="user-card member-ui ambient-glow" :data-playback-activity="activity" :aria-label="member.embyName">
  <Transition name="member-activity">
   <span v-if="activity !== 'idle'" class="member-activity-light ambient-glow ambient-glow--playback" :class="{ 'is-playing': activity === 'playing' }" :style="activityPhase" aria-hidden="true" />
  </Transition>
  <header class="member-card-heading">
   <EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="available ? member.imageTag : undefined" />
   <div class="member-name"><h2 :title="member.embyName">{{ member.embyName }}</h2><p class="member-muted member-card-identity"><span>{{ identity }}</span><span v-if="canToggleProxy && embyDisabled" class="member-emby-disabled" title="Emby 账号本身已停用，需在 Emby 中恢复；反代播放开关不会修改此状态">Emby 已停用</span></p></div>
   <MemberProxyPlaybackSwitch v-if="canToggleProxy" :token="token" :user-id="member.userId" :name="member.embyName" :enabled="!member.proxyPlaybackDisabled" :revision="member.proxyPlaybackRevision || 0" :emby-disabled="embyDisabled" :disabled="disabled || !!action || opened || changed" @updated="emit('proxyPlayback', $event)" @busy="markAccountBusy" />
   <span v-else-if="member.embyState === 'active'" class="member-account-ok" role="img" aria-label="账号已启用" :title="memberEmbyLabel(member.embyState)"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9" /><path d="m8 12 2.5 2.5 5-5" stroke-linecap="round" stroke-linejoin="round" /></svg></span>
   <span v-else class="member-account-badge" :class="member.embyState === 'disabled' ? 'is-disabled' : 'is-unverified'" :title="memberEmbyLabel(member.embyState)">{{ accountLabel }}</span>
  </header>
  <section v-if="available" class="member-drive-summary" aria-label="网盘摘要">
   <span class="member-source-symbol member-source-icon" :class="`source-${savedSource}`"><MemberDriveSourceIcon :source="savedSource" /></span>
   <div class="member-source-copy">
    <p class="member-source-name">{{ sourceLabel }}</p>
    <p v-if="!playback || error || statusTone === 'warning' || statusTone === 'error'" class="member-drive-state" :class="error ? 'tone-error' : `tone-${statusTone}`"><span>{{ error || statusText }}</span><time v-if="checkedTime && !error" class="member-checked-time member-muted" :datetime="member.driveStatus?.checkedAt"><span class="sr-only">最近检查时间：</span>{{ checkedTime }}</time></p>
    <button v-if="playback" type="button" class="member-playback-link" :class="{ 'is-stale': playbackStale }" :aria-label="`${member.embyName}，${playbackStale ? '取链状态待核实' : playback.detail}，查看播放会话`" aria-haspopup="dialog" @click="emit('sessions', member.userId, $event)">
     <template v-if="!playbackStale"><span class="member-playback-label">{{ playback.label }}</span><span v-if="playback.name" class="member-playback-name" :title="playback.name">{{ playback.name }}</span><span v-if="playback.suffix" class="member-playback-suffix">{{ playback.suffix }}</span></template>
     <span v-else class="member-playback-label">取链状态待核实</span>
     <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m9 5 7 7-7 7" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
   </div>
  </section>
  <section v-else class="member-summary-warning"><p>{{ accountHint }}</p></section>
  <footer class="member-card-footer">
   <p v-if="member.playbackOnly" class="member-footer-summary" :title="member.ownerName || member.ownerUserId"><span class="member-muted">所有者</span><strong>{{ member.ownerName || member.ownerUserId || '待核实' }}</strong></p>
   <p v-else-if="!member.administrator && available" class="member-footer-summary"><span v-if="member.childUsed === 0 && !member.childLimit" class="member-muted">未开放子账号</span><template v-else><span class="member-muted">子账号</span><strong>{{ usage }} / {{ member.childLimit || 0 }}</strong></template></p>
   <p v-else class="member-muted member-caption">{{ member.administrator ? '管理员账号' : '原业务关联保留' }}</p>
   <button ref="trigger" type="button" class="member-button member-button-secondary member-manage-button" :disabled="locked" :aria-label="`${available ? '管理用户' : '查看记录'} ${member.embyName}`" aria-haspopup="dialog" :aria-expanded="opened" :aria-controls="opened ? dialogID : undefined" @click="openDialog">{{ available ? '管理用户' : '查看记录' }}</button>
  </footer>
 </article>

 <Teleport to="body">
  <dialog v-if="opened" :id="dialogID" ref="dialog" class="member-dialog member-ui" :aria-labelledby="titleID" @cancel.prevent="requestClose" @pointerdown="backdropStarted = outsideDialog($event)" @click="backdropClick">
   <form class="member-dialog-form" @submit.prevent="save">
    <header class="member-dialog-heading">
     <div class="member-name"><h2 :id="titleID">{{ available ? '管理用户' : '查看记录' }}</h2><p class="member-muted" :title="member.embyName">{{ member.embyName }}<span class="member-identity">{{ identity }}</span></p></div>
     <button ref="closeButton" type="button" :disabled="!!action" class="member-button member-icon-button" aria-label="关闭用户管理" @click="requestClose"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
    </header>
    <div class="member-dialog-body" :inert="confirmClose" @focusin="rememberField">
     <section v-if="!available" class="member-message member-message-warning"><p>{{ accountHint }}</p><p v-if="member.embyCheckedAt" class="member-caption">最近核查：{{ new Date(member.embyCheckedAt).toLocaleString('zh-CN') }}</p><button type="button" :disabled="locked" class="member-button member-button-text" @click="refreshAccount">重新核实</button></section>
     <fieldset v-if="available && !member.playbackOnly" class="member-section" :disabled="locked || embyDisabled">
      <legend>播放来源</legend>
      <div class="member-source-options">
       <label v-for="option in sourceOptions" :key="option.value" class="member-source-option" :class="{ 'is-selected': source === option.value, 'is-locked': locked || embyDisabled }">
        <input v-model="source" type="radio" :name="`${dialogID}-source`" :value="option.value" :aria-label="option.label" />
        <span class="member-source-icon" :class="`source-${option.value}`"><MemberDriveSourceIcon :source="option.value" /></span>
        <span class="member-option-copy"><strong>{{ option.label }}</strong><span class="member-muted">{{ option.description }}</span></span>
        <svg class="member-radio-mark" aria-hidden="true" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="1.7" /><circle v-if="source === option.value" cx="12" cy="12" r="5" fill="currentColor" /></svg>
       </label>
      </div>
      <p v-if="source === 'assigned'" class="member-hint">全池不可用时暂停新取链，不会回退到主网盘。</p>
      <p v-if="source === 'personal'" class="member-hint">{{ member.personalDrive?.connected ? '个人网盘已连接，由本人管理。' : '待用户在个人中心扫码并选择目录。' }}</p>
      <p v-if="embyDisabled" class="member-hint tone-warning">Emby 已停用，恢复后可修改播放来源。</p>
     </fieldset>
     <section v-else-if="available" class="member-section"><h3>播放来源</h3><div class="member-readonly-source"><span class="member-source-icon source-personal_shared"><MemberDriveSourceIcon source="personal_shared" /></span><div><strong>所有者个人网盘</strong><p class="member-muted member-caption">所有者：{{ member.ownerName || member.ownerUserId || '待核实' }}</p></div></div><p class="member-hint">仅允许 Emby 播放，不允许网站登录；共享状态由所有者管理。</p></section>
     <section v-if="available && !member.playbackOnly && !member.administrator" class="member-section">
      <div class="member-section-heading"><label :for="`${dialogID}-limit`">子账号名额</label><button v-if="member.childUsed" type="button" :disabled="locked || changed" class="member-button member-button-text" @click="showChildren">查看子账号</button></div>
      <div class="member-quota-row"><input :id="`${dialogID}-limit`" v-model.number="childLimit" aria-label="子账号名额" type="number" required min="0" max="20" step="1" :disabled="locked" /><p class="member-muted member-caption">已使用 <strong>{{ usage }}</strong> 个，不包含本人</p></div>
      <p class="member-hint">可设 0–20 个。填 0 停止新建，已有子账号保留。</p>
      <p v-if="member.childUsed && childLimit < member.childUsed" class="member-caption tone-warning">设置少于已使用数量，保存后暂时不能新建。</p>
     </section>
     <section v-if="available" class="member-section member-health" aria-label="网盘检查">
      <div class="member-section-heading"><h3>网盘检查</h3><button type="button" :disabled="locked" class="member-button member-button-secondary" @click="mutate('/sync', {})">{{ action === 'check' ? '检查中…' : '检查网盘' }}</button></div>
      <p :class="`tone-${statusTone}`">{{ statusText }}</p>
      <p class="member-hint">{{ poolScheduled ? '共享池在播放时实时核验，不记录统一检查时间。' : checkedTime ? `最近检查：${checkedTime}` : '尚无网盘检查记录。' }}</p>
      <p v-if="changed" class="member-hint">检查使用已保存配置，保留当前未保存的修改。</p>
     </section>
     <p v-if="error" role="alert" class="member-message member-message-error">{{ error }}</p><p v-if="notice" role="status" class="member-message member-message-success">{{ notice }}</p>
     <details class="member-more"><summary>更多信息与操作</summary><p class="member-id member-muted">Emby ID：{{ member.embyId }}</p><p class="member-hint">媒体库、分级、设备及下载权限在 Emby 中管理。</p><button v-if="!member.administrator && (available || member.embyState === 'deleted' || member.embyState === 'missing')" type="button" :disabled="locked || changed || deleteDisabled" class="member-button member-button-danger" @click="inspectDeletion">{{ member.embyState === 'deleted' || member.embyState === 'missing' ? '预览清理影响' : '预览删除影响' }}</button><p v-if="changed" class="member-hint">查看子账号或删除影响前，请先保存或取消修改。</p></details>
    </div>
    <footer class="member-dialog-footer">
     <template v-if="confirmClose"><p role="status" class="member-caption tone-warning">有未保存的修改，是否放弃？</p><div class="member-dialog-actions"><button ref="continueButton" type="button" class="member-button member-button-secondary" @click="continueEditing">继续编辑</button><button type="button" class="member-button member-button-danger" @click="discardAndClose">放弃并关闭</button></div></template>
     <template v-else><p class="member-caption member-muted">{{ action === 'check' ? '正在检查，请稍候' : changed ? '有未保存的修改' : available ? '设置保存后生效' : '仅查看保留的业务关联' }}</p><div class="member-dialog-actions"><button type="button" :disabled="!!action" class="member-button member-button-secondary" @click="requestClose">{{ available ? '取消' : '关闭' }}</button><button v-if="available && !member.playbackOnly" type="submit" :disabled="locked || !changed" class="member-button member-button-primary">{{ action === 'save' ? '保存中…' : '保存更改' }}</button></div></template>
    </footer>
   </form>
  </dialog>
 </Teleport>
</template>

<style scoped>
.member-ui { --member-bg: #fff; --member-soft: #f5f7fb; --member-text: #192b47; --member-muted: #61718a; --member-outline: #e5eaf2; --member-blue: #2563eb; --member-blue-hover: #1d4ed8; --member-blue-soft: #eef4ff; --member-blue-text: #245bc7; --member-success: #14714e; --member-success-bg: #eaf8f0; --member-warning: #96601d; --member-warning-bg: #fff6e5; --member-error: #be3446; --member-error-bg: #fff0f2; color: var(--member-text); }
.dark .member-ui { --member-bg: rgb(var(--color-slate-900)); --member-soft: rgb(var(--color-slate-800)); --member-text: rgb(var(--color-slate-100)); --member-muted: rgb(var(--color-slate-400)); --member-outline: rgb(var(--color-slate-700)); --member-blue: #3769b5; --member-blue-hover: #3b70bd; --member-blue-soft: color-mix(in srgb, var(--member-soft) 90%, #8daee8); --member-blue-text: #8daee8; --member-success: #62c499; --member-success-bg: #142921; --member-warning: #dcb068; --member-warning-bg: #2d2516; --member-error: #e8788a; --member-error-bg: #301a20; color-scheme: dark; }
.user-card { --member-card-wash: rgb(37 99 235 / 3.5%); display: flex; flex-direction: column; gap: 16px; min-width: 0; height: 100%; padding: 18px; border: 1px solid color-mix(in srgb, var(--member-outline) 55%, transparent); border-radius: 16px; background: radial-gradient(ellipse at 100% 0%, var(--member-card-wash), transparent 60%), var(--member-bg); box-shadow: 0 1px 3px rgb(15 23 42 / 4%); transition: box-shadow 250ms ease-out, transform 250ms ease-out; }
.dark .user-card { --member-card-wash: rgb(147 197 253 / 5%); }
.user-card:has(> .member-activity-light)::before, .user-card:has(> .member-activity-light)::after { animation: none; opacity: 0; }
.user-card > .member-activity-light { position: absolute; inset: 0; z-index: 0; border-radius: inherit; pointer-events: none; opacity: .55; transition: opacity 600ms ease; }
.user-card > .member-activity-light.is-playing { opacity: 1; }
.member-activity-enter-active, .member-activity-leave-active { transition: opacity 700ms ease; }
.user-card > .member-activity-light.member-activity-enter-from, .user-card > .member-activity-light.member-activity-leave-to { opacity: 0; }
.member-card-heading { display: flex; align-items: center; gap: 10px; }
.member-name { flex: 1; min-width: 0; }
.member-name h2 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 15px; font-weight: 650; }
.member-name > p { margin-top: 4px; font-size: 12px; }
.member-card-identity { display: flex; align-items: center; gap: 6px; min-width: 0; }
.member-card-identity > span:first-child { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.member-emby-disabled { flex-shrink: 0; font-size: 11px; color: var(--member-muted); }
.member-muted { color: var(--member-muted); }
.member-caption { font-size: 12px; line-height: 1.6; }
.member-account-badge { flex-shrink: 0; border-radius: 7px; font-size: 11px; line-height: 1.4; color: var(--member-muted); }
.member-account-badge.is-active { color: var(--member-success); }
.member-account-ok { display: flex; flex: 0 0 52px; align-items: center; justify-content: center; min-height: 44px; color: var(--member-blue-text); }.member-account-ok svg { width: 16px; height: 16px; }
.member-account-badge.is-disabled, .member-account-badge.is-unverified { padding: 4px 7px; color: var(--member-warning); background: var(--member-warning-bg); }
.member-drive-summary { display: grid; grid-template-columns: 28px minmax(0, 1fr); align-items: start; column-gap: 10px; }
.member-source-copy { min-width: 0; }
.member-source-symbol.member-source-icon { flex-basis: 28px; width: 28px; height: 28px; margin-top: -4px; border-radius: 8px; }
.member-source-icon { display: flex; flex: 0 0 30px; align-items: center; justify-content: center; width: 30px; height: 30px; border-radius: 9px; color: #7555c9; background: #ede8fb; }
.source-assigned { color: #2868d9; background: #e0ecff; }
.source-personal, .source-personal_shared { color: #137d80; background: #ddf4ef; }
.dark .member-source-icon { color: #c2b1f4; background: #352f50; }
.dark .source-assigned { color: #a5c6ff; background: #273f65; }
.dark .source-personal, .dark .source-personal_shared { color: #8cddd2; background: #203e40; }
.member-source-name { font-size: 13px; font-weight: 600; line-height: 20px; overflow-wrap: anywhere; }
.member-playback-link { display: flex; align-items: center; gap: 4px; width: fit-content; max-width: calc(100% + 12px); min-height: 44px; margin-inline: -6px; padding-inline: 6px; color: var(--member-muted); text-align: left; font-size: 12px; line-height: 20px; cursor: pointer; border-radius: 7px; transition: background-color 160ms ease; }
.member-playback-label, .member-playback-suffix { flex-shrink: 0; }
.member-playback-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--member-text); font-weight: 600; }
.member-playback-link > svg { flex: 0 0 13px; width: 13px; height: 13px; color: var(--member-muted); transition: color 160ms ease, transform 160ms ease; }
.member-playback-link:focus-visible { outline: 2px solid #6095ff; outline-offset: 2px; }
@media (hover: hover) { .member-playback-link:hover { background: var(--member-blue-soft); } .member-playback-link:hover > svg { color: var(--member-blue-text); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .member-playback-link:hover > svg { transform: translateX(2px); } }
.member-drive-state { display: flex; flex-wrap: wrap; align-items: baseline; align-content: center; gap: 2px 8px; min-height: 44px; font-size: 12px; line-height: 20px; overflow-wrap: anywhere; }
.member-checked-time { white-space: nowrap; }
.member-drive-state.tone-success, .member-drive-state.tone-scheduled { color: var(--member-muted); }
.tone-muted { color: var(--member-muted); }
.tone-success { color: var(--member-success); }
.tone-scheduled { color: var(--member-blue-text); }
.tone-warning { color: var(--member-warning); }
.tone-error { color: var(--member-error); }
.member-card-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: auto; }
.member-card-footer .member-button { min-height: 44px; padding: 0 12px; font-size: 12px; border-radius: 10px; }
.member-footer-summary { display: flex; flex: 1; min-width: 0; flex-wrap: wrap; gap: 4px 7px; font-size: 12px; line-height: 1.5; }
.member-footer-summary strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.member-button { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; gap: 7px; min-height: 40px; padding: 0 13px; border-radius: 10px; font-size: 13px; font-weight: 600; line-height: 1.4; transition: background 150ms ease, transform 150ms ease; cursor: pointer; }
.member-button svg { width: 16px; height: 16px; }
.member-button-primary { color: #fff; background: var(--member-blue); box-shadow: 0 2px 4px #1d4ed818; }
.member-button-secondary { color: var(--member-text); background: var(--member-soft); }
.member-manage-button { --member-action-bg: var(--member-blue-soft); color: var(--member-blue-text); background: var(--member-action-bg); }
.dark .member-manage-button { --member-action-bg: color-mix(in srgb, var(--member-bg) 78%, #3b82f6); }
.member-button-text { color: var(--member-blue-text); background: transparent; }
.member-button-danger { color: var(--member-error); background: var(--member-error-bg); }
.member-icon-button { width: 40px; padding: 0; color: var(--member-muted); }
.member-icon-button svg { width: 20px; height: 20px; }
.member-button:disabled { cursor: default; opacity: .45; box-shadow: none; }
.dark .member-ui .member-button:disabled { opacity: 1; background: var(--member-soft); color: var(--member-muted); }
.member-button:not(:disabled):active { transform: scale(.97); }
.member-ui button:focus-visible, .member-ui input:focus-visible, .member-ui summary:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }
.member-message { padding: 12px 14px; border-radius: 12px; font-size: 13px; line-height: 1.7; overflow-wrap: anywhere; }
.member-message-warning { background: var(--member-warning-bg); color: var(--member-warning); }
.member-message-error { background: var(--member-error-bg); color: var(--member-error); }
.member-message-success { background: var(--member-success-bg); color: var(--member-success); }
.member-summary-warning { min-width: 0; color: var(--member-warning); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.member-dialog { width: min(560px, calc(100% - 24px)); max-width: none; max-height: calc(100dvh - 32px - env(safe-area-inset-top) - env(safe-area-inset-bottom)); margin: auto; padding: 0; border: 1px solid var(--member-outline); border-radius: 22px; overflow: hidden; background: var(--member-bg); box-shadow: 0 24px 90px #02061735; }
.member-dialog::backdrop { background: #0b15275c; backdrop-filter: blur(3px); }
.member-dialog[open] { animation: member-dialog-enter 200ms cubic-bezier(.2, 0, 0, 1); }
.member-dialog-form { display: flex; flex-direction: column; max-height: inherit; }
.member-dialog-heading { display: flex; align-items: flex-start; flex-shrink: 0; gap: 16px; padding: 20px 18px 16px 24px; }
.member-dialog-heading h2 { font-size: 18px; }
.member-dialog-heading .member-name > p { white-space: normal; overflow-wrap: anywhere; }
.member-identity { display: inline-block; margin-left: 10px; }
.member-dialog-body { min-height: 0; flex: 1; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.member-section { min-width: 0; margin: 0 0 22px; padding: 0; border: 0; font-size: 13px; }
.member-section legend, .member-section h3, .member-section-heading > label { margin-bottom: 10px; font-size: 13px; font-weight: 650; }
.member-source-options { display: flex; flex-direction: column; gap: 7px; }
.member-source-option { position: relative; display: flex; align-items: center; gap: 12px; padding: 12px; border: 1px solid transparent; border-radius: 12px; background: var(--member-soft); cursor: pointer; transition: background 150ms ease, box-shadow 150ms ease; }
.member-source-option.is-selected { background: var(--member-blue-soft); box-shadow: inset 3px 0 var(--member-blue); }
.member-source-option.is-locked { cursor: default; }
.member-source-option.is-locked:not(.is-selected) { opacity: .65; }
.member-source-option:has(input:focus-visible) { outline: 2px solid #6095ff; outline-offset: 2px; }
.member-source-option input { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
.member-source-option .member-source-icon { flex-basis: 36px; width: 36px; height: 36px; }
.member-option-copy { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 3px; }
.member-option-copy strong { font-weight: 600; }
.member-option-copy > span { font-size: 12px; line-height: 1.5; }
.member-radio-mark { width: 22px; height: 22px; flex-shrink: 0; color: var(--member-muted); }
.is-selected .member-radio-mark { color: var(--member-blue-text); }
.member-hint { margin-top: 8px; color: var(--member-muted); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.member-hint.tone-warning { color: var(--member-warning); }
.member-section-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.member-section-heading h3, .member-section-heading > label { margin: 0; }
.member-quota-row { display: flex; align-items: center; gap: 12px; }
.member-quota-row input { width: 84px; min-height: 40px; padding: 8px 12px; border: 1px solid var(--member-outline); border-radius: 10px; background: var(--member-soft); color: var(--member-text); font-size: 16px; }
.member-readonly-source { display: flex; align-items: center; gap: 12px; padding: 12px; border-radius: 12px; background: var(--member-soft); overflow-wrap: anywhere; }
.member-readonly-source > div { min-width: 0; }
.member-health { padding: 14px; border-radius: 14px; background: var(--member-soft); }
.member-health > p { overflow-wrap: anywhere; }
.member-health .member-button-secondary { background: var(--member-bg); }
.member-more { margin-top: 18px; font-size: 12px; }
.member-more summary { width: fit-content; padding-block: 6px; color: var(--member-muted); cursor: pointer; }
.member-more .member-button { margin-top: 12px; }
.member-id { margin-top: 10px; font-family: monospace; overflow-wrap: anywhere; user-select: text; }
.member-dialog-footer { display: flex; align-items: center; flex-shrink: 0; flex-wrap: wrap; justify-content: space-between; gap: 10px; padding: 16px 24px max(16px, env(safe-area-inset-bottom)); border-top: 1px solid var(--member-outline); background: var(--member-bg); }
.member-dialog-actions { display: flex; gap: 8px; margin-left: auto; }
.member-dialog-actions .member-button { min-height: 42px; }
@media (hover: hover) and (pointer: fine) { .user-card:hover { z-index: 1; box-shadow: 0 12px 28px -12px rgb(15 23 42 / 18%); } .dark .user-card:hover { box-shadow: 0 12px 28px -12px rgb(0 0 0 / 40%); } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .user-card:hover { transform: translateY(-3px); } }
@media (hover: hover) { .member-button-primary:not(:disabled):hover { background: var(--member-blue-hover); } .member-button-secondary:not(:disabled):hover, .member-button-text:not(:disabled):hover, .member-icon-button:not(:disabled):hover { background: var(--member-blue-soft); } .member-manage-button:not(:disabled):hover { background: color-mix(in srgb, var(--member-action-bg) 88%, var(--member-blue-text)); } .member-source-option:not(.is-locked):not(.is-selected):hover { background: var(--member-blue-soft); } }
@media (max-width: 639px) { .user-card { padding: 16px; } .member-button { min-height: 44px; } .member-dialog { max-height: calc(100dvh - 24px - env(safe-area-inset-top) - env(safe-area-inset-bottom)); border-radius: 20px; } .member-dialog-heading { padding: 18px 12px 14px 18px; } .member-dialog-body { padding: 0 18px 18px; scrollbar-gutter: auto; } .member-dialog-footer { padding: 14px 18px max(14px, env(safe-area-inset-bottom)); } .member-dialog-footer > p { flex-basis: 100%; } .member-dialog-actions { width: 100%; } .member-dialog-actions > button { flex: 1; } .member-source-option { gap: 10px; padding: 11px; } }
@keyframes member-dialog-enter { from { opacity: 0; transform: translateY(10px) scale(.98); } to { opacity: 1; transform: translateY(0) scale(1); } }
@media (prefers-reduced-motion: reduce) { .user-card, .member-button, .member-source-option, .member-playback-link, .member-playback-link > svg, .user-card > .member-activity-light { transition: none; } .member-dialog[open] { animation: none; } .user-card:hover, .member-button:not(:disabled):active { transform: none; } }
@media (hover: hover) { .member-button-danger:not(:disabled):hover { background: color-mix(in srgb, var(--member-error) 12%, var(--member-error-bg)); } }
</style>
