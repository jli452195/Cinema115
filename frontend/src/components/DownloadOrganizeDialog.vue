<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { IconFolder, IconArrowBackUp, IconChevronDown } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import OrganizeFileSelection from './OrganizeFileSelection.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import SelectMenu from './SelectMenu.vue'
import { p115Request } from '../lib/p115-api'
import type { DownloadBatchResult, DownloadOrganizeResource, OrganizeJob, OrganizeSubtitle } from '../types/manualOrganize'
import { useDownloadOrganizeFiles } from '../composables/useDownloadOrganizeFiles'
import { applyOrganizeMapping } from '../utils/organizeNaming'
import { useMediaNamingPreview } from '../composables/useMediaNamingPreview'
import { useStickySurface } from '../composables/useStickySurface'
import { directoryFileRows, directoryGroups } from '../utils/organizeSelection'
import type { NamingResult } from '../types/naming'

const props = defineProps<{
  token: string; entries: DownloadOrganizeResource[]; hiddenCount?: number; batch?: boolean
  tmdbId: number; kind: 'movie' | 'tv'; title: string; date: string
  seasons: { number: number; name: string; episodeCount: number }[]
  themeColors?: Record<string, string>
}>()
const emit = defineEmits<{ close: []; submitted: [keys: string[]]; uncertain: [keys: string[]] }>()
const lifecycle = new AbortController()
const preparation = useDownloadOrganizeFiles(props.token, props.entries, !!props.batch, lifecycle.signal)
const { rows, subtitles, sources, parsing, active, unresolved, readyCount, states, selectedKeys } = preparation
const busy = ref(false), loading = ref(true), error = ref(''), notice = ref('')
const confirming = ref(false)
const errorElement = ref<HTMLElement>()
watch(error, async value => { if (value) { await nextTick(); errorElement.value?.scrollIntoView({ block: 'nearest' }) } })
const batchOpen = ref(window.matchMedia('(min-width: 640px)').matches)
const floatingDialog = ref<InstanceType<typeof AnimeFloatingDialog>>()
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
const directoryOpen = ref(false), accountId = ref(''), targetCID = ref(''), targetPath = ref(''), downloadPath = ref('')
const season = ref(String(props.seasons.find(s => s.number > 0)?.number ?? 1)), sourceStart = ref(1), targetStart = ref(1)
const selected = computed(() => rows.value.filter(row => row.selected))
const titleOnlyCount = computed(() => rows.value.filter(row => row.titleOnly).length)
const seasonOptions = computed(() => props.seasons.map(s => {
  const label = s.number ? `第 ${s.number} 季` : '特别篇'
  return { value: String(s.number), label: [label, s.name && s.name !== label ? s.name : '', `${s.episodeCount} 集`].filter(Boolean).join(' · ') }
}))
const selectedSeason = computed(() => props.seasons.find(s => String(s.number) === season.value))
const job = ref<OrganizeJob>()
const fileSelection = ref<InstanceType<typeof OrganizeFileSelection>>()
const lastProblem = ref('')
const namingNumber = (value: unknown) => Number.isInteger(Number(value)) ? Number(value) : -1
const namingPreview = useMediaNamingPreview(props.token, () => rows.value.length && !parsing.value && !job.value ? {
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
const localConflicts = computed(() => {
  const targets = new Map<string, string[]>(), out: Record<string, string> = {}
  for (const row of selected.value) {
    const names = [{ id: row.id, result: results.value[row.id] }, ...(subtitlesByVideo.value[row.id] || []).map(sub => ({ id: sub.file.id, result: results.value[sub.file.id] }))]
    for (const file of names) {
      if (!file.result) continue
      const key = `${file.result.directory}/${file.id === row.id ? file.result.stem : file.result.name}`.toLowerCase()
      const ids = targets.get(key) || []; ids.push(file.id); targets.set(key, ids)
    }
  }
  for (const ids of targets.values()) if (ids.length > 1) for (const id of ids) out[id] = '本次多个文件目标同名，请核对季集编号、命名模板或取消重复文件'
  return out
})
const displayedConflicts = computed(() => {
  const selectedIDs = selected.value.flatMap(row => [row.id, ...(subtitlesByVideo.value[row.id] || []).map(sub => sub.file.id)])
  return { ...conflicts.value, ...localConflicts.value, ...Object.fromEntries(selectedIDs.filter(id => namingPreview.errors.value[id]).map(id => [id, namingPreview.errors.value[id]!])) }
})
const rowProblems = computed(() => Object.fromEntries(selected.value.flatMap(row => {
  const issue = displayedConflicts.value[row.id] || ((subtitlesByVideo.value[row.id] || []).some(sub => displayedConflicts.value[sub.file.id]) ? '关联字幕存在命名问题，请展开查看' : '')
  return issue ? [[row.id, issue]] : []
})))
async function locateProblem() {
  const ids = Object.keys(rowProblems.value)
  const id = ids[(ids.indexOf(lastProblem.value) + 1) % ids.length]
  if (id) { lastProblem.value = id; await fileSelection.value?.locateIssue(id) }
}
const previewId = ref('')
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(130000)])
const snapshotKey = computed(() => JSON.stringify({ naming: namingPreview.value.value, targetCID: targetCID.value, targetPath: targetPath.value, resources: preparation.resourceSelections() }))
watch(snapshotKey, () => { conflicts.value = {} }, { flush: 'sync' })
let previewKey = ''

function mapFiles(sequential = false) {
  error.value = ''; notice.value = ''; conflicts.value = {}
  try {
    undo.value = applyOrganizeMapping(props.batch ? rows.value : directoryFileRows(directoryGroups(rows.value, '/')[0]!), sources, Number(season.value), sourceStart.value, targetStart.value, sequential)
    const skipped = selected.value.length - undo.value.length
    notice.value = `已更新 ${undo.value.length} 项${skipped ? `，跳过 ${skipped} 项无发布编号的文件，可在行内填写目标季集` : !sequential ? '，保留发布编号的缺号和连集跨度' : ''}`
  } catch (e) { error.value = e instanceof Error ? e.message : '编号设置无效' }
}
function undoMapping() {
  for (const before of undo.value) { const row = rows.value.find(row => row.id === before.id); if (row) Object.assign(row, before) }
  undo.value = []; notice.value = '已撤销上次批量编号'; conflicts.value = {}
}
async function loadConfig() {
  loading.value = true; error.value = ''
  try {
    const [cloud, organize] = await Promise.all([
      p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', { signal: signal() }),
      p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'download-organize/config', { signal: signal() }),
    ])
    if (!cloud.accountId || !cloud.targetCID) throw new Error('请先在 115 云下载中配置主账号的下载目录')
    accountId.value = cloud.accountId; downloadPath.value = cloud.targetPath
    if (organize?.accountId === cloud.accountId) { targetCID.value = organize.targetCID; targetPath.value = organize.targetPath }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取目录配置失败' }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
async function start() {
  if (busy.value || loading.value || !confirming.value && !namingPreview.ready.value || parsing.value || unresolved.value.length || !selected.value.length || invalid.value || Object.keys(localConflicts.value).length || !targetCID.value || !accountId.value || job.value) return
  busy.value = true; error.value = ''; notice.value = ''
  let creationAttempt = false
  try {
    const key = snapshotKey.value
    if (!previewId.value || previewKey !== key) {
      const payload = { namingRevision: namingPreview.value.value?.naming.revision, tmdbId: props.tmdbId, kind: props.kind, accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }
      const resources = preparation.resourceSelections()
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
          official[id] = { directory: item.directory, stem: item.stem, name: item.name }
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
    // Reuse the ID after a lost response; the server persists before submitting.
    creationAttempt = true
    if (props.batch) {
      const created = await p115Request<DownloadBatchResult>(props.token, 'download-organize/batch/jobs', { method: 'POST', signal: signal(), body: JSON.stringify({ previewId: previewId.value }) })
      job.value = created.jobs[0]
    } else job.value = await p115Request<OrganizeJob>(props.token, 'download-organize/jobs', { method: 'POST', signal: signal(), body: JSON.stringify({ previewId: previewId.value }) })
    confirming.value = false
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
onMounted(() => { void loadConfig(); void preparation.parse() })
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <AnimeFloatingDialog ref="floatingDialog" :title="job ? '任务已创建' : '下载并整理'" close-label="关闭下载并整理" size="organize" :close-disabled="busy" :style="themeColors" @close="emit('close')">
  <section class="download-organize" aria-label="下载并整理映射" :aria-busy="busy || loading" :style="{ '--organize-sticky-offset': `${numberingHeight}px` }">
    <p class="resource-title">{{ batch ? `批量整理 ${active.length} 项资源${hiddenCount ? `，包含外层筛选隐藏的 ${hiddenCount} 项` : ''}` : entries[0]?.resource.title }}</p>
    <template v-if="job">
      <p role="status" class="notice">后台将下载{{ batch ? `所选 ${selectedKeys.length} 条磁力` : '整条磁力' }}，并按本次映射整理 {{ selected.length }} 个视频<span v-if="selectedSubtitleCount">及 {{ selectedSubtitleCount }} 个字幕</span>。<span v-if="selected.some(row => row.titleOnly)">按标题识别的单集将自动匹配实际视频与同名字幕。</span>关闭弹窗后继续执行。</p>
      <RouterLink to="/115/download-organize" class="task-link">查看下载整理任务</RouterLink>
    </template>
    <template v-else>
      <div class="directory-bar"><IconFolder :size="17" class="shrink-0" aria-hidden="true" /><div><p><span>整理到</span><strong :title="targetPath">{{ targetPath || '请选择整理目录' }}</strong></p><small v-if="sharedDirectory">公共子目录：{{ sharedDirectory }}</small></div><button type="button" class="secondary" :disabled="editDisabled || !accountId" @click="directoryOpen = true">选择目录</button></div>
      <div class="naming-summary"><strong>{{ title }}</strong><RouterLink :to="{ path: '/settings/naming', query: kind === 'tv' ? { kind: 'tv' } : {} }" target="_blank" rel="noopener">公共命名模板</RouterLink><span role="status">{{ namingPreview.pending.value ? '正在更新命名…' : namingPreview.error.value || (namingPreview.value.value ? `版本 ${namingPreview.value.value.naming.revision}` : '等待命名预览') }}</span><button type="button" class="secondary" :disabled="editDisabled || parsing || namingPreview.pending.value" @click="namingPreview.refresh">刷新命名</button></div>
      <details class="organize-help">
        <summary><IconChevronDown :size="13" aria-hidden="true" />整理说明与参考</summary>
        <div>
          <p class="scope-note">整条磁力下载，整理所选视频及已关联的字幕；其余文件保留在下载目录。</p>
          <p v-if="titleOnlyCount" class="scope-note">{{ titleOnlyCount }} 项单集按标题预览命名，扩展名与关联字幕下载后确认；合集及不明确的资源读取文件清单。</p>
          <div class="reference"><span>TMDB 季集参考</span><span v-for="s in seasons" :key="s.number">{{ s.number === 0 ? '特别篇' : `第 ${s.number} 季` }} {{ s.episodeCount }} 集</span><span v-if="kind === 'movie'">电影</span></div>
          <p v-if="downloadPath" class="scope-note">临时下载：{{ downloadPath.replace(/\/$/, '') }}/garden-任务编号</p>
          <p class="scope-note">请先选中需要整理的文件并核对季集；批量编号仅影响当前所选文件，其他修改会保留。</p>
        </div>
      </details>
      <template v-if="batch">
        <div class="resource-progress"><span role="status">已解析 {{ readyCount }} / {{ active.length }} 项资源<span v-if="unresolved.length && !parsing">，{{ unresolved.length }} 项待处理</span></span><button v-if="parsing" class="secondary" @click="preparation.pause">停止解析</button><button v-else-if="states.some(state => state.status === 'waiting')" class="secondary" :disabled="editDisabled" @click="preparation.parse">继续解析</button></div>
        <details class="resource-status"><summary>查看全部已选资源</summary><div class="resource-status-list"><div v-for="state in states.filter(state => state.status !== 'removed')" :key="state.key"><p>{{ state.resource.title }}<small>{{ state.status === 'ready' ? state.files?.files.some(file => file.titleOnly) ? '按标题识别' : '已读取文件清单' : state.status === 'loading' ? '识别中' : state.status === 'error' ? state.error : '待识别' }}</small></p><button class="secondary" :disabled="editDisabled" :aria-label="`移除资源 ${state.resource.title}`" @click="preparation.remove(state.key)">移除</button></div></div></details>
        <div v-for="state in states.filter(state => state.status === 'error')" :key="state.key" class="resource-error"><p role="alert">{{ state.resource.title }}：{{ state.error }}</p><button class="secondary" :disabled="editDisabled || parsing" @click="preparation.retry(state.key)">重试解析</button><button class="secondary" :disabled="editDisabled" @click="preparation.remove(state.key)">移除本次整理</button><span>可关闭弹窗后普通离线，失败项不会自动提交。</span></div>
      </template>
      <div v-if="kind === 'tv' || notice || Object.keys(localConflicts).length" ref="numberingAnchor" aria-hidden="true" class="pointer-events-none h-px !-mb-px"></div>
      <div v-if="kind === 'tv' || notice || Object.keys(localConflicts).length" ref="numberingBar" class="numbering-bar !mt-0" :class="{ 'is-stuck': numberingStuck }">
      <div v-if="kind === 'tv'" class="batch-editor"><button type="button" class="batch-toggle" :aria-expanded="batchOpen" @click="batchOpen = !batchOpen">批量编号 <IconChevronDown :size="14" aria-hidden="true" :class="{ 'rotate-180': batchOpen }" /></button><div v-show="batchOpen" class="mapping-bar">
        <label>目标季 <SelectMenu v-if="seasonOptions.length" v-model="season" :options="seasonOptions" label="目标季" variant="anime" :menu-min-width="300" :disabled="editDisabled" class="season-select"><template #trigger><span class="truncate">{{ selectedSeason?.number === 0 ? '特别篇' : `第 ${season} 季` }}<template v-if="selectedSeason"> · {{ selectedSeason.episodeCount }} 集</template></span></template></SelectMenu><input v-else v-model="season" type="number" min="0" max="1000" aria-label="目标季" :disabled="editDisabled" /></label>
        <label>发布集 <input v-model.number="sourceStart" type="number" min="1" max="10000" aria-label="发布起始集" :disabled="editDisabled" /></label><span>对应</span>
        <label>目标集 <input v-model.number="targetStart" type="number" min="1" max="10000" aria-label="目标起始集" :disabled="editDisabled" /></label>
        <button type="button" class="secondary" :aria-label="`应用偏移（${selected.length} 项）`" :disabled="editDisabled || !selected.length" @click="mapFiles()"><span>应用偏移<span class="hidden sm:inline">（{{ selected.length }} 项）</span></span></button>
        <button type="button" class="secondary" title="从目标集开始，按所选顺序连续编号；连集文件保留跨度" :disabled="editDisabled || !selected.length" @click="mapFiles(true)">连续编号</button>
        <button v-if="undo.length" type="button" class="secondary" :disabled="editDisabled" @click="undoMapping"><IconArrowBackUp :size="15" aria-hidden="true" />撤销</button>
      </div></div>
      <p v-if="notice" role="status" class="notice">{{ notice }}</p>
      <p v-if="Object.keys(localConflicts).length" role="alert" class="error">有 {{ Object.keys(localConflicts).length }} 个文件目标重名，请核对季集编号、命名模板或取消重复文件。</p>
      </div>
      <OrganizeFileSelection ref="fileSelection" :rows="rows" root-path="/" :flat="batch" :tv="kind === 'tv'" :episode-editable="true" :disabled="editDisabled" :target-names="targetNames" :conflicts="rowProblems" compact>
        <template #row-details="{ row }">
          <p v-if="results[row.id]?.directory && results[row.id]!.directory !== sharedDirectory" class="scope-note break-all">目录：{{ results[row.id]!.directory }}</p>
          <details v-if="subtitlesByVideo[row.id]?.length" class="subtitle-details" :open="row.selected && subtitlesByVideo[row.id]!.some(sub => displayedConflicts[sub.file.id])" @click.stop @mousedown.stop>
            <summary><IconChevronDown :size="13" aria-hidden="true" />关联字幕（{{ subtitlesByVideo[row.id]!.length }}）<span v-if="!row.selected">未选择</span></summary>
            <div v-for="sub in subtitlesByVideo[row.id]" :key="sub.file.id" class="subtitle-file">
              <p>{{ sub.file.name }}</p>
              <p v-if="row.selected && !namingPreview.errors.value[sub.file.id]" class="subtitle-target">整理后：{{ subtitleName(sub) }}</p>
              <p v-else>随视频选择，当前保留原位</p>
              <p v-if="row.selected && displayedConflicts[sub.file.id]" role="alert" class="error">{{ displayedConflicts[sub.file.id] }}</p>
            </div>
          </details>
        </template>
      </OrganizeFileSelection>
      <details v-if="unmatchedSubtitles.length" class="subtitle-details">
        <summary><IconChevronDown :size="13" aria-hidden="true" />{{ unmatchedSubtitles.length }} 个字幕未能唯一匹配，保留原位</summary>
        <p v-for="sub in unmatchedSubtitles" :key="sub.file.id" class="subtitle-file">{{ sub.file.sourcePath }}</p>
      </details>
    </template>
    <p v-if="error" ref="errorElement" role="alert" class="error">{{ error }} <button v-if="!accountId && !loading" class="secondary" @click="loadConfig">重新读取配置</button><RouterLink v-if="!accountId" to="/115/cloud-download">前往云下载设置</RouterLink></p>
    <p v-if="loading" role="status" class="notice">读取目录配置…</p>
  </section>
    <template v-if="!job" #footer><div class="submit-bar"><span>已选 {{ selected.length }} 个视频<span v-if="selectedSubtitleCount">，关联 {{ selectedSubtitleCount }} 个字幕</span><button v-if="Object.keys(rowProblems).length" type="button" class="locate-problem" @click="locateProblem">{{ Object.keys(rowProblems).length }} 项待处理 · 定位问题</button></span><button type="button" class="primary" :disabled="busy || loading || !confirming && !namingPreview.ready.value || parsing || !!unresolved.length || !accountId || !targetCID || !selected.length || invalid || !!Object.keys(localConflicts).length" @click="start">{{ busy ? '正在确认并创建任务…' : confirming ? '确认任务状态' : planKey === snapshotKey ? '确认下载并整理' : '下载并自动整理' }}</button></div></template>
  </AnimeFloatingDialog>
  <P115DirectoryDialog v-if="directoryOpen" :token="token" :account-id="accountId" title="选择本次整理目录" description="只用于本次任务，不修改全局整理配置" @close="directoryOpen = false" @select="value => { targetCID = value.id; targetPath = value.path; directoryOpen = false }" />
</template>

<style scoped>
.download-organize { @apply min-w-0 space-y-2 pt-1.5 text-slate-700 dark:text-slate-200; }
.naming-summary { @apply flex flex-wrap items-center gap-2 text-xs text-slate-500 dark:text-slate-400; }.naming-summary strong { @apply text-slate-800 dark:text-slate-100; }.naming-summary a { @apply text-blue-600 dark:text-blue-400; }.naming-summary button { @apply ml-auto; }
.resource-title { @apply break-all text-xs leading-5 text-slate-500 dark:text-slate-400; }
.reference { @apply flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400; }.reference strong { @apply text-slate-800 dark:text-slate-100; }
.scope-note { @apply text-xs leading-5 text-slate-500 dark:text-slate-400; }
.locate-problem { @apply ml-2 rounded px-1 py-1 text-xs font-medium text-red-600 underline decoration-red-300 underline-offset-2 hover:bg-red-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-500 dark:text-red-400 dark:hover:bg-red-950/40; }
.organize-help { @apply text-xs text-slate-500 dark:text-slate-400; }.organize-help summary { @apply flex w-fit cursor-pointer list-none items-center gap-1 py-1; }.organize-help summary::-webkit-details-marker { display: none; }.organize-help[open]>summary>svg { transform: rotate(180deg); }.organize-help>div { @apply space-y-1 pt-1; }
.resource-progress { @apply flex items-center justify-between gap-2 text-xs; }.resource-status { @apply text-xs; }.resource-status summary { @apply cursor-pointer py-1; }.resource-status-list { @apply max-h-40 overflow-auto overscroll-contain; }.resource-status-list>div { @apply flex items-center gap-2 py-2; }.resource-status-list p { @apply min-w-0 flex-1 break-all; }.resource-status-list small { @apply block text-slate-500 dark:text-slate-400; }.resource-error { @apply flex flex-wrap items-center gap-2 text-xs; }.resource-error p { @apply w-full break-all text-red-600 dark:text-red-400; }.resource-error>span { @apply text-slate-500 dark:text-slate-400; }
.subtitle-details { @apply mt-1 min-w-0 text-xs leading-5 text-slate-500 dark:text-slate-400; }
.subtitle-details summary { @apply flex cursor-pointer list-none items-center gap-1 py-1; }.subtitle-details summary::-webkit-details-marker { display: none; }.subtitle-details[open]>summary>svg { transform: rotate(180deg); }
.subtitle-file { @apply mb-2 min-w-0 break-all pl-2; }.subtitle-target { @apply text-slate-700 dark:text-slate-200; }
.directory-bar { @apply flex items-center gap-2 rounded-lg bg-slate-100/80 px-3 py-2 dark:bg-slate-800/70; }.directory-bar>div { @apply min-w-0 flex-1 text-xs; }.directory-bar p { @apply flex flex-wrap items-baseline gap-x-2 gap-y-1; }.directory-bar strong { @apply break-all font-medium; }.directory-bar small { @apply mt-1 block break-all text-slate-500 dark:text-slate-400; }
.mapping-bar { @apply flex flex-wrap items-center gap-2 text-xs; }.mapping-bar label { @apply flex items-center gap-1; }.mapping-bar input { @apply w-16 rounded-lg border border-slate-200 bg-white text-slate-800 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100; height: var(--ae-control-height); padding: var(--ae-control-padding); font-size: var(--ae-control-font); }.season-select { @apply w-40; }
.numbering-bar { @apply sticky top-0 z-10 space-y-2 -mx-[22px] px-[22px] py-2; background: transparent; }
.numbering-bar.is-stuck { background: color-mix(in srgb, var(--floating-surface-container) 95%, var(--floating-surface-text)); box-shadow: 0 1px 0 rgb(148 163 184 / .16); }
.season-select :deep(.select-menu-trigger) { height: var(--ae-control-height); min-height: var(--ae-control-height); padding: var(--ae-control-padding); font-size: var(--ae-control-font); }
.batch-toggle { @apply mb-2 flex items-center gap-2 text-xs font-medium text-slate-600 dark:text-slate-300; }
.secondary { @apply inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg bg-slate-100 font-medium text-slate-700 hover:bg-slate-200 disabled:opacity-40 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700; height: var(--ae-control-height); padding: var(--ae-control-padding); font-size: var(--ae-control-font); }
.primary { @apply w-full rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40 sm:w-auto; }
.submit-bar { @apply flex flex-wrap items-center justify-between gap-3 text-xs; }
.notice { @apply text-xs leading-6 text-slate-600 dark:text-slate-300; }.error { @apply break-all text-sm leading-6 text-red-600 dark:text-red-400; }.error a,.task-link { @apply text-sm text-blue-600 underline dark:text-blue-400; }
@media (max-width: 639px) {
  .download-organize { padding-top: 8px; }
  .numbering-bar { margin-inline: -18px; padding-inline: 18px; }
  .mapping-bar { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .mapping-bar label { min-width: 0; white-space: nowrap; }
  .mapping-bar label:first-child { grid-column: 1 / -1; }
  .mapping-bar input { min-width: 0; width: 0; flex: 1; }
  .mapping-bar .season-select { width: auto; flex: 1; }
  .mapping-bar > span { display: none; }
}
</style>
