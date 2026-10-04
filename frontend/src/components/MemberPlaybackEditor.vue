<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
const props = defineProps<{ token: string; member: MemberPlayback; disabled: boolean; deleteDisabled?: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean]; children: [owner: string]; delete: [id: string] }>()
interface Values { source: string; accountId: string; childLimit: number }
function values(m: MemberPlayback): Values { return { source: m.source || 'primary', accountId: '', childLimit: m.childLimit || 0 } }
const initial = ref(values(props.member)), source = ref(props.member.source || 'primary')
const childLimit = ref(props.member.childLimit || 0), action = ref(''), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const locked = computed(() => !!action.value || props.disabled)
const draft = computed<Values>(() => ({ source: source.value, accountId: '', childLimit: Number(childLimit.value) }))
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(initial.value))
const state = computed(() => props.member.childRevoked ? '共享已撤销' : props.member.childDisabled ? '所有者已停用登录' : props.member.childPaused ? '所有者已暂停播放' : props.member.driveStatus?.message || memberStage(props.member))
const identity = computed(() => props.member.playbackOnly ? '播放子账号' : props.member.administrator ? '管理员' : props.member.createdByCinema ? '邀请注册用户' : '已有 Emby 用户')
const usage = computed(() => props.member.childUsed === undefined ? '未读取' : String(props.member.childUsed))
const checkedTime = computed(() => {
 const value = props.member.driveStatus?.checkedAt
 if (!value) return '尚未检查'
 const date = new Date(value)
 return Number.isNaN(date.getTime()) ? '尚未检查' : date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
})
watch(changed, value => emit('dirty', value), { flush: 'sync' })
watch(() => props.member, member => { if (!changed.value) reset(member) })
function reset(member: MemberPlayback) {
 initial.value = values(member); source.value = member.source || 'primary'; childLimit.value = member.childLimit || 0
}
function cancel() { reset(props.member); error.value = ''; notice.value = '' }
async function mutate(suffix: string, body: object, save = false) {
 if (locked.value) return
 action.value = save ? 'save' : 'check'; emit('busy', true); error.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, `/${encodeURIComponent(props.member.userId)}${suffix}`, body)
  if (lifecycle.signal.aborted) return
  const updated = (data.members as MemberPlayback[]).find(m => m.userId === props.member.userId)
  if (!updated) throw new Error('状态未返回，请刷新核查；请勿重复提交')
  const merged = { ...props.member, ...updated, disabled: props.member.disabled, imageTag: props.member.imageTag }
  // Health checks retain both the draft and its original write baseline.
  if (save) reset(merged)
  emit('updated', merged, data.settings)
  notice.value = save ? '设置已保存。' : '已更新网盘检查结果。'
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败，请重试' }
 finally { action.value = ''; emit('busy', false) }
}
function save() { if (changed.value) void mutate('/settings', { ...draft.value, expected: initial.value }, true) }
onBeforeUnmount(() => { lifecycle.abort(); emit('dirty', false); emit('busy', false) })
</script>

<template>
 <article class="user-card flex h-full min-w-0 flex-col gap-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
  <header class="flex items-center gap-3"><EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="member.imageTag" /><div class="min-w-0 flex-1"><h2 class="truncate font-semibold text-slate-900 dark:text-slate-100" :title="member.embyName">{{ member.embyName }}</h2><p class="mt-1 text-xs text-slate-500">{{ identity }}</p></div></header>
  <section class="rounded-xl bg-slate-50 p-3 dark:bg-slate-800/60" aria-label="网盘状态">
   <div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="break-words text-sm" :class="member.driveStatus?.state === 'ready' ? 'text-emerald-700 dark:text-emerald-400' : 'text-slate-600 dark:text-slate-300'">{{ state }}</p><p class="mt-1 text-xs text-slate-500">最近检查：<time :datetime="member.driveStatus?.checkedAt">{{ checkedTime }}</time></p></div><button type="button" :disabled="locked" class="shrink-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-blue-700 disabled:opacity-40 dark:border-slate-600 dark:bg-slate-900 dark:text-blue-300" @click="mutate('/sync', {})">{{ action === 'check' ? '检查中…' : '检查网盘' }}</button></div>
   <p v-if="changed" class="mt-2 text-xs text-slate-500">检查使用已保存配置，不影响下方未保存的修改。</p>
  </section>
  <form class="flex flex-1 flex-col gap-4" @submit.prevent="save">
   <section v-if="!member.playbackOnly"><label class="block text-sm font-medium">播放来源<select v-model="source" aria-label="播放来源" :disabled="locked || member.disabled" class="field mt-2"><option value="primary">主网盘</option><option value="assigned">动态共享小号池</option><option v-if="!member.administrator" value="personal">个人网盘（本人扫码）</option></select></label><p v-if="source === 'assigned'" class="hint">播放时自动分配健康小号；全池不可用时暂停新取链。</p><p v-if="source === 'personal'" class="hint">{{ member.personalDrive?.connected ? '个人网盘已连接，由本人管理。' : '待用户在个人中心扫码并选择目录。' }}</p><p v-if="member.disabled" class="hint">Emby 已停用，恢复后可修改播放来源。</p></section>
   <section v-else class="space-y-2 text-sm"><p>播放来源：所有者个人网盘</p><p class="break-words">所有者：{{ member.ownerName || member.ownerUserId }}</p><p class="hint">仅允许 Emby 播放，不允许网站登录。</p></section>
   <section v-if="!member.playbackOnly && !member.administrator" class="border-t border-slate-100 pt-4 dark:border-slate-800">
    <div class="flex flex-wrap items-center justify-between gap-2"><label class="text-sm font-medium">子账号名额<input v-model.number="childLimit" aria-label="子账号名额" type="number" required min="0" max="20" step="1" :disabled="locked" class="ml-3 w-20 rounded-lg border border-slate-200 bg-transparent px-2 py-2 dark:border-slate-700" /></label><button v-if="member.childUsed" type="button" :disabled="locked" class="text-sm text-blue-700 disabled:opacity-40 dark:text-blue-300" @click="emit('children', member.userId)">查看子账号</button></div>
    <p class="hint">已使用 {{ usage }} / {{ member.childLimit || 0 }} 个名额，不包含本人。填 0 停止新建，已有子账号保留。</p>
    <p v-if="member.childUsed && childLimit < member.childUsed" class="mt-2 text-xs text-amber-700 dark:text-amber-300">设置少于已使用数量，保存后暂时不能新建。</p>
   </section>
   <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-300">{{ error }}</p><p v-if="notice" role="status" class="text-xs text-emerald-700 dark:text-emerald-400">{{ notice }}</p>
   <footer class="mt-auto space-y-3 border-t border-slate-100 pt-4 dark:border-slate-800">
    <div class="flex flex-wrap items-center justify-between gap-3"><button v-if="!member.administrator" type="button" :disabled="locked || changed || deleteDisabled" class="rounded-lg px-1 py-2 text-sm text-red-700 disabled:opacity-40 dark:text-red-300" @click="emit('delete', member.userId)">删除用户</button><div v-if="changed" class="ml-auto flex gap-2"><button type="button" :disabled="locked" class="rounded-lg border border-slate-200 px-3 py-2 text-sm disabled:opacity-40 dark:border-slate-700" @click="cancel">取消修改</button><button type="submit" :disabled="locked" class="rounded-lg bg-blue-600 px-3 py-2 text-sm text-white disabled:opacity-40">{{ action === 'save' ? '保存中…' : '保存更改' }}</button></div><span v-else class="ml-auto text-xs text-slate-500">{{ member.playbackOnly ? '共享状态由所有者管理' : '修改后保存生效' }}</span></div>
    <p v-if="changed" class="text-xs text-amber-700 dark:text-amber-300">有未保存的修改；删除前请先保存或取消。</p>
    <p class="select-text break-all font-mono text-[11px] text-slate-400">Emby ID：{{ member.embyId }}</p>
   </footer>
  </form>
 </article>
</template>

<style scoped>
.field { display: block; width: 100%; border: 1px solid rgb(var(--color-slate-200)); border-radius: 9px; background: transparent; padding: 10px 12px; font-size: 14px; }
.hint { margin-top: 6px; font-size: 12px; line-height: 1.7; color: rgb(var(--color-text-muted)); }
button:focus-visible, input:focus-visible, select:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
.dark .field { border-color: rgb(var(--color-slate-700)); background: rgb(var(--color-slate-950)); }
.dark option { background: rgb(var(--color-slate-950)); color: rgb(var(--color-slate-200)); }
</style>
