<script setup lang="ts">
import { computed, ref } from 'vue'
import type { EmbyMissingSeason, EmbyMissingSeries } from '../types/emby'
import { useLazyPoster } from '../composables/useLazyPoster'

const props = defineProps<{ item: EmbyMissingSeries; token: string }>()
defineEmits<{ details: [] }>()
const card = ref<HTMLElement>()
const posterURL = useLazyPoster(card, () => props.item.posterUrl, () => props.token)
const path = computed(() => `/tv/${props.item.tmdbId}`)
function rangesLabel(season: EmbyMissingSeason, limit = season.ranges.length) {
  const ranges = season.ranges.slice(0, limit)
  const label = ranges.map(range => range.first === range.last ? `第${range.first}集` : `第${range.first}-${range.last}集`).join('、')
  return `缺${label}${ranges.length < season.ranges.length ? `等共${season.missingCount}集` : ''}`
}
const summary = computed(() => {
  const first = props.item.seasons[0]
  if (!first) return ''
  const label = `第 ${first.season} 季：${rangesLabel(first, 2)}`
  return props.item.seasons.length === 1
    ? `${label}（截至第 ${first.lastEpisode} 集）`
    : `${label}；另 ${props.item.seasons.length - 1} 季缺集`
})
</script>

<template>
  <article ref="card" class="group relative min-w-0 overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-3 transition-all duration-300 ease-out hover:-translate-y-1 hover:border-amber-300/60 hover:shadow-xl hover:shadow-slate-900/10 dark:hover:border-amber-500/40 dark:hover:shadow-black/40" data-missing-card>
    <div v-if="posterURL" class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <img :src="posterURL" alt="" class="absolute -left-1/4 -top-1/4 h-[150%] w-[150%] object-cover blur-3xl opacity-50 saturate-150 transition-opacity duration-500 ease-out group-hover:opacity-70 dark:opacity-40 dark:group-hover:opacity-60 transform-gpu backface-hidden" @error="posterURL = ''" />
      <div class="absolute inset-0 bg-white/40 transition-colors duration-500 ease-out group-hover:bg-white/20 dark:bg-slate-900/50 dark:group-hover:bg-slate-900/30"></div>
      <div class="absolute inset-0 bg-gradient-to-t from-white via-white/80 to-white/10 dark:from-slate-900 dark:via-slate-900/80 dark:to-slate-900/10"></div>
    </div>
    <div class="relative z-10 flex min-h-[112px] min-w-0 items-start gap-3">
        <component :is="item.tmdbId ? 'RouterLink' : 'div'" :to="item.tmdbId ? path : undefined" :aria-label="item.tmdbId ? `查看${item.title}` : undefined" class="flex h-24 w-16 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-slate-100 shadow-sm dark:bg-slate-800 sm:h-28 sm:w-[74px]">
          <img v-if="posterURL" :src="posterURL" :alt="item.title" class="h-full w-full object-cover transition-transform duration-500 ease-out group-hover:scale-110" @error="posterURL = ''" />
          <svg v-else aria-hidden="true" class="h-7 w-7 text-slate-400 dark:text-slate-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="m10 8 6 4-6 4V8Z"/></svg>
        </component>
        <div class="flex min-h-[112px] min-w-0 flex-1 flex-col">
          <h2 class="truncate text-sm font-semibold leading-5 text-slate-900 dark:text-slate-100" :title="item.title">
            <RouterLink v-if="item.tmdbId" :to="path" class="hover:text-amber-700 dark:hover:text-amber-300">{{ item.title }}</RouterLink><template v-else>{{ item.title }}</template>
          </h2>
          <div class="mt-1 flex min-w-0 items-center gap-2">
            <span class="shrink-0 rounded bg-amber-50 px-1.5 py-0.5 text-xs font-medium leading-4 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300">共缺 {{ item.missingCount.toLocaleString() }} 集</span>
            <span class="truncate text-[11px] leading-4 text-slate-500 dark:text-slate-400">{{ item.tmdbId ? `TMDB ${item.tmdbId}` : '未关联 TMDB' }}</span>
          </div>
          <p class="mt-1 line-clamp-2 break-words text-xs leading-[18px] text-slate-600 dark:text-slate-300" :title="summary" data-missing-summary>{{ summary }}</p>
          <div class="mt-auto flex min-h-7 flex-wrap items-center gap-x-3 pt-1 text-xs">
            <button type="button" aria-haspopup="dialog" class="inline-flex min-h-6 items-center text-amber-800 hover:underline dark:text-amber-300" @click="$emit('details')">缺集明细</button>
            <template v-if="item.tmdbId">
              <RouterLink :to="{ path, query: { botSearch: '1' } }" class="inline-flex min-h-6 items-center text-blue-600 hover:underline dark:text-blue-400">搜索资源</RouterLink>
              <RouterLink :to="path" class="inline-flex min-h-6 items-center text-slate-600 hover:text-blue-600 dark:text-slate-400 dark:hover:text-blue-400">详情</RouterLink>
            </template>
          </div>
        </div>
    </div>
  </article>
</template>
