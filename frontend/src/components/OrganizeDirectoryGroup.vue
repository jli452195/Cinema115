<script setup lang="ts">
import { IconChevronDown, IconChevronRight, IconFolder } from '@tabler/icons-vue'
import type { OrganizeDirectoryGroup as DirectoryGroup } from '../utils/organizeSelection'

const props = defineProps<{
  group: DirectoryGroup
  rootPath: string
  visible: Set<string>
  collapsed: string[]
  compact?: boolean
  baseDepth: number
  disabled: boolean
  checked: (directory: string) => boolean
  partial: (directory: string) => boolean
  issues: Record<string, number>
}>()
const emit = defineEmits<{ select: [path: string]; toggle: [path: string]; exclude: [path: string] }>()
defineSlots<{ default(props: { group: DirectoryGroup }): unknown }>()
</script>

<template>
  <section v-if="visible.has(group.path)" class="directory-group" :style="{ '--directory-depth': Math.min(4, Math.max(0, group.depth - baseDepth)) }">
    <div v-if="!compact || group.path !== rootPath || group.rows.length" class="directory-heading">
      <label class="directory-check"><input type="checkbox" :checked="checked(group.path)" :indeterminate="partial(group.path)" :disabled="disabled" :aria-label="`选择目录 ${group.label} 及下级`" @change="emit('select', group.path)" /></label>
      <button type="button" class="directory-toggle" :title="group.path" :aria-expanded="!collapsed.includes(group.path)" :aria-label="`展开或折叠 ${group.label}`" @click="emit('toggle', group.path)">
        <component :is="collapsed.includes(group.path) ? IconChevronRight : IconChevronDown" :size="14" aria-hidden="true" class="shrink-0" />
        <IconFolder :size="17" aria-hidden="true" class="directory-icon shrink-0" />
        <span class="min-w-0 flex-1"><span class="directory-name">{{ group.name }}</span><span class="directory-count">{{ group.descendants.length }} 个文件<template v-if="group.descendants.length !== group.rows.length">（{{ group.rows.length ? `本层 ${group.rows.length} 个` : '含下级目录' }}）</template></span><span v-if="issues[group.path]" class="directory-issues">{{ issues[group.path] }} 项待处理</span></span>
      </button>
      <button v-if="group.path !== rootPath" type="button" class="directory-exclude" :disabled="disabled" :aria-label="`排除目录 ${group.label} 及下级`" @click="emit('exclude', group.path)">本次排除</button>
    </div>
    <p v-if="!compact && group.path === rootPath" class="break-all px-3 py-2 text-xs text-content-secondary">{{ rootPath }}</p>
    <template v-if="!collapsed.includes(group.path)">
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
.directory-count { @apply ml-2 inline-block text-xs font-normal text-content-secondary; }
.directory-issues { @apply ml-2 inline-block rounded bg-status-danger-surface px-1.5 py-0.5 text-xs font-medium text-status-danger; }
.directory-exclude { @apply shrink-0 rounded-lg px-2 py-1.5 text-xs text-content-secondary enabled:hover:bg-surface-hover enabled:hover:text-content-primary disabled:cursor-not-allowed disabled:opacity-40; min-height: var(--ae-control-height, 36px); }
.directory-heading :is(button, input):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
@media (max-width: 639px) {
  .directory-group { --directory-indent: calc(min(var(--directory-depth), 2) * 12px); }
  .directory-count { @apply ml-0 block; }
  .directory-issues { @apply ml-0; }
}
@media (max-width: 639px), (pointer: coarse) {
  .directory-check { width: 44px; min-height: 44px; margin-left: -10px; }
  .directory-toggle, .directory-exclude { min-height: 44px; }
}
</style>
