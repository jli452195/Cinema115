<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import MemberPlaybackEditor from '../components/MemberPlaybackEditor.vue'
import EmbyAdoptionEditor from '../components/EmbyAdoptionEditor.vue'
import { memberRequest, memberStage, type MemberPlayback as Member, type MemberSettings } from '../types/memberPlayback'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import EmbyUserAvatar from '../components/EmbyUserAvatar.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Binding { userId: string; userName: string; accountId: string }
interface User { Id: string; Name: string; PrimaryImageTag?: string; Policy?: { IsDisabled?: boolean; IsAdministrator?: boolean } }
const members = ref<Record<string, Member>>({})
const settings = ref<MemberSettings>({ enabled: false, revision: 0, libraryIds: [], allowDownload: false })
const selected = ref(''), drawer = ref<HTMLDialogElement>(), memberBusy = ref(false), memberDirty = ref(false)
const confirmClose = ref(false)
async function adoptionLinked() {
  memberBusy.value = false; memberDirty.value = false; closeDrawer(true)
  await refresh(true)
}
const route = useRoute()
const selectedUser = computed(() => cards.value.find(u => u.Id === selected.value))
function memberKey(m: Member) { return m.embyId || 'pending:' + m.userId }
async function openUser(id: string) { selected.value = id; await nextTick(); drawer.value?.showModal() }
function closeDrawer(discard = false) {
  if (saving.value) return
  if (!discard && (memberDirty.value || drafts.value[selected.value] && dirty(drafts.value[selected.value]))) { confirmClose.value = true; return }
  if (drafts.value[selected.value]) drafts.value[selected.value] = makeDraft(selected.value)
  drawer.value?.close(); selected.value = ''; memberDirty.value = false; confirmClose.value = false
}
function updateMember(member: Member, value: MemberSettings) {
  const oldKey = selected.value, key = memberKey(member)
  if (oldKey !== key) delete members.value[oldKey]
  members.value[key] = member; selected.value = key; settings.value = value
}

interface Draft { accountId: string; original: string; busy: boolean; error: string; notice: string }
const config = ref({ embyUrl: '', bindings: [] as Binding[] })
const users = ref<User[]>([]), accounts = ref<P115Account[]>([]), drafts = ref<Record<string, Draft>>({})
const busy = ref(false), loaded = ref(false), usersLoaded = ref(false), error = ref(''), proxyURL = ref('')
const query = ref(''), filter = ref('all'), confirmRefresh = ref(false), copied = ref('')
const lifecycle = new AbortController()
const sourceName = computed(() => accounts.value.find(a => a.primary)?.name || '未配置或已删除')
const secondary = computed(() => accounts.value.filter(a => !a.primary && !a.id.startsWith('member_')))
const serverChanged = computed(() => !!config.value.bindings.length && config.value.embyUrl !== proxyURL.value)
const cards = computed(() => {
  const list = users.value.map(user => ({ ...user, missing: false }))
  const ids = new Set(list.map(user => user.Id))
  for (const m of Object.values(members.value)) { const key = memberKey(m); if (!ids.has(key)) { list.push({ Id: key, Name: m.embyId ? m.embyName : m.websiteName || m.userId, missing: !!m.embyId && usersLoaded.value }); ids.add(key) } }
  for (const b of config.value.bindings) if (!ids.has(b.userId)) list.push({ Id: b.userId, Name: b.userName || b.userId, missing: usersLoaded.value })
  return list.sort((a, b) => a.Name.localeCompare(b.Name, 'zh-CN') || a.Id.localeCompare(b.Id))
})
const filtered = computed(() => cards.value.filter(user => {
  const m = members.value[user.Id]
  if (route.query.websiteUser && m?.userId !== route.query.websiteUser) return false
  if (filter.value === 'website' && !m || filter.value === 'independent' && m || filter.value === 'pending' && !(m && memberPending(m) || bindingUnavailable(user.Id)) || filter.value === 'issue' && !needsAttention(user)) return false
  const bound = !!savedAccount(user.Id)
  if (filter.value === 'bound' && !bound || filter.value === 'source' && (bound || !!members.value[user.Id] && members.value[user.Id].source !== 'primary') || filter.value === 'disabled' && !user.Policy?.IsDisabled) return false
  return `${user.Name} ${user.Id} ${m?.websiteName || ''} ${m?.userId || ''} ${playbackLabel(user.Id)}`.toLowerCase().includes(query.value.trim().toLowerCase())
}))
const saving = computed(() => memberBusy.value || Object.values(drafts.value).some(d => d.busy))
const unsaved = computed(() => memberDirty.value || Object.values(drafts.value).some(dirty))
function savedAccount(id: string) {
  const member = members.value[id]
  if (member) return member.source === 'assigned' ? member.assignedAccountId : member.source === 'personal' ? member.accountId : ''
  return config.value.bindings.find(b => b.userId === id)?.accountId || ''
}
function playbackLabel(id: string) {
  const m = members.value[id]
  if (m?.source === 'personal') return `播放来源：本人网盘 · ${m.personalDrive?.uid || '待绑定'}`
  const assigned = m ? m.source === 'assigned' : !!savedAccount(id)
  return `播放来源：${assigned ? '管理员小号' : '主账号'} · ${assigned && !savedAccount(id) ? '未分配' : accountName(savedAccount(id))}`
}
function differentServer(m: Member) { return !!m.embyUrl && m.embyUrl !== proxyURL.value }
function accountName(id: string) { return id ? accounts.value.find(a => a.id === id)?.name || '原账号已删除' : sourceName.value }
function accountIssue(id: string) {
  const a = id ? accounts.value.find(a => a.id === id) : accounts.value.find(a => a.primary)
  return !a ? (id ? '原账号已删除，请重新选择' : '主账号未配置，请前往 115 账号管理') : !a.configured ? (id ? '账号未登录，请更新凭据' : '主账号未登录，请更新凭据') : ''
}
function bindingUnavailable(userID: string) {
  const m = members.value[userID]
  if (m?.source === 'personal') return !m.personalDrive?.connected || !m.personalDrive.directoryId || m.stage === 'paused'
  if (m?.source === 'assigned' && !m.assignedAccountId) return true
  return !!accountIssue(savedAccount(userID)) || m?.stage === 'paused'
}
function memberPending(m: Member) { return !settings.value.enabled || !m.policyEnabled || m.stage !== 'ready' || m.appliedRevision !== settings.value.revision || !!m.syncError || m.adminBlocked || m.websiteDisabled }
function needsAttention(user: User & { missing: boolean }) {
  const m = members.value[user.Id]
  return user.missing || user.Policy?.IsDisabled || bindingUnavailable(user.Id) || !!m && (differentServer(m) || memberPending(m))
}
function playbackStatus(user: User & { missing: boolean }) {
  const m = members.value[user.Id]
  if (m && differentServer(m)) return '原服务关联，请检查配置'
  if (user.missing) return 'Emby 用户不存在 · 保留关联'
  if (!usersLoaded.value && m?.embyId) return 'Emby 状态未确认 · 保留关联'
  if (m) {
    if (memberPending(m)) return memberStage(m, settings.value)
    return bindingUnavailable(user.Id) ? '播放来源不可用 · 保留绑定' : memberStage(m, settings.value)
  }
  if (user.Policy?.IsDisabled) return 'Emby 已停用'
  const issue = accountIssue(savedAccount(user.Id))
  if (issue) return issue
  return usersLoaded.value ? '已保存播放配置 · 凭据有效性在播放时校验' : 'Emby 状态未确认'
}
function directoryCID(id: string) { return accounts.value.find(a => a.id === id)?.playbackParentCid || '' }
function directory(id: string) { const cid = directoryCID(id); return cid === '0' ? '根目录（0）' : cid || '未设置（分享直取不受影响）' }
function dirty(d: Draft) { return d.accountId !== d.original }
function makeDraft(id: string): Draft { const accountId = savedAccount(id); return { accountId, original: accountId, busy: false, error: '', notice: '' } }
function resetDrafts() { drafts.value = Object.fromEntries(cards.value.map(user => [user.Id, makeDraft(user.Id)])) }
async function refresh(discard = false) {
  if (busy.value || saving.value) return
  if (unsaved.value && !discard) { confirmRefresh.value = true; return }
  confirmRefresh.value = false; busy.value = true; error.value = ''
  try {
    const [status, accountList, proxy, directory] = await Promise.all([
      embyRequest(props.token, 'playback-cache', lifecycle.signal),
      p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }),
      embyRequest(props.token, 'proxy', lifecycle.signal),
      memberRequest(props.token, lifecycle.signal, '/users'),
    ])
    config.value = { embyUrl: status.config.embyUrl, bindings: status.config.bindings }
    accounts.value = accountList.accounts; proxyURL.value = proxy.config.url
    if (!Array.isArray(directory.members)) throw new Error('播放用户关联读取失败，请刷新后重试')
    members.value = Object.fromEntries((directory.members as Member[]).map(m => [memberKey(m), m]))
    settings.value = directory.settings
    loaded.value = true
    try {
      const data = await embyRequest(props.token, 'playback-cache/users', lifecycle.signal)
      if (data.embyUrl !== proxyURL.value) throw new Error('Emby 服务配置已变化，请重新刷新。')
      if (serverChanged.value) { users.value = []; usersLoaded.value = false; throw new Error('Emby 服务已变化，请先移除原服务绑定，再刷新读取新服务用户。') }
      users.value = data.users; usersLoaded.value = true
    } catch (e) {
      usersLoaded.value = false
      users.value = []
      if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取 Emby 用户失败，已保留绑定'
    }
    resetDrafts()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取用户配置失败' }
  finally { busy.value = false }
}
async function save(user: User) {
  if (members.value[user.Id] || !usersLoaded.value) return
  const d = drafts.value[user.Id]
  if (!d || d.busy || busy.value) return
  d.busy = true; d.error = ''; d.notice = ''
  try {
    const data = await embyRequest(props.token, `playback-cache/bindings/${encodeURIComponent(user.Id)}`, lifecycle.signal, {
      embyUrl: !d.accountId && config.value.bindings.length ? config.value.embyUrl : proxyURL.value,
      mode: d.accountId ? 'personal' : 'unbound', accountId: d.accountId, expectedAccountId: d.original,
    })
    // Apply only this card; concurrent responses must not reset other cards or their drafts.
    const binding = (data.config.bindings as Binding[]).find(b => b.userId === user.Id)
    config.value.bindings = config.value.bindings.filter(b => b.userId !== user.Id)
    if (binding) config.value.bindings.push(binding)
    config.value.embyUrl = data.config.embyUrl
    d.original = binding?.accountId || ''; d.notice = '已保存，新播放请求生效。'
  } catch (e) { if (!lifecycle.signal.aborted) d.error = e instanceof Error ? e.message : '保存失败' }
  finally { d.busy = false }
}
async function copy(value: string) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value)
    else {
      const box = document.createElement('textarea'); box.value = value; box.style.position = 'fixed'; box.style.opacity = '0'; document.body.appendChild(box)
      try { box.select(); if (!document.execCommand('copy')) throw new Error('copy') } finally { box.remove() }
    }
    copied.value = value
  } catch { copied.value = ''; error.value = '复制失败，请选中文本手动复制。' }
}
onMounted(() => refresh())
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="Emby 用户管理" description="统一管理播放授权、账号绑定与个人网盘。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions><div class="flex flex-wrap gap-2"><RouterLink to="/users?tab=playback" class="rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">自助注册设置</RouterLink><RouterLink to="/helper115" class="rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">115 账号与目录</RouterLink></div></template>
    <section class="mb-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
      <div class="flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-semibold text-slate-900 dark:text-slate-100">播放用户 <span class="ml-1 text-slate-400">{{ cards.length }}</span></h2><p class="mt-2 text-sm text-slate-500 dark:text-slate-400">主账号：{{ sourceName }}</p></div><button :disabled="busy || saving" class="text-sm text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="refresh()">{{ busy ? '读取中…' : '刷新用户与状态' }}</button></div>
      <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">网站用户按授权开通播放；独立 Emby 用户未绑定时使用主账号。绑定账号失效时保留绑定，不自动回退。</p>
      <div class="flex flex-wrap gap-3"><input v-model="query" aria-label="搜索 Emby 用户" placeholder="搜索 Emby 名称、网站用户名或播放账号" class="min-w-0 flex-1 basis-64 rounded-xl border border-slate-200 bg-slate-50 px-4 py-2.5 text-sm dark:border-slate-700 dark:bg-slate-950" /><select v-model="filter" aria-label="筛选 Emby 用户" class="rounded-xl border border-slate-200 bg-white px-3 py-2.5 text-sm dark:border-slate-700 dark:bg-slate-900"><option value="all">全部用户</option><option value="website">网站关联用户</option><option value="independent">独立 Emby 用户</option><option value="pending">待开通 / 已暂停</option><option value="issue">需处理</option><option value="bound">已绑定网盘</option><option value="source">使用主账号</option><option value="disabled">Emby 已停用</option></select></div>
      <p v-if="route.query.websiteUser" class="text-sm text-slate-500 dark:text-slate-400">正在查看指定网站用户的播放关联。<RouterLink to="/emby/users" class="text-blue-600 dark:text-blue-300">查看全部</RouterLink></p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
      <div v-if="confirmRefresh" class="flex flex-wrap gap-2 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-400/10 dark:text-amber-200"><span>有未保存的修改，刷新将丢弃这些修改。</span><button class="underline" @click="selected = ''; memberDirty = false; refresh(true)">丢弃修改并刷新</button><button @click="confirmRefresh = false">继续编辑</button></div>
    </section>
    <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="user in filtered" :key="user.Id" :aria-label="user.Name + ' 的播放配置'" class="flex min-w-0 flex-col gap-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
        <div class="flex items-center gap-3"><EmbyUserAvatar :token="token" :user-id="user.Id" :name="user.Name" :image-tag="serverChanged ? undefined : user.PrimaryImageTag" /><div class="min-w-0"><h3 class="break-all font-semibold text-slate-900 dark:text-slate-100">{{ user.Name }}</h3><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{{ members[user.Id] ? '网站关联用户' : user.Policy?.IsAdministrator ? 'Emby 管理员' : '独立 Emby 用户' }}</p></div></div>
        <div class="space-y-2 text-sm text-slate-600 dark:text-slate-300"><p v-if="members[user.Id]" class="break-all">网站用户：{{ members[user.Id].websiteName || members[user.Id].userId }}</p><p class="break-all">{{ playbackLabel(user.Id) }}</p><p class="text-xs" :class="needsAttention(user) ? 'text-amber-700 dark:text-amber-300' : 'text-slate-500 dark:text-slate-400'">{{ playbackStatus(user) }}</p></div>
        <button :disabled="busy || saving" class="mt-auto self-end text-sm text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="openUser(user.Id)">管理</button>
      </article>
    </div>
    <p v-if="!filtered.length" class="py-12 text-center text-sm text-slate-500 dark:text-slate-400">{{ busy ? '正在读取用户…' : '暂无符合条件的播放用户。未注册 Emby 的网站账号可先在“我的播放”开通。' }}</p>
    <dialog ref="drawer" aria-labelledby="playback-drawer-title" class="playback-drawer border-l border-slate-200 bg-white text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" @cancel.prevent="closeDrawer()">
      <template v-for="user in selectedUser ? [selectedUser] : []" :key="user.Id">
        <header class="sticky top-0 z-10 flex items-center justify-between gap-4 border-b border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900"><h2 id="playback-drawer-title" class="min-w-0 break-all text-lg font-semibold">管理 {{ user.Name }}</h2><button :disabled="saving" autofocus class="shrink-0 text-sm text-slate-500 disabled:opacity-40 dark:text-slate-400" @click="closeDrawer()">关闭</button></header>
        <div class="space-y-6 p-5">
          <div v-if="confirmClose" class="space-y-3 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-400/10 dark:text-amber-200"><p>当前用户有未保存的修改。</p><div class="flex gap-4"><button @click="closeDrawer(true)">放弃修改并关闭</button><button @click="confirmClose = false">继续编辑</button></div></div>
          <MemberPlaybackEditor v-if="members[user.Id]" :key="user.Id + ':' + settings.revision" :token="token" :member="members[user.Id]" :settings="settings" :accounts="secondary" :disabled="differentServer(members[user.Id])" @updated="updateMember" @busy="memberBusy = $event" @dirty="memberDirty = $event" />
          <form v-else-if="drafts[user.Id] && usersLoaded" @submit.prevent="save(user)">
            <fieldset :disabled="busy || drafts[user.Id].busy || memberBusy" class="space-y-4 text-sm disabled:opacity-60">
              <h3 class="font-semibold">播放账号</h3><P115AccountSelect :id="'account-' + user.Id" v-model="drafts[user.Id].accountId" :accounts="secondary" :disable-accounts="user.missing || user.Policy?.IsDisabled || serverChanged" allow-source compact label="选择播放账号" source-label="使用主账号（不绑定小号）" />
              <p v-if="bindingUnavailable(user.Id)" class="text-xs leading-6 text-amber-700 dark:text-amber-300">当前播放来源不可用，保留原配置，不自动改用其他账号。</p>
              <p v-if="accountIssue(drafts[user.Id].accountId)" class="text-xs text-red-600 dark:text-red-300">{{ accountIssue(drafts[user.Id].accountId) }}</p>
              <div v-if="drafts[user.Id].accountId" class="space-y-2 text-xs text-slate-500 dark:text-slate-400"><p class="break-all">缓存目录：{{ directory(drafts[user.Id].accountId) }}</p><RouterLink to="/helper115" class="text-blue-600 dark:text-blue-300">管理账号与目录</RouterLink></div>
              <p v-else class="text-xs leading-6 text-slate-500 dark:text-slate-400">{{ drafts[user.Id].original ? '保存后主动解除小号绑定，新播放请求使用主账号。' : '未绑定小号，新播放请求默认使用主账号。' }}</p>
              <p v-if="user.missing || serverChanged" class="text-xs text-amber-700 dark:text-amber-300">选择主账号并保存可移除原绑定；播放仍须通过 Emby 权限校验。</p>
              <p v-if="drafts[user.Id].error" role="alert" class="text-red-600 dark:text-red-300">{{ drafts[user.Id].error }}</p>
              <p role="status" class="text-xs text-slate-500 dark:text-slate-400">{{ dirty(drafts[user.Id]) ? '未保存' : drafts[user.Id].notice || '已保存' }}</p>
              <button :disabled="!dirty(drafts[user.Id]) || !!drafts[user.Id].accountId && (user.missing || user.Policy?.IsDisabled || serverChanged)" class="rounded-lg bg-blue-600 px-4 py-2 text-white disabled:opacity-40">{{ drafts[user.Id].busy ? '保存中…' : '保存' }}</button>
            </fieldset>
          </form>
          <p v-else class="text-sm text-slate-500 dark:text-slate-400">Emby 用户信息尚未读取成功，原绑定已保留，请刷新后操作。</p>
          <template v-if="!members[user.Id] && usersLoaded && !user.missing && !serverChanged && !user.Policy?.IsDisabled">
            <div class="rounded-lg bg-slate-50 p-3 text-xs leading-6 text-slate-600 dark:bg-slate-800/70 dark:text-slate-300">
              <p>独立 Emby 用户尚未关联网站账号，当前仅由管理员管理播放来源。</p>
              <RouterLink to="/helper115" class="text-blue-600 dark:text-blue-300">去 115 账号管理新增小号 →</RouterLink>
            </div>
            <EmbyAdoptionEditor v-if="!user.Policy?.IsAdministrator" :key="user.Id + ':' + savedAccount(user.Id)" :token="token" :emby-id="user.Id" :disabled="dirty(drafts[user.Id]) || drafts[user.Id].busy" @busy="memberBusy = $event" @dirty="memberDirty = $event" @linked="adoptionLinked" />
          </template>
          <section class="space-y-3 border-t border-slate-200 pt-5 text-xs text-slate-500 dark:border-slate-700 dark:text-slate-400"><p v-if="members[user.Id]" class="break-all">网站用户：{{ members[user.Id].websiteName || members[user.Id].userId }}{{ members[user.Id].websiteDisabled ? '（已停用）' : '' }}</p><p v-if="!user.Id.startsWith('pending:')" class="break-all">Emby ID：{{ user.Id }} <button class="text-blue-600 dark:text-blue-300" @click="copy(user.Id)">{{ copied === user.Id ? '已复制' : '复制' }}</button></p><p v-else>尚未创建 Emby 账号</p><RouterLink v-if="members[user.Id]" to="/users" class="inline-block text-blue-600 dark:text-blue-300">管理网站账号</RouterLink></section>
        </div>
      </template>
    </dialog>
  </ManagementLayout>
</template>
<style scoped>
.playback-drawer { position: fixed; inset: 0 0 0 auto; margin: 0; width: min(100%, 32rem); height: 100dvh; max-width: 100%; max-height: 100dvh; padding: 0; overflow-y: auto; }
.playback-drawer::backdrop { background: rgb(0 0 0 / 35%); }
</style>
