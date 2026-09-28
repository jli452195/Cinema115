<script setup lang="ts">
import type { EmbyMatch } from '../types/emby'
import EmbyMediaVersions from './EmbyMediaVersions.vue'

defineProps<{ token: string; tmdbId: string; media?: EmbyMatch }>()
</script>

<template>
  <section v-if="media?.items.length" aria-label="电影媒体版本" class="mt-8 rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900/90 p-5">
    <EmbyMediaVersions :token="token" kind="movie" :tmdb-id="tmdbId" :show-title="false" />
  </section>
</template>
