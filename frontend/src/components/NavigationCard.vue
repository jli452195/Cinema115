<script setup lang="ts">
import { useId } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

defineProps<{ to: RouteLocationRaw; title: string; description?: string }>()
const titleID = `navigation-card-title-${useId()}`
</script>

<template>
  <RouterLink :to="to" :aria-labelledby="titleID" class="navigation-card ambient-glow relative isolate flex min-w-0 items-start gap-3 rounded-2xl bg-white p-4 shadow-sm transition duration-[250ms] ease-out hover:bg-slate-50/60 active:scale-[0.99] active:bg-slate-100 active:duration-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-slate-500 motion-reduce:transition-none motion-reduce:active:scale-100 sm:gap-4 sm:p-5 dark:bg-slate-900 dark:hover:bg-slate-800/40 dark:active:bg-slate-800 dark:focus-visible:outline-slate-400">
    <span v-if="$slots.icon" class="relative z-10 flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400">
      <slot name="icon" />
    </span>
    <div class="relative z-10 min-w-0 flex-1">
      <div class="flex items-center justify-between gap-3">
        <h2 :id="titleID" class="min-w-0 break-words text-base font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ title }}</h2>
        <!-- Keep slot content informational: the whole card is a link. -->
        <slot name="trailing" />
      </div>
      <div v-if="description || $slots.default" class="mt-2 break-words text-sm leading-6 text-slate-500 dark:text-slate-400">
        <slot><p>{{ description }}</p></slot>
      </div>
    </div>
  </RouterLink>
</template>

<style scoped>
@media (hover: hover) and (pointer: fine) {
  .navigation-card:hover {
    z-index: 1;
    box-shadow: 0 12px 28px -12px rgb(15 23 42 / 0.18);
  }

  :global(.dark .navigation-card:hover) {
    box-shadow: 0 12px 28px -12px rgb(0 0 0 / 0.4);
  }
}

@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .navigation-card:hover { transform: translateY(-3px); }
  .navigation-card:hover:active { transform: translateY(-1px) scale(0.99); }
}
</style>
