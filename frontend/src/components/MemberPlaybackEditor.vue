<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; member: MemberPlayback; settings: MemberSettings; accounts: P115Account[]; disabled: boolean }>()
const emit = defineEmits<{ updated: [member: MemberPlayback, settings: MemberSettings]; busy: [value: boolean]; dirty: [value: boolean] }>()
const source = ref(props.member.source), accountId = ref(props.member.assignedAccountId || ''), directoryId = ref('')
const busy = ref(false), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const changed = computed(() => source.value !== props.member.source || (source.value === 'assigned' && accountId.value !== props.member.assignedAccountId))
watch(changed, value => emit('dirty', value), { immediate: true })
async function mutate(suffix: string, body: object) {
  if (busy.value || props.disabled) return
  busy.value = true; emit('busy', true); error.value = ''; notice.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, `/${encodeURIComponent(props.member.userId)}${suffix}`, body)
    const updated = (data.members as MemberPlayback[]).find(m => m.userId === props.member.userId)
    if (!updated) throw new Error('用户状态缺失，请刷新检查')
    emit('updated', { ...updated, websiteName: props.member.websiteName, websiteDisabled: props.member.websiteDisabled }, data.settings)
    if (suffix === '/source') { source.value = updated.source; accountId.value = updated.assignedAccountId || '' }
    notice.value = '已保存'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = false; emit('busy', false) }
}
onBeforeUnmount(() => { lifecycle.abort(); emit('dirty', false) })
</script>
<template>
  <div class="space-y-6 text-sm text-slate-700 dark:text-slate-300">
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
    <div class="space-y-2"><p class="font-medium">{{ memberStage(member, settings) }}</p><p class="leading-6 text-slate-500 dark:text-slate-400">{{ member.message }}</p><p v-if="disabled" class="text-amber-700 dark:text-amber-300">Emby 服务配置已变化，请先检查服务关联。</p></div>
    <form class="space-y-3" @submit.prevent="mutate('/source', { source, accountId: source === 'assigned' ? accountId : '' })">
      <h3 class="font-semibold">播放授权</h3>
      <fieldset :disabled="busy || disabled" class="space-y-3 disabled:opacity-50">
        <label class="block">播放账号来源<select v-model="source" class="mt-2 block w-full rounded-lg border border-slate-200 bg-slate-50 p-2.5 dark:border-slate-700 dark:bg-slate-950"><option value="personal">本人网盘</option><option value="primary">主账号（管理员授权）</option><option value="assigned">管理员小号</option></select></label>
        <label v-if="source === 'assigned'" class="block">选择小号<select v-model="accountId" required class="mt-2 block w-full rounded-lg border border-slate-200 bg-slate-50 p-2.5 dark:border-slate-700 dark:bg-slate-950"><option value="">请选择已配置播放目录的小号</option><option v-if="accountId && !accounts.some(a => a.id === accountId)" :value="accountId" disabled>原账号已删除</option><option v-for="account in accounts" :key="account.id" :value="account.id" :disabled="!account.playbackParentCid">{{ account.name }}{{ !account.playbackParentCid ? '（未设置目录）' : account.configured ? '' : '（未登录）' }}</option></select></label>
        <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">待开通用户不会自动使用主账号。绑定账号不可用时保留绑定并暂停播放。</p>
        <button :disabled="!changed" class="rounded-lg bg-blue-600 px-4 py-2 text-white disabled:opacity-40">保存播放来源</button>
      </fieldset>
    </form>
    <RouterLink to="/helper115" class="inline-block text-blue-600 dark:text-blue-300">去 115 账号管理新增小号 →</RouterLink>
    <section class="space-y-2 border-t border-slate-200 pt-5 dark:border-slate-700">
      <h3 class="font-semibold">个人网盘</h3>
      <p>{{ !member.personalDrive?.uid ? '未绑定' : member.source === 'personal' ? '用于当前播放' : '闲置保留' }}</p>
      <template v-if="member.personalDrive?.uid"><p>UID：{{ member.personalDrive.uid }} · {{ member.personalDrive.connected ? '已保存登录凭据' : '待重新登录' }}</p><p v-if="member.personalDrive.directoryId" class="break-all">个人目录：{{ member.personalDrive.directoryId }}</p><p class="text-xs leading-5 text-slate-500 dark:text-slate-400">归属始终保留在该用户下。用户可在“我的播放”中重新扫码或清除凭据，网盘文件保留。</p></template>
    </section>
    <section class="space-y-3 border-t border-slate-200 pt-5 dark:border-slate-700">
      <h3 class="font-semibold">权限与异常</h3>
      <p>全局策略：{{ settings.libraryIds.length }} 个媒体库 · {{ settings.allowDownload ? '允许下载原文件' : '不允许下载' }} · 禁止转码</p>
      <p v-if="member.embyId" :class="member.syncError ? 'text-red-600 dark:text-red-300' : 'text-slate-500 dark:text-slate-400'">{{ member.syncError || (member.appliedRevision === settings.revision ? '权限已同步' : '权限待同步，暂时不可播放') }}</p>
      <button v-if="member.embyId" :disabled="busy || disabled" class="mr-4 text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="mutate('/sync', {})">检查并同步此用户</button>
      <button :disabled="busy || disabled" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="mutate('', { blocked: !member.adminBlocked })">{{ member.adminBlocked ? '解除播放停用' : '停用播放' }}</button>
      <form v-if="(member.source === 'personal' || member.allowPersonalSwitch) && member.directoryStarted && !member.directoryId" class="space-y-2" @submit.prevent="mutate('', { directoryId })"><label class="block">确认播放目录编号<input v-model="directoryId" required pattern="[0-9]+" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-2 dark:border-slate-700 dark:bg-slate-950" /></label><p class="text-xs">请先在 115 核查已创建的专用目录。</p><button :disabled="busy || disabled" class="text-blue-600 dark:text-blue-300">确认目录</button></form>
      <button v-if="member.stage === 'creating' && !member.embyId" :disabled="busy || disabled" class="block text-amber-700 dark:text-amber-300" @click="mutate('', { retry: true })">检查 Emby，未创建时允许重试</button>
    </section>
  </div>
</template>
