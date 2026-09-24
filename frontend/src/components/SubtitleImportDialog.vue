<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { p115Request } from '../lib/p115-api'
import { episodeNumber, matchSubtitle, readLocalSubtitles, subtitleLanguage } from '../utils/subtitleImport'
import type { LocalSubtitle } from '../utils/subtitleImport'
import type { OrganizeJob } from '../types/manualOrganize'
import type { SubtitleImport, SubtitleVideo } from '../types/subtitleImport'

const props = defineProps<{ token: string; job: OrganizeJob; accountName: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()
interface Row extends LocalSubtitle { id: string; selected: boolean; videoId: string; language: string; version: string }
const dialog = ref<HTMLDialogElement>()
const picker = ref<HTMLInputElement>()
const lifecycle = new AbortController()
const videos = ref<SubtitleVideo[]>([])
const rows = ref<Row[]>([])
const busy = ref('')
const error = ref('')
const notice = ref('')
const season = ref(1)
const firstEpisode = ref(1)
const language = ref('zh-CN')
const plan = ref<SubtitleImport>()
const result = ref<SubtitleImport>()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
const selected = computed(() => rows.value.filter(row => row.selected))
const unmatched = computed(() => selected.value.filter(row => !row.videoId).length)
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
async function readFiles(files: File[]) {
  plan.value = undefined; result.value = undefined; rows.value = []; notice.value = ''
  const data = await readLocalSubtitles(files, lifecycle.signal)
  rows.value = data.files.sort((a, b) => a.name.localeCompare(b.name, 'zh-CN', { numeric: true })).map((file, index) => ({ ...file, id: String(index + 1), selected: true, videoId: matchSubtitle(file.name, videos.value, season.value), language: subtitleLanguage(file.name), version: '' }))
  notice.value = `读取 ${rows.value.length} 个字幕${data.ignored ? `，已忽略 ${data.ignored} 个其他文件（包括字体、图片等）` : ''}。请核对视频、语言和版本。`
}
function chooseFiles(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (files.length) void work('读取本地字幕', () => readFiles(files))
}
function dropFiles(event: DragEvent) {
  if (busy.value || !videos.value.length) return
  const files = Array.from(event.dataTransfer?.files || [])
  if (files.length) void work('读取本地字幕', () => readFiles(files))
}
function matchAll() { for (const row of rows.value) row.videoId = matchSubtitle(row.name, videos.value, season.value) }
function matchInOrder() {
  let episode = firstEpisode.value
  const groups = new Map<string, number>()
  for (const row of selected.value) {
    // Preserve IDX/SUB pairs as a single episode when numbering in order.
    const key = row.name.replace(/\.(idx|sub)$/i, '').toLowerCase()
    const number = groups.get(key) ?? episode++
    groups.set(key, number)
    const candidates = videos.value.filter(video => { const ep = episodeNumber(video.name); return ep?.season === season.value && ep.episode === number })
    row.videoId = candidates.length === 1 ? candidates[0].id : ''
  }
}
async function previewImport() {
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
onMounted(() => {
  document.body.style.overflow = 'hidden'; dialog.value?.showModal()
  void work('读取已整理的视频', loadVideos)
})
onBeforeUnmount(() => {
  lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="subtitle-title" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-5xl overflow-y-auto rounded-2xl bg-white p-5 text-slate-700 shadow-2xl backdrop:bg-slate-950/50" @cancel.prevent="!busy && emit('close')">
      <header class="flex items-start justify-between gap-3"><div class="min-w-0"><h2 id="subtitle-title" class="font-semibold text-slate-900">补充字幕 · {{ job.title }}</h2><p class="mt-2 break-all text-xs text-slate-500">账号：{{ accountName }} · {{ job.config.targetPath }}</p></div><button :disabled="!!busy" aria-label="关闭字幕导入" class="shrink-0 rounded-lg px-3 py-1 text-xl disabled:opacity-40" @click="emit('close')">×</button></header>
      <p v-if="busy" role="status" class="mt-3 text-sm text-blue-700">{{ busy }}…</p>
      <p v-if="error" role="alert" class="mt-3 break-all rounded-xl bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <button v-if="!videos.length && !busy" class="secondary mt-3" @click="work('读取视频', loadVideos)">重新读取视频</button>
      <fieldset :disabled="!!busy || !videos.length" class="mt-4 min-w-0 space-y-4 disabled:opacity-60">
        <div class="rounded-xl border-2 border-dashed border-blue-200 bg-blue-50 p-5 text-center" @dragover.prevent @drop.prevent="dropFiles">
          <p class="text-sm">拖入本地字幕 ZIP，或直接选择多个字幕文件</p>
          <p class="mt-2 text-xs leading-5 text-slate-500">ZIP 在本机解压，确认后才上传字幕。支持 SRT / ASS / SSA / VTT / IDX / SUB；RAR、7z 请先解压。单个字幕 16 MiB，合计 64 MiB，最多 100 个。</p>
          <input ref="picker" type="file" multiple accept=".zip,.srt,.ass,.ssa,.vtt,.idx,.sub" aria-label="选择本地字幕或 ZIP" class="sr-only" @change="chooseFiles" />
          <button class="secondary mt-3" @click="picker?.click()">{{ rows.length ? '重新选择文件' : '选择字幕文件 / ZIP' }}</button>
        </div>
        <p v-if="notice" role="status" class="text-xs leading-5 text-slate-500">{{ notice }}</p>
        <template v-if="rows.length">
          <div class="flex flex-wrap items-center gap-2 text-xs">
            <button class="secondary" @click="rows.forEach(row => row.selected = true)">全选字幕</button><button class="secondary" @click="rows.forEach(row => row.selected = false)">全不选字幕</button>
            <label>默认季 <input v-model.number="season" aria-label="字幕默认季" type="number" min="0" max="1000" class="input w-20" /></label>
            <button class="secondary" @click="matchAll">按季集重新匹配</button>
            <label>起始集 <input v-model.number="firstEpisode" aria-label="字幕起始集" type="number" min="1" max="10000" class="input w-20" /></label><button class="secondary" :disabled="firstEpisode < 1" @click="matchInOrder">按当前顺序匹配</button>
            <select v-model="language" aria-label="批量字幕语言" class="input"><option v-for="lang in languages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select><button class="secondary" @click="selected.forEach(row => row.language = language)">应用到勾选字幕</button>
          </div>
          <p class="text-xs text-slate-500">已勾选 {{ selected.length }} 个 · 未匹配 {{ unmatched }} 个。多个版本需手动选择；IDX 与 SUB 请勾选同一组并使用相同语言、版本。</p>
          <div class="max-h-80 space-y-3 overflow-y-auto">
            <div v-for="row in rows" :key="row.id" class="rounded-xl border border-slate-200 p-3">
              <label class="flex items-start gap-2 text-sm"><input v-model="row.selected" type="checkbox" :aria-label="`导入 ${row.name}`" class="mt-1" /><span class="min-w-0 break-all">{{ row.name }} <span class="text-xs text-slate-400">{{ (row.data.length / 1024).toFixed(1) }} KiB</span></span></label>
              <div class="mt-2 grid gap-2 sm:grid-cols-[minmax(0,1fr)_9rem_9rem]">
                <select v-model="row.videoId" :aria-label="`${row.name} 对应视频`" class="input min-w-0 w-full"><option value="">未匹配，请选择视频</option><option v-for="video in videos" :key="video.id" :value="video.id">{{ video.name }}</option></select>
                <select v-model="row.language" :aria-label="`${row.name} 语言`" class="input"><option v-for="lang in languages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select>
                <input v-model="row.version" :aria-label="`${row.name} 版本`" class="input min-w-0" maxlength="30" placeholder="版本，如 字幕组" />
              </div>
            </div>
          </div>
          <div class="flex justify-end"><button class="primary" :disabled="!selected.length || !!unmatched" @click="work('生成字幕预览', previewImport)">预览字幕导入</button></div>
        </template>
        <section v-if="plan" class="rounded-xl border border-blue-200 p-4">
          <h3 class="text-sm font-semibold">确认字幕导入</h3>
          <p class="mt-2 text-xs text-slate-500">上传到对应视频目录，已有同名文件会跳过。字幕时间轴是否适配片源，请导入后播放检查。</p>
          <ul class="my-3 max-h-60 space-y-2 overflow-y-auto text-xs"><li v-for="item in plan.items" :key="item.localId" class="break-all"><p class="text-slate-500">{{ item.originalName }}</p><p>→ {{ item.targetPath }}</p><p v-if="item.message" class="text-amber-700">{{ item.message }}</p></li></ul>
          <button class="primary" :disabled="!plan.items.some(item => item.status === 'pending')" @click="work('上传字幕并核实结果，请保持页面打开', upload)">确认上传 {{ plan.items.filter(item => item.status === 'pending').length }} 个字幕</button>
        </section>
      </fieldset>
      <section v-if="result" class="mt-4 rounded-xl bg-slate-50 p-4"><h3 class="text-sm font-semibold">{{ result.message }} · 已上传 {{ result.items.filter(item => item.status === 'done').length }}/{{ result.items.length }}</h3><ul class="mt-3 max-h-60 space-y-2 overflow-y-auto text-xs"><li v-for="item in result.items" :key="item.localId" class="break-all">{{ item.name }}：{{ item.message || '未处理' }}</li></ul></section>
      <footer class="mt-5 flex justify-end"><button class="secondary" :disabled="!!busy" @click="emit('close')">关闭</button></footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.input { @apply rounded-lg border border-slate-200 bg-white px-2 py-2 text-xs text-slate-700; }
.primary { @apply rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-40; }
.secondary { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-600 hover:bg-slate-50 disabled:opacity-40; }
</style>
