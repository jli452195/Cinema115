<script setup lang="ts">
import { ref, watch } from 'vue'
import { IconPhotoOff } from '@tabler/icons-vue'
import type { AnimeCardItem } from '../types/anime'
import { animeImageURL } from '../composables/useAnimeDiscovery'

const props = withDefaults(defineProps<{ item: AnimeCardItem; actionLabel?: string; hideMeta?: boolean; highlight?: boolean }>(), { highlight: true })
defineEmits<{ select: [item: AnimeCardItem] }>()
const failed = ref(false)
watch(() => [props.item.id, props.item.coverUrl], () => { failed.value = false })
</script>

<template>
  <button type="button" class="anime-card group" :class="{ 'anime-card-highlight': highlight }" :aria-label="`${item.title}，${actionLabel || '查看篇章'}`" aria-haspopup="dialog" :data-anime-id="item.id" @click="$emit('select', item)">
    <span class="anime-poster" :class="{ 'ambient-glow': highlight }">
      <img v-if="animeImageURL(item.coverUrl) && !failed" :src="animeImageURL(item.coverUrl)" :alt="item.title" loading="lazy" decoding="async" referrerpolicy="no-referrer" @error="failed = true" />
      <span v-else class="anime-poster-empty"><IconPhotoOff :size="28" aria-hidden="true" /><span>暂无海报</span></span>
      <slot name="badge" />
    </span>
    <span class="anime-card-title" :title="item.title">{{ item.title }}</span>
    <span v-if="!hideMeta" class="anime-card-date"><slot name="meta">{{ item.date ? `${item.date} 首播` : '首播日期未收录' }}</slot></span>
  </button>
</template>

<style scoped>
/* Reuse MediaCard's poster proportions, fixed two-line title and subtle hover;
   keep Bangumi identity separate from the TMDB/Emby card model. */
.anime-card { display: block; width: 100%; min-width: 0; text-align: left; border-radius: 12px; }
.anime-poster { display: block; position: relative; aspect-ratio: 2 / 3; overflow: hidden; border-radius: 12px; background: rgb(var(--color-slate-200)); box-shadow: 0 12px 24px -14px rgb(15 23 42 / .4); transition: transform 220ms ease, box-shadow 220ms ease; }
.anime-poster img { display: block; width: 100%; height: 100%; object-fit: cover; }
.anime-poster::before, .anime-poster::after { z-index: 2; }
.anime-poster-empty { display: flex; height: 100%; flex-direction: column; align-items: center; justify-content: center; gap: 10px; color: rgb(var(--color-slate-500)); font-size: 12px; }
.anime-card-title { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; line-clamp: 2; overflow: hidden; overflow-wrap: anywhere; min-height: 40px; margin-top: 12px; line-height: 20px; font-size: 14px; font-weight: 600; color: rgb(var(--color-slate-800)); }
.anime-card-date { display: block; margin-top: 4px; font-size: 11px; line-height: 18px; color: rgb(var(--color-slate-500)); }
.anime-card:focus-visible { outline: 2px solid #60a5fa; outline-offset: 5px; }
.dark .anime-poster { background: rgb(var(--color-slate-800)); }
.dark .anime-card-title { color: rgb(var(--color-slate-100)); }
.dark .anime-card-date, .dark .anime-poster-empty { color: rgb(var(--color-slate-400)); }
@media (hover: hover) and (pointer: fine) { .anime-card:hover .anime-poster { box-shadow: 0 16px 28px -12px rgb(15 23 42 / .3); } .anime-card-highlight:hover .anime-card-title { color: #3b82f6; } }
.dark .anime-card-highlight:hover .anime-card-title { color: #93c5fd; }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .anime-card:hover .anime-poster { transform: translateY(-3px); } }
@media (prefers-reduced-motion: reduce) { .anime-poster { transition: none; } }
</style>
