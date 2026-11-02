<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import DirectoryPickerDialog from './DirectoryPickerDialog.vue'
import { cd2Request, type CD2DirectoryPage, type CD2DirectorySelection } from '../lib/cd2-api'

const props = withDefaults(defineProps<{
  token: string
  connectionId: string
  revision: number
  title: string
  initialPath?: string
  allowRoot?: boolean
  requireWritable?: boolean
  confirmLabel?: string
}>(), { initialPath: '/', allowRoot: true, requireWritable: false, confirmLabel: '选择此目录' })
const emit = defineEmits<{ close: []; select: [value: CD2DirectorySelection] }>()
const page = ref<CD2DirectoryPage>()
const pathInput = ref(props.initialPath || '/'), requestedPath = ref(props.initialPath || '/')
const loading = ref(true), error = ref('')
const pathEdited = computed(() => !!page.value && pathInput.value !== page.value.path)
const canSelect = computed(() => !!page.value && !loading.value && !error.value && !pathEdited.value && (props.allowRoot || page.value.path !== '/') && (!props.requireWritable || !page.value.readOnly))
const entries = computed(() => (page.value?.directories || []).map(directory => ({
  id: directory.path, path: directory.path,
  name: directory.readOnly ? `${directory.name}（只读）` : directory.name,
  ariaLabel: `打开${directory.name}`,
})))
const notice = computed(() => [
  page.value?.readOnly ? '此目录为只读目录。' : '',
  page.value?.truncated ? '目录内容较多，列表仅展示部分文件夹，可输入完整路径前往。' : '',
].filter(Boolean).join(' '))
let controller: AbortController | undefined, generation = 0

async function openDirectory(path: string) {
  const version = ++generation
  controller?.abort()
  const current = new AbortController()
  controller = current
  const signal = AbortSignal.any([current.signal, AbortSignal.timeout(30000)])
  requestedPath.value = path || '/'
  pathInput.value = requestedPath.value
  loading.value = true
  error.value = ''
  try {
    const data = await cd2Request<CD2DirectoryPage>(props.token, `/directories?path=${encodeURIComponent(requestedPath.value)}&revision=${props.revision}`, { signal })
    if (version !== generation || signal.aborted) return
    if (data.connectionId !== props.connectionId || data.revision !== props.revision) throw new Error('CloudDrive2 连接已变化，请关闭后重新选择目录')
    page.value = data
    pathInput.value = data.path
  } catch (reason) {
    if (version !== generation || current.signal.aborted) return
    error.value = signal.aborted ? '读取目录超时，请检查 CloudDrive2 服务后重试' : reason instanceof Error ? reason.message : '读取 CloudDrive2 目录失败'
  } finally { if (version === generation) loading.value = false }
}

function goUp() { const parent = page.value?.parentPath; if (!loading.value && parent != null) void openDirectory(parent) }
function select() { if (canSelect.value && page.value) emit('select', { path: page.value.path, connectionId: page.value.connectionId, revision: page.value.revision }) }
onMounted(() => { void openDirectory(props.initialPath || '/') })
onBeforeUnmount(() => { generation++; controller?.abort() })
</script>

<template>
  <DirectoryPickerDialog
    v-model:path-input="pathInput"
    :title="title"
    description="浏览 CloudDrive2 授权范围内的网盘、账号和目录。"
    :entries="entries"
    :current-path="page?.path || ''"
    :can-go-up="page?.parentPath != null"
    :loading="loading"
    :error="error"
    :can-select="canSelect"
    :confirm-label="confirmLabel"
    path-editable
    path-placeholder="输入 CD2 绝对路径，或从根目录逐级选择"
    :path-hint="pathEdited && !loading && !error ? '点击“前往”打开输入的目录后再选择。' : requireWritable && page?.readOnly ? '请选择可写目录，以执行移动、重命名和清理。' : !allowRoot && page?.path === '/' ? '请选择网盘或账号中的目录。' : ''"
    loading-text="正在读取 CloudDrive2 目录…"
    :notice="notice"
    @close="emit('close')"
    @open="openDirectory"
    @navigate="openDirectory"
    @up="goUp"
    @root="openDirectory('/')"
    @refresh="openDirectory(requestedPath)"
    @select="select"
  />
</template>
