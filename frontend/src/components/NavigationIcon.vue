<script setup lang="ts">
import { accountIcons } from '../utils/accountNavigation'

withDefaults(defineProps<{ name: keyof typeof accountIcons; size?: number; stroke?: number | string }>(), { size: 19, stroke: 1.75 })
</script>

<template>
  <component :is="accountIcons[name]" :size="size" :stroke="String(stroke)" aria-hidden="true" focusable="false" />
</template>
