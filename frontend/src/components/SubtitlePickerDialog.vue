<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { IconArrowLeft, IconSearch, IconUpload } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import SubtitleMatchList from './SubtitleMatchList.vue'
import SelectMenu from './SelectMenu.vue'
import { analyzeSubtitle, formatSubtitleSize, mergeSubtitleFiles, readLocalSubtitles, subtitleLanguage, subtitlePairKey, type LocalSubtitle, type LocalSubtitleSelection, type SubtitleMatchRow } from '../utils/subtitleImport'
import { maxSubtitleFiles, subtitleSelectionIssue } from '../utils/subtitleLimits'
import { subtitleOutputConflicts } from '../utils/subtitlePresentation'
import { subtitleTargetName, type LibrarySubtitleFile, type LibrarySubtitleSelection, type SubtitleVersion } from '../utils/subtitleLibrary'
import { subtitleRequest, type AssrtSearchResult, type AssrtSubtitle } from '../utils/subtitleApi'
import { readSubtitleResponse } from '../utils/subtitleSources'
import type { OrganizeRow } from '../types/organize'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; animationLibrary?: boolean; title: string; originalTitle?: string; videos: OrganizeRow[]; targetNames: Record<string, string>; library: LibrarySubtitleSelection[]; local: LocalSubtitleSelection[]; version?: SubtitleVersion }>()
const emit = defineEmits<{ close: []; select: [library: LibrarySubtitleSelection[], local: LocalSubtitleSelection[], version?: SubtitleVersion] }>()
type LibraryRow = LibrarySubtitleSelection & SubtitleMatchRow
type LocalRow = LocalSubtitleSelection & SubtitleMatchRow
const lifecycle = new AbortController()
const query = ref(props.title), busy = ref<'' | 'search' | 'files'>(''), searchError = ref(''), versions = ref<SubtitleVersion[]>([]), version = ref(props.version), showResults = ref(!props.library.length && !props.local.length)
const dialogRef = ref<InstanceType<typeof AnimeFloatingDialog>>()
const matchList = ref<InstanceType<typeof SubtitleMatchList>>()
const batchOpen = ref(false)
let searched = false, savedScroll = 0
let savedTrigger: HTMLElement | null = null
let savedAction = ''
watch([showResults, batchOpen], async ([searchView, batchView], [oldSearch, oldBatch]) => {
  if (!oldSearch && !oldBatch && (searchView || batchView)) {
    savedScroll = dialogRef.value?.body?.scrollTop || 0
    savedTrigger = document.activeElement as HTMLElement | null
    savedAction = savedTrigger?.getAttribute('data-subtitle-action') || ''
    await nextTick(); dialogRef.value?.scrollTo(0)
    document.querySelector<HTMLElement>('.subtitle-picker .subtitle-batch button:not(:disabled), .subtitle-picker .subtitle-batch input:not(:disabled), .subtitle-picker .picker-search input')?.focus({ preventScroll: true })
  } else if ((oldSearch || oldBatch) && !searchView && !batchView) {
    await nextTick(); dialogRef.value?.scrollTo(savedScroll)
    if (savedTrigger?.isConnected) savedTrigger.focus({ preventScroll: true })
    else { const trigger = [...document.querySelectorAll<HTMLElement>('.subtitle-picker [data-subtitle-action]')].find(element => element.getAttribute('data-subtitle-action') === savedAction); (trigger || document.querySelector<HTMLElement>('.subtitle-picker .match-batch-button, .subtitle-picker .picker-back'))?.focus({ preventScroll: true }) }
  }
}, { flush: 'pre' })
async function closeView() {
  if (batchOpen.value) { batchOpen.value = false; return }
  if (showResults.value && rows.value.length) { showResults.value = false; return }
  if (await matchList.value?.closeEditor()) return
  emit('close')
}
function openSearch() { showResults.value = true; if (!searched) void search(true) }
const choosing = computed(() => busy.value === 'files')
const assrtItems = ref<AssrtSubtitle[]>([]), assrtBusy = ref(false), assrtError = ref(''), assrtConfigured = ref(false), assrtMore = ref(false)
const searching = computed(() => busy.value === 'search' || assrtBusy.value)
const targetSeasons = computed(() => [...new Set(props.videos.map(video => video.season).filter(season => Number.isInteger(season) && season >= 0 && season <= 1000))].sort((a, b) => a - b))
const matchingSeason = ref(targetSeasons.value.length === 1 ? String(targetSeasons.value[0]) : '')
const seasonOptions = computed(() => [{ value: '', label: '请选择字幕所属季' }, ...targetSeasons.value.map(number => ({ value: String(number), label: number === 0 ? '特别篇' : `第 ${number} 季` }))])
const subtitleMatch = (name: string) => analyzeSubtitle(name, props.videos, matchingSeason.value === '' ? NaN : Number(matchingSeason.value), props.kind, { sourceEpisodes: true })
let assrtQuery = '', assrtPosition = 0
const libraryRows = ref<LibraryRow[]>(props.library.map(item => ({ ...item, key: `library:${item.versionId}:${item.path}`, pairKey: libraryPairKey(item.versionId, item.path), sourceLabel: '字幕库', selected: true, reason: '已保存的字幕匹配' })))
const localRows = ref<LocalRow[]>(props.local.map(item => ({ ...item, key: item.localId, pairKey: localPairKey(item.file), sourceLabel: localSourceLabel(item.file), selected: true, reason: '已保存的字幕匹配' })))
const hasLocal = computed(() => localRows.value.some(row => row.file.origin !== 'assrt'))
const rows = computed(() => [...libraryRows.value, ...localRows.value])
const manualRows = reactive(new Set<string>())
let matching = false
const manualRowWatch = watch(() => rows.value.map(row => ({ key: row.key, videoId: row.videoId, selected: row.selected })), (current, previous) => {
  if (matching) return
  const before = new Map(previous.map(row => [row.key, row]))
  for (const row of current) { const old = before.get(row.key); if (old && (old.videoId !== row.videoId || old.selected !== row.selected)) manualRows.add(row.key) }
}, { flush: 'sync' })
const conflicts = computed(() => subtitleOutputConflicts(rows.value))
const selected = computed(() => rows.value.filter(row => row.selected))
const total = computed(() => selected.value.reduce((sum, row) => sum + ('localId' in row ? row.file.data.length : row.file.size), 0))
const picker = ref<HTMLInputElement>(), reading = ref(false), importError = ref(''), notice = ref(''), append = ref(true)
let controller: AbortController | undefined
let loadedVersion = ''
const loadedAssrt = new Set<number>()
const issue = computed(() => {
  const limitIssue = subtitleSelectionIssue(selected.value.length, total.value)
  if (limitIssue) return limitIssue
  const targets = new Set<string>()
  for (const row of selected.value) {
    if (!props.videos.some(video => video.id === row.videoId)) return '请为所选字幕指定对应视频。'
    const marker = row.version || ''
    if (Array.from(marker).length > 30 || /[\\/:*?"<>|\x00-\x1f]/.test(marker) || marker.trim() !== marker || marker.endsWith('.')) return '字幕版本不能包含路径符号、首尾空格或末尾句点。'
    const target = JSON.stringify([row.videoId, subtitleTargetName('', row.file.name, row.language, marker).toLowerCase()])
    if (targets.has(target)) return '多个字幕的目标名称相同，请调整语言、版本或取消勾选。'
    targets.add(target)
    if (/\.idx$/i.test(row.file.name) && !selected.value.some(other => /\.sub$/i.test(other.file.name) && other.pairKey === row.pairKey && other.videoId === row.videoId && other.language === row.language && (other.version || '') === marker)) return 'IDX 需要选择配套 SUB，并关联相同视频、语言和版本。'
  }
  return ''
})
const request = <T,>(path: string) => subtitleRequest<T>(props.token, path, lifecycle.signal)
async function search(initial = false) {
  if (busy.value || assrtBusy.value || reading.value || !query.value.trim()) return
  searched = true; searchError.value = ''; showResults.value = true; versions.value = []
  assrtItems.value = []; assrtMore.value = false; assrtPosition = 0; assrtQuery = query.value.trim()
  await Promise.allSettled([searchLibrary(initial), searchAssrt()])
}
async function searchLibrary(initial: boolean) {
  if (!props.animationLibrary) return
  busy.value = 'search'
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
    libraryRows.value = [...libraryRows.value.filter(row => row.versionId !== item.id), ...data.files.map(file => {
      const match = subtitleMatch(file.name), lang = subtitleLanguage(file.name)
      const saved = libraryRows.value.find(row => row.versionId === item.id && row.path === file.path && row.file.sha === file.sha)
      return { key: `library:${item.id}:${file.path}`, pairKey: libraryPairKey(item.id, file.path), sourceLabel: `字幕库 · ${item.name}`, versionId: item.id, path: file.path, file, language: saved?.language ?? lang, version: saved?.version || '', videoId: saved ? props.videos.some(video => video.id === saved.videoId) ? saved.videoId : '' : match.videoId, selected: existing ? !!saved?.selected : props.kind !== 'movie' && !!match.videoId && lang === 'zh-CN', reason: saved?.reason || match.reason }
    })]
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
  if (item && loadedAssrt.has(item.id) && localRows.value.some(row => row.file.sourceKey === sourceKey)) { notice.value = '这份射手字幕已载入，可在下方调整选择。'; showResults.value = false; return }
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
    signal.throwIfAborted()
    if (!data.files.length) throw new Error('没有找到支持的字幕文件，原列表已保留。')
    if (item) data.files = data.files.map(file => ({ ...file, sourceKey, sourceLabel: `射手网 · ${item.group || item.title}`, origin: 'assrt' }))
    const current = append.value || item ? localRows.value : localRows.value.filter(row => row.file.origin === 'assrt')
    const merged = mergeSubtitleFiles(current.map(row => row.file), data.files, true)
    const kept = localRows.value.filter(row => merged.kept.includes(row.file))
    localRows.value = [...kept, ...merged.added.map(file => {
      const match = subtitleMatch(file.name)
      const localId = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
      return { key: localId, pairKey: localPairKey(file), sourceLabel: localSourceLabel(file), localId, file, videoId: match.videoId, language: subtitleLanguage(file.name), version: '', selected: props.kind !== 'movie' && !!match.videoId, reason: match.reason }
    })]
    showResults.value = false
    if (item) loadedAssrt.add(item.id)
    notice.value = [merged.duplicates ? `已跳过 ${merged.duplicates} 个重复字幕` : '', data.ignored ? `已忽略 ${data.ignored} 个其他文件` : ''].filter(Boolean).join('；')
  } catch (reason) {
    if (lifecycle.signal.aborted) return
    if (controller.signal.aborted) notice.value = '已取消读取，原列表已保留。'
    else importError.value = reason instanceof Error ? reason.message : '读取字幕失败'
  } finally { reading.value = false }
}
function selectFiles(event: Event) { const input = event.target as HTMLInputElement; const files = Array.from(input.files || []); input.value = ''; void read(files) }
function matchUnassigned() {
  matching = true
  try {
    for (const row of rows.value) {
      if (row.videoId || manualRows.has(row.key)) continue
      const match = subtitleMatch(row.file.name)
      row.reason = match.reason
      row.videoId = match.videoId
      if (match.videoId) row.selected = props.kind !== 'movie' && ('localId' in row || row.language === 'zh-CN')
    }
  } finally { matching = false }
}
// 批量应用和撤销不冒充逐项手动编辑，否则撤销后的空映射会被永久保护而无法再次匹配。
function beginBatchEdit() { matching = true; manualRowWatch.pause() }
function endBatchEdit() { try { manualRowWatch.resume() } finally { matching = false } }
function clear() { libraryRows.value = []; localRows.value = []; manualRows.clear(); version.value = undefined; loadedVersion = ''; loadedAssrt.clear(); notice.value = ''; importError.value = ''; showResults.value = true }
function apply() {
  if (reading.value || choosing.value || issue.value) return
  const library: LibrarySubtitleSelection[] = [], local: LocalSubtitleSelection[] = []
  for (const row of selected.value) {
    if ('localId' in row) { const { localId, file, videoId, language, version } = row; local.push({ localId, file, videoId, language, version }) }
    else { const { versionId, path, file, videoId, language, version } = row; library.push({ versionId, path, file, videoId, language, version }) }
  }
  emit('select', library, local, library.length ? version.value : undefined)
}
onMounted(() => { if (showResults.value) void search(true) })
onBeforeUnmount(() => { lifecycle.abort(); controller?.abort() })
</script>

<template>
  <AnimeFloatingDialog ref="dialogRef" :title="batchOpen ? '批量处理字幕' : '选择字幕'" close-label="关闭字幕选择" size="organize" class="subtitle-picker" @close="closeView">
    <div class="picker-content" @dragover.prevent @drop.prevent="!batchOpen && read(Array.from($event.dataTransfer?.files || []))">
      <input ref="picker" type="file" multiple accept=".zip,.rar,.7z,.ass,.ssa,.srt,.vtt,.idx,.sub" class="sr-only" aria-label="选择本地字幕或压缩包" :disabled="reading" @change="selectFiles" />
      <template v-if="!batchOpen">
        <div class="picker-heading"><div><h3>{{ showResults ? '为影片寻找字幕' : version?.name || '已载入字幕' }}</h3><p>{{ title }}<span> · {{ videos.length }} 个视频</span></p></div><button v-if="showResults && rows.length" type="button" class="picker-back" @click="showResults = false"><IconArrowLeft :size="16" aria-hidden="true" />返回列表</button><button v-else-if="!showResults" type="button" class="picker-back" :disabled="reading || choosing" data-subtitle-action="search" @click="openSearch"><IconSearch :size="16" aria-hidden="true" />更换字幕</button></div>
        <div class="picker-actions">
          <form v-if="showResults" class="picker-search" @submit.prevent="search()"><input v-model="query" aria-label="字幕作品搜索词" maxlength="128" placeholder="作品中文名、原名或英文名" /><button type="submit" :disabled="!!busy || assrtBusy || reading || !query.trim()"><IconSearch :size="16" aria-hidden="true" />搜索</button></form>
          <div v-else class="picker-stats"><span>已匹配 <strong>{{ rows.filter(row => row.videoId).length }}</strong> / {{ rows.length }}</span><span v-if="rows.some(row => !row.videoId)">{{ rows.filter(row => !row.videoId).length }} 个待指定</span><a v-if="version" :href="version.url" target="_blank" rel="noopener noreferrer">查看来源</a></div>
          <button type="button" class="picker-import" :disabled="reading || choosing" @click="picker?.click()"><IconUpload :size="16" aria-hidden="true" />导入字幕</button>
        </div>
        <div v-if="kind === 'tv' && targetSeasons.length > 1" class="picker-season"><label>字幕所属季 <SelectMenu v-model="matchingSeason" :options="seasonOptions" label="字幕所属季" :disabled="reading || choosing" @change="matchUnassigned" /></label><p class="picker-note">只为未关联且未手动调整的字幕补充匹配。</p></div>
        <p v-if="busy" role="status" class="picker-note">{{ choosing ? '正在读取字幕文件…' : '正在查找字幕…' }}</p>
        <p v-if="searchError" role="alert" class="picker-error">{{ searchError }}</p>
        <template v-if="showResults">
          <div v-if="!searching && !versions.length && !assrtItems.length" class="picker-empty"><IconUpload :size="32" aria-hidden="true" /><strong>{{ searchError || assrtError ? '可以直接导入本地字幕' : '没有找到匹配字幕' }}</strong><p>试试作品原名，或拖入字幕文件、ZIP / RAR / 7z 压缩包。</p></div>
          <ul class="picker-versions"><li v-for="item in versions" :key="item.id"><button type="button" :disabled="choosing || reading" @click="choose(item)"><strong>{{ item.title }} <small>{{ item.year }}</small><small class="picker-source">字幕库</small></strong><span>{{ item.source }} · {{ item.name }}</span></button></li><li v-for="item in assrtItems" :key="'assrt:' + item.id"><button type="button" :disabled="choosing || reading" @click="read([], item)"><strong>{{ item.title || item.videoName }}<small class="picker-source">射手网</small></strong><span>{{ [item.language, item.group, item.format].filter(Boolean).join(' · ') }}</span><span v-if="item.videoName">{{ item.videoName }}</span></button></li></ul>
          <p v-if="assrtBusy" role="status" class="picker-note">射手网正在搜索…</p><p v-if="assrtError" role="alert" class="picker-note">射手网：{{ assrtError }} <button type="button" :disabled="assrtBusy" @click="searchAssrt(assrtItems.length > 0)">重试</button></p><button v-if="assrtMore" type="button" :disabled="assrtBusy || reading || choosing" @click="searchAssrt(true)">加载更多射手字幕</button>
        </template>
        <p v-if="reading" role="status" class="picker-note">正在读取和解析字幕…<button type="button" @click="controller?.abort()">取消读取</button></p><p v-if="notice" role="status" class="picker-note">{{ notice }}</p><p v-if="importError" role="alert" class="picker-error">{{ importError }}</p>
      </template>
      <SubtitleMatchList v-if="rows.length" v-show="!showResults" ref="matchList" v-model:batch-open="batchOpen" external-footer :rows="rows" :videos="videos" :target-names="targetNames" :kind="kind" :protected-keys="manualRows" :disabled="reading || choosing" @batch-start="beginBatchEdit" @batch-end="endBatchEdit" />
      <details v-if="!batchOpen" class="picker-help"><summary>导入说明与管理</summary><p>支持字幕文件、ZIP / RAR / 7z，可拖入。单个 16 MiB，合计 64 MiB / {{ maxSubtitleFiles }} 个。确认下载整理后上传。</p><p v-if="version?.description" class="picker-description">{{ version.description }}</p><div v-if="hasLocal" class="picker-local"><label><input v-model="append" type="checkbox" :disabled="reading" />继续添加时保留已导入文件</label><button type="button" :disabled="reading" @click="localRows = localRows.filter(row => row.file.origin === 'assrt'); notice = ''; importError = ''">移除本地字幕</button></div><button v-if="rows.length" type="button" :disabled="reading || choosing" @click="clear">移除全部</button></details>
    </div>
    <template #footer>
      <div v-if="batchOpen" class="picker-footer"><span>将修改 <strong>{{ matchList?.batchCount || 0 }}</strong> 项</span><div><button type="button" @click="batchOpen = false">取消</button><button type="button" class="picker-apply" :disabled="!matchList?.batchCanApply" @click="matchList?.applyPending()">应用设置</button></div></div>
      <div v-else-if="showResults" class="picker-footer"><span>{{ rows.length ? '已载入 ' + rows.length + ' 个字幕' : '选择来源或导入文件' }}</span><button v-if="rows.length" type="button" class="picker-apply" @click="showResults = false">返回列表</button><button v-else-if="library.length || local.length" type="button" class="picker-apply" @click="apply">使用字幕</button><button v-else type="button" class="picker-apply" :disabled="reading" @click="picker?.click()">导入字幕</button></div>
      <div v-else class="picker-footer"><div class="picker-selection"><span>已选 <strong>{{ selected.length }}</strong> 个<span class="picker-size"> · {{ formatSubtitleSize(total) }}</span></span><div v-if="issue" class="picker-validation" role="alert"><button v-if="conflicts.length" type="button" @click="matchList?.showConflicts(); dialogRef?.scrollTo(0)">{{ conflicts.length }} 组重名，查看冲突</button><span v-else>{{ issue }}</span></div></div><button type="button" class="picker-apply" :disabled="reading || choosing || !!issue" @click="apply">使用字幕</button></div>
      <p v-if="!batchOpen && (assrtConfigured || assrtItems.length || localRows.some(row => row.file.origin === 'assrt'))" class="picker-attribution">字幕服务由 <a href="https://secure.assrt.net/" target="_blank" rel="noopener noreferrer">assrt.net</a> 提供</p>
    </template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.subtitle-picker { font-size:13px;color:rgb(var(--content-primary)); }
.picker-content { min-width:0;padding-top:6px; }
.picker-heading,.picker-actions,.picker-search,.picker-footer,.picker-footer>div,.picker-local { display:flex;align-items:center;gap:10px; }
.picker-heading { justify-content:space-between;margin-bottom:10px; }.picker-heading>div { min-width:0; }.picker-heading h3 { font-size:20px;font-weight:650;line-height:1.4;overflow-wrap:anywhere; }.picker-heading p { margin-top:4px;color:rgb(var(--content-secondary));font-size:12px; }
button { display:inline-flex;align-items:center;justify-content:center;gap:6px;min-height:36px;border-radius:8px;padding:6px 12px;background:rgb(var(--surface-hover) / .3);font-weight:500; }button:hover { background:rgb(var(--surface-hover) / .6); }button:disabled { opacity:.45;cursor:default; }.picker-back { flex:none;background:transparent;color:rgb(var(--content-secondary)); }
.picker-actions { justify-content:space-between;margin-bottom:8px; }.picker-search { flex:1;min-width:0; }.picker-search input { flex:1;min-width:0;min-height:40px;padding:8px 12px;border:1px solid rgb(var(--line-default));border-radius:9px;background:rgb(var(--surface-card));color:inherit; }.picker-import { flex:none;border:1px solid rgb(var(--line-default));background:transparent; }.picker-stats { display:flex;flex-wrap:wrap;gap:8px 16px;color:rgb(var(--content-secondary));font-size:12px; }.picker-stats strong { color:rgb(var(--content-primary)); }.picker-stats a,a { color:rgb(var(--action-link)); }
.picker-season { margin:12px 0; }.picker-season label { display:flex;align-items:center;gap:8px; }.picker-note { margin:8px 0;color:rgb(var(--content-secondary));font-size:12px;line-height:1.6;overflow-wrap:anywhere; }.picker-note button { margin-left:6px; }
.picker-versions { list-style:none;padding:0;margin:16px 0;display:grid;gap:8px; }.picker-versions button { display:block;width:100%;padding:16px;text-align:left;border:1px solid rgb(var(--line-default));border-radius:12px;background:rgb(var(--surface-card) / .5); }.picker-versions button:hover { border-color:rgb(var(--action-link) / .5);background:rgb(var(--action-primary) / .06); }.picker-versions strong,.picker-versions span { display:block;overflow-wrap:anywhere; }.picker-versions small { font-weight:400;color:rgb(var(--content-secondary)); }.picker-source { display:inline-block;margin-left:8px;padding:2px 6px;border-radius:5px;background:rgb(var(--surface-hover) / .4);font-size:11px; }.picker-versions span { margin-top:5px;color:rgb(var(--content-secondary));font-size:12px; }
.picker-empty { display:flex;flex-direction:column;align-items:center;gap:12px;padding:48px 20px;text-align:center;border:1px dashed rgb(var(--line-default));border-radius:14px; }.picker-empty svg,.picker-empty p { color:rgb(var(--content-secondary)); }.picker-empty p { font-size:12px;line-height:1.7; }
.picker-footer { justify-content:space-between;min-width:0; }.picker-footer>span { color:rgb(var(--content-secondary));font-size:12px; }.picker-footer strong { color:rgb(var(--content-primary)); }.picker-apply { background:rgb(var(--action-primary));color:#fff;min-width:110px; }.picker-apply:hover { background:rgb(var(--action-primary-hover)); }
.picker-selection { display:flex;flex-direction:column;align-items:flex-start!important;gap:2px!important;min-width:0;color:rgb(var(--content-secondary));font-size:12px; }.picker-selection .picker-validation { margin-top:0; }.picker-selection .picker-validation button { min-height:20px;font-size:11px; }
.picker-validation { display:flex;align-items:center;gap:8px;justify-content:space-between;color:rgb(var(--status-warning));font-size:12px;margin-top:6px; }.picker-validation>span { min-width:0;overflow-wrap:anywhere; }.picker-validation button { flex:none;min-height:28px;background:transparent;padding:2px 0; }.picker-error { color:rgb(var(--status-danger));margin-top:8px;font-size:12px;overflow-wrap:anywhere; }
.picker-help { margin-top:18px;border-top:1px solid rgb(var(--line-default));padding-top:12px;color:rgb(var(--content-secondary));font-size:12px;line-height:1.7; }.picker-help summary { cursor:pointer;width:fit-content; }.picker-help p,.picker-help button { margin-top:8px; }.picker-description { white-space:pre-wrap;overflow-wrap:anywhere; }.picker-local { flex-wrap:wrap;margin-top:8px; }.picker-local label { display:flex;align-items:center;gap:6px; }.picker-attribution { color:rgb(var(--content-secondary));font-size:11px;margin-top:5px; }input { accent-color:rgb(var(--action-primary)); }
@media(max-width:639px) { button,.picker-search input { min-height:44px; }.picker-search input { font-size:16px; }.picker-heading { align-items:flex-start;margin-bottom:12px; }.picker-heading h3 { font-size:17px; }.picker-heading .picker-back { font-size:12px;padding:6px 2px; }.picker-heading p span { display:block; }.picker-actions { flex-wrap:wrap; }.picker-search { flex-basis:100%; }.picker-search button { padding:6px 8px; }.picker-import { margin-left:auto; }.picker-stats { flex:1; }.picker-footer { gap:8px; }.picker-footer>div { gap:6px; }.picker-size { display:none; }.picker-apply { min-width:96px;padding:6px 12px; }.picker-validation { font-size:11px; }.picker-selection .picker-validation button { min-height:44px;text-align:left;justify-content:flex-start; }.picker-validation button { min-height:44px; }.picker-empty { padding:32px 12px; } }
</style>
