<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import type { PersonItem } from '../types/person'
import { tmdbImageURL } from '../utils/tmdbImage'
import PosterPlaceholder from './PosterPlaceholder.vue'
import '../styles/mediaCards.css'
const props = defineProps<{ person: PersonItem }>()
const route = useRoute()
const failed = ref(false)
watch(() => props.person.profileUrl, () => { failed.value = false })
</script>
<template>
  <RouterLink :to="{ path: `/person/${person.id}`, query: { q: route.query.q, page: route.query.page } }" :aria-label="`${person.name}，查看演员`" class="media-poster-card group min-w-0 rounded-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-500">
    <div class="poster-card-frame aspect-[2/3] overflow-hidden bg-slate-200 dark:bg-slate-700"><img v-if="person.profileUrl && !failed" :src="tmdbImageURL(person.profileUrl)" :alt="person.name" loading="lazy" referrerpolicy="no-referrer" class="poster-card-image h-full w-full object-cover" @error="failed = true"/><PosterPlaceholder v-else :title="person.name" /></div>
    <h2 class="poster-card-title truncate leading-6 text-slate-800 dark:text-slate-200">{{ person.name }}</h2>
    <p v-if="person.originalName && person.originalName !== person.name" class="mt-1 truncate text-xs text-slate-400 dark:text-slate-500">{{ person.originalName }}</p>
    <p class="mt-2 line-clamp-2 text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ person.knownFor.length ? `代表作：${person.knownFor.join('、')}` : '暂无代表作信息' }}</p>
  </RouterLink>
</template>
