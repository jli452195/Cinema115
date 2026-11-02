<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowUp, IconChevronRight, IconFolder, IconRefresh, IconServer } from '@tabler/icons-vue'
import FloatingDialog from './AnimeFloatingDialog.vue'
import { mediaOrganizeRequest, type MediaLocalDirectoryPage } from '../lib/media-organize-api'

const props = withDefaults(defineProps<{ token: string; title: string; initialPath?: string }>(), { initialPath: '' })
const emit = defineEmits<{ close: []; select: [path: string] }>()
const page = ref<MediaLocalDirectoryPage>()
const pathInput = ref(props.initialPath)
const requestedPath = ref(props.initialPath)
const loading = ref(true), error = ref('')
const pathEdited = computed(() => !!page.value && pathInput.value !== page.value.path)
const canSelect = computed(() => !!page.value?.path && !loading.value && !error.value && !pathEdited.value)
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
  <FloatingDialog :title="title" close-label="关闭目录选择器" class="media-local-directory-dialog" @close="emit('close')">
    <template #header>
      <p class="ld-help">浏览服务器或容器内已挂载的目录。</p>
      <form class="ld-path" @submit.prevent="openDirectory(pathInput)">
        <label><span>目录路径</span><input v-model="pathInput" aria-label="目录路径" autocomplete="off" spellcheck="false" placeholder="输入绝对路径，或从根目录逐级选择" /></label>
        <button type="submit" class="ld-button" :disabled="loading">前往</button>
      </form>
      <nav class="ld-navigation" aria-label="本地目录导航">
        <button type="button" class="ld-button" :disabled="loading || page?.parentPath == null" @click="goUp"><IconArrowUp :size="16" aria-hidden="true" />返回上级</button>
        <button type="button" class="ld-button" :disabled="loading" @click="openDirectory('')"><IconServer :size="16" aria-hidden="true" />根目录</button>
        <button type="button" class="ld-button ld-refresh" aria-label="刷新目录" :disabled="loading" @click="openDirectory(requestedPath)"><IconRefresh :size="17" aria-hidden="true" /></button>
      </nav>
    </template>
    <div class="ld-content" :aria-busy="loading">
      <p v-if="loading" role="status" class="ld-empty">正在读取本地目录…</p>
      <div v-else-if="error" class="ld-empty"><p role="alert" class="ld-error">{{ error }}</p><button type="button" class="ld-button" @click="openDirectory(requestedPath)">重新读取</button></div>
      <template v-else-if="page">
        <p v-if="!page.directories.length" class="ld-empty">{{ page.path ? '当前目录没有子文件夹，可直接选择此目录。' : '没有可访问的磁盘，可输入完整路径。' }}</p>
        <div v-else class="ld-list">
          <button v-for="directory in page.directories" :key="directory.path" type="button" class="ld-entry" :title="directory.path" :aria-label="`打开${directory.name}`" @click="openDirectory(directory.path)">
            <IconFolder :size="21" class="ld-folder" aria-hidden="true" /><span>{{ directory.name }}</span><IconChevronRight :size="17" aria-hidden="true" />
          </button>
        </div>
        <p v-if="page.truncated" role="status" class="ld-help ld-truncated">目录内容较多，列表仅展示部分文件夹。可输入完整路径前往。</p>
      </template>
    </div>
    <template #footer>
      <p class="ld-current" :title="page?.path || ''">{{ page?.path || '请选择目录' }}</p>
      <p v-if="pathEdited && !loading && !error" class="ld-help">点击“前往”打开输入的目录后再选择。</p>
      <div class="ld-actions"><button type="button" class="ld-button" @click="emit('close')">取消</button><button type="button" class="ld-button ld-primary" :disabled="!canSelect" @click="select">选择此目录</button></div>
    </template>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.media-local-directory-dialog.anime-floating){width:min(640px,calc(100vw - 32px));height:min(660px,calc(100dvh - 48px))}
.ld-help{font-size:12px;line-height:1.7;color:var(--floating-surface-muted)}
.ld-path{display:flex;align-items:flex-end;gap:8px;margin-top:12px}.ld-path label{display:grid;flex:1;min-width:0;gap:6px;font-size:12px;color:var(--floating-surface-muted)}.ld-path input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.25);border-radius:9px;background:rgb(148 163 184/.06);color:var(--floating-surface-text);font-size:13px;outline:none}.ld-path input:focus{border-color:#3b82f6}
.ld-button{display:inline-flex;flex:none;align-items:center;justify-content:center;gap:5px;min-height:36px;padding:7px 12px;border-radius:9px;background:rgb(148 163 184/.1);color:var(--floating-surface-text);font-size:12px;white-space:nowrap}.ld-button:hover{background:rgb(148 163 184/.18)}.ld-button:disabled{opacity:.4;cursor:default}.ld-navigation{display:flex;gap:8px;margin-top:12px}.ld-refresh{margin-left:auto;padding-inline:10px}.ld-primary{background:#2563eb;color:#fff}.ld-primary:hover{background:#1d4ed8}
.ld-content{min-width:0}.ld-empty{display:flex;min-height:180px;flex-direction:column;align-items:center;justify-content:center;gap:16px;padding:18px 4px;text-align:center;font-size:13px;line-height:1.7;color:var(--floating-surface-muted)}.ld-error{color:#dc2626;overflow-wrap:anywhere}.ld-list{display:grid;gap:6px}.ld-entry{display:flex;align-items:center;gap:12px;min-width:0;min-height:48px;padding:10px 12px;border:1px solid rgb(148 163 184/.17);border-radius:10px;text-align:left;color:var(--floating-surface-text)}.ld-entry:hover{background:rgb(59 130 246/.08);border-color:rgb(59 130 246/.35)}.ld-entry svg{flex:none;color:var(--floating-surface-muted)}.ld-entry .ld-folder{color:#3b82f6}.ld-entry span{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px}.ld-truncated{padding:14px 2px 0}.ld-current{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-family:ui-monospace,monospace;font-size:12px;line-height:1.8;color:var(--floating-surface-muted)}.ld-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:10px}
:global(.dark .media-local-directory-dialog .ld-primary){background:#3769b5}:global(.dark .media-local-directory-dialog .ld-primary:hover){background:#4479ca}:global(.dark .media-local-directory-dialog .ld-error){color:#fca5a5}
@media(max-width:639px){:global(dialog.media-local-directory-dialog.anime-floating){width:calc(100vw - 16px);height:min(660px,calc(100dvh - 24px))}.ld-button,.ld-path input{min-height:44px}.ld-path input{font-size:16px}.ld-entry{min-height:52px}}
</style>
