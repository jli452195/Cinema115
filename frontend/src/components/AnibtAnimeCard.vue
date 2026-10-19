<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { IconPhotoOff, IconStar } from '@tabler/icons-vue'
import type { AnibtCatalogItem } from '../types/animeCatalog'
import { animeEditorEntry } from '../utils/animeEditorEntry'
const props = defineProps<{ item: AnibtCatalogItem; returnTo: string }>()
const failed = ref(false)
watch(() => [props.item.id, props.item.coverUrl], () => { failed.value = false })
const cover = computed(() => /^\/api\/anibt\/image\/[a-zA-Z0-9_-]{1,64}\/[a-f0-9]{64}\.webp$/.test(props.item.coverUrl) ? props.item.coverUrl : '')
const target = computed(() => animeEditorEntry(props.item.bangumiId, 'anibt', props.item.id, props.returnTo))
const schedule = computed(() => props.item.airingAt && Number.isFinite(props.item.airingAt) ? new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false }).format(props.item.airingAt * 1000) : '放送时间待定')
</script>

<template>
  <RouterLink :to="target" class="anibt-anime-card" :data-anibt-id="item.id" :aria-label="`${item.title}，配置订阅`">
    <span class="anibt-poster ambient-glow"><img v-if="cover && !failed" :src="cover" :alt="item.title" loading="lazy" decoding="async" @error="failed = true" /><span v-else class="anibt-placeholder"><IconPhotoOff :size="26" aria-hidden="true" /><span>暂无封面</span></span><span v-if="item.format" class="anibt-format">{{ item.format }}</span></span>
    <strong :title="item.title">{{ item.title }}</strong>
    <span v-if="item.originalTitle && item.originalTitle !== item.title" class="anibt-original" :title="item.originalTitle">{{ item.originalTitle }}</span><span v-else class="anibt-original" aria-hidden="true"></span>
    <span class="anibt-facts"><span>{{ item.episodes ? `全 ${item.episodes} 集` : '集数待定' }}</span><span v-if="item.rating != null && item.rating > 0" class="anibt-rating"><IconStar :size="12" aria-hidden="true" />{{ item.rating.toFixed(1) }}<span class="sr-only">分</span></span></span>
    <small>{{ schedule }}</small><small class="anibt-releases">{{ item.releaseCount ? `${item.releaseCount} 条资源` : '暂无资源，可保存草稿' }}</small>
  </RouterLink>
</template>

<style scoped>
.anibt-anime-card { display: block; min-width: 0; border-radius: 13px; text-decoration: none; }
.anibt-poster { display: block; position: relative; overflow: hidden; aspect-ratio: 3 / 4; border-radius: 13px; background: rgb(148 163 184 / .12); box-shadow: 0 10px 24px -17px rgb(15 23 42 / .5); transition: transform 200ms ease, box-shadow 200ms ease; }
.anibt-poster img { display: block; width: 100%; height: 100%; object-fit: cover; }
.anibt-placeholder { display: flex; height: 100%; gap: 8px; flex-direction: column; align-items: center; justify-content: center; font-size: 11px; color: rgb(var(--color-slate-500)); }
.anibt-format { position: absolute; left: 8px; bottom: 8px; padding: 2px 7px; border-radius: 6px; background: rgb(15 23 42 / .7); color: #fff; font-size: 10px; }
.anibt-anime-card strong { display: -webkit-box; overflow: hidden; -webkit-line-clamp: 2; -webkit-box-orient: vertical; min-height: 40px; margin-top: 10px; font-size: 13px; font-weight: 600; line-height: 20px; overflow-wrap: anywhere; color: rgb(var(--color-slate-800)); }
.anibt-original { display: block; height: 18px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 10px; line-height: 18px; color: rgb(var(--color-slate-500)); }
.anibt-facts { display: flex; align-items: center; gap: 8px; margin-top: 5px; font-size: 11px; color: rgb(var(--color-slate-600)); }
.anibt-rating { display: inline-flex; align-items: center; gap: 3px; color: #b37b34; }
.anibt-anime-card small { display: block; margin-top: 3px; font-size: 10px; line-height: 17px; color: rgb(var(--color-slate-500)); }
.anibt-releases { color: rgb(var(--color-slate-600)); }
.dark .anibt-anime-card strong { color: rgb(var(--color-slate-100)); }
.dark .anibt-facts, .dark .anibt-releases { color: rgb(var(--color-slate-400)); }
.dark .anibt-rating { color: #d7af77; }
.anibt-anime-card:focus-visible { outline: 2px solid #60a5fa; outline-offset: 5px; }
@media (hover: hover) and (pointer: fine) { .anibt-anime-card:hover strong { color: #d2668d; } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .anibt-anime-card:hover .anibt-poster { transform: translateY(-3px); box-shadow: 0 16px 26px -16px rgb(15 23 42 / .4); } }
@media (prefers-reduced-motion: reduce) { .anibt-poster { transition: none; } }
</style>
