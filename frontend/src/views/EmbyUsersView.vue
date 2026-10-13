<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
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
const filterControl = ref<HTMLFieldSetElement>()
const filterOptions = [
 { value: 'all', label: '全部', hint: '全部用户' },
 { value: 'primary', label: '主网盘', hint: '使用主网盘' },
 { value: 'assigned', label: '共享池', hint: '动态共享小号池' },
 { value: 'personal', label: '个人网盘', hint: '本人个人网盘' },
 { value: 'personal_shared', label: '子账号', hint: '播放子账号' },
 { value: 'pending', label: '需关注', hint: '网盘待就绪或账号异常' },
]
const { indicatorStyle: filterIndicatorStyle } = useSlidingIndicator(filterControl, '.is-selected', [filter], { durationMs: 260 })
function showChildren(owner: string) {
 childOwner.value = owner; filter.value = 'personal_shared'; query.value = ''
 void nextTick(() => filterControl.value?.querySelector<HTMLInputElement>('input:checked')?.focus({ preventScroll: true }))
}
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
  <template #actions><div class="users-page-actions flex flex-wrap justify-end gap-2"><RouterLink to="/helper115#pool" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">共享小号池</RouterLink><RouterLink to="/users" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">注册邀请</RouterLink><MemberDeletionPanel ref="deletionPanel" :token="token" :disabled="loading || dirtyUsers.size > 0 || [...busyUsers].some(id => id !== 'deletion')" @busy="mark(busyUsers, 'deletion', $event)" @changed="load(false, false)" /></div></template>
  <section v-if="statusLoaded" class="mb-5 flex flex-wrap items-center gap-4 rounded-xl border border-slate-200 bg-slate-50 p-4 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300" aria-label="全局媒体访问状态"><span>反代：{{ proxyListening ? '运行中' : '未运行，播放和下载入口不可用' }}</span><span v-if="proxyListening">播放需 Emby 授权及有效网盘</span><span>原文件下载：{{ settings.allowDownload ? (proxyListening ? '已开放' : '已允许，待 302 启动') : '已关闭' }}</span><RouterLink to="/emby/proxy" class="text-blue-600 dark:text-blue-300">管理播放与下载</RouterLink></section>
  <section class="users-toolbar" aria-label="用户筛选">
   <div class="users-toolbar-heading">
    <div class="users-title-group"><h2>Emby 用户 <span>{{ directoryKnown ? currentCount : loading ? '读取中' : '待核实' }}</span></h2><details class="users-help"><summary aria-label="用户管理说明"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9" /><path d="M12 10v6M12 7.5v.5" stroke-linecap="round" /></svg></summary><div class="users-help-panel"><p>用户数包含正常及已停用账号；待核实与待清理记录单独列出。</p><p>“需关注”筛选网盘待就绪或账号异常的记录。刷新同时核实 Emby 用户与网盘状态。</p><p>媒体库、分级、设备及下载权限由 Emby 管理，Cinema115 不会覆盖。所有用户仅允许原文件播放。</p></div></details></div>
    <div class="users-search"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4.5 4.5" /></svg><input v-model="query" type="search" aria-label="搜索 Emby 用户" placeholder="搜索用户名或 ID" /></div>
    <button :disabled="busy" class="users-refresh" @click="load()"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" :class="{ 'is-loading': loading }"><path d="M20 7v5h-5M4 17v-5h5" /><path d="M5.4 7a8 8 0 0 1 13.9 1M4.7 16A8 8 0 0 0 18.6 17" /></svg><span>{{ loading ? '正在刷新…' : '刷新状态' }}</span></button>
   </div>
   <div class="users-filter-row">
    <fieldset ref="filterControl" class="users-filters" aria-label="筛选用户"><legend class="sr-only">筛选用户</legend><div class="users-filter-indicator" :style="filterIndicatorStyle" aria-hidden="true"></div><label v-for="option in filterOptions" :key="option.value" :class="{ 'is-selected': filter === option.value }" :title="option.hint"><input v-model="filter" type="radio" name="emby-user-filter" :value="option.value" :aria-label="option.label" /><span>{{ option.label }}</span></label></fieldset>
    <p v-if="statusError || dirtyUsers.size || statusRefreshing" class="users-filter-status" :class="{ 'has-error': !!statusError }" :title="statusError || (dirtyUsers.size ? `${dirtyUsers.size} 位用户有未保存的修改` : '')" role="status">{{ statusError ? '网盘状态待核实' : dirtyUsers.size ? '有未保存的修改' : '检查网盘中' }}</p>
   </div>
   <UserPlaybackSessions :token="token" :members="members" />
  </section>
  <p v-if="childOwner" class="mb-4 flex items-center gap-3 text-sm text-slate-600 dark:text-slate-300">正在查看 {{ members.find(m => m.userId === childOwner)?.embyName }} 的子账号<button class="text-blue-600" @click="childOwner = ''; filter = 'all'">显示全部用户</button></p>
  <p v-if="error" role="alert" class="mb-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
  <div v-if="confirmDiscard" class="mb-4 flex flex-wrap items-center gap-3 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">刷新会丢弃未保存的修改。<button class="underline" :disabled="busy" @click="load(true)">放弃修改并刷新</button><button @click="confirmDiscard = false">继续编辑</button></div>
  <div class="grid items-stretch gap-4 md:grid-cols-2 lg:grid-cols-3">
   <div v-for="m in currentMembers" v-show="visible.has(m.userId)" :key="generation + ':' + m.userId" class="min-w-0">
    <MemberPlaybackEditor :token="token" :member="m" :disabled="busy" :delete-disabled="dirtyUsers.size > 0" @refresh="load(false, false)" @delete="deletionPanel?.inspect($event)" @updated="updated" @children="showChildren" @dirty="mark(dirtyUsers, m.userId, $event)" @busy="mark(busyUsers, m.userId, $event)" />
   </div>
  </div>
  <details v-if="reviewMembers.length" :open="reviewOpen || !!query || !!childOwner" class="mt-6 rounded-2xl border border-amber-200 bg-white p-5 dark:border-amber-900 dark:bg-slate-900" @toggle="reviewOpen = ($event.target as HTMLDetailsElement).open">
   <summary class="cursor-pointer text-sm font-medium text-amber-800 dark:text-amber-300">待核实 / 待清理记录（{{ reviewMembers.length }}）</summary>
   <p class="my-4 text-xs leading-6 text-slate-500">保留原业务关联，核实后可预览清理影响。不会因刷新自动清理，也不会关联同名新账号。</p>
   <div class="grid items-stretch gap-4 md:grid-cols-2 lg:grid-cols-3">
    <div v-for="m in reviewMembers" v-show="visible.has(m.userId)" :key="generation + ':' + m.userId" class="min-w-0">
     <MemberPlaybackEditor :token="token" :member="m" :disabled="busy" :delete-disabled="dirtyUsers.size > 0" @refresh="load(false, false)" @delete="deletionPanel?.inspect($event)" @updated="updated" @children="showChildren" @dirty="mark(dirtyUsers, m.userId, $event)" @busy="mark(busyUsers, m.userId, $event)" />
    </div>
   </div>
   <p v-if="!reviewMembers.some(m => visible.has(m.userId))" class="text-sm text-slate-500">没有符合筛选条件的待核实或待清理记录</p>
  </details>
  <p v-if="!visible.size" class="py-12 text-center text-sm text-slate-500">{{ loading ? '正在读取用户…' : error ? '用户状态读取失败，请重新核实' : members.length ? '没有符合筛选条件的用户' : '暂无 Emby 用户' }}</p>
 </ManagementLayout>
</template>
<style scoped>
.users-toolbar { --ut-bg: #fff; --ut-soft: #f5f7fb; --ut-text: #192b47; --ut-muted: #61718a; --ut-outline: #e5eaf2; --ut-blue: #2563eb; --ut-blue-soft: #eaf1ff; position: relative; margin-bottom: 20px; padding: 16px 18px; border: 1px solid var(--ut-outline); border-radius: 18px; background: var(--ut-bg); color: var(--ut-text); box-shadow: 0 3px 12px #193c7410; }
.dark .users-toolbar { --ut-bg: #182131; --ut-soft: #1f2b3e; --ut-text: #e4ecf8; --ut-muted: #a1b0c7; --ut-outline: #2c3b50; --ut-blue: #3770db; --ut-blue-soft: #20365b; }
.users-toolbar-heading { display: grid; grid-template-columns: minmax(0, 1fr) minmax(320px, 360px) auto; align-items: center; gap: 12px; }
.users-title-group { display: flex; min-width: 0; align-items: center; gap: 6px; }.users-title-group h2 { font-size: 15px; font-weight: 650; white-space: nowrap; }.users-title-group h2 span { margin-left: 8px; color: var(--ut-muted); font-weight: 500; font-variant-numeric: tabular-nums; }
.users-help { position: relative; }.users-help > summary { display: flex; align-items: center; justify-content: center; width: 32px; height: 40px; color: var(--ut-muted); cursor: pointer; list-style: none; }.users-help > summary::-webkit-details-marker { display: none; }.users-help svg { width: 17px; height: 17px; }.users-help-panel { position: absolute; top: 44px; left: -120px; z-index: 20; width: 300px; max-width: calc(100vw - 48px); padding: 14px; border: 1px solid var(--ut-outline); border-radius: 12px; background: var(--ut-bg); color: var(--ut-muted); box-shadow: 0 10px 32px #10203a20; font-size: 12px; line-height: 1.7; }.users-help-panel p + p { margin-top: 8px; }
.users-search { display: flex; align-items: center; gap: 9px; min-width: 0; height: 40px; padding: 0 12px; border: 1px solid var(--ut-outline); border-radius: 10px; background: var(--ut-soft); }.users-search > svg { flex-shrink: 0; width: 17px; height: 17px; color: var(--ut-muted); }.users-search input { min-width: 0; width: 100%; padding: 8px 0; background: transparent; color: var(--ut-text); font-size: 14px; outline: none; }.users-search input::placeholder { color: var(--ut-muted); }.users-search:focus-within { outline: 2px solid #6095ff; outline-offset: 2px; }
.users-refresh { display: flex; align-items: center; justify-content: center; gap: 7px; min-width: 100px; min-height: 40px; padding: 0 12px; border-radius: 10px; background: var(--ut-soft); color: var(--ut-text); font-size: 12px; font-weight: 600; cursor: pointer; }.users-refresh > svg { width: 16px; height: 16px; }.users-refresh:disabled { opacity: .45; cursor: default; }.users-refresh .is-loading { animation: users-refresh-spin 1s linear infinite; }
.users-filter-row { display: flex; align-items: center; gap: 10px; margin-top: 12px; }.users-filters { position: relative; display: flex; flex-wrap: wrap; gap: 6px; min-width: 0; margin: 0; padding: 0; border: 0; }.users-filters label { position: relative; display: flex; align-items: center; justify-content: center; min-height: 36px; padding: 0 14px; border-radius: 9px; background: var(--ut-soft); color: var(--ut-muted); font-size: 12px; font-weight: 600; cursor: pointer; transition: color 150ms ease; }.users-filters label.is-selected { color: white; }.users-filters input { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }.users-filters label:has(input:focus-visible), .users-toolbar button:focus-visible, .users-help summary:focus-visible { outline: 2px solid #6095ff; outline-offset: 3px; }.users-filter-status { margin-left: auto; max-width: 12em; color: var(--ut-muted); font-size: 11px; white-space: nowrap; text-overflow: ellipsis; overflow: hidden; }.users-filter-status.has-error { color: #a33f4d; }.dark .users-filter-status.has-error { color: #ffb7bf; }
.users-filter-indicator { position: absolute; z-index: 1; border-radius: 9px; background: var(--ut-blue); box-shadow: 0 2px 5px #2563eb20; pointer-events: none; }
.users-filters label span { position: relative; z-index: 2; transition: transform 150ms ease; }
.users-filters label:active span { transform: scale(.97); }
@media (hover: hover) { .users-refresh:not(:disabled):hover, .users-filters label:not(.is-selected):hover { background: var(--ut-blue-soft); } }
@media (max-width: 767px) { .users-toolbar { padding: 14px; }.users-toolbar-heading { grid-template-columns: minmax(0,1fr) auto; gap: 10px; }.users-search { grid-column: 1 / -1; grid-row: 2; height: 44px; }.users-search input { font-size: 16px; }.users-refresh { min-height: 44px; }.users-help > summary { height: 44px; }.users-filter-row { display: block; }.users-filters { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); }.users-filters label { min-height: 44px; padding-inline: 4px; }.users-filter-status { margin: 8px 2px 0; max-width: none; text-align: center; }.users-title-group h2 { font-size: 14px; }.users-help { position: static; }.users-help-panel { top: 62px; left: 14px; right: 14px; width: auto; max-width: none; } }
@keyframes users-refresh-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .users-filters label, .users-filters label span { transition: none; }.users-filters label:active span { transform: none; }.users-refresh .is-loading { animation: none; } }
@media (max-width: 639px) {
 :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
 :deep(.management-heading > div) { grid-row: 3; }
 :deep(.management-heading > p) { grid-column: 1; }
 .users-page-actions { justify-content: flex-start; }
}
</style>
