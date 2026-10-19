<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { IconPhotoOff } from '@tabler/icons-vue'
import type { MikanCatalogItem } from '../types/animeCatalog'
import { animeEditorEntry } from '../utils/animeEditorEntry'
const props = defineProps<{ item: MikanCatalogItem; returnTo: string }>()
const target = computed(() => animeEditorEntry('', 'mikan', props.item.id, props.returnTo))
const failed = ref(false)
watch(() => [props.item.id, props.item.coverUrl], () => { failed.value = false })
const cover = () => /^\/api\/mikan\/image\/\d{6}\/[a-zA-Z0-9._-]+$/.test(props.item.coverUrl) ? props.item.coverUrl : ''
</script>

<template>
  <RouterLink :to="target" class="mikan-anime-card" :data-mikan-id="item.id" :aria-label="`${item.title}，配置订阅`">
    <span class="mikan-anime-poster ambient-glow"><img v-if="cover() && !failed" :src="cover()" :alt="item.title" loading="lazy" decoding="async" @error="failed = true" /><span v-else class="mikan-anime-placeholder"><IconPhotoOff :size="26" aria-hidden="true" /><span>暂无封面</span></span></span>
    <strong :title="item.title">{{ item.title }}</strong>
    <small>{{ item.updatedDate ? `资源更新 ${item.updatedDate}` : '蜜柑资源条目' }}</small>
  </RouterLink>
</template>

<style scoped>
.mikan-anime-card { display: block; width: 100%; min-width: 0; border-radius: 12px; text-align: left; }
.mikan-anime-poster { display: block; position: relative; aspect-ratio: 3 / 4; overflow: hidden; border-radius: 13px; background: rgb(148 163 184 / .12); box-shadow: 0 10px 24px -17px rgb(15 23 42 / .5); transition: transform 200ms ease, box-shadow 200ms ease; }
.mikan-anime-poster img { width: 100%; height: 100%; display: block; object-fit: cover; }
.mikan-anime-placeholder { display: flex; height: 100%; flex-direction: column; gap: 8px; align-items: center; justify-content: center; color: rgb(var(--color-slate-500)); font-size: 11px; }
.mikan-anime-card strong { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; min-height: 40px; margin-top: 11px; font-size: 13px; line-height: 20px; font-weight: 600; color: rgb(var(--color-slate-800)); }
.mikan-anime-card small { display: block; margin-top: 4px; font-size: 11px; line-height: 18px; color: rgb(var(--color-slate-500)); }
.mikan-anime-card:focus-visible { outline: 2px solid #60a5fa; outline-offset: 5px; }
.dark .mikan-anime-card strong { color: rgb(var(--color-slate-100)); }
.dark .mikan-anime-card small { color: rgb(var(--color-slate-400)); }
@media (hover: hover) and (pointer: fine) { .mikan-anime-card:hover strong { color: #3b82f6; } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .mikan-anime-card:hover .mikan-anime-poster { transform: translateY(-3px); box-shadow: 0 16px 26px -16px rgb(15 23 42 / .4); } }
@media (prefers-reduced-motion: reduce) { .mikan-anime-poster { transition: none; } }
</style>
