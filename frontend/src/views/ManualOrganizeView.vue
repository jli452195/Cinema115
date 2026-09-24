<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import DirectoryShareButton from '../components/DirectoryShareButton.vue'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'
import { tmdbImageURL } from '../utils/tmdbImage'
import type { P115Account } from '../types/p115Accounts'
import type { MediaCardItem } from '../types/media'
import type { DriveFile, OrganizeConfig, OrganizeFile, OrganizeJob, OrganizeRow } from '../types/manualOrganize'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const accounts = ref<P115Account[]>([])
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
const rows = ref<OrganizeRow[]>([])
const query = ref('')
const kind = ref<'movie' | 'tv'>('movie')
const results = ref<MediaCardItem[]>([])
const searchPage = ref(1)
const searchTotalPages = ref(0)
const media = ref<(MediaCardItem & { seasons?: { number: number; name: string; episodeCount: number }[] })>()
const subtitles = ref(true)
const batchSeason = ref(1)
const startEpisode = ref(1)
const preview = ref<OrganizeJob>()
const jobs = ref<OrganizeJob[]>([])
const historyError = ref('')
const historyReady = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let applying = false
let historyRevision = 0

const accountReady = computed(() => accounts.value.some(a => a.id === config.value.accountId && a.configured))
const configured = computed(() => accountReady.value && !!config.value.sourceCID && !!config.value.targetCID)
const active = computed(() => jobs.value.some(j => j.status === 'queued' || j.status === 'running'))
const selectedRows = computed(() => rows.value.filter(row => row.selected))
const conflicts = computed(() => preview.value?.items.filter(item => item.conflict).length ?? 0)
const statuses: Record<string, string> = { preview: '待确认', queued: '排队中', running: '整理中', completed: '已完成', partial: '已停止', interrupted: '已中断', pending: '未处理', preparing: '检查目录', renaming: '重命名中', renamed: '已命名', moving: '移动中', done: '完成', blocked: '已停止', uncertain: '结果待核实' }

function request<T>(path: string, init: RequestInit = {}, timeout = 45000) {
  return p115Request<T>(props.token, `manual-organize/${path}`, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(timeout)]) })
}
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，请重试' }
function duration(ms: number) {
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} 秒`
  return `${Math.floor(ms / 60000)} 分 ${Math.round((ms % 60000) / 1000)} 秒`
}
async function work(label: string, action: () => Promise<void>) {
  if (busy.value) return
  busy.value = label; error.value = ''; notice.value = ''
  try { await action() } catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { busy.value = '' }
}
function clearSelection() { scanId.value = ''; rows.value = []; preview.value = undefined; selectedName.value = '' }
async function save() { await request('config', { method: 'PUT', body: JSON.stringify(config.value) }); notice.value = '账号、来源目录和目标目录已保存。' }
async function browse(nextOffset = 0) {
  const data = await request<{ files: DriveFile[]; count: number }>(`files?accountId=${encodeURIComponent(config.value.accountId)}&cid=${config.value.sourceCID}&offset=${nextOffset}`)
  files.value = data.files; offset.value = nextOffset; count.value = data.count
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
  clearSelection()
  await save()
  const data = await request<{ id: string; files: OrganizeFile[] }>('scan', { method: 'POST', body: JSON.stringify({ accountId: config.value.accountId, parentId: config.value.sourceCID, entryId: file.id }) }, 190000)
  scanId.value = data.id; selectedName.value = file.name
  rows.value = data.files.filter(f => f.mediaType === 'video').sort((a, b) => a.sourcePath.localeCompare(b.sourcePath, 'zh-CN', { numeric: true })).map(f => ({ ...f, selected: !/(?:^|[. _-])(sample|trailer)(?:[. _-]|$)/i.test(f.name), endEpisode: '' }))
  query.value = file.name.replace(/\.(mkv|mp4|avi|mov|ts|m2ts|iso)$/i, '')
  media.value = undefined; results.value = []; searchTotalPages.value = 0
  notice.value = rows.value.length ? `已展开 ${rows.value.length} 个视频，请指定 TMDB 作品。` : '所选条目中没有支持的视频文件。'
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
}
async function search(page = 1) {
  const text = query.value.trim(); if (!text) return
  const link = text.match(/themoviedb\.org\/(movie|tv)\/(\d+)/i)
  if (link || /^\d+$/.test(text)) {
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
  for (const row of rows.value) if (row.selected) { row.season = Number(batchSeason.value); row.episode = number++; row.endEpisode = '' }
}
async function makePreview() {
  if (!media.value) return
  await save()
  preview.value = await request<OrganizeJob>(`preview?accountId=${encodeURIComponent(config.value.accountId)}`, { method: 'POST', body: JSON.stringify({ scanId: scanId.value, kind: media.value.kind, tmdbId: media.value.id, subtitles: subtitles.value, files: selectedRows.value.map(row => ({ id: row.id, season: Number(row.season), episode: Number(row.episode), endEpisode: Number(row.endEpisode), suffix: row.suffix })) }) }, 190000)
  notice.value = conflicts.value ? '存在冲突，请调整选择或命名后重新预览。' : '请核对下方原路径和目标路径，确认后执行。'
}
async function execute() {
  if (!preview.value || conflicts.value) return
  const job = await request<OrganizeJob>('jobs', { method: 'POST', body: JSON.stringify({ accountId: config.value.accountId, previewId: preview.value.id }) })
  jobs.value = [job, ...jobs.value.filter(j => j.id !== job.id)]
  preview.value = undefined; scanId.value = ''; rows.value = []; selectedName.value = ''
  notice.value = '整理任务已提交，进度见下方记录；离开页面后任务仍会继续。'
  await refreshHistory()
}
async function refreshHistory() {
  const revision = ++historyRevision
  try {
    const data = await request<{ jobs: OrganizeJob[] }>('jobs')
    if (revision !== historyRevision || lifecycle.signal.aborted) return
    jobs.value = data.jobs
    historyError.value = ''; historyReady.value = true
  } catch (reason) { if (!lifecycle.signal.aborted && revision === historyRevision) historyError.value = message(reason) }
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
watch(config, () => { if (!applying) clearSelection() }, { deep: true, flush: 'sync' })
watch([rows, media, subtitles], () => { preview.value = undefined }, { deep: true, flush: 'sync' })
watch(kind, () => { results.value = []; searchTotalPages.value = 0 })
onMounted(() => {
  void work('加载配置', async () => {
    const [a, c] = await Promise.all([p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }), request<OrganizeConfig>('config')])
    accounts.value = a.accounts; applying = true; config.value = c; applying = false
    if (accountReady.value && c.sourceCID) await browse()
  })
  void poll()
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) clearTimeout(timer) })
</script>

<template>
  <ManagementLayout title="手动整理" description="选择网盘文件，指定 TMDB 作品，核对命名后移动到目标目录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="space-y-6">
      <section class="rounded-2xl border border-slate-200 bg-white p-5">
        <h2 class="mb-3 font-semibold text-slate-900">整理后的目录分享</h2>
        <DirectoryShareButton :token="token" :disabled="!historyReady || active || !!busy || !!historyError" :disabled-reason="historyError ? '整理状态读取失败，请等待刷新后再分享' : '请等待当前整理操作完成后再分享'" />
      </section>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-xl bg-blue-50 p-4 text-sm text-blue-700">{{ notice }}</p>
      <p v-if="busy" role="status" class="text-sm text-slate-500">{{ busy }}…</p>
      <fieldset :disabled="!!busy" class="min-w-0 space-y-6 disabled:opacity-70">
        <section class="panel">
          <h2 class="heading">整理位置</h2>
          <div class="mt-4 grid gap-4 lg:grid-cols-3">
            <div><P115AccountSelect id="organize-account" v-model="config.accountId" :accounts="accounts" /></div>
            <div><label class="label">来源目录</label><button type="button" class="directory" :disabled="!accountReady" @click="directoryDialog = 'source'">{{ config.sourcePath || '选择待整理目录' }}</button></div>
            <div><label class="label">目标目录</label><button type="button" class="directory" :disabled="!accountReady" @click="directoryDialog = 'target'">{{ config.targetPath || '选择整理后的目录' }}</button></div>
          </div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3"><p class="text-xs leading-5 text-slate-500">同一账号内移动。目标下按作品建目录，剧集按季建目录。</p><button class="secondary" :disabled="!configured" @click="work('保存配置', save)">保存配置</button></div>
        </section>

        <div class="grid items-start gap-6 xl:grid-cols-2">
          <section class="panel">
            <div class="flex items-center justify-between gap-3"><h2 class="heading">1. 选择文件或文件夹</h2><button class="secondary" :disabled="!config.sourceCID || !accountReady" @click="work('读取文件', () => browse())">刷新</button></div>
            <div class="my-4 flex items-center gap-3"><button v-if="breadcrumbs.length" class="text-sm text-blue-600" @click="work('读取文件', goBack)">返回上级</button><p class="min-w-0 break-all text-xs text-slate-500">{{ config.sourcePath || '先选择来源目录' }}</p></div>
            <div class="max-h-[420px] overflow-y-auto divide-y divide-slate-100">
              <div v-for="file in files" :key="file.id" class="flex items-center gap-3 py-3">
                <span class="shrink-0 text-xs" :class="file.directory ? 'text-amber-600' : 'text-slate-400'">{{ file.directory ? '目录' : '文件' }}</span>
                <button v-if="file.directory" class="min-w-0 flex-1 break-all text-left text-sm text-blue-700 hover:underline" @click="work('读取文件', () => enter(file))">{{ file.name }}</button>
                <span v-else class="min-w-0 flex-1 break-all text-sm text-slate-700">{{ file.name }}</span>
                <button class="secondary shrink-0" :disabled="!configured || active" @click="work('展开所选条目', () => scan(file))">选择</button>
              </div>
              <p v-if="!files.length" class="py-12 text-center text-sm text-slate-400">{{ config.sourceCID ? '当前目录没有文件' : '选择目录后显示网盘内容' }}</p>
            </div>
            <div v-if="count > 100" class="mt-4 flex items-center justify-between text-xs text-slate-500"><button class="secondary" :disabled="offset === 0" @click="work('读取文件', () => browse(Math.max(0, offset - 100)))">上一页</button><span>{{ offset + 1 }}–{{ offset + files.length }} / {{ count }}</span><button class="secondary" :disabled="offset + files.length >= count" @click="work('读取文件', () => browse(offset + files.length))">下一页</button></div>
          </section>

          <section class="panel">
            <h2 class="heading">2. 指定 TMDB 作品</h2>
            <form class="mt-4 flex flex-wrap gap-2" @submit.prevent="work('搜索 TMDB', () => search())">
              <select v-model="kind" aria-label="作品类型" class="input w-24"><option value="movie">电影</option><option value="tv">剧集</option></select>
              <input v-model="query" aria-label="搜索 TMDB" placeholder="片名、TMDB ID 或链接" maxlength="300" class="input min-w-0 flex-1" />
              <button class="primary" :disabled="!query.trim()">搜索</button>
            </form>
            <div v-if="media" class="mt-4 rounded-xl border border-blue-200 bg-blue-50 p-3"><p class="text-sm font-semibold text-blue-800">已选：{{ media.title }} {{ media.date?.slice(0, 4) }} · {{ media.kind === 'tv' ? '剧集' : '电影' }}</p><p class="mt-1 text-xs text-blue-600">TMDB {{ media.id }}</p></div>
            <div class="mt-4 max-h-[360px] space-y-2 overflow-y-auto">
              <button v-for="item in results" :key="`${item.kind}:${item.id}`" class="flex w-full gap-3 rounded-xl border border-slate-200 p-3 text-left hover:border-blue-400" @click="work('读取作品信息', () => choose(item))">
                <img v-if="item.posterUrl" :src="tmdbImageURL(item.posterUrl)" alt="" class="h-24 w-16 shrink-0 rounded-md object-cover" loading="lazy" />
                <div class="min-w-0"><p class="text-sm font-medium text-slate-800">{{ item.title }} <span class="text-slate-400">{{ item.date?.slice(0, 4) }}</span></p><p class="mt-1 text-xs text-slate-400">{{ item.originalTitle }} · TMDB {{ item.id }}</p><p class="mt-2 line-clamp-3 text-xs leading-5 text-slate-500">{{ item.overview || '暂无简介' }}</p></div>
              </button>
              <p v-if="!results.length && !media" class="py-12 text-center text-sm text-slate-400">搜索后选择正确的作品</p>
            </div>
            <div v-if="searchTotalPages > 1" class="mt-3 flex items-center justify-between text-xs"><button class="secondary" :disabled="searchPage <= 1" @click="work('搜索 TMDB', () => search(searchPage - 1))">上一页</button><span>{{ searchPage }} / {{ searchTotalPages }}</span><button class="secondary" :disabled="searchPage >= searchTotalPages" @click="work('搜索 TMDB', () => search(searchPage + 1))">下一页</button></div>
          </section>
        </div>

        <section v-if="scanId" class="panel">
          <h2 class="heading">3. 校正文件与命名</h2>
          <p class="mt-2 break-all text-sm text-slate-500">{{ selectedName }} · 已选 {{ selectedRows.length }} / {{ rows.length }} 个视频</p>
          <div class="my-4 flex flex-wrap items-center gap-3 text-sm">
            <button class="secondary" @click="rows.forEach(row => row.selected = true)">全选</button><button class="secondary" @click="rows.forEach(row => row.selected = false)">全不选</button>
            <label class="flex items-center gap-2"><input v-model="subtitles" type="checkbox" />同时整理匹配的外挂字幕</label>
          </div>
          <div v-if="media?.kind === 'tv'" class="mb-4 flex flex-wrap items-center gap-3 rounded-xl bg-slate-50 p-3 text-sm">
            <label>季 <select v-model.number="batchSeason" class="input ml-1 w-36"><option v-for="season in media.seasons" :key="season.number" :value="season.number">{{ season.name }}（{{ season.episodeCount }} 集）</option></select></label>
            <label>起始集 <input v-model.number="startEpisode" type="number" min="1" max="10000" class="input ml-1 w-20" /></label>
            <button class="secondary" :disabled="!selectedRows.length || startEpisode < 1" @click="numberEpisodes">按当前顺序连续编号</button>
            <p class="w-full text-xs text-slate-500">按文件路径自然排序。请核对集数；合并多集文件可填写结束集，单集留空。</p>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[620px] text-left text-sm">
              <thead class="border-b text-xs text-slate-500"><tr><th class="p-2">选择</th><th class="p-2">原文件</th><template v-if="media?.kind === 'tv'"><th class="p-2">季</th><th class="p-2">集</th><th class="p-2">结束集</th></template><th class="p-2">版本标记（可修改）</th></tr></thead>
              <tbody><tr v-for="row in rows" :key="row.id" class="border-b border-slate-100"><td class="p-2"><input v-model="row.selected" type="checkbox" :aria-label="`选择 ${row.name}`" /></td><td class="max-w-sm break-all p-2 text-xs leading-5">{{ row.sourcePath }}</td><template v-if="media?.kind === 'tv'"><td class="p-2"><input v-model.number="row.season" :aria-label="`${row.name} 季`" type="number" min="0" max="1000" class="input w-20" /></td><td class="p-2"><input v-model.number="row.episode" :aria-label="`${row.name} 集`" type="number" min="1" max="10000" class="input w-20" /></td><td class="p-2"><input v-model.number="row.endEpisode" :aria-label="`${row.name} 结束集`" type="number" min="0" max="10000" class="input w-20" /></td></template><td class="p-2"><input v-model="row.suffix" :aria-label="`${row.name} 版本标记`" maxlength="100" placeholder="如 1080p WEB-DL" class="input w-44" /></td></tr></tbody>
            </table>
          </div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-slate-500">字幕按同目录、原视频文件名匹配，保留语言后缀。未勾选内容保留原位。</p><button class="primary" :disabled="!media || !selectedRows.length || !configured || active" @click="work('生成整理预览', makePreview)">预览整理结果</button></div>
        </section>

        <section v-if="preview" class="panel">
          <h2 class="heading">4. 确认整理预览</h2>
          <p class="mt-2 text-sm text-slate-500">{{ preview.title }} · {{ preview.items.length }} 个文件（含字幕）<span v-if="conflicts" class="text-red-600"> · {{ conflicts }} 项冲突</span></p>
          <div class="mt-4 max-h-[480px] space-y-3 overflow-y-auto"><div v-for="item in preview.items" :key="item.file.id" class="rounded-xl border p-3 text-xs leading-6" :class="item.conflict ? 'border-red-200 bg-red-50' : 'border-slate-200'"><p class="break-all text-slate-500">{{ item.file.sourcePath }}</p><p class="break-all text-slate-800">→ {{ item.targetPath }}</p><p v-if="item.conflict" class="text-red-600">{{ item.conflict }}</p></div></div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-slate-500">将修改 115 中的文件名和位置。同名冲突需处理后重新预览。</p><button class="primary" :disabled="!!conflicts || active" @click="work('提交整理任务', execute)">确认并开始整理</button></div>
        </section>
      </fieldset>

      <section class="panel">
        <div class="flex items-center justify-between"><h2 class="heading">整理记录</h2><button class="secondary" @click="refreshHistory">刷新</button></div>
        <p v-if="historyError" role="alert" class="mt-3 text-sm text-red-600">{{ historyError }}</p>
        <p v-if="!jobs.length" class="py-10 text-center text-sm text-slate-400">暂无整理记录</p>
        <details v-for="job in jobs" :key="job.id" class="mt-4 rounded-xl border border-slate-200 p-4" :open="job.status === 'running' || job.status === 'queued'">
          <summary class="cursor-pointer text-sm font-medium text-slate-800">{{ job.title }} · {{ statuses[job.status] || job.status }} · {{ job.items.filter(i => i.status === 'done').length }}/{{ job.items.length }} <span class="ml-2 text-xs font-normal text-slate-400">{{ new Date(job.createdAt).toLocaleString() }}</span></summary>
          <p class="mt-3 break-all text-xs text-slate-500">账号：{{ accounts.find(a => a.id === job.config.accountId)?.name || job.config.accountId }} · {{ job.config.sourcePath }} → {{ job.config.targetPath }}</p>
          <p v-if="job.message" class="mt-2 text-sm" :class="job.status === 'completed' ? 'text-emerald-700' : 'text-amber-700'">{{ job.message }}</p>
          <div v-if="job.stats?.batchSize" class="mt-3 text-xs leading-6 text-slate-500">
            <p>已完成 {{ job.stats.batchesCompleted }}/{{ job.stats.batchesTotal }} 批 · 每批最多 {{ job.stats.batchSize }} 个文件 · 累计用时 {{ duration(job.stats.elapsedMs) }}</p>
            <details class="mt-1">
              <summary class="cursor-pointer text-blue-600">查看耗时详情</summary>
              <p>115 请求 {{ job.stats.httpRequests }} 次 · 接口响应 {{ duration(job.stats.httpMs) }} · 排队 / 限速等待 {{ duration(job.stats.waitMs) }}</p>
              <p>目录读取 {{ job.stats.directoryRequests }} 次 · 批量改名 {{ job.stats.renameRequests }} 次 · 批量移动 {{ job.stats.moveRequests }} 次 · 新建目录 {{ job.stats.directoryCreates }} 次</p>
              <p>目录读取累计 {{ duration(job.stats.directoryMs) }} · 写操作累计 {{ duration(job.stats.mutationMs) }}（均包含等待）</p>
              <p>记录保存 {{ job.stats.checkpoints }} 次 · {{ duration(job.stats.saveMs) }}；统计在执行阶段结束后更新。</p>
            </details>
          </div>
          <div class="mt-3 max-h-96 space-y-2 overflow-y-auto"><div v-for="item in job.items" :key="item.file.id" class="rounded-lg bg-slate-50 p-3 text-xs leading-5"><p class="break-all text-slate-500">{{ item.file.sourcePath }}</p><p class="break-all">→ {{ item.targetPath }}</p><p class="mt-1 font-medium" :class="item.status === 'done' ? 'text-emerald-700' : 'text-amber-700'">{{ statuses[item.status] || item.status }}{{ item.message ? `：${item.message}` : '' }}</p></div></div>
        </details>
      </section>
    </div>
    <P115DirectoryDialog v-if="directoryDialog" :token="token" :account-id="config.accountId" :title="directoryDialog === 'source' ? '选择来源目录' : '选择目标目录'" endpoint="manual-organize/files" @close="directoryDialog = ''" @select="selectDirectory" />
  </ManagementLayout>
</template>

<style scoped>
.panel { @apply min-w-0 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm; }
.heading { @apply text-base font-semibold text-slate-900; }
.label { @apply text-sm font-medium text-slate-700; }
.directory { @apply mt-2 w-full break-all rounded-xl border border-slate-200 bg-slate-50 p-3 text-left text-sm text-blue-700 disabled:opacity-40; }
.input { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-700 outline-none focus:border-blue-500; }
.primary { @apply rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-40; }
.secondary { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-600 hover:bg-slate-50 disabled:opacity-40; }
button:focus-visible { outline: 2px solid #3b82f6; outline-offset: 3px; }
</style>
