<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { IconPhotoOff } from '@tabler/icons-vue'

const props = defineProps<{
  to: RouteLocationRaw
  label: string
  title: string
  subtitle?: string
  coverUrl: string
  sticker?: string
  format?: string
  priority?: boolean
}>()
const failed = ref(false)
watch(() => props.coverUrl, () => { failed.value = false })
</script>

<template>
  <RouterLink :to="to" class="anime-poster-card" :aria-label="label">
    <span class="poster-card-art">
      <span class="poster-card-cover">
        <span class="poster-card-placeholder" aria-hidden="true"><IconPhotoOff :size="24" /><span>暂无封面</span></span>
        <img v-if="coverUrl && !failed" :src="coverUrl" alt="" width="400" height="600" :loading="priority ? 'eager' : 'lazy'" :fetchpriority="priority ? 'high' : 'auto'" decoding="async" @error="failed = true" />
      </span>
      <span v-if="sticker" class="poster-card-sticker" :title="sticker">{{ sticker }}</span>
      <span v-if="format || $slots.badge" class="poster-card-cover-footer"><span v-if="format" class="poster-card-format" :title="format">{{ format }}</span><span class="poster-card-badge"><slot name="badge" /></span></span>
    </span>
    <span class="poster-card-body">
      <strong class="poster-card-title" :title="title">{{ title }}</strong>
      <span class="poster-card-subtitle" :title="subtitle || undefined" :aria-hidden="!subtitle || undefined">{{ subtitle }}</span>
      <span class="poster-card-foot"><span class="poster-card-meta"><slot name="meta" /></span><span v-if="$slots.status" class="poster-card-status"><slot name="status" /></span></span>
    </span>
  </RouterLink>
</template>

<style scoped>
/* Vue presentation component inspired by AniBT's documented poster-card design.
   Provider data and verified image URLs stay in the three catalog adapters. */
.anime-poster-card { position: relative; display: flex; height: 100%; min-width: 0; flex-direction: column; padding: 4px; border-radius: 14px; background: #fff; text-align: left; text-decoration: none; box-shadow: 0 0 0 1px rgb(148 163 184 / .2), 0 2px 2px rgb(15 23 42 / .02), 0 7px 18px -12px rgb(15 23 42 / .2); transition: transform 300ms cubic-bezier(.34, 1.56, .64, 1), box-shadow 200ms ease; }
.poster-card-art { display: block; position: relative; flex: none; container: poster-art / inline-size; }
.poster-card-cover { display: block; position: relative; overflow: hidden; aspect-ratio: 2 / 3; border-radius: 10px; background: rgb(var(--color-slate-100)); }
.poster-card-cover img { display: block; position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
.poster-card-cover::before, .poster-card-cover::after { content: ''; position: absolute; inset: 0; pointer-events: none; }
.poster-card-cover::before { z-index: 1; border-radius: inherit; background: linear-gradient(135deg, rgb(255 255 255 / .2), transparent 38%); box-shadow: inset 0 0 0 1px rgb(255 255 255 / .2); }
.poster-card-cover::after { z-index: 2; background: linear-gradient(115deg, transparent 35%, rgb(255 255 255 / .45) 50%, transparent 65%); transform: translateX(-120%); }
.poster-card-placeholder { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 8px; color: rgb(var(--color-slate-400)); font-size: 11px; background-image: radial-gradient(rgb(148 163 184 / .15) 1px, transparent 1px); background-size: 16px 16px; }
.poster-card-body { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 2px; padding: 8px 4px 3px; }
.poster-card-title { display: -webkit-box; height: 36px; overflow: hidden; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow-wrap: anywhere; color: rgb(var(--color-slate-800)); font-size: 14px; font-weight: 650; line-height: 18px; transition: color 150ms ease; }
.poster-card-subtitle { display: block; height: 18px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; color: rgb(var(--color-slate-500)); font-size: 11px; line-height: 18px; }
.poster-card-foot { display: flex; min-width: 0; flex-direction: column; gap: 2px; margin-top: auto; padding-top: 4px; }
.poster-card-meta, .poster-card-status { display: flex; align-items: center; gap: 6px; height: 18px; min-width: 0; overflow: hidden; color: rgb(var(--color-slate-600)); font-size: 11px; line-height: 18px; white-space: nowrap; font-variant-numeric: tabular-nums; }
.poster-card-meta :deep(.poster-meta-label) { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.poster-card-meta :deep(.poster-rating) { display: inline-flex; flex: none; align-items: center; gap: 3px; }
.poster-card-meta :deep(.poster-rating svg) { color: #b37b34; }
.poster-card-status :deep(.poster-resource-status) { display: inline-flex; min-width: 0; align-items: center; gap: 5px; color: rgb(var(--color-slate-500)); }
.poster-card-status :deep(.poster-resource-status)::before { content: ''; width: 5px; height: 5px; flex: none; border: 1px solid currentColor; border-radius: 50%; }
.poster-card-status :deep(.poster-resource-status.has-resources)::before { background: currentColor; }
.poster-card-sticker { --poster-sticker-angle: var(--poster-tilt, -3deg); position: absolute; top: 7px; left: 6px; z-index: 3; max-width: calc(100% - 12px); overflow: hidden; padding: 3px 6px; border: 1px solid rgb(148 163 184 / .18); border-radius: 6px; background: #fff; color: rgb(var(--color-slate-700)); box-shadow: 0 2px 4px rgb(15 23 42 / .15); font-size: 11px; font-weight: 600; line-height: 16px; white-space: nowrap; text-overflow: ellipsis; font-variant-numeric: tabular-nums; transform: rotate(var(--poster-sticker-angle)); transform-origin: top left; transition: transform 300ms cubic-bezier(.34, 1.56, .64, 1); }
.poster-card-cover-footer { position: absolute; bottom: 6px; left: 6px; right: 6px; z-index: 3; display: flex; align-items: center; gap: 4px; pointer-events: none; }
.poster-card-format { display: block; flex: none; max-width: 34%; overflow: hidden; padding: 3px 5px; border-radius: 5px; background: rgb(15 23 42 / .75); color: #fff; font-size: 10px; line-height: 14px; text-overflow: ellipsis; white-space: nowrap; }
.poster-card-badge { min-width: 0; margin-left: auto; }
.poster-card-badge :deep(.catalog-subscription-badge) { position: static; max-width: 100%; height: 22px; padding: 0 5px; background: rgb(15 23 42 / .86); overflow: hidden; text-overflow: ellipsis; }
.anime-poster-card:focus-visible { z-index: 2; outline: 2px solid #60a5fa; outline-offset: 3px; }
.dark .anime-poster-card { background: rgb(var(--color-slate-800)); box-shadow: 0 0 0 1px rgb(148 163 184 / .17), 0 5px 16px -10px rgb(0 0 0 / .4); }
.dark .poster-card-cover { background: rgb(var(--color-slate-900)); }
.dark .poster-card-title { color: rgb(var(--color-slate-100)); }
.dark .poster-card-subtitle, .dark .poster-card-meta, .dark .poster-card-status, .dark .poster-card-status :deep(.poster-resource-status) { color: rgb(var(--color-slate-400)); }
.dark .poster-card-meta :deep(.poster-rating svg) { color: #d7af77; }
.dark .poster-card-sticker { border-color: rgb(148 163 184 / .35); background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-100)); }
@container poster-art (max-width: 152px) { .poster-card-sticker { font-size: 10px; padding: 2px 5px; } }
@media (hover: hover) and (pointer: fine) { .anime-poster-card:hover { z-index: 2; box-shadow: 0 0 0 1px rgb(96 165 250 / .35), 0 3px 6px rgb(15 23 42 / .05), 0 16px 28px -12px rgb(15 23 42 / .23); } .anime-poster-card:hover .poster-card-title { color: #2563eb; } .dark .anime-poster-card:hover { box-shadow: 0 0 0 1px rgb(96 165 250 / .4), 0 12px 24px -10px rgb(0 0 0 / .45); } .dark .anime-poster-card:hover .poster-card-title { color: #93c5fd; } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) { .anime-poster-card:hover { transform: translateY(-4px) rotate(-.6deg); } .anime-poster-card:hover .poster-card-cover::after { transform: translateX(120%); transition: transform 700ms cubic-bezier(.2, .7, .2, 1); } .anime-poster-card:hover .poster-card-sticker { --poster-sticker-angle: calc(var(--poster-tilt, -3deg) * -1); } }
@media (prefers-reduced-motion: reduce) { .anime-poster-card, .poster-card-title, .poster-card-sticker { transition: none; } }
</style>
