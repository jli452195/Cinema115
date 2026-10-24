<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'
import OrganizeDirectoryGroup from './OrganizeDirectoryGroup.vue'
import type { OrganizeRow } from '../types/manualOrganize'
import { directoryFileRows, directoryGroups, inDirectory, parentDirectory, rowExcluded, setRangeSelection } from '../utils/organizeSelection'

const props = defineProps<{ rows: OrganizeRow[]; rootPath: string; tv: boolean; episodeEditable: boolean; disabled: boolean; compact?: boolean; flat?: boolean; targetNames?: Record<string, string>; conflicts?: Record<string, string> }>()
const search = ref('')
const onlySelected = ref(false)
const excluded = ref<string[]>([])
const collapsed = ref<string[]>([])
const anchor = ref('')
const expandedPaths = ref<string[]>([])
const bulkOpen = ref(false)
const rootElement = ref<HTMLElement>()
const toolbar = ref<HTMLElement>(), toolbarHeight = ref(0)
let toolbarObserver: ResizeObserver | undefined
onMounted(() => {
  if (!props.compact || !toolbar.value) return
  toolbarHeight.value = toolbar.value.offsetHeight
  toolbarObserver = new ResizeObserver(() => { toolbarHeight.value = toolbar.value?.offsetHeight || 0 })
  toolbarObserver.observe(toolbar.value)
})
onBeforeUnmount(() => toolbarObserver?.disconnect())
const groups = computed(() => props.flat ? [{ path: props.rootPath, label: '所选文件', name: '所选文件', depth: 0, rows: props.rows, descendants: props.rows, children: [] }] : directoryGroups(props.rows, props.rootPath))
const available = computed(() => props.rows.filter(row => !rowExcluded(row, excluded.value)))
const selected = computed(() => available.value.filter(row => row.selected))
const matches = (row: OrganizeRow) => (row.titleOnly ? row.name : row.sourcePath).toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase()) && (!onlySelected.value || row.selected)
const filtered = computed(() => available.value.filter(matches))
const displayedGroups = computed(() => groups.value.filter(group => !collapsed.value.some(root => root !== group.path && inDirectory(group.path, root)) && !excluded.value.some(root => inDirectory(group.path, root)) && group.descendants.some(row => !rowExcluded(row, excluded.value) && matches(row))))
const visiblePaths = computed(() => new Set(displayedGroups.value.map(group => group.path)))
const baseDepth = computed(() => props.compact && !groups.value[0]?.rows.length ? 1 : 0)
const visibleRows = computed(() => (props.flat ? props.rows : directoryFileRows(groups.value[0]!)).filter(row => {
  const directory = props.flat ? props.rootPath : parentDirectory(row.sourcePath)
  return visiblePaths.value.has(directory) && !collapsed.value.includes(directory) && matches(row)
}))
const issueCounts = computed(() => Object.fromEntries(groups.value.map(group => [group.path, group.descendants.filter(row => row.selected && props.conflicts?.[row.id]).length])))
const groupRows = (directory: string) => available.value.filter(row => inDirectory(parentDirectory(row.sourcePath), directory))
const groupChecked = (directory: string) => { const rows = groupRows(directory); return rows.length > 0 && rows.every(row => row.selected) }
const groupPartial = (directory: string) => { const rows = groupRows(directory); return rows.some(row => row.selected) && !rows.every(row => row.selected) }
function bulk(mode: 'all' | 'none' | 'invert') {
  if (props.disabled) return
  for (const row of [...filtered.value]) row.selected = mode === 'invert' ? !row.selected : mode === 'all'
  anchor.value = ''
}
function selectCurrent() {
  if (props.disabled) return
  for (const row of props.rows) row.selected = !rowExcluded(row, excluded.value) && parentDirectory(row.sourcePath) === props.rootPath
  anchor.value = ''
}
function toggleGroup(directory: string) {
  if (props.disabled) return
  const value = !groupChecked(directory)
  for (const row of groupRows(directory)) row.selected = value
  anchor.value = ''
}
function exclude(directory: string) {
  if (props.disabled || directory === props.rootPath) return
  excluded.value = [...excluded.value.filter(root => !inDirectory(root, directory)), directory]
  for (const row of props.rows) if (rowExcluded(row, excluded.value)) row.selected = false
  anchor.value = ''
}
function restore(directory: string) {
  if (props.disabled) return
  excluded.value = excluded.value.filter(root => root !== directory)
  // Restoring visibility never silently selects files.
  anchor.value = ''
}
function toggleRow(row: OrganizeRow, event: MouseEvent) {
  if (props.disabled || rowExcluded(row, excluded.value)) return
  const selected = !row.selected
  if (event.shiftKey) setRangeSelection(visibleRows.value, anchor.value, row.id, selected)
  else row.selected = selected
  anchor.value = row.id
}
function clickRow(row: OrganizeRow, event: MouseEvent) {
  if ((event.target as HTMLElement).closest('button,input,select,label,a')) return
  toggleRow(row, event)
}
function toggleList(list: string[], value: string) { return list.includes(value) ? list.filter(item => item !== value) : [...list, value] }
async function locateIssue(id: string) {
  const row = props.rows.find(row => row.id === id)
  if (!row) return
  search.value = ''; onlySelected.value = false
  const directory = props.flat ? props.rootPath : parentDirectory(row.sourcePath)
  collapsed.value = collapsed.value.filter(root => !inDirectory(directory, root))
  await nextTick()
  const element = rootElement.value?.querySelector<HTMLElement>(`[data-organize-file="${CSS.escape(id)}"]`)
  element?.scrollIntoView({ block: 'center' })
  const target = element?.querySelector<HTMLElement>('[aria-invalid="true"]') || element?.querySelector<HTMLElement>('button')
  target?.focus({ preventScroll: true })
}
defineExpose({ locateIssue })
watch([search, onlySelected], () => { anchor.value = '' })
</script>

<template>
  <div ref="rootElement" class="min-w-0 rounded-xl border border-slate-200 dark:border-slate-700" :class="{ 'compact-selection': compact }" :style="compact ? { '--organize-toolbar-height': `${toolbarHeight}px` } : undefined">
    <div :class="{ 'max-h-[60vh] overflow-y-auto overscroll-contain': !compact }">
      <div ref="toolbar" class="sticky top-0 z-10 space-y-2 border-b border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 shadow-sm">
        <div class="flex flex-wrap items-center justify-between gap-2">
        <p role="status" class="text-xs font-medium text-slate-700 dark:text-slate-300"><template v-if="!compact">已选 {{ selected.length }} / </template>可选 {{ available.length }}<template v-if="!compact || rows.length > available.length"> · 已排除 {{ rows.length - available.length }}</template> · 筛选结果 {{ filtered.length }}</p>
        <details class="text-xs text-slate-500 dark:text-slate-400"><summary class="cursor-pointer">选择操作说明</summary><p class="max-w-lg py-2 leading-5">点击文件名或行内空白切换勾选，Shift + 点击连续选择或取消。搜索不改变已有选择；目录勾选作用于整组及下级，不受搜索限制。</p></details>
        </div>
        <div class="selection-filters flex flex-wrap items-center gap-2">
          <input v-model="search" aria-label="筛选整理文件名" type="search" placeholder="搜索文件名" class="input min-w-0 w-40 flex-1 lg:max-w-xs" />
          <label class="flex items-center gap-2 text-xs"><input v-model="onlySelected" type="checkbox" />只看已选</label>
          <button class="secondary" :aria-expanded="bulkOpen" @click="bulkOpen = !bulkOpen">批量选择 <IconChevronDown :size="14" aria-hidden="true" class="inline" :class="{ 'rotate-180': bulkOpen }" /></button>
        </div>
        <div v-if="bulkOpen" class="flex flex-wrap gap-2 rounded-lg bg-slate-50 p-2 dark:bg-slate-950">
          <button v-if="!compact" class="secondary" :disabled="disabled" @click="selectCurrent">只选当前层</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('all')">全选筛选结果</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('none')">取消筛选结果</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('invert')">反选筛选结果</button>
        </div>
        <details v-if="excluded.length" class="text-xs">
          <summary class="cursor-pointer text-amber-700 dark:text-amber-400">查看已排除目录（{{ excluded.length }}）</summary>
          <p class="my-2 text-slate-500 dark:text-slate-400">仅本次整理排除，文件保留原位。恢复后保持未勾选。</p>
          <div class="max-h-32 overflow-y-auto"><div v-for="directory in excluded" :key="directory" class="mt-2 flex items-center gap-2"><span class="min-w-0 flex-1 break-all">{{ directory }}</span><button class="secondary shrink-0" :disabled="disabled" :aria-label="`恢复目录 ${directory}`" @click="restore(directory)">恢复</button></div></div>
        </details>
        <div v-if="!compact" class="file-columns hidden items-center gap-3 pt-1 text-xs text-slate-500 dark:text-slate-400 lg:grid" :class="tv ? 'tv-columns' : 'movie-columns'"><span>选择 / {{ rows.some(row => row.titleOnly) ? '资源或原文件' : '原文件' }}</span><template v-if="tv"><span>季</span><span>集</span></template></div>
      </div>
      <OrganizeDirectoryGroup v-if="groups[0]" :group="groups[0]" :root-path="rootPath" :visible="visiblePaths" :collapsed="collapsed" :compact="compact" :base-depth="baseDepth" :disabled="disabled" :checked="groupChecked" :partial="groupPartial" :issues="issueCounts" @select="toggleGroup" @toggle="collapsed = toggleList(collapsed, $event); anchor = ''" @exclude="exclude">
        <template #default="{ group }">
          <div v-for="row in group.rows.filter(matches)" :key="row.id" :data-organize-file="row.id" class="file-columns organize-file-row cursor-pointer gap-x-3 gap-y-2 px-3 py-2 hover:bg-blue-50/60 dark:hover:bg-blue-950/40" :class="[row.selected ? 'bg-blue-50/40 dark:bg-blue-950/20' : 'bg-white dark:bg-slate-900', tv ? 'tv-columns' : 'movie-columns', { 'lg:grid lg:items-center': !compact, 'file-has-issue': !!conflicts?.[row.id] }]" @click="clickRow(row, $event)" @mousedown="event => { if (event.shiftKey && !(event.target as HTMLElement).closest('input')) event.preventDefault() }">
            <div class="flex min-w-0 items-start gap-3">
              <input type="checkbox" :checked="row.selected" :aria-label="`选择 ${row.name}`" :disabled="disabled" class="mt-1 h-4 w-4 shrink-0" @click.stop="toggleRow(row, $event)" />
              <div class="min-w-0 flex-1"><button tabindex="-1" class="w-full break-all text-left text-sm leading-5 text-slate-800 dark:text-slate-200 lg:text-xs" :disabled="disabled" :aria-pressed="row.selected" @click.stop="toggleRow(row, $event)">{{ row.name }}</button><p v-if="targetNames?.[row.id]" class="mt-1 break-all text-xs leading-5 text-slate-500 dark:text-slate-400">{{ row.titleOnly ? '预计命名' : '整理后' }}：{{ targetNames[row.id] }}</p><p v-if="conflicts?.[row.id]" role="alert" class="mt-1 break-all text-xs text-red-600 dark:text-red-400">{{ conflicts[row.id] }}</p><slot name="row-details" :row="row" /></div>
              <button v-if="!row.titleOnly" class="shrink-0 text-xs text-blue-600 dark:text-blue-400" :aria-expanded="expandedPaths.includes(row.id)" :aria-label="`查看完整路径 ${row.name}`" @click.stop="expandedPaths = toggleList(expandedPaths, row.id)">路径</button>
            </div>
            <div v-if="tv" class="file-editors ml-7 mt-2 flex flex-wrap gap-2" :class="{ 'lg:contents': !compact }" @click.stop>
                <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">季</span><input v-model.number="row.season" :aria-label="`${row.name} 季`" :disabled="disabled || !episodeEditable" type="number" min="0" max="1000" class="input w-16 sm:w-20 lg:w-full" /></label>
                <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">集</span><input v-model.number="row.episode" :aria-label="`${row.name} 集`" :aria-invalid="!!conflicts?.[row.id]" :disabled="disabled || !episodeEditable" type="number" min="1" max="10000" class="input w-16 sm:w-20 lg:w-full" /></label>
            </div>
            <p v-if="expandedPaths.includes(row.id)" class="col-span-full mt-2 break-all text-xs leading-5 text-slate-500 dark:text-slate-400">{{ row.sourcePath }}</p>
          </div>
        </template>
      </OrganizeDirectoryGroup>
      <p v-if="!filtered.length" class="p-8 text-center text-sm text-slate-500 dark:text-slate-400">没有符合筛选条件的文件，可清除搜索、关闭“只看已选”或恢复已排除目录。</p>
    </div>
  </div>
</template>

<style scoped>
.input { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-2 text-sm text-slate-700 dark:text-slate-300 outline-none focus:border-blue-500 disabled:bg-slate-50 dark:bg-slate-950 disabled:text-slate-400 dark:text-slate-500; }
.secondary { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 text-xs text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 disabled:opacity-40; }
.edit-label { @apply flex flex-col gap-1 text-xs text-slate-500 dark:text-slate-400; }
.compact-selection { container-type: inline-size; }
.organize-file-row { padding-left: calc(28px + var(--directory-indent, 0px)); }
.file-has-issue { box-shadow: inset 3px 0 rgb(239 68 68 / .65); }
.file-has-issue:focus-within { @apply bg-red-50 dark:bg-red-950/30; }
.compact-selection .file-columns { display: grid; grid-template-columns: minmax(0, 1fr); }
.compact-selection .file-editors { align-items: center; margin: 0 0 0 28px; gap: 6px; min-width: 0; }
.compact-selection .edit-label { flex-direction: row; align-items: center; white-space: nowrap; }
.compact-selection .input, .compact-selection .secondary { height: var(--ae-control-height, 36px); padding: var(--ae-control-padding, 6px 10px); font-size: var(--ae-control-font, 13px); }
.compact-selection .edit-label > .input { width: 3.5rem; min-width: 0; padding-inline: 6px; }
@container (min-width: 720px) {
 .compact-selection .tv-columns { grid-template-columns: minmax(0, 1fr) auto; align-items: start; }
 .compact-selection .file-editors { margin: 0; justify-content: flex-end; }
}
@media (max-width: 639px) {
 .compact-selection .selection-filters > input { flex-basis: 100%; width: 100%; max-width: none; }
}
@media (min-width: 1024px) {
  .tv-columns { grid-template-columns: minmax(0, 1fr) 4.5rem 4.5rem; }
  .movie-columns { grid-template-columns: minmax(0, 1fr); }
}
</style>
