<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { OrganizeRow } from '../types/manualOrganize'
import { directoryGroups, inDirectory, parentDirectory, rowExcluded, setRangeSelection } from '../utils/organizeSelection'

const props = defineProps<{ rows: OrganizeRow[]; rootPath: string; tv: boolean; episodeEditable: boolean; disabled: boolean }>()
const search = ref('')
const onlySelected = ref(false)
const excluded = ref<string[]>([])
const collapsed = ref<string[]>([])
const anchor = ref('')
const expandedPaths = ref<string[]>([])
const groups = computed(() => directoryGroups(props.rows, props.rootPath))
const available = computed(() => props.rows.filter(row => !rowExcluded(row, excluded.value)))
const selected = computed(() => available.value.filter(row => row.selected))
const matches = (row: OrganizeRow) => row.name.toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase()) && (!onlySelected.value || row.selected)
const filtered = computed(() => available.value.filter(matches))
const displayedGroups = computed(() => groups.value.filter(group => !collapsed.value.some(root => root !== group.path && inDirectory(group.path, root)) && !excluded.value.some(root => inDirectory(group.path, root)) && group.descendants.some(row => !rowExcluded(row, excluded.value) && matches(row))))
const visibleRows = computed(() => displayedGroups.value.flatMap(group => collapsed.value.includes(group.path) ? [] : group.rows.filter(matches)))
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
watch([search, onlySelected], () => { anchor.value = '' })
</script>

<template>
  <div class="min-w-0 rounded-xl border border-slate-200 dark:border-slate-700">
    <div class="max-h-[70vh] overflow-y-auto overscroll-contain">
      <div class="sticky top-0 z-10 space-y-2 border-b border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 shadow-sm">
        <div class="flex flex-wrap items-center justify-between gap-2">
        <p role="status" class="text-xs font-medium text-slate-700 dark:text-slate-300">已选 {{ selected.length }} / 可选 {{ available.length }} · 已排除 {{ rows.length - available.length }} · 筛选结果 {{ filtered.length }}</p>
        <details class="text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><summary class="cursor-pointer">选择操作说明</summary><p class="max-w-lg py-2 leading-5">点击文件名或行内空白切换勾选，Shift + 点击连续选择或取消。搜索不改变已有选择；目录勾选作用于整组及下级，不受搜索限制。</p></details>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <input v-model="search" aria-label="筛选整理文件名" type="search" placeholder="搜索文件名" class="input min-w-0 w-40 flex-1 lg:max-w-xs" />
          <label class="flex items-center gap-2 text-xs"><input v-model="onlySelected" type="checkbox" />只看已选</label>
          <button class="secondary" :disabled="disabled" @click="selectCurrent">只选当前层</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('all')">全选筛选结果</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('none')">取消筛选结果</button>
          <button class="secondary" :disabled="disabled || !filtered.length" @click="bulk('invert')">反选筛选结果</button>
        </div>
        <details v-if="excluded.length" class="text-xs">
          <summary class="cursor-pointer text-amber-700">查看已排除目录（{{ excluded.length }}）</summary>
          <p class="my-2 text-slate-500 dark:text-slate-400 dark:text-slate-500">仅本次整理排除，文件保留原位。恢复后保持未勾选。</p>
          <div class="max-h-32 overflow-y-auto"><div v-for="directory in excluded" :key="directory" class="mt-2 flex items-center gap-2"><span class="min-w-0 flex-1 break-all">{{ directory }}</span><button class="secondary shrink-0" :disabled="disabled" :aria-label="`恢复目录 ${directory}`" @click="restore(directory)">恢复</button></div></div>
        </details>
        <div class="file-columns hidden items-center gap-3 pt-1 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 lg:grid" :class="tv ? 'tv-columns' : 'movie-columns'"><span>选择 / 原文件</span><template v-if="tv"><span>季</span><span>集</span><span>结束集</span></template><span>版本标记（可修改）</span></div>
      </div>
      <section v-for="group in displayedGroups" :key="group.path" class="border-b border-slate-200 dark:border-slate-700 last:border-b-0">
        <div class="flex flex-wrap items-center gap-2 bg-slate-50 dark:bg-slate-950 px-3 py-2">
          <input type="checkbox" :checked="groupChecked(group.path)" :indeterminate="groupPartial(group.path)" :disabled="disabled" :aria-label="`选择目录 ${group.label} 及下级`" @change="toggleGroup(group.path)" />
          <button class="min-w-0 flex-1 break-all text-left text-sm font-medium text-slate-700 dark:text-slate-300" :aria-expanded="!collapsed.includes(group.path)" :aria-label="`展开或折叠 ${group.label}`" @click="collapsed = toggleList(collapsed, group.path); anchor = ''">{{ collapsed.includes(group.path) ? '▸' : '▾' }} {{ group.label }} <span class="text-xs font-normal text-slate-500 dark:text-slate-400 dark:text-slate-500">· 本层 {{ group.rows.length }} / 含下级 {{ group.descendants.length }} 个</span></button>
          <button v-if="group.path !== rootPath" class="secondary" :disabled="disabled" :aria-label="`排除目录 ${group.label} 及下级`" @click="exclude(group.path)">本次排除</button>
        </div>
        <p v-if="group.path === rootPath" class="break-all px-3 py-2 text-xs text-slate-400 dark:text-slate-500">{{ rootPath }}</p>
        <div v-if="!collapsed.includes(group.path)" class="divide-y divide-slate-100 dark:divide-slate-800">
          <div v-for="row in group.rows.filter(matches)" :key="row.id" :data-organize-file="row.id" class="file-columns cursor-pointer gap-x-3 gap-y-2 px-3 py-2 hover:bg-blue-50/60 lg:grid lg:items-center" :class="[row.selected ? 'bg-blue-50/40' : 'bg-white dark:bg-slate-900', tv ? 'tv-columns' : 'movie-columns']" @click="clickRow(row, $event)" @mousedown="event => { if (event.shiftKey && !(event.target as HTMLElement).closest('input')) event.preventDefault() }">
            <div class="flex min-w-0 items-start gap-3">
              <input type="checkbox" :checked="row.selected" :aria-label="`选择 ${row.name}`" :disabled="disabled" class="mt-1 h-4 w-4 shrink-0" @click.stop="toggleRow(row, $event)" />
              <button class="min-w-0 flex-1 break-all text-left text-sm leading-5 text-slate-800 dark:text-slate-200 lg:text-xs" :disabled="disabled" :aria-pressed="row.selected" @click.stop="toggleRow(row, $event)">{{ row.name }}</button>
              <button class="shrink-0 text-xs text-blue-600" :aria-expanded="expandedPaths.includes(row.id)" :aria-label="`查看完整路径 ${row.name}`" @click.stop="expandedPaths = toggleList(expandedPaths, row.id)">路径</button>
            </div>
            <div class="ml-7 mt-2 flex flex-wrap gap-2 lg:contents" @click.stop>
              <template v-if="tv">
                <label class="edit-label"><span class="lg:hidden">季</span><input v-model.number="row.season" :aria-label="`${row.name} 季`" :disabled="disabled || !episodeEditable" type="number" min="0" max="1000" class="input w-20 lg:w-full" /></label>
                <label class="edit-label"><span class="lg:hidden">集</span><input v-model.number="row.episode" :aria-label="`${row.name} 集`" :disabled="disabled || !episodeEditable" type="number" min="1" max="10000" class="input w-20 lg:w-full" /></label>
                <label class="edit-label"><span class="lg:hidden">结束集</span><input v-model.number="row.endEpisode" :aria-label="`${row.name} 结束集`" :disabled="disabled || !episodeEditable" type="number" min="0" max="10000" class="input w-20 lg:w-full" /></label>
              </template>
              <label class="edit-label min-w-0 flex-1"><span class="lg:hidden">版本标记</span><input v-model="row.suffix" :aria-label="`${row.name} 版本标记`" :disabled="disabled" maxlength="100" placeholder="如 1080p WEB-DL" class="input w-full min-w-32" /></label>
            </div>
            <p v-if="expandedPaths.includes(row.id)" class="col-span-full mt-2 break-all text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ row.sourcePath }}</p>
          </div>
        </div>
      </section>
      <p v-if="!filtered.length" class="p-8 text-center text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">没有符合筛选条件的文件，可清除搜索、关闭“只看已选”或恢复已排除目录。</p>
    </div>
  </div>
</template>

<style scoped>
.input { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-2 text-sm text-slate-700 dark:text-slate-300 outline-none focus:border-blue-500 disabled:bg-slate-50 dark:bg-slate-950 disabled:text-slate-400 dark:text-slate-500; }
.secondary { @apply rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2 text-xs text-slate-600 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 disabled:opacity-40; }
.edit-label { @apply flex flex-col gap-1 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500; }
@media (min-width: 1024px) {
  .tv-columns { grid-template-columns: minmax(0, 1fr) 4.5rem 4.5rem 5rem 11rem; }
  .movie-columns { grid-template-columns: minmax(0, 1fr) 11rem; }
}
</style>
