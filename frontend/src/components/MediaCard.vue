<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { MediaCardItem } from '../types/media'
import type { EmbyMatch } from '../types/emby'
import embyIcon from '../assets/emby.svg'
import { tmdbImageURL } from '../utils/tmdbImage'
import { vTooltip } from '../directives/tooltip'
import PosterPlaceholder from './PosterPlaceholder.vue'
import '../styles/mediaCards.css'

const props = withDefaults(defineProps<{ item: MediaCardItem; library?: EmbyMatch; showKindBadge?: boolean; titleLines?: 1 | 2 }>(), { showKindBadge: true, titleLines: 1 })
const imageFailed = ref(false)
const detailPath = computed(() => `/${props.item.kind}/${props.item.id}`)
const libraryLabel = computed(() => props.library?.state === 'stale' ? '已入库 · 待更新' : '已入库')
const ratingLabel = computed(() => props.item.votes > 0 && Number.isFinite(props.item.rating)
  ? props.item.rating.toFixed(1)
  : '暂无评分')

// 图片失败状态由卡片自己管理，切换影视或海报后重新尝试加载。
watch(() => [props.item.kind, props.item.id, props.item.posterUrl], () => { imageFailed.value = false })
</script>

<template>
  <RouterLink
    :to="detailPath"
    class="media-poster-card group block min-w-0 rounded-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-500"
    :aria-label="`${item.title}，查看详情`"
    v-tooltip="{ title: item.title, text: item.overview || (item.originalTitle !== item.title ? item.originalTitle : '') }"
  >
    <div class="poster-card-frame relative aspect-[2/3] overflow-hidden bg-slate-200 dark:bg-slate-700">
      <img
        v-if="item.posterUrl && !imageFailed"
        :key="`${item.kind}-${item.id}-${item.posterUrl}`"
        :src="tmdbImageURL(item.posterUrl)"
        :alt="item.title"
        loading="lazy"
        referrerpolicy="no-referrer"
        class="poster-card-image h-full w-full object-cover"
        @error="imageFailed = true"
      />
      <PosterPlaceholder v-else :title="item.title" />
      <span v-if="showKindBadge" class="poster-card-badge absolute left-2.5 top-2.5 px-2 py-0.5 text-[10px] leading-4">{{ item.kind === 'tv' ? '剧集' : '电影' }}</span>
      <span class="poster-card-badge absolute right-2.5 top-2.5 inline-flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] leading-4"><svg v-if="item.votes > 0 && Number.isFinite(item.rating)" class="h-2.5 w-2.5" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="m12 3 2.8 5.7 6.3.9-4.6 4.5 1.1 6.3-5.6-3-5.6 3 1.1-6.3L2.9 9.6l6.3-.9Z" /></svg>{{ ratingLabel }}</span>
    </div>
    <h2 class="poster-card-title px-0.5 text-slate-800 dark:text-slate-200" :class="titleLines === 2 ? 'media-card-title-two-lines' : 'truncate leading-6'">{{ item.title }}</h2>
    <div class="mt-0.5 flex flex-wrap items-center justify-between gap-x-2 gap-y-1 px-0.5 text-xs">
      <span class="shrink-0 text-slate-500 dark:text-slate-400 dark:text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ item.date?.slice(0, 4) || '年份未知' }}</span>
      <span v-if="library?.items.length" class="ml-auto inline-flex shrink-0 items-center gap-1 text-[11px] font-medium" :class="library.state === 'stale' ? 'text-amber-600' : 'text-emerald-600'" :aria-label="`Emby ${libraryLabel}`" v-tooltip="library.state === 'stale' ? 'Emby 索引待更新，当前显示上次入库记录' : undefined">
        <img :src="embyIcon" alt="" aria-hidden="true" class="h-3.5 w-3.5 shrink-0" />
        {{ libraryLabel }}
      </span>
    </div>
  </RouterLink>
</template>

<style scoped>
.media-card-title-two-lines {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  min-height: 40px;
  overflow: hidden;
  overflow-wrap: anywhere;
  line-height: 20px;
}
</style>
