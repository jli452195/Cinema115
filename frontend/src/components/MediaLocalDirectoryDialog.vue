<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import DirectoryPickerDialog from './DirectoryPickerDialog.vue'
import { mediaOrganizeRequest, type MediaLocalDirectoryPage } from '../lib/media-organize-api'

const props = withDefaults(defineProps<{ token: string; title: string; initialPath?: string }>(), { initialPath: '' })
const emit = defineEmits<{ close: []; select: [path: string] }>()
const page = ref<MediaLocalDirectoryPage>()
const pathInput = ref(props.initialPath)
const requestedPath = ref(props.initialPath)
const loading = ref(true), error = ref('')
const pathEdited = computed(() => !!page.value && pathInput.value !== page.value.path)
const canSelect = computed(() => !!page.value?.path && !loading.value && !error.value && !pathEdited.value)
const entries = computed(() => (page.value?.directories || []).map(directory => ({ id: directory.path, name: directory.name, path: directory.path, ariaLabel: `打开${directory.name}` })))
let controller: AbortController | undefined, generation = 0

async function openDirectory(path: string) {
  const version = ++generation
  controller?.abort()
  controller = new AbortController()
  const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(15000)])
  requestedPath.value = path
  pathInput.value = path
  loading.value = true
  error.value = ''
  try {
    const data = await mediaOrganizeRequest<MediaLocalDirectoryPage>(props.token, `/directories?path=${encodeURIComponent(path)}`, { signal })
    if (version !== generation) return
    page.value = data
    pathInput.value = data.path
  } catch (reason) {
    if (version !== generation || controller.signal.aborted) return
    error.value = signal.aborted ? '读取目录超时，请检查挂载状态后重试' : reason instanceof Error ? reason.message : '读取本地目录失败'
  } finally {
    if (version === generation) loading.value = false
  }
}

function goUp() { const parent = page.value?.parentPath; if (!loading.value && parent != null) void openDirectory(parent) }
function select() { if (canSelect.value && page.value) emit('select', page.value.path) }
onMounted(() => { void openDirectory(props.initialPath) })
onBeforeUnmount(() => { generation++; controller?.abort() })
</script>

<template>
  <DirectoryPickerDialog
    v-model:path-input="pathInput"
    :title="title"
    description="浏览服务器或容器内已挂载的目录。"
    :entries="entries"
    :current-path="page?.path || ''"
    :can-go-up="page?.parentPath != null"
    :loading="loading"
    :error="error"
    :can-select="canSelect"
    path-editable
    path-placeholder="输入绝对路径，或从根目录逐级选择"
    :path-hint="pathEdited && !loading && !error ? '点击“前往”打开输入的目录后再选择。' : ''"
    :empty-text="page?.path ? '当前目录没有子文件夹，可直接选择此目录。' : '没有可访问的磁盘，可输入完整路径。'"
    loading-text="正在读取本地目录…"
    :notice="page?.truncated ? '目录内容较多，列表仅展示部分文件夹。可输入完整路径前往。' : ''"
    @close="emit('close')"
    @open="openDirectory"
    @navigate="openDirectory"
    @up="goUp"
    @root="openDirectory('')"
    @refresh="openDirectory(requestedPath)"
    @select="select"
  />
</template>
