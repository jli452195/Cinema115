<script setup lang="ts">
import MediaResourceSearchDialog from './MediaResourceSearchDialog.vue'
defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; originalTitle: string; imdbId: string; themeColors?: Record<string, string> }>()
defineEmits<{ close: [] }>()
</script>
<template><MediaResourceSearchDialog source="gying" v-bind="$props" @close="$emit('close')" /></template>
