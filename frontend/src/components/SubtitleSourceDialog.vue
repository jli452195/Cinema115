<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import type { DriveFile } from '../types/manualOrganize'
import type { SubtitleSourceFile } from '../utils/subtitleSources'
import { formatSubtitleSize, isSubtitle, isSubtitleArchive, maxFileBytes, maxTotalBytes, splitSubtitleArchive } from '../utils/subtitleImport'

const props = defineProps<{ token: string; accountId: string }>()
const emit = defineEmits<{ close: []; select: [files: SubtitleSourceFile[]] }>()
const dialog = ref<HTMLDialogElement>()
const path = ref<{ id: string; name: string }[]>([])
const files = ref<DriveFile[]>([])
const selected = ref<SubtitleSourceFile[]>([])
const loading = ref(false), error = ref('')
const lifecycle = new AbortController()
let controller: AbortController | undefined, generation = 0
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
const cid = computed(() => path.value.at(-1)?.id || '0')
const currentPath = computed(() => '/' + path.value.map(item => item.name).join('/'))
const visible = computed(() => files.value.filter(file => file.directory || isSubtitle(file.name) || isSubtitleArchive(file.name) || splitSubtitleArchive.test(file.name)))
const total = computed(() => selected.value.reduce((sum, file) => sum + file.size, 0))
const selectionError = computed(() => selected.value.length > 100 || total.value > maxTotalBytes ? '所选文件不能超过 100 个或 64 MiB，请取消部分勾选。' : '')
function unavailable(file: DriveFile) {
  if (splitSubtitleArchive.test(file.name)) return '不支持分卷'
  if (!Number.isSafeInteger(file.size) || file.size <= 0) return '空文件或大小无效'
  if (file.size > (isSubtitleArchive(file.name) ? maxTotalBytes : maxFileBytes)) return '文件超过大小限制'
  return ''
}
function toggle(file: DriveFile) {
  if (unavailable(file)) return
  const index = selected.value.findIndex(item => item.id === file.id)
  if (index >= 0) selected.value.splice(index, 1)
  else selected.value.push({ ...file, parentId: cid.value, sourcePath: `${currentPath.value.replace(/\/$/, '')}/${file.name}` })
}
async function load() {
  const version = ++generation
  controller?.abort(); controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, lifecycle.signal, AbortSignal.timeout(90000)])
  const directory = cid.value
  files.value = []; loading.value = true; error.value = ''
  try {
    const found: DriveFile[] = [], seen = new Set<string>()
    let count = -1
    for (let offset = 0; ;) {
      const data = await p115Request<{ files: DriveFile[]; count: number; offset: number }>(props.token, `manual-organize/files?accountId=${encodeURIComponent(props.accountId)}&cid=${directory}&offset=${offset}`, { signal })
      signal.throwIfAborted()
      if (version !== generation) return
      if (data.offset !== offset || data.count < offset + data.files.length || (count >= 0 && data.count !== count)) throw new Error('目录分页发生变化，请重新读取。')
      count = data.count
      for (const file of data.files) {
        if (seen.has(file.id)) throw new Error('目录分页重复，请重新读取。')
        seen.add(file.id); found.push(file)
      }
      offset += data.files.length
      if (offset >= count) break
      if (!data.files.length || offset >= 20000) throw new Error('目录过大，请选择较小的来源目录。')
    }
    files.value = found
  } catch (reason) {
    if (!lifecycle.signal.aborted && version === generation) error.value = reason instanceof Error ? reason.message : '读取目录失败'
  } finally { if (version === generation) loading.value = false }
}
function enter(file: DriveFile) { path.value.push({ id: file.id, name: file.name }); void load() }
function up() { path.value.pop(); void load() }
function confirm() { if (selected.value.length && !selectionError.value && !loading.value && !error.value) emit('select', [...selected.value]) }
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal(); void load() })
onBeforeUnmount(() => { lifecycle.abort(); controller?.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected) previousFocus.focus() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="subtitle-source-title" class="m-auto h-[100dvh] max-h-[100dvh] w-full max-w-2xl overflow-hidden overscroll-contain rounded-none border border-slate-200 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 sm:h-[min(760px,calc(100dvh-2rem))] sm:w-[calc(100%-2rem)] sm:rounded-2xl dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300" @cancel.stop.prevent="emit('close')">
      <section class="flex h-full min-h-0 flex-col">
        <header class="flex shrink-0 items-center justify-between gap-3 border-b border-slate-200 px-4 py-4 dark:border-slate-700">
          <h2 id="subtitle-source-title" class="font-semibold">从 115 网盘选择字幕</h2>
          <button type="button" aria-label="关闭网盘字幕选择" class="rounded-lg p-2 hover:bg-slate-100 dark:hover:bg-slate-800" @click="emit('close')"><svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
        </header>
        <p class="shrink-0 px-4 py-3 text-xs leading-5 text-slate-500 dark:text-slate-400">选择主网盘中的 ZIP / RAR / 7z 或字幕文件。这里只读取当前目录，子目录需点击进入。所选目录用于读取来源，字幕将上传到匹配视频所在目录。</p>
        <nav aria-label="字幕来源目录" class="flex shrink-0 items-center gap-3 border-y border-slate-100 bg-slate-50 px-4 py-3 dark:border-slate-800 dark:bg-slate-950">
          <p class="min-w-0 flex-1 truncate text-xs" :title="currentPath">{{ currentPath === '/' ? '115 根目录' : currentPath }}</p>
          <button type="button" :disabled="!path.length || loading" class="source-button shrink-0" @click="up">返回上级</button>
        </nav>
        <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4" :aria-busy="loading">
          <p v-if="loading" role="status" class="py-10 text-center text-sm">正在读取网盘目录…</p>
          <div v-else-if="error" class="py-8 text-center"><p role="alert" class="break-all text-sm text-red-600">{{ error }}</p><button class="source-button mt-3" @click="load">重新读取</button></div>
          <p v-else-if="!visible.length" class="py-10 text-center text-sm text-slate-500">当前目录没有子文件夹、字幕或支持的压缩包。</p>
          <ul v-else class="space-y-2">
            <li v-for="file in visible" :key="file.id">
              <button v-if="file.directory" type="button" class="flex min-h-12 w-full items-center gap-3 rounded-xl border border-slate-200 p-3 text-left hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800" @click="enter(file)"><svg class="h-5 w-5 shrink-0 text-amber-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" /></svg><span class="min-w-0 break-all text-sm">{{ file.name }}</span></button>
              <label v-else class="flex min-h-12 items-start gap-3 rounded-xl border border-slate-200 p-3 dark:border-slate-700" :class="unavailable(file) ? 'opacity-50' : 'cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800'">
                <input type="checkbox" :aria-label="`选择 ${file.name}`" :checked="selected.some(item => item.id === file.id)" :disabled="!!unavailable(file)" class="mt-1 h-4 w-4 shrink-0" @change="toggle(file)" />
                <span class="min-w-0"><span class="block break-all text-sm">{{ file.name }}</span><span class="mt-1 block text-xs text-slate-500">{{ formatSubtitleSize(file.size) }}<span v-if="unavailable(file)"> · {{ unavailable(file) }}</span></span></span>
              </label>
            </li>
          </ul>
        </div>
        <footer class="shrink-0 space-y-3 border-t border-slate-200 bg-white px-4 py-4 dark:border-slate-700 dark:bg-slate-900">
          <div class="flex flex-wrap items-center justify-between gap-2 text-xs"><span>已选 {{ selected.length }} 个 · {{ formatSubtitleSize(total) }} / 64 MiB</span><button v-if="selected.length" class="text-blue-600 underline" @click="selected = []">清空勾选</button></div>
          <details v-if="selected.length" class="text-xs"><summary class="cursor-pointer">查看已选文件（切换目录保留勾选）</summary><ul class="mt-2 max-h-24 space-y-2 overflow-y-auto"><li v-for="file in selected" :key="file.id" class="flex items-start gap-2"><span class="min-w-0 flex-1 break-all">{{ file.sourcePath }}</span><button :aria-label="`移除 ${file.name}`" class="shrink-0 text-blue-600 underline" @click="selected = selected.filter(item => item.id !== file.id)">移除</button></li></ul></details>
          <p v-if="selectionError" role="alert" class="text-xs text-red-600">{{ selectionError }}</p>
          <div class="flex flex-wrap justify-end gap-3"><button class="source-button" @click="emit('close')">取消</button><button class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40" :disabled="!selected.length || !!selectionError || loading || !!error" @click="confirm">下载并解析 {{ selected.length || '' }} 个文件</button></div>
        </footer>
      </section>
    </dialog>
  </Teleport>
</template>

<style scoped>
.source-button { @apply rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-600 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800; }
</style>
