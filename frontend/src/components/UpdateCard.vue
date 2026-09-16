<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { RecentUpdateItem } from '../types/updates'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ item: RecentUpdateItem }>()
const imageFailed = ref(false)
const path = computed(() => `/${props.item.kind}/${props.item.tmdbId}`)
const rating = computed(() => props.item.votes > 0 && Number.isFinite(props.item.rating) ? `★ ${props.item.rating.toFixed(1)}` : '')
watch(() => props.item.posterUrl, () => { imageFailed.value = false })
</script>

<template>
  <RouterLink :to="path" class="group block min-w-0 rounded-lg focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400" :aria-label="`查看${item.title}`">
    <div class="relative aspect-[2/3] overflow-hidden rounded-lg bg-slate-200 shadow-[0_12px_24px_-14px_rgba(15,23,42,0.4)] transition group-hover:-translate-y-1 group-hover:shadow-lg motion-reduce:transform-none motion-reduce:transition-none">
      <img v-if="item.posterUrl && !imageFailed" :src="tmdbImageURL(item.posterUrl)" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="imageFailed = true" />
      <div v-else class="flex h-full flex-col items-center justify-center gap-3 p-5 text-center text-slate-400">
        <svg class="h-9 w-9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="m3 16 5-5 4 4 3-3 6 6M15 8h.01"/></svg>
        <span class="text-xs">暂无海报</span>
      </div>
      <span v-if="item.episodeLabel" class="absolute left-2 top-2 max-w-[calc(100%-4.5rem)] truncate rounded-md bg-white/95 px-2 py-1 font-mono text-[10px] font-semibold leading-4 text-amber-600 shadow-sm" :title="item.episodeLabel">{{ item.episodeLabel }}</span>
      <span class="absolute right-2 top-2 rounded-md bg-white/95 px-2 py-1 text-[10px] font-medium leading-4 text-slate-600 shadow-sm">{{ item.kind === 'tv' ? '剧集' : '电影' }}</span>
      <span v-if="rating" class="absolute bottom-2 right-2 rounded-md bg-white/95 px-2 py-1 text-[10px] font-bold leading-4 text-amber-600 shadow-sm">{{ rating }}</span>
    </div>
    <h3 class="update-title mt-2.5 px-0.5 text-sm font-semibold leading-5 text-slate-800 group-hover:text-amber-600" :title="item.title">{{ item.title }}</h3>
    <p class="mt-1 px-0.5 text-xs text-slate-400">{{ item.releaseDate?.slice(0, 4) || (item.metadataMissing ? '资料待补充' : '年份未知') }}</p>
  </RouterLink>
</template>

<style scoped>
.update-title { min-height: 2.5rem; display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
</style>

