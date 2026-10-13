<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { P115Account } from '../types/p115Accounts'
import { embyRequest } from '../types/emby'

const props = defineProps<{ token: string; accounts: P115Account[] }>()
interface Config { userId: string; sourceAccountId: string; accountId: string; targetCid: string; targetPath: string }
interface Binding { userId: string; userName: string }
interface Record { id: string; sourceAccountId: string; accountId: string; targetCid: string; status: string; message: string; name: string; size: number; sha1: string; fileId?: string; pickCode?: string; challenged: boolean; startedAt: string; durationMs: number }
interface Status { config: Config; records: Record[]; running: boolean }
const config = ref<Config>({ userId: '', sourceAccountId: '', accountId: '', targetCid: '', targetPath: '' })
const saved = ref<Config>()
const bindings = ref<Binding[]>([])
const records = ref<Record[]>([])
const running = ref(false), submitting = ref(false), busy = ref(false), loaded = ref(false)
const strm = ref(''), error = ref(''), notice = ref(''), requestId = ref('')
const showAll = ref(false), dialog = ref<HTMLDialogElement>(), isOpen = ref(false)
const lifecycle = new AbortController()
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let previousOverflow = '', previousFocus: HTMLElement | null = null
let backdropPointer = false
const dirty = computed(() => loaded.value && JSON.stringify(config.value) !== JSON.stringify(saved.value))
const blocked = computed(() => busy.value || running.value || !loaded.value)
const inFlight = computed(() => submitting.value || running.value)
const ready = computed(() => !!props.accounts.find(a => a.primary && a.configured) && bindings.value.some(b => b.userId === config.value.userId))
const visibleRecords = computed(() => showAll.value ? records.value : records.value.slice(0, 5))
watch(strm, () => { requestId.value = '' })
function acceptStatus(data: Status, replaceConfig: boolean) {
  records.value = data.records || []; running.value = !!data.running; saved.value = { ...data.config }
  if (replaceConfig) config.value = { ...data.config }
  loaded.value = true
}
function scheduleRefresh() {
  clearTimeout(refreshTimer)
  if (running.value && !lifecycle.signal.aborted) refreshTimer = setTimeout(() => void load(), 4000)
}
async function load(replaceConfig = false) {
  if (busy.value) return
  clearTimeout(refreshTimer)
  busy.value = true; error.value = ''
  try {
    const [status, users]: [Status, { users: { Id: string; Policy?: { IsDisabled?: boolean } }[]; members: { embyId: string; embyName: string; source: string; playbackOnly?: boolean; suspended?: boolean }[] }] = await Promise.all([
      embyRequest(props.token, 'instant-upload', lifecycle.signal),
      embyRequest(props.token, 'playback-cache/users', lifecycle.signal),
    ])
    bindings.value = (users.members || []).filter(m => m.source === 'assigned' && !m.playbackOnly && !m.suspended && users.users.some(u => u.Id === m.embyId && !u.Policy?.IsDisabled)).map(m => ({ userId: m.embyId, userName: m.embyName }))
    acceptStatus(status, replaceConfig || !loaded.value)
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取秒传配置失败' }
  finally { busy.value = false; scheduleRefresh() }
}
async function save() {
  if (blocked.value || !ready.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    acceptStatus(await embyRequest(props.token, 'instant-upload', lifecycle.signal, { userId: config.value.userId }), true)
    requestId.value = ''; notice.value = '秒传配置已保存。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存失败' }
  finally { busy.value = false; scheduleRefresh() }
}
async function run() {
  if (blocked.value || dirty.value || !ready.value || !strm.value.trim()) return
  busy.value = true; submitting.value = true; error.value = ''; notice.value = ''
  if (!requestId.value) requestId.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
  try {
    const result: Record = await embyRequest(props.token, 'instant-upload/run', lifecycle.signal, { id: requestId.value, strm: strm.value.trim() })
    records.value = [result, ...records.value.filter(item => item.id !== result.id)].slice(0, 20)
    running.value = result.status === 'running'; notice.value = result.message
    // Preserve the ID until the sample or saved target changes, including on ambiguous failures.
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = `${reason instanceof Error ? reason.message : '验证请求失败'}。如请求已发出，请刷新记录并核查目标目录。`
  } finally { busy.value = false; submitting.value = false; scheduleRefresh() }
}
function open() {
  if (!dialog.value || isOpen.value) return
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow
  dialog.value.showModal(); isOpen.value = true; document.body.style.overflow = 'hidden'
}
function restorePage() {
  if (!isOpen.value) return
  isOpen.value = false; document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
}
function close() { dialog.value?.close(); restorePage() }
function backdropClick(event: MouseEvent) {
  if (backdropPointer && event.target === dialog.value) close()
  backdropPointer = false
}
function recordTime(value: string) {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '时间未记录'
}
function statusLabel(value: string) { return ({ success: '秒传成功', unsupported: '需要普通上传', uncertain: '结果待确认', failed: '验证失败', running: '验证中' } as { [key: string]: string })[value] || value }
function accountName(id: string) { return props.accounts.find(account => account.id === id)?.name || (id ? '个人网盘或账号已删除' : '网盘尚未就绪') }
defineExpose({ open, dirty, inFlight })
onMounted(() => load(true))
onBeforeUnmount(() => { clearTimeout(refreshTimer); close(); lifecycle.abort() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="proxy-upload-dialog proxy-ui" aria-labelledby="proxy-upload-title" aria-describedby="proxy-upload-description" @cancel.prevent="close" @close="restorePage" @pointerdown="event => { backdropPointer = event.target === dialog }" @click="backdropClick">
      <div class="proxy-upload-shell">
        <header class="proxy-upload-header">
          <div><h2 id="proxy-upload-title">单文件秒传验证</h2><p id="proxy-upload-description" class="proxy-help mt-1">池用户在任务启动时选择小号并固定目录，写入后不自动换号。</p></div>
          <button type="button" class="proxy-dialog-close" aria-label="关闭秒传验证" @click="close"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        </header>
        <div class="proxy-upload-content">
          <p v-if="inFlight" role="status" class="proxy-upload-progress">验证正在执行。可以关闭飞窗，任务和草稿会保留在当前页面。</p>
          <p v-if="error" role="alert" class="proxy-alert is-error mb-4">{{ error }}</p>
          <p v-if="notice" role="status" class="proxy-alert is-success mb-4">{{ notice }}</p>
          <div class="proxy-upload-target">
            <form class="space-y-3" @submit.prevent="save">
              <label class="block text-sm">目标用户<select v-model="config.userId" aria-label="目标用户" :disabled="blocked" class="proxy-input mt-2" required @change="requestId = ''"><option value="" disabled>请选择动态池用户</option><option v-for="binding in bindings" :key="binding.userId" :value="binding.userId">{{ binding.userName }} · 动态小号池</option></select></label>
              <p class="proxy-help">目标网盘和已验证目录在启动时自动选择并固定。</p>
              <p v-if="loaded && !bindings.length" class="proxy-help">暂无可用的动态池用户，请先配置用户来源。</p>
              <p v-if="!accounts.some(a => a.primary && a.configured)" class="proxy-help">需要先配置主账号和动态小号池。</p>
              <div class="flex flex-wrap items-center gap-4"><button type="submit" :disabled="blocked || !ready" class="proxy-button">保存秒传配置</button><RouterLink to="/emby/users" class="proxy-text-link">管理用户来源</RouterLink></div>
            </form>
          </div>
          <div class="mt-5">
            <label for="instant-strm" class="text-sm">单个 STRM 文件中的播放地址</label>
            <textarea id="instant-strm" v-model="strm" :disabled="blocked" rows="3" spellcheck="false" autocomplete="off" placeholder="打开一个 .strm 文件，粘贴其中的完整地址" class="proxy-input mt-2" />
            <p class="proxy-help mt-2">仅尝试秒传，115 要求普通上传时停止；校验最多读取 8 MB 片段。验证文件会保留，不纳入播放缓存的定期清理。</p>
            <p v-if="requestId && !inFlight" class="proxy-help mt-2">相同目标和地址重复提交会复用本次结果，避免重复写入。</p>
            <p v-if="dirty" class="proxy-pending">请先保存目标用户，再开始验证。</p>
            <button type="button" :disabled="blocked || dirty || !ready || !strm.trim()" class="proxy-button mt-3" @click="run">{{ inFlight ? '正在验证…' : '开始单文件秒传' }}</button>
          </div>
          <section class="mt-6" aria-labelledby="proxy-records-title">
            <div class="flex items-center justify-between gap-3"><h3 id="proxy-records-title" class="text-sm font-semibold">最近验证记录</h3><button type="button" :disabled="busy" class="proxy-text-link" @click="load()">刷新记录</button></div>
            <p v-if="loaded && !records.length" class="proxy-help mt-3">还没有验证记录。</p>
            <div class="mt-3 space-y-2">
              <article v-for="record in visibleRecords" :key="record.id" class="proxy-upload-record">
                <div class="flex items-center justify-between gap-3"><span class="proxy-record-state" :class="record.status">{{ statusLabel(record.status) }}</span><time class="proxy-help">{{ recordTime(record.startedAt) }}</time></div>
                <p v-if="record.name" class="mt-2 break-all text-sm font-medium">{{ record.name }}</p>
                <p class="proxy-help mt-1">{{ record.message }}</p>
                <details class="proxy-details mt-2"><summary>查看结果详情</summary><p>目标：{{ accountName(record.accountId) }} · 目录 {{ record.targetCid }} · 耗时 {{ ((record.durationMs || 0) / 1000).toFixed(2) }} 秒<span v-if="record.challenged"> · 已完成片段校验</span></p><p v-if="record.fileId || record.pickCode" class="break-all"><span v-if="record.fileId">目标文件 ID：{{ record.fileId }} </span><span v-if="record.pickCode">PickCode：{{ record.pickCode }}</span></p></details>
              </article>
            </div>
            <button v-if="records.length > 5" type="button" class="proxy-text-link mt-3" @click="showAll = !showAll">{{ showAll ? '收起记录' : '查看全部 ' + records.length + ' 条' }}</button>
          </section>
        </div>
        <footer class="proxy-upload-footer"><span class="proxy-help">关闭飞窗会保留当前目标与 STRM 草稿。</span><button type="button" class="proxy-tool" @click="close">完成</button></footer>
      </div>
    </dialog>
  </Teleport>
</template>
