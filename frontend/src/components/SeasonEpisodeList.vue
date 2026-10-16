<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import type { EmbyMatch } from '../types/emby'
import type { TMDBEpisode } from '../composables/useTMDBSeasons'
import { episodeHasAired } from '../types/seasonLibrary'
import { tmdbImageURL } from '../utils/tmdbImage'
import EmbyMediaVersions from './EmbyMediaVersions.vue'

const props = defineProps<{
  token: string; tmdbId: string; season: number; episodes: TMDBEpisode[]
  mediaState?: EmbyMatch['state']; ownedEpisodes?: Set<number>; admin: boolean; today: string
}>()
const openEpisode = ref<number>()
const failedImages = ref(new Set<string>())
const rows = computed(() => props.episodes.map(episode => {
  const available = (props.mediaState === 'present' || props.mediaState === 'stale') && !!props.ownedEpisodes?.has(episode.number)
  const aired = episodeHasAired(episode, props.today)
  const status = available ? props.mediaState === 'stale' ? '已入库 · 待更新' : '已入库'
    : !aired ? episode.date ? '未播出' : '播出日期待定'
      : props.mediaState === 'present' || props.mediaState === 'absent' ? '未入库' : ''
  return { episode, available, status, tone: available ? 'owned' : !aired ? 'pending' : 'missing' }
}))
const buttonId = (number: number) => `episode-${props.tmdbId}-${props.season}-${number}-button`
const panelId = (number: number) => `episode-${props.tmdbId}-${props.season}-${number}-details`

async function toggleEpisode(number: number) {
  const button = document.getElementById(buttonId(number))
  const previousTop = button?.getBoundingClientRect().top ?? 0
  openEpisode.value = openEpisode.value === number ? undefined : number
  await nextTick()
  if (!button?.isConnected) return
  button.focus({ preventScroll: true })
  const headerBottom = document.querySelector('.workbench-header')?.getBoundingClientRect().bottom || 0
  const targetTop = Math.max(headerBottom + 12, Math.min(previousTop, window.innerHeight - button.offsetHeight - 12))
  window.scrollBy({ top: button.getBoundingClientRect().top - targetTop, behavior: 'instant' })
}
</script>

<template>
  <div class="episode-list" aria-label="分集列表">
    <article v-for="{ episode, available, status, tone } in rows" :key="episode.id" class="episode-row" :class="{ 'is-open': openEpisode === episode.number }" :data-episode-number="episode.number">
      <h3 class="episode-heading">
        <button :id="buttonId(episode.number)" type="button" class="episode-toggle" :aria-expanded="openEpisode === episode.number" :aria-controls="panelId(episode.number)" :aria-label="`第 ${episode.number} 集 ${episode.name || ''}${status ? '，' + status : ''}，${openEpisode === episode.number ? '收起详情' : '展开详情'}`" @click="toggleEpisode(episode.number)">
          <span class="episode-still" aria-hidden="true">
            <img v-if="episode.stillUrl && !failedImages.has(episode.stillUrl)" :src="tmdbImageURL(episode.stillUrl)" alt="" loading="lazy" referrerpolicy="no-referrer" @error="failedImages.add(episode.stillUrl)" />
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="3" y="5" width="18" height="14" rx="3" /><path d="m9 9 6 3-6 3Z" /></svg>
          </span>
          <span class="episode-copy">
            <span class="episode-title-line"><span class="episode-number">{{ String(episode.number).padStart(2, '0') }}</span><span class="episode-name">{{ episode.name || `第 ${episode.number} 集` }}</span><span v-if="status" class="episode-status" :class="`episode-status-${tone}`">{{ status }}</span></span>
            <span v-if="episode.date || available || episode.runtime || episode.votes > 0" class="episode-meta"><span v-if="episode.date || available">{{ episode.date || '播出日期待定' }}</span><span v-if="episode.runtime">{{ episode.runtime }} 分钟</span><span v-if="episode.votes > 0" class="episode-rating"><svg aria-hidden="true" viewBox="0 0 24 24" fill="currentColor"><path d="m12 3 2.8 5.7 6.3.9-4.6 4.5 1.1 6.3-5.6-3-5.6 3 1.1-6.3L2.9 9.6l6.3-.9Z" /></svg>{{ episode.rating.toFixed(1) }}</span></span>
            <span v-if="episode.overview && openEpisode !== episode.number" class="episode-preview" aria-hidden="true">{{ episode.overview }}</span>
          </span>
          <svg aria-hidden="true" class="episode-chevron" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5"><path d="m5 8 5 5 5-5" /></svg>
        </button>
      </h3>
      <div v-if="openEpisode === episode.number" :id="panelId(episode.number)" role="region" :aria-labelledby="buttonId(episode.number)" class="episode-detail">
        <p class="episode-overview">{{ episode.overview || '暂无分集简介。' }}</p>
        <EmbyMediaVersions v-if="admin && available" :token="token" kind="tv" :tmdb-id="tmdbId" :season="season" :episode="episode.number" :show-title="false" eager compact class="episode-media" />
        <button type="button" class="episode-close" :aria-controls="panelId(episode.number)" :aria-label="`收起第 ${episode.number} 集详情`" @click="toggleEpisode(episode.number)">收起本集</button>
      </div>
    </article>
  </div>
</template>

<style scoped>
.episode-row + .episode-row { border-top: 1px solid color-mix(in srgb, var(--detail-media-outline) 65%, transparent); }
.episode-heading { margin: 0; }
.episode-toggle { display: grid; grid-template-columns: 112px minmax(0, 1fr) 16px; align-items: center; gap: 16px; width: 100%; min-height: 96px; padding: 12px 0; border: 0; border-radius: 8px; background: transparent; color: var(--md-sys-color-on-surface); text-align: left; cursor: pointer; }
.episode-toggle:focus-visible { outline: 2px solid var(--md-sys-color-primary); outline-offset: 3px; }
.episode-still { display: grid; place-items: center; width: 100%; aspect-ratio: 16 / 9; overflow: hidden; border-radius: 8px; background: var(--detail-media-chip); color: var(--detail-media-muted); }
.episode-still img { width: 100%; height: 100%; object-fit: cover; }
.episode-still > svg { width: 24px; height: 24px; }
.episode-copy { min-width: 0; }
.episode-title-line { display: flex; align-items: flex-start; gap: 8px; min-width: 0; }
.episode-number { flex-shrink: 0; color: var(--detail-media-muted); font-size: 12px; font-weight: 500; line-height: 22px; font-variant-numeric: tabular-nums; }
.episode-name { display: -webkit-box; min-width: 0; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow-wrap: anywhere; font-size: 14px; line-height: 22px; font-weight: 600; }
.is-open .episode-name { display: block; }
.episode-status { flex-shrink: 0; margin-top: 2px; padding: 0 6px; border-radius: 5px; font-size: 10px; line-height: 18px; font-weight: 500; }
.episode-status-owned { background: rgb(16 185 129 / 8%); color: #047857; }
.episode-status-missing { background: rgb(217 119 6 / 7%); color: #a16207; }
.episode-status-pending { background: var(--detail-media-chip); color: var(--detail-media-muted); }
.episode-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 3px 12px; margin-top: 5px; color: var(--detail-media-muted); font-size: 11px; line-height: 18px; font-weight: 400; }
.episode-rating { display: inline-flex; align-items: center; gap: 3px; color: #a16207; }
.episode-rating svg { width: 12px; height: 12px; }
.episode-preview { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 1; margin-top: 4px; color: var(--detail-media-muted); font-size: 12px; line-height: 20px; font-weight: 400; }
.episode-chevron { width: 16px; height: 16px; color: var(--detail-media-muted); transition: transform 160ms ease; }
.is-open .episode-chevron { transform: rotate(180deg); }
.episode-detail { min-width: 0; padding: 0 0 16px 128px; }
.episode-overview { max-width: 85ch; margin: 0; color: var(--md-sys-color-on-surface-variant); font-size: 13px; line-height: 1.9; white-space: pre-line; overflow-wrap: anywhere; }
.episode-media { margin-top: 14px; }
.episode-close { min-height: 44px; margin-top: 4px; padding: 0 8px; border: 0; border-radius: 8px; background: transparent; color: var(--md-sys-color-primary); font-size: 11px; cursor: pointer; }
.episode-close:focus-visible { outline: 2px solid var(--md-sys-color-primary); outline-offset: -2px; }
.dark .episode-status-owned { background: rgb(52 211 153 / 10%); color: #a7f3d0; }
.dark .episode-status-missing, .dark .episode-rating { color: #e2bf76; }
@media (hover: hover) {
  .episode-toggle:hover { background: color-mix(in srgb, var(--md-sys-color-on-surface) 3%, transparent); }
  .episode-close:hover { background: var(--detail-media-chip); }
}
@media (max-width: 639px) {
  .episode-toggle { grid-template-columns: 72px minmax(0, 1fr) 14px; gap: 10px; min-height: 88px; }
  .episode-title-line { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 2px 6px; }
  .episode-name { font-size: 13px; line-height: 20px; }
  .episode-status { grid-column: 1 / -1; justify-self: start; margin-top: 0; }
  .episode-meta { gap: 2px 8px; margin-top: 4px; }
  .episode-preview { display: none; }
  .episode-chevron { width: 14px; height: 14px; }
  .episode-detail { padding-left: 0; }
}
@media (max-width: 359px) {
  .episode-toggle { grid-template-columns: 60px minmax(0, 1fr) 14px; gap: 8px; }
  .episode-meta { column-gap: 6px; }
}
@media (prefers-reduced-motion: reduce) { .episode-chevron { transition: none; } }
</style>
