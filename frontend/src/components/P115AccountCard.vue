<script setup lang="ts">
defineProps<{ name: string; configId: string; status: string; statusClass: string }>()
</script>

<template>
  <section class="min-w-0 overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 shadow-sm">
    <header class="flex flex-wrap items-start justify-between gap-4 border-b border-slate-100 dark:border-slate-800 p-5 sm:p-6">
      <div class="flex min-w-0 items-start gap-3">
        <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600"><svg viewBox="0 0 24 24" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z" /></svg></span>
        <div class="min-w-0"><h2 class="break-words text-base font-semibold text-slate-800 dark:text-slate-200">{{ name }}</h2><p class="mt-1.5 text-xs text-slate-400 dark:text-slate-500">配置 ID：{{ configId }}</p></div>
      </div>
      <span class="rounded-full px-3 py-1 text-xs font-medium" :class="statusClass" role="status">{{ status }}</span>
      <div class="flex w-full flex-wrap items-center gap-2"><slot name="actions" /></div>
    </header>
    <div class="space-y-5 p-5 sm:p-6"><slot /></div>
    <footer class="border-t border-slate-100 dark:border-slate-800 bg-slate-50 dark:bg-slate-950/60 px-5 py-4 sm:px-6"><slot name="footer" /></footer>
  </section>
</template>
