<script setup lang="ts">
const value = defineModel<boolean>({ required: true })
defineProps<{ label: string; description?: string }>()
</script>

<template>
  <div>
    <label class="flex cursor-pointer items-start gap-3">
      <input v-model="value" type="checkbox" role="switch" :aria-label="label" class="peer sr-only" />
      <span class="relative mt-0.5 h-5 w-9 shrink-0 rounded-full bg-slate-300 transition after:absolute after:left-0.5 after:top-0.5 after:h-4 after:w-4 after:rounded-full after:bg-white after:shadow after:transition peer-checked:bg-blue-600 peer-checked:after:translate-x-4 peer-focus-visible:ring-2 peer-focus-visible:ring-blue-400 peer-focus-visible:ring-offset-2"></span>
      <span><span class="text-sm font-medium text-slate-700">{{ label }}</span><span v-if="description" class="mt-1 block text-xs leading-6 text-slate-500">{{ description }}</span></span>
    </label>
    <slot />
  </div>
</template>
