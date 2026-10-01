<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; member: MemberPlayback; settings: MemberSettings; accounts: P115Account[]; disabled: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean]; children: [owner: string] }>()
interface Values { source: string; accountId: string; childLimit: number; skipLibrarySync: boolean }
function values(m: MemberPlayback): Values { return { source: m.source || 'primary', accountId: m.assignedAccountId || '', childLimit: m.childLimit || 0, skipLibrarySync: !!m.skipLibrarySync } }
const initial = ref(values(props.member)), source = ref(''), childLimit = ref(3), allowChildren = ref(false), skipSync = ref(false)
const open = ref(false), busy = ref(false), error = ref(''), notice = ref(''), discard = ref(false), confirmBlock = ref(false)
const dialog = ref<HTMLDialogElement>(), trigger = ref<HTMLButtonElement>()
const titleID = useId(), formID = useId()
const lifecycle = new AbortController()
let previousOverflow = ''
const locked = computed(() => busy.value || props.disabled)
const draft = computed<Values>(() => ({ source: source.value.startsWith('pool:') ? 'assigned' : source.value, accountId: source.value.startsWith('pool:') ? source.value.slice(5) : '', childLimit: allowChildren.value ? Number(childLimit.value) : 0, skipLibrarySync: skipSync.value }))
const changed = computed(() => open.value && JSON.stringify(draft.value) !== JSON.stringify(initial.value))
const state = computed(() => props.member.childRevoked ? '共享已撤销' : props.member.adminBlocked ? '管理员已停用' : props.member.childDisabled ? '所有者已停用登录' : props.member.childPaused ? '所有者已暂停播放' : memberStage(props.member, props.settings))
const sourceLabel = computed(() => props.member.playbackOnly ? '所有者个人网盘' : props.member.source === 'assigned' ? props.accounts.find(a => a.id === props.member.assignedAccountId)?.name || '共享小号不可用' : props.member.source === 'personal' ? '个人网盘' : '主网盘')
const identity = computed(() => props.member.playbackOnly ? '播放子账号' : props.member.administrator ? '管理员' : '普通用户')
const usage = computed(() => props.member.childUsed === undefined ? '—' : String(props.member.childUsed))
const missingPool = computed(() => source.value.startsWith('pool:') && source.value !== 'pool:' && !props.accounts.some(a => `pool:${a.id}` === source.value))
watch(changed, value => { emit('dirty', value); discard.value = false; confirmBlock.value = false })
function reset(m: MemberPlayback) {
 initial.value = values(m); source.value = m.source === 'assigned' ? `pool:${m.assignedAccountId || ''}` : m.source || 'primary'
 allowChildren.value = (m.childLimit || 0) > 0; childLimit.value = m.childLimit || 3; skipSync.value = !!m.skipLibrarySync
}
async function show() {
 reset(props.member); error.value = ''; notice.value = ''; discard.value = false; confirmBlock.value = false
 previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; open.value = true
 await nextTick(); dialog.value?.showModal()
}
function close(force = false) {
 if (busy.value) return
 if (changed.value && !force) { discard.value = true; confirmBlock.value = false; return }
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
  emit('updated', merged, data.settings); reset(merged); confirmBlock.value = false
  notice.value = save ? (updated.appliedRevision !== data.settings.revision ? '设置已保存，播放权限正在同步。' : '设置已保存。') : '操作已完成。'
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败，请重试' }
 finally { busy.value = false; emit('busy', false) }
}
function save() { if (changed.value) void mutate('/settings', { ...draft.value, expected: initial.value }, true) }
function viewChildren() { if (changed.value) return; close(); emit('children', props.member.userId) }
onBeforeUnmount(() => { lifecycle.abort(); if (open.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }; emit('dirty', false); emit('busy', false) })
</script>

<template>
 <article class="user-card flex min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
  <header class="flex items-center gap-3"><EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="member.imageTag" /><div class="min-w-0 flex-1"><h2 class="truncate font-semibold text-slate-900 dark:text-slate-100" :title="member.embyName">{{ member.embyName }}</h2><p class="mt-1 text-xs text-slate-500">{{ identity }}</p></div></header>
  <dl class="mt-5 space-y-3 text-sm"><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">播放来源</dt><dd class="truncate text-slate-800 dark:text-slate-200" :title="sourceLabel">{{ sourceLabel }}</dd></div><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">播放状态</dt><dd class="truncate" :title="state" :class="state === '播放已开通' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-300'"><span aria-hidden="true">●</span> {{ state }}</dd></div><div class="flex justify-between gap-4"><dt class="shrink-0 text-slate-500">{{ member.playbackOnly ? '所有者' : '子账号' }}</dt><dd class="truncate text-slate-600 dark:text-slate-300">{{ member.playbackOnly ? member.ownerName || '所有者不可用' : member.administrator ? '不适用' : member.childLimit ? `${usage} / ${member.childLimit} 个` : member.childUsed ? `${usage} 个 · 已停止新建` : '未开放' }}</dd></div></dl>
  <footer class="mt-5 flex items-center gap-4 border-t border-slate-100 pt-4 dark:border-slate-800">
   <p class="min-w-0 flex-1 text-slate-500">
    <span class="mb-1 block text-[11px]">Emby ID</span>
    <span class="block select-text break-all font-mono text-[11px] leading-relaxed">{{ member.embyId }}</span>
   </p>
   <button ref="trigger" :disabled="disabled" class="shrink-0 rounded-lg bg-blue-50 px-4 py-2 text-sm font-medium text-blue-700 transition hover:bg-blue-100 disabled:opacity-40 dark:bg-blue-500/10 dark:text-blue-300 dark:hover:bg-blue-500/20" @click="show">管理用户 <span aria-hidden="true" class="ml-1">→</span></button>
  </footer>
 </article>
 <Teleport to="body">
  <dialog v-if="open" ref="dialog" :aria-labelledby="titleID" class="member-dialog" @cancel.prevent="close()" @click="event => { if (event.target === dialog) close() }">
   <div class="flex min-h-0 flex-1 flex-col">
    <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 px-6 py-5 dark:border-slate-800"><div><h2 :id="titleID" class="text-lg font-semibold">{{ member.playbackOnly ? '管理播放子账号' : '管理用户' }}</h2><p class="mt-1 break-all text-sm text-slate-500">{{ member.embyName }} · {{ identity }}</p></div><button autofocus type="button" :disabled="busy" aria-label="关闭用户设置" class="rounded-lg px-2 text-2xl text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800" @click="close()">×</button></header>
    <form :id="formID" class="min-h-0 flex-1 space-y-6 overflow-y-auto px-6 py-5" @submit.prevent="save">
     <p class="rounded-lg bg-slate-50 px-3 py-2 text-sm dark:bg-slate-800">{{ state }}</p>
     <section v-if="!member.playbackOnly"><label class="block text-sm font-medium">播放来源<select aria-label="播放来源" v-model="source" :disabled="locked || member.disabled" class="field mt-2"><option value="primary">主网盘</option><optgroup label="共享小号"><option value="pool:">自动分配共享小号</option><option v-if="missingPool" :value="source" disabled>原小号不可用</option><option v-for="a in accounts" :key="a.id" :value="`pool:${a.id}`" :disabled="!a.playbackParentCid">{{ a.name }} · {{ a.userCount || 0 }} 人{{ !a.playbackParentCid ? '（未设目录）' : !a.configured ? '（未登录）' : '' }}</option></optgroup><option v-if="!member.administrator" value="personal">个人网盘（本人扫码）</option></select></label><p v-if="source === 'pool:'" class="hint">没有可用小号时使用主网盘。</p><p v-if="source === 'personal'" class="hint">{{ member.personalDrive?.connected ? '个人网盘已连接，由本人管理。' : '待用户在个人中心扫码并选择目录。' }}</p><p v-if="member.disabled" class="hint">Emby 已停用，恢复后可修改播放来源。</p></section>
     <section v-else class="space-y-2 text-sm"><p>播放来源：所有者个人网盘</p><p>所有者：{{ member.ownerName || member.ownerUserId }}</p><p class="hint">仅允许 Emby 播放，不允许网站登录。</p></section>
     <section v-if="!member.playbackOnly && !member.administrator" class="border-t border-slate-100 pt-5 dark:border-slate-800"><div class="flex items-center justify-between gap-4"><div><h3 class="text-sm font-medium">播放子账号</h3><p class="hint">已使用 {{ usage }} 个名额，不包含本人</p></div><button type="button" role="switch" aria-label="允许创建子账号" :aria-checked="allowChildren" :disabled="locked" class="relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-40" :class="allowChildren ? 'bg-blue-600' : 'bg-slate-300 dark:bg-slate-600'" @click="allowChildren = !allowChildren"><span class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all" :class="allowChildren ? 'left-[22px]' : 'left-0.5'"></span></button></div><label v-if="allowChildren" class="mt-4 block text-sm">子账号名额<div class="mt-2 flex w-40 overflow-hidden rounded-lg border border-slate-200 dark:border-slate-700"><button type="button" aria-label="减少子账号名额" :disabled="locked || childLimit <= 1" class="w-10 bg-slate-50 text-lg disabled:opacity-40 dark:bg-slate-800" @click="childLimit--">−</button><input v-model.number="childLimit" aria-label="子账号名额" type="number" required min="1" max="20" :disabled="locked" class="min-w-0 flex-1 bg-transparent py-2 text-center outline-none" /><button type="button" aria-label="增加子账号名额" :disabled="locked || childLimit >= 20" class="w-10 bg-slate-50 text-lg disabled:opacity-40 dark:bg-slate-800" @click="childLimit++">+</button></div></label><p v-if="!allowChildren" class="hint">关闭后禁止继续新建，现有子账号保留。</p><p v-else-if="member.childUsed && childLimit < member.childUsed" class="mt-2 text-xs text-amber-700 dark:text-amber-300">名额少于已使用数量。现有账号保留，暂时不能新建。</p><button v-if="member.childUsed" type="button" :disabled="locked || changed" class="mt-3 text-sm text-blue-600 disabled:opacity-40 dark:text-blue-400" @click="viewChildren">查看子账号 →</button></section>
     <section class="border-t border-slate-100 pt-5 dark:border-slate-800"><label class="block text-sm font-medium">媒体库权限<select aria-label="媒体库权限" v-model="skipSync" :disabled="locked || member.administrator" class="field mt-2"><option :value="false">默认同步组</option><option :value="true">不同步组</option></select></label><p class="hint">{{ member.administrator ? '管理员固定属于不同步组，在 Emby 中管理媒体库。' : skipSync ? '媒体库权限在 Emby 中单独设置。' : '按管理员统一配置同步媒体库。' }}</p></section>
     <details class="border-t border-slate-100 pt-4 text-sm dark:border-slate-800"><summary class="cursor-pointer text-slate-500">更多操作</summary><p v-if="changed" class="hint">请先保存或取消修改，再执行以下操作。</p><div class="mt-3 flex flex-wrap gap-4"><button type="button" :disabled="locked || changed" class="text-blue-600 disabled:opacity-40" @click="mutate('/sync', {})">重新同步权限</button><button type="button" :disabled="locked || changed || member.childRevoked" class="text-red-600 disabled:opacity-40" @click="confirmBlock = true">{{ member.adminBlocked ? '解除管理员停用' : '停用播放' }}</button></div></details>
    </form>
    <footer class="shrink-0 space-y-3 border-t border-slate-100 bg-white px-6 py-4 dark:border-slate-800 dark:bg-slate-900">
     <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error }}</p><p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
     <div v-if="discard" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"><p>有未保存的修改，是否放弃？</p><div class="mt-3 flex gap-4"><button @click="close(true)">放弃修改并关闭</button><button class="font-medium" @click="discard = false">继续编辑</button></div></div>
     <div v-else-if="confirmBlock" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"><p>{{ member.adminBlocked ? '解除管理员停用后，仍需满足网盘及其他播放条件。' : member.playbackOnly ? '停用后拒绝此子账号后续登录和播放请求。' : '停用后拒绝此用户及其共享子账号的新播放请求。' }}</p><div class="mt-3 flex gap-4"><button :disabled="busy" @click="mutate('', { blocked: !member.adminBlocked })">确认{{ member.adminBlocked ? '解除停用' : '停用' }}</button><button :disabled="busy" @click="confirmBlock = false">取消操作</button></div></div>
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
