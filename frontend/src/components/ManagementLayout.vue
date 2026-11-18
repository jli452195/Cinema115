<script setup lang="ts">
import { computed, inject, ref } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'
import { useRoute } from 'vue-router'
import PageBreadcrumb from './PageBreadcrumb.vue'
import WorkbenchHeader from './WorkbenchHeader.vue'
import ManagementSidebar from './ManagementSidebar.vue'
import NavigationIcon from './NavigationIcon.vue'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { useManagementSidebar } from '../composables/useManagementSidebar'
import { mobileToolNavigationKey } from '../composables/useMobileToolNavigation'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'
import { mobileWorkspaceSection, mobileWorkspaceTool } from '../utils/mobileWorkspace'

const props = defineProps<{ title: string; description: string; parent?: { label: string; to: string }; contentClass?: string; compactHeading?: boolean }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), sidebar = ref<InstanceType<typeof ManagementSidebar>>()
const { desktop } = useManagementSidebar()
const showSidebar = computed(() => isPortalAdmin.value && adminAccountGroups.some(group => group.links.some(link => isAccountLinkActive(route, link))))
const hasMobileDock = computed(() => !!mobileWorkspaceSection(route.name))
const desktopSidebar = computed(() => showSidebar.value && desktop.value)
const toolNavigation = inject(mobileToolNavigationKey)
const phoneToolHeader = computed(() => {
  if (!toolNavigation?.enabled.value) return
  const tool = mobileWorkspaceTool(route)
  if (!tool) return
  const back = props.parent || (route.path.toLowerCase().replace(/\/$/, '') === tool.to ? toolNavigation.back.value : tool)
  return { title: props.title, back, controls: toolNavigation.controls, opened: toolNavigation.opened.value }
})
function openTools(event: MouseEvent) { toolNavigation?.openTools(event) }
</script>

<template>
  <div class="management-layout flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950" :class="{ 'has-management-sidebar': desktopSidebar, 'has-compact-heading': compactHeading, 'uses-mobile-dock': hasMobileDock }">
    <ManagementSidebar v-if="showSidebar && !phoneToolHeader && !desktop" ref="sidebar" mobile-only :desktop="desktop" />
    <WorkbenchHeader :sidebar-layout="desktopSidebar" :tonal-surface="showSidebar" :managed-sidebar="showSidebar" :sidebar-open="sidebar?.opened" :sidebar-controls="sidebar?.drawerID" :tool-navigation="phoneToolHeader" @toggle-sidebar="sidebar?.toggleDrawer($event)" @open-tools="openTools" @logout="$emit('logout')" />
    <div class="management-shell">
      <main :class="contentClass || (desktop ? 'max-w-[1600px]' : 'max-w-7xl')" class="workbench-content management-content mx-auto w-full min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
        <div v-if="phoneToolHeader || parent || showSidebar && !desktop" class="management-breadcrumb-row" :class="{ 'navigation-only': !parent && !phoneToolHeader }">
          <button v-if="phoneToolHeader" type="button" class="management-tool-switch" aria-haspopup="dialog" :aria-expanded="phoneToolHeader.opened" :aria-controls="phoneToolHeader.controls" @click="openTools">
            <NavigationIcon name="sidebar" :size="16" /><span>切换工具</span><IconChevronDown :size="14" aria-hidden="true" />
          </button>
          <button v-else-if="showSidebar && !desktop" type="button" class="management-navigation-trigger" aria-label="打开功能导航" aria-haspopup="dialog" :aria-expanded="sidebar?.opened || false" :aria-controls="sidebar?.drawerID" @click="sidebar?.openDrawer($event)"><NavigationIcon name="sidebar" :size="18" /><span>功能导航</span></button>
          <PageBreadcrumb v-if="parent && !phoneToolHeader" :parent="parent" />
        </div>
        <header class="management-heading mb-4 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1">
          <div class="management-title min-w-0" :class="{ 'with-title-tools': $slots.titleTools }">
            <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ title }}</h1>
            <slot name="titleTools" />
          </div>
          <div v-if="$slots.actions" class="management-heading-actions flex items-center gap-3"><slot name="actions" /></div>
          <p class="management-description col-span-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400">{{ description }}</p>
        </header>
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
.management-shell { --management-content-top: calc(var(--workbench-header-height) + 20px); width: 100%; min-width: 0; flex: 1; }
.management-content { padding-top: var(--management-content-top); }
.management-breadcrumb-row { display: flex; min-width: 0; align-items: center; gap: 12px; margin-bottom: 4px; }
.management-navigation-trigger { display: inline-flex; min-height: 44px; flex-shrink: 0; align-items: center; gap: 7px; padding: 0 10px; margin-left: -10px; border-radius: 10px; font-size: 12px; color: rgb(var(--color-slate-600)); cursor: pointer; }.management-navigation-trigger svg { width: 18px; height: 18px; }.management-navigation-trigger:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }.dark .management-navigation-trigger { color: rgb(var(--color-slate-300)); }
.management-tool-switch {
  display: inline-flex; min-height: 44px; align-items: center; gap: 6px; padding: 0 8px; margin-left: -8px;
  border-radius: 12px; color: rgb(var(--selection-content)); font-size: 12px; font-weight: 600; cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
.management-tool-switch:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: -2px; }
.management-tool-switch:active, .management-tool-switch[aria-expanded='true'] { background: rgb(var(--selection-surface)); }
.management-title.with-title-tools { display: flex; align-items: center; gap: 6px; }
@media (max-width: 639px) {
  .has-compact-heading .management-shell { --management-content-top: calc(var(--workbench-header-height) + 8px); }
  .uses-mobile-dock .management-breadcrumb-row.navigation-only,
  .uses-mobile-dock .management-navigation-trigger,
  .has-compact-heading .management-breadcrumb-row.navigation-only,
  .has-compact-heading .management-navigation-trigger,
  .has-compact-heading .management-description { display: none; }
  .has-compact-heading .management-heading { align-items: center; column-gap: 4px; margin-bottom: 8px; }
  .has-compact-heading .management-heading h1 { flex: none; font-size: 17px; white-space: nowrap; }
  .has-compact-heading .management-heading-actions { gap: 0; }
}
@media (hover: hover) {
  .management-navigation-trigger:hover { background: rgb(116 143 180 / 8%); }
  .management-tool-switch:hover { background: rgb(var(--selection-surface)); }
}
@media (max-width: 1023px) { .management-breadcrumb-row { margin-bottom: 8px; } }
</style>

