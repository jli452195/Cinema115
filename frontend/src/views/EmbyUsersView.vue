<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import MemberPlaybackEditor from '../components/MemberPlaybackEditor.vue'
import UserPlaybackSessions from '../components/UserPlaybackSessions.vue'
import MemberDeletionPanel from '../components/MemberDeletionPanel.vue'
import { memberRequest, memberNeedsReview, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const members = ref<MemberPlayback[]>([])
const settings = ref<MemberSettings>({ enabled: false, libraryIds: [], allowDownload: false })
const proxyListening = ref(false), statusLoaded = ref(false)
const directoryKnown = ref(false)
const error = ref(''), query = ref(''), filter = ref('all'), loading = ref(false)
const dirtyUsers = ref(new Set<string>()), busyUsers = ref(new Set<string>())
const generation = ref(0), confirmDiscard = ref(false)
const childOwner = ref('')
const deletionPanel = ref<InstanceType<typeof MemberDeletionPanel>>()
const currentMembers = computed(() => members.value.filter(m => !memberNeedsReview(m)))
const reviewMembers = computed(() => members.value.filter(memberNeedsReview))
const currentCount = computed(() => members.value.filter(m => m.embyState === 'active' || m.embyState === 'disabled').length)
const reviewOpen = ref(false)
const statusRefreshing = computed(() => members.value.some(m => m.driveStatus?.state === 'pending'))
let pollTimer: ReturnType<typeof setTimeout> | undefined
let statusReading = false
let statusGeneration = 0
const statusError = ref('')
function showChildren(owner: string) { childOwner.value = owner; filter.value = 'personal_shared'; query.value = '' }
const busy = computed(() => loading.value || busyUsers.value.size > 0)
const lifecycle = new AbortController()
const visible = computed(() => new Set(members.value.filter(m => {
 const matches = `${m.embyName} ${m.embyId} ${m.ownerName || ''}`.toLowerCase().includes(query.value.trim().toLowerCase())
 const state = filter.value === 'all' || m.source === filter.value || filter.value === 'pending' && ((m.driveStatus ? m.driveStatus.state !== 'ready' : m.stage !== 'ready') || m.embyState !== 'active' || m.childPaused || m.childDisabled || m.childRevoked)
 return matches && state && (!childOwner.value || m.ownerUserId === childOwner.value)
}).map(m => m.userId)))
function mark(set: Set<string>, id: string, value: boolean) { if (value) set.add(id); else set.delete(id) }
async function load(discard = false, checkDrives = true) {
 if (busy.value) return
 if (dirtyUsers.value.size && !discard) { confirmDiscard.value = true; return }
 loading.value = true; error.value = ''; statusGeneration++
 let directoryLoaded = false
 try {
  const [data, serviceStatus] = await Promise.all([memberRequest(props.token, lifecycle.signal, '/users'), memberRequest(props.token, lifecycle.signal, '/settings')])
  members.value = [...data.members].sort((a: MemberPlayback, b: MemberPlayback) => a.embyName.localeCompare(b.embyName, 'zh-CN', { numeric: true }) || a.userId.localeCompare(b.userId))
  directoryLoaded = true
  directoryKnown.value = true
  settings.value = data.settings; proxyListening.value = serviceStatus.proxy.listening; statusLoaded.value = true
  dirtyUsers.value.clear(); generation.value++; confirmDiscard.value = false
  if (checkDrives) {
   const result = await memberRequest(props.token, lifecycle.signal, '/refresh', {})
   applyDriveStatuses(result.members || [])
  }
 } catch (e) { if (!lifecycle.signal.aborted) {
  error.value = e instanceof Error ? e.message : '读取失败'
  if (!directoryLoaded) { directoryKnown.value = false; members.value = members.value.map(m => ({ ...m, embyState: 'unknown' })) }
 } }
 finally { loading.value = false; scheduleStatusPoll() }
}
function updated(member: MemberPlayback, next: MemberSettings) {
	statusGeneration++
 members.value = members.value.map(m => m.userId === member.userId ? member : m)
 settings.value = next
 scheduleStatusPoll()
}
function applyDriveStatuses(items: Array<{ userId: string; driveStatus?: MemberPlayback['driveStatus'] }>) {
 const byID = new Map(items.map(item => [item.userId, item.driveStatus]))
 members.value = members.value.map(member => byID.has(member.userId) ? { ...member, driveStatus: byID.get(member.userId) } : member)
}
function scheduleStatusPoll() {
 clearTimeout(pollTimer)
 if (!lifecycle.signal.aborted) pollTimer = setTimeout(readDriveStatuses, statusRefreshing.value ? 1500 : 15000)
}
async function readDriveStatuses() {
 if (lifecycle.signal.aborted) return
 if (statusReading || loading.value || document.hidden) { scheduleStatusPoll(); return }
 statusReading = true
 const started = statusGeneration
 try {
  const data = await memberRequest(props.token, lifecycle.signal, '/drive-status')
  if (started === statusGeneration) { applyDriveStatuses(data.members || []); statusError.value = '' }
 } catch { if (!lifecycle.signal.aborted) statusError.value = '暂时无法读取最新网盘状态，将自动重试。' }
 finally { statusReading = false; scheduleStatusPoll() }
}
function onReturn() {
 if (document.hidden || lifecycle.signal.aborted) return
 if (!busy.value && !dirtyUsers.value.size && !document.querySelector('dialog[open]')) void load(false, false)
 else void readDriveStatuses()
}
onBeforeRouteLeave(() => !dirtyUsers.value.size || window.confirm('有未保存的播放账号修改，离开后将丢弃。是否离开？'))
onMounted(() => { void load(false, false); window.addEventListener('focus', onReturn); document.addEventListener('visibilitychange', onReturn) })
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(pollTimer); window.removeEventListener('focus', onReturn); document.removeEventListener('visibilitychange', onReturn) })
</script>
<template>
 <ManagementLayout title="Emby 用户管理" description="管理播放网盘和子账号；用户播放及媒体库权限由 Emby 管理。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
  <template #actions><div class="flex flex-wrap justify-end gap-2"><RouterLink to="/helper115#pool" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">共享小号池</RouterLink><RouterLink to="/users" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">注册邀请</RouterLink></div></template>
  <section v-if="statusLoaded" class="mb-5 flex flex-wrap items-center gap-4 rounded-xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300" aria-label="全局媒体访问状态"><span>反代：{{ proxyListening ? '运行中' : '未运行，播放和下载入口不可用' }}</span><span v-if="proxyListening">播放需 Emby 授权及有效网盘</span><span>原文件下载：{{ settings.allowDownload ? (proxyListening ? '已开放' : '已允许，待 302 启动') : '已关闭' }}</span><RouterLink to="/emby/proxy" class="text-blue-600 dark:text-blue-300">管理播放与下载</RouterLink></section>
  <section class="mb-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900" aria-label="用户筛选">
   <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">Emby 用户 <span class="ml-2 font-normal text-slate-400">{{ directoryKnown ? currentCount : loading ? '读取中' : '待核实' }}</span></h2><button :disabled="busy" class="rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300" @click="load()">{{ loading ? '正在刷新…' : '刷新状态' }}</button></div>
   <p class="text-xs text-slate-500 dark:text-slate-400">用户数包含正常及已停用账号；待核实与待清理记录单独列出。刷新同时检查用户及网盘状态。</p>
   <p v-if="statusError" role="status" class="text-xs text-amber-700 dark:text-amber-300">{{ statusError }}</p>
   <p v-else-if="statusRefreshing" role="status" class="text-xs text-slate-500">正在检查网盘，用户状态仍可刷新。</p>
   <div class="grid grid-cols-2 gap-3 md:grid-cols-[minmax(0,1fr)_auto]"><input v-model="query" aria-label="搜索 Emby 用户" placeholder="搜索用户名、用户 ID 或播放账号" class="col-span-2 min-w-0 rounded-xl border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-700 md:col-span-1 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" /><select v-model="filter" aria-label="筛选用户" class="min-w-0 rounded-xl border border-slate-200 bg-white px-3 py-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"><option value="all">全部用户</option><option value="primary">主网盘</option><option value="assigned">共享小号</option><option value="personal">个人网盘</option><option value="personal_shared">播放子账号</option><option value="pending">网盘待就绪 / 账号异常</option></select></div>
   <p v-if="dirtyUsers.size" class="text-xs text-amber-700 dark:text-amber-300">{{ dirtyUsers.size }} 位用户有未保存的修改</p>
  </section>
  <p v-if="childOwner" class="mb-4 flex items-center gap-3 text-sm text-slate-600 dark:text-slate-300">正在查看 {{ members.find(m => m.userId === childOwner)?.embyName }} 的子账号<button class="text-blue-600" @click="childOwner = ''; filter = 'all'">显示全部用户</button></p>
  <p v-if="error" role="alert" class="mb-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
  <div v-if="confirmDiscard" class="mb-4 flex flex-wrap items-center gap-3 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">刷新会丢弃未保存的修改。<button class="underline" :disabled="busy" @click="load(true)">放弃修改并刷新</button><button @click="confirmDiscard = false">继续编辑</button></div>
  <UserPlaybackSessions :token="token" :members="members" />
  <p class="mb-4 text-xs leading-6 text-slate-500">媒体库、分级、设备及下载权限在 Emby 中管理，Cinema115 不会覆盖这些权限。所有用户仅允许原文件播放。</p>
  <div class="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3">
   <div v-for="m in currentMembers" v-show="visible.has(m.userId)" :key="generation + ':' + m.userId" class="min-w-0">
    <MemberPlaybackEditor :token="token" :member="m" :disabled="busy" :delete-disabled="dirtyUsers.size > 0" @refresh="load(false, false)" @delete="deletionPanel?.inspect($event)" @updated="updated" @children="showChildren" @dirty="mark(dirtyUsers, m.userId, $event)" @busy="mark(busyUsers, m.userId, $event)" />
   </div>
  </div>
  <details v-if="reviewMembers.length" :open="reviewOpen || !!query || !!childOwner" class="mt-6 rounded-2xl border border-amber-200 bg-white p-5 dark:border-amber-900 dark:bg-slate-900" @toggle="reviewOpen = ($event.target as HTMLDetailsElement).open">
   <summary class="cursor-pointer text-sm font-medium text-amber-800 dark:text-amber-300">待核实 / 待清理记录（{{ reviewMembers.length }}）</summary>
   <p class="my-4 text-xs leading-6 text-slate-500">保留原业务关联，核实后可预览清理影响。不会因刷新自动清理，也不会关联同名新账号。</p>
   <div class="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3">
    <div v-for="m in reviewMembers" v-show="visible.has(m.userId)" :key="generation + ':' + m.userId" class="min-w-0">
     <MemberPlaybackEditor :token="token" :member="m" :disabled="busy" :delete-disabled="dirtyUsers.size > 0" @refresh="load(false, false)" @delete="deletionPanel?.inspect($event)" @updated="updated" @children="showChildren" @dirty="mark(dirtyUsers, m.userId, $event)" @busy="mark(busyUsers, m.userId, $event)" />
    </div>
   </div>
   <p v-if="!reviewMembers.some(m => visible.has(m.userId))" class="text-sm text-slate-500">没有符合筛选条件的待核实或待清理记录</p>
  </details>
  <p v-if="!visible.size" class="py-12 text-center text-sm text-slate-500">{{ loading ? '正在读取用户…' : error ? '用户状态读取失败，请重新核实' : members.length ? '没有符合筛选条件的用户' : '暂无 Emby 用户' }}</p>
  <MemberDeletionPanel ref="deletionPanel" :token="token" :members="members" :disabled="loading || dirtyUsers.size > 0 || [...busyUsers].some(id => id !== 'deletion')" @busy="mark(busyUsers, 'deletion', $event)" @changed="load(false, false)" />
 </ManagementLayout>
</template>
<style scoped>
@media (max-width: 639px) {
 :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
 :deep(.management-heading > div) { grid-row: 3; }
 :deep(.management-heading > p) { grid-column: 1; }
}
</style>
