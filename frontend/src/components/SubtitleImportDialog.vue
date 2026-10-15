<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { p115Request } from '../lib/p115-api'
import { analyzeSubtitle, episodeNumber, readLocalSubtitles, subtitleLanguage, subtitlePairKey } from '../utils/subtitleImport'
import type { LocalSubtitle, SubtitleMatch } from '../utils/subtitleImport'
import type { OrganizeJob } from '../types/manualOrganize'
import type { SubtitleImport, SubtitleVideo } from '../types/subtitleImport'
import SubtitleSourceDialog from './SubtitleSourceDialog.vue'
import { formatSubtitleSize, mergeSubtitleFiles, readDriveSubtitles, type SubtitleSourceFile } from '../utils/subtitleSources'

const props = defineProps<{ token: string; job: OrganizeJob; accountName: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()
interface Row extends LocalSubtitle { id: string; selected: boolean; videoId: string; language: string; version: string; match: SubtitleMatch; manual: boolean; pending: boolean }
const dialog = ref<HTMLDialogElement>()
const picker = ref<HTMLInputElement>()
const sourceDialog = ref(false)
const importMode = ref<'append' | 'replace'>('append')
const reading = ref(false)
let readController: AbortController | undefined, nextRowId = 0
const lifecycle = new AbortController()
const videos = ref<SubtitleVideo[]>([])
const rows = ref<Row[]>([])
const busy = ref('')
const error = ref('')
const notice = ref('')
const season = ref(1)
const firstEpisode = ref(1)
const language = ref('zh-CN')
const filter = ref('all')
const plan = ref<SubtitleImport>()
const result = ref<SubtitleImport>()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
const selected = computed(() => rows.value.filter(row => row.selected))
const unmatched = computed(() => selected.value.filter(row => !row.videoId).length)
const review = computed(() => selected.value.filter(row => row.pending).length)
const high = computed(() => selected.value.filter(row => !row.manual && row.videoId && row.match.confidence === 'high').length)
const confidenceLabel = { high: '高置信度', medium: '中置信度', low: '低置信度', none: '未匹配' }
const conflicts = computed(() => {
  const found = new Map<string, string>()
  const names = new Map<string, Row[]>()
  for (const row of selected.value) {
    if (/\.idx$/i.test(row.name)) {
      const partners = pairRows(row)
      const partner = partners.find(other => /\.sub$/i.test(other.name))
      if (!partner || !partner.selected || partner.videoId !== row.videoId || partner.language !== row.language || partner.version !== row.version) for (const item of partners) found.set(item.id, 'IDX / SUB 配对不完整或设置不一致')
    }
    if (!row.videoId) continue
    const key = `${row.videoId}/${row.version.toLowerCase()}/${row.language}/${row.name.split('.').pop()?.toLowerCase()}`
    names.set(key, [...(names.get(key) || []), row])
  }
  for (const group of names.values()) if (group.length > 1) for (const row of group) found.set(row.id, '多个字幕的目标同名，请调整语言、版本或取消勾选')
  return found
})
const displayed = computed(() => rows.value.filter(row => filter.value === 'all' || filter.value === 'unmatched' && !row.videoId || filter.value === 'review' && row.pending || filter.value === 'conflict' && (conflicts.value.has(row.id) || !row.videoId && row.match.candidates.length > 1)))
const languages = [{ value: '', label: '未知 / 不标记' }, { value: 'zh-CN', label: '简体中文' }, { value: 'zh-TW', label: '繁体中文' }, { value: 'en', label: '英文' }, { value: 'zh-en', label: '中英双语' }, { value: 'zh', label: '中文' }]
const request = <T,>(path: string, init: RequestInit = {}, timeout = 130000) => p115Request<T>(props.token, `manual-organize/subtitles${path}`, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(timeout)]) })
const message = (reason: unknown) => reason instanceof Error ? reason.message : '字幕操作失败'

async function work(label: string, action: () => Promise<void>) {
  if (busy.value) return
  busy.value = label; error.value = ''
  try { await action() } catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { busy.value = '' }
}
async function loadVideos() {
  const data = await request<{ videos: SubtitleVideo[] }>(`/videos?accountId=${encodeURIComponent(props.job.config.accountId)}&jobId=${encodeURIComponent(props.job.id)}`)
  videos.value = data.videos
  season.value = data.videos.map(video => episodeNumber(video.name)?.season).find(number => number !== undefined) ?? 1
}
function applyFiles(data: { files: LocalSubtitle[]; ignored: number }) {
  if (!data.files.length) throw new Error(`未找到支持的字幕文件${data.ignored ? `，已忽略 ${data.ignored} 个其他文件` : ''}。原有列表已保留。`)
  const merged = mergeSubtitleFiles(rows.value, data.files, importMode.value === 'append')
  plan.value = undefined; result.value = undefined
  filter.value = 'all'
  const added: Row[] = merged.added.sort((a, b) => a.name.localeCompare(b.name, 'zh-CN', { numeric: true })).map(file => {
    const match = analyzeSubtitle(file.name, videos.value, season.value)
    return { ...file, id: String(++nextRowId), selected: true, videoId: match.videoId, language: subtitleLanguage(file.name), version: '', match, manual: false, pending: match.confidence === 'medium' || match.confidence === 'low' }
  })
  rows.value = [...merged.kept, ...added]
  notice.value = `新增 ${added.length} 个字幕，当前共 ${rows.value.length} 个${merged.duplicates ? `，跳过 ${merged.duplicates} 个重复字幕` : ''}${data.ignored ? `，已忽略 ${data.ignored} 个其他文件（包括字体、图片等）` : ''}。请核对视频、语言和版本。`
}
async function startRead(action: (signal: AbortSignal) => Promise<{ files: LocalSubtitle[]; ignored: number }>) {
  if (busy.value) return
  await work('正在读取字幕文件或压缩包', async () => {
    reading.value = true; readController = new AbortController()
    const signal = AbortSignal.any([lifecycle.signal, readController.signal])
    try {
      const data = await action(signal)
      signal.throwIfAborted()
      applyFiles(data)
    } catch (reason) {
      if (readController.signal.aborted && !lifecycle.signal.aborted) notice.value = '已取消读取，原有字幕列表和人工匹配已保留。'
      else throw reason
    } finally { reading.value = false; readController = undefined }
  })
}
function selectDriveFiles(files: SubtitleSourceFile[]) {
  sourceDialog.value = false
  void startRead(signal => readDriveSubtitles(props.token, props.job.config.accountId, props.job.id, files, signal, progress => {
    if (progress.phase === 'prepare') busy.value = '正在核验网盘来源文件'
    else if (progress.phase === 'extract') busy.value = `正在本地解析 ${progress.index}/${progress.count}：${progress.name}`
    else busy.value = `正在下载 ${progress.index}/${progress.count}：${progress.name} · ${formatSubtitleSize(progress.loaded)} / ${formatSubtitleSize(progress.total)}`
  }))
}
function chooseFiles(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (files.length) void startRead(signal => readLocalSubtitles(files, signal))
}
function dropFiles(event: DragEvent) {
  if (busy.value || !videos.value.length) return
  const files = Array.from(event.dataTransfer?.files || [])
  if (files.length) void startRead(signal => readLocalSubtitles(files, signal))
}
function pairRows(row: Row) {
  const key = subtitlePairKey(row.name)
  return key ? rows.value.filter(other => other.sourceKey === row.sourceKey && subtitlePairKey(other.name) === key) : [row]
}
function syncPair(row: Row) { for (const other of pairRows(row)) { other.selected = row.selected; other.videoId = row.videoId; other.language = row.language; other.version = row.version; other.manual = row.manual; other.pending = row.pending; other.match = { ...row.match } } }
function manualMatch(row: Row) {
  row.manual = true; row.pending = false
  row.match = { videoId: row.videoId, confidence: row.videoId ? 'high' : 'none', reason: row.videoId ? '人工指定，自动匹配会保留此选择' : '已手动清空，可点击重新自动匹配', candidates: [] }
  syncPair(row)
}
function matchAll() {
  for (const row of rows.value) if (!row.manual) {
    row.match = analyzeSubtitle(row.name, videos.value, season.value)
    row.videoId = row.match.videoId; row.pending = ['medium', 'low'].includes(row.match.confidence)
  }
  notice.value = '已更新自动匹配结果，人工指定和已确认的选择保持不变。'
}
function resetMatch(row: Row) {
  for (const other of pairRows(row)) { other.manual = false; other.match = analyzeSubtitle(other.name, videos.value, season.value); other.videoId = other.match.videoId; other.pending = ['medium', 'low'].includes(other.match.confidence) }
}
function acceptSuggestion(row: Row, id: string) { row.videoId = id; manualMatch(row) }
function confirmOrder() { for (const row of selected.value) if (row.pending && row.videoId) manualMatch(row) }
function matchInOrder() {
  let episode = firstEpisode.value
  const groups = new Map<string, number>()
  for (const row of selected.value) {
    // Preserve IDX/SUB pairs as a single episode when numbering in order.
    const key = subtitlePairKey(row.name) ? `${row.sourceKey || ''}/${subtitlePairKey(row.name)}` : row.id
    const number = groups.get(key) ?? episode++
    groups.set(key, number)
    if (row.manual) continue
    const candidates = videos.value.filter(video => { const ep = episodeNumber(video.name); return ep && (ep.season ?? season.value) === season.value && ep.episode === number })
    row.videoId = candidates.length === 1 ? candidates[0].id : ''
    row.pending = !!row.videoId || candidates.length > 1
    row.match = { videoId: '', confidence: candidates.length ? 'low' : 'none', reason: `按当前顺序推测 S${season.value}E${number}，${candidates.length === 1 ? '请核对后确认' : candidates.length > 1 ? '存在多个版本，请手动选择' : '没有对应视频'}`, candidates: candidates.length > 1 ? candidates.map(video => ({ id: video.id, score: 0, reason: '顺序推测候选' })) : [] }
  }
  notice.value = '已生成顺序建议，未覆盖人工选择。请核对每行视频后点击“确认已勾选的顺序建议”。'
}
async function previewImport() {
  if (unmatched.value || review.value || conflicts.value.size) throw new Error('请先处理未匹配、待确认和冲突项。')
  result.value = undefined
  plan.value = await request<SubtitleImport>('/preview', { method: 'POST', body: JSON.stringify({ accountId: props.job.config.accountId, jobId: props.job.id, files: selected.value.map(row => ({ localId: row.id, originalName: row.name, videoId: row.videoId, language: row.language, version: row.version, size: row.data.length, sha256: row.sha256 })) }) })
}
async function upload() {
  const confirmed = plan.value
  if (!confirmed) return
  const form = new FormData()
  form.append('accountId', confirmed.accountId); form.append('previewId', confirmed.id)
  for (const item of confirmed.items) {
    const row = rows.value.find(row => row.id === item.localId)
    if (!row) throw new Error('本地字幕已变化，请重新选择。')
    form.append(item.localId, new Blob([new Uint8Array(row.data)]), item.name)
  }
  // A lost response must not leave an active upload button that can blindly retry.
  plan.value = undefined
  try { result.value = await request<SubtitleImport>('/import', { method: 'POST', body: form }, 310000) }
  catch (reason) { throw new Error(`${message(reason)}。请关闭窗口查看字幕导入记录，并核实目标目录。`) }
  finally { emit('changed') }
}
watch(rows, () => { plan.value = undefined }, { deep: true })
watch(season, matchAll)
onMounted(() => {
  document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  void work('读取已整理的视频', loadVideos)
})
onBeforeUnmount(() => {
  readController?.abort()
  lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="subtitle-title" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-5xl overflow-y-auto overscroll-contain rounded-2xl bg-white dark:bg-slate-900 p-5 text-slate-700 dark:text-slate-300 shadow-2xl backdrop:bg-slate-950/50" @cancel.prevent="!busy && !sourceDialog && emit('close')">
      <header class="flex items-start justify-between gap-3"><div class="min-w-0"><h2 id="subtitle-title" class="font-semibold text-slate-900 dark:text-slate-100">补充字幕 · {{ job.title }}</h2><p class="mt-2 break-all text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">账号：{{ accountName }} · {{ job.config.targetPath }}</p></div><button :disabled="!!busy" aria-label="关闭字幕导入" class="shrink-0 rounded-lg p-2 disabled:opacity-40" @click="emit('close')"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button></header>
      <div v-if="busy" class="mt-3 flex items-start justify-between gap-3"><p role="status" class="min-w-0 break-all text-sm text-blue-700 dark:text-blue-400">{{ busy }}…</p><button v-if="reading" class="secondary shrink-0" @click="readController?.abort()">取消读取</button></div>
      <p v-if="error" role="alert" class="mt-3 break-all rounded-xl bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <button v-if="!videos.length && !busy" class="secondary mt-3" @click="work('读取视频', loadVideos)">重新读取视频</button>
      <fieldset :disabled="!!busy || !videos.length" class="mt-4 min-w-0 space-y-4 disabled:opacity-60">
        <div class="rounded-xl border-2 border-dashed border-blue-200 bg-blue-50 p-5 text-center" @dragover.prevent @drop.prevent="dropFiles">
          <p class="text-sm">拖入字幕压缩包（ZIP / RAR / 7z）或字幕文件</p>
          <p class="mt-2 text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">压缩包仅在本地解析，确认后才上传字幕。支持 SRT / ASS / SSA / VTT / IDX / SUB；不支持加密和分卷。单个字幕 16 MiB，合计 64 MiB，最多 100 个。</p>
          <input ref="picker" type="file" multiple accept=".zip,.rar,.7z,.srt,.ass,.ssa,.vtt,.idx,.sub" aria-label="选择字幕文件或压缩包" class="sr-only" @change="chooseFiles" />
          <div class="mt-3 flex flex-wrap justify-center gap-2"><button class="secondary" @click="picker?.click()">选择本地字幕 / 压缩包</button><button class="secondary" @click="sourceDialog = true">从 115 网盘选择</button></div>
          <div v-if="rows.length" class="mt-3 flex flex-wrap justify-center gap-3 text-xs"><span>新读取字幕：</span><label class="flex items-center gap-1"><input v-model="importMode" type="radio" value="append" name="subtitle-import-mode" />追加到列表</label><label class="flex items-center gap-1"><input v-model="importMode" type="radio" value="replace" name="subtitle-import-mode" />替换当前列表</label></div>
        </div>
        <p v-if="notice" role="status" class="text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ notice }}</p>
        <template v-if="rows.length">
          <div class="flex flex-wrap items-center gap-2 text-xs">
            <button class="secondary" @click="rows.forEach(row => row.selected = true)">全选字幕</button><button class="secondary" @click="rows.forEach(row => row.selected = false)">全不选字幕</button>
            <label>默认季 <input v-model.number="season" aria-label="字幕默认季" type="number" min="0" max="1000" class="input w-20" /></label>
            <button class="secondary" @click="matchAll">自动匹配字幕</button>
            <label>起始集 <input v-model.number="firstEpisode" aria-label="字幕起始集" type="number" min="1" max="10000" class="input w-20" /></label><button class="secondary" :disabled="firstEpisode < 1" @click="matchInOrder">按当前顺序匹配</button>
            <select v-model="language" aria-label="批量字幕语言" class="input"><option v-for="lang in languages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select><button class="secondary" @click="selected.forEach(row => { row.language = language; syncPair(row) })">应用到勾选字幕</button>
          </div>
          <p role="status" class="text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500">已勾选 {{ selected.length }} · 自动匹配 {{ high }} · 人工确认 {{ selected.filter(row => row.manual && row.videoId).length }} · 待确认 {{ review }} · 未匹配 {{ unmatched }} · 同名或配对冲突 {{ conflicts.size }}</p>
          <div class="flex flex-wrap items-center gap-2 text-xs">
            <label>显示 <select v-model="filter" aria-label="字幕匹配筛选" class="input"><option value="all">全部字幕</option><option value="unmatched">只看未匹配</option><option value="review">只看待确认</option><option value="conflict">只看冲突项</option></select></label>
            <button v-if="selected.some(row => row.pending && row.videoId)" class="secondary" @click="confirmOrder">确认已勾选的顺序建议</button>
            <span class="text-slate-500 dark:text-slate-400 dark:text-slate-500">筛选不改变勾选。置信度只表示文件对应关系，不代表时间轴匹配。</span>
          </div>
          <div class="max-h-80 space-y-3 overflow-y-auto">
            <div v-for="row in displayed" :key="row.id" class="rounded-xl border border-slate-200 dark:border-slate-700 p-3">
              <label class="flex items-start gap-2 text-sm"><input v-model="row.selected" type="checkbox" :aria-label="`导入 ${row.name}`" class="mt-1" @change="syncPair(row)" /><span class="min-w-0 break-all">{{ row.name }} <span class="text-xs text-slate-400 dark:text-slate-500">{{ (row.data.length / 1024).toFixed(1) }} KiB</span></span></label>
              <p v-if="row.sourceLabel" class="mt-1 break-all text-xs text-slate-500 dark:text-slate-400">来源：{{ row.sourceLabel }}</p>
              <p class="mt-2 break-all text-xs" :class="row.videoId && !row.pending ? 'text-emerald-700' : 'text-amber-700'">{{ row.manual ? '人工指定' : confidenceLabel[row.match.confidence] }} · {{ row.match.reason }} <button v-if="row.manual" class="ml-2 text-blue-600 underline" :aria-label="`${row.name} 重新自动匹配`" @click="resetMatch(row)">重新自动匹配</button></p>
              <p v-if="conflicts.has(row.id)" class="mt-2 text-xs text-red-700">{{ conflicts.get(row.id) }}</p>
              <div v-if="!row.videoId && row.match.candidates.length" class="mt-2 space-y-1">
                <button v-for="candidate in row.match.candidates" :key="candidate.id" class="block w-full break-all rounded-lg bg-amber-50 px-2 py-2 text-left text-xs text-amber-800" @click="acceptSuggestion(row, candidate.id)">确认候选：{{ videos.find(video => video.id === candidate.id)?.name }} · {{ candidate.reason }}</button>
              </div>
              <div class="mt-2 grid gap-2 sm:grid-cols-[minmax(0,1fr)_9rem_9rem]">
                <select v-model="row.videoId" :aria-label="`${row.name} 对应视频`" class="input min-w-0 w-full" @change="manualMatch(row)"><option value="">未匹配，请选择视频</option><option v-for="video in videos" :key="video.id" :value="video.id">{{ video.name }}</option></select>
                <select v-model="row.language" :aria-label="`${row.name} 语言`" class="input" @change="syncPair(row)"><option v-for="lang in languages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select>
                <input v-model="row.version" :aria-label="`${row.name} 版本`" class="input min-w-0" maxlength="30" placeholder="版本，如 字幕组" @input="syncPair(row)" />
              </div>
            </div>
            <p v-if="!displayed.length" class="p-4 text-center text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">没有符合当前筛选的字幕。</p>
          </div>
          <div class="flex justify-end"><button class="primary" :disabled="!selected.length || !!unmatched || !!review || !!conflicts.size" @click="work('生成字幕预览', previewImport)">预览字幕导入</button></div>
        </template>
        <section v-if="plan" class="rounded-xl border border-blue-200 p-4">
          <h3 class="text-sm font-semibold">确认字幕导入</h3>
          <p class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">上传到对应视频目录，已有同名文件会跳过。字幕时间轴是否适配片源，请导入后播放检查。</p>
          <ul class="my-3 max-h-60 space-y-2 overflow-y-auto text-xs"><li v-for="item in plan.items" :key="item.localId" class="break-all"><p class="text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ item.originalName }}</p><p>→ {{ item.targetPath }}</p><p v-if="item.message" class="text-amber-700">{{ item.message }}</p></li></ul>
          <button class="primary" :disabled="!plan.items.some(item => item.status === 'pending')" @click="work('上传字幕并核实结果，请保持页面打开', upload)">确认上传 {{ plan.items.filter(item => item.status === 'pending').length }} 个字幕</button>
        </section>
      </fieldset>
      <section v-if="result" class="mt-4 rounded-xl bg-slate-50 dark:bg-slate-950 p-4"><h3 class="text-sm font-semibold">{{ result.message }} · 已上传 {{ result.items.filter(item => item.status === 'done').length }}/{{ result.items.length }}</h3><ul class="mt-3 max-h-60 space-y-2 overflow-y-auto text-xs"><li v-for="item in result.items" :key="item.localId" class="break-all">{{ item.name }}：{{ item.message || '未处理' }}</li></ul></section>
      <footer class="mt-5 flex justify-end"><button class="secondary" :disabled="!!busy" @click="emit('close')">关闭</button></footer>
    </dialog>
  </Teleport>
  <SubtitleSourceDialog v-if="sourceDialog" :token="token" :account-id="job.config.accountId" @close="sourceDialog = false" @select="selectDriveFiles" />
</template>

<style scoped>
.input { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-2 text-xs text-slate-700 dark:text-slate-300; }
.primary { @apply rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-40; }
.secondary { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 disabled:opacity-40; }
</style>
