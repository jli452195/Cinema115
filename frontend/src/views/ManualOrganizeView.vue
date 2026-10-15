<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import DirectoryShareButton from '../components/DirectoryShareButton.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import OrganizeCleanupDialog from '../components/OrganizeCleanupDialog.vue'
import SubtitleImportDialog from '../components/SubtitleImportDialog.vue'
import OrganizeFileSelection from '../components/OrganizeFileSelection.vue'
import OrganizeTaskHistory from '../components/OrganizeTaskHistory.vue'
import type { HistoryKind } from '../utils/organizeHistory'
import SelectMenu from '../components/SelectMenu.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import TMDBEpisodeGroupDialog from '../components/TMDBEpisodeGroupDialog.vue'
import { numberingLabel, type NumberingInfo } from '../types/tvNumbering'
import { directoryGroups } from '../utils/organizeSelection'
import type { SubtitleImport } from '../types/subtitleImport'
import { p115Request } from '../lib/p115-api'
import { tmdbImageURL } from '../utils/tmdbImage'
import type { P115Account } from '../types/p115Accounts'
import type { MediaCardItem } from '../types/media'
import type { DriveFile, OrganizeCleanup, OrganizeConfig, OrganizeFile, OrganizeJob, OrganizeRow } from '../types/manualOrganize'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const accounts = ref<P115Account[]>([])
const accountsLoaded = ref(false)
const config = ref<OrganizeConfig>({ accountId: '', sourceCID: '', sourcePath: '', targetCID: '', targetPath: '' })
const busy = ref('')
const error = ref('')
const notice = ref('')
const directoryDialog = ref<'source' | 'target' | ''>('')
const files = ref<DriveFile[]>([])
const offset = ref(0)
const count = ref(0)
const breadcrumbs = ref<{ id: string; path: string }[]>([])
const scanId = ref('')
const selectedName = ref('')
const selectionRoot = ref('')
const rows = ref<OrganizeRow[]>([])
const query = ref('')
const kind = ref('multi')
const kindOptions = [{ value: 'multi', label: '全部' }, { value: 'movie', label: '电影' }, { value: 'tv', label: '剧集' }]
let scannedEntryKey = ''
const results = ref<MediaCardItem[]>([])
const searchPage = ref(1)
const searchTotalPages = ref(0)
const media = ref<(MediaCardItem & { numbering?: NumberingInfo; seasons?: { number: number; name: string; episodeCount: number }[] })>()
const numberingOpen = ref(false)
const subtitles = ref(true)
const batchSeason = ref(1)
const seasonValue = computed({ get: () => String(batchSeason.value), set: (value: string) => { batchSeason.value = Number(value) } })
const seasonOptions = computed(() => media.value?.kind === 'tv' ? (media.value.seasons ?? []).map(season => ({ value: String(season.number), label: `${season.name}（${season.episodeCount} 集）` })) : [{ value: '1', label: media.value ? '电影无需设置季集' : '请先选择剧集作品' }])
const startEpisode = ref(1)
const preview = ref<OrganizeJob>()
const jobs = ref<OrganizeJob[]>([])
const cleanupJobs = ref<OrganizeCleanup[]>([])
const subtitleJobs = ref<SubtitleImport[]>([])
const subtitleJob = ref<OrganizeJob>()
const cleanupPlan = ref<OrganizeCleanup>()
const deleteIDs = ref<string[]>([])
const cleanupOpen = ref(false)
const shareOpen = ref(false)
const activeStep = ref(1)
const historyTab = ref<HistoryKind>('organize')
const onlyConflicts = ref(false)
const savedConfig = ref('')
const workflowPanel = ref<HTMLElement>()
const sourcePanel = ref<HTMLElement>()
const matchPanel = ref<HTMLElement>()
const editPanel = ref<HTMLElement>()
const previewPanel = ref<HTMLElement>()
const historyError = ref('')
const historyReady = ref(false)
const historyRefreshing = ref(false)
const historyRefreshedAt = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let applying = false
let historyRevision = 0

const accountReady = computed(() => accounts.value.some(a => a.primary && a.id === config.value.accountId && a.configured))
const configured = computed(() => accountReady.value && !!config.value.sourceCID && !!config.value.targetCID)
const active = computed(() => jobs.value.some(j => j.status === 'queued' || j.status === 'running') || cleanupJobs.value.some(j => j.status === 'running') || subtitleJobs.value.some(j => j.status === 'running'))
const cleanupBlocked = computed(() => !historyReady.value || !!historyError.value || active.value || !accountReady.value || !config.value.sourceCID)
const allPageSelected = computed(() => files.value.length > 0 && files.value.every(file => deleteIDs.value.includes(file.id)))
const selectedRows = computed(() => rows.value.filter(row => row.selected))
const conflicts = computed(() => preview.value?.items.filter(item => item.conflict).length ?? 0)
const currentStep = computed(() => preview.value ? 4 : scanId.value && media.value ? 3 : scanId.value ? 2 : 1)
const configDirty = computed(() => savedConfig.value !== JSON.stringify(config.value))
const previewItems = computed(() => preview.value?.items.filter(item => !onlyConflicts.value || item.conflict) ?? [])
const previewVideos = computed(() => preview.value?.items.filter(item => item.file.mediaType === 'video').length ?? 0)
const previewSubtitles = computed(() => preview.value?.items.filter(item => item.file.mediaType === 'subtitle').length ?? 0)
const stepItems = [
  { number: 1, label: '选择内容' },
  { number: 2, label: '匹配作品' },
  { number: 3, label: '校正命名' },
  { number: 4, label: '确认执行' },
]

function request<T>(path: string, init: RequestInit = {}, timeout = 45000) {
  return p115Request<T>(props.token, `manual-organize/${path}`, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(timeout)]) })
}
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，请重试' }
function accountLabel(id: string) {
  const account = accounts.value.find(value => value.id === id)
  return account?.primary ? '主账号' : account?.name || id
}
async function work(label: string, action: () => Promise<void>) {
  if (busy.value) return
  busy.value = label; error.value = ''; notice.value = ''
  try { await action() } catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { busy.value = '' }
}
function clearSelection(resetMedia = true) {
  activeStep.value = 1
  scanId.value = ''; rows.value = []; preview.value = undefined; selectedName.value = ''; selectionRoot.value = ''
  if (resetMedia) { media.value = undefined; results.value = []; searchTotalPages.value = 0; scannedEntryKey = '' }
}
async function scrollToStep(number: number) {
  if (number > currentStep.value) return
  notice.value = ''
  activeStep.value = number
  await nextTick()
  workflowPanel.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  const panel = number === 1 ? sourcePanel.value : number === 2 ? matchPanel.value : number === 3 ? editPanel.value : previewPanel.value
  panel?.focus({ preventScroll: true })
}
async function save() {
  if (!configDirty.value) return
  await request('config', { method: 'PUT', body: JSON.stringify(config.value) })
  savedConfig.value = JSON.stringify(config.value)
  notice.value = '来源目录和目标目录已保存。'
}
async function browse(nextOffset = 0) {
  deleteIDs.value = []; cleanupPlan.value = undefined
  const data = await request<{ files: DriveFile[]; count: number }>(`files?accountId=${encodeURIComponent(config.value.accountId)}&cid=${config.value.sourceCID}&offset=${nextOffset}`)
  files.value = data.files; offset.value = nextOffset; count.value = data.count
}
function togglePageSelection() { deleteIDs.value = allPageSelected.value ? [] : files.value.map(file => file.id) }
async function previewCleanup(mode: 'selected' | 'empty', ids = deleteIDs.value) {
  cleanupPlan.value = undefined
  const plan = await request<OrganizeCleanup>('cleanup/preview', { method: 'POST', body: JSON.stringify({ accountId: config.value.accountId, parentId: config.value.sourceCID, parentPath: config.value.sourcePath, mode, ids: mode === 'empty' ? [] : ids }) }, 190000)
  if (!plan.items.length) { notice.value = '当前目录下没有可清理的空文件夹。'; return }
  cleanupPlan.value = plan
}
async function executeCleanup() {
  const plan = cleanupPlan.value
  if (!plan) return
  clearSelection()
  try {
    const result = await request<OrganizeCleanup>('cleanup', { method: 'POST', body: JSON.stringify({ accountId: plan.accountId, previewId: plan.id }) }, 190000)
    cleanupJobs.value = [result, ...cleanupJobs.value.filter(job => job.id !== result.id)]
    const done = result.items.filter(item => item.status === 'done').length
    notice.value = `已移入回收站 ${done} / ${result.items.length} 项。${result.message || ''}`
  } catch (reason) {
    throw new Error(`${message(reason)}；请查看清理记录并刷新网盘核实结果。`)
  } finally {
    cleanupPlan.value = undefined
    deleteIDs.value = []
    await refreshHistory()
    try { await browse() } catch (reason) { error.value = `文件列表刷新失败：${message(reason)}` }
  }
}
function historyActionAllowed(job: OrganizeJob) {
  return !busy.value && !active.value && historyReady.value && !historyError.value && job.status !== 'running' && job.status !== 'queued' && accounts.value.some(account => account.id === job.config.accountId && account.configured)
}
function viewHistorySource(job: OrganizeJob) {
  if (historyActionAllowed(job)) void work('查看源目录剩余内容', () => showSource(job))
}
function supplementSubtitles(job: OrganizeJob) {
  if (historyActionAllowed(job) && job.items.some(item => item.status === 'done' && item.file.mediaType === 'video')) subtitleJob.value = job
}
async function showSource(job: OrganizeJob) {
  if (!accounts.value.some(account => account.id === job.config.accountId && account.configured)) throw new Error('此任务的账号已删除或未登录，请先配置凭据。')
  applying = true
  config.value = { ...job.config }
  applying = false
  breadcrumbs.value = []; files.value = []; count.value = 0
  clearSelection()
  await browse()
  sourcePanel.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
function selectDirectory(value: { id: string; path: string }) {
  if (directoryDialog.value === 'source') {
    config.value.sourceCID = value.id; config.value.sourcePath = value.path
    breadcrumbs.value = []; files.value = []; count.value = 0
    void work('读取文件', () => browse())
  } else { config.value.targetCID = value.id; config.value.targetPath = value.path }
  directoryDialog.value = ''
}
async function enter(file: DriveFile) {
  const previous = { id: config.value.sourceCID, path: config.value.sourcePath }
  config.value.sourceCID = file.id
  config.value.sourcePath = `${previous.path.replace(/\/$/, '')}/${file.name}`
  breadcrumbs.value.push(previous)
  files.value = []; count.value = 0
  await browse()
}
async function goBack() {
  const previous = breadcrumbs.value.pop(); if (!previous) return
  config.value.sourceCID = previous.id; config.value.sourcePath = previous.path
  files.value = []; count.value = 0
  await browse()
}
async function scan(file: DriveFile) {
  const entryKey = `${config.value.accountId}/${config.value.sourceCID}/${file.id}`
  const sameEntry = entryKey === scannedEntryKey
  clearSelection(!sameEntry)
  await save()
  const data = await request<{ id: string; files: OrganizeFile[] }>('scan', { method: 'POST', body: JSON.stringify({ accountId: config.value.accountId, parentId: config.value.sourceCID, entryId: file.id }) }, 190000)
  scanId.value = data.id; selectedName.value = file.name
  scannedEntryKey = entryKey
  selectionRoot.value = file.directory ? `${config.value.sourcePath.replace(/\/$/, '')}/${file.name}` : config.value.sourcePath
  rows.value = data.files.filter(f => f.mediaType === 'video').sort((a, b) => a.sourcePath.localeCompare(b.sourcePath, 'zh-CN', { numeric: true })).map(f => ({ ...f, selected: !/(?:^|[. _-])(sample|trailer)(?:[. _-]|$)/i.test(f.name), endEpisode: '' }))
  if (!sameEntry) query.value = file.directory ? file.name : file.name.replace(/\.[^.]+$/, '')
  if (rows.value.length) await scrollToStep(media.value ? 3 : 2)
  notice.value = rows.value.length ? `已扫描 ${rows.value.length} 个视频，${media.value ? '已保留作品及季选择，请核对文件。' : '请指定 TMDB 作品。'}` : '所选条目中没有支持的视频文件。'
}
async function tmdb<T>(path: string): Promise<T> {
  const response = await fetch(`/api/admin/tmdb/${path}`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(45000)]) })
  const body = await response.json().catch(() => null)
  if (!response.ok || !body) throw new Error(body?.error || '读取 TMDB 失败')
  return body
}
async function choose(item: MediaCardItem) {
  media.value = await tmdb(`detail/${item.kind}/${item.id}`)
  batchSeason.value = media.value?.seasons?.find(s => s.number > 0)?.number ?? 1
  await scrollToStep(3)
  notice.value = `已选择《${media.value?.title}》，请核对文件${media.value?.kind === 'tv' ? '与季集编号' : '与版本标记'}。`
}
async function numberingSaved() {
  numberingOpen.value = false
  if (!media.value) return
  const selected = media.value
  preview.value = undefined
  activeStep.value = 3
  await work('更新季集方案', async () => {
    const updated = await tmdb<typeof selected>(`detail/tv/${selected.id}`)
    if (media.value?.id !== selected.id) return
    media.value = updated
    if (!updated.seasons?.some(season => season.number === batchSeason.value)) batchSeason.value = updated.seasons?.find(season => season.number > 0)?.number ?? 1
    notice.value = '全站季集方案已保存，请按新方案核对文件季集编号后重新预览。'
  })
}
async function search(page = 1) {
  const text = query.value.trim(); if (!text) return
  const link = text.match(/themoviedb\.org\/(movie|tv)\/(\d+)/i)
  if (link || /^\d+$/.test(text)) {
    if (!link && kind.value === 'multi') { results.value = []; searchTotalPages.value = 0; notice.value = '纯 TMDB ID 需要先选择电影或剧集，也可以粘贴包含类型的 TMDB 作品链接。'; return }
    const selectedKind = link ? link[1] : kind.value
    const id = Number(link ? link[2] : text)
    const detail = await tmdb<MediaCardItem>(`detail/${selectedKind}/${id}`)
    results.value = [detail]; searchPage.value = 1; searchTotalPages.value = 1
    await choose(detail); return
  }
  const data = await tmdb<{ items: MediaCardItem[]; totalPages: number }>(`browse?kind=${kind.value}&query=${encodeURIComponent(text)}&page=${page}`)
  results.value = data.items; searchPage.value = page; searchTotalPages.value = Math.min(500, data.totalPages)
  if (!data.items.length) notice.value = '没有找到作品，可换一个片名或直接输入 TMDB ID。'
}
function numberEpisodes() {
  let number = Number(startEpisode.value)
  for (const group of directoryGroups(rows.value, selectionRoot.value)) for (const row of group.rows) if (row.selected) { row.season = Number(batchSeason.value); row.episode = number++; row.endEpisode = '' }
}
async function makePreview() {
  if (!media.value) return
  await save()
  preview.value = await request<OrganizeJob>(`preview?accountId=${encodeURIComponent(config.value.accountId)}`, { method: 'POST', body: JSON.stringify({ scanId: scanId.value, kind: media.value.kind, tmdbId: media.value.id, numberingRevision: media.value.numbering?.revision || 0, subtitles: subtitles.value, files: selectedRows.value.map(row => ({ id: row.id, season: Number(row.season), episode: Number(row.episode), endEpisode: Number(row.endEpisode), suffix: row.suffix })) }) }, 190000)
  onlyConflicts.value = false
  await scrollToStep(4)
  notice.value = conflicts.value ? '存在冲突，请调整选择或命名后重新预览。' : '请核对下方原路径和目标路径，确认后执行。'
}
async function execute() {
  if (!preview.value || conflicts.value || active.value || !historyReady.value || historyError.value) return
  const job = await request<OrganizeJob>('jobs', { method: 'POST', body: JSON.stringify({ accountId: config.value.accountId, previewId: preview.value.id }) })
  jobs.value = [job, ...jobs.value.filter(j => j.id !== job.id)]
  preview.value = undefined; scanId.value = ''; rows.value = []; selectedName.value = ''
  cleanupOpen.value = false
  activeStep.value = 1
  historyTab.value = 'organize'
  notice.value = '整理任务已提交，进度见下方记录；离开页面后任务仍会继续。'
  await refreshHistory()
}
async function refreshHistory() {
  const revision = ++historyRevision
  historyRefreshing.value = true
  try {
    const [data, cleanup, subtitles] = await Promise.all([request<{ jobs: OrganizeJob[] }>('jobs'), request<{ jobs: OrganizeCleanup[] }>('cleanup'), request<{ jobs: SubtitleImport[] }>('subtitles')])
    if (revision !== historyRevision || lifecycle.signal.aborted) return
    jobs.value = data.jobs
    cleanupJobs.value = cleanup.jobs
    subtitleJobs.value = subtitles.jobs
    historyError.value = ''; historyReady.value = true; historyRefreshedAt.value = new Date().toISOString()
  } catch (reason) { if (!lifecycle.signal.aborted && revision === historyRevision) historyError.value = message(reason) }
  finally { if (revision === historyRevision) historyRefreshing.value = false }
}
async function poll() {
  await refreshHistory()
  if (!lifecycle.signal.aborted) timer = setTimeout(poll, active.value ? 2000 : 10000)
}
watch(() => config.value.accountId, () => {
  if (applying) return
  config.value.sourceCID = ''; config.value.sourcePath = ''; config.value.targetCID = ''; config.value.targetPath = ''
  directoryDialog.value = ''; files.value = []; count.value = 0; breadcrumbs.value = []
}, { flush: 'sync' })
watch(config, () => { if (!applying) { clearSelection(); deleteIDs.value = []; cleanupPlan.value = undefined } }, { deep: true, flush: 'sync' })
watch([rows, media, subtitles], () => {
  preview.value = undefined
  if (activeStep.value === 4) activeStep.value = scanId.value && media.value ? 3 : 1
}, { deep: true, flush: 'sync' })
watch(kind, () => { results.value = []; searchTotalPages.value = 0 })
onMounted(() => {
  void work('加载配置', async () => {
    const [a, c] = await Promise.all([p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }), request<OrganizeConfig>('config')])
    accounts.value = a.accounts; accountsLoaded.value = true; applying = true; config.value = c; applying = false
    savedConfig.value = JSON.stringify(c)
    if (accountReady.value && c.sourceCID) await browse()
  })
  void poll()
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) clearTimeout(timer) })
</script>

<template>
  <TMDBEpisodeGroupDialog v-if="numberingOpen && media?.kind === 'tv'" :key="media.id" :token="token" :tmdb-id="String(media.id)" :title="media.title" @close="numberingOpen = false" @saved="numberingSaved" />
  <ManagementLayout title="手动整理" description="选择网盘文件，指定 TMDB 作品，核对命名后移动到目标目录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <template #actions><button type="button" class="secondary" @click="shareOpen = true">目录分享</button></template>
    <div class="space-y-6">
      <fieldset :disabled="!!busy" class="min-w-0 space-y-5">
        <section class="panel">
          <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="heading">整理位置</h2><span class="text-xs" :class="configDirty ? 'text-amber-600 dark:text-amber-400' : 'text-slate-400'">{{ configDirty ? '扫描或预览时保存目录设置' : '目录设置已保存' }}</span></div>
          <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" class="mt-4" />
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <div><label class="label" for="organize-source">来源目录</label><button id="organize-source" type="button" class="directory" :disabled="!accountReady" @click="directoryDialog = 'source'">{{ config.sourcePath || '选择待整理目录' }}</button></div>
            <div><label class="label" for="organize-target">目标目录</label><button id="organize-target" type="button" class="directory" :disabled="!accountReady" @click="directoryDialog = 'target'">{{ config.targetPath || '选择整理后的目录' }}</button></div>
          </div>
          <div class="mt-3 flex flex-wrap items-center justify-between gap-3"><p class="text-xs leading-5 text-slate-500 dark:text-slate-400">目标下按作品建目录，剧集按季建目录。</p><button v-if="configDirty" class="secondary" :disabled="!configured" @click="work('保存配置', save)">保存目录设置</button></div>
          <p v-if="scanId" class="mt-2 text-xs text-slate-400">更改整理位置后，需要重新扫描和预览。</p>
        </section>

        <div ref="workflowPanel" class="scroll-mt-6 space-y-4">
          <nav aria-label="整理步骤" class="grid grid-cols-4 gap-1 rounded-2xl border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
            <button v-for="step in stepItems" :key="step.number" type="button" :disabled="step.number > currentStep" :aria-current="activeStep === step.number ? 'step' : undefined" class="step-button" :class="activeStep === step.number ? 'step-active' : ''" @click="scrollToStep(step.number)">
              <span class="step-number"><svg v-if="step.number < currentStep" aria-hidden="true" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m5 12 4 4L19 6" /></svg><template v-else>{{ step.number }}</template></span><span>{{ step.label }}</span>
            </button>
          </nav>
          <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
          <p v-if="busy" role="status" class="rounded-xl bg-blue-50 p-3 text-sm text-blue-700 dark:bg-blue-950/30 dark:text-blue-300">{{ busy }}…</p>
          <p v-else-if="notice" role="status" class="rounded-xl bg-blue-50 p-3 text-sm text-blue-700 dark:bg-blue-950/30 dark:text-blue-300">{{ notice }}</p>
          <div v-if="scanId" class="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl bg-slate-100 px-4 py-3 text-xs dark:bg-slate-800">
            <p class="min-w-0 flex-1 truncate" :title="selectedName">已扫描：{{ selectedName }} · {{ rows.length }} 个视频</p>
            <button v-if="media" class="min-w-0 max-w-full break-words text-left text-blue-600 dark:text-blue-400" @click="scrollToStep(2)">{{ media.title }} · {{ media.date?.slice(0, 4) }} · 更换作品</button>
          </div>
          <section v-show="activeStep === 1" ref="sourcePanel" tabindex="-1" aria-label="选择待整理内容" class="panel">
            <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="heading">选择待整理内容</h2><div class="flex gap-2"><button class="secondary" :disabled="cleanupBlocked" @click="deleteIDs = []; cleanupOpen = true">清理网盘</button><button class="secondary" :disabled="!config.sourceCID || !accountReady" @click="work('读取文件', () => browse())">刷新</button></div></div>
            <div class="my-4 flex items-center gap-3"><button v-if="breadcrumbs.length" class="text-sm text-blue-600" @click="work('读取文件', goBack)">返回上级</button><p class="min-w-0 break-all text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ config.sourcePath || '先选择来源目录' }}</p></div>
            <p class="mb-3 text-xs leading-5 text-slate-500 dark:text-slate-400">每次整理一部作品。进入目录查看内容，或直接扫描整个文件夹。</p>
            <div class="max-h-[420px] overflow-y-auto divide-y divide-slate-100 dark:divide-slate-800">
              <div v-for="file in files" :key="file.id" class="flex flex-wrap items-center gap-2 py-3">
                <span class="shrink-0 text-xs" :class="file.directory ? 'text-amber-600' : 'text-slate-400 dark:text-slate-500'">{{ file.directory ? '目录' : '文件' }}</span>
                <button v-if="file.directory" class="min-w-0 flex-1 break-all text-left text-sm text-blue-700 hover:underline" @click="work('读取文件', () => enter(file))">{{ file.name }}</button>
                <span v-else class="min-w-0 flex-1 break-all text-sm text-slate-700 dark:text-slate-300">{{ file.name }}</span>
                <div class="ml-auto flex w-full shrink-0 justify-end gap-2 sm:w-auto">
                  <button v-if="file.directory" class="secondary" :aria-label="`进入目录 ${file.name}`" @click="work('读取文件', () => enter(file))">进入目录</button>
                  <button class="primary" :disabled="!configured || active" @click="work('扫描所选内容', () => scan(file))">{{ file.directory ? '扫描此目录' : '扫描文件' }}</button>
                </div>
              </div>
              <p v-if="!files.length" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">{{ config.sourceCID ? '当前目录没有文件' : '选择目录后显示网盘内容' }}</p>
            </div>
            <div v-if="count > 100" class="mt-4 flex items-center justify-between text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><button class="secondary" :disabled="offset === 0" @click="work('读取文件', () => browse(Math.max(0, offset - 100)))">上一页</button><span>{{ offset + 1 }}–{{ offset + files.length }} / {{ count }}</span><button class="secondary" :disabled="offset + files.length >= count" @click="work('读取文件', () => browse(offset + files.length))">下一页</button></div>
            <p v-if="!configured && accountReady" class="mt-3 text-sm text-amber-700 dark:text-amber-400">先选择来源目录和目标目录，再扫描内容。</p>
            <div v-if="scanId" class="action-bar"><span class="text-xs text-slate-500">已保留上次扫描，可继续校正。</span><button class="primary" @click="scrollToStep(media ? 3 : 2)">继续整理</button></div>
          </section>

          <section v-show="activeStep === 2" ref="matchPanel" tabindex="-1" aria-label="匹配 TMDB 作品" class="panel">
            <h2 class="heading">匹配 TMDB 作品</h2>
            <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">已填入文件名，可修改为片名，或粘贴 TMDB 作品链接。</p>
            <form class="mt-4 grid grid-cols-[7rem_minmax(0,1fr)] gap-2 sm:flex" @submit.prevent="work('搜索 TMDB', () => search())">
              <SelectMenu v-model="kind" :options="kindOptions" label="作品类型" :disabled="!!busy" class="w-28" />
              <input v-model="query" aria-label="搜索 TMDB" placeholder="片名、TMDB ID 或链接" maxlength="300" class="input col-span-2 row-start-1 min-w-0 flex-1 sm:order-2" />
              <button class="primary sm:order-3" :disabled="!query.trim()">搜索</button>
            </form>
            <div v-if="media" class="mt-4 rounded-xl border border-blue-200 bg-blue-50 p-3 dark:border-blue-900 dark:bg-blue-950/30"><p class="text-sm font-semibold text-blue-800 dark:text-blue-300">已选：{{ media.title }} {{ media.date?.slice(0, 4) }} · {{ media.kind === 'tv' ? '剧集' : '电影' }}</p><p class="mt-1 text-xs text-blue-600 dark:text-blue-400">TMDB {{ media.id }}</p></div>
            <div class="mt-4 max-h-[360px] space-y-2 overflow-y-auto">
              <button v-for="item in results" :key="`${item.kind}:${item.id}`" :aria-pressed="media?.id === item.id && media?.kind === item.kind" class="flex w-full gap-3 rounded-xl border border-slate-200 dark:border-slate-700 p-3 text-left hover:border-blue-400" @click="work('读取作品信息', () => choose(item))">
                <img v-if="item.posterUrl" :src="tmdbImageURL(item.posterUrl)" alt="" class="h-24 w-16 shrink-0 rounded-md object-cover" loading="lazy" />
                <div class="min-w-0"><p class="text-sm font-medium text-slate-800 dark:text-slate-200">{{ item.title }} <span class="text-slate-400 dark:text-slate-500">{{ item.date?.slice(0, 4) }} · {{ item.kind === 'tv' ? '剧集' : '电影' }}</span></p><p class="mt-1 text-xs text-slate-400 dark:text-slate-500">{{ item.originalTitle }} · TMDB {{ item.id }}</p><p class="mt-2 line-clamp-3 text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ item.overview || '暂无简介' }}</p></div>
              </button>
              <p v-if="!results.length && !media" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">搜索后选择正确的作品</p>
            </div>
            <div v-if="searchTotalPages > 1" class="mt-3 flex items-center justify-between text-xs"><button class="secondary" :disabled="searchPage <= 1" @click="work('搜索 TMDB', () => search(searchPage - 1))">上一页</button><span>{{ searchPage }} / {{ searchTotalPages }}</span><button class="secondary" :disabled="searchPage >= searchTotalPages" @click="work('搜索 TMDB', () => search(searchPage + 1))">下一页</button></div>
            <div class="action-bar"><button class="secondary" @click="scrollToStep(1)">返回选择内容</button><button class="primary" :disabled="!media" @click="scrollToStep(3)">校正文件与命名</button></div>
          </section>

        <section v-if="scanId" v-show="activeStep === 3" ref="editPanel" tabindex="-1" aria-label="校正文件与命名" class="panel">
          <h2 class="heading">校正文件与命名</h2>
          <div v-if="media?.kind === 'tv'" class="mt-3 flex flex-wrap items-center gap-3 text-xs"><span class="text-slate-500 dark:text-slate-400">季集方案：{{ numberingLabel(media.numbering) }}</span><button type="button" class="text-blue-600 disabled:opacity-50 dark:text-blue-400" :disabled="!!busy || active" @click="numberingOpen = true">设置全站季集方案</button></div>
          <p class="mt-2 break-all text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ selectedName }} · 已选 {{ selectedRows.length }} / {{ rows.length }} 个视频</p>
          <div class="my-4 flex flex-wrap items-center gap-3 text-sm">
            <label class="flex items-center gap-2"><input v-model="subtitles" type="checkbox" />同时整理匹配的外挂字幕</label>
          </div>
          <div v-if="media?.kind === 'tv'" class="mb-3 flex flex-wrap items-center gap-3 rounded-xl bg-slate-50 dark:bg-slate-950 p-3 text-sm">
            <div class="flex items-center gap-2"><span>季</span><SelectMenu v-model="seasonValue" :options="seasonOptions" label="整理季" :disabled="!!busy || active || media?.kind !== 'tv' || !seasonOptions.length" class="w-48" /></div>
            <label>起始集 <input v-model.number="startEpisode" aria-label="起始集" :disabled="media?.kind !== 'tv' || active" type="number" min="1" max="10000" class="input ml-1 w-20" /></label>
            <button class="secondary" :disabled="media?.kind !== 'tv' || !seasonOptions.length || active || !selectedRows.length || startEpisode < 1" @click="numberEpisodes">按当前顺序连续编号</button>
            <p class="w-full text-xs text-slate-500 dark:text-slate-400">将为全部已选 {{ selectedRows.length }} 个视频编号，包含搜索隐藏或折叠的已选文件。按目录分组及文件名自然排序。</p>
          </div>
          <OrganizeFileSelection :key="scanId" :rows="rows" :root-path="selectionRoot" :tv="media?.kind !== 'movie'" :episode-editable="media?.kind === 'tv'" :disabled="!!busy || active" />
          <p class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">字幕按同目录、原视频文件名匹配，保留语言后缀。未勾选内容保留原位。</p>
          <div class="action-bar"><button class="secondary" @click="scrollToStep(2)">返回匹配作品</button><span class="text-xs text-slate-500">已选 {{ selectedRows.length }} 个视频</span><button class="primary" :disabled="!media || !selectedRows.length || !configured || active" @click="work('生成整理预览', makePreview)">预览整理结果</button></div>
        </section>

        <section v-if="preview" v-show="activeStep === 4" ref="previewPanel" tabindex="-1" aria-label="确认整理预览" class="panel">
          <h2 class="heading">确认整理预览</h2>
          <p class="mt-2 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ preview.title }} · {{ preview.items.length }} 个文件（含字幕）<span v-if="conflicts" class="text-red-600"> · {{ conflicts }} 项冲突</span></p>
          <p v-if="preview.numbering" class="mt-2 text-xs text-slate-500 dark:text-slate-400">本次季集方案：{{ numberingLabel(preview.numbering) }}</p>
          <div class="my-4 grid grid-cols-3 gap-2 rounded-xl bg-slate-50 p-3 text-center dark:bg-slate-950"><p><strong class="block text-xl">{{ previewVideos }}</strong><span class="text-xs text-slate-500">视频</span></p><p><strong class="block text-xl">{{ previewSubtitles }}</strong><span class="text-xs text-slate-500">字幕</span></p><p><strong class="block text-xl" :class="conflicts ? 'text-red-600' : 'text-emerald-600'">{{ conflicts }}</strong><span class="text-xs text-slate-500">冲突</span></p></div>
          <label v-if="conflicts" class="flex items-center gap-2 text-sm text-red-600 dark:text-red-400"><input v-model="onlyConflicts" type="checkbox" />只显示冲突项</label>
          <div class="mt-4 max-h-[480px] space-y-3 overflow-y-auto"><div v-for="item in previewItems" :key="item.file.id" class="rounded-xl border p-3 text-xs leading-6" :class="item.conflict ? 'border-red-200 bg-red-50 dark:border-red-900 dark:bg-red-950/30' : 'border-slate-200 dark:border-slate-700'"><p class="break-all text-slate-500 dark:text-slate-400">原路径：{{ item.file.sourcePath }}</p><p class="break-all text-slate-800 dark:text-slate-200">目标：{{ item.targetPath }}</p><p v-if="item.conflict" class="text-red-600 dark:text-red-400">{{ item.conflict }}</p></div></div>
          <p class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">将修改 115 中的文件名和位置。同名冲突需处理后重新预览。</p>
          <div class="action-bar"><button class="secondary" @click="scrollToStep(3)">返回校正命名</button><button class="primary" :disabled="!!conflicts || active || !historyReady || !!historyError" @click="work('提交整理任务', execute)">确认路径并开始整理</button></div>
        </section>
        </div>
      </fieldset>

      <OrganizeTaskHistory v-model:tab="historyTab" :jobs="jobs" :cleanup-jobs="cleanupJobs" :subtitle-jobs="subtitleJobs" :accounts="accounts" :ready="historyReady" :error="historyError" :refreshing="historyRefreshing" :refreshed-at="historyRefreshedAt" :busy="!!busy" @refresh="refreshHistory" @source="viewHistorySource" @subtitle="supplementSubtitles" />
    </div>
    <P115ActionDialog v-if="shareOpen" title="目录分享" @close="shareOpen = false">
      <p class="text-xs text-slate-500 dark:text-slate-400">分享范围由目录分享配置决定，请核对目标目录。</p>
      <DirectoryShareButton :token="token" :disabled="!historyReady || active || !!busy || !!historyError" :disabled-reason="historyError ? '整理状态读取失败，请刷新任务记录' : '请等待当前整理操作完成后再分享'" />
    </P115ActionDialog>
    <P115ActionDialog v-if="cleanupOpen" title="清理网盘" :busy="!!busy" @close="cleanupOpen = false; deleteIDs = []">
      <p class="break-all text-xs text-slate-500 dark:text-slate-400">{{ config.sourcePath }}</p>
      <p class="text-sm">选择要移入 115 回收站的内容，下一步会核对删除范围。</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-blue-600">{{ notice }}</p>
      <p v-if="busy" role="status" class="text-sm text-slate-500">{{ busy }}…</p>
      <div class="flex flex-wrap items-center justify-between gap-2"><label class="flex items-center gap-2 text-xs"><input type="checkbox" :checked="allPageSelected" :disabled="cleanupBlocked || !!busy || !files.length" @change="togglePageSelection" />全选当前页</label><button class="secondary" :disabled="cleanupBlocked || !!busy" @click="work('扫描空目录', () => previewCleanup('empty'))">清理空目录</button></div>
      <div class="max-h-[45vh] divide-y divide-slate-100 overflow-y-auto dark:divide-slate-800"><label v-for="file in files" :key="file.id" class="flex items-start gap-3 py-3"><input v-model="deleteIDs" :value="file.id" type="checkbox" :aria-label="`勾选删除 ${file.name}`" :disabled="cleanupBlocked || !!busy" class="mt-1 shrink-0" /><span class="min-w-0 break-all text-sm"><span class="mr-2 text-xs text-slate-400">{{ file.directory ? '目录' : '文件' }}</span>{{ file.name }}</span></label><p v-if="!files.length" class="py-6 text-center text-slate-400">当前目录没有内容</p></div>
      <div v-if="count > 100" class="flex items-center justify-between gap-2 text-xs"><button class="secondary" :disabled="!!busy || offset === 0" @click="work('读取文件', () => browse(Math.max(0, offset - 100)))">上一页</button><span>{{ offset + 1 }}–{{ offset + files.length }} / {{ count }}</span><button class="secondary" :disabled="!!busy || offset + files.length >= count" @click="work('读取文件', () => browse(offset + files.length))">下一页</button></div>
      <template #footer><button class="secondary" :disabled="!!busy" @click="cleanupOpen = false; deleteIDs = []">取消</button><button class="danger" :disabled="cleanupBlocked || !!busy || !deleteIDs.length" @click="work('生成删除预览', () => previewCleanup('selected'))">预览删除 {{ deleteIDs.length }} 项</button></template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="directoryDialog" :token="token" :account-id="config.accountId" :title="directoryDialog === 'source' ? '选择来源目录' : '选择目标目录'" endpoint="manual-organize/files" @close="directoryDialog = ''" @select="selectDirectory" />
    <OrganizeCleanupDialog v-if="cleanupPlan" :plan="cleanupPlan" :account-name="accountLabel(cleanupPlan.accountId)" :busy="busy === '执行清理'" :blocked="cleanupBlocked" :error="error" @close="cleanupPlan = undefined" @confirm="work('执行清理', executeCleanup)" />
    <SubtitleImportDialog v-if="subtitleJob" :token="token" :job="subtitleJob" :account-name="accountLabel(subtitleJob.config.accountId)" @close="subtitleJob = undefined" @changed="refreshHistory" />
  </ManagementLayout>
</template>

<style scoped>
.panel { @apply min-w-0 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 text-slate-700 dark:text-slate-200 shadow-sm; }
.heading { @apply text-base font-semibold text-slate-900 dark:text-slate-100; }
.label { @apply text-sm font-medium text-slate-700 dark:text-slate-300; }
.directory { @apply mt-2 w-full break-all rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-left text-sm text-blue-700 dark:text-blue-400 hover:border-blue-300 disabled:opacity-40; }
.input { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 text-sm text-slate-700 dark:text-slate-300 outline-none focus:border-blue-500; }
.primary { @apply rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-40; }
.secondary { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 text-xs text-slate-600 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800 disabled:opacity-40; }
.danger { @apply rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-slate-900 px-3 py-2 text-xs text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 disabled:opacity-40; }
.step-button { @apply flex min-w-0 flex-col items-center justify-center gap-2 rounded-xl px-1 py-3 text-xs text-slate-500 transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40 sm:flex-row sm:text-sm dark:text-slate-400 dark:hover:bg-slate-800; }
.step-number { @apply flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-slate-100 text-xs font-semibold dark:bg-slate-800; }
.step-active { @apply bg-blue-50 font-semibold text-blue-700 dark:bg-blue-950/40 dark:text-blue-300; }
.step-active .step-number { @apply bg-blue-600 text-white; }
.action-bar { @apply sticky bottom-0 z-20 -mx-5 -mb-5 mt-5 flex flex-wrap items-center justify-between gap-3 rounded-b-2xl border-t border-slate-200 bg-white px-5 py-4 shadow-sm dark:border-slate-700 dark:bg-slate-900; padding-bottom: max(1rem, env(safe-area-inset-bottom)); }
button:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
