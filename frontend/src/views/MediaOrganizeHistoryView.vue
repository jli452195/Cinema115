<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconSearch, IconX, IconRefresh, IconTrash, IconCheck, IconChevronRight, IconListDetails } from '@tabler/icons-vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import PaginationControl from '../components/PaginationControl.vue'
import { mediaOrganizeRequest, type MediaHistory, type MediaHistoryItem, type MediaHistoryStatus, type MediaTask, type MediaGroup, type MediaEntry } from '../lib/media-organize-api'

const props = defineProps<{ token: string }>()
const route = useRoute(), router = useRouter()
const filters: { value: MediaHistoryStatus; label: string }[] = [{ value: 'all', label: '全部' }, { value: 'running', label: '整理中' }, { value: 'done', label: '成功' }, { value: 'attention', label: '待处理' }]
const filter = computed<MediaHistoryStatus>(() => filters.some(item => item.value === route.query.status) ? route.query.status as MediaHistoryStatus : 'all')
const keyword = computed(() => typeof route.query.q === 'string' ? route.query.q.trim() : '')
const page = computed(() => typeof route.query.page === 'string' && /^\d+$/.test(route.query.page) && Number.isSafeInteger(Number(route.query.page)) ? Math.max(1, Number(route.query.page)) : 1)
const searchText = ref(keyword.value)
const history = ref<MediaHistory>({ items: [], page: 1, pages: 1, total: 0, counts: { all: 0, running: 0, done: 0, attention: 0 }, active: false, activity: [], cleanup: [] })
const loading = ref(true), refreshing = ref(false), busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref('')
const bulk = ref(false), selected = ref(new Set<string>())
const key = (item: MediaHistoryItem) => `${item.runId}:${item.fileId}`
const selectedItems = computed(() => history.value.items.filter(item => selected.value.has(key(item)) && item.canDelete))
const selectable = computed(() => history.value.items.filter(item => item.canDelete))
const allSelected = computed(() => selectable.value.length > 0 && selectedItems.value.length === selectable.value.length)
const deletion = ref<{ completed: true } | { items: MediaHistoryItem[] }>()
const deleteAttention = computed(() => deletion.value && 'items' in deletion.value && deletion.value.items.some(item => item.status !== 'done'))
const current = ref<MediaTask>(), taskSnapshot = ref(''), matchChanged = ref(false), leaveDialog = ref(false)
const primary = computed(() => current.value?.entries.find(entry => entry.file.id === current.value?.fileId))
const attachments = computed(() => current.value?.entries.filter(entry => entry.file.id !== current.value?.fileId) || [])
const editable = computed(() => primary.value?.status !== 'done')
const dirty = computed(() => !!current.value && editable.value && taskSignature(current.value) !== taskSnapshot.value)
const query = ref(''), searchGroup = ref(''), candidates = ref<{ id: number; kind: string; title: string; date: string }[]>([])
const running = (status: string) => ['queued', 'scanning', 'running'].includes(status)
const taskLabels: Record<string, string> = { done: '成功', partial: '部分完成', error: '失败', queued: '排队中', scanning: '识别中', running: '整理中', needs_match: '待匹配', invalid: '待修正', conflict: '冲突', interrupted: '已中断', failed: '失败', completed: '扫描完成' }
const labels: Record<string, string> = { ready: '可整理', needs_match: '待匹配', invalid: '待修正', conflict: '冲突', error: '失败', done: '已完成', unmatched: '未关联', retained: '已保留' }
const lifecycle = new AbortController()
let reader = new AbortController(), readVersion = 0, mutationVersion = 0, generation = 0, timer: number | undefined, canonicalPage: number | undefined
let normalizedPath: string | undefined
let leaveDecision: ((value: boolean) => void) | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
function statusClass(status: string) { return status === 'done' || status === 'completed' ? 'mo-success' : status === 'partial' ? 'mo-warning' : running(status) ? 'mo-processing' : 'mo-failure' }
function taskSignature(task: MediaTask) { const file = task.entries.find(entry => entry.file.id === task.fileId)?.file; return JSON.stringify([task.group.kind, task.group.tmdbId, file?.season, file?.episode]) }
async function request<T>(path: string, init: RequestInit = {}) { return mediaOrganizeRequest<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, ...(init.signal ? [init.signal] : [])]) }) }
function acceptTask(task: MediaTask) {
  const { rule, group, entries, ...item } = task
  history.value.items = history.value.items.map(row => key(row) === key(item) ? item : row)
  current.value = task; taskSnapshot.value = taskSignature(task)
}
async function refresh() {
  if (document.hidden || lifecycle.signal.aborted) return
  reader.abort(); reader = new AbortController()
  const signal = reader.signal, version = ++readVersion, mutation = mutationVersion
  refreshing.value = true
  try {
    const params = new URLSearchParams({ status: filter.value, page: String(page.value) })
    if (keyword.value) params.set('q', keyword.value)
    const value = await request<MediaHistory>(`/history?${params}`, { signal })
    if (signal.aborted || version !== readVersion || mutation !== mutationVersion) return
    history.value = value
    selected.value = new Set(value.items.filter(item => item.canDelete && selected.value.has(key(item))).map(key))
    if (value.page !== page.value) { canonicalPage = value.page; normalizePage() }
    const listed = value.items.find(item => current.value && key(item) === key(current.value))
    if (current.value && (running(current.value.status) || listed && (listed.status !== current.value.status || listed.at !== current.value.at || listed.canDelete !== current.value.canDelete))) {
      const id = current.value.runId, fileId = current.value.fileId, taskGeneration = generation
      const task = await request<MediaTask>(`/runs/${id}/task?fileId=${encodeURIComponent(fileId)}`, { signal })
      if (!signal.aborted && mutation === mutationVersion && current.value?.runId === id && current.value.fileId === fileId && taskGeneration === generation) {
        const draft = dirty.value ? { group: current.value.group, season: primary.value?.file.season, episode: primary.value?.file.episode } : undefined
        acceptTask(task)
        if (draft && primary.value?.status !== 'done') { current.value.group = draft.group; if (primary.value) { primary.value.file.season = draft.season!; primary.value.file.episode = draft.episode! } } else matchChanged.value = false
      }
    }
    error.value = ''
  } catch (reason) { if (!signal.aborted && !lifecycle.signal.aborted && version === readVersion && mutation === mutationVersion) error.value = message(reason) }
  finally { if (version === readVersion) { loading.value = false; refreshing.value = false; poll() } }
}
async function act(name: string, fn: () => Promise<void>, dialog = false) {
  if (busy.value) return
  busy.value = name; (dialog ? dialogError : error).value = ''; notice.value = ''; window.clearTimeout(timer)
  try { await fn() } catch (reason) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = message(reason) }
  finally { busy.value = ''; normalizePage(); poll() }
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
  if (bulk.value) { if (item.canDelete) { const next = new Set(selected.value); next.has(key(item)) ? next.delete(key(item)) : next.add(key(item)); selected.value = next }; return }
  await act(item.fileId, async () => { acceptTask(await request<MediaTask>(`/runs/${item.runId}/task?fileId=${encodeURIComponent(item.fileId)}`)); generation++; matchChanged.value = false; dialogError.value = ''; searchGroup.value = ''; candidates.value = [] })
}
function closeRun() { current.value = undefined; generation++; searchGroup.value = ''; candidates.value = []; normalizePage() }
async function requestCloseRun() { if (await guard()) closeRun() }
function askDelete(items: MediaHistoryItem[]) { if (!items.length || items.some(item => !item.canDelete)) return; dialogError.value = ''; deletion.value = { items: [...items] } }
function askClearCompleted() { dialogError.value = ''; deletion.value = { completed: true } }
async function removeRecords() {
  const intent = deletion.value
  if (!intent) return
  await act('delete', async () => {
    const body = 'completed' in intent ? intent : { items: intent.items.map(({ runId, fileId }) => ({ runId, fileId })) }
    // A failed receipt is not retried automatically: a later clear could include new results.
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
    if (!task) return
    if (dirty.value && editable.value && task.group.kind === 'tv') {
      const file = primary.value?.file
      if (!file || !Number.isInteger(file.season) || file.season < 0 || file.season > 1000 || !Number.isInteger(file.episode) || file.episode < 1 || file.episode > 10000) throw new Error('请填写有效的目标季集')
    }
    const correction = dirty.value && editable.value ? { ...(task.group.kind === 'tv' ? { season: primary.value?.file.season, episode: primary.value?.file.episode } : {}), ...(matchChanged.value ? { kind: task.group.kind, tmdbId: task.group.tmdbId } : {}) } : {}
    const updated = await request<MediaTask>(`/runs/${task.runId}/retry`, { method: 'POST', body: JSON.stringify({ fileId: task.fileId, ...correction }) })
    mutationVersion++; acceptTask(updated); matchChanged.value = false; await refresh()
  }, true)
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
function pick(candidate: { id: number; kind: string; title: string }) { if (current.value) { Object.assign(current.value.group, { tmdbId: candidate.id, kind: candidate.kind, title: candidate.title }); matchChanged.value = true }; searchGroup.value = ''; candidates.value = [] }
function episodeLabel(item: MediaHistoryItem) { return item.kind === 'tv' && item.episode > 0 ? `S${String(item.season).padStart(2, '0')}E${String(item.episode).padStart(2, '0')}` : '' }
function sourcePath(entry: MediaEntry) { return entry.failure?.state === 'stored' ? `${current.value?.rule.failure}/${entry.file.sourcePath}` : `${current.value?.rule.source}/${entry.file.sourcePath}` }
function failureNotice(entry: MediaEntry) { if (entry.failure?.state === 'stored') return '已移入失败目录；可在此修正匹配并重试当前任务。'; if (!entry.failure?.message) return ''; return entry.failure.state === 'moving' ? `移入结果尚未确认，继续整理将核对文件位置：${entry.failure.message}` : `未能移入失败目录，文件保留原处：${entry.failure.message}` }
function poll() {
  window.clearTimeout(timer)
  if (lifecycle.signal.aborted || document.hidden || busy.value) return
  // Only the live first page updates automatically; history browsing stays in place.
  const liveList = history.value.active && page.value === 1 && ['all', 'running'].includes(filter.value) && !bulk.value
  if (!liveList && !(current.value && running(current.value.status))) return
  timer = window.setTimeout(() => { if (!bulk.value && !deletion.value) void refresh(); else poll() }, 5000)
}
async function visible() { if (!document.hidden) await refresh(); else window.clearTimeout(timer) }
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
  if (value === normalizedPath) { normalizedPath = undefined; poll(); return }
  canonicalPage = undefined; searchText.value = keyword.value; closeRun(); clearSelection(); notice.value = ''; void refresh()
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); document.addEventListener('visibilitychange', visible); void refresh() })
onBeforeUnmount(() => { lifecycle.abort(); reader.abort(); window.clearTimeout(timer); window.removeEventListener('beforeunload', beforeUnload); document.removeEventListener('visibilitychange', visible); decideLeave(false) })
</script>

<template>
  <section class="mo-page mo-results" aria-label="整理记录">
    <div class="mo-record-toolbar">
      <div class="mo-status-filter" role="group" aria-label="记录状态"><button v-for="item in filters" :key="item.value" type="button" :aria-label="item.label" :aria-pressed="filter === item.value" :disabled="!!busy" @click="navigate(item.value)">{{ item.label }}<span>{{ history.counts[item.value] }}</span></button></div>
      <div class="mo-record-actions"><button type="button" class="mo-button" :disabled="loading || !!busy" @click="bulk ? clearSelection() : bulk = true">{{ bulk ? '退出管理' : '批量管理' }}</button><button type="button" class="mo-button" :disabled="loading || !!busy" @click="askClearCompleted"><IconTrash :size="16" aria-hidden="true" />清理全部成功</button><button type="button" class="mo-icon" aria-label="刷新记录" :disabled="refreshing || !!busy" @click="refresh"><IconRefresh :size="18" aria-hidden="true" /></button></div>
    </div>
    <form class="mo-record-search" @submit.prevent="submitSearch"><IconSearch :size="18" aria-hidden="true" /><input v-model="searchText" type="search" aria-label="搜索整理记录" placeholder="搜索片名、文件名或路径" :disabled="!!busy" /><button v-if="searchText" type="button" class="mo-icon" aria-label="清空搜索" :disabled="!!busy" @click="clearSearch"><IconX :size="16" aria-hidden="true" /></button><button type="submit" class="mo-button mo-primary" :disabled="!!busy">搜索记录</button></form>
    <p v-if="error" role="alert" class="mo-error">{{ error }}</p>
    <p v-if="notice" role="status" class="mo-muted">{{ notice }}</p>
    <div v-if="history.activity.length" class="mo-activity" aria-label="扫描状态"><p v-for="item in history.activity" :key="item.runId"><span class="mo-chip" :class="statusClass(item.status)">{{ taskLabels[item.status] || item.status }}</span><strong>{{ item.ruleName }}</strong><span>{{ item.message || (running(item.status) ? '正在扫描来源并识别文件，完成后显示整理结果' : '本次扫描未生成媒体任务') }}</span></p></div>
    <div v-if="bulk" class="mo-bulk-bar"><label><input type="checkbox" :checked="allSelected" :disabled="!!busy || !selectable.length" @change="selectAll" />全选当前页</label><span>已选 {{ selectedItems.length }} 条</span><button type="button" class="mo-button mo-danger" :disabled="!!busy || !selectedItems.length" @click="askDelete(selectedItems)">删除选中 {{ selectedItems.length }} 条</button></div>
    <p v-if="loading" class="mo-empty" role="status">正在读取整理记录…</p>
    <div v-else-if="!history.items.length" class="mo-empty"><IconListDetails :size="30" aria-hidden="true" /><p>{{ keyword || filter !== 'all' ? '未找到符合条件的整理记录' : history.active ? '正在扫描来源目录，媒体任务将在识别后显示' : '暂无整理记录，可在整理规则中立即运行或启用监控' }}</p><button v-if="keyword || filter !== 'all'" type="button" class="mo-button" @click="navigate('all', '')">清空筛选</button></div>
    <div v-else class="mo-history-grid" :aria-busy="refreshing">
      <button v-for="item in history.items" :key="key(item)" type="button" class="mo-result" :class="{ 'is-selected': bulk && selected.has(key(item)) }" :disabled="!!busy || bulk && !item.canDelete" :aria-label="`${bulk ? '选择' : '查看'}${item.name}的整理记录`" :aria-pressed="bulk ? selected.has(key(item)) : undefined" :title="bulk && !item.canDelete ? '所属批次正在处理，暂不能删除' : undefined" @click="cardClick(item)">
        <span class="mo-result-heading"><strong><span class="mo-result-name" :title="item.title || item.name">{{ item.title || item.name }}</span><span v-if="episodeLabel(item)" class="mo-episode">{{ episodeLabel(item) }}</span></strong><span v-if="bulk" class="mo-selection" aria-hidden="true"><IconCheck v-if="selected.has(key(item))" :size="14" /></span><IconChevronRight v-else :size="16" aria-hidden="true" /></span>
        <span class="mo-result-meta"><span class="mo-chip mo-status-chip" :class="statusClass(item.status)">{{ taskLabels[item.status] || '待处理' }}</span><span class="mo-chip mo-kind">{{ item.kind === 'movie' ? '电影' : item.kind === 'tv' ? '剧集' : '待识别' }}</span><span class="mo-chip mo-format">{{ item.name.split('.').pop()?.toUpperCase() }}</span><span v-if="item.attachmentCount" class="mo-chip mo-format">附件 {{ item.attachmentCount }}</span></span>
        <span class="mo-result-path"><span>原名</span><code :title="item.source">{{ item.name }}</code></span><span v-if="item.target" class="mo-result-path"><span>目标</span><code :title="item.target">{{ item.target.split('/').pop() }}</code></span>
        <span v-if="item.message" class="mo-result-message" :class="item.status === 'done' ? 'mo-muted' : 'mo-red'" :title="item.message">{{ item.message }}</span><time :datetime="item.at">{{ new Date(item.at).toLocaleString() }}</time>
      </button>
    </div>
    <PaginationControl v-if="history.pages > 1" :page="history.page" :pages="history.pages" :disabled="refreshing || !!busy" label="整理记录分页" @change="value => navigate(filter, keyword, value)" />
    <details v-if="history.cleanup.length" class="mo-cleanup"><summary>来源清理记录 <span>{{ history.cleanup.length }} 次运行</span></summary><p v-for="item in history.cleanup" :key="item.runId"><strong>{{ item.ruleName }}</strong><span>文件 {{ item.files }} · 空目录 {{ item.directories }}<span v-if="item.failed" class="mo-red"> · 失败 {{ item.failed }}</span></span><time>{{ new Date(item.at).toLocaleString() }}</time></p></details>
  </section>
    <FloatingDialog v-if="current" class="mo-preview-dialog" title="整理详情" close-label="关闭整理详情" size="organize" :close-disabled="!!busy" @close="requestCloseRun">
      <template #header><div class="mo-title"><h3>{{ current.group.title || current.name }}</h3><span class="mo-chip" :class="statusClass(current.status)">{{ taskLabels[current.status] }}</span></div><p class="mo-muted">{{ current.rule.name }} · {{ new Date(current.at).toLocaleString() }}</p><p v-if="current.message && !running(current.status)" class="mo-muted">{{ current.message }}</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p></template>
      <div class="mo-preview">
        <p v-if="running(current.status)" class="mo-muted" role="status">正在处理当前任务，关闭弹窗后继续。</p>
        <section class="mo-work">
          <header><div><h3>主文件</h3><p class="mo-muted">{{ current.group.tmdbId ? `${current.group.kind === 'movie' ? '电影' : '剧集'} · TMDB ${current.group.tmdbId}` : current.group.message }}<span v-if="current.group.category" class="mo-meta">{{ current.group.category }}</span></p></div><button v-if="editable && !running(current.status)" class="mo-button" :disabled="!!busy" @click="match(current.group)">{{ current.group.tmdbId ? '更换作品' : '匹配作品' }}</button></header>
          <form v-if="searchGroup" class="mo-search" @submit.prevent="search"><div class="mo-input-action"><input v-model="query" aria-label="搜索 TMDB 作品" required placeholder="片名" /><button class="mo-icon" type="submit" :disabled="!!busy" aria-label="搜索作品"><IconSearch :size="18" /></button></div><div class="mo-candidates"><button v-for="candidate in candidates" :key="`${candidate.kind}:${candidate.id}`" type="button"  :disabled="!!busy" @click="pick(candidate)"><strong>{{ candidate.title }}</strong><span>{{ candidate.kind === 'movie' ? '电影' : '剧集' }} · {{ candidate.date?.slice(0, 4) }} · {{ candidate.id }}</span></button><p v-if="busy === 'search'" class="mo-muted" role="status">正在搜索 TMDB 作品…</p><p v-if="!busy && candidates.length === 0" class="mo-muted">输入片名并搜索，选择正确作品。</p></div></form>
          <article v-if="primary" class="mo-file"><div class="mo-file-path"><strong>{{ primary.file.name }}</strong><span class="mo-muted">原路径</span><code>{{ sourcePath(primary) }}</code><template v-if="primary.target"><span class="mo-muted">{{ dirty ? '原计划目标（保存后重新计算）' : '目标路径' }}</span><code class="mo-target">{{ primary.target }}</code></template><p v-if="primary.message" class="mo-red">{{ primary.message }}</p><p v-if="failureNotice(primary)" class="mo-muted">{{ failureNotice(primary) }}</p></div><div class="mo-file-controls"><template v-if="editable && current.group.kind === 'tv'"><label>季<input v-model.number="primary.file.season" type="number" min="0" max="1000" :disabled="!!busy || running(current.status)" /></label><label>集<input v-model.number="primary.file.episode" type="number" min="1" max="10000" :disabled="!!busy || running(current.status)" /></label></template><span class="mo-status">{{ labels[primary.status] || primary.status }}</span></div></article>
        </section>
        <section v-if="attachments.length" class="mo-work"><header><h3>关联文件 <span class="mo-muted">{{ attachments.length }} 项</span></h3></header><article v-for="entry in attachments" :key="entry.file.id" class="mo-file"><div class="mo-file-path"><strong>{{ entry.file.name }}</strong><code>{{ sourcePath(entry) }}</code><code v-if="entry.target" class="mo-target">{{ entry.target }}</code><p v-if="entry.message" class="mo-red">{{ entry.message }}</p><p v-if="failureNotice(entry)" class="mo-muted">{{ failureNotice(entry) }}</p></div><span class="mo-status" :class="{ 'mo-red': entry.status !== 'done' }">{{ labels[entry.status] || entry.status }}</span></article></section>
      </div>
      <template #footer><div class="mo-footer"><span v-if="dirty" class="mo-muted">仅更新当前任务，保存后在后台重新整理。</span><button class="mo-button mo-danger" :disabled="!!busy || !current.canDelete" @click="askDelete([current])"><IconTrash :size="16" aria-hidden="true" />删除此记录</button><button class="mo-button" :disabled="!!busy" @click="requestCloseRun">关闭</button><button v-if="!running(current.status) && current.status !== 'done'" class="mo-button mo-primary" :disabled="!!busy || !!searchGroup" @click="retry">{{ dirty ? '保存并重试此任务' : '重试此任务' }}</button></div></template>
    </FloatingDialog>

    <P115ActionDialog v-if="deletion" :title="'completed' in deletion ? '清理全部成功记录？' : '删除整理记录？'" :busy="busy === 'delete'" @close="deletion = undefined">
      <p>{{ 'completed' in deletion ? '将删除所有已成功且所属批次已结束的记录，包括当前搜索条件之外的记录。' : `将删除选中的 ${deletion.items.length} 条整理记录。` }}实际文件保持不变。</p>
      <p v-if="deleteAttention" class="mo-red">包含待处理记录，删除后失去原来的修正和重试入口。需要重新整理时，可将文件放回来源目录。</p>
      <p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p>
      <template #footer><button type="button" class="mo-button" :disabled="!!busy" @click="deletion = undefined">取消</button><button type="button" class="mo-button mo-danger" :disabled="!!busy" @click="removeRecords">确认删除</button></template>
    </P115ActionDialog>
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的整理修改？" @close="decideLeave(false)"><p>任务修正尚未保存，离开后将丢弃这些修改。后台整理任务会继续运行。</p><template #footer><button type="button" class="mo-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="mo-button mo-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
</template>

<style scoped src="../styles/media-organize.css"></style>
<style scoped>
.mo-record-toolbar,.mo-record-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.mo-record-toolbar{justify-content:space-between}.mo-status-filter{display:flex;gap:5px;padding:3px;border-radius:11px;background:rgb(148 163 184/.1);overflow-x:auto;max-width:100%;scrollbar-width:none}.mo-status-filter button{display:inline-flex;align-items:center;justify-content:center;gap:7px;white-space:nowrap;min-height:36px;padding:7px 12px;border-radius:8px;font-size:13px;color:rgb(var(--color-slate-600))}.mo-status-filter button span{font-size:11px;font-variant-numeric:tabular-nums;opacity:.8}.mo-status-filter button[aria-pressed=true]{color:#1d4ed8;background:#fff;box-shadow:0 1px 4px rgb(15 23 42/.07)}.mo-record-search{display:flex;gap:8px;align-items:center;max-width:560px;min-width:0}.mo-record-search>svg{color:#64748b;flex:none}.mo-record-search input{flex:1;min-width:0;width:100%;height:38px;background:rgb(148 163 184/.08);border-radius:9px;padding:8px 11px;font-size:13px;outline:none}.mo-record-search input:focus{box-shadow:0 0 0 2px rgb(59 130 246/.3)}.mo-record-search input::-webkit-search-cancel-button{display:none}.mo-bulk-bar{display:flex;align-items:center;gap:16px;flex-wrap:wrap;border-radius:11px;background:#dbeafe;padding:10px 13px;font-size:12px;color:#1e40af}.mo-bulk-bar label{display:inline-flex;align-items:center;gap:8px;min-height:30px}.mo-bulk-bar input{width:17px;height:17px;accent-color:#2563eb}.mo-bulk-bar .mo-button{margin-left:auto}.mo-danger{color:#b91c1c;background:#fee2e2}.mo-danger:hover{background:#fecaca}.mo-activity{display:grid;gap:7px;color:rgb(var(--color-slate-600));font-size:12px}.mo-activity p{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}.mo-activity strong{font-weight:500}.mo-cleanup{min-width:0;padding:12px 14px;border-radius:12px;background:rgb(148 163 184/.08);font-size:12px;color:rgb(var(--color-slate-600))}.mo-cleanup summary{cursor:pointer}.mo-cleanup summary span{margin-left:8px;color:rgb(var(--color-slate-500))}.mo-cleanup p{display:flex;flex-wrap:wrap;gap:8px 16px;margin-top:12px}.mo-cleanup time{margin-left:auto}.mo-results{min-width:0}.mo-history-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr));gap:12px}.mo-result{display:flex;flex-direction:column;min-width:0;gap:7px;padding:14px;text-align:left;border-radius:14px;border:1px solid transparent;background:#fff;box-shadow:0 2px 8px rgb(15 23 42/.035);transition:background-color .16s,box-shadow .16s}.mo-result:hover{background:#eff6ff;box-shadow:0 4px 16px rgb(37 99 235/.1)}.mo-result.is-selected{background:#dbeafe;border-color:#60a5fa;box-shadow:0 3px 12px rgb(37 99 235/.15)}.mo-selection{width:19px;height:19px;border:1.5px solid #94a3b8;border-radius:5px;display:inline-flex;align-items:center;justify-content:center;flex:none}.is-selected .mo-selection{background:#2563eb;border-color:#2563eb;color:white}.mo-result-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;font-size:14px;line-height:1.6}.mo-result-heading strong{display:flex;align-items:center;flex:1;min-width:0;gap:8px;font-weight:600}.mo-result-heading>svg{flex:none;color:rgb(var(--color-slate-400))}.mo-result-name,.mo-result-path code,.mo-result-message{display:block;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.mo-episode{flex:none;border-radius:5px;padding:2px 6px;background:#2563eb;color:#fff;font-size:11px;font-weight:500;line-height:1.5;font-variant-numeric:tabular-nums}.mo-result-meta{display:flex;flex-wrap:wrap;align-items:center;gap:6px;font-size:11px}.mo-chip{display:inline-flex;align-items:center;gap:4px;border-radius:5px;padding:2px 7px;line-height:1.5;font-weight:500}.mo-status-chip{font-size:12px;font-weight:600}.mo-success{color:#fff;background:#15803d}.mo-failure{color:#fff;background:#b91c1c}.mo-warning{color:#92400e;background:#fef3c7}.mo-processing{color:#1d4ed8;background:#dbeafe}.mo-kind{color:#1d4ed8;background:#bfdbfe}.mo-format{color:#475569;background:#e2e8f0}.mo-result-path{display:grid;grid-template-columns:32px minmax(0,1fr);gap:8px;align-items:baseline;font-size:12px;line-height:1.7;color:rgb(var(--color-slate-500))}.mo-result-path code{color:rgb(var(--color-slate-600));font-size:11px}.mo-result-message{font-size:12px;line-height:1.7}.mo-result time{margin-top:auto;padding-top:2px;font-size:11px;color:rgb(var(--color-slate-500))}
.mo-work h3,.mo-title h3{font-size:16px;font-weight:600}.mo-title h3{min-width:0;overflow-wrap:anywhere}.mo-meta{margin-left:12px}.mo-preview{display:grid;gap:18px}.mo-work{padding-top:16px}.mo-work>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.mo-work+.mo-work{border-top:1px solid rgb(148 163 184/.18)}.mo-file{display:flex;align-items:center;gap:14px;padding:11px 0}.mo-file+.mo-file{border-top:1px solid rgb(148 163 184/.09)}.mo-file-path{flex:1;min-width:0;display:grid;gap:6px}.mo-file-path code{font-size:13px;overflow-wrap:anywhere;line-height:1.75;color:rgb(var(--color-slate-600))}.mo-file-path code.mo-target{color:#2563eb}.mo-file-path strong{font-size:13px;overflow-wrap:anywhere}.mo-file-path p{font-size:13px;line-height:1.6}.mo-file-controls{display:flex;align-items:center;gap:7px;flex-shrink:0}.mo-file-controls label{display:grid;gap:3px;font-size:12px;color:rgb(var(--color-slate-500))}.mo-file-controls input,.mo-search input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.23);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));font-size:13px;outline:none}.mo-file-controls input{width:72px;font-size:14px}.mo-file-controls input:focus,.mo-search input:focus{border-color:#3b82f6}.mo-status{width:56px;text-align:right;font-size:12px;color:rgb(var(--color-slate-500))}.mo-search{padding:10px 0}.mo-input-action{display:flex;gap:6px}.mo-input-action input{flex:1}.mo-candidates{display:grid;gap:4px;margin-top:8px;max-height:220px;overflow:auto}.mo-candidates button{display:flex;justify-content:space-between;gap:10px;text-align:left;padding:10px;border-radius:8px;background:rgb(148 163 184/.08);font-size:12px}.mo-candidates span{font-size:11px;color:rgb(var(--color-slate-500))}:global(.mo-preview-dialog.anime-floating.anime-floating-organize){height:fit-content;min-height:min(360px,calc(100dvh - 48px))}:global(.mo-preview-dialog .anime-floating-body){flex:0 1 auto}:global(.mo-preview-dialog .mo-muted),:global(.mo-preview-dialog .mo-button){font-size:13px}
:global(.dark .mo-status-filter button){color:#cbd5e1}:global(.dark .mo-status-filter button[aria-pressed=true]){background:#334155;color:#bfdbfe}:global(.dark .mo-result){background:#1e293b}:global(.dark .mo-result:hover){background:#24334d}:global(.dark .mo-result.is-selected){background:#1e3a5f;border-color:#3b82f6}:global(.dark .mo-success){color:#dcfce7;background:#166534}:global(.dark .mo-failure){color:#fee2e2;background:#991b1b}:global(.dark .mo-warning){color:#fde68a;background:#78350f}:global(.dark .mo-processing){color:#bfdbfe;background:#1e3a5f}:global(.dark .mo-kind){color:#bfdbfe;background:#1e40af}:global(.dark .mo-format){color:#cbd5e1;background:rgb(100 116 139/.25)}:global(.dark .mo-result-path code),:global(.dark .mo-cleanup),:global(.dark .mo-activity),:global(.dark .mo-file-path code),:global(.dark .mo-file-controls label){color:#cbd5e1}:global(.dark .mo-file-path code.mo-target){color:#93c5fd}:global(.dark .mo-bulk-bar){background:#1e3a5f;color:#bfdbfe}:global(.dark .mo-danger){background:#472327;color:#fecaca}:global(.dark .mo-search input),:global(.dark .mo-file-controls input){background:#1e293b;color:#e2e8f0;border-color:rgb(148 163 184/.3)}:global(.dark .mo-preview){color:#e2e8f0}:global(.dark .mo-status),:global(.dark .mo-candidates span){color:#94a3b8}
:global(.dark .mo-record-search input){color:#e2e8f0;background:rgb(148 163 184/.1)}
@media(max-width:639px){.mo-page{gap:16px}.mo-record-toolbar{gap:12px}.mo-status-filter{width:100%}.mo-status-filter button{flex:1;min-height:40px;padding:7px 10px}.mo-record-actions{width:100%;justify-content:flex-end}.mo-record-search{width:100%;gap:5px}.mo-record-search>svg{display:none}.mo-record-search input{height:44px;font-size:16px}.mo-history-grid{gap:10px}.mo-result{padding:14px 12px}.mo-file-path{width:100%}.mo-file{align-items:flex-start;flex-direction:column;gap:8px}.mo-file-controls{align-self:flex-end}.mo-file-controls input{font-size:16px;min-height:44px;width:78px}.mo-candidates button{flex-direction:column;gap:4px}.mo-bulk-bar{position:sticky;top:8px;z-index:10;box-shadow:0 4px 14px rgb(15 23 42/.08)}}
@media(prefers-reduced-motion:reduce){.mo-result{transition:none}}
</style>
