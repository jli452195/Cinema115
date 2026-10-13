<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import MemberDriveSourceIcon from './MemberDriveSourceIcon.vue'
import { useToast } from '../composables/useToast'
import { memberRequest, memberStage, memberEmbyLabel, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'

const props = defineProps<{ token: string; member: MemberPlayback; disabled: boolean; deleteDisabled?: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean]; children: [owner: string]; delete: [id: string]; refresh: [] }>()
interface Values { source: string; accountId: string; childLimit: number }
function values(m: MemberPlayback): Values { return { source: m.source || 'primary', accountId: '', childLimit: m.childLimit || 0 } }
const initial = ref(values(props.member)), source = ref(props.member.source || 'primary')
const childLimit = ref(props.member.childLimit || 0), action = ref(''), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const toast = useToast()
const locked = computed(() => !!action.value || props.disabled)
const draft = computed<Values>(() => ({ source: source.value, accountId: '', childLimit: Number(childLimit.value) }))
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(initial.value))
const state = computed(() => props.member.childRevoked ? '共享已撤销' : props.member.childDisabled ? '所有者已停用登录' : props.member.childPaused ? '所有者已暂停播放' : props.member.driveStatus?.message || memberStage(props.member))
const identity = computed(() => props.member.playbackOnly ? '播放子账号' : props.member.administrator ? '管理员' : props.member.createdByCinema ? '邀请注册用户' : 'Emby 原有账号')
const available = computed(() => props.member.embyState === 'active' || props.member.embyState === 'disabled')
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
const statusText = computed(() => poolScheduled.value ? '动态调度，播放时核验' : state.value)
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
 { value: 'assigned', label: '动态共享池', description: '播放时分配健康小号，不固定账号' },
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
 <article class="user-card member-ui" :aria-label="member.embyName">
  <header class="member-card-heading">
   <EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="available ? member.imageTag : undefined" />
   <div class="member-name"><h2 :title="member.embyName">{{ member.embyName }}</h2><p class="member-muted">{{ identity }}</p></div>
   <span class="member-account-badge" :class="member.embyState === 'active' ? 'is-active' : member.embyState === 'disabled' ? 'is-disabled' : 'is-unverified'" :title="memberEmbyLabel(member.embyState)">{{ accountLabel }}</span>
  </header>
  <section v-if="available" class="member-drive-summary" aria-label="网盘摘要">
   <div class="member-source-line"><span class="member-source-icon" :class="`source-${savedSource}`"><MemberDriveSourceIcon :source="savedSource" /></span><span class="member-source-name">{{ sourceLabel }}</span><button type="button" :disabled="locked" class="member-button member-button-text member-check-button" @click="mutate('/sync', {})">{{ action === 'check' ? '检查中…' : '检查网盘' }}</button></div>
   <p class="member-drive-state" :class="error ? 'tone-error' : `tone-${statusTone}`" :title="error || state">{{ error || statusText }}</p>
   <p v-if="checkedTime" class="member-muted member-caption">最近检查 <time :datetime="member.driveStatus?.checkedAt">{{ checkedTime }}</time></p>
   <p v-else class="member-muted member-caption">{{ poolScheduled ? '按播放会话分配健康小号' : '尚无网盘检查记录' }}</p>
  </section>
  <section v-else class="member-message member-message-warning member-summary-warning"><p>{{ accountHint }}</p><button type="button" :disabled="locked" class="member-button member-button-text" @click="refreshAccount">重新核实</button></section>
  <footer class="member-card-footer">
   <p v-if="member.playbackOnly" class="member-footer-summary" :title="member.ownerName || member.ownerUserId"><span class="member-muted">所有者</span><strong>{{ member.ownerName || member.ownerUserId || '待核实' }}</strong></p>
   <p v-else-if="!member.administrator && available" class="member-footer-summary"><span class="member-muted">子账号</span><strong>{{ usage }} / {{ member.childLimit || 0 }}</strong></p>
   <p v-else class="member-muted member-caption">{{ member.administrator ? '管理员账号' : '原业务关联保留' }}</p>
   <button ref="trigger" type="button" class="member-button member-button-primary" :disabled="locked" :aria-label="`${available ? '管理用户' : '查看记录'} ${member.embyName}`" aria-haspopup="dialog" :aria-expanded="opened" :aria-controls="opened ? dialogID : undefined" @click="openDialog">{{ available ? '管理用户' : '查看记录' }}</button>
  </footer>
  <p class="member-card-id member-muted">Emby ID：{{ member.embyId }}</p>
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
.dark .member-ui { --member-bg: #182131; --member-soft: #1f2b3e; --member-text: #e4ecf8; --member-muted: #a1b0c7; --member-outline: #2c3b50; --member-blue: #3978f0; --member-blue-hover: #4c86f6; --member-blue-soft: #20365b; --member-blue-text: #a6c6ff; --member-success: #8adbb6; --member-success-bg: #1d3a32; --member-warning: #efd099; --member-warning-bg: #3b3021; --member-error: #ffacb7; --member-error-bg: #3d2834; color-scheme: dark; }
.user-card { display: flex; flex-direction: column; gap: 10px; min-width: 0; height: 100%; padding: 16px 18px; border: 1px solid var(--member-outline); border-radius: 18px; background: var(--member-bg); box-shadow: 0 3px 12px #193c7410; transition: transform 180ms ease, box-shadow 180ms ease; }
.member-card-heading { display: flex; align-items: center; gap: 10px; }
.member-name { flex: 1; min-width: 0; }
.member-name h2 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 15px; font-weight: 650; }
.member-name > p { margin-top: 4px; font-size: 12px; }
.member-muted { color: var(--member-muted); }
.member-caption { font-size: 12px; line-height: 1.6; }
.member-account-badge { flex-shrink: 0; border-radius: 7px; padding: 4px 7px; font-size: 11px; font-weight: 600; line-height: 1.4; background: var(--member-soft); color: var(--member-muted); }
.member-account-badge.is-active { color: var(--member-success); background: var(--member-success-bg); }
.member-account-badge.is-disabled, .member-account-badge.is-unverified { color: var(--member-warning); background: var(--member-warning-bg); }
.member-drive-summary { border-radius: 12px; padding: 10px 12px; background: var(--member-soft); }
.member-source-line { display: flex; align-items: center; gap: 8px; }
.member-source-icon { display: flex; flex: 0 0 30px; align-items: center; justify-content: center; width: 30px; height: 30px; border-radius: 9px; color: #7555c9; background: #ede8fb; }
.source-assigned { color: #2868d9; background: #e0ecff; }
.source-personal, .source-personal_shared { color: #137d80; background: #ddf4ef; }
.dark .member-source-icon { color: #c2b1f4; background: #352f50; }
.dark .source-assigned { color: #a5c6ff; background: #273f65; }
.dark .source-personal, .dark .source-personal_shared { color: #8cddd2; background: #203e40; }
.member-source-name { flex: 1; min-width: 0; font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }
.member-drive-state { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; height: 20px; margin-top: 6px; font-size: 12px; line-height: 20px; }
.member-drive-summary > .member-caption { margin-top: 2px; }
.tone-muted { color: var(--member-muted); }
.tone-success { color: var(--member-success); }
.tone-scheduled { color: var(--member-blue-text); }
.tone-warning { color: var(--member-warning); }
.tone-error { color: var(--member-error); }
.member-card-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: auto; }
.member-card-footer .member-button { position: relative; top: 6px; min-height: 32px; padding: 0 12px; font-size: 12px; border-radius: 9px; }
.member-footer-summary { display: flex; flex: 1; min-width: 0; flex-wrap: wrap; gap: 4px 7px; font-size: 12px; line-height: 1.5; }
.member-footer-summary strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.member-button { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; gap: 7px; min-height: 40px; padding: 0 13px; border-radius: 10px; font-size: 13px; font-weight: 600; line-height: 1.4; transition: background 150ms ease, transform 150ms ease; cursor: pointer; }
.member-button svg { width: 16px; height: 16px; }
.member-button-primary { color: #fff; background: var(--member-blue); box-shadow: 0 2px 4px #1d4ed818; }
.member-button-secondary { color: var(--member-text); background: var(--member-soft); }
.member-button-text { color: var(--member-blue-text); background: transparent; }
.member-check-button { min-height: 36px; margin-right: -6px; padding: 0 7px; font-size: 12px; }
.member-button-danger { color: var(--member-error); background: var(--member-error-bg); }
.member-icon-button { width: 40px; padding: 0; color: var(--member-muted); }
.member-icon-button svg { width: 20px; height: 20px; }
.member-button:disabled { cursor: default; opacity: .45; box-shadow: none; }
.member-button:not(:disabled):active { transform: scale(.97); }
.member-ui button:focus-visible, .member-ui input:focus-visible, .member-ui summary:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }
.member-message { padding: 12px 14px; border-radius: 12px; font-size: 13px; line-height: 1.7; overflow-wrap: anywhere; }
.member-message-warning { background: var(--member-warning-bg); color: var(--member-warning); }
.member-message-error { background: var(--member-error-bg); color: var(--member-error); }
.member-message-success { background: var(--member-success-bg); color: var(--member-success); }
.member-summary-warning { padding: 10px 12px; font-size: 12px; }
.member-summary-warning .member-button { min-height: 32px; padding: 0; }
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
.member-card-id { margin-top: -4px; padding-top: 4px; font-family: monospace; font-size: 11px; line-height: 1.5; overflow-wrap: anywhere; user-select: text; }
.member-dialog-footer { display: flex; align-items: center; flex-shrink: 0; flex-wrap: wrap; justify-content: space-between; gap: 10px; padding: 16px 24px max(16px, env(safe-area-inset-bottom)); border-top: 1px solid var(--member-outline); background: var(--member-bg); }
.member-dialog-actions { display: flex; gap: 8px; margin-left: auto; }
.member-dialog-actions .member-button { min-height: 42px; }
@media (hover: hover) { .user-card:hover { transform: translateY(-2px); box-shadow: 0 8px 22px #193c7418; } .member-button-primary:not(:disabled):hover { background: var(--member-blue-hover); } .member-button-secondary:not(:disabled):hover, .member-button-text:not(:disabled):hover, .member-icon-button:not(:disabled):hover { background: var(--member-blue-soft); } .member-source-option:not(.is-locked):not(.is-selected):hover { background: var(--member-blue-soft); } }
@media (max-width: 639px) { .user-card { padding: 16px; min-height: 230px; } .member-button { min-height: 44px; } .member-check-button { min-height: 44px; } .member-dialog { max-height: calc(100dvh - 24px - env(safe-area-inset-top) - env(safe-area-inset-bottom)); border-radius: 20px; } .member-dialog-heading { padding: 18px 12px 14px 18px; } .member-dialog-body { padding: 0 18px 18px; scrollbar-gutter: auto; } .member-dialog-footer { padding: 14px 18px max(14px, env(safe-area-inset-bottom)); } .member-dialog-footer > p { flex-basis: 100%; } .member-dialog-actions { width: 100%; } .member-dialog-actions > button { flex: 1; } .member-source-option { gap: 10px; padding: 11px; } }
@keyframes member-dialog-enter { from { opacity: 0; transform: translateY(10px) scale(.98); } to { opacity: 1; transform: translateY(0) scale(1); } }
@media (prefers-reduced-motion: reduce) { .user-card, .member-button, .member-source-option { transition: none; } .member-dialog[open] { animation: none; } .user-card:hover, .member-button:not(:disabled):active { transform: none; } }
@media (hover: hover) { .member-button-danger:not(:disabled):hover { background: color-mix(in srgb, var(--member-error) 12%, var(--member-error-bg)); } }
@media (max-width: 639px) { .user-card { min-height: 0; } .member-card-footer .member-button { position: relative; min-height: 36px; } .member-card-footer .member-button::before { content: ''; position: absolute; inset: -4px 0; } }
</style>
