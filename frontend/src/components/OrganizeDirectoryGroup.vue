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
    <div v-if="!compact || group.path !== rootPath || group.rows.length" class="directory-heading" :class="{ 'directory-heading-sticky': compact && !collapsed.includes(group.path) }">
      <input type="checkbox" :checked="checked(group.path)" :indeterminate="partial(group.path)" :disabled="disabled" :aria-label="`选择目录 ${group.label} 及下级`" @change="emit('select', group.path)" />
      <button type="button" class="directory-toggle" :title="group.path" :aria-expanded="!collapsed.includes(group.path)" :aria-label="`展开或折叠 ${group.label}`" @click="emit('toggle', group.path)">
        <component :is="collapsed.includes(group.path) ? IconChevronRight : IconChevronDown" :size="14" aria-hidden="true" class="shrink-0" />
        <IconFolder :size="17" aria-hidden="true" class="shrink-0 text-slate-500 dark:text-slate-400" />
        <span class="min-w-0 flex-1"><span class="directory-name">{{ group.name }}</span><span class="directory-count">{{ group.rows.length }} 个文件<template v-if="group.descendants.length !== group.rows.length"> · 含下级 {{ group.descendants.length }} 个</template></span><span v-if="issues[group.path]" class="directory-issues">{{ issues[group.path] }} 项待处理</span></span>
      </button>
      <button v-if="group.path !== rootPath" type="button" class="directory-exclude" :disabled="disabled" :aria-label="`排除目录 ${group.label} 及下级`" @click="emit('exclude', group.path)">本次排除</button>
    </div>
    <p v-if="!compact && group.path === rootPath" class="break-all px-3 py-2 text-xs text-slate-400 dark:text-slate-500">{{ rootPath }}</p>
    <template v-if="!collapsed.includes(group.path)">
      <OrganizeDirectoryGroup v-for="child in group.children" :key="child.path" v-bind="props" :group="child" @select="emit('select', $event)" @toggle="emit('toggle', $event)" @exclude="emit('exclude', $event)">
        <template #default="scope"><slot v-bind="scope" /></template>
      </OrganizeDirectoryGroup>
      <div v-if="group.rows.length" class="directory-files divide-y divide-slate-100 dark:divide-slate-800"><slot :group="group" /></div>
    </template>
  </section>
</template>

<style scoped>
.directory-group { --directory-indent: calc(var(--directory-depth) * 16px); }
.directory-heading { @apply flex items-center gap-2 border-y border-slate-200/80 bg-slate-100 py-2 pr-3 dark:border-slate-700/80 dark:bg-slate-800; padding-left: calc(12px + var(--directory-indent)); }
.directory-heading-sticky { position: sticky; top: var(--organize-toolbar-height); z-index: 5; box-shadow: 0 1px 2px rgb(15 23 42 / .05); }
.directory-toggle { @apply flex min-w-0 flex-1 items-center gap-1.5 text-left text-sm font-medium text-slate-700 dark:text-slate-200; }
.directory-name { overflow-wrap: anywhere; }
.directory-count { @apply ml-2 inline-block text-xs font-normal text-slate-500 dark:text-slate-400; }
.directory-issues { @apply ml-2 inline-block rounded bg-red-50 px-1.5 py-0.5 text-xs font-medium text-red-600 dark:bg-red-950/50 dark:text-red-400; }
.directory-exclude { @apply shrink-0 rounded-lg border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-600 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400 dark:hover:bg-slate-800; min-height: var(--ae-control-height, 36px); }
@media (max-width: 639px) {
  .directory-group { --directory-indent: calc(min(var(--directory-depth), 2) * 12px); }
  .directory-count { @apply ml-0 block; }
  .directory-issues { @apply ml-0; }
}
</style>
