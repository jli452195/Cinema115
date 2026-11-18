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
import { mobileWorkspaceSection } from '../utils/mobileWorkspace'

const props = defineProps<{ title: string; description: string; parent?: { label: string; to: string }; contentClass?: string; mobileSectionHeading?: boolean; mobileBackFallback?: string; mobileBackTarget?: string; mobileTitle?: string }>()
defineEmits<{ logout: [] }>()
const mobilePage = computed(() => {
  const fallback = props.mobileBackTarget || props.mobileBackFallback
  return fallback ? { title: props.mobileTitle || props.title, fallback, target: props.mobileBackTarget } : undefined
})
const route = useRoute(), sidebar = ref<InstanceType<typeof ManagementSidebar>>()
const { desktop } = useManagementSidebar()
const showSidebar = computed(() => isPortalAdmin.value && adminAccountGroups.some(group => group.links.some(link => isAccountLinkActive(route, link))))
const isFollowSection = computed(() => mobileWorkspaceSection(route.name) === 'follows')
const desktopSidebar = computed(() => showSidebar.value && desktop.value)
const toolNavigation = inject(mobileToolNavigationKey)
const mobileTool = computed(() => toolNavigation?.tool.value)
const mobileBack = computed(() => {
  const tool = mobileTool.value
  if (!tool || !toolNavigation) return
  return props.parent || (route.path.toLowerCase().replace(/\/$/, '') === tool.to ? toolNavigation.toolBack.value : tool)
})
</script>

<template>
  <div class="management-layout flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950" :class="{ 'has-section-heading': mobileSectionHeading, 'has-follow-heading': isFollowSection, 'without-mobile-header': isFollowSection && !isPortalAdmin }">
    <ManagementSidebar v-if="showSidebar && !mobileTool && !desktop" ref="sidebar" mobile-only :desktop="desktop" />
    <WorkbenchHeader :sidebar-layout="desktopSidebar" :tonal-surface="showSidebar" :managed-sidebar="showSidebar" :sidebar-open="sidebar?.opened" :sidebar-controls="sidebar?.drawerID" :mobile-back="mobileBack" :mobile-page="mobilePage" :mobile-section-heading="mobileSectionHeading" :mobile-section-title="mobileTitle || title" @toggle-sidebar="sidebar?.toggleDrawer($event)" @logout="$emit('logout')">
      <template v-if="$slots['mobile-actions']" #mobile-actions><slot name="mobile-actions" /></template>
    </WorkbenchHeader>
    <div class="management-shell">
      <main :class="contentClass || (desktop ? 'max-w-[1600px]' : 'max-w-7xl')" class="workbench-content management-content mx-auto w-full min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
        <div v-if="!mobileTool && (parent || showSidebar && !desktop)" class="management-breadcrumb-row" :class="{ 'navigation-only': !parent }">
          <button v-if="showSidebar && !desktop" type="button" class="management-navigation-trigger" aria-label="打开功能导航" aria-haspopup="dialog" :aria-expanded="sidebar?.opened || false" :aria-controls="sidebar?.drawerID" @click="sidebar?.openDrawer($event)"><NavigationIcon name="sidebar" :size="18" /><span>功能导航</span></button>
          <PageBreadcrumb v-if="parent" :parent="parent" />
        </div>
        <header class="management-heading mb-4 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1">
          <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100" :class="{ 'sr-only sm:not-sr-only': mobilePage }">
            <template v-if="mobileTitle">
              <span class="sm:hidden">{{ mobileTitle }}</span><span class="hidden sm:inline">{{ title }}</span>
            </template>
            <template v-else>{{ title }}</template>
          </h1>
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
@media (max-width: 639px) {
  .has-section-heading { --workbench-toolbar-height: 52px; }
  .has-follow-heading .management-shell { --management-content-top: calc(var(--workbench-header-height) + 4px); }
  :is(.has-follow-heading, .has-section-heading) .management-content { padding-inline: max(16px, var(--app-safe-left)) max(16px, var(--app-safe-right)); }
  .has-follow-heading .management-heading { align-items: center; column-gap: 4px; margin-bottom: 12px; }
  .has-follow-heading .management-heading h1 { font-size: 21px; }
  .has-follow-heading .management-heading-actions { gap: 0; }
  .has-follow-heading .management-description { display: none; }
  .has-section-heading .management-shell { --management-content-top: calc(var(--workbench-header-height) + 1px); }
  .has-section-heading .management-heading { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; clip-path: inset(50%); overflow: hidden; white-space: nowrap; }
  .has-section-heading .management-heading :is(.management-heading-actions, .management-description) { display: none; }
  .without-mobile-header .management-shell { --management-content-top: calc(var(--app-safe-top) + 12px); }
  .has-follow-heading .management-breadcrumb-row.navigation-only,
  .has-follow-heading .management-navigation-trigger { display: none; }
}
@media (hover: hover) {
  .management-navigation-trigger:hover { background: rgb(116 143 180 / 8%); }
}
@media (max-width: 1023px) { .management-breadcrumb-row { margin-bottom: 8px; } }
</style>
