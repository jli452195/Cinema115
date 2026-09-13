<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { MediaCardItem } from '../types/media'

const props = defineProps<{ item: MediaCardItem }>()
const imageFailed = ref(false)
const detailPath = computed(() => `/${props.item.kind}/${props.item.id}`)
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
    <div class="relative aspect-[2/3] overflow-hidden rounded-xl bg-slate-200 shadow-sm transition group-hover:-translate-y-1 group-hover:shadow-lg motion-reduce:transform-none motion-reduce:transition-none">
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
      <div class="absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-black/50 to-transparent"></div>
      <span class="absolute left-2 top-2 rounded bg-slate-950/85 px-1.5 py-0.5 text-xs font-semibold text-white">{{ ratingLabel }}</span>
    </div>
    <h2 class="mt-3 line-clamp-2 text-sm font-semibold leading-6 text-slate-800 group-hover:text-amber-600">{{ item.title }}</h2>
    <p class="mt-1 text-xs text-slate-400">{{ item.date?.slice(0, 4) || '年份未知' }}</p>
  </RouterLink>
</template>
