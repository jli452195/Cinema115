<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowLeft, IconSearch } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import { analyzeSubtitle, maxTotalBytes, subtitlePairKey } from '../utils/subtitleImport'
import { libraryLanguage, subtitleLanguages, subtitleTargetName, type LibrarySubtitleFile, type LibrarySubtitleSelection, type SubtitleVersion } from '../utils/subtitleLibrary'
import type { OrganizeRow } from '../types/manualOrganize'
import type { SubtitleVideo } from '../types/subtitleImport'

const props = defineProps<{ token: string; title: string; originalTitle?: string; videos: OrganizeRow[]; targetNames: Record<string, string>; selections?: LibrarySubtitleSelection[] }>()
const emit = defineEmits<{ close: []; select: [items: LibrarySubtitleSelection[], version: SubtitleVersion] }>()
const lifecycle = new AbortController()
const query = ref(props.title), busy = ref(false), error = ref(''), versions = ref<SubtitleVersion[]>([]), version = ref<SubtitleVersion>()
const language = ref('all')
const rows = ref<(LibrarySubtitleSelection & { selected: boolean; reason: string })[]>([])
const selected = computed(() => rows.value.filter(row => row.selected))
const visible = computed(() => rows.value.filter(row => language.value === 'all' || row.language === language.value))
const availableLanguages = computed(() => subtitleLanguages.filter(lang => rows.value.some(row => row.language === lang.value)))
const oversized = computed(() => selected.value.length > 100 || selected.value.reduce((sum, row) => sum + row.file.size, 0) > maxTotalBytes)
const badSelection = computed(() => !selected.value.length || oversized.value || selected.value.some(row => !row.videoId) || new Set(selected.value.map(row => `${row.videoId}/${subtitleTargetName('', row.file.name, row.language)}`.toLowerCase())).size !== selected.value.length)
async function request<T>(path: string): Promise<T> {
  const response = await fetch(`/api/admin/subtitles/${path}`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || '读取字幕库失败')
  return data
}
async function search(initial = false) {
  if (busy.value || !query.value.trim()) return
  busy.value = true; error.value = ''
  try {
    const params = new URLSearchParams(); params.append('q', query.value.trim())
    if (initial && props.originalTitle && props.originalTitle !== query.value.trim()) params.append('q', props.originalTitle)
    versions.value = (await request<{ versions: SubtitleVersion[] }>(`search?${params}`)).versions
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '搜索失败' }
  finally { busy.value = false }
}
async function choose(item: SubtitleVersion) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const data = await request<{ files: LibrarySubtitleFile[] }>(`versions/${encodeURIComponent(item.id)}/files`)
    const videos: SubtitleVideo[] = props.videos.map(video => ({ ...video, path: video.sourcePath }))
    rows.value = data.files.map(file => {
      const match = analyzeSubtitle(file.name, videos, props.videos[0]?.season ?? 1), lang = libraryLanguage(file.name)
      const saved = props.selections?.find(row => row.versionId === item.id && row.path === file.path && row.file.sha === file.sha)
      const existing = props.selections?.some(row => row.versionId === item.id)
      return { versionId: item.id, path: file.path, file, language: saved?.language ?? lang, videoId: saved && videos.some(video => video.id === saved.videoId) ? saved.videoId : match.videoId, selected: existing ? !!saved : !!match.videoId && lang === 'zh-CN', reason: match.reason }
    })
    version.value = item; language.value = rows.value.some(row => row.language === 'zh-CN') ? 'zh-CN' : 'all'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取版本失败' }
  finally { busy.value = false }
}
function pair(row: typeof rows.value[number]) {
  const key = subtitlePairKey(row.file.path)
  if (!key) return
  for (const other of rows.value) if (other !== row && subtitlePairKey(other.file.path) === key) Object.assign(other, { videoId: row.videoId, language: row.language, selected: row.selected })
}
function selectVisible() { for (const row of visible.value) { row.selected = !!row.videoId; pair(row) } }
function apply() { if (!badSelection.value && version.value) emit('select', selected.value.map(({ selected, reason, ...item }) => item), version.value) }
onMounted(() => search(true))
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <AnimeFloatingDialog title="查找字幕" close-label="关闭字幕库" size="config" class="subtitle-library" @close="emit('close')">
    <template v-if="!version">
      <form class="library-search" @submit.prevent="search()"><input v-model="query" aria-label="字幕作品搜索词" maxlength="128" placeholder="作品中文名、原名或英文名" /><button type="submit" :disabled="busy || !query.trim()"><IconSearch :size="16" aria-hidden="true" />搜索</button></form>
      <p class="library-note">字幕共享计划 · 按作品和片源选择，BD / WEB / TV 版本的时间轴可能不同。</p>
      <p v-if="busy" role="status" class="library-note">正在读取字幕索引…</p>
      <p v-else-if="!versions.length && !error" class="library-note">没有找到匹配作品，可以换用原名或英文名搜索。</p>
      <ul class="library-versions"><li v-for="item in versions" :key="item.id"><button type="button" :disabled="busy" @click="choose(item)"><strong>{{ item.title }} <small>{{ item.year }}</small></strong><span>{{ item.source }} · {{ item.name }}</span></button></li></ul>
    </template>
    <template v-else>
      <button type="button" class="library-back" @click="version = undefined; error = ''"><IconArrowLeft :size="15" aria-hidden="true" />返回作品与版本</button>
      <p class="library-heading">{{ version.title }} · {{ version.year }}</p><p class="library-note">{{ version.source }} · {{ version.name }}</p>
      <details v-if="version.description" class="library-note"><summary>版本说明</summary><p class="whitespace-pre-wrap break-words">{{ version.description }}</p></details>
      <div class="library-filters"><label>语言 <select v-model="language" aria-label="筛选字幕语言"><option value="all">全部</option><option v-for="lang in availableLanguages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select></label><button type="button" @click="selectVisible">选择当前已匹配项</button><button type="button" @click="rows.forEach(row => row.selected = false)">清空选择</button></div>
      <ul class="library-files"><li v-for="row in visible" :key="row.path">
        <label class="library-file-name"><input v-model="row.selected" type="checkbox" :aria-label="`选择字幕 ${row.file.name}`" @change="pair(row)" /><span>{{ row.file.name }}</span></label>
        <div class="library-mapping"><select v-model="row.videoId" :aria-label="`${row.file.name} 对应视频`" @change="pair(row)"><option value="">请选择对应视频</option><option v-for="video in videos" :key="video.id" :value="video.id">{{ video.name }}</option></select><select v-model="row.language" :aria-label="`${row.file.name} 语言`" @change="pair(row)"><option v-for="lang in subtitleLanguages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select></div>
        <p v-if="row.videoId" class="library-target">整理后：{{ subtitleTargetName(targetNames[row.videoId] || videos.find(v => v.id === row.videoId)?.name || '', row.file.name, row.language) }}</p><p v-else class="library-note">{{ row.reason }}</p>
      </li></ul>
    </template>
    <p v-if="error" role="alert" class="library-error">{{ error }}</p>
    <template #footer><div class="library-footer"><span>{{ version ? `已选 ${selected.length} 个字幕` : '来源：foxofice/sub_share' }}</span><a v-if="version" :href="version.url" target="_blank" rel="noopener noreferrer">查看来源</a><button v-if="version" type="button" :disabled="badSelection" @click="apply">使用所选字幕</button></div><p v-if="version && selected.length && badSelection" class="library-error">{{ oversized ? '每次最多选择 100 个字幕，总大小不超过 64 MiB。' : '请为所选字幕指定视频，且同一视频的同语言字幕不能重复。' }}</p></template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.subtitle-library { font-size: 13px; }
.library-search,.library-filters,.library-footer,.library-mapping { display:flex;align-items:center;gap:8px; }
.library-search input { flex:1;min-width:0; }
input:not([type="checkbox"]),select { min-height:36px;padding:6px 10px;border:1px solid rgb(148 163 184 / .25);border-radius:9px;background:var(--floating-surface-container,#fff);color:inherit; }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }
button:disabled { opacity:.45;cursor:default; }
.library-search button,.library-back { display:inline-flex;align-items:center;gap:6px; }
.library-note { margin:8px 0;color:var(--floating-surface-muted,#64748b);font-size:12px;line-height:1.6; }
.library-versions,.library-files { list-style:none;margin:0;padding:0; }
.library-versions li,.library-files li { border-bottom:1px solid rgb(148 163 184 / .14); }
.library-versions button { width:100%;padding:12px 6px;text-align:left;background:none; }
.library-versions button:hover { background:rgb(59 130 246 / .07); }
.library-versions strong,.library-versions span { display:block;overflow-wrap:anywhere; }
.library-versions small { font-weight:400;color:var(--floating-surface-muted); }
.library-versions span { margin-top:4px;color:var(--floating-surface-muted);font-size:12px; }
.library-heading { margin-top:12px;font-weight:600; }
.library-filters { flex-wrap:wrap;margin:12px 0; }
.library-files li { padding:12px 0; }
.library-file-name { display:flex;align-items:flex-start;gap:8px;overflow-wrap:anywhere; }
.library-file-name input { flex:none;margin-top:3px;accent-color:#2563eb; }
.library-mapping { margin:8px 0 0 23px; }
.library-mapping select:first-child { flex:1;min-width:0; }
.library-target { margin:6px 0 0 23px;font-size:12px;color:var(--floating-surface-muted);overflow-wrap:anywhere; }
.library-footer { justify-content:space-between;flex-wrap:wrap; }
.library-footer button { background:#2563eb;color:#fff; }
.library-footer a { color:#2563eb;font-size:12px; }
.library-error { color:#dc2626;margin-top:8px;font-size:12px; }
@media(max-width:639px) { input:not([type="checkbox"]),select,button { min-height:44px; } .library-search input,.library-mapping select { font-size:16px; } .library-mapping { flex-wrap:wrap; } .library-mapping select:first-child { flex-basis:100%; } }
</style>
