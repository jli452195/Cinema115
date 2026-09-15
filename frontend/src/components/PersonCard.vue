<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import type { PersonItem } from '../types/person'
import { tmdbImageURL } from '../utils/tmdbImage'
const props = defineProps<{ person: PersonItem }>()
const route = useRoute()
const failed = ref(false)
watch(() => props.person.profileUrl, () => { failed.value = false })
</script>
<template>
  <RouterLink :to="{ path: `/person/${person.id}`, query: { q: route.query.q, page: route.query.page } }" :aria-label="`${person.name}，查看演员`" class="group min-w-0 rounded-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400">
    <div class="aspect-[2/3] overflow-hidden rounded-xl bg-slate-200"><img v-if="person.profileUrl && !failed" :src="tmdbImageURL(person.profileUrl)" :alt="person.name" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failed = true"/><div v-else class="flex h-full items-center justify-center text-sm text-slate-400">暂无照片</div></div>
    <h2 class="mt-3 truncate text-sm font-semibold text-slate-800 group-hover:text-amber-600">{{ person.name }}</h2>
    <p v-if="person.originalName && person.originalName !== person.name" class="mt-1 truncate text-xs text-slate-400">{{ person.originalName }}</p>
    <p class="mt-2 line-clamp-2 text-xs leading-5 text-slate-500">{{ person.knownFor.length ? `代表作：${person.knownFor.join('、')}` : '暂无代表作信息' }}</p>
  </RouterLink>
</template>
