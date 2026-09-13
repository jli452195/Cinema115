<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { MediaCardItem } from '../types/media'
import type { EmbyMatch } from '../types/emby'
import embyIcon from '../assets/emby.svg'

const props = defineProps<{ item: MediaCardItem; library?: EmbyMatch }>()
const imageFailed = ref(false)
const detailPath = computed(() => `/${props.item.kind}/${props.item.id}`)
const libraryLabel = computed(() => props.library?.state === 'stale' ? '已入库 · 待更新' : '已入库')
const ratingLabel = computed(() => props.item.votes > 0 && Number.isFinite(props.item.rating)
  ? `★ ${props.item.rating.toFixed(1)}`
  : '暂无评分')

// 图片失败状态由卡片自己管理，切换影视或海报后重新尝试加载。
watch(() => [props.item.kind, props.item.id, props.item.posterUrl], () => { imageFailed.value = false })
</script>

<template>
  <RouterLink
    :to="detailPath"
    class="group block min-w-0 rounded-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-amber-400"
    :aria-label="`${item.title}，查看详情`"
    :title="item.overview || item.originalTitle || item.title"
  >
    <div class="relative aspect-[2/3] overflow-hidden rounded-xl bg-slate-200 shadow-[0_12px_24px_-14px_rgba(15,23,42,0.4)] transition group-hover:-translate-y-1 group-hover:shadow-lg motion-reduce:transform-none motion-reduce:transition-none">
      <img
        v-if="item.posterUrl && !imageFailed"
        :key="`${item.kind}-${item.id}-${item.posterUrl}`"
        :src="item.posterUrl"
        :alt="item.title"
        loading="lazy"
        referrerpolicy="no-referrer"
        class="h-full w-full object-cover"
        @error="imageFailed = true"
      />
      <div v-else class="flex h-full flex-col items-center justify-center gap-3 p-5 text-center text-slate-400">
        <svg class="h-9 w-9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" aria-hidden="true">
          <rect x="3" y="3" width="18" height="18" rx="3" />
          <path d="m3 16 5-5 4 4 3-3 6 6M15 8h.01" />
        </svg>
        <span class="text-xs">暂无海报</span>
      </div>
      <span class="absolute left-2.5 top-2.5 rounded bg-blue-600 px-2 py-0.5 text-[10px] font-semibold leading-4 text-white shadow-sm">{{ item.kind === 'tv' ? '剧集' : '电影' }}</span>
      <span class="absolute right-2.5 top-2.5 rounded bg-white/95 px-1.5 py-0.5 text-[10px] font-bold leading-4 text-amber-600 shadow-sm">{{ ratingLabel }}</span>
    </div>
    <h2 class="mt-2.5 truncate px-0.5 text-sm font-semibold leading-6 text-slate-800 group-hover:text-amber-600" :title="item.title">{{ item.title }}</h2>
    <div class="mt-0.5 flex flex-wrap items-center justify-between gap-x-2 gap-y-1 px-0.5 text-xs">
      <span class="shrink-0 text-slate-500">{{ item.date?.slice(0, 4) || '年份未知' }}</span>
      <span v-if="library?.items.length" class="ml-auto inline-flex shrink-0 items-center gap-1 text-[11px] font-medium" :class="library.state === 'stale' ? 'text-amber-600' : 'text-emerald-600'" :aria-label="`Emby ${libraryLabel}`" :title="library.state === 'stale' ? 'Emby 索引待更新，当前显示上次入库记录' : 'Emby 已入库'">
        <img :src="embyIcon" alt="" aria-hidden="true" class="h-3.5 w-3.5 shrink-0" />
        {{ libraryLabel }}
      </span>
    </div>
  </RouterLink>
</template>
