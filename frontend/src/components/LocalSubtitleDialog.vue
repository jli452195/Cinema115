<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { IconUpload } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import SubtitleMatchList from './SubtitleMatchList.vue'
import { analyzeSubtitle, readLocalSubtitles, subtitleLanguage, subtitlePairKey, type LocalSubtitleSelection, type SubtitleMatchRow } from '../utils/subtitleImport'
import { formatSubtitleSize, mergeSubtitleFiles } from '../utils/subtitleSources'
import { subtitleTargetName } from '../utils/subtitleLibrary'
import type { OrganizeRow } from '../types/manualOrganize'

const props = defineProps<{ videos: OrganizeRow[]; targetNames: Record<string, string>; selections: LocalSubtitleSelection[] }>()
const emit = defineEmits<{ close: []; select: [items: LocalSubtitleSelection[]] }>()
const lifecycle = new AbortController()
const rows = ref<(LocalSubtitleSelection & SubtitleMatchRow)[]>(props.selections.map(item => ({ ...item, key: item.localId, pairKey: pairKey(item.file), selected: true, reason: '已保存的字幕匹配' })))
const picker = ref<HTMLInputElement>(), reading = ref(false), error = ref(''), notice = ref('')
const append = ref(true)
let controller: AbortController | undefined
const selected = computed(() => rows.value.filter(row => row.selected))
const total = computed(() => selected.value.reduce((sum, row) => sum + row.file.data.length, 0))
const issue = computed(() => {
  const targets = new Set<string>()
  for (const row of selected.value) {
    if (!props.videos.some(video => video.id === row.videoId)) return '请为所选字幕指定对应视频。'
    if (/[\\/:*?"<>|\x00-\x1f]/.test(row.version) || row.version.trim() !== row.version || row.version.endsWith('.')) return '字幕版本不能包含路径符号、首尾空格或末尾句点。'
    const target = `${row.videoId}/${subtitleTargetName('', row.file.name, row.language, row.version)}`.toLowerCase()
    if (targets.has(target)) return '多个字幕的目标名称相同，请调整语言、版本或取消勾选。'
    targets.add(target)
    if (/\.idx$/i.test(row.file.name) && !selected.value.some(other => /\.sub$/i.test(other.file.name) && other.pairKey === row.pairKey && other.videoId === row.videoId && other.language === row.language && other.version === row.version)) return 'IDX 需要选择配套 SUB，并关联相同视频、语言和版本。'
  }
  return ''
})
function pairKey(file: LocalSubtitleSelection['file']) { const key = subtitlePairKey(file.name); return key ? `${file.sourceKey || 'local'}/${key}` : '' }
async function read(files: File[]) {
  if (reading.value || !files.length) return
  reading.value = true; error.value = ''; controller = new AbortController()
  try {
    const data = await readLocalSubtitles(files, AbortSignal.any([lifecycle.signal, controller.signal]))
    if (!data.files.length) throw new Error('没有找到支持的字幕文件，原列表已保留。')
    const merged = mergeSubtitleFiles(rows.value.map(row => row.file), data.files, append.value)
    const kept = rows.value.filter(row => merged.kept.includes(row.file))
    const videos = props.videos.map(video => ({ ...video, path: video.sourcePath }))
    rows.value = [...kept, ...merged.added.map(file => {
      const match = analyzeSubtitle(file.name, videos, props.videos[0]?.season ?? 1)
      const localId = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
      return { key: localId, pairKey: pairKey(file), localId, file, videoId: match.videoId, language: subtitleLanguage(file.name), version: '', selected: !!match.videoId, reason: match.reason }
    })]
    notice.value = `共 ${rows.value.length} 个字幕，${rows.value.filter(row => !row.videoId).length} 个未匹配，默认不选${merged.duplicates ? `；已跳过 ${merged.duplicates} 个重复字幕` : ''}${data.ignored ? `；已忽略 ${data.ignored} 个其他文件` : ''}。`
  } catch (reason) {
    if (lifecycle.signal.aborted) return
    if (controller.signal.aborted) notice.value = '已取消读取，原列表已保留。'
    else error.value = reason instanceof Error ? reason.message : '读取字幕失败'
  } finally { reading.value = false }
}
function choose(event: Event) { const input = event.target as HTMLInputElement; const files = Array.from(input.files || []); input.value = ''; void read(files) }
function apply() { if (!reading.value && !issue.value) emit('select', selected.value.map(({ localId, file, videoId, language, version }) => ({ localId, file, videoId, language, version }))) }
onBeforeUnmount(() => { lifecycle.abort(); controller?.abort() })
</script>

<template>
  <AnimeFloatingDialog title="导入本地字幕" close-label="关闭本地字幕导入" size="config" class="local-subtitle-dialog" @close="emit('close')">
    <div class="import-area" @dragover.prevent @drop.prevent="read(Array.from($event.dataTransfer?.files || []))">
      <input ref="picker" type="file" multiple accept=".zip,.rar,.7z,.ass,.ssa,.srt,.vtt,.idx,.sub" class="sr-only" aria-label="选择本地字幕或压缩包" :disabled="reading" @change="choose" />
      <button type="button" :disabled="reading" @click="picker?.click()"><IconUpload :size="17" aria-hidden="true" />选择字幕或压缩包</button>
      <p>支持 ZIP / RAR / 7z，或多选解压后的字幕，也可拖入文件。字体和说明文件会忽略。</p>
      <p>每次最多 100 个字幕，单个 16 MiB、合计 64 MiB；加密或分卷包请先解压。</p>
    </div>
    <div v-if="rows.length" class="import-mode"><label><input v-model="append" type="checkbox" :disabled="reading" />继续添加时保留当前列表</label><button type="button" :disabled="reading" @click="rows = []; notice = ''">移除全部</button></div>
    <div v-if="reading" role="status" class="import-note">正在本地读取和解析…<button type="button" @click="controller?.abort()">取消读取</button></div>
    <p v-if="notice" role="status" class="import-note">{{ notice }}</p>
    <p v-if="error" role="alert" class="import-error">{{ error }}</p>
    <SubtitleMatchList :rows="rows" :videos="videos" :target-names="targetNames" editable-version :disabled="reading" />
    <template #footer><div class="import-footer"><span>已选 {{ selected.length }} 个 · {{ formatSubtitleSize(total) }}</span><button type="button" :disabled="reading || !!issue" @click="apply">使用所选字幕</button></div><p v-if="issue" role="alert" class="import-error">{{ issue }}</p><p class="import-note">确认下载整理时上传所选字幕，视频整理后自动跟随命名。</p></template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.local-subtitle-dialog { font-size:13px; }
.import-area { padding:16px;border-radius:12px;background:rgb(59 130 246 / .06); }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }
.import-area button { display:inline-flex;align-items:center;gap:8px;color:#2563eb;background:rgb(59 130 246 / .1); }
.import-area p,.import-note { margin-top:8px;font-size:12px;line-height:1.6;color:var(--floating-surface-muted,#64748b);overflow-wrap:anywhere; }
.import-mode,.import-footer { display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px;margin-top:12px; }
.import-mode label { display:flex;align-items:center;gap:6px;font-size:12px; }
input { accent-color:#2563eb; }
.import-footer button { background:#2563eb;color:#fff; }
.import-error { margin-top:8px;font-size:12px;color:#dc2626; }
button:disabled { opacity:.45;cursor:default; }
:global(.dark) .import-area button { color:#93c5fd; }
@media(max-width:639px) { button { min-height:44px; } }
</style>
