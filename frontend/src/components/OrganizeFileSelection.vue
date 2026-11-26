<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { IconChevronDown, IconChevronUp, IconEdit, IconInfoCircle } from '@tabler/icons-vue'
import OrganizeCompactName from './OrganizeCompactName.vue'
import OrganizeDirectoryGroup from './OrganizeDirectoryGroup.vue'
import type { OrganizeRow } from '../types/organize'
import { directoryFileRows, directoryGroups, inDirectory, mobileDirectoryNames, mobileFileNames, parentDirectory, rowExcluded, setRangeSelection } from '../utils/organizeSelection'

const props = defineProps<{ rows: OrganizeRow[]; rootPath: string; tv: boolean; episodeEditable: boolean; disabled: boolean; compact?: boolean; flat?: boolean; autoExpandSingleTopDirectory?: boolean; emptyText?: string; targetNames?: Record<string, string>; conflicts?: Record<string, string>; shortConflicts?: Record<string, string> }>()
const excluded = ref<string[]>([])
const hasExcludedDirectories = computed(() => excluded.value.length > 0)
const directoryOverrides = ref(new Map<string, boolean>())
const anchor = ref('')
const expandedPaths = ref<string[]>([])
const expandedEditors = ref(new Set<string>())
const instanceId = useId()
const rootElement = ref<HTMLElement>()
const mobileQuery = window.matchMedia('(max-width: 639px)')
const mobileViewport = ref(mobileQuery.matches)
const isMobileCompact = computed(() => !!props.compact && mobileViewport.value)
const syncMobile = () => { mobileViewport.value = mobileQuery.matches }
onMounted(() => {
  syncMobile()
  mobileQuery.addEventListener('change', syncMobile)
})
onBeforeUnmount(() => { mobileQuery.removeEventListener('change', syncMobile) })
const groups = computed(() => props.flat ? [{ path: props.rootPath, label: '所选文件', name: '所选文件', depth: 0, rows: props.rows, descendants: props.rows, children: [] }] : directoryGroups(props.rows, props.rootPath))
const mobileNames = computed(() => isMobileCompact.value ? mobileFileNames(groups.value, props.flat) : undefined)
const mobileDirectoryNameMap = computed(() => isMobileCompact.value && !props.flat ? mobileDirectoryNames(groups.value) : undefined)
const fileRows = (rows: OrganizeRow[]) => rows.map(row => ({ row, mobileName: mobileNames.value?.get(row.id) }))
const available = computed(() => props.rows.filter(row => !rowExcluded(row, excluded.value)))
// 虚拟根分组直接展示入口，实际子目录初始折叠；按需在首次有数据时仅展开单个真实顶层目录一层。
let autoExpandedSingleTop = false
watch(groups, list => {
  if (!props.autoExpandSingleTopDirectory || props.flat || autoExpandedSingleTop || !props.rows.length) return
  autoExpandedSingleTop = true
  const root = list[0]
  if (!root || root.rows.length > 0 || root.children.length !== 1) return
  const top = root.children[0]!
  if (!excluded.value.some(dir => inDirectory(top.path, dir)) && !directoryOverrides.value.has(top.path)) {
    directoryOverrides.value.set(top.path, true)
  }
}, { immediate: true })
const directoryExpanded = (path: string) => directoryOverrides.value.get(path) ?? (path === props.rootPath)
const visiblePaths = computed(() => {
  const paths = new Set<string>()
  for (const group of groups.value) {
    if (group.path !== props.rootPath) {
      const parent = parentDirectory(group.path)
      if (!paths.has(parent) || !directoryExpanded(parent)) continue
    }
    if (group.descendants.some(row => !rowExcluded(row, excluded.value))) paths.add(group.path)
  }
  return paths
})
const baseDepth = computed(() => props.compact && !groups.value[0]?.rows.length ? 1 : 0)
const visibleRows = computed(() => (props.flat ? props.rows : directoryFileRows(groups.value[0]!)).filter(row => {
  const directory = props.flat ? props.rootPath : parentDirectory(row.sourcePath)
  return visiblePaths.value.has(directory) && directoryExpanded(directory)
}))
const issueCounts = computed(() => Object.fromEntries(groups.value.map(group => [group.path, group.descendants.filter(row => row.selected && props.conflicts?.[row.id]).length])))
const groupRows = (directory: string) => available.value.filter(row => inDirectory(parentDirectory(row.sourcePath), directory))
const groupChecked = (directory: string) => { const rows = groupRows(directory); return rows.length > 0 && rows.every(row => row.selected) }
const groupPartial = (directory: string) => { const rows = groupRows(directory); return rows.some(row => row.selected) && !rows.every(row => row.selected) }
function toggleDirectory(path: string) {
  directoryOverrides.value.set(path, !directoryExpanded(path))
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
const shortConflict = (row: OrganizeRow) => props.shortConflicts?.[row.id]
const detailsAction = (row: OrganizeRow) => expandedPaths.value.includes(row.id) ? '收起' : props.compact ? row.titleOnly ? '原名' : '详情' : '路径'
const showEditors = (row: OrganizeRow) => !props.compact || (!isMobileCompact.value && row.selected) || !!props.conflicts?.[row.id] || expandedEditors.value.has(row.id)
function keepEditorsOpen(row: OrganizeRow) {
  // 冲突修正并通过预览后，已开始编辑的行仍可继续修改。
  if (isMobileCompact.value) expandedEditors.value.add(row.id)
}
const hasSeason = (row: OrganizeRow) => Number.isInteger(row.season) && row.season >= 0
const hasEpisode = (row: OrganizeRow) => Number.isInteger(row.episode) && row.episode > 0
const episodePending = (row: OrganizeRow) => !hasSeason(row) || !hasEpisode(row)
const episodeSummary = (row: OrganizeRow) => {
  const season = hasSeason(row) ? row.season === 0 ? '特别篇' : `第 ${row.season} 季` : '季待定'
  const episode = hasEpisode(row) ? `第 ${row.episode} 集` : '集待定'
  return `${season} · ${episode}`
}
const compactEpisodeSummary = (row: OrganizeRow) => {
  const season = hasSeason(row) ? `S${String(row.season).padStart(2, '0')}` : 'S?'
  const episode = hasEpisode(row) ? `E${String(row.episode).padStart(2, '0')}` : 'E?'
  return `${season}${episode}`
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
    const editors = fileElement(row.id)?.querySelector('.file-editors')
    if (isMobileCompact.value) editors?.scrollIntoView({ block: 'nearest' })
    editors?.querySelector<HTMLInputElement>('input:not(:disabled)')?.focus({ preventScroll: true })
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
  const directories = props.rows.filter(row => ids.includes(row.id)).map(row => parentDirectory(row.sourcePath))
  excluded.value = excluded.value.filter(root => !directories.some(directory => inDirectory(directory, root)))
  for (const group of groups.value) {
    if (directories.some(directory => inDirectory(directory, group.path))) directoryOverrides.value.set(group.path, true)
  }
}
defineExpose({ locateIssue, revealRows, hasExcludedDirectories })
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
      <OrganizeDirectoryGroup v-if="groups[0]" :group="groups[0]" :root-path="rootPath" :visible="visiblePaths" :expanded="directoryExpanded" :compact="compact" :base-depth="baseDepth" :disabled="disabled" :checked="groupChecked" :partial="groupPartial" :issues="issueCounts" :mobile-names="mobileDirectoryNameMap" @select="toggleGroup" @toggle="toggleDirectory" @exclude="exclude">
        <template #default="{ group }">
          <div v-for="{ row, mobileName } in fileRows(group.rows)" :key="row.id" :data-organize-file="row.id" class="file-columns organize-file-row gap-x-3 gap-y-2 px-3 py-2" :class="[tv ? 'tv-columns' : 'movie-columns', { 'lg:grid lg:items-center': !compact, 'file-selected': row.selected, 'file-has-issue': !!conflicts?.[row.id], 'file-has-editors': tv && showEditors(row), 'file-disabled': disabled }]" @click="clickRow(row, $event)" @mousedown="event => { if (event.shiftKey && !(event.target as HTMLElement).closest('input')) event.preventDefault() }">
            <div class="file-main flex min-w-0 items-start gap-2">
              <label class="file-check"><input type="checkbox" :checked="row.selected" :aria-label="`选择 ${row.name}`" :disabled="disabled" @click.stop="toggleRow(row, $event)" /></label>
              <div class="file-content min-w-0 flex-1">
                <div class="file-head">
                  <button type="button" tabindex="-1" class="file-name" :disabled="disabled" :aria-pressed="row.selected" :aria-label="isMobileCompact ? row.name : undefined" :title="isMobileCompact ? row.name : compact && plannedName(row) ? plannedName(row) : row.name" @click.stop="toggleRow(row, $event)">
                    <template v-if="isMobileCompact">
                      <OrganizeCompactName v-if="mobileName" :name="mobileName" />
                      <span v-else>{{ row.name }}</span>
                    </template>
                    <template v-else-if="compact && plannedName(row)"><span v-if="row.titleOnly" class="file-name-label">预计命名：</span><span>{{ plannedName(row) }}</span></template>
                    <span v-else>{{ row.name }}</span>
                  </button>
                  <p v-if="!isMobileCompact && compact && plannedName(row)" class="file-original" :title="row.name"><span>{{ row.titleOnly ? '资源' : '原名' }}：</span><span>{{ row.name }}</span></p>
                  <p v-else-if="!isMobileCompact && targetNames?.[row.id]" class="file-fallback-target mt-1 break-all text-xs leading-5 text-content-secondary">{{ compact ? targetNames[row.id] : `${row.titleOnly ? '预计命名' : '整理后'}：${targetNames[row.id]}` }}</p>
                </div>
                <p v-if="conflicts?.[row.id]" :id="rowId(row, 'error')" role="alert" class="file-error mt-1 break-all text-xs leading-5 text-status-danger" :class="{ 'file-error-compact-hidden': compact && !!shortConflict(row) }">{{ conflicts[row.id] }}</p>
                <slot name="row-details" :row="row" />
              </div>
              <div class="file-tail" @click.stop>
                <span v-if="compact && shortConflict(row)" role="alert" class="file-conflict-badge">{{ shortConflict(row) }}</span>
                <button v-if="compact || !row.titleOnly" type="button" class="path-toggle" :class="{ 'is-icon-only': isMobileCompact }" :aria-expanded="expandedPaths.includes(row.id)" :aria-controls="rowId(row, 'details')" :aria-label="`${detailsAction(row)}：${row.titleOnly ? '完整资源名' : compact ? '完整原名' : '完整路径'} ${row.name}`" @click.stop="expandedPaths = toggleList(expandedPaths, row.id)">
                  <IconInfoCircle v-if="isMobileCompact" :size="18" aria-hidden="true" />
                  <template v-else><span>{{ detailsAction(row) }}</span><component :is="expandedPaths.includes(row.id) ? IconChevronUp : IconChevronDown" :size="13" aria-hidden="true" /></template>
                </button>
              </div>
            </div>
            <div v-if="tv && showEditors(row)" :id="rowId(row, 'editors')" class="file-editors ml-7 mt-2 flex flex-wrap gap-2" :class="{ 'lg:contents': !compact }" @focusin="keepEditorsOpen(row)" @click.stop>
              <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">季</span><input v-model.number="row.season" :aria-label="`${row.name} 季`" :aria-describedby="conflicts?.[row.id] ? rowId(row, 'error') : undefined" :disabled="disabled || !episodeEditable" type="number" min="0" max="1000" class="input w-16 sm:w-20 lg:w-full" /></label>
              <label class="edit-label"><span :class="{ 'lg:hidden': !compact }">集</span><input v-model.number="row.episode" :aria-label="`${row.name} 集`" :aria-invalid="!!conflicts?.[row.id]" :aria-describedby="conflicts?.[row.id] ? rowId(row, 'error') : undefined" :disabled="disabled || !episodeEditable" type="number" min="1" max="10000" class="input w-16 sm:w-20 lg:w-full" /></label>
              <button v-if="compact && expandedEditors.has(row.id) && (!row.selected || isMobileCompact) && !conflicts?.[row.id]" type="button" class="editor-collapse" :disabled="disabled" :aria-label="`收起季集编辑 ${row.name}`" :aria-controls="rowId(row, 'editors')" aria-expanded="true" @click="toggleEditors(row)">收起</button>
            </div>
            <button v-else-if="tv && compact" type="button" class="episode-summary" :disabled="disabled || !episodeEditable" :aria-label="`编辑季集 ${row.name}`" :aria-describedby="rowId(row, 'episode-summary')" :aria-controls="rowId(row, 'editors')" aria-expanded="false" @click.stop="toggleEditors(row)">
              <span :id="rowId(row, 'episode-summary')" class="sr-only">{{ episodeSummary(row) }}</span>
              <span class="episode-summary-inner" :class="{ 'is-pending': isMobileCompact && episodePending(row) }"><span class="episode-summary-text">{{ isMobileCompact ? compactEpisodeSummary(row) : episodeSummary(row) }}</span><IconEdit :size="13" class="episode-summary-icon shrink-0" aria-hidden="true" /></span>
            </button>
            <div v-if="expandedPaths.includes(row.id)" :id="rowId(row, 'details')" class="file-path-details col-span-full mt-2 break-all text-xs leading-5 text-content-secondary" @click.stop>
              <p v-if="compact && shortConflict(row)" class="text-status-danger sm:hidden">{{ conflicts?.[row.id] }}</p>
              <p v-if="compact && plannedName(row)">{{ row.titleOnly ? '预计命名' : '整理后' }}：{{ plannedName(row) }}</p>
              <p v-if="compact">{{ row.titleOnly ? '完整资源名' : '完整原名' }}：{{ row.name }}</p>
              <p v-else-if="!row.titleOnly">{{ row.sourcePath }}</p>
            </div>
          </div>
        </template>
      </OrganizeDirectoryGroup>
      <p v-if="!available.length" class="p-8 text-center text-sm text-content-secondary">{{ !rows.length && emptyText ? emptyText : excluded.length ? '所有目录均已排除，可在上方恢复。' : emptyText || '没有可整理的文件。' }}</p>
    </div>
  </div>
</template>

<style scoped>
.file-selection { color: rgb(var(--content-primary)); border-radius: var(--organize-card-radius, 12px); background: var(--organize-card-bg, rgb(var(--surface-card))); border-color: var(--organize-border, rgb(var(--line-default))); }
.selection-header { padding: 6px 12px; border-bottom: 1px solid var(--organize-border, rgb(var(--line-default))); border-radius: var(--organize-card-radius, 12px) var(--organize-card-radius, 12px) 0 0; background: var(--organize-sub-bg, rgb(var(--surface-page))); }
.input { @apply rounded-lg border px-2 py-2 text-sm text-content-primary outline-none focus:border-focus; background: var(--organize-input-bg, rgb(var(--surface-card))); border-color: var(--organize-control-border, rgb(var(--line-control))); }
.input:disabled { @apply cursor-not-allowed border-line-default bg-surface-page text-content-secondary; opacity: .6; }
.input[aria-invalid="true"] { border-color: rgb(var(--status-danger)); }
.secondary { @apply rounded-lg border border-line-default bg-surface-card px-3 py-2 text-xs text-content-secondary enabled:hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-40; }
.edit-label { @apply flex flex-col gap-1 text-xs text-content-secondary; }
.file-selection :is(button, input, summary):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.file-check { display: grid; flex-shrink: 0; place-items: center; width: 32px; min-height: 32px; margin: -4px 0 -4px -8px; cursor: pointer; }
.file-check input { width: 16px; height: 16px; accent-color: var(--organize-accent, rgb(var(--action-primary))); cursor: inherit; }
.file-check:has(input:disabled) { cursor: not-allowed; opacity: .5; }
.file-name { width: 100%; border-radius: 4px; text-align: left; overflow-wrap: anywhere; font-size: 14px; line-height: 20px; color: rgb(var(--content-primary)); }
.file-name:disabled { cursor: default; }
.file-original { margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; line-height: 20px; color: rgb(var(--content-secondary)); }
.file-tail { display: flex; flex-shrink: 0; align-items: center; }
.file-conflict-badge { display: none; }
.path-toggle, .editor-collapse { @apply inline-flex shrink-0 items-center justify-center gap-1 rounded-md px-1.5 text-xs text-action-link enabled:hover:bg-selection-surface disabled:cursor-not-allowed disabled:opacity-40; min-height: 32px; }
.path-toggle { margin: -6px -4px 0 0; color: rgb(var(--content-secondary)); }
.path-toggle:hover, .path-toggle[aria-expanded='true'] { color: rgb(var(--action-link)); }
.file-head { min-width: 0; }
.episode-summary { @apply inline-flex items-center justify-start rounded-lg text-xs text-content-secondary disabled:cursor-not-allowed disabled:opacity-50; justify-self: start; min-height: var(--dialog-control-height, 36px); margin-left: 32px; }
.episode-summary-inner { @apply inline-flex items-center gap-1.5 rounded-lg border border-transparent px-2 transition-colors; background: var(--organize-sub-bg, rgb(var(--surface-hover) / .5)); min-height: var(--dialog-control-height, 36px); }
.episode-summary:enabled:hover .episode-summary-inner { border-color: rgb(var(--focus)); background: rgb(var(--selection-surface)); color: rgb(var(--action-link)); }
.compact-selection { container-type: inline-size; overflow: clip; }
.organize-file-row { background: var(--organize-row-bg, rgb(var(--surface-card))); padding-left: calc(28px + var(--directory-indent, 0px)); scroll-margin-top: calc(var(--organize-sticky-offset, 0px) + 12px); cursor: pointer; }
.organize-file-row:not(.file-disabled):hover { background: var(--organize-row-hover, rgb(var(--surface-hover) / .6)); }
.file-selected { background: var(--organize-row-selected, rgb(var(--selection-surface))); box-shadow: inset 3px 0 color-mix(in srgb, var(--organize-accent, rgb(var(--action-primary))) 65%, transparent); }
.file-selected:not(.file-disabled):hover { background: var(--organize-row-selected-hover, rgb(var(--selection-surface))); }
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
  .compact-selection .selection-header { padding: 4px var(--sheet-content-inset, 12px); border-radius: 0; }
  .organize-file-row { padding-left: calc(var(--sheet-content-inset, 12px) + var(--directory-indent, 0px)); padding-right: var(--sheet-content-inset, 12px); }
  .file-selection:not(.compact-selection) .organize-file-row { display: grid; grid-template-columns: var(--sheet-touch-size, 44px) minmax(0, 1fr) auto; gap: 4px; }
  .file-selection:not(.compact-selection) .file-main { display: contents; }
  .file-selection:not(.compact-selection) .file-check { grid-column: 1; grid-row: 1; align-self: start; margin: 0; }
  .file-selection:not(.compact-selection) .file-content { grid-column: 2 / -1; grid-row: 1; }
  .file-selection:not(.compact-selection) .file-tail { grid-column: 3; grid-row: 2; justify-self: end; }
  .file-selection:not(.compact-selection) .file-editors { grid-column: 2; grid-row: 2; align-items: center; min-width: 0; margin: 0; gap: 8px; }
  .file-selection:not(.compact-selection) .episode-summary { grid-column: 2; grid-row: 2; margin-left: 0; }
  .file-selection:not(.compact-selection) .file-path-details { grid-column: 2 / -1; padding-left: 0; }
  /* 手机端单轨紧凑布局：主行展示原名差异段或中间截断，右侧放季集摘要与固定 44px 详情图标。 */
  .compact-selection .organize-file-row { grid-template-columns: var(--sheet-touch-size, 44px) minmax(0, 1fr) auto auto; grid-auto-flow: row dense; gap: 4px; padding-block: 4px; padding-left: calc(4px + var(--directory-indent, 0px)); }
  .compact-selection .file-main, .compact-selection .file-content { display: contents; }
  .compact-selection .file-check { grid-column: 1; grid-row: 1; align-self: center; margin: 0; }
  .compact-selection .file-head { grid-column: 2; grid-row: 1; min-width: 0; align-self: center; }
  .compact-selection .movie-columns .file-head { grid-column-end: 4; }
  .compact-selection .file-has-editors .file-head { grid-column: 2 / -1; }
  .compact-selection .file-name { display: block; overflow: hidden; min-height: 0; padding-block: 2px; font-size: 13px; line-height: 18px; font-weight: 500; }
  .compact-selection .file-content > :deep(:not(.file-head)) { grid-column: 1 / -1; padding-left: 8px; }
  .compact-selection .file-error-compact-hidden { display: none; }
  .compact-selection .file-editors { grid-column: 1 / 4; justify-self: start; align-self: center; margin: 0; padding-left: 8px; gap: 6px; scroll-margin-top: var(--organize-sticky-offset, 0px); }
  .compact-selection .edit-label { flex-direction: row; align-items: center; gap: 4px; white-space: nowrap; }
  .compact-selection .edit-label > .input { width: 3.5rem; min-width: 0; padding-inline: 4px; text-align: center; }
  .compact-selection .episode-summary { grid-column: 3; grid-row: 1; justify-self: end; align-self: center; margin: 0; padding: 8px 0; }
  .compact-selection .episode-summary-inner { max-width: 84px; min-height: 26px; padding-inline: 7px; background: var(--organize-sub-bg, rgb(var(--surface-hover) / .75)); font-variant-numeric: tabular-nums; }
  .compact-selection .episode-summary-inner.is-pending { border-color: rgb(var(--status-warning) / .35); color: rgb(var(--status-warning)); }
  .compact-selection .episode-summary-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .compact-selection .file-tail { grid-column: 4; grid-row: 1; justify-self: end; align-self: center; gap: 4px; }
  .compact-selection .file-has-editors .file-tail { grid-row: auto; }
  .compact-selection .file-conflict-badge { display: inline-flex; align-items: center; padding: 2px 6px; border-radius: 6px; background: rgb(var(--status-danger-surface)); color: rgb(var(--status-danger)); font-size: 11px; line-height: 16px; font-weight: 500; white-space: nowrap; }
  .compact-selection .path-toggle.is-icon-only { width: var(--sheet-touch-size, 44px); padding: 0; border-radius: 8px; }
  .compact-selection .path-toggle.is-icon-only[aria-expanded='true'] { background: rgb(var(--selection-surface)); color: rgb(var(--action-link)); }
  .compact-selection .file-path-details { grid-column: 1 / -1; padding-left: 8px; margin-top: 2px; font-size: var(--sheet-font-meta, 12px); line-height: var(--sheet-line-body, 22px); }
  .file-selection:not(.compact-selection) .file-name { min-height: var(--sheet-touch-size, 44px); font-size: var(--sheet-font-body, 14px); line-height: 20px; }
  .file-original, .file-name-label, .path-toggle, .editor-collapse, .episode-summary, .edit-label, .secondary, .compact-selection .secondary { font-size: var(--sheet-font-meta, 12px); line-height: 20px; }
  .path-toggle, .editor-collapse, .episode-summary { min-width: var(--sheet-touch-size, 44px); }
  .path-toggle { margin: 0; }
}
@media (max-width: 359px) {
  /* 极窄屏隐藏季集编辑图标以腾出文件名空间，外层按钮仍保持 44px 触控区。 */
  .compact-selection .episode-summary-icon { display: none; }
}
</style>
