<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import P115SidePanel from './P115SidePanel.vue'
import P115Icon from './P115Icon.vue'

const props = defineProps<{ token: string; driveReady: boolean; playbackLoaded: boolean }>()
interface Child { requestId: string; userId: string; name: string; completed: boolean; paused: boolean; disabled: boolean; revoked: boolean; blocked: boolean; revision: number; stage: string; message: string }
interface Children { limit: number; used: number; items: Child[] }
const data = ref<Children>(), busy = ref(false), loading = ref(true), error = ref(''), notice = ref('')
const panel = ref<'' | 'create' | 'manage' | 'password' | 'revoke'>(''), heading = ref<HTMLElement>()
const name = ref(''), password = ref(''), requestId = ref(''), retryName = ref(''), consent = ref(false), unconfirmed = ref(false)
const selectedID = ref(''), newPassword = ref('')
const lifecycle = new AbortController()
const selected = computed(() => data.value?.items.find(child => child.requestId === selectedID.value))
const items = computed(() => [...(data.value?.items || [])].sort((a, b) => Number(!b.completed && !b.revoked) - Number(!a.completed && !a.revoked)))
const canCreate = computed(() => props.playbackLoaded && props.driveReady && !!data.value && data.value.used < data.value.limit)
const panelTitle = computed(() => panel.value === 'create' ? requestId.value ? '继续创建子账号' : '新增播放子账号' : panel.value === 'password' ? '重置子账号密码' : panel.value === 'revoke' ? '永久撤销子账号' : '管理 ' + (selected.value?.name || '子账号'))
class ChildRequestError extends Error {
  constructor(message: string, readonly status: number) { super(message) }
}
watch(panel, async value => {
  if (value) return
  await nextTick()
  if (document.activeElement === document.body) heading.value?.focus()
})

async function request(path = '', body?: object): Promise<Children> {
  const response = await fetch('/api/me/playback/children' + path, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]) })
  const value = await response.json().catch(() => null)
  if (!response.ok) throw new ChildRequestError(value?.error || '子账号操作失败', response.status)
  return value
}
async function load(clearError = true) {
  try {
    const value = await request()
    if (lifecycle.signal.aborted) return false
    data.value = value
    if (clearError) error.value = ''
    const known = value.items.find(child => child.requestId === requestId.value)
    if (known && (known.completed || known.revoked || known.blocked)) {
      requestId.value = ''; retryName.value = ''; unconfirmed.value = false
      if (panel.value === 'create') panel.value = ''
      notice.value = known.revoked || known.blocked ? '原任务状态已变化，请查看列表。' : '已确认子账号创建完成，请使用 Emby 登录。'
    }
    return true
  }
  catch (e) { if (!lifecycle.signal.aborted && clearError) error.value = e instanceof Error ? e.message : '状态读取失败'; return false }
  finally { loading.value = false }
}
async function refresh() { if (busy.value) return; busy.value = true; await load(); busy.value = false }
async function closePanel() {
  if (busy.value) return
  panel.value = ''; password.value = ''; newPassword.value = ''; consent.value = false
  await nextTick()
  if (document.activeElement === document.body) heading.value?.focus()
}
function openCreate(child?: Child) {
  if (busy.value || child?.blocked || child?.revoked || (!child && !requestId.value && !canCreate.value)) return
  if (child) { requestId.value = child.requestId; retryName.value = child.name; name.value = child.name }
  else if (requestId.value) name.value = retryName.value
  else name.value = ''
  password.value = ''; consent.value = false; error.value = ''; notice.value = ''; panel.value = 'create'
}
function openChild(child: Child) {
  if (busy.value) return
  selectedID.value = child.requestId; newPassword.value = ''; error.value = ''; notice.value = ''; panel.value = 'manage'
}
function newID() { const bytes = crypto.getRandomValues(new Uint8Array(16)); return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('') }
async function create() {
  if (busy.value || !consent.value || !props.driveReady || !props.playbackLoaded || (!requestId.value && !canCreate.value)) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    // If even the recovery read failed, resolve the old request before any further write.
    if (unconfirmed.value) {
      data.value = await request()
      const old = data.value.items.find(child => child.requestId === requestId.value)
      unconfirmed.value = false
      if (old?.completed) { requestId.value = ''; retryName.value = ''; panel.value = ''; notice.value = '已确认子账号创建完成，请使用 Emby 登录。'; return }
      if (old?.blocked || old?.revoked) throw new Error('原创建任务的身份或状态已变化，请刷新列表或联系管理员')
      // A slow POST may not have published its reservation yet. Keep this ID even when absent.
    }
    if (!requestId.value) { requestId.value = newID(); retryName.value = name.value.trim() }
    const value = await request('', { requestId: requestId.value, name: retryName.value, password: password.value })
    if (lifecycle.signal.aborted) return
    data.value = value; requestId.value = ''; retryName.value = ''; name.value = ''; panel.value = ''; notice.value = '子账号已创建，请使用 Emby 登录。'
  } catch (e) {
    if (!lifecycle.signal.aborted) {
      error.value = e instanceof Error ? e.message : '创建结果未确认'
      try {
        const value = await request()
        if (lifecycle.signal.aborted) return
        data.value = value; unconfirmed.value = false
        const child = value.items.find(child => child.requestId === requestId.value)
        const rejectedBeforeReservation = !child && e instanceof ChildRequestError && [400, 403, 409, 422].includes(e.status)
        if (rejectedBeforeReservation || child?.completed || child?.revoked || child?.blocked) { requestId.value = ''; retryName.value = '' }
        else if (!child) unconfirmed.value = true
        if (child?.completed || child?.revoked || child?.blocked) {
          panel.value = ''
          if (child.blocked || child.revoked) error.value = '原任务身份或状态已变化，请查看列表或联系管理员'
          else { error.value = ''; notice.value = '已确认子账号创建完成，请使用 Emby 登录。' }
        }
      } catch { unconfirmed.value = true }
    }
  } finally { password.value = ''; busy.value = false }
}
async function action(action: string) {
  const child = selected.value
  if (busy.value || !child || child.blocked || child.revoked || !child.completed) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const value = await request('/' + encodeURIComponent(child.userId), { action, revision: child.revision, ...(action === 'password' ? { password: newPassword.value } : {}) })
    if (lifecycle.signal.aborted) return
    data.value = value
    panel.value = action === 'revoke' ? '' : 'manage'
    notice.value = action === 'password' ? '密码已更新；已登录设备不保证立即退出。' : '子账号状态已更新。'
  } catch (e) {
    if (!lifecycle.signal.aborted) { error.value = e instanceof Error ? e.message : '操作结果未确认'; await load(false) }
  } finally { newPassword.value = ''; busy.value = false }
}
onMounted(() => { void load() })
onBeforeUnmount(() => { lifecycle.abort(); password.value = ''; newPassword.value = '' })
</script>

<template>
  <section v-if="loading || error || requestId || data && (data.limit > 0 || data.items.length)" class="pp-card">
    <div class="mb-5 flex items-center justify-between gap-3"><h2 ref="heading" tabindex="-1" class="pp-title"><P115Icon name="users" class="text-slate-400" />播放子账号</h2><span v-if="data" class="pp-muted shrink-0"><strong class="text-sm font-semibold text-slate-800 dark:text-slate-200">{{ data.used }}</strong> / {{ data.limit }} 名额</span></div>
    <p class="pp-muted">仅供登录 Emby，共享你已绑定的个人网盘。本人切换播放来源不改变共享网盘。</p>
    <p v-if="loading" role="status" class="pp-muted mt-4">正在读取子账号…</p>
    <p v-if="error && !panel" role="alert" class="pp-error mt-4">{{ error }}</p>
    <p v-if="notice && !panel" role="status" class="mt-4 text-xs text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
    <div v-if="requestId && !data?.items.some(child => child.requestId === requestId)" class="pp-warning mt-4"><p>有一项创建结果待确认，先读取原任务状态，不会重复创建。</p><button type="button" class="pp-link mt-2" :disabled="busy" @click="openCreate()">继续原创建任务</button></div>
    <div v-if="data && !data.items.length" class="py-7 text-center"><div class="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-xl bg-slate-50 text-slate-400 dark:bg-slate-800"><P115Icon name="users" /></div><h3 class="text-sm font-medium text-slate-800 dark:text-slate-200">还没有播放子账号</h3><p class="pp-muted mx-auto mt-2 max-w-64">{{ !playbackLoaded ? '等待个人网盘状态确认。' : !driveReady ? '先完成个人网盘和保存目录绑定，再创建供家人使用的 Emby 账号。' : '为家人创建独立的 Emby 账号，各自保留观看记录。' }}</p></div>
    <div v-for="child in items" :key="child.requestId" class="flex min-w-0 items-center gap-3 border-b border-slate-100 py-4 dark:border-slate-800">
      <div class="hidden h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600 sm:flex dark:bg-blue-400/10 dark:text-blue-300"><P115Icon name="user" /></div>
      <div class="min-w-0 flex-1"><h3 class="break-all text-sm font-medium text-slate-800 dark:text-slate-200">{{ child.name }}</h3><div class="mt-1 flex flex-wrap gap-1.5"><span v-if="child.revoked" class="pp-badge">已永久撤销</span><span v-else-if="child.blocked" class="pp-badge">已停止托管</span><span v-else-if="!child.completed" class="pp-badge pp-badge-amber">创建待完成</span><template v-else><span v-if="child.paused" class="pp-badge pp-badge-amber">共享已暂停</span><span v-if="child.disabled" class="pp-badge">登录已停用</span><span v-if="!child.paused && !child.disabled" class="pp-badge" :class="child.stage === 'ready' ? 'pp-badge-green' : 'pp-badge-amber'">{{ child.stage === 'ready' ? '网盘最近检查通过' : '检查待恢复' }}</span></template></div><p class="pp-muted mt-1 break-words">{{ child.blocked ? '此账号已被设置为 Emby 管理员，系统停止托管，操作已锁定。' : child.revoked ? '保留禁用身份和观看记录，不能恢复共享。' : child.message }}</p></div>
      <button v-if="!child.revoked" type="button" class="pp-button shrink-0 !px-3 !text-xs" :disabled="busy || child.blocked" @click="child.completed ? openChild(child) : openCreate(child)">{{ child.completed ? '管理' : '继续完成' }}</button>
    </div>
    <p v-if="data?.items.length && playbackLoaded && !driveReady" class="pp-warning mt-4">共享的个人网盘尚未就绪，新的播放请求可能被拒绝。你仍可管理已有子账号。</p>
    <div class="mt-5 flex flex-wrap items-center justify-between gap-3"><button v-if="data" type="button" class="pp-button" :disabled="busy || !canCreate || !!requestId" @click="openCreate()"><P115Icon name="plus" />新增子账号</button><button type="button" class="pp-link" :disabled="busy" @click="refresh">{{ busy ? '处理中…' : '刷新子账号' }}</button></div>
    <p v-if="data" class="pp-muted mt-3">{{ data.used >= data.limit ? '名额已用满。' : '' }}创建中、暂停共享和停用登录仍占名额，永久撤销后释放。</p>

    <P115SidePanel v-if="panel" :title="panelTitle" description="子账号只能登录 Emby，不能登录网站或个人中心。" :busy="busy" @close="closePanel">
      <p v-if="error" role="alert" class="pp-error mb-4">{{ error }}</p><p v-if="notice" role="status" class="mb-4 text-xs text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
      <template v-if="panel === 'create'">
        <p v-if="requestId" class="pp-warning mb-4">请重新输入首次提交的密码，继续原创建任务；用户名和任务标识保持不变。</p>
        <div class="pp-info">共享本人已完成绑定的个人网盘与秒传目录，文件占用个人空间。创建中与停用仍占名额。媒体权限由管理员设置。</div>
        <p v-if="!driveReady" class="pp-warning mt-4">请先恢复个人网盘与保存目录，再继续创建。</p>
        <form id="playback-child-form" class="mt-5 space-y-5" @submit.prevent="create">
          <label class="pp-field">子账号用户名<input v-model="name" :disabled="busy || !!requestId" required minlength="3" maxlength="32" autocomplete="off" class="pp-input" /></label>
          <div><label class="pp-field">子账号初始密码<input v-model="password" :disabled="busy" required type="password" minlength="8" maxlength="128" autocomplete="new-password" aria-describedby="child-password-help" class="pp-input" /></label><p id="child-password-help" class="pp-muted mt-2">密码不保存在创建任务中，每次提交后清空。</p></div>
          <label class="pp-check"><input v-model="consent" :disabled="busy" type="checkbox" />我授权这些子账号使用我的个人网盘播放，并承担秒传文件占用的空间。</label>
        </form>
      </template>
      <template v-else-if="selected">
        <div class="pp-info"><p class="break-all font-medium">{{ selected.name }}</p><p v-if="selected.blocked" class="mt-2">账号已被设置为 Emby 管理员，已停止托管。</p><p v-else-if="selected.revoked" class="mt-2">共享已永久撤销，不能恢复。</p><p v-else class="mt-2">{{ selected.paused ? '共享已暂停。' : '共享未暂停。' }}{{ selected.disabled ? '登录已停用。' : '登录未停用。' }}</p></div>
        <template v-if="panel === 'manage'">
          <button type="button" class="pp-action" :disabled="busy || selected.blocked || selected.revoked" @click="action(selected.paused ? 'resume' : 'pause')"><span>{{ selected.paused ? '恢复共享' : '暂停共享' }}<small>限制新的播放取链，保留账号和名额；已签发直链不保证立即失效。</small></span></button>
          <button type="button" class="pp-action" :disabled="busy || selected.blocked || selected.revoked" @click="action(selected.disabled ? 'enable' : 'disable')"><span>{{ selected.disabled ? '启用 Emby 登录' : '停用 Emby 登录' }}<small>可恢复，仍占子账号名额。</small></span></button>
          <button type="button" class="pp-action" :disabled="busy || selected.blocked || selected.revoked" @click="newPassword = ''; panel = 'password'"><span>重置密码<small>不保证已登录设备立即退出。</small></span><P115Icon name="key" /></button>
          <button type="button" class="pp-link mt-7 !text-red-600 dark:!text-red-300" :disabled="busy || selected.blocked || selected.revoked" @click="panel = 'revoke'">永久撤销子账号</button>
        </template>
        <template v-else-if="panel === 'password'"><form id="playback-child-password" class="mt-5" @submit.prevent="action('password')"><label class="pp-field">子账号新密码<input v-model="newPassword" :disabled="busy || selected.blocked || selected.revoked" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="pp-input" /></label></form><p class="pp-muted mt-4">更新密码不保证已登录设备立即退出；需要阻止账号使用时，可停用 Emby 登录。</p></template>
        <div v-else-if="panel === 'revoke'" class="pp-warning mt-5 space-y-3"><p>撤销后释放名额，不能恢复此身份的共享。</p><p>保留禁用的 Emby 账号及观看记录。后续播放请求被拒绝，已发出的直链可能仍有效。</p></div>
      </template>
      <p v-else class="pp-warning">子账号状态已变化，请关闭面板后刷新列表。</p>
      <template #footer>
        <button type="button" class="pp-button" :disabled="busy" @click="closePanel">关闭</button>
        <button v-if="panel === 'create'" type="submit" form="playback-child-form" class="pp-button pp-primary ml-auto" :disabled="busy || !consent || !driveReady || !playbackLoaded || (!requestId && !canCreate)">{{ busy ? '处理中…' : requestId ? '继续创建' : '创建子账号' }}</button>
        <button v-else-if="panel === 'password'" type="submit" form="playback-child-password" class="pp-button pp-primary ml-auto" :disabled="busy || !selected || selected.blocked || selected.revoked">保存新密码</button>
        <button v-else-if="panel === 'revoke'" type="button" class="pp-button pp-danger ml-auto" :disabled="busy || !selected || selected.blocked || selected.revoked" @click="action('revoke')">确认永久撤销</button>
      </template>
    </P115SidePanel>
  </section>
</template>
