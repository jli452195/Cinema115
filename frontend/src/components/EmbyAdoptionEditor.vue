<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { memberRequest, type MemberPlayback } from '../types/memberPlayback'
const props = defineProps<{ token: string; embyId: string; disabled: boolean }>()
const emit = defineEmits<{ linked: []; busy: [value: boolean]; dirty: [value: boolean] }>()
interface Preview { embyName: string; fingerprint: string; source: string; accountName: string; currentLibraries: string[]; newLibraries: string[]; currentDownload: boolean; newDownload: boolean }
const preview = ref<Preview>(), busy = ref(false), error = ref(''), confirmed = ref(false), websiteID = ref(''), inviteLink = ref('')
const users = ref<{ id: string; username: string; disabled: boolean }[]>([]), offset = ref(0), hasMore = ref(false)
const members = ref<MemberPlayback[]>([]), mode = ref('existing')
const eligibleUsers = computed(() => users.value.filter(user => !user.disabled && !members.value.some(m => m.userId === user.id && (m.embyId || m.accountId || m.stage !== 'unbound'))))
const selectedEligible = computed(() => eligibleUsers.value.some(user => user.id === websiteID.value))
const lifecycle = new AbortController()
watch(() => !!(confirmed.value || websiteID.value || inviteLink.value), value => emit('dirty', value))
watch(() => props.disabled, value => { if (value) reset() })
function reset() { preview.value = undefined; confirmed.value = false; websiteID.value = ''; inviteLink.value = ''; error.value = '' }
function setBusy(value: boolean) { busy.value = value; emit('busy', value) }
async function loadUsers() {
  const response = await fetch(`/api/admin/users?offset=${offset.value}`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  if (!response.ok) throw new Error('网站用户列表读取失败')
  const data = await response.json()
  if (!Array.isArray(data.users)) throw new Error('网站用户列表读取失败')
  users.value.push(...data.users); hasMore.value = data.hasMore; offset.value += 50
}
async function inspect() {
  if (busy.value || props.disabled) return
  setBusy(true); reset(); users.value = []; offset.value = 0; hasMore.value = false
  try {
    const [inspection, directory] = await Promise.all([
      memberRequest(props.token, lifecycle.signal, `/adoption/${encodeURIComponent(props.embyId)}`),
      memberRequest(props.token, lifecycle.signal, '/users'),
      loadUsers(),
    ])
    if (!Array.isArray(directory.members)) throw new Error('播放用户关联读取失败，请重试')
    members.value = directory.members; preview.value = inspection
  }
  catch (e) { error.value = e instanceof Error ? e.message : '读取失败' }
  finally { setBusy(false) }
}
async function link(invite: boolean) {
  if (busy.value || props.disabled || !confirmed.value || !preview.value || !invite && !selectedEligible.value || invite && inviteLink.value) return
  setBusy(true); error.value = ''
  try {
    const data = await memberRequest(props.token, lifecycle.signal, `/adoption/${encodeURIComponent(props.embyId)}`, { fingerprint: preview.value.fingerprint, websiteUserId: invite ? '' : websiteID.value, invite })
    if (invite) inviteLink.value = `${location.origin}/register#invite=${encodeURIComponent(data.invite.code)}`
    else emit('linked')
  } catch (e) { error.value = e instanceof Error ? e.message : '关联失败'; confirmed.value = false }
  finally { setBusy(false) }
}
async function more() {
  if (busy.value || props.disabled || !hasMore.value) return
  setBusy(true)
  try { await loadUsers() } catch (e) { error.value = e instanceof Error ? e.message : '读取失败' }
  finally { setBusy(false) }
}
onBeforeUnmount(() => { lifecycle.abort(); emit('dirty', false) })
</script>
<template>
  <section class="space-y-4 border-t border-slate-200 pt-5 text-sm dark:border-slate-700">
    <h3 class="font-semibold">网站账号关联</h3>
    <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">关联网站账号后，对方可登录“我的播放”，准备自己的 115 并主动切换。保留当前 Emby 账号和播放来源，第一版不开放已有账号的改名与改密码。</p>
    <p v-if="disabled" class="text-xs text-amber-700 dark:text-amber-300">请先保存或还原播放来源，再关联网站账号。</p>
    <button type="button" :disabled="busy || disabled" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="inspect">{{ busy ? '处理中…' : preview ? '重新检查关联与权限变化' : '关联网站账号' }}</button>
    <p v-if="error" role="alert" class="text-red-600 dark:text-red-300">{{ error }}</p>
    <template v-if="preview">
      <div class="space-y-2 rounded-lg bg-slate-50 p-3 text-xs leading-6 dark:bg-slate-800"><p>Emby：{{ preview.embyName }}</p><p>保留播放来源：{{ preview.source === 'primary' ? '主账号' : '管理员小号' }} · {{ preview.accountName }}</p><p>原媒体库：{{ preview.currentLibraries.join('、') || '无' }}</p><p>接管后媒体库：{{ preview.newLibraries.join('、') || '无' }}</p><p>下载权限：{{ preview.currentDownload ? '允许' : '禁止' }} → {{ preview.newDownload ? '允许' : '禁止' }}</p><p>关联后纳入自助用户策略，禁用转码、重新封装、删除、直播、字幕管理及控制其他用户；权限确认前暂不可播放。</p></div>
      <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">自助策略还会允许远程访问和所有设备、在登录列表隐藏账号，并取消远程客户端码率限制。后续全局自助设置变化会同步到该账号。</p>
      <fieldset :disabled="busy || disabled" class="space-y-4 disabled:opacity-60">
        <label class="flex items-start gap-2 text-xs leading-6"><input v-model="confirmed" type="checkbox" class="mt-1" />我确认将此 Emby 账号交给指定网站用户，并接受以上权限变化。</label>
        <div class="flex flex-wrap gap-4"><label class="flex items-center gap-2"><input v-model="mode" type="radio" value="existing" />已有网站账号</label><label class="flex items-center gap-2"><input v-model="mode" type="radio" value="invite" />邀请新用户注册</label></div>
        <form v-if="mode === 'existing'" class="space-y-3" @submit.prevent="link(false)">
          <label class="block">关联已有网站账号<select v-model="websiteID" required class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-2 dark:border-slate-700 dark:bg-slate-950"><option value="">请选择该 Emby 用户的网站账号</option><option v-for="user in eligibleUsers" :key="user.id" :value="user.id">{{ user.username }}</option></select></label>
          <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">仅列出未停用、未关联 Emby 且未绑定个人网盘或开始注册的网站账号。</p>
          <p v-if="!eligibleUsers.length" class="text-xs text-slate-500 dark:text-slate-400">{{ hasMore ? '当前页没有可关联账号，可加载更多。' : '没有可关联的网站账号，可邀请新用户注册。' }}</p>
          <button v-if="hasMore" type="button" class="text-blue-600 dark:text-blue-300" @click="more">加载更多网站用户</button>
          <button :disabled="!confirmed || !selectedEligible" class="rounded-lg bg-blue-600 px-3 py-2 text-white disabled:opacity-40">关联此网站用户</button>
        </form>
        <button v-else type="button" :disabled="!confirmed || !!inviteLink" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="link(true)">生成专属注册邀请（一天有效）</button>
      </fieldset>
      <div v-if="inviteLink" class="space-y-2"><label class="block text-xs">专属邀请链接<input :value="inviteLink" readonly class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-2 dark:border-slate-700 dark:bg-slate-950" @focus="($event.target as HTMLInputElement).select()" /></label><p class="text-xs leading-6 text-slate-500 dark:text-slate-400">仅交给该 Emby 账号本人，一次使用。可在注册邀请页撤销。注册时若原用户、来源或权限已变化，需要重新生成邀请。</p></div>
    </template>
  </section>
</template>
