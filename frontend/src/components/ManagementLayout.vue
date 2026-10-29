<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import PageBreadcrumb from './PageBreadcrumb.vue'
import WorkbenchHeader from './WorkbenchHeader.vue'
import ManagementSidebar from './ManagementSidebar.vue'
import NavigationIcon from './NavigationIcon.vue'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { useManagementSidebar } from '../composables/useManagementSidebar'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'

defineProps<{ title: string; description: string; parent?: { label: string; to: string }; contentClass?: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), sidebar = ref<InstanceType<typeof ManagementSidebar>>()
const { desktop } = useManagementSidebar()
const showSidebar = computed(() => isPortalAdmin.value && adminAccountGroups.some(group => group.links.some(link => isAccountLinkActive(route, link))))
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <div class="management-shell" :class="{ 'has-management-sidebar': showSidebar && desktop }">
      <ManagementSidebar v-if="showSidebar" ref="sidebar" :desktop="desktop" />
      <main :class="contentClass || 'max-w-7xl'" class="workbench-content management-content mx-auto w-full min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
        <div v-if="parent || showSidebar && !desktop" class="management-breadcrumb-row">
          <button v-if="showSidebar && !desktop" type="button" class="management-navigation-trigger" aria-label="打开功能导航" aria-haspopup="dialog" :aria-expanded="sidebar?.opened || false" @click="sidebar?.openDrawer($event)"><NavigationIcon name="sidebar" :size="18" /><span>功能导航</span></button>
          <PageBreadcrumb v-if="parent" :parent="parent" />
        </div>
        <header class="management-heading mb-4 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1">
          <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ title }}</h1>
          <div v-if="$slots.actions" class="flex items-center gap-3"><slot name="actions" /></div>
          <p class="col-span-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ description }}</p>
        </header>
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
.management-shell { --management-content-top: calc(var(--workbench-header-height) + 9px); width: 100%; min-width: 0; flex: 1; }
.management-content { padding-top: var(--management-content-top); }
.management-shell.has-management-sidebar { display: grid; grid-template-columns: 216px minmax(0, 1fr); max-width: 1600px; margin-inline: auto; padding-left: 16px; }
.management-breadcrumb-row { display: flex; min-width: 0; align-items: center; gap: 12px; margin-bottom: 4px; }
.management-navigation-trigger { display: inline-flex; min-height: 44px; flex-shrink: 0; align-items: center; gap: 7px; padding: 0 10px; margin-left: -10px; border-radius: 10px; font-size: 12px; color: rgb(var(--color-slate-600)); cursor: pointer; }.management-navigation-trigger svg { width: 18px; height: 18px; }.management-navigation-trigger:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }.dark .management-navigation-trigger { color: rgb(var(--color-slate-300)); }
@media (hover: hover) { .management-navigation-trigger:hover { background: rgb(116 143 180 / 8%); } }
@media (max-width: 1023px) { .management-breadcrumb-row { margin-bottom: 8px; } }
</style>

