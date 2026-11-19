<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { IconChevronDown, IconChevronUp, IconEdit } from '@tabler/icons-vue'
import OrganizeDirectoryGroup from './OrganizeDirectoryGroup.vue'
import type { OrganizeRow } from '../types/organize'
import { directoryFileRows, directoryGroups, inDirectory, parentDirectory, rowExcluded, setRangeSelection } from '../utils/organizeSelection'

const props = defineProps<{ rows: OrganizeRow[]; rootPath: string; tv: boolean; episodeEditable: boolean; disabled: boolean; compact?: boolean; flat?: boolean; emptyText?: string; targetNames?: Record<string, string>; conflicts?: Record<string, string> }>()
const excluded = ref<string[]>([])
const collapsed = ref<string[]>([])
const anchor = ref('')
const expandedPaths = ref<string[]>([])
const expandedEditors = ref(new Set<string>())
const instanceId = useId()
const rootElement = ref<HTMLElement>()
const groups = computed(() => props.flat ? [{ path: props.rootPath, label: '所选文件', name: '所选文件', depth: 0, rows: props.rows, descendants: props.rows, children: [] }] : directoryGroups(props.rows, props.rootPath))
const available = computed(() => props.rows.filter(row => !rowExcluded(row, excluded.value)))
const displayedGroups = computed(() => groups.value.filter(group => !collapsed.value.some(root => root !== group.path && inDirectory(group.path, root)) && !excluded.value.some(root => inDirectory(group.path, root)) && group.descendants.some(row => !rowExcluded(row, excluded.value))))
const visiblePaths = computed(() => new Set(displayedGroups.value.map(group => group.path)))
const baseDepth = computed(() => props.compact && !groups.value[0]?.rows.length ? 1 : 0)
const visibleRows = computed(() => (props.flat ? props.rows : directoryFileRows(groups.value[0]!)).filter(row => {
  const directory = props.flat ? props.rootPath : parentDirectory(row.sourcePath)
  return visiblePaths.value.has(directory) && !collapsed.value.includes(directory)
}))
const issueCounts = computed(() => Object.fromEntries(groups.value.map(group => [group.path, group.descendants.filter(row => row.selected && props.conflicts?.[row.id]).length])))
const groupRows = (directory: string) => available.value.filter(row => inDirectory(parentDirectory(row.sourcePath), directory))
const groupChecked = (directory: string) => { const rows = groupRows(directory); return rows.length > 0 && rows.every(row => row.selected) }
const groupPartial = (directory: string) => { const rows = groupRows(directory); return rows.some(row => row.selected) && !rows.every(row => row.selected) }
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
  // 恢复目录只改变可见性，不自动勾选文件。
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
  // 插槽中的字幕详情和交互控件只执行自身操作，不能连带切换整行勾选。
  if ((event.target as Element).closest('button,input,select,textarea,label,a,details,summary,[role="button"],[contenteditable="true"]')) return
  toggleRow(row, event)
}
function toggleList(list: string[], value: string) { return list.includes(value) ? list.filter(item => item !== value) : [...list, value] }
const rowId = (row: OrganizeRow, part: string) => `${instanceId}-${encodeURIComponent(row.id)}-${part}`
const plannedName = (row: OrganizeRow) => {
  const name = props.targetNames?.[row.id]?.trim()
  return name && name !== '等待命名预览' ? name : ''
}
const detailsAction = (row: OrganizeRow) => expandedPaths.value.includes(row.id) ? '收起' : props.compact ? row.titleOnly ? '原名' : '详情' : '路径'
const showEditors = (row: OrganizeRow) => !props.compact || row.selected || !!props.conflicts?.[row.id] || expandedEditors.value.has(row.id)
const episodeSummary = (row: OrganizeRow) => {
  const season = Number.isInteger(row.season) && row.season >= 0 ? row.season === 0 ? '特别篇' : `第 ${row.season} 季` : '季待定'
  const episode = Number.isInteger(row.episode) && row.episode > 0 ? `第 ${row.episode} 集` : '集待定'
  return `${season} · ${episode}`
}
const fileElement = (id: string) => rootElement.value?.querySelector<HTMLElement>(`[data-organize-file="${CSS.escape(id)}"]`)
async function toggleEditors(row: OrganizeRow) {
  if (props.disabled) return
  if (expandedEditors.value.has(row.id)) {
    expandedEditors.value.delete(row.id)
    await nextTick()
    fileElement(row.id)?.querySelector<HTMLElement>('.episode-summary')?.focus({ preventScroll: true })
  } else {
    if (!props.episodeEditable) return
    // 编辑器展开状态独立于勾选，失焦和命名预览更新都不收起。
    expandedEditors.value.add(row.id)
    await nextTick()
    fileElement(row.id)?.querySelector<HTMLElement>('.file-editors input:not(:disabled)')?.focus({ preventScroll: true })
  }
}
async function locateIssue(id: string) {
  const row = props.rows.find(row => row.id === id)
  if (!row) return
  revealRows([id])
  expandedEditors.value.add(id)
  await nextTick()
  const element = fileElement(id)
  element?.scrollIntoView({ block: 'center' })
  const target = element?.querySelector<HTMLElement>('input[aria-invalid="true"]:not(:disabled)')
    || element?.querySelector<HTMLElement>('.file-editors input:not(:disabled)')
    || element?.querySelector<HTMLElement>('input[type="checkbox"]:not(:disabled)')
    || element?.querySelector<HTMLElement>('button:not(:disabled)')
  target?.focus({ preventScroll: true })
}
function revealRows(ids: string[]) {
  const paths = props.rows.filter(row => ids.includes(row.id)).map(row => parentDirectory(row.sourcePath))
  excluded.value = excluded.value.filter(root => !paths.some(directory => inDirectory(directory, root)))
  collapsed.value = collapsed.value.filter(root => !paths.some(directory => inDirectory(directory, root)))
}
defineExpose({ locateIssue, revealRows })
</script>

<template>
  <div ref="rootElement" class="file-selection min-w-0 rounded-xl border border-line-default" :class="{ 'compact-selection': compact }">
    <div v-if="compact && $slots.header" class="selection-header"><slot name="header" /></div>
    <details v-if="excluded.length" class="border-b border-line-default px-3 py-2 text-xs">
      <summary class="cursor-pointer rounded text-content-secondary">查看已排除目录（{{ excluded.length }}）</summary>
      <p class="my-2 text-content-secondary">仅本次排除，文件保留原位。恢复后保持未勾选。</p>
      <div class="max-h-32 overflow-y-auto"><div v-for="directory in excluded" :key="directory" class="mt-2 flex items-center gap-2"><span class="min-w-0 flex-1 break-all">{{ directory }}</span><button type="button" class="secondary shrink-0" :disabled="disabled" :aria-label="`恢复目录 ${directory}`" @click="restore(directory)">恢复</button></div></div>
    </details>
    <div data-auto-scrollbar :class="{ 'max-h-[60vh] overflow-y-auto overscroll-contain': !compact }">
      <div v-if="!compact" class="file-columns sticky top-0 z-10 hidden items-center gap-3 border-b border-line-default bg-surface-card px-3 py-2 text-xs text-content-secondary lg:grid" :class="tv ? 'tv-columns' : 'movie-columns'"><span>选择 / {{ rows.some(row => row.titleOnly) ? '资源或原文件' : '原文件' }}</span><template v-if="tv"><span>季</span><span>集</span></template></div>
      <OrganizeDirectoryGroup v-if="groups[0]" :group="groups[0]" :root-path="rootPath" :visible="visiblePaths" :collapsed="collapsed" :compact="compact" :base-depth="baseDepth" :disabled="disabled" :checked="groupChecked" :partial="groupPartial" :issues="issueCounts" @select="toggleGroup" @toggle="collapsed = toggleList(collapsed, $event); anchor = ''" @exclude="exclude">
        <template #default="{ group }">
          <div v-for="row in group.rows" :key="row.id" :data-organize-file="row.id" class="file-columns organize-file-row gap-x-3 gap-y-2 px-3 py-2" :class="[tv ? 'tv-columns' : 'movie-columns', { 'lg:grid lg:items-center': !compact, 'file-selected': row.selected, 'file-has-issue': !!conflicts?.[row.id], 'file-disabled': disabled }]" @click="clickRow(row, $event)" @mousedown="event => { if (event.shiftKey && !(event.target as HTMLElement).closest('input')) event.preventDefault() }">
            <div class="file-main flex min-w-0 items-start gap-2">
              <label class="file-check"><input type="checkbox" :checked="row.selected" :aria-label="`选择 ${row.name}`" :disabled="disabled" @click.stop="toggleRow(row, $event)" /></label>
              <div class="file-content min-w-0 flex-1">
                <button type="button" tabindex="-1" class="file-name" :disabled="disabled" :aria-pressed="row.selected" :title="compact && plannedName(row) ? plannedName(row) : row.name" @click.stop="toggleRow(row, $event)"><template v-if="compact && plannedName(row)"><span v-if="row.titleOnly" class="file-name-label">预计命名：</span><span>{{ plannedName(row) }}</span></template><span v-else>{{ row.name }}</span></button>
                <p v-if="compact && plannedName(row)" class="file-original" :title="row.name"><span>{{ row.titleOnly ? '资源' : '原名' }}：</span><span>{{ row.name }}</span></p>
                <p v-else-if="targetNames?.[row.id]" class="mt-1 break-all text-xs leading-5 text-content-secondary">{{ compact ? targetNames[row.id] : `${row.titleOnly ? '预计命名' : '整理后'}：${targetNames[row.id]}` }}</p>
                <p v-if="conflicts?.[row.id]" :id="rowId(row, 'error')" role="alert" class="mt-1 break-all text-xs leading-5 text-status-danger">{{ conflicts[row.id] }}</p>
                <slot name="row-details" :row="row" />
              </div>
              <button v-if="compact || !row.titleOnly" type="button" class="path-toggle" :aria-expanded="expandedPaths.includes(row.id)" :aria-controls="rowId(row, 'details')" :aria-label="`${detailsAction(row)}：${row.titleOnly ? '完整资源名' : '完整路径'} ${row.name}`" @click.stop="expandedPaths = toggleList(expandedPaths, row.id)"><span>{{ detailsAction(row) }}</span><component :is="expandedPaths.includes(row.id) ? IconChevronUp : IconChevronDown" :size="13" aria-hidden="true" /></button>
            </div>
            <div v-if="tv && showEditors(row)" :id="rowId(row, 'editors')" class="file-editors ml-7 mt-2 flex flex-wrap gap-2" :class="{ 'lg:contents': !compact }" @click.stop>
              <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">季</span><input v-model.number="row.season" :aria-label="`${row.name} 季`" :aria-describedby="conflicts?.[row.id] ? rowId(row, 'error') : undefined" :disabled="disabled || !episodeEditable" type="number" min="0" max="1000" class="input w-16 sm:w-20 lg:w-full" /></label>
              <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">集</span><input v-model.number="row.episode" :aria-label="`${row.name} 集`" :aria-invalid="!!conflicts?.[row.id]" :aria-describedby="conflicts?.[row.id] ? rowId(row, 'error') : undefined" :disabled="disabled || !episodeEditable" type="number" min="1" max="10000" class="input w-16 sm:w-20 lg:w-full" /></label>
              <button v-if="compact && expandedEditors.has(row.id) && !row.selected && !conflicts?.[row.id]" type="button" class="editor-collapse" :disabled="disabled" :aria-label="`收起季集编辑 ${row.name}`" :aria-controls="rowId(row, 'editors')" aria-expanded="true" @click="toggleEditors(row)">收起</button>
            </div>
            <button v-else-if="tv && compact" type="button" class="episode-summary" :disabled="disabled || !episodeEditable" :aria-label="`编辑季集 ${row.name}`" :aria-controls="rowId(row, 'editors')" aria-expanded="false" @click.stop="toggleEditors(row)"><span>{{ episodeSummary(row) }}</span><IconEdit :size="14" aria-hidden="true" /></button>
            <div v-if="expandedPaths.includes(row.id)" :id="rowId(row, 'details')" class="file-path-details col-span-full mt-2 break-all text-xs leading-5 text-content-secondary" @click.stop>
              <p v-if="compact">{{ row.titleOnly ? '完整资源名' : '完整原名' }}：{{ row.name }}</p>
              <p v-if="!row.titleOnly">{{ compact ? '完整路径：' : '' }}{{ row.sourcePath }}</p>
            </div>
          </div>
        </template>
      </OrganizeDirectoryGroup>
      <p v-if="!available.length" class="p-8 text-center text-sm text-content-secondary">{{ !rows.length && emptyText ? emptyText : excluded.length ? '所有目录均已排除，可在上方恢复。' : emptyText || '没有可整理的文件。' }}</p>
    </div>
  </div>
</template>

<style scoped>
.file-selection { color: rgb(var(--content-primary)); background: var(--organize-card-bg, rgb(var(--surface-card))); border-color: var(--organize-border, rgb(var(--line-default))); }
.selection-header { padding: 6px 12px; border-bottom: 1px solid var(--organize-border, rgb(var(--line-default))); border-radius: 12px 12px 0 0; background: var(--organize-sub-bg, rgb(var(--surface-page))); }
.input { @apply rounded-lg border px-2 py-2 text-sm text-content-primary outline-none focus:border-focus; background: var(--organize-input-bg, rgb(var(--surface-card))); border-color: var(--organize-control-border, rgb(var(--line-control))); }
.input:disabled { @apply cursor-not-allowed border-line-default bg-surface-page text-content-secondary; opacity: .6; }
.input[aria-invalid="true"] { border-color: rgb(var(--status-danger)); }
.secondary { @apply rounded-lg border border-line-default bg-surface-card px-3 py-2 text-xs text-content-secondary enabled:hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-40; }
.edit-label { @apply flex flex-col gap-1 text-xs text-content-secondary; }
.file-selection :is(button, input, summary):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.file-check { display: grid; flex-shrink: 0; place-items: center; width: 32px; min-height: 32px; margin: -4px 0 -4px -8px; cursor: pointer; }
.file-check input { width: 16px; height: 16px; accent-color: rgb(var(--action-primary)); cursor: inherit; }
.file-check:has(input:disabled) { cursor: not-allowed; opacity: .5; }
.file-name { width: 100%; border-radius: 4px; text-align: left; overflow-wrap: anywhere; font-size: 14px; line-height: 20px; color: rgb(var(--content-primary)); }
.file-name:disabled { cursor: default; }
.file-original { margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; line-height: 20px; color: rgb(var(--content-secondary)); }
.path-toggle, .editor-collapse { @apply inline-flex shrink-0 items-center justify-center gap-1 rounded-md px-1.5 text-xs text-action-link enabled:hover:bg-selection-surface disabled:cursor-not-allowed disabled:opacity-40; min-height: 32px; }
.path-toggle { margin: -6px -4px 0 0; color: rgb(var(--content-secondary)); }
.path-toggle:hover, .path-toggle[aria-expanded='true'] { color: rgb(var(--action-link)); }
.episode-summary { @apply inline-flex items-center justify-center gap-1.5 rounded-lg border border-transparent px-2 text-xs text-content-secondary enabled:hover:border-focus enabled:hover:bg-selection-surface enabled:hover:text-action-link disabled:cursor-not-allowed disabled:opacity-50; background: var(--organize-sub-bg, rgb(var(--surface-hover) / .5)); justify-self: start; min-height: var(--dialog-control-height, 36px); margin-left: 32px; }
.compact-selection { container-type: inline-size; }
.organize-file-row { background: var(--organize-row-bg, rgb(var(--surface-card))); padding-left: calc(28px + var(--directory-indent, 0px)); scroll-margin-top: calc(var(--organize-sticky-offset, 0px) + 12px); cursor: pointer; }
.organize-file-row:not(.file-disabled):hover { background: var(--organize-row-hover, rgb(var(--surface-hover) / .6)); }
.file-selected { background: rgb(var(--selection-surface)); box-shadow: inset 3px 0 rgb(var(--action-primary) / .65); }
.file-selected:not(.file-disabled):hover { background: rgb(var(--selection-surface)); }
.file-disabled { cursor: default; }
.file-has-issue { box-shadow: inset 3px 0 rgb(var(--status-danger) / .7); }
.file-has-issue:focus-within { background: rgb(var(--status-danger-surface)); }
.file-path-details { padding-left: 32px; }
.compact-selection .file-columns { display: grid; grid-template-columns: minmax(0, 1fr); }
.compact-selection .organize-file-row { padding-block: 10px; }
.compact-selection .file-name { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; font-size: 13px; font-weight: 600; }
.file-name-label { font-size: 12px; font-weight: 400; color: rgb(var(--content-secondary)); }
.compact-selection .file-editors { align-items: center; margin: 0 0 0 32px; gap: 8px; min-width: 0; }
.compact-selection .edit-label { flex-direction: row; align-items: center; white-space: nowrap; }
.compact-selection .input, .compact-selection .secondary { height: var(--dialog-control-height, 36px); padding: var(--dialog-control-padding, 6px 10px); font-size: var(--dialog-control-font, 13px); }
.compact-selection .edit-label > .input { width: 4rem; min-width: 0; padding-inline: 6px; }
@container (min-width: 720px) {
 .compact-selection .tv-columns { grid-template-columns: minmax(0, 1fr) auto; align-items: start; }
 .compact-selection .file-editors { margin: 0; justify-content: flex-end; }
 .compact-selection .episode-summary { margin: 0; justify-self: end; }
}
@media (min-width: 1024px) {
  .tv-columns { grid-template-columns: minmax(0, 1fr) 4.5rem 4.5rem; }
  .movie-columns { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 639px), (pointer: coarse) {
  .file-check { width: var(--sheet-touch-size, 44px); min-height: var(--sheet-touch-size, 44px); margin: -6px 0 -6px -14px; }
  .path-toggle, .editor-collapse, .episode-summary, .secondary { min-height: var(--sheet-touch-size, 44px); }
  .path-toggle { margin-top: -8px; }
  .input, .compact-selection .input { min-height: var(--sheet-touch-size, 44px); font-size: var(--sheet-font-input, 16px); }
  .compact-selection .edit-label > .input { width: 4.25rem; }
  .compact-selection .file-editors, .episode-summary { margin-left: 38px; }
  .file-path-details { padding-left: 38px; }
}
@media (max-width: 639px) {
  .compact-selection { margin-inline: calc(-1 * var(--dialog-content-inset, 0px)); border-inline: 0; border-radius: 0; background: transparent; }
  .compact-selection .selection-header { padding-inline: var(--sheet-content-inset, 12px); border-radius: 0; }
  .organize-file-row { padding-left: calc(var(--sheet-content-inset, 12px) + var(--directory-indent, 0px)); padding-right: var(--sheet-content-inset, 12px); }
  .file-selection .organize-file-row { display: grid; grid-template-columns: var(--sheet-touch-size, 44px) minmax(0, 1fr) auto; gap: 4px; }
  .file-main { display: contents; }
  .file-check { grid-column: 1; grid-row: 1; align-self: start; margin: 0; }
  .file-content { grid-column: 2 / -1; grid-row: 1; }
  .file-name, .compact-selection .file-name { min-height: var(--sheet-touch-size, 44px); font-size: var(--sheet-font-body, 14px); line-height: 20px; }
  .file-original, .file-name-label, .path-toggle, .editor-collapse, .episode-summary, .edit-label, .secondary, .compact-selection .secondary { font-size: var(--sheet-font-meta, 12px); line-height: 20px; }
  .path-toggle, .editor-collapse { min-width: var(--sheet-touch-size, 44px); }
  .path-toggle { grid-column: 3; grid-row: 2; justify-self: end; margin: 0; }
  .file-editors, .compact-selection .file-editors { grid-column: 2; grid-row: 2; align-items: center; min-width: 0; margin: 0; gap: 8px; }
  .edit-label { flex-direction: row; align-items: center; white-space: nowrap; }
  .edit-label > .input, .compact-selection .edit-label > .input { width: 4.25rem; min-width: 0; padding-inline: 6px; }
  .compact-selection .episode-summary, .episode-summary { grid-column: 2; grid-row: 2; margin-left: 0; }
  .file-path-details { grid-column: 2 / -1; padding-left: 0; font-size: var(--sheet-font-meta, 12px); line-height: var(--sheet-line-body, 22px); }
}
</style>
