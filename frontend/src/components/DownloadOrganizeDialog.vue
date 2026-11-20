<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { IconFolder, IconArrowBackUp, IconChevronDown, IconRefresh, IconShare2, IconSubtitles } from '@tabler/icons-vue'
import FloatingDialog from './FloatingDialog.vue'
import OrganizeFileSelection from './OrganizeFileSelection.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import SelectMenu from './SelectMenu.vue'
import { p115Request } from '../lib/p115-api'
import type { DownloadBatchResult, DownloadOrganizeResource, OrganizeJob, OrganizeRow, OrganizeSubtitle } from '../types/organize'
import { useDownloadOrganizeFiles } from '../composables/useDownloadOrganizeFiles'
import { trackDownloadPipeline } from '../composables/useDownloadPipelineToast'
import { applyOrganizeMapping } from '../utils/organizeNaming'
import { useMediaNamingPreview } from '../composables/useMediaNamingPreview'
import { useStickySurface } from '../composables/useStickySurface'
import type { DismissDecision, DismissReason } from '../composables/useMobileSheet'
import { directoryFileRows, directoryGroups } from '../utils/organizeSelection'
import type { NamingResult } from '../types/naming'
import { magnetKey } from '../lib/cloud-download-batch'
import SubtitlePickerDialog from './SubtitlePickerDialog.vue'
import type { LocalSubtitleSelection } from '../utils/subtitleImport'
import { subtitleSelectionIssue } from '../utils/subtitleLimits'
import { subtitleTargetName, type LibrarySubtitleSelection, type SubtitleVersion } from '../utils/subtitleLibrary'
import { formatEpisodeRanges } from '../types/seasonLibrary'

const props = withDefaults(defineProps<{
  active?: boolean
  token: string; entries: DownloadOrganizeResource[]; hiddenCount?: number; batch?: boolean
  tmdbId: number; kind: 'movie' | 'tv'; title: string; date: string
  seasons: { number: number; name: string; episodeCount: number }[]
  themeColors?: Record<string, string>
  animationLibrary?: boolean; originalTitle?: string
}>(), { active: true })
const emit = defineEmits<{ close: []; submitted: [keys: string[]]; uncertain: [keys: string[]] }>()
const lifecycle = new AbortController()
const preparation = useDownloadOrganizeFiles(props.token, props.entries, !!props.batch, lifecycle.signal)
const { rows, subtitles, sources, parsing, active, unresolved, readyCount, states } = preparation
const busy = ref(false), loading = ref(true), error = ref(''), notice = ref('')
const subtitleOpen = ref(false), externalSubtitles = ref<LibrarySubtitleSelection[]>([]), subtitleVersion = ref<SubtitleVersion>()
const localSubtitles = ref<LocalSubtitleSelection[]>([])
const subtitleScope = ref<number>()
const subtitleVideos = computed(() => selected.value.filter(row => subtitleScope.value === undefined || row.season === subtitleScope.value))
const subtitleIDs = computed(() => new Set(subtitleVideos.value.map(row => row.id)))
const scopedLibrary = computed(() => externalSubtitles.value.filter(sub => subtitleIDs.value.has(sub.videoId)))
const scopedLocal = computed(() => localSubtitles.value.filter(sub => subtitleIDs.value.has(sub.videoId)))
function openSubtitles(targetSeason?: number) { subtitleScope.value = targetSeason; subtitleOpen.value = true }
const selectedLibrary = computed(() => externalSubtitles.value.filter(sub => selected.value.some(row => row.id === sub.videoId)))
const selectedLocal = computed(() => localSubtitles.value.filter(sub => selected.value.some(row => row.id === sub.videoId)))
const localSubtitleCount = computed(() => selectedLocal.value.filter(sub => sub.file.origin !== 'assrt').length)
const assrtSubtitleCount = computed(() => selectedLocal.value.length - localSubtitleCount.value)
const allExternal = computed(() => [...externalSubtitles.value, ...localSubtitles.value])
const selectedExternal = computed(() => [...selectedLibrary.value, ...selectedLocal.value])
const externalIssue = computed(() => subtitleSelectionIssue(selectedExternal.value.length, selectedExternal.value.reduce((sum, sub) => sum + ('localId' in sub ? sub.file.data.length : sub.file.size), 0)))
const externalName = (sub: LibrarySubtitleSelection | LocalSubtitleSelection) => subtitleTargetName(results.value[sub.videoId] ? `${results.value[sub.videoId]!.stem}.mkv` : rows.value.find(row => row.id === sub.videoId)?.name || '', sub.file.name, sub.language, sub.version)
function selectSubtitles(library: LibrarySubtitleSelection[], local: LocalSubtitleSelection[], version?: SubtitleVersion) {
  if (!props.active || !subtitleOpen.value) return
  const ids = subtitleIDs.value
  externalSubtitles.value = [...externalSubtitles.value.filter(sub => !ids.has(sub.videoId)), ...library]
  localSubtitles.value = [...localSubtitles.value.filter(sub => !ids.has(sub.videoId)), ...local]
  subtitleVersion.value = version || (externalSubtitles.value.length ? subtitleVersion.value : undefined)
  subtitleOpen.value = false
}
const confirming = ref(false)
const shareIssue = ref('正在读取分享设置…'), shareAction = ref(false)
const errorElement = ref<HTMLElement>()
watch(error, async value => { if (value) { await nextTick(); errorElement.value?.scrollIntoView({ block: 'nearest' }) } })
const batchOpen = ref(false)
const mappingPanelID = useId()
const batchToggleButton = ref<HTMLButtonElement>()
const mappingPanel = ref<HTMLElement>()
const mappingStyle = ref<Record<string, string>>({})
const mappingError = ref('')

function positionMappingPanel() {
  const anchor = numberingBar.value || batchToggleButton.value
  if (!anchor) return
  const rect = anchor.getBoundingClientRect()
  const viewport = window.visualViewport
  const viewportWidth = viewport?.width ?? window.innerWidth
  const viewportHeight = viewport?.height ?? window.innerHeight
  const viewportLeft = viewport?.offsetLeft ?? 0
  const viewportTop = viewport?.offsetTop ?? 0
  const compact = viewportWidth < 640
  const inset = compact ? 8 : 12
  const gap = 4
  const width = Math.min(compact ? viewportWidth : Math.max(rect.width - inset * 2, 460), viewportWidth - inset * 2)
  const left = compact ? viewportLeft + inset : Math.max(viewportLeft + inset, Math.min(rect.left + inset, viewportLeft + viewportWidth - width - inset))
  const below = viewportTop + viewportHeight - rect.bottom - gap - inset
  const above = rect.top - viewportTop - gap - inset
  const upward = below < 180 && above > below
  const available = upward ? above : below
  const fitsByAnchor = available >= 44
  const maxHeight = Math.min(360, fitsByAnchor ? available : viewportHeight - inset * 2)
  mappingStyle.value = {
    width: `${width}px`,
    maxHeight: `${maxHeight}px`,
    left: `${left}px`,
    top: fitsByAnchor && upward ? 'auto' : `${fitsByAnchor ? Math.max(viewportTop + inset, rect.bottom + gap) : viewportTop + inset}px`,
    bottom: fitsByAnchor && upward ? `${window.innerHeight - rect.top + gap}px` : 'auto',
  }
}

async function openBatch() {
  if (batchOpen.value || editDisabled.value || !editing.value.length) return
  mappingError.value = ''
  positionMappingPanel()
  batchOpen.value = true
  await nextTick()
  if (!batchOpen.value || !mappingPanel.value) return
  mappingPanel.value.showPopover()
  mappingPanel.value.focus({ preventScroll: true })
}

function closeBatch(restoreFocus = false) {
  mappingError.value = ''
  if (mappingPanel.value?.matches(':popover-open')) mappingPanel.value.hidePopover()
  if (!batchOpen.value) return
  batchOpen.value = false
  if (restoreFocus) void nextTick(() => batchToggleButton.value?.focus({ preventScroll: true }))
}

function toggleBatch() {
  if (batchOpen.value) closeBatch(true)
  else void openBatch()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (!batchOpen.value || event.key !== 'Escape' || event.defaultPrevented) return
  if (!(event.target instanceof Node) || !scrollBody.value?.closest('dialog')?.contains(event.target)) return
  event.preventDefault()
  event.stopPropagation()
  closeBatch(true)
}

function onDocumentPointerDown(event: PointerEvent) {
  if (!batchOpen.value) return
  const target = event.target
  if (target instanceof Element && target.closest('dialog[role="alertdialog"]')) return
  if (target instanceof Node && !mappingPanel.value?.contains(target) && !batchToggleButton.value?.contains(target)) closeBatch(false)
}

function onViewportScroll(event: Event) {
  if (!batchOpen.value) return
  const target = event.target
  if (target instanceof Node && mappingPanel.value?.contains(target)) return
  positionMappingPanel()
}

function onViewportResize() {
  if (batchOpen.value) positionMappingPanel()
}

function beforeDialogDismiss(reason: DismissReason): DismissDecision {
  if (reason !== 'escape' || !batchOpen.value) return 'dismiss'
  closeBatch(true)
  return 'handled'
}
const floatingDialog = ref<InstanceType<typeof FloatingDialog>>()
const scrollBody = computed(() => floatingDialog.value?.body)
const numberingAnchor = ref<HTMLElement>()
const numberingStuck = useStickySurface(scrollBody, numberingAnchor)
const numberingBar = ref<HTMLElement>(), numberingHeight = ref(0)
watch(numberingBar, (element, _, onCleanup) => {
  const measure = () => { numberingHeight.value = element?.offsetHeight || 0 }
  measure()
  if (!element) return
  const observer = new ResizeObserver(measure)
  observer.observe(element)
  onCleanup(() => observer.disconnect())
}, { flush: 'post' })
const editDisabled = computed(() => busy.value || loading.value || confirming.value)
watch(editDisabled, disabled => { if (disabled) closeBatch() })
const directoryOpen = ref(false), accountId = ref(''), targetCID = ref(''), targetPath = ref(''), downloadPath = ref('')
const defaultTarget = ref({ id: '', path: '' })
const targetChanged = computed(() => targetCID.value !== defaultTarget.value.id)
watch(() => props.active, active => {
  if (!active) {
    // 未确认的目录和字幕草稿随子页关闭；滚动位置由共享页面保存。
    directoryOpen.value = false
    subtitleOpen.value = false
    closeBatch(false)
  }
}, { flush: 'pre' })
function selectDirectory(value: { id: string; path: string }) {
  if (!props.active || !directoryOpen.value) return
  targetCID.value = value.id
  targetPath.value = value.path
  directoryOpen.value = false
}
const season = ref(String(props.seasons.find(s => s.number > 0)?.number ?? 1)), sourceStart = ref(1), targetStart = ref(1)
const stagedIDs = ref(new Set<string>())
const staged = computed(() => rows.value.filter(row => stagedIDs.value.has(row.id)))
const pendingRows = computed(() => rows.value.filter(row => !stagedIDs.value.has(row.id)))
const editing = computed(() => pendingRows.value.filter(row => row.selected))
watch(() => editing.value.length, count => { if (!count) closeBatch(false) })
watch([season, sourceStart, targetStart], () => { mappingError.value = '' })
const selected = computed(() => rows.value.filter(row => stagedIDs.value.has(row.id) || row.selected))
const selectedKeys = computed(() => [...new Set(selected.value.map(row => preparation.owners.get(row.id)!.key))])
const stagedGroups = computed(() => {
  const groups = new Map<number, OrganizeRow[]>()
  for (const row of staged.value) { const list = groups.get(row.season) || []; list.push(row); groups.set(row.season, list) }
  return [...groups].sort(([a], [b]) => a - b).map(([number, files]) => ({ number, rows: files, label: props.kind === 'movie' ? '电影' : number === 0 ? '特别篇' : `第 ${number} 季`, range: formatEpisodeRanges(files.map(row => row.episode)) }))
})
const titleOnlyCount = computed(() => rows.value.filter(row => row.titleOnly).length)
const seasonOptions = computed(() => props.seasons.map(s => {
  const label = s.number ? `第 ${s.number} 季` : '特别篇'
  return { value: String(s.number), label: [label, s.name && s.name !== label ? s.name : '', `${s.episodeCount} 集`].filter(Boolean).join(' · ') }
}))
const selectedSeason = computed(() => props.seasons.find(s => String(s.number) === season.value))
const fileSelection = ref<InstanceType<typeof OrganizeFileSelection>>()
const hasUnsavedDraft = computed(() => confirming.value || preparation.hasFileChanges.value || staged.value.length > 0
  || !!fileSelection.value?.hasExcludedDirectories || targetChanged.value
  || allExternal.value.some(sub => rows.value.some(row => row.id === sub.videoId)))
defineExpose({ busy, hasUnsavedDraft })
const lastProblem = ref('')
const namingNumber = (value: unknown) => Number.isInteger(Number(value)) ? Number(value) : -1
const namingPreview = useMediaNamingPreview(props.token, () => rows.value.length && !parsing.value ? {
  kind: props.kind, tmdbId: props.tmdbId,
  files: rows.value.map(row => ({ id: row.id, name: row.name, titleOnly: row.titleOnly, namingValues: row.namingValues, season: namingNumber(row.season), episode: namingNumber(row.episode) })),
  subtitles: subtitles.value.filter(sub => sub.videoId).map(sub => ({ file: { id: sub.file.id, name: sub.file.name }, videoId: sub.videoId, tail: sub.tail })),
} : undefined, lifecycle.signal)
type PlannedName = Pick<NamingResult, 'directory' | 'stem' | 'name'>
const planKey = ref(''), planResults = ref<Record<string, PlannedName>>({})
const results = computed(() => planKey.value === snapshotKey.value ? { ...namingPreview.results.value, ...planResults.value } : namingPreview.results.value)
const sharedDirectory = computed(() => {
  const directories = rows.value.map(row => results.value[row.id]?.directory)
  return directories.length > 1 && directories[0] && directories.every(directory => directory === directories[0]) ? directories[0] : ''
})
const targetNames = computed(() => Object.fromEntries(rows.value.map(row => [row.id, namingPreview.errors.value[row.id] ? '' : results.value[row.id]?.name || '等待命名预览'])))
const subtitleName = (sub: OrganizeSubtitle) => results.value[sub.file.id]?.name || '等待命名预览'
const subtitlesByVideo = computed(() => {
  const groups: Record<string, OrganizeSubtitle[]> = {}
  for (const sub of subtitles.value) if (sub.videoId) (groups[sub.videoId] ||= []).push(sub)
  return groups
})
const unmatchedSubtitles = computed(() => subtitles.value.filter(sub => !sub.videoId))
const selectedSubtitleCount = computed(() => selected.value.reduce((count, row) => count + (subtitlesByVideo.value[row.id]?.length || 0), 0))
const invalid = computed(() => selected.value.some(row => !results.value[row.id] || !!namingPreview.errors.value[row.id] || (subtitlesByVideo.value[row.id] || []).some(sub => !results.value[sub.file.id] || !!namingPreview.errors.value[sub.file.id])))
const undo = ref<ReturnType<typeof applyOrganizeMapping>>([])
const conflicts = ref<Record<string, string>>({})
function findConflicts(videos: OrganizeRow[]) {
  const targets = new Map<string, string[]>(), out: Record<string, string> = {}
  const ids = new Set(videos.map(row => row.id))
  for (const row of videos) {
    const names = [{ id: row.id, result: results.value[row.id] }, ...(subtitlesByVideo.value[row.id] || []).map(sub => ({ id: sub.file.id, result: results.value[sub.file.id] }))]
    for (const file of names) {
      if (!file.result) continue
      const key = `${file.result.directory}/${file.id === row.id ? file.result.stem : file.result.name}`.toLowerCase()
      const ids = targets.get(key) || []; ids.push(file.id); targets.set(key, ids)
    }
  }
  for (const sub of allExternal.value.filter(sub => ids.has(sub.videoId))) {
    const video = results.value[sub.videoId]
    if (!video) continue
    const key = `${video.directory}/${externalName(sub)}`.toLowerCase()
    const ids = targets.get(key) || []; ids.push(sub.videoId); targets.set(key, ids)
  }
  for (const ids of targets.values()) if (ids.length > 1) for (const id of ids) out[id] = '本次多个文件目标同名，请核对季集编号、命名模板或取消重复文件'
  return out
}
const localConflicts = computed(() => findConflicts(selected.value))
const displayedConflicts = computed(() => {
  const selectedIDs = selected.value.flatMap(row => [row.id, ...(subtitlesByVideo.value[row.id] || []).map(sub => sub.file.id)])
  return { ...conflicts.value, ...localConflicts.value, ...Object.fromEntries(selectedIDs.filter(id => namingPreview.errors.value[id]).map(id => [id, namingPreview.errors.value[id]!])) }
})
const shortConflicts = computed(() => Object.fromEntries(Object.keys(localConflicts.value).filter(id => displayedConflicts.value[id] === localConflicts.value[id]).map(id => [id, '目标重名'])))
const rowProblems = computed(() => Object.fromEntries(selected.value.flatMap(row => {
  const issue = displayedConflicts.value[row.id] || ((subtitlesByVideo.value[row.id] || []).some(sub => displayedConflicts.value[sub.file.id]) ? '关联字幕存在命名问题，请展开查看' : '')
  return issue ? [[row.id, issue]] : []
})))
async function locateProblem() {
  const ids = Object.keys(rowProblems.value)
  const id = ids[(ids.indexOf(lastProblem.value) + 1) % ids.length]
  if (id) {
    lastProblem.value = id
    if (stagedIDs.value.has(id)) await returnToEditing([rows.value.find(row => row.id === id)!])
    else await fileSelection.value?.locateIssue(id)
  }
}
async function stageFiles() {
  if (editDisabled.value || parsing.value || !namingPreview.ready.value) return
  const ready = editing.value.filter(row => !rowProblems.value[row.id])
  const remaining = editing.value.length - ready.length
  const problem = editing.value.find(row => rowProblems.value[row.id])
  for (const row of ready) { stagedIDs.value.add(row.id); row.selected = false }
  undo.value = []
  notice.value = remaining ? `已暂存 ${ready.length} 个视频，${remaining} 个文件存在问题，请修正或取消勾选` : ''
  await nextTick()
  if (problem) await fileSelection.value?.locateIssue(problem.id)
  else if (ready[0]) {
    const summary = scrollBody.value?.querySelector<HTMLElement>(`.confirmed-group[data-staged-season="${ready[0].season}"] summary`)
    summary?.scrollIntoView({ block: 'nearest' })
    summary?.focus({ preventScroll: true })
  }
}
async function returnToEditing(files: OrganizeRow[]) {
  if (editDisabled.value) return
  for (const row of pendingRows.value) row.selected = false
  for (const row of files) { stagedIDs.value.delete(row.id); row.selected = true }
  undo.value = []; error.value = ''; notice.value = `已退回 ${files.length} 个视频，季集与字幕关联已保留`
  await nextTick()
  fileSelection.value?.revealRows(files.map(row => row.id))
  if (files[0]) await fileSelection.value?.locateIssue(files[0].id)
}
const previewId = ref('')
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(130000)])
function resourceSelections() {
  return preparation.resourceSelections(selected.value).map(resource => ({ ...resource,
    subtitles: selectedExternal.value.filter(sub => preparation.owners.get(sub.videoId)?.key === magnetKey(resource.magnet))
      .map(sub => ({ language: sub.language, version: sub.version, videoId: preparation.owners.get(sub.videoId)!.id, ...('localId' in sub
        ? { localId: sub.localId, originalName: sub.file.name, sha256: sub.file.sha256, size: sub.file.data.length }
        : { versionId: sub.versionId, path: sub.path, sha: sub.file.sha }) })),
  }))
}
const snapshotKey = computed(() => JSON.stringify({ shareSTRM: shareAction.value, naming: namingPreview.value.value, targetCID: shareAction.value ? undefined : targetCID.value, targetPath: shareAction.value ? undefined : targetPath.value, resources: resourceSelections() }))
const submitDisabled = computed(() => busy.value || loading.value || !confirming.value && !namingPreview.ready.value || parsing.value || !accountId.value || !selected.value.length || invalid.value || !!externalIssue.value || !!Object.keys(localConflicts.value).length)
watch(snapshotKey, () => { conflicts.value = {} }, { flush: 'sync' })
let previewKey = ''
let uploadedPreview = ''

function mapFiles(sequential = false) {
  mappingError.value = ''; error.value = ''; notice.value = ''; conflicts.value = {}
  try {
    undo.value = applyOrganizeMapping(props.batch ? pendingRows.value : directoryFileRows(directoryGroups(pendingRows.value, '/')[0]!), sources, Number(season.value), sourceStart.value, targetStart.value, sequential)
    const skipped = editing.value.length - undo.value.length
    notice.value = `已更新 ${undo.value.length} 项${skipped ? `，跳过 ${skipped} 项无发布编号的文件，可在行内填写目标季集` : !sequential ? '，保留发布编号的缺号和连集跨度' : ''}`
    closeBatch(true)
  } catch (e) { mappingError.value = e instanceof Error ? e.message : '编号设置无效' }
}
function undoMapping() {
  for (const before of undo.value) { const row = rows.value.find(row => row.id === before.id); if (row) Object.assign(row, before) }
  undo.value = []; notice.value = '已撤销上次批量编号'; conflicts.value = {}
}
async function loadConfig() {
  loading.value = true; error.value = ''
  try {
    const [cloud, organize, share] = await Promise.all([
      p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', { signal: signal() }),
      p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'download-organize/config', { signal: signal() }).catch(() => undefined),
      p115Request<{ ready: boolean; reason: string }>(props.token, 'download-organize/share-config', { signal: signal() }).catch(() => ({ ready: false, reason: '读取分享设置失败，可重新读取配置' })),
    ])
    if (!cloud.accountId || !cloud.targetCID) throw new Error('请先在 115 云下载中配置主账号的下载目录')
    const sameAccount = accountId.value === cloud.accountId
    const keepTarget = sameAccount && targetChanged.value
    accountId.value = cloud.accountId; downloadPath.value = cloud.targetPath
    // 同一主账号下，重读默认配置保留用户本次选择。换号后只采用新账号的配置。
    if (organize || !sameAccount) {
      defaultTarget.value = organize?.accountId === cloud.accountId ? { id: organize.targetCID, path: organize.targetPath } : { id: '', path: '' }
      if (!keepTarget) {
        targetCID.value = defaultTarget.value.id
        targetPath.value = defaultTarget.value.path
      }
    }
    shareIssue.value = share.ready ? '' : share.reason || '请先完善分享 STRM 设置'
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取目录配置失败' }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
async function start(sharing = false) {
  if (confirming.value && sharing !== shareAction.value || sharing && shareIssue.value && !confirming.value) return
  if (busy.value || loading.value || !confirming.value && !namingPreview.ready.value || parsing.value || !selected.value.length || invalid.value || externalIssue.value || Object.keys(localConflicts.value).length || !sharing && !targetCID.value || !accountId.value) return
  busy.value = true; error.value = ''; notice.value = ''
  shareAction.value = sharing
  let creationAttempt = false
  try {
    const key = snapshotKey.value
    if (!confirming.value && (!previewId.value || previewKey !== key)) {
      const payload = { shareSTRM: sharing, namingRevision: namingPreview.value.value?.naming.revision, tmdbId: props.tmdbId, kind: props.kind, accountId: accountId.value, targetCID: sharing ? undefined : targetCID.value, targetPath: sharing ? undefined : targetPath.value }
      const resources = resourceSelections()
      const previews = props.batch
        ? (await p115Request<DownloadBatchResult>(props.token, 'download-organize/batch/preview', { method: 'POST', signal: signal(), body: JSON.stringify({ ...payload, resources }) }))
        : { id: '', jobs: [await p115Request<OrganizeJob>(props.token, 'download-organize/preview', { method: 'POST', signal: signal(), body: JSON.stringify({ ...payload, ...resources[0] }) })] }
      conflicts.value = Object.fromEntries(previews.jobs.flatMap(preview => preview.items.filter(item => item.conflict).map(item => [props.batch ? `${preview.download!.hash}:${item.file.id}` : item.file.id, item.conflict!])))
      if (Object.keys(conflicts.value).length) throw new Error('命名或目标目录存在冲突，请调整标出的文件')
      const official: Record<string, PlannedName> = {}
      for (const preview of previews.jobs) {
        if (preview.naming?.revision !== payload.namingRevision) throw Object.assign(new Error('任务命名模板与预览不一致，请刷新命名预览'), { status: 409 })
        for (const item of preview.items) {
          const id = props.batch ? `${preview.download!.hash}:${item.file.id}` : item.file.id
          if (!results.value[id] || !item.stem || typeof item.directory !== 'string' || !item.name || official[id]) throw new Error('任务命名预览结果不完整，请重试')
          const isolated = preview.download?.share?.directory
          if (sharing && (!isolated || item.directory !== isolated && !item.directory.startsWith(`${isolated}/`))) throw new Error('分享整理缺少独立结果目录，请重新预览')
          official[id] = { directory: isolated ? item.directory.slice(isolated.length).replace(/^\//, '') : item.directory, stem: item.stem, name: item.name }
        }
      }
      const expected = selected.value.flatMap(row => [row.id, ...(subtitlesByVideo.value[row.id] || []).map(sub => sub.file.id)])
      if (expected.some(id => !official[id]) || Object.keys(official).length !== expected.length) throw new Error('任务文件与所选文件不一致，请重新预览')
      const changed = expected.some(id => official[id]!.name !== results.value[id]!.name || official[id]!.directory !== results.value[id]!.directory)
      previewId.value = props.batch ? previews.id : previews.jobs[0]!.id; previewKey = key
      if (!previewId.value) throw new Error('任务预览缺少编号，请重试')
      planResults.value = official; planKey.value = key
      if (changed) { notice.value = '最终命名有变化，已更新下方预览，请核对后再次确认。'; return }
    }
    if (!confirming.value && selectedLocal.value.length && uploadedPreview !== previewId.value) {
      const body = new FormData(); body.append('previewId', previewId.value)
      for (const sub of selectedLocal.value) body.append(sub.localId, new Blob([new Uint8Array(sub.file.data)]), sub.file.name.split(/[\\/]/).pop()!)
      await p115Request<void>(props.token, 'download-organize/subtitles/attachments', { method: 'POST', signal: signal(), body })
      uploadedPreview = previewId.value
    }
    // 回执丢失时复用同一编号，服务端先保存任务再提交下载。
    creationAttempt = true
    const created = props.batch
      ? await p115Request<DownloadBatchResult>(props.token, 'download-organize/batch/jobs', { method: 'POST', signal: signal(), body: JSON.stringify({ previewId: previewId.value }) })
      : { id: '', jobs: [await p115Request<OrganizeJob>(props.token, 'download-organize/jobs', { method: 'POST', signal: signal(), body: JSON.stringify({ previewId: previewId.value }) })] }
    const pipelineID = created.id || created.jobs[0]?.id
    if (!pipelineID || !created.jobs.length) throw new Error('创建回执缺少任务编号，请确认任务状态')
    confirming.value = false
    const summary = [props.title, `${created.jobs.length} 项资源 · ${selected.value.length} 个视频`, shareAction.value ? '下载 → 整理 → STRM' : '下载 → 整理'].join(' · ')
    trackDownloadPipeline(props.token, pipelineID, summary)
    emit('submitted', selectedKeys.value)
  } catch (e) {
    if (!lifecycle.signal.aborted) {
      const status = (e as { status?: number }).status
      if (creationAttempt && (!status || status >= 500)) {
        confirming.value = true; emit('uncertain', selectedKeys.value)
        error.value = '创建结果暂未确认，请点击“确认任务状态”读取同一任务，或在整理任务记录中核对。'
      } else {
        confirming.value = false
        if (creationAttempt) previewId.value = ''
        if (!creationAttempt && status === 409) { previewId.value = ''; namingPreview.refresh() }
        error.value = e instanceof Error ? e.message : '提交失败，请重试'
      }
    }
  }
  finally { busy.value = false }
}
onMounted(() => {
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onDocumentKeydown)
  document.addEventListener('scroll', onViewportScroll, true)
  window.addEventListener('resize', onViewportResize)
  window.visualViewport?.addEventListener('resize', onViewportResize)
  window.visualViewport?.addEventListener('scroll', onViewportResize)
  void loadConfig()
  void preparation.parse()
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onDocumentKeydown)
  document.removeEventListener('scroll', onViewportScroll, true)
  window.removeEventListener('resize', onViewportResize)
  window.visualViewport?.removeEventListener('resize', onViewportResize)
  window.visualViewport?.removeEventListener('scroll', onViewportResize)
  if (mappingPanel.value?.matches(':popover-open')) mappingPanel.value.hidePopover()
  lifecycle.abort()
})
</script>

<template>
  <FloatingDialog ref="floatingDialog" class="download-organize-dialog" :active="props.active" :title="`下载并整理 · ${title}`" close-label="关闭下载并整理" size="organize" :close-disabled="busy" :before-dismiss="beforeDialogDismiss" :style="themeColors" @close="emit('close')">
  <section class="download-organize" aria-label="下载并整理映射" :aria-busy="busy || loading" :style="{ '--organize-sticky-offset': `${numberingHeight}px` }">
    <p class="resource-title" :title="batch ? undefined : entries[0]?.resource.title">{{ batch ? `批量整理 ${active.length} 项资源${hiddenCount ? `，包含外层筛选隐藏的 ${hiddenCount} 项` : ''}` : entries[0]?.resource.title }}</p>
      <div class="organize-config">
        <div class="directory-bar">
          <IconFolder :size="17" class="shrink-0 text-content-secondary" aria-hidden="true" />
          <div class="directory-info">
            <p><span class="directory-label">整理到</span><strong :title="targetPath">{{ targetPath || '请选择整理目录' }}</strong></p>
            <p v-if="sharedDirectory" class="scope-note" :title="`公共子目录：${sharedDirectory}`">公共子目录：{{ sharedDirectory }}</p>
          </div>
          <div class="directory-actions">
            <button type="button" class="secondary directory-pick" :disabled="editDisabled || !accountId" @click="directoryOpen = true">选择目录</button>
          </div>
        </div>
        <details class="naming-details" :class="{ 'has-issue': Boolean(namingPreview.error.value) }">
          <summary>
            <IconChevronDown :size="14" class="shrink-0 transition-transform" aria-hidden="true" />
            <span>命名设置</span>
            <span v-if="namingPreview.error.value" class="naming-state is-error" role="status">命名异常</span>
            <span v-else-if="namingPreview.pending.value" class="naming-state" role="status">更新中…</span>
            <span v-else-if="namingPreview.value.value" class="naming-state">版本 {{ namingPreview.value.value.naming.revision }}</span>
          </summary>
          <div class="naming-panel">
            <div class="naming-summary">
              <RouterLink :to="{ name: 'naming-settings', query: kind === 'tv' ? { kind: 'tv' } : {} }" target="_blank" rel="noopener">公共命名模板</RouterLink>
              <span v-if="namingPreview.pending.value || namingPreview.error.value" role="status">{{ namingPreview.pending.value ? '正在更新命名…' : namingPreview.error.value }}</span>
              <button type="button" class="secondary tool-action" :disabled="editDisabled || parsing || namingPreview.pending.value" @click="namingPreview.refresh"><IconRefresh :size="15" aria-hidden="true" />刷新命名</button>
            </div>
            <p v-if="targetPath" class="scope-note break-all">整理目录：{{ targetPath }}</p>
            <p v-if="sharedDirectory" class="scope-note break-all">公共子目录：{{ sharedDirectory }}</p>
          </div>
        </details>
      </div>
      <template v-if="batch">
        <div class="resource-progress"><span role="status">已解析 {{ readyCount }} / {{ active.length }} 项资源<span v-if="unresolved.length && !parsing">，{{ unresolved.length }} 项待处理</span></span><button v-if="parsing" class="secondary" @click="preparation.pause">停止解析</button><button v-else-if="states.some(state => state.status === 'waiting')" class="secondary" :disabled="editDisabled" @click="preparation.parse">继续解析</button></div>
        <details class="resource-status"><summary>查看全部已选资源</summary><div class="resource-status-list"><div v-for="state in states.filter(state => state.status !== 'removed')" :key="state.key"><p>{{ state.resource.title }}<small>{{ state.status === 'ready' ? state.files?.files.some(file => file.titleOnly) ? '按标题识别' : '已读取文件清单' : state.status === 'loading' ? '识别中' : state.status === 'error' ? state.error : '待识别' }}</small></p><button class="secondary" :disabled="editDisabled" :aria-label="`移除资源 ${state.resource.title}`" @click="preparation.remove(state.key)">移除</button></div></div></details>
        <div v-for="state in states.filter(state => state.status === 'error')" :key="state.key" class="resource-error"><p role="alert">{{ state.resource.title }}：{{ state.error }}</p><button class="secondary" :disabled="editDisabled || parsing" @click="preparation.retry(state.key)">重试解析</button><button class="secondary" :disabled="editDisabled" @click="preparation.remove(state.key)">移除本次整理</button><span>可关闭弹窗后普通离线，失败项不会自动提交。</span></div>
      </template>
      <section class="organize-workbench" aria-label="文件整理">
      <header class="workbench-heading">
        <div class="workbench-title"><h3>文件整理</h3><p class="workbench-counts"><span>待处理 {{ pendingRows.length }} 个视频</span><span> · 已勾选 {{ editing.length }} 个</span><span v-if="staged.length"> · 已暂存 {{ staged.length }} 个</span></p></div>
        <div class="workbench-actions">
          <button type="button" class="secondary subtitle-action" aria-label="查找全部所选字幕" :disabled="editDisabled || parsing || !selected.length" :title="!selected.length ? '请先勾选需要整理的视频' : `为已暂存和当前勾选的 ${selected.length} 个视频查找字幕`" @click="openSubtitles()"><IconSubtitles :size="16" aria-hidden="true" />全部所选字幕<span v-if="selected.length" class="action-count">{{ selected.length }}</span></button>
        </div>
      </header>
      <section v-if="staged.length || selectedExternal.length" class="confirmation-zone" aria-label="已暂存的整理文件">
        <header v-if="staged.length" class="confirmation-heading" aria-live="polite" aria-atomic="true"><strong>已暂存 {{ staged.length }} 个视频</strong><span>与当前勾选合并提交</span></header>
        <div v-for="group in stagedGroups" :key="group.number" class="confirmed-group" :data-staged-season="group.number">
          <details>
            <summary>
              <span class="confirmed-summary-main">
                <IconChevronDown :size="14" class="confirmed-chevron" aria-hidden="true" />
                <strong>{{ group.label }}</strong>
                <span>{{ group.rows.length }} 个视频</span>
                <span v-if="kind === 'tv'" class="confirmed-range" :title="group.range">集号 {{ group.range }}</span>
              </span>
              <span class="confirmed-actions" @click.stop @keydown.space.stop @keydown.enter.stop>
                <button type="button" class="secondary tool-action" :disabled="editDisabled" :aria-label="`查找${group.label}字幕`" @click="openSubtitles(group.number)"><IconSubtitles :size="15" aria-hidden="true" />{{ kind === 'movie' ? '电影字幕' : '本季字幕' }}</button>
                <button type="button" class="secondary tool-action" :disabled="editDisabled" :aria-label="`退回编辑${group.label}`" @click="returnToEditing(group.rows)"><IconArrowBackUp :size="15" aria-hidden="true" />退回编辑</button>
              </span>
            </summary>
            <ul>
              <li v-for="row in group.rows" :key="row.id">
                <p class="confirmed-target">整理后：{{ targetNames[row.id] }}</p>
                <p class="confirmed-source" :title="row.sourcePath">原名：<span>{{ row.name }}</span></p>
                <p v-if="rowProblems[row.id]" role="alert" class="error">{{ rowProblems[row.id] }}</p>
                <p v-for="sub in subtitlesByVideo[row.id] || []" :key="sub.file.id" class="confirmed-target">字幕：{{ subtitleName(sub) }}</p>
                <p v-for="sub in allExternal.filter(sub => sub.videoId === row.id)" :key="'localId' in sub ? sub.localId : sub.path" class="confirmed-target">外部字幕：{{ externalName(sub) }}</p>
              </li>
            </ul>
          </details>
        </div>
        <p v-if="selectedExternal.length" class="confirmation-subtitles">外部字幕 {{ selectedExternal.length }} 个<span v-if="localSubtitleCount"> · 本地 {{ localSubtitleCount }} 个</span><span v-if="assrtSubtitleCount"> · 射手网 {{ assrtSubtitleCount }} 个</span></p>
      </section>
      <p v-if="externalIssue" role="alert" class="error">{{ externalIssue }}</p>
      <div ref="numberingAnchor" aria-hidden="true" class="numbering-anchor pointer-events-none h-px !-mb-px"></div>
      <div v-if="editing.length" ref="numberingBar" class="numbering-bar" :class="{ 'is-stuck': numberingStuck }">
        <button v-if="kind === 'tv'" ref="batchToggleButton" type="button" class="batch-toggle" aria-haspopup="dialog" :aria-controls="batchOpen ? mappingPanelID : undefined" :aria-expanded="batchOpen" :disabled="editDisabled" @click="toggleBatch">调整季集 <IconChevronDown :size="15" aria-hidden="true" class="transition-transform" :class="{ 'rotate-180': batchOpen }" /></button>
        <span class="editing-count">已勾选 {{ editing.length }} 项</span>
        <button v-if="undo.length" type="button" class="secondary tool-action" :disabled="editDisabled" @click="undoMapping"><IconArrowBackUp :size="15" aria-hidden="true" />撤销</button>
        <button type="button" class="secondary stage-action" :disabled="editDisabled || parsing || !namingPreview.ready.value" title="可选：暂存当前映射后，继续处理其他季；无需暂存也可直接提交" @click="stageFiles">暂存这 {{ editing.length }} 项</button>
      </div>
      <section v-if="kind === 'tv' && editing.length && batchOpen" :id="mappingPanelID" ref="mappingPanel" popover="manual" role="dialog" aria-label="调整季集" tabindex="-1" class="mapping-popover floating-surface" :style="mappingStyle">
        <div class="mapping-bar">
          <label class="mapping-season">目标季 <SelectMenu v-if="seasonOptions.length" v-model="season" :options="seasonOptions" label="目标季" variant="anime" density="compact" :menu-min-width="300" :disabled="editDisabled" class="season-select"><template #trigger><span class="truncate">{{ selectedSeason?.number === 0 ? '特别篇' : `第 ${season} 季` }}<template v-if="selectedSeason"> · {{ selectedSeason.episodeCount }} 集</template></span></template></SelectMenu><input v-else v-model="season" type="number" min="0" max="1000" aria-label="目标季" :disabled="editDisabled" /></label>
          <div class="mapping-range">
            <label>发布集 <input v-model.number="sourceStart" type="number" min="1" max="10000" aria-label="发布起始集" :disabled="editDisabled" /></label><span>对应</span>
            <label>目标集 <input v-model.number="targetStart" type="number" min="1" max="10000" aria-label="目标起始集" :disabled="editDisabled" /></label>
          </div>
          <div class="mapping-actions">
            <button type="button" class="secondary mapping-apply" :aria-label="`应用偏移（${editing.length} 项）`" :disabled="editDisabled || !editing.length" @click="mapFiles()"><span>应用偏移<span class="hidden sm:inline">（{{ editing.length }} 项）</span></span></button>
            <button type="button" class="secondary" title="从目标集开始，按所选顺序连续编号；连集文件保留跨度" :disabled="editDisabled || !editing.length" @click="mapFiles(true)">连续编号</button>
          </div>
        </div>
        <p v-if="mappingError" role="alert" class="mapping-error">{{ mappingError }}</p>
      </section>
      <p v-if="notice" role="status" class="notice">{{ notice }}</p>
      <p v-if="Object.keys(localConflicts).length" role="alert" class="error">有 {{ Object.keys(localConflicts).length }} 个文件目标重名，请核对季集编号、命名模板或取消重复文件。</p>
      <OrganizeFileSelection ref="fileSelection" :rows="pendingRows" root-path="/" :flat="batch" :tv="kind === 'tv'" :episode-editable="true" :disabled="editDisabled" :target-names="targetNames" :conflicts="rowProblems" :short-conflicts="shortConflicts" :empty-text="staged.length ? '文件已全部暂存，可继续核对整理清单。' : undefined" compact>
        <template v-if="!editing.length && pendingRows.length" #header>
          <p class="selection-prompt"><span>{{ kind === 'tv' ? '勾选文件，可批量调整季集或暂存。' : '勾选文件，可暂存或直接提交整理。' }}</span><button v-if="undo.length" type="button" class="secondary tool-action" :disabled="editDisabled" @click="undoMapping"><IconArrowBackUp :size="15" aria-hidden="true" />撤销编号</button></p>
        </template>
        <template #row-details="{ row }">
          <details v-if="allExternal.some(sub => sub.videoId === row.id)" class="subtitle-details" @click.stop @mousedown.stop><summary><IconChevronDown :size="13" aria-hidden="true" />外部字幕（{{ allExternal.filter(sub => sub.videoId === row.id).length }}）<span v-if="!row.selected">未选择</span></summary><div v-for="sub in allExternal.filter(sub => sub.videoId === row.id)" :key="'localId' in sub ? sub.localId : sub.path" class="subtitle-file"><p>{{ sub.file.name }}</p><p class="subtitle-target">{{ row.selected ? `整理后：${externalName(sub)}` : '随视频选择，当前不上传' }}</p></div></details>
          <details v-if="subtitlesByVideo[row.id]?.length" class="subtitle-details" :open="row.selected && subtitlesByVideo[row.id]!.some(sub => displayedConflicts[sub.file.id])" @click.stop @mousedown.stop>
            <summary><IconChevronDown :size="13" aria-hidden="true" />关联字幕（{{ subtitlesByVideo[row.id]!.length }}）<span v-if="!row.selected">未选择</span></summary>
            <div v-for="sub in subtitlesByVideo[row.id]" :key="sub.file.id" class="subtitle-file">
              <p>{{ sub.file.name }}</p>
              <p v-if="row.selected && !namingPreview.errors.value[sub.file.id]" class="subtitle-target">整理后：{{ subtitleName(sub) }}</p>
              <p v-else>随视频选择，未选字幕整理成功后清理</p>
              <p v-if="row.selected && displayedConflicts[sub.file.id]" role="alert" class="error">{{ displayedConflicts[sub.file.id] }}</p>
            </div>
          </details>
        </template>
      </OrganizeFileSelection>
      </section>
      <details v-if="unmatchedSubtitles.length" class="subtitle-details">
        <summary><IconChevronDown :size="13" aria-hidden="true" />{{ unmatchedSubtitles.length }} 个字幕未能唯一匹配，整理成功后清理</summary>
        <p v-for="sub in unmatchedSubtitles" :key="sub.file.id" class="subtitle-file">{{ sub.file.sourcePath }}</p>
      </details>
      <details class="organize-help">
        <summary><IconChevronDown :size="13" aria-hidden="true" />整理说明与参考</summary>
        <div>
          <p class="scope-note">整条磁力下载，整理已暂存和当前勾选的视频及其关联字幕；全部整理成功后，临时目录连同未选、排除及其他残留文件一并回收。暂存只保留本次映射，最终提交后才执行网盘操作。</p>
          <p class="scope-note">“115 分享 STRM”使用主网盘根目录下独立的 Cinema115-任务编号 文件夹，无需选择普通整理目录。完整进度与重试可在下载整理的任务记录中查看，分享设置沿用 115 分享 STRM 页面。</p>
          <p v-if="titleOnlyCount" class="scope-note">{{ titleOnlyCount }} 项单集按标题预览命名，扩展名与关联字幕下载后确认；合集及不明确的资源读取文件清单。</p>
          <div class="reference"><span>TMDB 季集参考</span><span v-for="s in seasons" :key="s.number">{{ s.number === 0 ? '特别篇' : `第 ${s.number} 季` }} {{ s.episodeCount }} 集</span><span v-if="kind === 'movie'">电影</span></div>
          <p v-if="downloadPath" class="scope-note">临时下载：{{ downloadPath.replace(/\/$/, '') }}/garden-任务编号</p>
          <p class="scope-note">批量编号仅影响待处理区中勾选的文件。暂存后可继续处理其他季，或退回编辑并保留已有映射；无需暂存也可直接提交。</p>
        </div>
      </details>
    <p v-if="error" ref="errorElement" role="alert" class="error">{{ error }} <button v-if="!accountId && !loading" class="secondary" @click="loadConfig">重新读取配置</button><RouterLink v-if="!accountId" to="/115/cloud-download">前往云下载设置</RouterLink></p>
    <p v-if="loading" role="status" class="notice">读取目录配置…</p>
  </section>
    <template #footer>
      <div class="submit-bar">
        <div class="submit-summary"><strong>待整理 {{ selected.length }} 个视频</strong><span v-if="staged.length" class="submit-composition">已暂存 {{ staged.length }} · 当前勾选 {{ editing.length }}</span><span v-if="selectedSubtitleCount">关联 {{ selectedSubtitleCount }} 个字幕</span><span v-if="selectedExternal.length">外部字幕 {{ selectedExternal.length }} 个</span><button v-if="Object.keys(rowProblems).length" type="button" class="locate-problem" @click="locateProblem">{{ Object.keys(rowProblems).length }} 项待处理 · 定位问题</button></div>
        <div class="submit-actions">
          <button type="button" class="secondary share-action" :title="shareIssue || '下载、整理后生成分享 STRM'" :disabled="submitDisabled || !!shareIssue && !confirming || confirming && !shareAction" @click="start(true)"><IconShare2 :size="16" aria-hidden="true" />{{ shareAction && busy ? '正在创建分享任务…' : shareAction && confirming ? '确认任务状态' : shareAction && planKey === snapshotKey ? '确认分享 STRM' : '115 分享 STRM' }}</button>
          <button type="button" class="primary" :disabled="submitDisabled || !targetCID || confirming && shareAction" @click="start(false)">{{ !shareAction && busy ? '正在确认并创建任务…' : !shareAction && confirming ? '确认任务状态' : !shareAction && planKey === snapshotKey ? '确认下载并整理' : '下载并自动整理' }}</button>
        </div>
      </div>
      <p v-if="!targetCID && !loading" class="share-config-note">普通整理需选择目录；115 分享 STRM 使用主网盘根目录的独立文件夹。</p>
      <p v-if="shareIssue" class="share-config-note">{{ shareIssue }} · <RouterLink to="/115/share" target="_blank" rel="noopener">分享设置</RouterLink><button v-if="!loading" type="button" :disabled="editDisabled" @click="loadConfig">重新读取配置</button></p>
    </template>
  </FloatingDialog>
  <P115DirectoryDialog v-if="props.active && directoryOpen" :token="token" :account-id="accountId" title="选择本次整理目录" description="只用于本次任务，不修改全局整理配置" @close="directoryOpen = false" @select="selectDirectory" />
  <SubtitlePickerDialog v-if="props.active && subtitleOpen" :token="token" :kind="kind" :animation-library="animationLibrary" :title="title" :original-title="originalTitle" :videos="subtitleVideos" :library="scopedLibrary" :local="scopedLocal" :version="subtitleVersion" :target-names="Object.fromEntries(subtitleVideos.filter(row => results[row.id]).map(row => [row.id, `${results[row.id]!.stem}.mkv`]))" @close="subtitleOpen = false" @select="selectSubtitles" />
</template>

<style scoped>
/* 外壳与搜索窗口共用玻璃材质，内部只在容器上铺底，避免逐行叠加成实色面板。 */
:global(.download-organize-dialog) {
  --organize-card-bg: rgb(255 255 255 / 54%);
  --organize-sub-bg: rgb(241 245 249 / 60%);
  --organize-row-bg: transparent;
  --organize-row-hover: rgb(241 245 249 / 68%);
  --organize-input-bg: rgb(255 255 255 / 80%);
  --organize-border: rgb(148 163 184 / 24%);
  --organize-control-border: rgb(100 116 139 / 34%);
  --organize-footer-bg: rgb(241 245 249 / 58%);
}
:global(.dark .download-organize-dialog) {
  --organize-card-bg: rgb(255 255 255 / 5%);
  --organize-sub-bg: rgb(255 255 255 / 3.5%);
  --organize-row-hover: rgb(255 255 255 / 6%);
  --organize-input-bg: rgb(10 15 24 / 45%);
  --organize-border: rgb(255 255 255 / 10%);
  --organize-control-border: rgb(255 255 255 / 16%);
  --organize-footer-bg: rgb(10 15 24 / 38%);
}
:global(.download-organize-dialog .floating-dialog-footer) { background: var(--organize-footer-bg); border-top-color: var(--organize-border); }
:global(.download-organize-dialog .floating-dialog-body) { padding-top: 0; scroll-padding-top: 60px; }
.download-organize { @apply min-w-0 space-y-3 pt-1.5 text-content-primary; }
.download-organize :is(button, summary, a, input):focus-visible, .submit-bar button:focus-visible, .share-config-note :is(a, button):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 3px; }
.resource-title { @apply truncate text-xs leading-5 text-content-secondary; }
.organize-config { @apply flex min-w-0 flex-col gap-1 rounded-xl border border-line-default bg-surface-card px-3.5 py-2; }
.organize-config, .confirmation-zone { background: var(--organize-card-bg); border-color: var(--organize-border); }
.directory-bar { @apply flex min-w-0 items-center justify-between gap-2; }
.directory-info { @apply min-w-0 flex-1 text-xs; }
.directory-info p:first-child { @apply flex min-w-0 items-center gap-x-1.5; }
.directory-label { @apply shrink-0 text-content-secondary; }
.directory-info strong { @apply truncate font-medium text-content-primary; }
.directory-info .scope-note { @apply truncate leading-5; }
.directory-actions { @apply flex shrink-0 items-center gap-1.5; }
.naming-details { @apply text-xs text-content-secondary; padding-left: 23px; }
.naming-details summary { @apply inline-flex cursor-pointer list-none items-center gap-1.5 rounded-md py-1 text-content-secondary transition-colors hover:text-action-link; min-height: 28px; }
.naming-details summary::-webkit-details-marker { display: none; }
.naming-details[open] > summary > svg { transform: rotate(180deg); }
.naming-details.has-issue summary, .naming-state.is-error { @apply text-status-danger; }
.naming-state { @apply text-content-secondary; }
.naming-panel { @apply space-y-1 border-t border-line-default pt-2 pb-1; }
.naming-summary { @apply flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-content-secondary; }
.naming-summary a { @apply font-medium text-action-link hover:underline; }
.organize-workbench { @apply min-w-0 space-y-2; }
.workbench-heading { @apply flex flex-wrap items-center justify-between gap-3 pt-1 pb-1; }
.workbench-title { @apply min-w-0; }
.workbench-title h3 { @apply text-sm font-semibold text-content-primary; }
.workbench-counts { @apply mt-1 text-xs leading-5 text-content-secondary; }
.workbench-counts > span { display: inline-block; white-space: nowrap; }
.workbench-actions { @apply ml-auto flex shrink-0 items-center; }
.action-count { @apply rounded-md bg-selection-surface px-1.5 text-xs text-selection-content; font-variant-numeric: tabular-nums; }
.confirmation-zone { @apply rounded-xl border px-3 py-2; box-shadow: inset 3px 0 rgb(var(--action-primary) / .55); }
.confirmation-heading { @apply flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs leading-5; }
.confirmation-heading strong { @apply font-medium text-action-link; }
.confirmation-heading > span, .confirmation-subtitles { @apply text-xs leading-5 text-content-secondary; }
.confirmed-group { @apply min-w-0 border-b border-line-default py-1 last:border-b-0; }
.confirmed-group details { @apply block w-full min-w-0; }
.confirmed-group summary { @apply flex w-full cursor-pointer list-none flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-md text-xs text-content-secondary; min-height: var(--dialog-control-height); }
.confirmed-group summary::-webkit-details-marker { display: none; }
.confirmed-summary-main { @apply flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5; flex: 1 1 12rem; }
.confirmed-summary-main strong { @apply font-medium text-content-primary; }
.confirmed-chevron { @apply shrink-0 transition-transform; }
.confirmed-group details[open] .confirmed-chevron { transform: rotate(180deg); }
.confirmed-range { @apply max-w-52 truncate; }
.confirmed-actions { @apply ml-auto flex shrink-0 items-center gap-1; }
.confirmed-group ul { @apply mt-1.5 w-full space-y-3 border-t border-line-default pt-2; }
.confirmed-group li { @apply min-w-0 text-xs leading-5; overflow-wrap: anywhere; }
.confirmed-target { @apply font-medium text-content-primary; }
.confirmed-source { @apply text-content-secondary; }
.reference { @apply flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-content-secondary; }
.scope-note { @apply text-xs leading-5 text-content-secondary; }
.locate-problem { @apply rounded px-1 py-1 text-xs font-medium text-status-danger underline underline-offset-2 hover:bg-status-danger-surface; }
.organize-help { @apply text-xs text-content-secondary; }
.organize-help summary { @apply flex w-fit cursor-pointer list-none items-center gap-1 rounded-md py-1; min-height: 32px; }
.organize-help summary::-webkit-details-marker { display: none; }
.organize-help[open] > summary > svg { transform: rotate(180deg); }
.organize-help > div { @apply space-y-1 pt-1; }
.resource-progress { @apply flex items-center justify-between gap-2 text-xs; }
.resource-status { @apply text-xs; }
.resource-status summary { @apply cursor-pointer py-1; }
.resource-status-list { @apply max-h-40 overflow-auto overscroll-contain; }
.resource-status-list > div { @apply flex items-center gap-2 py-2; }
.resource-status-list p { @apply min-w-0 flex-1 break-all; }
.resource-status-list small { @apply block text-content-secondary; }
.resource-error { @apply flex flex-wrap items-center gap-2 text-xs; }
.resource-error p { @apply w-full break-all text-status-danger; }
.resource-error > span { @apply text-content-secondary; }
.subtitle-details { @apply mt-1 min-w-0 text-xs leading-5 text-content-secondary; }
.subtitle-details summary { @apply flex cursor-pointer list-none items-center gap-1 rounded py-1; }
.subtitle-details summary::-webkit-details-marker { display: none; }
.subtitle-details[open] > summary > svg { transform: rotate(180deg); }
.subtitle-file { @apply mb-2 min-w-0 break-all pl-2; }
.subtitle-target { @apply text-content-primary; }
.organize-workbench > .numbering-anchor, .organize-workbench > .numbering-bar { margin-top: 0; }
.numbering-bar { @apply sticky top-0 z-10 flex items-center gap-2 py-2; margin-inline: calc(-1 * var(--dialog-content-inset)); padding-inline: var(--dialog-content-inset); background: transparent; }
.numbering-bar.is-stuck { background: var(--floating-surface-container); box-shadow: 0 1px 0 rgb(var(--line-default)); }
.editing-count { @apply text-xs text-content-secondary; font-variant-numeric: tabular-nums; }
.batch-toggle { @apply inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg border border-line-default bg-surface-card px-3 text-sm font-medium text-content-primary transition-colors enabled:hover:bg-surface-hover disabled:opacity-40; min-height: var(--dialog-control-height); }
.batch-toggle[aria-expanded='true'] { @apply border-action-primary/40 bg-selection-surface text-selection-content; }
.mapping-popover[popover] { position: fixed; inset: auto; z-index: 20; margin: 0; overflow-y: auto; overscroll-behavior: contain; border-radius: 14px; padding: 12px; color: var(--floating-surface-text); outline: none; }
.mapping-popover:popover-open { display: flex; flex-direction: column; gap: 8px; }
.mapping-bar { @apply flex flex-wrap items-center gap-x-3 gap-y-2 text-xs; }
.mapping-error { @apply text-xs leading-5 text-status-danger; }
.mapping-bar label { @apply flex items-center gap-1.5 whitespace-nowrap text-content-secondary; }
.mapping-range { @apply flex items-center gap-2; }
.mapping-range > span { @apply text-content-secondary; }
.mapping-actions { @apply ml-auto flex flex-wrap items-center gap-1.5; }
.mapping-bar input { @apply w-16 min-w-0 rounded-lg border text-content-primary outline-none focus:border-focus disabled:bg-surface-hover disabled:text-content-secondary; background: var(--organize-input-bg); border-color: var(--organize-control-border); height: var(--dialog-control-height); padding: var(--dialog-control-padding); font-size: var(--dialog-control-font); font-variant-numeric: tabular-nums; }
.season-select { @apply w-40; }
.stage-action { @apply ml-auto border-action-primary/40 text-action-link; }
.selection-prompt { @apply flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs leading-5 text-content-secondary; }
.secondary { @apply inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg border font-medium text-content-primary transition-colors enabled:hover:border-line-control enabled:hover:bg-surface-hover disabled:opacity-40; background: var(--organize-card-bg); border-color: var(--organize-control-border); min-height: var(--dialog-control-height); padding: var(--dialog-control-padding); font-size: var(--dialog-control-font); }
.secondary.mapping-apply { @apply border-action-primary/40 bg-selection-surface text-selection-content enabled:hover:border-action-link; }
.secondary.tool-action { @apply border-transparent bg-transparent text-content-secondary enabled:hover:border-transparent enabled:hover:bg-surface-hover enabled:hover:text-action-link; }
.primary { @apply rounded-lg bg-action-primary px-4 py-2.5 text-sm font-medium text-action-on-primary enabled:hover:bg-action-primary-hover disabled:opacity-40; }
.submit-bar { @apply flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-xs; }
.submit-summary { @apply flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1 text-content-secondary; }
.submit-summary strong { @apply font-medium text-content-primary; }
.submit-composition { @apply w-full; }
.submit-actions { @apply flex flex-wrap items-center gap-2; }
.submit-actions > button { min-height: 44px; }
.secondary.share-action { @apply px-3 py-2.5 text-sm; }
.share-config-note { @apply mt-2 text-xs leading-5 text-content-secondary; }
.share-config-note a, .share-config-note button { @apply rounded text-action-link; }
.share-config-note button { @apply ml-2 disabled:opacity-40; }
.notice { @apply text-xs leading-6 text-content-secondary; }
.error { @apply break-all text-sm leading-6 text-status-danger; }
.error a { @apply text-sm text-action-link underline; }
/* 减少透明效果、高对比及不支持背景模糊时，恢复完整的实色阅读表面。 */
@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  :global(.download-organize-dialog) {
    --organize-card-bg: rgb(var(--surface-card));
    --organize-sub-bg: rgb(var(--surface-page));
    --organize-row-hover: rgb(var(--surface-hover));
    --organize-input-bg: rgb(var(--surface-card));
    --organize-border: rgb(var(--line-default));
    --organize-control-border: rgb(var(--line-control));
    --organize-footer-bg: var(--floating-surface-container);
  }
}
@media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
  :global(.download-organize-dialog) {
    --organize-card-bg: rgb(var(--surface-card));
    --organize-sub-bg: rgb(var(--surface-page));
    --organize-row-hover: rgb(var(--surface-hover));
    --organize-input-bg: rgb(var(--surface-card));
    --organize-border: rgb(var(--line-control));
    --organize-control-border: rgb(var(--line-control));
    --organize-footer-bg: var(--floating-surface-container);
  }
}
@media (max-width: 639px), (pointer: coarse) {
  .mapping-bar input { font-size: var(--sheet-font-input); }
  .mapping-bar .season-select :deep(.select-menu-control.select-menu-compact) { min-height: var(--sheet-touch-size); height: var(--sheet-touch-size); font-size: var(--sheet-font-body); }
  .naming-details summary, .organize-help summary, .subtitle-details summary { min-height: 44px; }
}
@media (max-width: 639px) {
  :global(.download-organize-dialog .floating-dialog-body) { scroll-padding-top: 68px; }
  .resource-title { white-space: normal; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow-wrap: anywhere; }
  .organize-config { padding: 8px 10px; border: 0; background: var(--organize-sub-bg); }
  .directory-bar { gap: 6px; }
  .directory-info p:first-child { @apply flex-col items-start gap-0; }
  .directory-info strong { max-width: 100%; font-size: var(--sheet-font-body); line-height: var(--sheet-line-body); }
  .directory-pick { padding-inline: 8px; border: 0; background: transparent; color: rgb(var(--action-link)); }
  .naming-details { padding-left: 23px; }
  .workbench-heading { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 8px; }
  .workbench-title { display: contents; }
  .workbench-counts { grid-column: 1 / -1; grid-row: 2; margin-top: 0; }
  .workbench-actions { grid-column: 2; grid-row: 1; margin-left: 0; }
  .subtitle-action { font-size: var(--sheet-font-body); }
  .confirmed-summary-main { flex-basis: 100%; }
  .confirmed-actions { margin-left: 18px; }
  :global(.download-organize-dialog .floating-dialog-footer) { padding-block: 8px 10px; }
  .download-organize { @apply space-y-2; }
  .numbering-bar { gap: 6px; padding-block: 6px; }
  .editing-count { display: none; }
  .mapping-popover[popover] { padding: 10px; }
  .mapping-bar { align-items: stretch; gap: 10px; }
  .mapping-season { width: 100%; }
  .mapping-bar .season-select, .mapping-season input { width: auto; min-width: 0; flex: 1; }
  .mapping-range { width: 100%; gap: 6px; }
  .mapping-range label { min-width: 0; flex: 1; }
  .mapping-range input { width: 0; flex: 1; padding-inline: 6px; text-align: center; }
  .mapping-range > span { flex-shrink: 0; }
  .mapping-actions { width: 100%; margin-left: 0; }
  .mapping-actions > button { flex: 1 1 max-content; min-width: 0; }
  .submit-bar { gap: 6px 12px; }
  .share-config-note { margin-top: 4px; }
  .submit-summary { flex-basis: 100%; }
  .submit-composition { width: auto; }
  .submit-actions { width: 100%; }
  .submit-actions > button { flex: 1 1 auto; min-width: 0; padding-inline: 8px; font-size: var(--sheet-font-body); }
  .submit-actions > .primary { flex-grow: 1.4; }
}
@media (max-width: 359px) {
  .mapping-range { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .mapping-range > span { display: none; }
}
</style>
