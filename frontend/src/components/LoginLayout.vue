<script setup lang="ts">
defineProps<{ subtitle: string }>()
</script>

<template>
  <main class="flex min-h-screen items-center justify-center bg-slate-950 px-4 py-10 sm:py-12">
    <div class="w-full max-w-md">
      <div class="mb-8 text-center text-white">
        <div aria-hidden="true" class="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-600 text-2xl font-bold shadow-lg shadow-blue-600/30">C</div>
        <h1 class="text-3xl font-semibold tracking-tight">Cinema115</h1>
        <p class="mt-2 text-sm text-slate-400">{{ subtitle }}</p>
      </div>
      <slot />
      <nav aria-label="登录入口" class="mt-6 text-center"><slot name="footer" /></nav>
    </div>
  </main>
</template>
