<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineProps<{ title: string; parent?: { label: string; to: string } }>()
</script>

<template>
  <nav aria-label="当前位置" class="flex flex-wrap items-center gap-2 text-xs leading-5 text-slate-400 dark:text-slate-400">
    <RouterLink to="/" class="rounded hover:text-slate-700 dark:hover:text-slate-200">首页</RouterLink>
    <template v-if="parent"><span aria-hidden="true">/</span><RouterLink :to="parent.to" class="rounded hover:text-slate-700 dark:hover:text-slate-200">{{ parent.label }}</RouterLink></template>
    <span aria-hidden="true">/</span><span aria-current="page" class="text-slate-600 dark:text-slate-300">{{ title }}</span>
  </nav>
</template>

<style scoped>
a:focus-visible { outline: 2px solid rgb(var(--color-accent)); outline-offset: 3px; }
</style>
