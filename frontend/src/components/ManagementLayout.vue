<script setup lang="ts">
import PageBreadcrumb from './PageBreadcrumb.vue'
import WorkbenchHeader from './WorkbenchHeader.vue'

defineProps<{ title: string; description: string; parent?: { label: string; to: string } }>()
defineEmits<{ logout: [] }>()
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <main class="workbench-content management-content mx-auto w-full max-w-7xl min-w-0 flex-1 px-4 pb-6 sm:px-8 lg:px-10 lg:pb-8">
      <PageBreadcrumb :title="title" :parent="parent" class="mb-5" />
      <header class="management-heading mb-7 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-2">
        <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ title }}</h1>
        <div v-if="$slots.actions" class="flex items-center gap-3"><slot name="actions" /></div>
        <p class="col-span-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ description }}</p>
      </header>
      <slot />
    </main>
  </div>
</template>

