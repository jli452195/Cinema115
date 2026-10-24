<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { IconFolder, IconArrowBackUp, IconChevronDown } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import OrganizeFileSelection from './OrganizeFileSelection.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import SelectMenu from './SelectMenu.vue'
import { p115Request } from '../lib/p115-api'
import type { OrganizeFile, OrganizeJob, OrganizeRow, OrganizeSubtitle } from '../types/manualOrganize'
import { applyOrganizeMapping, organizeTargetName, organizeSubtitleName } from '../utils/organizeNaming'
import { directoryGroups } from '../utils/organizeSelection'

const props = defineProps<{
  token: string; resource: { id: string; title: string; magnet: string; fansub?: string; publisher?: string }
  files: OrganizeFile[]
  subtitles?: OrganizeSubtitle[]
  tmdbId: number; kind: 'movie' | 'tv'; title: string; date: string
  seasons: { number: number; name: string; episodeCount: number }[]
  themeColors?: Record<string, string>
}>()
const emit = defineEmits<{ close: []; submitted: [job: OrganizeJob]; uncertain: [] }>()
const busy = ref(false), loading = ref(true), error = ref(''), notice = ref('')
const confirming = ref(false)
const batchOpen = ref(window.matchMedia('(min-width: 640px)').matches)
const editDisabled = computed(() => busy.value || loading.value || confirming.value)
const directoryOpen = ref(false), accountId = ref(''), targetCID = ref(''), targetPath = ref(''), downloadPath = ref('')
const season = ref(String(props.seasons.find(s => s.number > 0)?.number ?? 1)), sourceStart = ref(1), targetStart = ref(1)
const sources = Object.fromEntries(props.files.map(file => [file.id, file]))
const rows = ref<OrganizeRow[]>(props.files.map(file => ({ ...file, selected: false, endEpisode: file.endEpisode || '', suffix: props.resource.fansub || props.resource.publisher || '' })))
const selected = computed(() => rows.value.filter(row => row.selected))
const seasonOptions = computed(() => props.seasons.map(s => ({ value: String(s.number), label: `${s.name || (s.number ? `第 ${s.number} 季` : '特别篇')} · ${s.episodeCount} 集` })))
const targetNames = computed(() => Object.fromEntries(rows.value.map(row => [row.id, organizeTargetName(row, props.title, props.date, props.kind)])))
const subtitlesByVideo = computed(() => {
  const groups: Record<string, OrganizeSubtitle[]> = {}
  for (const sub of props.subtitles || []) if (sub.videoId) (groups[sub.videoId] ||= []).push(sub)
  return groups
})
const unmatchedSubtitles = computed(() => (props.subtitles || []).filter(sub => !sub.videoId))
const selectedSubtitleCount = computed(() => selected.value.reduce((count, row) => count + (subtitlesByVideo.value[row.id]?.length || 0), 0))
const invalid = computed(() => selected.value.some(row => props.kind === 'tv' && (targetNames.value[row.id] === '请填写目标季集' || row.episode > 10000 || Number(row.endEpisode) > 10000 || row.season > 1000)))
const undo = ref<ReturnType<typeof applyOrganizeMapping>>([])
const conflicts = ref<Record<string, string>>({})
const previewId = ref('')
const job = ref<OrganizeJob>()
const lifecycle = new AbortController()
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(130000)])
const snapshotKey = computed(() => JSON.stringify({ targetCID: targetCID.value, targetPath: targetPath.value, files: selected.value.map(row => ({ id: row.id, season: row.season, episode: row.episode, endEpisode: Number(row.endEpisode) || 0, suffix: row.suffix })) }))
let previewKey = ''

function mapFiles(sequential = false) {
  error.value = ''; notice.value = ''; conflicts.value = {}
  try {
    undo.value = applyOrganizeMapping(directoryGroups(rows.value, '/').flatMap(group => group.rows), sources, Number(season.value), sourceStart.value, targetStart.value, sequential)
    const skipped = selected.value.length - undo.value.length
    notice.value = `已更新 ${undo.value.length} 项${skipped ? `，跳过 ${skipped} 项无发布编号的文件，可在行内填写目标季集` : !sequential ? '，保留发布编号的缺号和连集范围' : ''}`
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
      p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'garden-organize/config', { signal: signal() }),
    ])
    if (!cloud.accountId || !cloud.targetCID) throw new Error('请先在 115 云下载中配置主账号的下载目录')
    accountId.value = cloud.accountId; downloadPath.value = cloud.targetPath
    if (organize?.accountId === cloud.accountId) { targetCID.value = organize.targetCID; targetPath.value = organize.targetPath }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取目录配置失败' }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
async function start() {
  if (busy.value || loading.value || !selected.value.length || invalid.value || !targetCID.value || !accountId.value || job.value) return
  busy.value = true; error.value = ''; notice.value = ''
  let creationAttempt = false
  try {
    const key = snapshotKey.value
    if (!previewId.value || previewKey !== key) {
      const [provider, providerId] = props.resource.id.split(':')
      const preview = await p115Request<OrganizeJob>(props.token, 'garden-organize/preview', { method: 'POST', signal: signal(), body: JSON.stringify({ provider, providerId, magnet: props.resource.magnet, tmdbId: props.tmdbId, kind: props.kind, accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value, files: JSON.parse(key).files }) })
      conflicts.value = Object.fromEntries(preview.items.filter(item => item.conflict).map(item => [item.file.id, item.conflict!]))
      if (Object.keys(conflicts.value).length) throw new Error('命名或目标目录存在冲突，请调整标出的文件')
      previewId.value = preview.id; previewKey = key
    }
    // Reuse the ID after a lost response; the server persists before submitting.
    creationAttempt = true
    job.value = await p115Request<OrganizeJob>(props.token, 'garden-organize/jobs', { method: 'POST', signal: signal(), body: JSON.stringify({ previewId: previewId.value }) })
    confirming.value = false
    emit('submitted', job.value)
  } catch (e) {
    if (!lifecycle.signal.aborted) {
      const status = (e as { status?: number }).status
      if (creationAttempt && (!status || status >= 500)) {
        confirming.value = true; emit('uncertain')
        error.value = '创建结果暂未确认，请点击“确认任务状态”读取同一任务，或在整理任务记录中核对。'
      } else {
        confirming.value = false
        if (creationAttempt) previewId.value = ''
        error.value = e instanceof Error ? e.message : '提交失败，请重试'
      }
    }
  }
  finally { busy.value = false }
}
onMounted(loadConfig)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <AnimeFloatingDialog :title="job ? '任务已创建' : '下载并整理'" close-label="关闭下载并整理" size="organize" :close-disabled="busy" :style="themeColors" @close="emit('close')">
  <section class="garden-organize" aria-label="下载并整理映射" :aria-busy="busy || loading">
    <p class="resource-title">{{ resource.title }}</p>
    <template v-if="job">
      <p role="status" class="notice">后台将下载整条磁力，并按本次映射整理 {{ selected.length }} 个视频<span v-if="selectedSubtitleCount">及 {{ selectedSubtitleCount }} 个字幕</span>。关闭弹窗后继续执行。</p>
      <RouterLink to="/115/download-organize" class="task-link">查看下载整理任务</RouterLink>
    </template>
    <template v-else>
      <p class="scope-note">整条磁力下载，整理所选视频及已关联的字幕；其余文件保留在下载目录。</p>
      <div class="reference"><strong>{{ title }}</strong><span>TMDB 季集参考</span><span v-for="s in seasons" :key="s.number">{{ s.number === 0 ? '特别篇' : `第 ${s.number} 季` }} {{ s.episodeCount }} 集</span><span v-if="kind === 'movie'">电影</span></div>
      <div class="directory-bar"><div><span>整理到</span><strong :title="targetPath">{{ targetPath || '请选择整理目录' }}</strong><small v-if="downloadPath">临时下载：{{ downloadPath.replace(/\/$/, '') }}/garden-任务编号</small></div><button type="button" class="secondary" :disabled="editDisabled || !accountId" @click="directoryOpen = true"><IconFolder :size="16" aria-hidden="true" />选择目录</button></div>
      <div v-if="kind === 'tv'" class="batch-editor"><button type="button" class="batch-toggle" :aria-expanded="batchOpen" @click="batchOpen = !batchOpen">批量编号 <IconChevronDown :size="14" aria-hidden="true" :class="{ 'rotate-180': batchOpen }" /></button><div v-show="batchOpen" class="mapping-bar">
        <label>目标季 <SelectMenu v-if="seasonOptions.length" v-model="season" :options="seasonOptions" label="目标季" :disabled="editDisabled" class="season-select" /><input v-else v-model="season" type="number" min="0" max="1000" aria-label="目标季" :disabled="editDisabled" /></label>
        <label>发布集 <input v-model.number="sourceStart" type="number" min="1" max="10000" aria-label="发布起始集" :disabled="editDisabled" /></label><span>对应</span>
        <label>目标集 <input v-model.number="targetStart" type="number" min="1" max="10000" aria-label="目标起始集" :disabled="editDisabled" /></label>
        <button type="button" class="secondary" :disabled="editDisabled || !selected.length" @click="mapFiles()">应用偏移（{{ selected.length }} 项）</button>
        <details><summary>更多编号</summary><button type="button" class="secondary" :disabled="editDisabled || !selected.length" @click="mapFiles(true)">按所选顺序连续编号</button><p>从目标集开始，连集文件保留跨度。</p></details>
        <button v-if="undo.length" type="button" class="secondary" :disabled="editDisabled" @click="undoMapping"><IconArrowBackUp :size="15" aria-hidden="true" />撤销</button>
      </div></div>
      <p v-if="notice" role="status" class="notice">{{ notice }}</p>
      <OrganizeFileSelection :rows="rows" root-path="/" :tv="kind === 'tv'" :episode-editable="true" :disabled="editDisabled" :target-names="targetNames" :conflicts="conflicts" compact>
        <template #row-details="{ row }">
          <details v-if="subtitlesByVideo[row.id]?.length" class="subtitle-details" :open="row.selected && subtitlesByVideo[row.id]!.some(sub => conflicts[sub.file.id])" @click.stop @mousedown.stop>
            <summary><IconChevronDown :size="13" aria-hidden="true" />关联字幕（{{ subtitlesByVideo[row.id]!.length }}）<span v-if="!row.selected">未选择</span></summary>
            <div v-for="sub in subtitlesByVideo[row.id]" :key="sub.file.id" class="subtitle-file">
              <p>{{ sub.file.name }}</p>
              <p v-if="row.selected" class="subtitle-target">整理后：{{ organizeSubtitleName(targetNames[row.id]!, sub) }}</p>
              <p v-else>随视频选择，当前保留原位</p>
              <p v-if="row.selected && conflicts[sub.file.id]" role="alert" class="error">{{ conflicts[sub.file.id] }}</p>
            </div>
          </details>
        </template>
      </OrganizeFileSelection>
      <details v-if="unmatchedSubtitles.length" class="subtitle-details">
        <summary><IconChevronDown :size="13" aria-hidden="true" />{{ unmatchedSubtitles.length }} 个字幕未能唯一匹配，保留原位</summary>
        <p v-for="sub in unmatchedSubtitles" :key="sub.file.id" class="subtitle-file">{{ sub.file.sourcePath }}</p>
      </details>
      <p class="scope-note">请先选中需要整理的文件并核对季集；批量编号仅影响当前所选文件，其他修改会保留。</p>
    </template>
    <p v-if="error" role="alert" class="error">{{ error }} <button v-if="!accountId && !loading" class="secondary" @click="loadConfig">重新读取配置</button><RouterLink v-if="!accountId" to="/115/cloud-download">前往云下载设置</RouterLink></p>
    <p v-if="loading" role="status" class="notice">读取目录配置…</p>
  </section>
    <template v-if="!job" #footer><div class="submit-bar"><span>已选 {{ selected.length }} 个视频<span v-if="selectedSubtitleCount">，关联 {{ selectedSubtitleCount }} 个字幕</span><span v-if="invalid">，请补全季集</span></span><button type="button" class="primary" :disabled="busy || loading || !accountId || !targetCID || !selected.length || invalid" @click="start">{{ busy ? '正在确认并创建任务…' : confirming ? '确认任务状态' : '下载并自动整理' }}</button></div></template>
  </AnimeFloatingDialog>
  <P115DirectoryDialog v-if="directoryOpen" :token="token" :account-id="accountId" title="选择本次整理目录" description="只用于本次任务，不修改全局整理配置" @close="directoryOpen = false" @select="value => { targetCID = value.id; targetPath = value.path; directoryOpen = false }" />
</template>

<style scoped>
.garden-organize { @apply min-w-0 space-y-3 text-slate-700 dark:text-slate-200; }
.resource-title { @apply break-all text-xs leading-5 text-slate-500 dark:text-slate-400; }
.reference { @apply flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400; }.reference strong { @apply text-slate-800 dark:text-slate-100; }
.scope-note { @apply text-xs leading-5 text-slate-500 dark:text-slate-400; }
.subtitle-details { @apply mt-1 min-w-0 text-xs leading-5 text-slate-500 dark:text-slate-400; }
.subtitle-details summary { @apply flex cursor-pointer list-none items-center gap-1 py-1; }.subtitle-details summary::-webkit-details-marker { display: none; }.subtitle-details[open]>summary>svg { transform: rotate(180deg); }
.subtitle-file { @apply mb-2 min-w-0 break-all pl-2; }.subtitle-target { @apply text-slate-700 dark:text-slate-200; }
.directory-bar { @apply flex items-center gap-3 rounded-xl bg-slate-100/80 p-3 dark:bg-slate-800/70; }.directory-bar>div { @apply flex min-w-0 flex-1 flex-col gap-1 text-xs; }.directory-bar strong { @apply break-all text-sm font-medium; }.directory-bar small { @apply break-all text-slate-500 dark:text-slate-400; }
.mapping-bar { @apply flex flex-wrap items-center gap-2 text-xs; }.mapping-bar label { @apply flex items-center gap-1; }.mapping-bar input { @apply w-16 rounded-lg border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-800 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100; }.season-select { @apply w-40; }
.batch-toggle { @apply mb-2 flex items-center gap-2 text-xs font-medium text-slate-600 dark:text-slate-300; }
.mapping-bar details { @apply relative text-slate-500 dark:text-slate-400; }.mapping-bar summary { @apply cursor-pointer rounded-lg px-2 py-2; }.mapping-bar details p { @apply max-w-48 pt-1 text-xs; }
.secondary { @apply inline-flex shrink-0 items-center justify-center gap-1.5 rounded-lg bg-slate-100 px-2.5 py-2 text-xs font-medium text-slate-700 hover:bg-slate-200 disabled:opacity-40 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700; }
.primary { @apply rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40; }
.submit-bar { @apply flex flex-wrap items-center justify-between gap-3 text-xs; }
.notice { @apply text-xs leading-6 text-slate-600 dark:text-slate-300; }.error { @apply break-all text-sm leading-6 text-red-600 dark:text-red-400; }.error a,.task-link { @apply text-sm text-blue-600 underline dark:text-blue-400; }
</style>
