<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'

interface Directory { id: string; name: string }
interface DirectoryPage { directories?: Directory[]; files?: (Directory & { directory: boolean })[]; count?: number; hasMore?: boolean; nextOffset?: number }

const props = withDefaults(defineProps<{ token: string; accountId: string; title?: string; endpoint?: string; allowRoot?: boolean; allowCreate?: boolean; loadPage?: (cid: string, offset: number, signal: AbortSignal) => Promise<DirectoryPage> }>(), { title: '选择下载目录', endpoint: 'cloud-download/paths', allowRoot: true, allowCreate: false })
const emit = defineEmits<{ close: []; select: [value: { id: string; path: string; name: string }]; create: [value: { id: string; path: string; name: string }] }>()
const dialog = ref<HTMLDialogElement>()
const currentCID = ref('0')
const currentPath = ref<{ id: string; name: string }[]>([])
const directories = ref<Directory[]>([])
const loading = ref(false)
const error = ref('')
const lifecycle = new AbortController()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow

async function loadCID(cid: string) {
  currentCID.value = cid
  loading.value = true
  error.value = ''
  try {
    const found: Directory[] = []
    for (let offset = 0; ;) {
      const query = `?accountId=${encodeURIComponent(props.accountId)}&cid=${encodeURIComponent(cid)}&offset=${offset}`
      const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(45000)])
      const data = props.loadPage ? await props.loadPage(cid, offset, signal) : await p115Request<DirectoryPage>(props.token, `${props.endpoint}${query}`, { signal })
      found.push(...(data.directories ?? data.files?.filter(file => file.directory) ?? []))
      const length = data.files?.length ?? 100
      if (data.files ? offset + length >= (data.count ?? 0) : !data.hasMore) break
      if (!length || offset >= 20000) throw new Error('目录分页不完整，请刷新重试')
      const next = data.nextOffset ?? offset + length
      if (next <= offset) throw new Error('目录分页无进展，请刷新重试')
      offset = next
    }
    if (!lifecycle.signal.aborted) directories.value = found
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取 115 目录失败'
  } finally {
    if (!lifecycle.signal.aborted) loading.value = false
  }
}

async function openDirectory(directory?: Directory) {
  const cid = directory?.id || '0'
  if (directory) currentPath.value = [...currentPath.value, { id: directory.id, name: directory.name }]
  else if (cid === '0') currentPath.value = []
  await loadCID(cid)
}

function goUp() {
  if (!currentPath.value.length) return
  currentPath.value = currentPath.value.slice(0, -1)
  void loadCID(currentPath.value.length ? currentPath.value[currentPath.value.length - 1].id : '0')
}

function close() { emit('close') }
function select(create = false) {
  const current = currentPath.value[currentPath.value.length - 1]
  const value = { id: currentCID.value, path: currentPath.value.length ? '/' + currentPath.value.map(item => item.name).join('/') : '/', name: current?.name || '115 根目录' }
  if (create) emit('create', value)
  else emit('select', value)
}

onMounted(() => {
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
  void openDirectory()
})
onBeforeUnmount(() => {
  lifecycle.abort()
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="p115-directory-title" class="m-auto h-[min(720px,calc(100dvh-2rem))] w-[calc(100%-2rem)] max-w-xl overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-0 text-slate-700 dark:text-slate-300 shadow-2xl backdrop:bg-slate-950/50" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <section class="flex h-full min-h-0 flex-col">
        <header class="flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 px-5 py-4">
          <h2 id="p115-directory-title" class="font-semibold text-slate-900 dark:text-slate-100">{{ title }}</h2>
          <button type="button" aria-label="关闭目录选择器" class="rounded-lg px-2 py-1 text-2xl leading-none text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 hover:text-slate-700 dark:hover:text-slate-300 dark:text-slate-300" @click="close">×</button>
        </header>
        <div class="flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 bg-slate-50 dark:bg-slate-950 px-5 py-3">
          <p class="min-w-0 truncate text-sm font-medium text-slate-700 dark:text-slate-300">根目录<span v-if="currentPath.length"> / {{ currentPath.map(item => item.name).join(' / ') }}</span></p>
          <button type="button" :disabled="!currentPath.length || loading" class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-1.5 text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="goUp">返回上级</button>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto p-4" :aria-busy="loading">
          <div v-if="loading" class="grid min-h-40 place-items-center text-sm text-slate-400 dark:text-slate-500">正在读取 115 目录…</div>
          <div v-else-if="error" class="grid min-h-40 place-items-center px-5 text-center"><div><p role="alert" class="text-sm text-red-600">{{ error }}</p><button type="button" class="mt-4 text-sm text-blue-600 hover:underline" @click="openDirectory()">重新读取</button></div></div>
          <div v-else-if="!directories.length" class="grid min-h-40 place-items-center text-center text-sm text-slate-400 dark:text-slate-500">当前目录没有子文件夹</div>
          <div v-else class="space-y-2">
            <button v-for="directory in directories" :key="directory.id" type="button" class="flex w-full items-center gap-3 rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-3 text-left transition hover:border-blue-300 hover:bg-blue-50/50" @click="openDirectory(directory)">
              <span class="text-amber-500">📁</span><span class="min-w-0 flex-1 truncate text-sm font-medium text-slate-800 dark:text-slate-200">{{ directory.name }}</span><span class="text-slate-400 dark:text-slate-500">›</span>
            </button>
          </div>
        </div>
        <footer class="flex shrink-0 flex-wrap items-center justify-end gap-3 border-t border-slate-100 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 py-4">
          <button type="button" class="rounded-lg px-4 py-2 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800" @click="close">取消</button>
          <button v-if="allowCreate" type="button" :disabled="loading || !!error" class="rounded-lg border px-4 py-2 text-sm disabled:opacity-40" @click="select(true)">在此新建 Cinema115 并使用</button>
          <button type="button" :disabled="loading || !!error || (!allowRoot && currentCID === '0')" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="select(false)">选择此目录</button>
        </footer>
      </section>
    </dialog>
  </Teleport>
</template>
