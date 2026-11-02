<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import DirectoryPickerDialog from './DirectoryPickerDialog.vue'

interface Directory { id: string; name: string }
interface DirectoryPage { directories?: Directory[]; files?: (Directory & { directory: boolean })[]; count?: number; hasMore?: boolean; nextOffset?: number }

const props = withDefaults(defineProps<{ token: string; accountId: string; title?: string; description?: string; endpoint?: string; allowRoot?: boolean; allowCreate?: boolean; saving?: boolean; saveError?: string; confirmLabel?: string; loadPage?: (cid: string, offset: number, signal: AbortSignal) => Promise<DirectoryPage> }>(), { title: '选择下载目录', endpoint: 'cloud-download/paths', allowRoot: true, allowCreate: false, confirmLabel: '选择此目录' })
const emit = defineEmits<{ close: []; select: [value: { id: string; path: string; name: string }]; create: [value: { id: string; path: string; name: string }] }>()
const currentCID = ref('0')
const currentPath = ref<{ id: string; name: string }[]>([])
const directories = ref<Directory[]>([])
const loading = ref(true)
const error = ref('')
const displayPath = computed(() => currentPath.value.length ? '/' + currentPath.value.map(item => item.name).join('/') : '/')
const entries = computed(() => directories.value.map(directory => ({ ...directory, path: `${displayPath.value === '/' ? '' : displayPath.value}/${directory.name}` })))
const lifecycle = new AbortController()
let loadGeneration = 0, loadController: AbortController | undefined

async function loadCID(cid: string) {
  if (props.saving) return
  const generation = ++loadGeneration
  loadController?.abort()
  loadController = new AbortController()
  const loadSignal = loadController.signal
  currentCID.value = cid
  loading.value = true
  error.value = ''
  try {
    const found: Directory[] = []
    for (let offset = 0; ;) {
      const query = `?accountId=${encodeURIComponent(props.accountId)}&cid=${encodeURIComponent(cid)}&offset=${offset}`
      const signal = AbortSignal.any([lifecycle.signal, loadSignal, AbortSignal.timeout(45000)])
      const data = props.loadPage ? await props.loadPage(cid, offset, signal) : await p115Request<DirectoryPage>(props.token, `${props.endpoint}${query}`, { signal })
      if (generation !== loadGeneration || signal.aborted) return
      found.push(...(data.directories ?? data.files?.filter(file => file.directory) ?? []))
      const length = data.files?.length ?? 100
      if (data.files ? offset + length >= (data.count ?? 0) : !data.hasMore) break
      if (!length || offset >= 20000) throw new Error('目录分页不完整，请刷新重试')
      const next = data.nextOffset ?? offset + length
      if (next <= offset) throw new Error('目录分页无进展，请刷新重试')
      offset = next
    }
    if (!lifecycle.signal.aborted && generation === loadGeneration) directories.value = found
  } catch (reason) {
    if (!lifecycle.signal.aborted && generation === loadGeneration) error.value = reason instanceof Error ? reason.message : '读取 115 目录失败'
  } finally {
    if (!lifecycle.signal.aborted && generation === loadGeneration) loading.value = false
  }
}

async function openDirectory(directory?: Directory) {
  if (props.saving) return
  const cid = directory?.id || '0'
  if (directory) currentPath.value = [...currentPath.value, { id: directory.id, name: directory.name }]
  else if (cid === '0') currentPath.value = []
  await loadCID(cid)
}

function goUp() {
  if (props.saving || loading.value) return
  if (!currentPath.value.length) return
  currentPath.value = currentPath.value.slice(0, -1)
  void loadCID(currentPath.value.length ? currentPath.value[currentPath.value.length - 1].id : '0')
}

function openEntry(id: string) {
  if (props.saving || loading.value) return
  const directory = directories.value.find(item => item.id === id)
  if (directory) void openDirectory(directory)
}

function close() { if (!props.saving) emit('close') }
function select(create = false) {
  if (props.saving || loading.value || error.value || (create && !props.allowCreate) || (!create && !props.allowRoot && currentCID.value === '0')) return
  const current = currentPath.value[currentPath.value.length - 1]
  const value = { id: currentCID.value, path: currentPath.value.length ? '/' + currentPath.value.map(item => item.name).join('/') : '/', name: current?.name || '115 根目录' }
  if (create) emit('create', value)
  else emit('select', value)
}

onMounted(() => { void openDirectory() })
onBeforeUnmount(() => {
  lifecycle.abort()
  loadController?.abort()
})
</script>

<template>
  <DirectoryPickerDialog
    :title="title"
    :description="description"
    :entries="entries"
    :current-path="displayPath"
    :can-go-up="currentPath.length > 0"
    :loading="loading"
    :error="error"
    :saving="saving"
    :save-error="saveError"
    :can-select="allowRoot || currentCID !== '0'"
    :confirm-label="confirmLabel"
    :allow-create="allowCreate"
    empty-text="当前目录没有子文件夹"
    loading-text="正在读取 115 目录…"
    @close="close"
    @open="openEntry"
    @up="goUp"
    @root="openDirectory()"
    @refresh="loadCID(currentCID)"
    @select="select(false)"
    @create="select(true)"
  />
</template>
