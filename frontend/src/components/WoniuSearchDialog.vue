<script setup lang="ts">
import MediaResourceSearchDialog from './MediaResourceSearchDialog.vue'
defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; originalTitle: string; themeColors?: Record<string, string> }>()
defineEmits<{ close: [] }>()
</script>
<template><MediaResourceSearchDialog source="woniu" v-bind="$props" imdb-id="" @close="$emit('close')" /></template>
