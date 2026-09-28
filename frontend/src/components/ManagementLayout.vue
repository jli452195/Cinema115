<script setup lang="ts">
import { RouterLink } from 'vue-router'
import WorkbenchHeader from './WorkbenchHeader.vue'

defineProps<{ title: string; description: string; parent?: { label: string; to: string } }>()
defineEmits<{ logout: [] }>()
</script>

<template>
  <div class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="$emit('logout')" />
    <main class="management-content mx-auto w-full max-w-7xl min-w-0 flex-1 px-4 py-6 sm:px-8 lg:px-10 lg:py-8">
      <nav aria-label="当前位置" class="mb-5 flex flex-wrap items-center gap-2 text-xs leading-5 text-slate-400 dark:text-slate-500">
        <RouterLink to="/" class="rounded hover:text-slate-700 dark:hover:text-slate-300 dark:text-slate-300 dark:hover:text-slate-300">首页</RouterLink>
        <template v-if="parent"><span aria-hidden="true">/</span><RouterLink :to="parent.to" class="rounded hover:text-slate-700 dark:hover:text-slate-300 dark:text-slate-300">{{ parent.label }}</RouterLink></template>
        <span aria-hidden="true">/</span><span aria-current="page" class="text-slate-600 dark:text-slate-300">{{ title }}</span>
      </nav>
      <header class="management-heading mb-7 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-2">
        <h1 class="min-w-0 text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">{{ title }}</h1>
        <div v-if="$slots.actions" class="flex items-center gap-3"><slot name="actions" /></div>
        <p class="col-span-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ description }}</p>
      </header>
      <slot />
    </main>
  </div>
</template>

<style scoped>
nav a:focus-visible { outline: 2px solid #f59e0b; outline-offset: 3px; }
</style>
