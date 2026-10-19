<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { IconPhotoOff } from '@tabler/icons-vue'
import type { GardenCatalogItem } from '../types/animeCatalog'
import { animeEditorEntry } from '../utils/animeEditorEntry'
const props = defineProps<{ item: GardenCatalogItem; returnTo: string }>()
const failed = ref(false)
watch(() => [props.item.id, props.item.coverUrl], () => { failed.value = false })
const cover = computed(() => /^\/api\/garden\/image\/[1-9]\d{0,7}\/poster\.jpeg$/.test(props.item.coverUrl) ? props.item.coverUrl : '')
const target = computed(() => animeEditorEntry(props.item.bangumiId, 'garden', props.item.id, props.returnTo))
</script>

<template>
  <RouterLink :to="target" class="garden-anime-card" :data-garden-id="item.id" :aria-label="`${item.title}，配置订阅`">
    <span class="garden-poster ambient-glow"><img v-if="cover && !failed" :src="cover" :alt="item.title" loading="lazy" decoding="async" @error="failed = true" /><span v-else class="garden-placeholder"><IconPhotoOff :size="26" aria-hidden="true" /><span>暂无封面</span></span><span v-if="item.platform" class="garden-platform">{{ item.platform }}</span></span>
    <strong :title="item.title">{{ item.title }}</strong>
    <span class="garden-original" :title="item.originalTitle !== item.title ? item.originalTitle : undefined">{{ item.originalTitle !== item.title ? item.originalTitle : '' }}</span>
    <small>{{ item.airDate ? `${item.airDate} 首播` : '首播日期待定' }}</small>
  </RouterLink>
</template>

<style scoped>
.garden-anime-card { display: block; min-width: 0; border-radius: 13px; text-decoration: none; }
.garden-poster { display: block; position: relative; overflow: hidden; aspect-ratio: 3 / 4; border-radius: 13px; background: rgb(148 163 184 / .12); box-shadow: 0 10px 24px -17px rgb(15 23 42 / .5); transition: transform 200ms ease, box-shadow 200ms ease; }
.garden-poster img { display: block; width: 100%; height: 100%; object-fit: cover; }
.garden-placeholder { display: flex; height: 100%; gap: 8px; flex-direction: column; align-items: center; justify-content: center; font-size: 11px; color: rgb(var(--color-slate-500)); }
.garden-platform { position: absolute; left: 8px; bottom: 8px; padding: 2px 7px; border-radius: 6px; background: rgb(15 23 42 / .7); color: #fff; font-size: 10px; }
.garden-anime-card strong { display: -webkit-box; overflow: hidden; -webkit-line-clamp: 2; -webkit-box-orient: vertical; min-height: 40px; margin-top: 10px; font-size: 13px; font-weight: 600; line-height: 20px; overflow-wrap: anywhere; color: rgb(var(--color-slate-800)); }
.garden-original { display: block; height: 18px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 10px; line-height: 18px; color: rgb(var(--color-slate-500)); }
.garden-anime-card small { display: block; margin-top: 5px; font-size: 11px; line-height: 18px; color: rgb(var(--color-slate-600)); }
.dark .garden-anime-card strong { color: rgb(var(--color-slate-100)); }
.dark .garden-anime-card small { color: rgb(var(--color-slate-400)); }
.garden-anime-card:focus-visible { outline: 2px solid #60a5fa; outline-offset: 5px; }
@media (hover: hover) and (pointer: fine) { .garden-anime-card:hover strong { color: #c45e86; } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .garden-anime-card:hover .garden-poster { transform: translateY(-3px); box-shadow: 0 16px 26px -16px rgb(15 23 42 / .4); } }
@media (prefers-reduced-motion: reduce) { .garden-poster { transition: none; } }
</style>
