<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import { STRM_ROOT } from '../utils/strmDirectory'

interface DirectoryEntry {
  name: string
  path: string
  displayPath: string
}

interface DirectoryResponse {
  path: string
  displayPath: string
  directories: DirectoryEntry[]
  truncated: boolean
}

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: []; select: [path: string] }>()
const dialog = ref<HTMLDialogElement>()
const currentPath = ref('')
const displayPath = ref('/strm')
const directories = ref<DirectoryEntry[]>([])
const loading = ref(true)
const error = ref('')
const truncated = ref(false)
let controller: AbortController | undefined
let requestID = 0
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow

async function openDirectory(path: string) {
  const id = ++requestID
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const query = path ? `?path=${encodeURIComponent(path)}` : ''
    const data = await p115Request<DirectoryResponse>(props.token, `share/output-directories${query}`, { signal: controller.signal })
    if (id !== requestID) return
    currentPath.value = data.path
    displayPath.value = data.displayPath
    directories.value = data.directories
    truncated.value = data.truncated
  } catch (reason) {
    if (id !== requestID || controller.signal.aborted) return
    error.value = reason instanceof Error ? reason.message : '读取媒体库目录失败'
  } finally {
    if (id === requestID) loading.value = false
  }
}

function goUp() {
  const parts = currentPath.value.split('/').filter(Boolean)
  parts.pop()
  void openDirectory(parts.join('/'))
}

function close() {
  controller?.abort()
  emit('close')
}

function select() {
  if (!loading.value && !error.value) emit('select', currentPath.value || STRM_ROOT)
}

onMounted(() => {
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
  void openDirectory('')
})

onBeforeUnmount(() => {
  requestID++
  controller?.abort()
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="strm-directory-title" class="m-auto h-[min(720px,calc(100dvh-2rem))] w-[calc(100%-2rem)] max-w-xl overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-0 text-slate-700 dark:text-slate-300 shadow-2xl backdrop:bg-slate-950/50" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <section class="flex h-full min-h-0 flex-col">
        <header class="flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 px-5 py-4">
          <h2 id="strm-directory-title" class="flex items-center gap-2 font-semibold text-slate-900 dark:text-slate-100">
            <svg class="h-5 w-5 text-blue-600 dark:text-blue-400" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M3 5.75A2.75 2.75 0 0 1 5.75 3h3.69c.73 0 1.43.29 1.95.8l1.06 1.07c.14.14.33.22.53.22h5.27A2.75 2.75 0 0 1 21 7.84v.41H3v-2.5Zm0 4h18l-1.2 8.41A2.75 2.75 0 0 1 17.08 20H5.92a2.75 2.75 0 0 1-2.72-2.36L2 9.75h1Z"/></svg>
            添加 STRM 目录
          </h2>
          <button type="button" aria-label="关闭目录选择器" class="rounded-lg px-2 py-1 text-2xl leading-none text-slate-400 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-700 dark:hover:text-slate-300" @click="close">×</button>
        </header>

        <div class="flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 bg-slate-50 dark:bg-slate-950 px-5 py-3">
          <p class="flex min-w-0 items-center gap-2 text-sm font-medium text-slate-700 dark:text-slate-300">
            <svg class="h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 7.5h6l2 2h10v9H3z"/><path d="M3 7.5V5h6l2 2h8v2.5"/></svg>
            <span class="truncate">{{ displayPath }}</span>
          </p>
          <button type="button" :disabled="!currentPath || loading" aria-label="返回上级目录" class="rounded-lg p-2 text-slate-500 dark:text-slate-400 hover:bg-white dark:hover:bg-slate-800 dark:bg-slate-900 hover:text-blue-600 disabled:opacity-30" @click="goUp">
            <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m18 15-6-6-6 6"/></svg>
          </button>
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto p-4" :aria-busy="loading">
          <div v-if="loading" class="grid h-full min-h-40 place-items-center text-sm text-slate-400 dark:text-slate-400">正在读取真实文件夹…</div>
          <div v-else-if="error" class="grid min-h-40 place-items-center px-5 text-center">
            <div><p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p><button type="button" class="mt-4 text-sm text-blue-600 dark:text-blue-400 hover:underline" @click="openDirectory(currentPath)">重新读取</button></div>
          </div>
          <div v-else-if="directories.length === 0" class="grid h-full min-h-40 place-items-center text-center text-sm text-slate-400 dark:text-slate-400">当前目录下没有可选择的子文件夹</div>
          <div v-else class="space-y-2">
            <button v-for="directory in directories" :key="directory.path" type="button" class="flex w-full items-center gap-3 rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-3 text-left transition hover:border-blue-300 hover:bg-blue-50/50 dark:hover:bg-slate-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500" @click="openDirectory(directory.path)">
              <span class="grid h-10 w-10 shrink-0 place-items-center rounded-lg border border-blue-100 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/30 text-amber-500">
                <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M3 7.5h6l2 2h10v9H3z"/><path d="M3 7.5V5h6l2 2h8v2.5"/></svg>
              </span>
              <span class="min-w-0 flex-1"><span class="block truncate text-sm font-semibold text-slate-800 dark:text-slate-200">{{ directory.name }}</span><span class="mt-1 block truncate text-xs text-slate-400 dark:text-slate-400">{{ directory.displayPath }}</span></span>
              <svg class="h-5 w-5 shrink-0 text-slate-400 dark:text-slate-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>
            </button>
            <p v-if="truncated" class="px-2 py-3 text-xs text-amber-600 dark:text-amber-400">目录过多，仅显示前 500 个文件夹。</p>
          </div>
        </div>

        <footer class="flex shrink-0 items-center justify-end gap-3 border-t border-slate-100 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 py-4">
          <button type="button" class="rounded-lg px-4 py-2 text-sm text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800" @click="close">取消</button>
          <button type="button" :disabled="loading || !!error" class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40" @click="select">✓ {{ currentPath ? '选择此目录' : '选择 STRM 根目录' }}</button>
        </footer>
      </section>
    </dialog>
  </Teleport>
</template>
