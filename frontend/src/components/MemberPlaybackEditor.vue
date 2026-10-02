<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; member: MemberPlayback; settings: MemberSettings; accounts: P115Account[]; disabled: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean]; children: [owner: string] }>()
interface Values { source: string; accountId: string; childLimit: number }
function values(m: MemberPlayback): Values { return { source: m.source || 'primary', accountId: m.assignedAccountId || '', childLimit: m.childLimit || 0 } }
const initial = ref(values(props.member)), source = ref(''), childLimit = ref(3), allowChildren = ref(false)
const open = ref(false), busy = ref(false), error = ref(''), notice = ref(''), discard = ref(false)
const dialog = ref<HTMLDialogElement>(), trigger = ref<HTMLButtonElement>()
const titleID = useId(), formID = useId()
const lifecycle = new AbortController()
let previousOverflow = ''
const locked = computed(() => busy.value || props.disabled)
const draft = computed<Values>(() => ({ source: source.value.startsWith('pool:') ? 'assigned' : source.value, accountId: source.value.startsWith('pool:') ? source.value.slice(5) : '', childLimit: allowChildren.value ? Number(childLimit.value) : 0 }))
const changed = computed(() => open.value && JSON.stringify(draft.value) !== JSON.stringify(initial.value))
const state = computed(() => props.member.childRevoked ? '共享已撤销' : props.member.childDisabled ? '所有者已停用登录' : props.member.childPaused ? '所有者已暂停播放' : props.member.driveStatus?.message || memberStage(props.member))
const checkedTime = computed(() => {
 const value = props.member.driveStatus?.checkedAt
 if (!value) return ''
 const date = new Date(value)
 return Number.isNaN(date.getTime()) ? '' : date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
})
const sourceLabel = computed(() => props.member.playbackOnly ? '所有者个人网盘' : props.member.source === 'assigned' ? props.accounts.find(a => a.id === props.member.assignedAccountId)?.name || '共享小号不可用' : props.member.source === 'personal' ? '个人网盘' : '主网盘')
const identity = computed(() => props.member.playbackOnly ? '播放子账号' : props.member.administrator ? '管理员' : props.member.createdByCinema ? '邀请注册用户' : '已有 Emby 用户')
const usage = computed(() => props.member.childUsed === undefined ? '—' : String(props.member.childUsed))
const missingPool = computed(() => source.value.startsWith('pool:') && source.value !== 'pool:' && !props.accounts.some(a => `pool:${a.id}` === source.value))
watch(changed, value => { emit('dirty', value); discard.value = false })
function reset(m: MemberPlayback) {
 initial.value = values(m); source.value = m.source === 'assigned' ? `pool:${m.assignedAccountId || ''}` : m.source || 'primary'
 allowChildren.value = (m.childLimit || 0) > 0; childLimit.value = m.childLimit || 3
}
async function show() {
 reset(props.member); error.value = ''; notice.value = ''; discard.value = false
 previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; open.value = true
 await nextTick(); dialog.value?.showModal()
}
function close(force = false) {
 if (busy.value) return
 if (changed.value && !force) { discard.value = true; return }
 dialog.value?.close(); open.value = false; document.body.style.overflow = previousOverflow; emit('dirty', false); trigger.value?.focus()
}
async function mutate(suffix: string, body: object, save = false) {
 if (locked.value) return
 busy.value = true; emit('busy', true); error.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, `/${encodeURIComponent(props.member.userId)}${suffix}`, body)
  const updated = (data.members as MemberPlayback[]).find(m => m.userId === props.member.userId)
  if (!updated) throw new Error('状态未返回，请刷新核查；请勿重复提交')
  const merged = { ...props.member, ...updated, disabled: props.member.disabled, imageTag: props.member.imageTag }
  emit('updated', merged, data.settings); reset(merged)
  notice.value = save ? '设置已保存。' : '操作已完成。'
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败，请重试' }
 finally { busy.value = false; emit('busy', false) }
}
const permissionUnknown = ref(false)
const toggleLocked = computed(() => locked.value || changed.value || permissionUnknown.value || memberPermissionUnknown() || !!props.member.childRevoked || !!props.member.childDisabled || !!props.member.disabled || !!props.member.childPaused)
function memberPermissionUnknown() { return typeof props.member.playbackAllowed !== 'boolean' }
async function refreshPermissions() {
 if (locked.value) return
 busy.value = true; emit('busy', true); error.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, '/' + encodeURIComponent(props.member.userId) + '/permissions')
  const updated = data.members.find((m: MemberPlayback) => m.userId === props.member.userId)
  if (!updated) throw new Error('未返回 Emby 权限，请重试')
  emit('updated', { ...props.member, ...updated, playbackAllowed: updated.playbackAllowed, imageTag: props.member.imageTag }, data.settings)
  permissionUnknown.value = false
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
 finally { busy.value = false; emit('busy', false) }
}
async function togglePlayback() {
 if (toggleLocked.value) return
 busy.value = true; emit('busy', true); error.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, '/' + encodeURIComponent(props.member.userId) + '/playback', { allowed: !props.member.playbackAllowed, expected: props.member.playbackAllowed })
  const updated = data.members.find((m: MemberPlayback) => m.userId === props.member.userId)
  if (!updated || typeof updated.playbackAllowed !== 'boolean') throw new Error('Emby 修改结果未确认，请刷新查看')
  emit('updated', { ...props.member, ...updated, imageTag: props.member.imageTag }, data.settings)
  notice.value = 'Emby 播放权限已更新。'
 } catch (e) {
  permissionUnknown.value = true
  if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : 'Emby 修改结果未确认，请刷新查看；不会自动重试'
 } finally { busy.value = false; emit('busy', false) }
}
function save() { if (changed.value) void mutate('/settings', { ...draft.value, expected: initial.value }, true) }
function viewChildren() { if (changed.value) return; close(); emit('children', props.member.userId) }
onBeforeUnmount(() => { lifecycle.abort(); if (open.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }; emit('dirty', false); emit('busy', false) })
</script>

<template>
 <article class="user-card flex h-full min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
  <header class="flex items-center gap-3"><EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="member.imageTag" /><div class="min-w-0 flex-1"><h2 class="truncate font-semibold text-slate-900 dark:text-slate-100" :title="member.embyName">{{ member.embyName }}</h2><p class="mt-1 text-xs text-slate-500">{{ identity }}</p></div></header>
  <div class="mt-5 flex items-center justify-between gap-3 text-sm"><span class="text-slate-500">Emby 播放权限</span><div class="flex items-center gap-2"><span>{{ permissionUnknown || memberPermissionUnknown() ? '待确认' : member.playbackAllowed ? '允许' : '禁止' }}</span><button type="button" role="switch" aria-label="允许 Emby 播放" :aria-checked="member.playbackAllowed === true" :disabled="toggleLocked" class="relative h-6 w-11 shrink-0 rounded-full disabled:opacity-40" :class="member.playbackAllowed ? 'bg-blue-600' : 'bg-slate-300 dark:bg-slate-600'" @click="togglePlayback"><span class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow" :class="member.playbackAllowed ? 'left-[22px]' : 'left-0.5'"></span></button></div></div>
  <p class="mt-2 text-xs leading-5 text-slate-500">直接修改 Emby 权限，影响所有客户端；不改变登录及下载权限。</p>
  <p v-if="member.disabled" class="mt-2 text-xs text-amber-700">{{ member.childRevoked ? '子账号共享已永久撤销，不能恢复。' : member.childDisabled ? '所有者已停用子账号登录。' : 'Emby 账号已停用，请先在 Emby 中恢复。' }}</p>
  <button v-if="permissionUnknown || memberPermissionUnknown()" type="button" :disabled="locked" class="mt-2 text-left text-sm text-blue-600" @click="refreshPermissions">刷新 Emby 权限</button>
  <p v-if="error && !open" role="alert" class="mt-2 text-sm text-red-600">{{ error }}</p>
  <p v-if="notice && !open" role="status" class="mt-2 text-xs text-emerald-700">{{ notice }}</p>
  <dl class="mb-5 mt-5 space-y-3 text-sm"><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">播放来源</dt><dd class="truncate text-slate-800 dark:text-slate-200" :title="sourceLabel">{{ sourceLabel }}</dd></div><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">网盘状态</dt><dd class="truncate" :title="state" :class="props.member.driveStatus?.state === 'ready' ? 'text-emerald-600 dark:text-emerald-400' : props.member.driveStatus?.state === 'pending' ? 'text-slate-500' : 'text-amber-700 dark:text-amber-300'"> {{ state }}</dd></div><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">{{ member.playbackOnly ? '所有者' : '子账号' }}</dt><dd class="truncate text-slate-600 dark:text-slate-300">{{ member.playbackOnly ? member.ownerName || '所有者不可用' : member.administrator ? '不适用' : member.childLimit ? `${usage} / ${member.childLimit} 个` : member.childUsed ? `${usage} 个 · 已停止新建` : '未开放' }}</dd></div></dl>
  <p class="mb-3 min-h-4 text-xs text-slate-500"><span v-if="checkedTime">最近检查：<time :datetime="member.driveStatus?.checkedAt">{{ checkedTime }}</time></span></p>
  <footer class="mt-auto flex items-center gap-4 border-t border-slate-100 pt-4 dark:border-slate-800">
   <p class="min-w-0 flex-1 text-slate-500">
    <span class="mb-1 block text-[11px]">Emby ID</span>
    <span class="block select-text break-all font-mono text-[11px] leading-relaxed">{{ member.embyId }}</span>
   </p>
   <button ref="trigger" :disabled="disabled" class="shrink-0 rounded-lg bg-blue-50 px-4 py-2 text-sm font-medium text-blue-700 transition hover:bg-blue-100 disabled:opacity-40 dark:bg-blue-500/10 dark:text-blue-300 dark:hover:bg-blue-500/20" @click="show">管理用户 <svg aria-hidden="true" class="ml-1 inline h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></button>
  </footer>
 </article>
 <Teleport to="body">
  <dialog v-if="open" ref="dialog" :aria-labelledby="titleID" class="member-dialog" @cancel.prevent="close()" @click="event => { if (event.target === dialog) close() }">
   <div class="flex min-h-0 flex-1 flex-col">
    <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 px-6 py-5 dark:border-slate-800"><div><h2 :id="titleID" class="text-lg font-semibold">{{ member.playbackOnly ? '管理播放子账号' : '管理用户' }}</h2><p class="mt-1 break-all text-sm text-slate-500">{{ member.embyName }} · {{ identity }}</p></div><button autofocus type="button" :disabled="busy" aria-label="关闭用户设置" class="rounded-lg px-2 text-2xl text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800" @click="close()"><svg aria-hidden="true" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m6 6 12 12M6 18 18 6" /></svg></button></header>
    <form :id="formID" class="min-h-0 flex-1 space-y-6 overflow-y-auto px-6 py-5" @submit.prevent="save">
     <p class="rounded-lg bg-slate-50 px-3 py-2 text-sm dark:bg-slate-800">{{ state }}</p>
     <p v-if="!member.driveStatus && (member.checkError || member.message)" class="text-sm text-amber-700 dark:text-amber-300">{{ member.checkError || member.message }}</p>
     <section v-if="!member.playbackOnly"><label class="block text-sm font-medium">播放来源<select aria-label="播放来源" v-model="source" :disabled="locked || member.disabled" class="field mt-2"><option value="primary">主网盘</option><optgroup label="共享小号"><option value="pool:">自动分配共享小号</option><option v-if="missingPool" :value="source" disabled>原小号不可用</option><option v-for="a in accounts" :key="a.id" :value="`pool:${a.id}`" :disabled="!a.playbackParentCid">{{ a.name }} · {{ a.userCount || 0 }} 人{{ !a.playbackParentCid ? '（未设目录）' : !a.configured ? '（未登录）' : '' }}</option></optgroup><option v-if="!member.administrator" value="personal">个人网盘（本人扫码）</option></select></label><p v-if="source === 'pool:'" class="hint">没有可用小号时使用主网盘。</p><p v-if="source === 'personal'" class="hint">{{ member.personalDrive?.connected ? '个人网盘已连接，由本人管理。' : '待用户在个人中心扫码并选择目录。' }}</p><p v-if="member.disabled" class="hint">Emby 已停用，恢复后可修改播放来源。</p></section>
     <section v-else class="space-y-2 text-sm"><p>播放来源：所有者个人网盘</p><p>所有者：{{ member.ownerName || member.ownerUserId }}</p><p class="hint">仅允许 Emby 播放，不允许网站登录。</p></section>
     <section v-if="!member.playbackOnly && !member.administrator" class="border-t border-slate-100 pt-5 dark:border-slate-800"><div class="flex items-center justify-between gap-4"><div><h3 class="text-sm font-medium">播放子账号</h3><p class="hint">已使用 {{ usage }} 个名额，不包含本人</p></div><button type="button" role="switch" aria-label="允许创建子账号" :aria-checked="allowChildren" :disabled="locked" class="relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-40" :class="allowChildren ? 'bg-blue-600' : 'bg-slate-300 dark:bg-slate-600'" @click="allowChildren = !allowChildren"><span class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all" :class="allowChildren ? 'left-[22px]' : 'left-0.5'"></span></button></div><label v-if="allowChildren" class="mt-4 block text-sm">子账号名额<div class="mt-2 flex w-40 overflow-hidden rounded-lg border border-slate-200 dark:border-slate-700"><button type="button" aria-label="减少子账号名额" :disabled="locked || childLimit <= 1" class="w-10 bg-slate-50 text-lg disabled:opacity-40 dark:bg-slate-800" @click="childLimit--"><svg aria-hidden="true" class="mx-auto h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14" /></svg></button><input v-model.number="childLimit" aria-label="子账号名额" type="number" required min="1" max="20" :disabled="locked" class="min-w-0 flex-1 bg-transparent py-2 text-center outline-none" /><button type="button" aria-label="增加子账号名额" :disabled="locked || childLimit >= 20" class="w-10 bg-slate-50 text-lg disabled:opacity-40 dark:bg-slate-800" @click="childLimit++"><svg aria-hidden="true" class="mx-auto h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14m-7-7v14" /></svg></button></div></label><p v-if="!allowChildren" class="hint">关闭后禁止继续新建，现有子账号保留。</p><p v-else-if="member.childUsed && childLimit < member.childUsed" class="mt-2 text-xs text-amber-700 dark:text-amber-300">名额少于已使用数量。现有账号保留，暂时不能新建。</p><button v-if="member.childUsed" type="button" :disabled="locked || changed" class="mt-3 text-sm text-blue-600 disabled:opacity-40 dark:text-blue-400" @click="viewChildren">查看子账号</button></section>
     <section class="border-t border-slate-100 pt-5 dark:border-slate-800"><h3 class="text-sm font-medium">媒体库与常规权限</h3><p class="hint">在 Emby 中管理。Cinema115 不会覆盖媒体库、分级、设备和下载权限。播放子账号的登录及播放状态由 Cinema115 管理；所有用户仅允许原文件播放。</p></section>
     <details class="border-t border-slate-100 pt-4 text-sm dark:border-slate-800"><summary class="cursor-pointer text-slate-500">更多操作</summary><p v-if="changed" class="hint">请先保存或取消修改，再执行以下操作。</p><div class="mt-3 flex flex-wrap gap-4"><button type="button" :disabled="locked || changed" class="text-blue-600 disabled:opacity-40" @click="mutate('/sync', {})">检查网盘</button></div></details>
    </form>
    <footer class="shrink-0 space-y-3 border-t border-slate-100 bg-white px-6 py-4 dark:border-slate-800 dark:bg-slate-900">
     <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
     <div v-if="discard" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"><p>有未保存的修改，是否放弃？</p><div class="mt-3 flex gap-4"><button @click="close(true)">放弃修改并关闭</button><button class="font-medium" @click="discard = false">继续编辑</button></div></div>
     <div v-else class="flex items-center justify-between gap-3"><span class="text-xs text-slate-500">{{ busy ? '正在处理…' : changed ? '有未保存的修改' : '修改后保存生效' }}</span><div class="flex shrink-0 gap-2"><button type="button" :disabled="busy" class="rounded-lg border border-slate-200 px-4 py-2 text-sm dark:border-slate-700" @click="close()">{{ changed ? '取消' : '关闭' }}</button><button :form="formID" type="submit" :disabled="locked || !changed" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-40">{{ busy ? '保存中…' : '保存更改' }}</button></div></div>
    </footer>
   </div>
  </dialog>
 </Teleport>
</template>

<style scoped>
.member-dialog { width: min(560px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 48px); margin: auto; padding: 0; overflow: hidden; border: 0; border-radius: 20px; background: white; color: rgb(var(--color-slate-800)); box-shadow: 0 24px 80px #0f172a33; }
.member-dialog[open] { display: flex; }
.member-dialog::backdrop { background: #0f172a70; backdrop-filter: blur(3px); }
.field { display: block; width: 100%; border: 1px solid rgb(var(--color-slate-200)); border-radius: 9px; background: transparent; padding: 10px 12px; font-size: 14px; }
.hint { margin-top: 6px; font-size: 12px; line-height: 1.7; color: rgb(var(--color-text-muted)); }
button:focus-visible, summary:focus-visible, input:focus-visible, select:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
input[type=number] { appearance: textfield; } input::-webkit-inner-spin-button { appearance: none; }
.dark .member-dialog { background: rgb(var(--color-slate-900)); color: rgb(var(--color-slate-200)); color-scheme: dark; }
.dark .field { border-color: rgb(var(--color-slate-700)); background: rgb(var(--color-slate-950)); }
.dark .member-dialog option, .dark .member-dialog optgroup { background: rgb(var(--color-slate-950)); color: rgb(var(--color-slate-200)); }
.dark .member-dialog .text-blue-600 { color: #60a5fa; }
.dark .member-dialog .text-red-600 { color: #fca5a5; }
@media (max-width: 639px) { .member-dialog { width: calc(100vw - 16px); height: calc(100dvh - 16px); max-height: calc(100dvh - 16px); border-radius: 16px; } }
@media (prefers-reduced-motion: reduce) { * { transition: none !important; } }
</style>
