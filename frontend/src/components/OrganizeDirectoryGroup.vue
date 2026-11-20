<script setup lang="ts">
import { computed, useId } from 'vue'
import { IconChevronDown, IconChevronRight, IconFolder } from '@tabler/icons-vue'
import type { MobileDirectoryName, OrganizeDirectoryGroup as DirectoryGroup } from '../utils/organizeSelection'

const props = defineProps<{
  group: DirectoryGroup
  rootPath: string
  visible: Set<string>
  expanded: (directory: string) => boolean
  compact?: boolean
  baseDepth: number
  disabled: boolean
  checked: (directory: string) => boolean
  partial: (directory: string) => boolean
  issues: Record<string, number>
  mobileNames?: ReadonlyMap<string, MobileDirectoryName>
}>()
const emit = defineEmits<{ select: [path: string]; toggle: [path: string]; exclude: [path: string] }>()
defineSlots<{ default(props: { group: DirectoryGroup }): unknown }>()

const summaryId = useId()
const mobileName = computed(() => props.mobileNames?.get(props.group.path))
const isExpanded = computed(() => props.expanded(props.group.path))
const showHeading = computed(() => !props.compact || props.group.path !== props.rootPath || props.group.rows.length > 0)
</script>

<template>
  <section v-if="visible.has(group.path)" class="directory-group" :class="{ 'is-compact': compact, 'has-issues': !!issues[group.path] }" :style="{ '--directory-depth': Math.min(4, Math.max(0, group.depth - baseDepth)) }">
    <div v-if="showHeading" class="directory-heading">
      <label class="directory-check"><input type="checkbox" :checked="checked(group.path)" :indeterminate="partial(group.path)" :disabled="disabled" :aria-label="`选择目录 ${group.label} 及下级`" @change="emit('select', group.path)" /></label>
      <button type="button" class="directory-toggle" :title="group.path" :aria-expanded="isExpanded" :aria-label="`展开或折叠 ${group.label}`" :aria-describedby="summaryId" @click="emit('toggle', group.path)">
        <component :is="isExpanded ? IconChevronDown : IconChevronRight" :size="14" aria-hidden="true" class="directory-chevron shrink-0" />
        <IconFolder :size="17" aria-hidden="true" class="directory-icon shrink-0" />
        <span class="min-w-0 flex-1">
          <span class="directory-name">
            <span v-if="!isExpanded && mobileName?.kind === 'same-as-parent'" class="directory-same-parent">与上级同名</span>
            <template v-else-if="!isExpanded && mobileName?.kind === 'suffix'"><span class="directory-omitted" aria-hidden="true">…</span>{{ mobileName.text }}</template>
            <template v-else>{{ group.name }}</template>
          </span>
          <span :id="summaryId">
            <span class="directory-count">{{ group.descendants.length }} 个文件<span v-if="group.descendants.length !== group.rows.length" class="directory-count-detail">（{{ group.rows.length ? `本层 ${group.rows.length} 个` : '含下级目录' }}）</span></span>
            <span v-if="issues[group.path]" class="directory-issues">{{ issues[group.path] }} 项待处理</span>
          </span>
        </span>
      </button>
      <button v-if="group.path !== rootPath" type="button" class="directory-exclude" :disabled="disabled" :aria-label="`排除目录 ${group.label} 及下级`" @click="emit('exclude', group.path)">本次排除</button>
    </div>
    <p v-if="!compact && group.path === rootPath" class="break-all px-3 py-2 text-xs text-content-secondary">{{ rootPath }}</p>
    <template v-if="isExpanded">
      <OrganizeDirectoryGroup v-for="child in group.children" :key="child.path" v-bind="props" :group="child" @select="emit('select', $event)" @toggle="emit('toggle', $event)" @exclude="emit('exclude', $event)">
        <template #default="scope"><slot v-bind="scope" /></template>
      </OrganizeDirectoryGroup>
      <div v-if="group.rows.length" class="directory-files divide-y divide-line-default"><slot :group="group" /></div>
    </template>
  </section>
</template>

<style scoped>
.directory-group { --directory-indent: calc(var(--directory-depth) * 16px); }
.directory-heading { @apply flex items-center gap-2 border-y py-1.5 pr-3; padding-left: calc(12px + var(--directory-indent)); background: var(--organize-sub-bg, rgb(var(--surface-page))); border-color: var(--organize-border, rgb(var(--line-default))); }
.directory-files > :not([hidden]) ~ :not([hidden]) { border-color: var(--organize-border, rgb(var(--line-default))); }
.directory-check { display: grid; flex-shrink: 0; place-items: center; width: 32px; min-height: 32px; margin-left: -8px; cursor: pointer; }
.directory-check input { width: 16px; height: 16px; accent-color: rgb(var(--action-primary)); cursor: inherit; }
.directory-check:has(input:disabled) { cursor: not-allowed; opacity: .5; }
.directory-toggle { @apply flex min-w-0 flex-1 items-center gap-1.5 rounded-md text-left text-sm font-medium text-content-primary; min-height: 32px; }
.directory-icon { color: rgb(var(--content-secondary)); }
.directory-name { overflow-wrap: anywhere; }
.directory-omitted { margin-right: 1px; color: rgb(var(--content-secondary)); }
.directory-same-parent { color: rgb(var(--content-secondary)); }
.directory-count { @apply ml-2 inline-block text-xs font-normal text-content-secondary; }
.directory-issues { @apply ml-2 inline-block rounded bg-status-danger-surface px-1.5 py-0.5 text-xs font-medium text-status-danger; }
.directory-exclude { @apply shrink-0 rounded-lg px-2 py-1.5 text-xs text-content-secondary enabled:hover:bg-surface-hover enabled:hover:text-content-primary disabled:cursor-not-allowed disabled:opacity-40; min-height: var(--dialog-control-height, 36px); }
.directory-heading :is(button, input):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
@media (max-width: 639px), (pointer: coarse) {
  .directory-check { width: var(--sheet-touch-size, 44px); min-height: var(--sheet-touch-size, 44px); margin-left: -10px; }
  .directory-toggle, .directory-exclude { min-height: var(--sheet-touch-size, 44px); }
}
@media (max-width: 639px) {
  .directory-group { --directory-indent: calc(min(var(--directory-depth), 2) * 8px); }
  .directory-heading { display: grid; grid-template-columns: var(--sheet-touch-size, 44px) minmax(0, 1fr); align-items: start; column-gap: 4px; row-gap: 0; padding-left: calc(var(--sheet-content-inset, 12px) + var(--directory-indent)); padding-right: var(--sheet-content-inset, 12px); }
  .directory-check { grid-column: 1; grid-row: 1; margin-left: 0; }
  .directory-toggle { grid-column: 2; grid-row: 1; align-items: start; gap: 4px; }
  .directory-chevron { margin-top: 3px; }
  .directory-icon { display: none; }
  .directory-name { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; font-size: var(--sheet-font-body, 14px); line-height: 20px; }
  .directory-count { @apply ml-0 block; font-size: var(--sheet-font-meta, 12px); line-height: 20px; }
  .directory-issues { @apply ml-0; font-size: var(--sheet-font-meta, 12px); line-height: 20px; }
  .directory-exclude { grid-column: 2; justify-self: end; font-size: var(--sheet-font-meta, 12px); line-height: 20px; }
  /* 手机端紧凑目录行：折叠单行省略，展开原地换行显示完整目录名；收紧复选框至箭头间距且保持 44px 触控区不重叠。 */
  .directory-group.is-compact .directory-heading { grid-template-columns: var(--sheet-touch-size, 44px) minmax(0, 1fr) auto; align-items: center; column-gap: 0; padding-block: 4px; padding-left: var(--directory-indent); padding-right: var(--sheet-content-inset, 12px); background: rgb(var(--line-default) / .48); border-color: rgb(var(--line-control) / .24); box-shadow: inset 2px 0 rgb(var(--line-control) / .55); }
  .directory-group.is-compact .directory-check { margin-left: 0; }
  .directory-group.is-compact .directory-toggle { align-items: flex-start; gap: 5px; padding: 3px 4px 3px 0; }
  .directory-group.is-compact .directory-chevron { margin-top: 2px; }
  .directory-group.is-compact .directory-icon { display: block; width: 15px; height: 15px; margin-top: 2px; color: rgb(var(--action-link) / .85); }
  .directory-group.is-compact .directory-name { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; line-height: 18px; font-weight: 500; }
  .directory-group.is-compact .directory-toggle[aria-expanded='true'] .directory-name { overflow: visible; text-overflow: clip; white-space: normal; overflow-wrap: anywhere; }
  .directory-group.is-compact .directory-count { display: inline; margin-inline-end: 5px; font-size: 11px; line-height: 16px; }
  .directory-group.is-compact .directory-issues { font-size: 11px; line-height: 16px; }
  .directory-group.is-compact .directory-exclude { grid-column: 3; grid-row: 1; align-self: center; padding-inline: 8px; }
}
@media (max-width: 359px) {
  .directory-group.is-compact.has-issues > .directory-heading .directory-count-detail { @apply sr-only; }
}
</style>
