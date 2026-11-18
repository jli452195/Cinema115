<script setup lang="ts">
import { computed, inject, ref } from 'vue'
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

const props = defineProps<{ title: string; description: string; parent?: { label: string; to: string }; contentClass?: string; compactHeading?: boolean; mobileSectionHeading?: boolean; mobileBackFallback?: string; mobileBackTarget?: string; mobileTitle?: string }>()
defineEmits<{ logout: [] }>()
const mobilePage = computed(() => {
  const fallback = props.mobileBackTarget || props.mobileBackFallback
  return fallback ? { title: props.mobileTitle || props.title, fallback, target: props.mobileBackTarget } : undefined
})
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
  <div class="management-layout flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950" :class="{ 'has-management-sidebar': desktopSidebar, 'has-compact-heading': compactHeading, 'uses-mobile-dock': hasMobileDock, 'has-section-heading': mobileSectionHeading }">
    <ManagementSidebar v-if="showSidebar && !phoneToolHeader && !desktop" ref="sidebar" mobile-only :desktop="desktop" />
    <WorkbenchHeader :sidebar-layout="desktopSidebar" :tonal-surface="showSidebar" :managed-sidebar="showSidebar" :sidebar-open="sidebar?.opened" :sidebar-controls="sidebar?.drawerID" :tool-navigation="phoneToolHeader" :mobile-page="mobilePage" :mobile-section-heading="mobileSectionHeading" @toggle-sidebar="sidebar?.toggleDrawer($event)" @open-tools="openTools" @logout="$emit('logout')">
      <template v-if="$slots['mobile-title-tools']" #mobile-title-tools><slot name="mobile-title-tools" /></template>
      <template v-if="$slots['mobile-actions']" #mobile-actions><slot name="mobile-actions" /></template>
    </WorkbenchHeader>
    <div class="management-shell">
      <main :class="contentClass || (desktop ? 'max-w-[1600px]' : 'max-w-7xl')" class="workbench-content management-content mx-auto w-full min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
        <div v-if="!phoneToolHeader && (parent || showSidebar && !desktop)" class="management-breadcrumb-row" :class="{ 'navigation-only': !parent }">
          <button v-if="showSidebar && !desktop" type="button" class="management-navigation-trigger" aria-label="打开功能导航" aria-haspopup="dialog" :aria-expanded="sidebar?.opened || false" :aria-controls="sidebar?.drawerID" @click="sidebar?.openDrawer($event)"><NavigationIcon name="sidebar" :size="18" /><span>功能导航</span></button>
          <PageBreadcrumb v-if="parent" :parent="parent" />
        </div>
        <header class="management-heading mb-4 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1">
          <div class="management-title min-w-0" :class="{ 'with-title-tools': $slots.titleTools }">
            <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100" :class="{ 'sr-only sm:not-sr-only': mobilePage }">{{ title }}</h1>
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
.management-title.with-title-tools { display: flex; align-items: center; gap: 6px; }
@media (max-width: 639px) {
  .has-section-heading {
    --workbench-toolbar-height: 52px;
    --workbench-section-tabs-height: 44px;
    --workbench-header-height: calc(var(--workbench-toolbar-height) + var(--workbench-section-tabs-height) + var(--app-safe-top));
  }
  .has-section-heading .management-shell { --management-content-top: calc(var(--workbench-header-height) + 1px); }
  .has-section-heading .management-content { padding-inline: max(16px, var(--app-safe-left)) max(16px, var(--app-safe-right)); }
  .has-section-heading .management-heading { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; clip-path: inset(50%); overflow: hidden; white-space: nowrap; }
  .has-section-heading .management-heading :is(.management-heading-actions, .management-description) { display: none; }
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
}
@media (max-width: 1023px) { .management-breadcrumb-row { margin-bottom: 8px; } }
</style>

