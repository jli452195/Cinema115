<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconSearch, IconX, IconRefresh, IconTrash, IconCheck, IconChevronRight, IconListDetails } from '@tabler/icons-vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import PaginationControl from '../components/PaginationControl.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import { useMediaOrganizeHistoryUpdates } from '../composables/useMediaOrganizeHistoryUpdates'
import { mediaOrganizeRequest, MediaOrganizeRequestError, type MediaHistory, type MediaHistoryItem, type MediaHistoryStatus, type MediaTask, type MediaGroup, type MediaEntry, type MediaRetryBatch, type MediaBatchRetryResult } from '../lib/media-organize-api'

const props = defineProps<{ token: string }>()
const route = useRoute(), router = useRouter()
const filters: { value: MediaHistoryStatus; label: string }[] = [{ value: 'all', label: '全部' }, { value: 'running', label: '整理中' }, { value: 'done', label: '成功' }, { value: 'failed', label: '失败' }]
const filter = computed<MediaHistoryStatus>(() => filters.some(item => item.value === route.query.status) ? route.query.status as MediaHistoryStatus : 'all')
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const page = computed(() => typeof route.query.page === 'string' && /^\d+$/.test(route.query.page) && Number.isSafeInteger(Number(route.query.page)) ? Math.max(1, Number(route.query.page)) : 1)
const searchText = ref(keyword.value)
const history = ref<MediaHistory>({ items: [], page: 1, pages: 1, total: 0, counts: { all: 0, running: 0, done: 0, failed: 0 }, active: false, activity: [], cleanup: [] })
const filterOptions = computed(() => filters.map(item => ({ ...item, count: history.value.counts[item.value] })))
const loading = ref(true), refreshing = ref(false), busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref('')
const bulk = ref(false), selected = ref(new Set<string>())
const key = (item: MediaHistoryItem) => `${item.runId}:${item.fileId}`
const selectedItems = computed(() => history.value.items.filter(item => selected.value.has(key(item)) && (item.canDelete || item.canRetry)))
const selectedDeleteItems = computed(() => selectedItems.value.filter(item => item.canDelete))
const selectedRetryItems = computed(() => selectedItems.value.filter(item => item.canRetry))
const selectable = computed(() => history.value.items.filter(item => item.canDelete || item.canRetry))
const allSelected = computed(() => selectable.value.length > 0 && selectedItems.value.length === selectable.value.length)
const retryBatchActive = computed(() => !!history.value.retryBatch && ['running', 'stopping'].includes(history.value.retryBatch.status))
const retryBatchLabels = { running: '整理中', stopping: '正在停止', completed: '已完成', stopped: '已停止', interrupted: '已中断' }
const deletion = ref<{ status: 'done' | 'failed' } | { items: MediaHistoryItem[] }>()
const current = ref<MediaTask>(), taskSnapshot = ref(''), matchChanged = ref(false), leaveDialog = ref(false)
const detailLoading = ref('')
const primary = computed(() => current.value?.entries.find(entry => entry.file.id === current.value?.fileId))
const attachments = computed(() => current.value?.entries.filter(entry => entry.file.id !== current.value?.fileId) || [])
const taskProcessing = computed(() => !!current.value && (running(current.value.status) || !current.value.canDelete && !current.value.canRetry))
const specificationLabels: Record<string, string> = { resolution: '分辨率', source: '资源来源', platform: '平台', videoCodec: '视频编码', bitDepth: '位深', dynamicRange: '动态范围', audioCodec: '音频编码', channels: '声道', audioEffect: '音效', frameRate: '帧率', languages: '字幕语言', subtitleMode: '字幕形式', releaseGroup: '发布组', edition: '版本' }
const specifications = computed(() => Object.entries(primary.value?.file.namingValues || {}).filter(([field, value]) => specificationLabels[field] && value.trim()).map(([field, value]) => ({ label: specificationLabels[field], value })))
const workSummary = computed(() => { const task = current.value; if (!task) return ''; const group = task.group, file = primary.value?.file; return group.tmdbId ? [group.kind === 'movie' ? '电影' : '剧集', group.year, `TMDB ${group.tmdbId}`, episodeLabel({ kind: group.kind, season: file?.season ?? task.season, episode: file?.episode ?? task.episode, episodeExplicit: file?.episodeExplicit ?? task.episodeExplicit, endEpisode: file?.endEpisode })].filter(Boolean).join(' · ') : group.message })
const editable = computed(() => primary.value?.status !== 'done')
const dirty = computed(() => !!current.value && editable.value && taskSignature(current.value) !== taskSnapshot.value)
const query = ref(''), searchGroup = ref(''), candidates = ref<{ id: number; kind: string; title: string; date: string }[]>([])
const running = (status: string) => ['queued', 'scanning', 'running'].includes(status)
const taskLabels: Record<string, string> = { done: '成功', running: '整理中', failed: '失败' }
const batchLabels: Record<string, string> = { queued: '排队中', scanning: '识别中', running: '整理中', completed: '扫描完成', partial: '扫描结束', failed: '扫描失败', interrupted: '已中断' }
const labels: Record<string, string> = { ready: '可整理', needs_match: '待匹配', invalid: '待修正', conflict: '冲突', error: '失败', done: '已完成', unmatched: '未关联', retained: '已保留', pending: '等待整理', waiting: '等待中', unconfirmed: '失败' }
const lifecycle = new AbortController()
let reader = new AbortController(), detailReader = new AbortController(), readVersion = 0, detailVersion = 0, mutationVersion = 0, generation = 0, canonicalPage: number | undefined
let normalizedPath: string | undefined
let leaveDecision: ((value: boolean) => void) | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
function statusClass(status: string) { return status === 'done' || status === 'completed' ? 'mo-success' : running(status) ? 'mo-processing' : 'mo-failure' }
function resultTime(at: string) { const date = new Date(at); return !at || at.startsWith('0001-') || !Number.isFinite(date.getTime()) ? '完成时间未知' : date.toLocaleString() }
function taskSignature(task: MediaTask) { const file = task.entries.find(entry => entry.file.id === task.fileId)?.file; return JSON.stringify([task.group.kind, task.group.tmdbId, file?.season, file?.episode, !!file?.episodeExplicit]) }
async function request<T>(path: string, init: RequestInit = {}) { return mediaOrganizeRequest<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, ...(init.signal ? [init.signal] : [])]) }) }
function acceptTask(task: MediaTask) {
  const { rule, group, entries, ...item } = task
  history.value.items = history.value.items.map(row => key(row) === key(item) ? item : row)
  current.value = task; taskSnapshot.value = taskSignature(task)
}
function cancelDetail() { detailReader.abort(); detailVersion++; detailLoading.value = '' }
async function loadDetail(item: MediaHistoryItem, preserveDraft = false): Promise<boolean> {
  if (preserveDraft && detailLoading.value === key(item)) return true
  cancelDetail(); detailReader = new AbortController()
  const signal = detailReader.signal, version = detailVersion, mutation = mutationVersion, taskGeneration = generation
  detailLoading.value = key(item)
  try {
    const task = await request<MediaTask>(`/runs/${item.runId}/task?fileId=${encodeURIComponent(item.fileId)}`, { signal })
    if (signal.aborted || version !== detailVersion || mutation !== mutationVersion || taskGeneration !== generation || preserveDraft && (!current.value || key(current.value) !== key(item))) return false
    const draft = preserveDraft && dirty.value && current.value ? { group: current.value.group, season: primary.value?.file.season, episode: primary.value?.file.episode, episodeExplicit: primary.value?.file.episodeExplicit } : undefined
    acceptTask(task)
    if (draft && current.value && primary.value?.status !== 'done') { current.value.group = draft.group; if (primary.value) { primary.value.file.season = draft.season!; primary.value.file.episode = draft.episode!; primary.value.file.episodeExplicit = draft.episodeExplicit } } else matchChanged.value = false
    return true
  } catch (reason) {
    if (signal.aborted || lifecycle.signal.aborted || version !== detailVersion || mutation !== mutationVersion || taskGeneration !== generation) return false
    if (reason instanceof MediaOrganizeRequestError && reason.status === 404) {
      if (current.value && key(current.value) === key(item)) closeRun(); else cancelDetail()
      dialogError.value = ''; notice.value = '当前任务已结束或记录已移除'
      return refresh()
    } else (preserveDraft ? dialogError : error).value = message(reason)
    return false
  }
  finally { if (version === detailVersion) detailLoading.value = '' }
}
async function refresh(): Promise<boolean> {
  if (document.hidden || lifecycle.signal.aborted) return false
  reader.abort(); reader = new AbortController()
  const signal = reader.signal, version = ++readVersion, mutation = mutationVersion
  refreshing.value = true
  try {
    const params = new URLSearchParams({ status: filter.value, page: String(page.value) })
    if (keyword.value) params.set('q', keyword.value)
    const value = await request<MediaHistory>(`/history?${params}`, { signal })
    if (signal.aborted || version !== readVersion || mutation !== mutationVersion) return false
    history.value = value
    selected.value = new Set(value.items.filter(item => (item.canDelete || item.canRetry) && selected.value.has(key(item))).map(key))
    if (value.page !== page.value) { canonicalPage = value.page; normalizePage() }
    const listed = value.items.find(item => current.value && key(item) === key(current.value))
    error.value = ''
    if (!busy.value && current.value && (taskProcessing.value || listed && (listed.status !== current.value.status || listed.at !== current.value.at || listed.canDelete !== current.value.canDelete || listed.canRetry !== current.value.canRetry))) return await loadDetail(current.value, true)
    return true
  } catch (reason) { if (!signal.aborted && !lifecycle.signal.aborted && version === readVersion && mutation === mutationVersion) error.value = message(reason); return false }
  finally { if (version === readVersion) { if (!signal.aborted) loading.value = false; refreshing.value = false } }
}
async function act(name: string, fn: () => Promise<void>, dialog = false) {
  if (busy.value) return
  cancelDetail()
  busy.value = name; (dialog ? dialogError : error).value = ''; notice.value = ''
  try { await fn() } catch (reason) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = message(reason) }
  finally { busy.value = ''; normalizePage() }
}
function normalizePage() {
  if (canonicalPage === undefined || busy.value || dirty.value) return
  const value = canonicalPage; canonicalPage = undefined
  if (value === page.value) return
  const target = { query: { ...route.query, page: value > 1 ? String(value) : undefined } }
  const path = router.resolve(target).fullPath
  normalizedPath = path
  void router.replace(target).finally(() => { if (normalizedPath === path) normalizedPath = undefined })
}
function navigate(status = filter.value, q = keyword.value, targetPage = 1) {
  if (busy.value) return
  void router.push({ query: { ...(status !== 'all' ? { status } : {}), ...(q ? { q } : {}), ...(targetPage > 1 ? { page: String(targetPage) } : {}) } })
}
function submitSearch() { const q = searchText.value.trim(); if (q === keyword.value && page.value === 1) void refresh(); else navigate(filter.value, q) }
function clearSearch() { searchText.value = ''; navigate(filter.value, '') }
function clearSelection() { selected.value = new Set(); bulk.value = false }
function selectAll() { selected.value = allSelected.value ? new Set() : new Set(selectable.value.map(key)) }
async function cardClick(item: MediaHistoryItem) {
  if (busy.value) return
  if (bulk.value) { if (item.canDelete || item.canRetry) { const next = new Set(selected.value); next.has(key(item)) ? next.delete(key(item)) : next.add(key(item)); selected.value = next }; return }
  if (dirty.value && !await guard()) return
  generation++; matchChanged.value = false; dialogError.value = ''; error.value = ''; searchGroup.value = ''; candidates.value = []
  await loadDetail(item)
}
function closeRun() { cancelDetail(); current.value = undefined; generation++; searchGroup.value = ''; candidates.value = []; normalizePage() }
async function requestCloseRun() { if (await guard()) closeRun() }
function askDelete(items: MediaHistoryItem[]) { if (!items.length || items.some(item => !item.canDelete)) return; dialogError.value = ''; deletion.value = { items: [...items] } }
function askClear(status: 'done' | 'failed') { dialogError.value = ''; deletion.value = { status } }
async function removeRecords() {
  const intent = deletion.value
  if (!intent) return
  await act('delete', async () => {
    const body = 'status' in intent ? intent : { items: intent.items.map(({ runId, fileId }) => ({ runId, fileId })) }
    // 删除回执失败时不自动重试，避免再次清理新产生的记录。
    deletion.value = undefined
    try {
      const result = await request<{ deleted: number }>('/history', { method: 'DELETE', body: JSON.stringify(body) })
      mutationVersion++; closeRun(); clearSelection(); notice.value = `已删除 ${result.deleted} 条整理记录，实际文件保持不变`
    } catch (reason) {
      const failure = `删除结果尚未确认：${message(reason)}。请刷新核对后再选择。`
      mutationVersion++; closeRun(); clearSelection(); await refresh(); error.value = failure
      return
    }
    await refresh()
  })
}
async function retry() {
  await act('retry', async () => {
    const task = current.value
    if (!task || !task.canRetry) return
    if (dirty.value && editable.value && task.group.kind === 'tv') {
      const file = primary.value?.file
      if (!file || !Number.isInteger(file.season) || file.season < 0 || file.season > 1000 || !Number.isInteger(file.episode) || file.episode < 0 || file.episode > 10000 || file.episode === 0 && !file.episodeExplicit) throw new Error('请填写有效的目标季集')
    }
    const correction = dirty.value && editable.value ? { ...(task.group.kind === 'tv' ? { season: primary.value?.file.season, episode: primary.value?.file.episode } : {}), ...(matchChanged.value ? { kind: task.group.kind, tmdbId: task.group.tmdbId } : {}) } : {}
    const updated = await request<MediaTask>(`/runs/${task.runId}/retry`, { method: 'POST', body: JSON.stringify({ fileId: task.fileId, ...correction }) })
    mutationVersion++; acceptTask(updated); matchChanged.value = false; void refresh()
  }, true)
}
async function retryRecords(items?: MediaHistoryItem[]) {
  if (retryBatchActive.value || items && !items.length) return
  await act('batch-retry', async () => {
    const body = items ? { items: items.map(({ runId, fileId }) => ({ runId, fileId })) } : { status: 'failed', q: keyword.value }
    try {
      const result = await request<MediaBatchRetryResult>('/history/retry', { method: 'POST', body: JSON.stringify(body) })
      mutationVersion++; clearSelection(); history.value.retryBatch = result.batch
      const reasons = Object.entries(result.reasons || {}).map(([reason, count]) => `${reason} ${count} 条`).join('，')
      notice.value = `已提交 ${result.queued} 条重试，跳过 ${result.skipped} 条${reasons ? `（${reasons}）` : ''}`
    } catch (reason) {
      // 回执丢失时先核对进度，不自动再次提交搜索集合。
      mutationVersion++; clearSelection(); await refresh(); error.value = `批量重试结果尚未确认：${message(reason)}。请刷新核对进度后再操作。`
      return
    }
    await refresh()
  })
}
async function stopRetryBatch() {
  const batch = history.value.retryBatch
  if (!batch || batch.status !== 'running') return
  await act('stop-retry', async () => {
    try {
      const result = await request<MediaRetryBatch>(`/history/retry/${encodeURIComponent(batch.id)}/stop`, { method: 'POST' })
      mutationVersion++; history.value.retryBatch = result
      notice.value = '已请求停止剩余重试，正在处理的文件将继续核对结果'
    } catch (reason) {
      mutationVersion++; await refresh(); error.value = `停止结果尚未确认：${message(reason)}。请刷新核对进度。`
      return
    }
    await refresh()
  })
}
function match(group: MediaGroup) { searchGroup.value = group.id; query.value = group.identity.name; candidates.value = []; dialogError.value = '' }
async function search() {
  const q = query.value.trim(), version = generation, group = searchGroup.value
  if (!q) return
  await act('search', async () => {
    const response = await fetch(`/api/admin/tmdb/browse?kind=multi&query=${encodeURIComponent(q)}&page=1`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(25000)]) })
    const data = await response.json(); if (!response.ok) throw new Error(data.error || '搜索失败')
    if (version === generation && group === searchGroup.value && q === query.value.trim()) candidates.value = (data.items || []).filter((item: { kind: string }) => ['movie', 'tv'].includes(item.kind))
  }, true)
}
function pick(candidate: { id: number; kind: string; title: string; date?: string }) { if (current.value) { Object.assign(current.value.group, { tmdbId: candidate.id, kind: candidate.kind, title: candidate.title, year: candidate.date?.slice(0, 4) }); matchChanged.value = true }; searchGroup.value = ''; candidates.value = [] }
function episodeLabel(item: { kind?: string; season: number; episode: number; episodeExplicit?: boolean; endEpisode?: number }) { return item.kind === 'tv' && (item.episode > 0 || item.episode === 0 && item.episodeExplicit) ? `S${String(item.season).padStart(2, '0')}E${String(item.episode).padStart(2, '0')}${item.endEpisode && item.endEpisode > item.episode ? `-E${String(item.endEpisode).padStart(2, '0')}` : ''}` : '' }
function entryAwaitingVideo(entry: MediaEntry) { return !!entry.videoId && (entry.resultCode === 'WAITING_MAIN_FILE' || ['pending', 'ready', 'waiting', 'unmatched'].includes(entry.status) || entry.status === 'error' && !entry.target) && current.value?.entries.some(video => video.file.id === entry.videoId && video.status !== 'done') }
function entryProcessing(entry: MediaEntry) { return !!current.value && entry.file.id === current.value.fileId && running(current.value.status) }
function entryOperationPending(entry: MediaEntry) { return !entryProcessing(entry) && entry.file.id === current.value?.fileId && (entry.failure?.state === 'moving' || !!entry.p115Move) }
function entryWaiting(entry: MediaEntry) { return !entryOperationPending(entry) && entry.status !== 'done' && (['pending', 'ready', 'waiting', 'retained', 'unmatched'].includes(entry.status) || entryAwaitingVideo(entry)) }
function entryFailed(entry: MediaEntry) { return !entryProcessing(entry) && entry.status !== 'done' && !entryWaiting(entry) && (entryOperationPending(entry) || ['error', 'failed', 'conflict', 'unconfirmed'].includes(entry.status)) }
function entryLabel(entry: MediaEntry) { if (entryProcessing(entry)) return '整理中'; if (entry.status === 'done') return '已完成'; if (entryOperationPending(entry) || entry.status === 'unconfirmed') return '失败'; if (entryAwaitingVideo(entry)) return '等待主文件'; return labels[entry.status] || entry.status }
function fileName(path?: string) { return path?.split(/[\\/]/).pop() || '' }
function sourcePath(entry: MediaEntry) { return entry.failure?.state === 'stored' ? `${current.value?.rule.failure}/${entry.file.sourcePath}` : `${current.value?.rule.source}/${entry.file.sourcePath}` }
function failureNotice(entry: MediaEntry) { if (entryProcessing(entry)) return ''; if (entry.file.id !== current.value?.fileId) return entry.failure?.state === 'moving' || entry.p115Move ? '本次附件处理已结束，保留现有文件。' : ''; if (entry.failure?.state === 'stored') return '已移入失败目录；可在此修正匹配并重试当前任务。'; if (entry.failure?.state === 'moving') return `移入结果尚未确认，重试时将先核对文件位置${entry.failure.message ? `：${entry.failure.message}` : '。'}`; if (entry.p115Move) return '文件移动结果尚未确认，重试时将先核对文件位置。'; return entry.failure?.message ? `未能移入失败目录，文件保留原处：${entry.failure.message}` : '' }
useMediaOrganizeHistoryUpdates(() => props.token, async () => {
  if (loading.value || page.value === 1 || retryBatchActive.value) return refresh()
  if (current.value && taskProcessing.value) return loadDetail(current.value, true)
  return true
}, () => !busy.value && !refreshing.value && !detailLoading.value && (!bulk.value || retryBatchActive.value) && !deletion.value && (loading.value || page.value === 1 || taskProcessing.value || retryBatchActive.value))
function visible() { if (document.hidden) { reader.abort(); cancelDetail() } }
function decideLeave(value: boolean) { leaveDialog.value = false; leaveDecision?.(value); leaveDecision = undefined }
function guard() {
  leaveDecision?.(false); leaveDecision = undefined
  if (busy.value) { error.value = '正在处理当前操作，请完成后再切换页面'; if (current.value || deletion.value) dialogError.value = error.value; return false }
  if (!dirty.value) { leaveDialog.value = false; return true }
  leaveDialog.value = true; return new Promise<boolean>(resolve => { leaveDecision = resolve })
}
function beforeUnload(event: BeforeUnloadEvent) { if (busy.value || dirty.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteLeave(guard)
onBeforeRouteUpdate(guard)
watch(() => route.fullPath, value => {
  if (value === normalizedPath) { normalizedPath = undefined; return }
  canonicalPage = undefined; searchText.value = keyword.value; closeRun(); clearSelection(); notice.value = ''; void refresh()
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); document.addEventListener('visibilitychange', visible) })
onBeforeUnmount(() => { lifecycle.abort(); reader.abort(); cancelDetail(); window.removeEventListener('beforeunload', beforeUnload); document.removeEventListener('visibilitychange', visible); decideLeave(false) })
</script>

<template>
  <section class="mo-page mo-results" aria-label="整理记录">
    <div class="mo-record-toolbar">
      <SegmentedControl class="mo-status-filter" role="group" label="记录状态" :model-value="filter" :options="filterOptions" :disabled="!!busy" @update:model-value="navigate($event)" />
      <div class="mo-record-actions"><button type="button" class="mo-button" :disabled="loading || !!busy" @click="bulk ? clearSelection() : bulk = true">{{ bulk ? '退出管理' : '批量管理' }}</button><button v-for="status in (['done', 'failed'] as const)" :key="status" type="button" class="mo-button" :disabled="loading || !!busy" @click="askClear(status)"><IconTrash :size="16" aria-hidden="true" />清理全部{{ taskLabels[status] }}</button><button type="button" class="mo-icon" aria-label="刷新记录" :disabled="refreshing || !!busy" @click="refresh"><IconRefresh :size="18" aria-hidden="true" /></button></div>
    </div>
    <form class="mo-record-search" @submit.prevent="submitSearch"><IconSearch :size="18" aria-hidden="true" /><input v-model="searchText" type="search" aria-label="搜索整理记录" placeholder="搜索片名、文件名或路径" :disabled="!!busy" /><button v-if="searchText" type="button" class="mo-icon" aria-label="清空搜索" :disabled="!!busy" @click="clearSearch"><IconX :size="16" aria-hidden="true" /></button><button type="submit" class="mo-button mo-primary" :disabled="!!busy">搜索记录</button></form>
    <div class="mo-retry-search"><button type="button" class="mo-button" :disabled="loading || !!busy || retryBatchActive || !history.counts.failed" @click="retryRecords()"><IconRefresh :size="16" aria-hidden="true" />{{ keyword ? '重试当前搜索全部失败' : '重试全部失败' }}</button><span class="mo-muted">{{ keyword ? `范围：搜索「${keyword}」的全部失败记录，包含其他页` : '范围：全部失败记录，包含其他页' }}</span></div>
    <p v-if="error" role="alert" class="mo-error">{{ error }}</p>
    <p v-if="notice" role="status" class="mo-muted">{{ notice }}</p>
    <p v-if="detailLoading && !current" role="status" class="mo-muted">正在读取整理详情…</p>
    <div v-if="history.activity.length" class="mo-activity" aria-label="扫描状态"><p v-for="item in history.activity" :key="item.runId"><span class="mo-chip" :class="statusClass(item.status)">{{ batchLabels[item.status] || '扫描失败' }}</span><strong>{{ item.ruleName }}</strong><span>{{ item.message || (running(item.status) ? '正在扫描来源并识别文件，完成后显示整理结果' : '本次扫描未生成媒体任务') }}</span></p></div>
    <div v-if="history.retryBatch" class="mo-retry-progress" role="status" aria-label="批量重试进度"><div><strong>批量重试{{ retryBatchLabels[history.retryBatch.status] }}</strong><span>共 {{ history.retryBatch.total }} 条 · 待处理 {{ history.retryBatch.pending }} · 成功 {{ history.retryBatch.success }} · 失败 {{ history.retryBatch.failed }} · 跳过 {{ history.retryBatch.skipped }}</span><span v-if="history.retryBatch.status === 'stopping'" class="mo-muted">正在处理的文件继续核对结果</span></div><button v-if="retryBatchActive" type="button" class="mo-button" :disabled="!!busy || history.retryBatch.status === 'stopping'" @click="stopRetryBatch">停止剩余重试</button></div>
    <div v-if="bulk" class="mo-bulk-bar"><label><input type="checkbox" :checked="allSelected" :disabled="!!busy || !selectable.length" @change="selectAll" />全选当前页</label><span>已选 {{ selectedItems.length }} 条</span><div class="mo-bulk-actions"><button type="button" class="mo-button mo-primary" :disabled="!!busy || retryBatchActive || !selectedRetryItems.length" @click="retryRecords(selectedRetryItems)">重试选中 {{ selectedRetryItems.length }} 条</button><button type="button" class="mo-button mo-danger" :disabled="!!busy || !selectedDeleteItems.length" @click="askDelete(selectedDeleteItems)">删除选中 {{ selectedDeleteItems.length }} 条</button></div></div>
    <p v-if="loading" class="mo-empty" role="status">正在读取整理记录…</p>
    <div v-else-if="!history.items.length" class="mo-empty"><IconListDetails :size="30" aria-hidden="true" /><p>{{ keyword || filter !== 'all' ? '未找到符合条件的整理记录' : history.active ? '正在扫描来源目录，媒体任务将在识别后显示' : '暂无整理记录，可在整理规则中立即运行或启用监控' }}</p><button v-if="keyword || filter !== 'all'" type="button" class="mo-button" @click="navigate('all', '')">清空筛选</button></div>
    <div v-else class="mo-history-grid" :aria-busy="refreshing">
      <button v-for="item in history.items" :key="key(item)" type="button" class="mo-result" :class="{ 'is-selected': bulk && selected.has(key(item)) }" :disabled="!!busy || bulk && !item.canDelete && !item.canRetry" :aria-label="`${bulk ? '选择' : '查看'}${item.name}的整理记录`" :aria-pressed="bulk ? selected.has(key(item)) : undefined" :aria-busy="detailLoading === key(item)" :title="bulk && !item.canDelete && !item.canRetry ? '当前文件正在操作，暂不能选择' : undefined" @click="cardClick(item)">
        <span class="mo-result-heading"><strong><span class="mo-result-name" :title="item.title || item.name">{{ item.title || item.name }}</span><span v-if="episodeLabel(item)" class="mo-episode">{{ episodeLabel(item) }}</span></strong><span v-if="bulk" class="mo-selection" aria-hidden="true"><IconCheck v-if="selected.has(key(item))" :size="14" /></span><IconChevronRight v-else :size="16" aria-hidden="true" /></span>
        <span class="mo-result-meta"><span class="mo-chip mo-status-chip" :class="statusClass(item.status)">{{ taskLabels[item.status] || '失败' }}</span><span class="mo-chip mo-kind">{{ item.kind === 'movie' ? '电影' : item.kind === 'tv' ? '剧集' : '待识别' }}</span><span class="mo-chip mo-format">{{ item.name.split('.').pop()?.toUpperCase() }}</span><span v-if="item.attachmentCount" class="mo-chip mo-format">附件 {{ item.attachmentCount }}</span></span>
        <span class="mo-result-path"><span>原名</span><code :title="item.source">{{ item.name }}</code></span><span v-if="item.target" class="mo-result-path"><span>目标</span><code :title="item.target">{{ item.target.split('/').pop() }}</code></span>
        <span v-if="item.message" class="mo-result-message" :class="['error', 'failed', 'conflict'].includes(item.status) ? 'mo-red' : 'mo-muted'" :title="item.message">{{ item.message }}</span><time :datetime="item.at.startsWith('0001-') ? undefined : item.at">{{ resultTime(item.at) }}</time>
      </button>
    </div>
    <PaginationControl v-if="history.pages > 1" :page="history.page" :pages="history.pages" :disabled="refreshing || !!busy" label="整理记录分页" @change="value => navigate(filter, keyword, value)" />
    <details v-if="history.cleanup.length" class="mo-cleanup"><summary>来源清理 <span>文件 {{ history.cleanup[0].files }} · 空目录 {{ history.cleanup[0].directories }}<span v-if="history.cleanup[0].failed" class="mo-red"> · 失败 {{ history.cleanup[0].failed }}</span></span></summary><p v-for="item in history.cleanup" :key="item.runId"><strong>{{ item.ruleName }}</strong><span>文件 {{ item.files }} · 空目录 {{ item.directories }}<span v-if="item.failed" class="mo-red"> · 失败 {{ item.failed }}</span></span><time>{{ new Date(item.at).toLocaleString() }}</time></p></details>
  </section>
    <FloatingDialog v-if="current" class="mo-preview-dialog" title="整理详情" close-label="关闭整理详情" size="organize" :close-disabled="!!busy" @close="requestCloseRun">
      <template #header><div class="mo-title"><h3>{{ current.group.title || current.name }}</h3><span class="mo-chip" :class="statusClass(current.status)">{{ taskLabels[current.status] || '失败' }}</span></div><p class="mo-muted">{{ current.rule.name }} · {{ resultTime(current.at) }}</p><p v-if="current.message && current.message !== primary?.message && current.message !== current.group.message && !running(current.status)" class="mo-muted">{{ current.message }}</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p></template>
      <div class="mo-preview">
        <p v-if="running(current.status)" class="mo-muted" role="status">正在处理当前任务，关闭弹窗后继续。</p>
        <section class="mo-work">
          <header><div><h3>主文件</h3><p class="mo-muted">{{ workSummary }}<span v-if="current.group.category" class="mo-meta">{{ current.group.category }}</span></p></div><button v-if="editable && !running(current.status)" class="mo-button" :disabled="!!busy" @click="match(current.group)">{{ current.group.tmdbId ? '更换作品' : '匹配作品' }}</button></header>
          <form v-if="searchGroup" class="mo-search" @submit.prevent="search"><div class="mo-input-action"><input v-model="query" aria-label="搜索 TMDB 作品" required placeholder="片名" /><button class="mo-icon" type="submit" :disabled="!!busy" aria-label="搜索作品"><IconSearch :size="18" /></button></div><div class="mo-candidates"><button v-for="candidate in candidates" :key="`${candidate.kind}:${candidate.id}`" type="button"  :disabled="!!busy" @click="pick(candidate)"><strong>{{ candidate.title }}</strong><span>{{ candidate.kind === 'movie' ? '电影' : '剧集' }} · {{ candidate.date?.slice(0, 4) }} · {{ candidate.id }}</span></button><p v-if="busy === 'search'" class="mo-muted" role="status">正在搜索 TMDB 作品…</p><p v-if="!busy && candidates.length === 0" class="mo-muted">输入片名并搜索，选择正确作品。</p></div></form>
          <article v-if="primary" class="mo-file"><div class="mo-file-path"><span class="mo-muted">原始文件名</span><strong>{{ primary.file.name }}</strong><template v-if="primary.target && fileName(primary.target) !== primary.file.name"><span class="mo-muted">{{ primary.status === 'done' ? '当前文件名' : '目标文件名' }}</span><strong>{{ fileName(primary.target) }}</strong></template><span class="mo-muted">{{ primary.failure?.state === 'stored' ? '当前位置' : '来源路径' }}</span><code>{{ sourcePath(primary) }}</code><template v-if="primary.target"><span class="mo-muted">{{ dirty ? '原计划目标（保存后重新计算）' : '目标路径' }}</span><code class="mo-target">{{ primary.target }}</code></template><p v-if="primary.message && !entryProcessing(primary)" :class="entryFailed(primary) ? 'mo-red' : 'mo-muted'">{{ primary.message }}</p><p v-if="failureNotice(primary)" class="mo-muted">{{ failureNotice(primary) }}</p></div><div class="mo-file-controls"><template v-if="editable && current.group.kind === 'tv'"><label>季<input v-model.number="primary.file.season" type="number" min="0" max="1000" :disabled="!!busy || running(current.status)" /></label><label>集<input v-model.number="primary.file.episode" type="number" min="0" @input="primary.file.episodeExplicit = true" max="10000" :disabled="!!busy || running(current.status)" /></label></template><span class="mo-status">{{ entryLabel(primary) }}</span></div></article>
          <dl v-if="specifications.length" class="mo-specifications" aria-label="文件规格"><div v-for="specification in specifications" :key="specification.label"><dt>{{ specification.label }}</dt><dd>{{ specification.value }}</dd></div></dl>
        </section>
        <section v-if="attachments.length" class="mo-work mo-attachments" aria-label="关联文件"><header><h3>关联文件 <span class="mo-muted">{{ attachments.length }} 项</span></h3></header><ul class="mo-attachment-list"><li v-for="entry in attachments" :key="entry.file.id"><div class="mo-attachment-name"><strong>{{ entry.file.name }}</strong><span class="mo-status" :class="{ 'mo-red': entryFailed(entry) }">{{ entryLabel(entry) }}</span></div><p class="mo-muted mo-attachment-path" :title="sourcePath(entry)">{{ sourcePath(entry) }}</p><p v-if="entry.target && entry.target !== sourcePath(entry)" class="mo-attachment-path mo-target" :title="entry.target">{{ entry.target }}</p><p v-if="entry.message" :class="entryFailed(entry) ? 'mo-red' : 'mo-muted'">{{ entry.message }}</p><p v-if="failureNotice(entry)" class="mo-muted">{{ failureNotice(entry) }}</p></li></ul></section>
      </div>
      <template #footer><div class="mo-footer"><span v-if="dirty" class="mo-muted">仅更新当前任务，保存后在后台重新整理。</span><span v-else-if="current.status === 'done' && taskProcessing" class="mo-muted" role="status">正在处理附件，主文件已完成。</span><button class="mo-button mo-danger" :disabled="!!busy || !current.canDelete" @click="askDelete([current])"><IconTrash :size="16" aria-hidden="true" />删除此记录</button><button class="mo-button" :disabled="!!busy" @click="requestCloseRun">关闭</button><button v-if="current.canRetry" class="mo-button mo-primary" :disabled="!!busy || !!searchGroup" @click="retry">{{ current.status === 'done' ? '重试附件' : dirty ? '保存并重试此任务' : '重试此任务' }}</button></div></template>
    </FloatingDialog>

    <P115ActionDialog v-if="deletion" :title="'status' in deletion ? `清理全部${taskLabels[deletion.status]}记录？` : '删除整理记录？'" :busy="busy === 'delete'" @close="deletion = undefined">
      <p>{{ 'status' in deletion ? `将清理所有可删除的${taskLabels[deletion.status]}记录，包括当前搜索和筛选以外的记录。` : `将删除选中的 ${deletion.items.length} 条整理记录。` }}同时结束对应的重试与恢复，不删除实际文件。</p>
      <p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p>
      <template #footer><button type="button" class="mo-button" :disabled="!!busy" @click="deletion = undefined">取消</button><button type="button" class="mo-button mo-danger" :disabled="!!busy" @click="removeRecords">确认删除</button></template>
    </P115ActionDialog>
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的整理修改？" @close="decideLeave(false)"><p>任务修正尚未保存，离开后将丢弃这些修改。后台整理任务会继续运行。</p><template #footer><button type="button" class="mo-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="mo-button mo-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
</template>

<style scoped src="../styles/media-organize.css"></style>
<style scoped>
.mo-specifications{display:flex;flex-wrap:wrap;gap:7px 18px;margin-top:12px;font-size:12px;line-height:1.6}.mo-specifications>div{display:flex;gap:6px;min-width:0}.mo-specifications dt{color:rgb(var(--color-slate-500))}.mo-specifications dd{overflow-wrap:anywhere}.mo-attachment-list{display:grid;gap:0;min-width:0;list-style:none}.mo-attachment-list>li{display:grid;gap:5px;min-width:0;padding:10px 0;font-size:12px;line-height:1.7}.mo-attachment-list>li+li{border-top:1px solid rgb(148 163 184/.12)}.mo-attachment-name{display:flex;justify-content:space-between;align-items:baseline;gap:12px;min-width:0}.mo-attachment-name strong{min-width:0;font-size:13px;font-weight:500;overflow-wrap:anywhere}.mo-attachment-name .mo-status{flex:none;width:auto}.mo-attachment-path{overflow-wrap:anywhere;font-size:11px}.mo-attachment-path.mo-target{color:#2563eb}:global(.dark .mo-attachment-path.mo-target){color:#93c5fd}
.mo-record-toolbar,.mo-record-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.mo-record-toolbar{justify-content:space-between;min-width:0}.mo-record-search{display:flex;gap:8px;align-items:center;max-width:560px;min-width:0}.mo-record-search>svg{color:#64748b;flex:none}.mo-record-search input{flex:1;min-width:0;width:100%;height:38px;background:rgb(148 163 184/.08);border-radius:9px;padding:8px 11px;font-size:13px;outline:none}.mo-record-search input:focus{box-shadow:0 0 0 2px rgb(59 130 246/.3)}.mo-record-search input::-webkit-search-cancel-button{display:none}.mo-bulk-bar{display:flex;align-items:center;gap:16px;flex-wrap:wrap;border-radius:11px;background:#dbeafe;padding:10px 13px;font-size:12px;color:#1e40af}.mo-bulk-bar label{display:inline-flex;align-items:center;gap:8px;min-height:30px}.mo-bulk-bar input{width:17px;height:17px;accent-color:#2563eb}.mo-bulk-actions{display:flex;gap:8px;flex-wrap:wrap;margin-left:auto}.mo-retry-search{display:flex;align-items:center;gap:10px;flex-wrap:wrap;font-size:12px}.mo-retry-progress{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;padding:12px 14px;border-radius:11px;background:rgb(148 163 184/.08);font-size:12px}.mo-retry-progress>div{display:grid;gap:5px;min-width:0}.mo-retry-progress strong{font-weight:600}.mo-danger{color:#b91c1c;background:#fee2e2}.mo-danger:hover{background:#fecaca}.mo-activity{display:grid;gap:7px;color:rgb(var(--color-slate-600));font-size:12px}.mo-activity p{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}.mo-activity strong{font-weight:500}.mo-cleanup{min-width:0;padding:12px 14px;border-radius:12px;background:rgb(148 163 184/.08);font-size:12px;color:rgb(var(--color-slate-600))}.mo-cleanup summary{cursor:pointer}.mo-cleanup summary span{margin-left:8px;color:rgb(var(--color-slate-500))}.mo-cleanup p{display:flex;flex-wrap:wrap;gap:8px 16px;margin-top:12px}.mo-cleanup time{margin-left:auto}.mo-results{min-width:0}.mo-history-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr));gap:12px}.mo-result{display:flex;flex-direction:column;min-width:0;gap:7px;padding:14px;text-align:left;border-radius:14px;border:1px solid transparent;background:#fff;box-shadow:0 2px 8px rgb(15 23 42/.035);transition:background-color .16s,box-shadow .16s}.mo-result:hover{background:#eff6ff;box-shadow:0 4px 16px rgb(37 99 235/.1)}.mo-result.is-selected{background:#dbeafe;border-color:#60a5fa;box-shadow:0 3px 12px rgb(37 99 235/.15)}.mo-selection{width:19px;height:19px;border:1.5px solid #94a3b8;border-radius:5px;display:inline-flex;align-items:center;justify-content:center;flex:none}.is-selected .mo-selection{background:#2563eb;border-color:#2563eb;color:white}.mo-result-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;font-size:14px;line-height:1.6}.mo-result-heading strong{display:flex;align-items:center;flex:1;min-width:0;gap:8px;font-weight:600}.mo-result-heading>svg{flex:none;color:rgb(var(--color-slate-400))}.mo-result-name,.mo-result-path code,.mo-result-message{display:block;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.mo-episode{flex:none;border-radius:5px;padding:2px 6px;background:#2563eb;color:#fff;font-size:11px;font-weight:500;line-height:1.5;font-variant-numeric:tabular-nums}.mo-result-meta{display:flex;flex-wrap:wrap;align-items:center;gap:6px;font-size:11px}.mo-chip{display:inline-flex;align-items:center;gap:4px;border-radius:5px;padding:2px 7px;line-height:1.5;font-weight:500}.mo-status-chip{font-size:12px;font-weight:600}.mo-success{color:#fff;background:#15803d}.mo-failure{color:#fff;background:#b91c1c}.mo-processing{color:#1d4ed8;background:#dbeafe}.mo-kind{color:#1d4ed8;background:#bfdbfe}.mo-format{color:#475569;background:#e2e8f0}.mo-result-path{display:grid;grid-template-columns:32px minmax(0,1fr);gap:8px;align-items:baseline;font-size:12px;line-height:1.7;color:rgb(var(--color-slate-500))}.mo-result-path code{color:rgb(var(--color-slate-600));font-size:11px}.mo-result-message{font-size:12px;line-height:1.7}.mo-result time{margin-top:auto;padding-top:2px;font-size:11px;color:rgb(var(--color-slate-500))}
.mo-work h3,.mo-title h3{font-size:16px;font-weight:600}.mo-title h3{min-width:0;overflow-wrap:anywhere}.mo-meta{margin-left:12px}.mo-preview{display:grid;gap:18px}.mo-work{padding-top:16px}.mo-work>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.mo-work+.mo-work{border-top:1px solid rgb(148 163 184/.18)}.mo-file{display:flex;align-items:center;gap:14px;padding:11px 0}.mo-file+.mo-file{border-top:1px solid rgb(148 163 184/.09)}.mo-file-path{flex:1;min-width:0;display:grid;gap:6px}.mo-file-path code{font-size:13px;overflow-wrap:anywhere;line-height:1.75;color:rgb(var(--color-slate-600))}.mo-file-path code.mo-target{color:#2563eb}.mo-file-path strong{font-size:13px;overflow-wrap:anywhere}.mo-file-path p{font-size:13px;line-height:1.6}.mo-file-controls{display:flex;align-items:center;gap:7px;flex-shrink:0}.mo-file-controls label{display:grid;gap:3px;font-size:12px;color:rgb(var(--color-slate-500))}.mo-file-controls input,.mo-search input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.23);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));font-size:13px;outline:none}.mo-file-controls input{width:72px;font-size:14px}.mo-file-controls input:focus,.mo-search input:focus{border-color:#3b82f6}.mo-status{width:56px;text-align:right;font-size:12px;color:rgb(var(--color-slate-500))}.mo-search{padding:10px 0}.mo-input-action{display:flex;gap:6px}.mo-input-action input{flex:1}.mo-candidates{display:grid;gap:4px;margin-top:8px;max-height:220px;overflow:auto}.mo-candidates button{display:flex;justify-content:space-between;gap:10px;text-align:left;padding:10px;border-radius:8px;background:rgb(148 163 184/.08);font-size:12px}.mo-candidates span{font-size:11px;color:rgb(var(--color-slate-500))}:global(.mo-preview-dialog.anime-floating.anime-floating-organize){height:fit-content;min-height:min(360px,calc(100dvh - 48px))}:global(.mo-preview-dialog .anime-floating-body){flex:0 1 auto}:global(.mo-preview-dialog .mo-muted),:global(.mo-preview-dialog .mo-button){font-size:13px}
:global(.dark .mo-result){background:rgb(var(--surface-card))}:global(.dark .mo-result:hover){background:rgb(var(--surface-hover))}:global(.dark .mo-result.is-selected){background:#1e3a5f;border-color:#3b82f6}:global(.dark .mo-success){color:#dcfce7;background:#166534}:global(.dark .mo-failure){color:#fee2e2;background:#991b1b}:global(.dark .mo-processing){color:#bfdbfe;background:#1e3a5f}:global(.dark .mo-kind){color:#bfdbfe;background:#1e40af}:global(.dark .mo-format){color:rgb(var(--content-secondary));background:rgb(var(--surface-elevated))}:global(.dark .mo-result-path code),:global(.dark .mo-cleanup),:global(.dark .mo-activity),:global(.dark .mo-file-path code),:global(.dark .mo-file-controls label){color:rgb(var(--content-secondary))}:global(.dark .mo-file-path code.mo-target){color:#93c5fd}:global(.dark .mo-bulk-bar){background:#1e3a5f;color:#bfdbfe}:global(.dark .mo-danger){background:#472327;color:#fecaca}:global(.dark .mo-search input),:global(.dark .mo-file-controls input){background:rgb(var(--surface-card));color:rgb(var(--content-primary));border-color:rgb(var(--line-default))}:global(.dark .mo-preview){color:rgb(var(--content-primary))}:global(.dark .mo-status),:global(.dark .mo-candidates span){color:rgb(var(--content-secondary))}
:global(.dark .mo-record-search input){color:rgb(var(--content-primary));background:rgb(148 163 184/.1)}
@media(max-width:639px){.mo-page{gap:16px}.mo-record-toolbar{gap:12px}.mo-status-filter{width:100%}.mo-record-actions{width:100%;justify-content:flex-end}.mo-record-search{width:100%;gap:5px}.mo-record-search>svg{display:none}.mo-record-search input{height:44px;font-size:16px}.mo-history-grid{gap:10px}.mo-result{padding:14px 12px}.mo-file-path{width:100%}.mo-file{align-items:flex-start;flex-direction:column;gap:8px}.mo-file-controls{align-self:flex-end}.mo-file-controls input{font-size:16px;min-height:44px;width:78px}.mo-candidates button{flex-direction:column;gap:4px}.mo-bulk-bar{position:sticky;top:8px;z-index:10;box-shadow:0 4px 14px rgb(15 23 42/.08)}}
@media(prefers-reduced-motion:reduce){.mo-result{transition:none}}
</style>
