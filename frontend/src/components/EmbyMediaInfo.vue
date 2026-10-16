<script setup lang="ts">
import type { EmbyMatch } from '../types/emby'
import EmbyMediaVersions from './EmbyMediaVersions.vue'

defineProps<{ token: string; tmdbId: string; media?: EmbyMatch }>()
</script>

<template>
  <section v-if="media?.items.length" aria-label="电影媒体版本" class="movie-media mt-8 min-w-0">
    <h2 class="mb-3 text-lg font-bold">媒体版本</h2>
    <EmbyMediaVersions :token="token" kind="movie" :tmdb-id="tmdbId" :show-title="false" />
  </section>
</template>

<style scoped>
.movie-media { color: var(--md-sys-color-on-surface); }
</style>
