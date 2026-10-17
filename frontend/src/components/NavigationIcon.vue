<script setup lang="ts">
import { accountIcons } from '../utils/accountNavigation'

withDefaults(defineProps<{ name: keyof typeof accountIcons; size?: number }>(), { size: 19 })
</script>

<template>
  <component :is="accountIcons[name]" :size="size" stroke="1.75" aria-hidden="true" focusable="false" />
</template>
