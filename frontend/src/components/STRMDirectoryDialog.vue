<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import { STRM_ROOT } from '../utils/strmDirectory'
import DirectoryPickerDialog from './DirectoryPickerDialog.vue'

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

const props = withDefaults(defineProps<{ token: string; title?: string }>(), { title: '添加 STRM 目录' })
const emit = defineEmits<{ close: []; select: [path: string] }>()
const currentPath = ref('')
const displayPath = ref('/strm')
const directories = ref<DirectoryEntry[]>([])
const loading = ref(true)
const error = ref('')
const truncated = ref(false)
const atRoot = computed(() => !currentPath.value || currentPath.value === STRM_ROOT)
const entries = computed(() => directories.value.map(directory => ({ id: directory.path, name: directory.name, path: directory.displayPath })))
let controller: AbortController | undefined
let requestID = 0

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
  if (loading.value || atRoot.value) return
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

onMounted(() => { void openDirectory('') })
onBeforeUnmount(() => { requestID++; controller?.abort() })
</script>

<template>
  <DirectoryPickerDialog
    :title="title"
    :entries="entries"
    :current-path="displayPath"
    :can-go-up="!atRoot"
    :loading="loading"
    :error="error"
    :confirm-label="atRoot ? '选择 STRM 根目录' : '选择此目录'"
    empty-text="当前目录下没有可选择的子文件夹"
    loading-text="正在读取真实文件夹…"
    :notice="truncated ? '目录过多，仅显示前 500 个文件夹。' : ''"
    @close="close"
    @open="openDirectory"
    @up="goUp"
    @root="openDirectory('')"
    @refresh="openDirectory(currentPath)"
    @select="select"
  />
</template>
