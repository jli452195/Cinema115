<script setup lang="ts">
const value = defineModel<boolean>({ required: true })
defineProps<{ label: string; description?: string; disabled?: boolean }>()
</script>

<template>
  <div>
    <label class="flex cursor-pointer items-start gap-3">
      <input v-model="value" :disabled="disabled" type="checkbox" role="switch" :aria-label="label" class="peer sr-only" />
      <span class="relative mt-0.5 h-5 w-9 shrink-0 rounded-full bg-slate-300 dark:bg-slate-600 transition after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:bg-white after:shadow after:transition peer-checked:bg-blue-600 dark:peer-checked:bg-amber-500 peer-checked:after:translate-x-4 peer-focus-visible:ring-2 peer-focus-visible:ring-blue-400 dark:peer-focus-visible:ring-amber-500 peer-focus-visible:ring-offset-2 dark:peer-focus-visible:ring-offset-slate-900"></span>
      <span><span class="text-sm font-medium text-slate-700 dark:text-slate-300">{{ label }}</span><span v-if="description" class="mt-1 block text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ description }}</span></span>
    </label>
    <slot />
  </div>
</template>
