<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { embyRequest } from '../types/emby'
const props = defineProps<{ token: string }>()
interface Entry { key: string; uid: string; directoryId: string; directoryName: string; status: string; message: string; lastUsed: string; file: { name: string; size: number; id: string; sha1: string } }
interface Material { id: string; name: string; size: number; sha1: string; verifiedAt: string; ranges: { start: number; end: number; sha1: string }[] }
const entries = ref<Entry[]>([]), materials = ref<Material[]>([])
const tab = ref<'copies' | 'materials'>('copies')
const search = ref(''), status = ref('all'), uid = ref('all')
const page = ref(1), pageSize = 8
const expanded = ref(''), forgetKey = ref(''), forgetMaterial = ref('')
const busy = ref(false), loaded = ref(false), error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const privateCopies = ref(0)
const stats = ref({ targetHits: 0, materialHits: 0, rangeHits: 0, donorReads: 0, primaryReads: 0 })
const uidOptions = computed(() => [...new Set(entries.value.map(e => e.uid).filter(Boolean))].sort((a, b) => a.localeCompare(b, undefined, { numeric: true })))
const query = computed(() => search.value.trim().toLowerCase())
const filteredEntries = computed(() => entries.value.filter(e =>
  (uid.value === 'all' || e.uid === uid.value) &&
  (status.value === 'all' || (status.value === 'attention' ? e.status !== 'ready' : e.status === status.value)) &&
  [e.file.name, e.file.sha1, e.file.id, e.uid].join(' ').toLowerCase().includes(query.value))
  .sort((a, b) => Date.parse(b.lastUsed) - Date.parse(a.lastUsed)))
const filteredMaterials = computed(() => materials.value.filter(m => [m.name, m.sha1].join(' ').toLowerCase().includes(query.value))
  .sort((a, b) => Date.parse(b.verifiedAt) - Date.parse(a.verifiedAt)))
const total = computed(() => tab.value === 'copies' ? filteredEntries.value.length : filteredMaterials.value.length)
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const pagedEntries = computed(() => filteredEntries.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const pagedMaterials = computed(() => filteredMaterials.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const copyCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const entry of entries.value) {
    if (entry.status !== 'ready') continue
    const key = `${entry.file.sha1.toUpperCase()}:${entry.file.size}`
    counts.set(key, (counts.get(key) || 0) + 1)
  }
  return counts
})
function copyCount(m: Material) { return copyCounts.value.get(`${m.sha1.toUpperCase()}:${m.size}`) || 0 }
function resetDetails() { expanded.value = ''; forgetKey.value = ''; forgetMaterial.value = '' }
watch([tab, search, status, uid], () => { page.value = 1; resetDetails() })
watch(page, resetDetails)
watch(pages, count => { page.value = Math.min(page.value, count) })
function toggleDetails(id: string) { expanded.value = expanded.value === id ? '' : id; forgetKey.value = ''; forgetMaterial.value = '' }
function accept(data: { entries: Entry[]; materials?: Material[]; privateCopies?: number; stats?: typeof stats.value }) { entries.value = data.entries || []; materials.value = data.materials || []; privateCopies.value = data.privateCopies || 0; if (data.stats) stats.value = data.stats; loaded.value = true }
function sizeLabel(size: number) { const gb = size >= 1024 ** 3; return `${(size / (gb ? 1024 ** 3 : 1024 ** 2)).toLocaleString(undefined, { maximumFractionDigits: 2 })} ${gb ? 'GB' : 'MB'}` }
function dateLabel(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? '暂无记录' : date.toLocaleString() }
async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { accept(await embyRequest(props.token, 'playback-cache', lifecycle.signal)) }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取缓存记录失败' }
  finally { busy.value = false }
}
async function action(path: string, body: object) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    accept(await embyRequest(props.token, `playback-cache${path}`, lifecycle.signal, body))
    notice.value = path === '/forget-material' ? '已移除文件资料及校验片段哈希，账号副本记录和云端文件保留。' : '已移除本地副本记录，文件资料和云端文件保留。'
    resetDetails()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = false }
}
function stateLabel(s: string) { return ({ ready: '已验证', uncertain: '需核查', creating: '创建目录中', uploading: '秒传中', deleting: '清理中' } as Record<string, string>)[s] || s }
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section aria-label="网盘文件复用记录" class="min-w-0 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900 md:p-7">
    <div class="flex items-center justify-between gap-3">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">网盘文件复用记录</h2>
      <button :disabled="busy" type="button" class="shrink-0 text-sm text-blue-600 disabled:opacity-40 dark:text-blue-400" @click="load">{{ busy ? '加载中…' : '刷新记录' }}</button>
    </div>
    <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">按 115 UID 查看文件副本，移除本地记录不会删除网盘文件。历史验证不代表当前可访问。</p>
    <details class="mt-2 text-xs leading-5 text-slate-500 dark:text-slate-400"><summary class="cursor-pointer">运行统计</summary><p>本次启动：目标复用 {{ stats.targetHits }} · 资料命中 {{ stats.materialHits }} · 片段命中 {{ stats.rangeHits }} · 副本校验 {{ stats.donorReads }} · 主盘读取 {{ stats.primaryReads }}</p></details>
    <p v-if="privateCopies" class="mt-1 text-xs text-slate-500 dark:text-slate-400">另有 {{ privateCopies }} 条个人或已移除账号的副本记录，仅展示汇总。</p>
    <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-700 dark:bg-blue-500/10 dark:text-blue-300">{{ notice }}</p>
    <div class="mt-5 flex gap-5 border-b border-slate-200 dark:border-slate-700" aria-label="记录分类">
      <button v-for="item in [{ key: 'copies', label: '网盘副本', count: entries.length }, { key: 'materials', label: '文件秒传资料', count: materials.length }] as const" :key="item.key" type="button" :aria-pressed="tab === item.key" class="border-b-2 pb-3 text-sm font-medium" :class="tab === item.key ? 'border-blue-600 text-blue-600 dark:border-blue-400 dark:text-blue-400' : 'border-transparent text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200'" @click="tab = item.key">
        {{ item.label }} <span class="ml-1 text-xs tabular-nums opacity-75">{{ item.count }}</span>
      </button>
    </div>
    <div class="mt-4 flex flex-wrap gap-2 text-slate-700 dark:text-slate-200">
      <input v-model="search" type="search" aria-label="搜索复用记录" :placeholder="tab === 'copies' ? '搜索文件名、SHA1 或 UID' : '搜索文件名或 SHA1'" class="min-w-0 flex-[1_1_220px] rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-950" />
      <template v-if="tab === 'copies'">
        <select v-model="uid" aria-label="筛选 115 UID" class="min-w-0 max-w-full rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-950"><option value="all">全部 UID</option><option v-for="id in uidOptions" :key="id" :value="id">UID {{ id }}</option></select>
        <select v-model="status" aria-label="筛选记录状态" class="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-950"><option value="all">全部状态</option><option value="ready">已验证记录</option><option value="attention">需关注</option></select>
      </template>
    </div>
    <p class="my-3 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ tab === 'copies' ? '按最近使用排序' : '按最近验证排序 · 相同校验范围可直接复用片段哈希，新范围仍需读取文件来源' }}</p>
    <div v-if="tab === 'copies'" class="divide-y divide-slate-100 dark:divide-slate-800">
      <article v-for="entry in pagedEntries" :key="entry.key" class="py-3">
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <p :title="entry.file.name" class="truncate text-sm font-medium text-slate-800 dark:text-slate-200">{{ entry.file.name || '文件信息待确认' }}</p>
            <p class="mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400"><span class="block truncate sm:inline"><template v-if="uid === 'all'">UID {{ entry.uid }} · </template>{{ sizeLabel(entry.file.size) }}</span><span class="hidden sm:inline"> · </span><span>{{ dateLabel(entry.lastUsed) }}</span></p>
          </div>
          <span class="shrink-0 rounded-md px-2 py-1 text-xs" :class="entry.status === 'ready' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400' : 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300'">{{ stateLabel(entry.status) }}</span>
          <button type="button" :aria-expanded="expanded === entry.key" :aria-label="'查看文件详情：' + entry.file.name" class="shrink-0 py-1 text-xs text-blue-600 dark:text-blue-400" @click="toggleDetails(entry.key)">{{ expanded === entry.key ? '收起' : '详情' }}</button>
        </div>
        <div v-if="expanded === entry.key" class="mt-3 space-y-2 rounded-lg bg-slate-50 p-3 text-xs leading-5 text-slate-600 dark:bg-slate-950 dark:text-slate-400">
          <p class="break-all font-medium text-slate-800 dark:text-slate-200">{{ entry.file.name }}</p>
          <p class="break-all">SHA1：{{ entry.file.sha1 || '待确认' }}</p>
          <p class="break-all">文件 ID：{{ entry.file.id || '待确认' }} · 目录：{{ entry.directoryId || '待确认' }}</p>
          <p v-if="entry.message" class="break-words">{{ entry.message }}</p>
          <button v-if="forgetKey !== entry.key" type="button" :disabled="busy" class="text-red-600 underline disabled:opacity-40 dark:text-red-400" @click="forgetKey = entry.key">移除本地记录</button>
          <div v-else class="rounded-lg bg-amber-50 p-3 text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"><p>仅移除本地记录，不删除云端文件。下次播放先按 SHA1 查找；存在未确认上传时，请先核查网盘。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline disabled:opacity-40" @click="action('/forget', { key: entry.key })">确认仅移除记录</button><button type="button" class="ml-4" @click="forgetKey = ''">取消</button></div>
        </div>
      </article>
    </div>
    <div v-else class="divide-y divide-slate-100 dark:divide-slate-800">
      <article v-for="material in pagedMaterials" :key="material.id" class="py-3">
        <div class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <p :title="material.name" class="truncate text-sm font-medium text-slate-800 dark:text-slate-200">{{ material.name }}</p>
            <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{{ sizeLabel(material.size) }} · 可见副本 {{ copyCount(material) }} · 校验片段 {{ material.ranges.length }}</p>
          </div>
          <button type="button" :aria-expanded="expanded === material.id" :aria-label="'查看资料详情：' + material.name" class="shrink-0 py-1 text-xs text-blue-600 dark:text-blue-400" @click="toggleDetails(material.id)">{{ expanded === material.id ? '收起' : '详情' }}</button>
        </div>
        <div v-if="expanded === material.id" class="mt-3 space-y-2 rounded-lg bg-slate-50 p-3 text-xs leading-5 text-slate-600 dark:bg-slate-950 dark:text-slate-400">
          <p class="break-all font-medium text-slate-800 dark:text-slate-200">{{ material.name }}</p>
          <p class="break-all">SHA1：{{ material.sha1 }}</p>
          <p>验证时间：{{ dateLabel(material.verifiedAt) }}</p>
          <details v-if="material.ranges.length"><summary class="cursor-pointer">查看校验片段（{{ material.ranges.length }}）</summary><div class="mt-2 max-h-48 space-y-2 overflow-y-auto"><p v-for="range in material.ranges" :key="range.start + ':' + range.end" class="break-all">字节 {{ range.start }}–{{ range.end }} · {{ range.sha1 }}</p></div></details>
          <button v-if="forgetMaterial !== material.id" type="button" :disabled="busy" class="text-red-600 underline disabled:opacity-40 dark:text-red-400" @click="forgetMaterial = material.id">移除文件资料</button>
          <div v-else class="rounded-lg bg-amber-50 p-3 text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"><p>移除后，再次秒传可能需要重新读取源文件。账号副本记录和云端文件保留。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline disabled:opacity-40" @click="action('/forget-material', { id: material.id })">确认移除资料</button><button type="button" class="ml-4" @click="forgetMaterial = ''">取消</button></div>
        </div>
      </article>
    </div>
    <p v-if="!total" role="status" class="py-10 text-center text-sm text-slate-500 dark:text-slate-400">{{ !loaded && busy ? '正在加载记录…' : error && !loaded ? '记录加载失败，请重试。' : (tab === 'copies' ? entries.length : materials.length) ? '没有匹配的记录，试试调整搜索或筛选条件。' : tab === 'copies' ? '暂无文件副本，复用文件或秒传后会自动记录。' : '暂无秒传资料，进入秒传流程后会自动积累。' }}</p>
    <div v-if="total" class="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-slate-200 pt-4 text-xs text-slate-500 dark:border-slate-700 dark:text-slate-400">
      <span>共 {{ total }} 条 · 每页 {{ pageSize }} 条</span>
      <div class="flex items-center gap-3"><button type="button" :disabled="page === 1" class="rounded-lg border border-slate-200 px-3 py-2 disabled:opacity-40 dark:border-slate-700" @click="page--">上一页</button><span class="tabular-nums">{{ page }} / {{ pages }}</span><button type="button" :disabled="page === pages" class="rounded-lg border border-slate-200 px-3 py-2 disabled:opacity-40 dark:border-slate-700" @click="page++">下一页</button></div>
    </div>
  </section>
</template>
