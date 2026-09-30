<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import EmbyUserAvatar from './EmbyUserAvatar.vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; member: MemberPlayback; settings: MemberSettings; accounts: P115Account[]; disabled: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean] }>()
function selection(m: MemberPlayback) { return m.source === 'assigned' ? `pool:${m.assignedAccountId || ''}` : m.source || 'primary' }
const value = ref(selection(props.member)), busy = ref(false), error = ref(''), notice = ref(''), more = ref(false), showID = ref(false), copied = ref(false)
const lifecycle = new AbortController()
const changed = computed(() => value.value !== selection(props.member))
const locked = computed(() => busy.value || props.disabled)
const state = computed(() => memberStage(props.member, props.settings))
const normal = computed(() => state.value === '播放已开通')
const poolName = computed(() => props.accounts.find(a => a.id === props.member.assignedAccountId)?.name || '账号不可用')
const sourceLabel = computed(() => props.member.source === 'assigned' ? poolName.value : props.member.source === 'personal' ? '个人网盘' : '主网盘')
const missingPool = computed(() => value.value.startsWith('pool:') && value.value !== 'pool:' && !props.accounts.some(a => `pool:${a.id}` === value.value))
watch(changed, v => { emit('dirty', v); if (v) notice.value = '' }, { immediate: true })
watch(() => [props.member.source, props.member.assignedAccountId], () => { value.value = selection(props.member) })
async function mutate(suffix: string, body: object) {
 if (locked.value || props.member.disabled && suffix === '/source') return
 busy.value = true; emit('busy', true); error.value = ''; notice.value = ''
 try {
  const data = await memberRequest(props.token, lifecycle.signal, `/${encodeURIComponent(props.member.userId)}${suffix}`, body)
  const updated = (data.members as MemberPlayback[]).find(m => m.userId === props.member.userId)
  if (!updated) throw new Error('用户状态缺失，请刷新检查')
  if (suffix === '/source') value.value = selection(updated)
  emit('updated', { ...props.member, ...updated, disabled: props.member.disabled, imageTag: props.member.imageTag }, data.settings)
  notice.value = '已保存'; more.value = false
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败，请重试' }
 finally { busy.value = false; emit('busy', false) }
}
function save() {
 const pool = value.value.startsWith('pool:')
 void mutate('/source', { source: pool ? 'assigned' : value.value, accountId: pool ? value.value.slice(5) : '' })
}
async function copyID() {
 try { await navigator.clipboard.writeText(props.member.embyId); copied.value = true }
 catch { showID.value = true; notice.value = '请选择下方 ID 手动复制' }
}
onBeforeUnmount(() => { lifecycle.abort(); emit('dirty', false); emit('busy', false) })
</script>
<template>
 <article class="min-w-0 rounded-2xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
  <header class="flex items-start justify-between gap-3 border-b border-slate-100 p-4 dark:border-slate-800">
   <div class="flex min-w-0 items-center gap-3"><EmbyUserAvatar :token="token" :user-id="member.embyId" :name="member.embyName" :image-tag="member.imageTag" /><div class="min-w-0"><h2 class="break-all font-semibold text-slate-900 dark:text-slate-100">{{ member.embyName }}</h2><div class="mt-1.5 flex flex-wrap gap-1.5"><span class="rounded-md px-2 py-0.5 text-xs" :class="member.administrator ? 'bg-purple-50 text-purple-600 dark:bg-purple-400/10 dark:text-purple-300' : 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400'">{{ member.administrator ? '管理员' : '普通用户' }}</span><span v-if="member.skipLibrarySync || member.administrator" title="媒体库权限在 Emby 中设置" class="rounded-md bg-slate-100 px-2 py-0.5 text-xs text-slate-500 dark:bg-slate-800 dark:text-slate-400">不同步组</span></div></div></div>
   <span class="max-w-[40%] shrink-0 break-words rounded-lg bg-slate-50 px-2.5 py-2 text-right text-xs leading-5 text-slate-500 dark:bg-slate-800 dark:text-slate-400">当前使用<strong class="block font-medium text-slate-700 dark:text-slate-200">{{ sourceLabel }}</strong></span>
  </header>
  <form class="space-y-3 p-4" @submit.prevent="save">
   <label class="block text-sm text-slate-700 dark:text-slate-300">播放账号<select v-model="value" aria-label="播放账号" :disabled="locked || member.disabled" class="mt-2 block w-full min-w-0 rounded-xl border border-slate-200 bg-slate-50 px-3 py-2.5 text-sm disabled:opacity-50 dark:border-slate-700 dark:bg-slate-950"><option value="primary">主网盘（默认）</option><optgroup label="共享小号"><option value="pool:">自动分配共享小号</option><option v-if="missingPool" :value="value" disabled>原小号不可用</option><option v-for="a in accounts" :key="a.id" :value="`pool:${a.id}`" :disabled="!a.playbackParentCid">{{ a.name }} · {{ a.userCount || 0 }} 人使用{{ !a.playbackParentCid ? '（未设目录）' : !a.configured ? '（未登录）' : '' }}</option></optgroup><option v-if="!member.administrator" value="personal">个人网盘（本人扫码）</option></select></label>
   <p v-if="value === 'personal'" class="text-xs text-slate-500">{{ member.personalDrive?.connected ? '个人网盘已连接' : '待本人扫码' }}</p>
   <p v-if="changed && value === 'pool:'" class="text-xs text-slate-500">没有可用小号时使用主网盘。</p>
   <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-2 text-xs leading-5 text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
   <p v-else-if="!normal" class="text-xs leading-5 text-amber-700 dark:text-amber-300">{{ member.syncError || state }}</p>
   <div class="flex items-center justify-between gap-3 pt-1"><span role="status" class="text-xs" :class="changed ? 'text-amber-600 dark:text-amber-300' : 'text-slate-400'">{{ busy ? '正在保存…' : changed ? '未保存' : notice || (normal ? '播放已开通' : '') }}</span><button :disabled="locked || member.disabled || !changed" class="rounded-lg bg-blue-600 px-3 py-1.5 text-sm text-white disabled:opacity-40">保存</button></div>
  </form>
  <footer class="rounded-b-2xl border-t border-slate-100 bg-slate-50/70 px-4 py-2.5 text-xs dark:border-slate-800 dark:bg-slate-950/40">
   <div class="flex items-center justify-between"><button type="button" class="text-slate-400 hover:text-blue-600" :aria-expanded="showID" @click="showID = !showID">用户 ID</button><button type="button" class="rounded px-1 py-1 text-slate-500 hover:text-blue-600 dark:text-slate-400" :aria-expanded="more" @click="more = !more">更多 <span aria-hidden="true">{{ more ? '⌃' : '⌄' }}</span></button></div>
   <div v-if="showID" class="mt-2 flex items-start gap-2"><span class="min-w-0 flex-1 select-all break-all text-slate-400">{{ member.embyId }}</span><button type="button" class="shrink-0 text-blue-600" @click="copyID">{{ copied ? '已复制' : '复制' }}</button></div>
   <div v-if="more" class="mt-2 space-y-1 border-t border-slate-200 pt-2 dark:border-slate-700">
    <button type="button" v-if="!member.administrator" :disabled="locked || changed" class="block w-full rounded-lg px-2 py-2 text-left text-slate-600 hover:bg-slate-100 disabled:opacity-40 dark:text-slate-300 dark:hover:bg-slate-800" @click="mutate('/library-group', { skipLibrarySync: !member.skipLibrarySync })">{{ member.skipLibrarySync ? '移回默认组' : '加入不同步组' }}</button>
    <p v-if="member.skipLibrarySync && !member.administrator" class="px-2 text-xs text-slate-400">移回默认组会按统一设置更新媒体库。</p>
    <button type="button" :disabled="locked || changed" class="block w-full rounded-lg px-2 py-2 text-left text-slate-600 hover:bg-slate-100 disabled:opacity-40 dark:text-slate-300 dark:hover:bg-slate-800" @click="mutate('/sync', {})">重新同步</button>
    <button type="button" :disabled="locked || changed" class="block w-full rounded-lg px-2 py-2 text-left disabled:opacity-40" :class="member.adminBlocked ? 'text-blue-600' : 'text-red-600 dark:text-red-400'" @click="mutate('', { blocked: !member.adminBlocked })">{{ member.adminBlocked ? '恢复播放' : '停用播放' }}</button>
   </div>
  </footer>
 </article>
</template>
