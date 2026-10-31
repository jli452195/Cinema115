<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { RecentUpdateItem } from '../types/updates'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ item: RecentUpdateItem }>()
const imageFailed = ref(false)
const path = computed(() => `/${props.item.kind}/${props.item.tmdbId}`)
const rating = computed(() => props.item.votes > 0 && Number.isFinite(props.item.rating) ? props.item.rating.toFixed(1) : '')
const rangeLabel = computed(() => {
  if (props.item.kind === 'movie') return ''
  const label = props.item.episodeLabel?.trim()
  if (label) return props.item.season === 0 ? label.replace(/^S00\b/, '特别篇') : label
  return props.item.episodeCount && props.item.episodeCount > 0 ? `更新 ${props.item.episodeCount} 集` : '剧集更新'
})
const rangeRow = ref<HTMLElement>()
const rangeText = ref<HTMLElement>()
const rangeOverflow = ref(false)
const expanded = ref(false)
const expandable = computed(() => rangeOverflow.value || expanded.value)
let resizeObserver: ResizeObserver | undefined
function measureRange() {
  if (!expanded.value && rangeText.value && rangeRow.value) {
    const styles = getComputedStyle(rangeText.value.parentElement!)
    const availableWidth = rangeRow.value.clientWidth - parseFloat(styles.paddingLeft) - parseFloat(styles.paddingRight)
    rangeOverflow.value = rangeText.value.scrollWidth > availableWidth + 1
  }
}
function toggleRange() { if (expandable.value) expanded.value = !expanded.value }
watch(() => [props.item.kind, props.item.tmdbId, props.item.posterUrl], () => { imageFailed.value = false })
watch(rangeLabel, () => { expanded.value = false; rangeOverflow.value = false; void nextTick(measureRange) })
watch([rangeText, expanded], () => { void nextTick(measureRange) }, { flush: 'post' })
watch(rangeRow, row => {
  resizeObserver?.disconnect()
  resizeObserver = row ? new ResizeObserver(measureRange) : undefined
  if (row) resizeObserver?.observe(row)
}, { flush: 'post' })
onBeforeUnmount(() => resizeObserver?.disconnect())
</script>

<template>
  <article class="update-card">
    <RouterLink :to="path" class="group update-card-link" :aria-label="`查看${item.title}`">
    <div class="update-poster relative aspect-[2/3] overflow-hidden rounded-xl bg-slate-200 dark:bg-slate-700 shadow-[0_12px_24px_-14px_rgba(15,23,42,0.4)]">
      <img v-if="item.posterUrl && !imageFailed" :src="tmdbImageURL(item.posterUrl)" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="imageFailed = true" />
      <div v-else class="flex h-full flex-col items-center justify-center gap-3 p-5 text-center text-slate-400 dark:text-slate-500">
        <svg class="h-9 w-9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="3"/><path d="m3 16 5-5 4 4 3-3 6 6M15 8h.01"/></svg>
        <span class="text-xs">暂无海报</span>
      </div>
      <span class="update-type update-badge">{{ item.kind === 'tv' ? '剧集' : '电影' }}</span>
      <span v-if="rating" class="update-rating update-badge" :aria-label="`TMDB 评分 ${rating}`"><svg aria-hidden="true" viewBox="0 0 24 24" fill="currentColor"><path d="m12 3 2.8 5.7 6.3.9-4.6 4.5 1.1 6.3-5.6-3-5.6 3 1.1-6.3L2.9 9.6l6.3-.9Z" /></svg>{{ rating }}</span>
    </div>
    <h3 class="update-title" :title="item.title">{{ item.title }}</h3>
    </RouterLink>
    <div v-if="item.kind === 'tv'" ref="rangeRow" class="update-range-row">
      <component :is="expandable ? 'button' : 'p'" class="update-range update-badge" :class="{ 'is-expanded': expanded, 'is-tv': item.kind === 'tv' }" :type="expandable ? 'button' : undefined" :aria-expanded="expandable ? expanded : undefined" :aria-label="expandable ? `${expanded ? '收起' : '展开'}${item.title}的更新集数：${rangeLabel}` : undefined" :title="rangeLabel" @click="toggleRange">
        <span ref="rangeText">{{ rangeLabel }}</span><svg v-if="expandable" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m7 10 5 5 5-5" /></svg>
      </component>
    </div>
    <p class="update-meta">{{ item.releaseDate?.slice(0, 4) || (item.metadataMissing ? '资料待补充' : '年份未知') }}</p>
  </article>
</template>

<style scoped>
.update-card { position: relative; min-width: 0; --badge-surface: #fff; --badge-text: rgb(var(--color-slate-600)); --badge-accent: rgb(var(--color-accent-text)); }
.dark .update-card { --badge-surface: rgb(var(--color-slate-900) / 95%); --badge-text: rgb(var(--color-slate-400)); }
.update-card-link { display: block; min-width: 0; border-radius: 12px; }
.update-title { min-height: 40px; margin: 10px 0 0; padding: 0 2px; display: -webkit-box; overflow: hidden; overflow-wrap: anywhere; -webkit-box-orient: vertical; -webkit-line-clamp: 2; line-clamp: 2; color: rgb(var(--color-slate-800)); font-size: 14px; font-weight: 600; line-height: 20px; }
.dark .update-title { color: rgb(var(--color-slate-200)); }
.update-poster { transition: transform 150ms cubic-bezier(0.4, 0, 0.2, 1), box-shadow 150ms cubic-bezier(0.4, 0, 0.2, 1); }
.update-badge { border-radius: 7px; background: var(--badge-surface); color: var(--badge-text); box-shadow: 0 2px 6px rgb(0 0 0 / 12%); }
.update-type { position: absolute; top: 8px; right: 8px; padding: 4px 8px; font-size: 10px; font-weight: 500; line-height: 16px; }
.update-range-row { position: absolute; z-index: 1; top: 8px; left: 8px; right: 56px; pointer-events: none; transition: transform 150ms cubic-bezier(0.4, 0, 0.2, 1); }
.update-range { display: flex; align-items: center; gap: 4px; width: fit-content; max-width: 100%; min-height: 24px; margin: 0; padding: 4px 8px; border: 0; color: var(--badge-accent); font-size: 10px; font-weight: 600; line-height: 16px; text-align: left; }
.update-range.is-tv { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
.update-range > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
button.update-range { position: relative; pointer-events: auto; cursor: pointer; }
button.update-range::before { content: ''; position: absolute; inset: -8px 0 -12px; }
button.update-range:hover { background: color-mix(in srgb, var(--badge-accent) 5%, var(--badge-surface)); }
.update-range.is-expanded { align-items: flex-start; }
.update-range.is-expanded > span { white-space: normal; overflow-wrap: anywhere; }
.update-range svg { flex-shrink: 0; width: 14px; height: 16px; transition: transform 160ms ease; }
.update-range.is-expanded svg { transform: rotate(180deg); }
.update-meta { margin: 4px 0 0; padding: 0 2px; color: rgb(var(--color-slate-400)); font-size: 12px; line-height: 18px; }
.dark .update-meta { color: rgb(var(--color-text-muted)); }
.update-rating { position: absolute; right: 8px; bottom: 8px; display: inline-flex; align-items: center; gap: 3px; padding: 4px 8px; color: var(--badge-accent); font-size: 10px; font-weight: 600; line-height: 16px; font-variant-numeric: tabular-nums; }
.update-rating svg { width: 11px; height: 11px; }
@media (hover: hover) and (pointer: fine) {
  .update-card:hover .update-poster, .update-card:hover .update-range-row { transform: translateY(-4px); }
  .update-card:hover .update-poster { box-shadow: 0 10px 15px -3px rgb(0 0 0 / 10%), 0 4px 6px -4px rgb(0 0 0 / 10%); }
  .update-card:hover .update-title { color: var(--badge-accent); }
}
.update-card-link:focus-visible .update-title { color: var(--badge-accent); }
@media (prefers-reduced-motion: reduce) {
  .update-range svg, .update-poster, .update-range-row { transition: none; }
  .update-card:hover .update-poster, .update-card:hover .update-range-row { transform: none; }
}
</style>

