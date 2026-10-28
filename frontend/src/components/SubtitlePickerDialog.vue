<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowLeft, IconSearch, IconUpload } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import SubtitleMatchList from './SubtitleMatchList.vue'
import { analyzeSubtitle, formatSubtitleSize, maxTotalBytes, mergeSubtitleFiles, readLocalSubtitles, subtitleLanguage, subtitlePairKey, type LocalSubtitle, type LocalSubtitleSelection, type SubtitleMatchRow } from '../utils/subtitleImport'
import { subtitleTargetName, type LibrarySubtitleFile, type LibrarySubtitleSelection, type SubtitleVersion } from '../utils/subtitleLibrary'
import { subtitleRequest, type AssrtSearchResult, type AssrtSubtitle } from '../utils/subtitleApi'
import { readSubtitleResponse } from '../utils/subtitleSources'
import type { OrganizeRow } from '../types/manualOrganize'

const props = defineProps<{ token: string; title: string; originalTitle?: string; videos: OrganizeRow[]; season: number; targetNames: Record<string, string>; library: LibrarySubtitleSelection[]; local: LocalSubtitleSelection[]; version?: SubtitleVersion }>()
const emit = defineEmits<{ close: []; select: [library: LibrarySubtitleSelection[], local: LocalSubtitleSelection[], version?: SubtitleVersion] }>()
type LibraryRow = LibrarySubtitleSelection & SubtitleMatchRow
type LocalRow = LocalSubtitleSelection & SubtitleMatchRow
const lifecycle = new AbortController()
const query = ref(props.title), busy = ref<'' | 'search' | 'files'>(''), searchError = ref(''), versions = ref<SubtitleVersion[]>([]), version = ref(props.version), showResults = ref(true)
const choosing = computed(() => busy.value === 'files')
const assrtItems = ref<AssrtSubtitle[]>([]), assrtBusy = ref(false), assrtError = ref(''), assrtConfigured = ref(false), assrtMore = ref(false)
const searching = computed(() => busy.value === 'search' || assrtBusy.value)
let assrtQuery = '', assrtPosition = 0
const libraryRows = ref<LibraryRow[]>(props.library.map(item => ({ ...item, key: `library:${item.versionId}:${item.path}`, pairKey: libraryPairKey(item.versionId, item.path), sourceLabel: '字幕库', selected: true, reason: '已保存的字幕匹配' })))
const localRows = ref<LocalRow[]>(props.local.map(item => ({ ...item, key: item.localId, pairKey: localPairKey(item.file), sourceLabel: localSourceLabel(item.file), selected: true, reason: '已保存的字幕匹配' })))
const hasLocal = computed(() => localRows.value.some(row => row.file.origin !== 'assrt'))
const rows = computed(() => [...libraryRows.value, ...localRows.value])
const selected = computed(() => rows.value.filter(row => row.selected))
const total = computed(() => selected.value.reduce((sum, row) => sum + ('localId' in row ? row.file.data.length : row.file.size), 0))
const picker = ref<HTMLInputElement>(), reading = ref(false), importError = ref(''), notice = ref(''), append = ref(true)
let controller: AbortController | undefined
let loadedVersion = ''
const loadedAssrt = new Set<number>()
const issue = computed(() => {
  if (selected.value.length > 100 || total.value > maxTotalBytes) return '每次最多选择 100 个字幕，总大小不超过 64 MiB。'
  const targets = new Set<string>()
  for (const row of selected.value) {
    if (!props.videos.some(video => video.id === row.videoId)) return '请为所选字幕指定对应视频。'
    const marker = 'localId' in row ? row.version : ''
    if (/[\\/:*?"<>|\x00-\x1f]/.test(marker) || marker.trim() !== marker || marker.endsWith('.')) return '字幕版本不能包含路径符号、首尾空格或末尾句点。'
    const target = `${row.videoId}/${subtitleTargetName('', row.file.name, row.language, marker)}`.toLowerCase()
    if (targets.has(target)) return '多个字幕的目标名称相同，请调整语言、版本或取消勾选。'
    targets.add(target)
    if (/\.idx$/i.test(row.file.name) && !selected.value.some(other => /\.sub$/i.test(other.file.name) && other.pairKey === row.pairKey && other.videoId === row.videoId && other.language === row.language && (other.version || '') === marker)) return 'IDX 需要选择配套 SUB，并关联相同视频、语言和版本。'
  }
  return ''
})
const request = <T,>(path: string) => subtitleRequest<T>(props.token, path, lifecycle.signal)
async function search(initial = false) {
  if (busy.value || assrtBusy.value || reading.value || !query.value.trim()) return
  busy.value = 'search'; searchError.value = ''; showResults.value = true; versions.value = []
  assrtItems.value = []; assrtMore.value = false; assrtPosition = 0; assrtQuery = query.value.trim()
  await Promise.allSettled([searchLibrary(initial), searchAssrt()])
}
async function searchLibrary(initial: boolean) {
  try {
    const params = new URLSearchParams(); params.append('q', query.value.trim())
    if (initial && props.originalTitle && props.originalTitle !== query.value.trim()) params.append('q', props.originalTitle)
    versions.value = (await request<{ versions: SubtitleVersion[] }>(`search?${params}`)).versions
  } catch (reason) { if (!lifecycle.signal.aborted) { versions.value = []; searchError.value = reason instanceof Error ? reason.message : '搜索失败' } }
  finally { busy.value = '' }
}
async function searchAssrt(more = false) {
  if (assrtBusy.value) return
  assrtBusy.value = true; assrtError.value = ''
  try {
    const data = await request<AssrtSearchResult>(`assrt/search?${new URLSearchParams({ q: assrtQuery, pos: String(more ? assrtPosition : 0) })}`)
    assrtConfigured.value = data.configured
    if (!data.configured) return
    assrtItems.value = [...(more ? assrtItems.value : []), ...data.items.filter(item => !more || !assrtItems.value.some(old => old.id === item.id))]
    assrtPosition = (more ? assrtPosition : 0) + data.items.length
    assrtMore.value = data.hasMore
  } catch (reason) { if (!lifecycle.signal.aborted) assrtError.value = reason instanceof Error ? reason.message : '射手网搜索失败' }
  finally { assrtBusy.value = false }
}
async function choose(item: SubtitleVersion) {
  if (busy.value || reading.value) return
  if (loadedVersion === item.id) { showResults.value = false; return }
  busy.value = 'files'; searchError.value = ''
  try {
    const data = await request<{ files: LibrarySubtitleFile[] }>(`versions/${encodeURIComponent(item.id)}/files`)
    const existing = libraryRows.value.some(row => row.versionId === item.id)
    libraryRows.value = data.files.map(file => {
      const match = analyzeSubtitle(file.name, props.videos, props.season), lang = subtitleLanguage(file.name)
      const saved = libraryRows.value.find(row => row.versionId === item.id && row.path === file.path && row.file.sha === file.sha)
      return { key: `library:${item.id}:${file.path}`, pairKey: libraryPairKey(item.id, file.path), sourceLabel: `字幕库 · ${item.name}`, versionId: item.id, path: file.path, file, language: saved?.language ?? lang, videoId: saved && props.videos.some(video => video.id === saved.videoId) ? saved.videoId : match.videoId, selected: existing ? !!saved?.selected : !!match.videoId && lang === 'zh-CN', reason: match.reason }
    })
    version.value = item; loadedVersion = item.id; showResults.value = false
  } catch (reason) { if (!lifecycle.signal.aborted) searchError.value = reason instanceof Error ? reason.message : '读取版本失败' }
  finally { busy.value = '' }
}
function libraryPairKey(id: string, name: string) { const pair = subtitlePairKey(name); return pair ? `library:${id}/${pair}` : '' }
function localPairKey(file: LocalSubtitleSelection['file']) { const pair = subtitlePairKey(file.name); return pair ? `local:${file.sourceKey || 'local'}/${pair}` : '' }
function localSourceLabel(file: LocalSubtitle) { return file.origin === 'assrt' ? file.sourceLabel || '射手网' : '本地导入' }
async function read(files: File[], item?: AssrtSubtitle) {
  if (reading.value || choosing.value || !item && !files.length) return
  const sourceKey = item ? `assrt:${item.id}` : ''
  if (item && loadedAssrt.has(item.id) && localRows.value.some(row => row.file.sourceKey === sourceKey)) { notice.value = '这份射手字幕已载入，可在下方调整选择。'; return }
  reading.value = true; importError.value = ''; controller = new AbortController()
  try {
    const signal = AbortSignal.any([lifecycle.signal, controller.signal, AbortSignal.timeout(90000)])
    if (item) {
      const response = await fetch(`/api/admin/subtitles/assrt/${item.id}/download`, { headers: { Authorization: `Bearer ${props.token}` }, cache: 'no-store', signal })
      const bytes = await readSubtitleResponse(response, Number(response.headers.get('content-length')), signal, () => {})
      const name = decodeURIComponent(response.headers.get('x-subtitle-filename') || '')
      if (!name) throw new Error('射手字幕缺少文件名，请重新选择。')
      files = [new File([new Uint8Array(bytes)], name)]
    }
    const data = await readLocalSubtitles(files, signal)
    if (!data.files.length) throw new Error('没有找到支持的字幕文件，原列表已保留。')
    if (item) data.files = data.files.map(file => ({ ...file, sourceKey, sourceLabel: `射手网 · ${item.group || item.title}`, origin: 'assrt' }))
    const current = append.value || item ? localRows.value : localRows.value.filter(row => row.file.origin === 'assrt')
    const merged = mergeSubtitleFiles(current.map(row => row.file), data.files, true)
    const kept = localRows.value.filter(row => merged.kept.includes(row.file))
    localRows.value = [...kept, ...merged.added.map(file => {
      const match = analyzeSubtitle(file.name, props.videos, props.season)
      const localId = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
      return { key: localId, pairKey: localPairKey(file), sourceLabel: localSourceLabel(file), localId, file, videoId: match.videoId, language: subtitleLanguage(file.name), version: '', selected: !!match.videoId, reason: match.reason }
    })]
    if (item) loadedAssrt.add(item.id)
    notice.value = `共 ${localRows.value.length} 个字幕，${localRows.value.filter(row => !row.videoId).length} 个未匹配，默认不选${merged.duplicates ? `；已跳过 ${merged.duplicates} 个重复字幕` : ''}${data.ignored ? `；已忽略 ${data.ignored} 个其他文件` : ''}。`
  } catch (reason) {
    if (lifecycle.signal.aborted) return
    if (controller.signal.aborted) notice.value = '已取消读取，原列表已保留。'
    else importError.value = reason instanceof Error ? reason.message : '读取字幕失败'
  } finally { reading.value = false }
}
function selectFiles(event: Event) { const input = event.target as HTMLInputElement; const files = Array.from(input.files || []); input.value = ''; void read(files) }
function clear() { libraryRows.value = []; localRows.value = []; version.value = undefined; loadedVersion = ''; loadedAssrt.clear(); notice.value = ''; importError.value = ''; showResults.value = true }
function apply() {
  if (reading.value || choosing.value || issue.value) return
  const library: LibrarySubtitleSelection[] = [], local: LocalSubtitleSelection[] = []
  for (const row of selected.value) {
    if ('localId' in row) { const { localId, file, videoId, language, version } = row; local.push({ localId, file, videoId, language, version }) }
    else { const { versionId, path, file, videoId, language } = row; library.push({ versionId, path, file, videoId, language }) }
  }
  emit('select', library, local, library.length ? version.value : undefined)
}
onMounted(() => search(true))
onBeforeUnmount(() => { lifecycle.abort(); controller?.abort() })
</script>

<template>
  <AnimeFloatingDialog title="查找字幕" close-label="关闭字幕选择" size="config" class="subtitle-picker" @close="emit('close')">
    <template #header>
      <div class="picker-actions">
        <form class="picker-search" @submit.prevent="search()"><input v-model="query" aria-label="字幕作品搜索词" maxlength="128" placeholder="作品中文名、原名或英文名" /><button type="submit" :disabled="!!busy || assrtBusy || reading || !query.trim()"><IconSearch :size="16" aria-hidden="true" />搜索</button></form>
        <button type="button" class="picker-import" :disabled="reading" @click="picker?.click()"><IconUpload :size="16" aria-hidden="true" />导入字幕</button>
        <input ref="picker" type="file" multiple accept=".zip,.rar,.7z,.ass,.ssa,.srt,.vtt,.idx,.sub" class="sr-only" aria-label="选择本地字幕或压缩包" :disabled="reading" @change="selectFiles" />
      </div>
    </template>
    <div @dragover.prevent @drop.prevent="read(Array.from($event.dataTransfer?.files || []))">
      <p v-if="busy" role="status" class="picker-note">{{ choosing ? '正在读取字幕文件…' : '正在查找字幕…' }}</p>
      <p v-if="searchError" role="alert" class="picker-error">{{ searchError }}</p>
      <template v-if="showResults">
        <div v-if="!searching && !versions.length && !assrtItems.length && !rows.length" class="picker-empty"><IconUpload :size="28" aria-hidden="true" /><p>{{ searchError || assrtError ? '可以直接导入本地字幕' : '没有找到匹配字幕' }}</p><span>换用原名搜索，或导入字幕文件、ZIP / RAR / 7z 压缩包。</span><button type="button" :disabled="reading" @click="picker?.click()">选择字幕或压缩包</button></div>
        <ul class="picker-versions"><li v-for="item in versions" :key="item.id"><button type="button" :disabled="choosing || reading" @click="choose(item)"><strong>{{ item.title }} <small>{{ item.year }}</small><small class="picker-source">字幕库</small></strong><span>{{ item.source }} · {{ item.name }}</span></button></li>
          <li v-for="item in assrtItems" :key="`assrt:${item.id}`"><button type="button" :disabled="choosing || reading" @click="read([], item)"><strong>{{ item.title || item.videoName }}<small class="picker-source">射手网</small></strong><span>{{ [item.language, item.group, item.format].filter(Boolean).join(' · ') }}</span><span v-if="item.videoName" class="picker-video">{{ item.videoName }}</span></button></li>
        </ul>
        <p v-if="assrtBusy" role="status" class="picker-note">射手网正在搜索…</p>
        <p v-if="assrtError" role="alert" class="picker-note">射手网：{{ assrtError }} <button type="button" :disabled="assrtBusy" @click="searchAssrt(assrtItems.length > 0)">重试</button></p>
        <button v-if="assrtMore" type="button" :disabled="assrtBusy || reading || choosing" @click="searchAssrt(true)">加载更多射手字幕</button>
      </template>
      <div v-else-if="version" class="picker-version"><button type="button" class="picker-back" :disabled="choosing" @click="showResults = true; searchError = ''"><IconArrowLeft :size="15" aria-hidden="true" />返回搜索结果</button><strong>{{ version.title }} · {{ version.name }}</strong><a :href="version.url" target="_blank" rel="noopener noreferrer">查看来源</a><details v-if="version.description" class="picker-note"><summary>版本说明</summary><p class="whitespace-pre-wrap break-words">{{ version.description }}</p></details></div>
      <p v-if="reading" role="status" class="picker-note">正在读取和解析字幕…<button type="button" @click="controller?.abort()">取消读取</button></p>
      <p v-if="notice" role="status" class="picker-note">{{ notice }}</p>
      <p v-if="importError" role="alert" class="picker-error">{{ importError }}</p>
      <div v-if="hasLocal" class="picker-local"><label><input v-model="append" type="checkbox" :disabled="reading" />继续添加时保留已导入文件</label><button type="button" :disabled="reading" @click="localRows = localRows.filter(row => row.file.origin === 'assrt'); notice = ''; importError = ''">移除本地字幕</button></div>
      <SubtitleMatchList v-if="rows.length" :rows="rows" :videos="videos" :target-names="targetNames" :disabled="reading || choosing" />
    </div>
    <template #footer>
      <div class="picker-footer"><span>已选 {{ selected.length }} 个字幕 · {{ formatSubtitleSize(total) }}</span><div><button v-if="rows.length" type="button" :disabled="reading || choosing" @click="clear">移除全部</button><button v-if="rows.length || library.length || local.length" type="button" class="picker-apply" :disabled="reading || choosing || !!issue" @click="apply">使用所选字幕</button></div></div>
      <p v-if="issue" role="alert" class="picker-error">{{ issue }}</p>
      <p class="picker-note">支持字幕文件、ZIP / RAR / 7z，可拖入。单个 16 MiB，合计 64 MiB / 100 个。确认下载整理后上传。</p>
      <p v-if="assrtConfigured || assrtItems.length || localRows.some(row => row.file.origin === 'assrt')" class="picker-note">字幕服务由 <a href="https://secure.assrt.net/" target="_blank" rel="noopener noreferrer">assrt.net</a> 提供。</p>
    </template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.subtitle-picker { font-size:13px; }
.picker-actions,.picker-search,.picker-footer,.picker-footer>div,.picker-local { display:flex;align-items:center;gap:8px; }
.picker-search { flex:1;min-width:0; }
.picker-search input { flex:1;min-width:0;min-height:36px;padding:6px 10px;border:1px solid rgb(148 163 184 / .25);border-radius:9px;background:var(--floating-surface-container,#fff);color:inherit; }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }
button:disabled { opacity:.45;cursor:default; }
.picker-search button,.picker-import,.picker-back { display:inline-flex;align-items:center;justify-content:center;gap:6px;flex:none; }
.picker-import { color:#2563eb;background:rgb(59 130 246 / .1); }
.picker-note { margin:8px 0 0;color:var(--floating-surface-muted,#64748b);font-size:12px;line-height:1.6;overflow-wrap:anywhere; }
.picker-note button { margin-left:6px; }
.picker-versions { list-style:none;margin:0;padding:0; }
.picker-versions li { border-bottom:1px solid rgb(148 163 184 / .14); }
.picker-versions button { width:100%;padding:12px 6px;text-align:left;background:none; }
.picker-versions button:hover { background:rgb(59 130 246 / .07); }
.picker-versions strong,.picker-versions span { display:block;overflow-wrap:anywhere; }
.picker-versions small { font-weight:400;color:var(--floating-surface-muted); }
.picker-versions .picker-source { display:inline-block;margin-left:8px;padding:1px 6px;border-radius:5px;background:rgb(148 163 184 / .1);font-size:11px; }
.picker-versions .picker-video { font-size:11px; }
.picker-note a { color:#2563eb; }
.picker-versions span { margin-top:4px;color:var(--floating-surface-muted);font-size:12px; }
.picker-version { display:flex;align-items:center;flex-wrap:wrap;gap:8px; }
.picker-version strong { flex:1;min-width:100px;overflow-wrap:anywhere; }
.picker-version a { color:#2563eb;font-size:12px; }
.picker-version details { flex-basis:100%; }
.picker-empty { display:flex;flex-direction:column;align-items:center;gap:10px;padding:28px 12px;text-align:center;border-radius:12px;background:rgb(148 163 184 / .06); }
.picker-empty svg,.picker-empty span { color:var(--floating-surface-muted,#64748b); }
.picker-empty span { font-size:12px;line-height:1.6; }
.picker-empty button { background:#2563eb;color:#fff; }
.picker-footer,.picker-local { justify-content:space-between;flex-wrap:wrap; }
.picker-local { margin-top:12px;font-size:12px; }
.picker-local label { display:flex;align-items:center;gap:6px; }
input { accent-color:#2563eb; }
.picker-apply { background:#2563eb;color:#fff; }
.picker-error { color:#dc2626;margin-top:8px;font-size:12px;overflow-wrap:anywhere; }
:global(.dark) .picker-import,:global(.dark) .picker-version a,:global(.dark) .picker-note a { color:#93c5fd; }
:global(.dark) .picker-error { color:#f87171; }
@media(max-width:639px) { button,.picker-search input { min-height:44px; } .picker-search input { font-size:16px; } .picker-actions { flex-wrap:wrap; } .picker-search { flex-basis:100%; } .picker-footer>div { margin-left:auto; } }
</style>
