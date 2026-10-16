<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { mediaSize, resolution, videoRange, type EmbySource, type EmbyStream, type EmbyVersion } from '../types/embyVersion'

const props = defineProps<{ version: EmbyVersion; source?: EmbySource; number: number; expanded: boolean; panelId: string; compact?: boolean }>()
const emit = defineEmits<{ toggle: [] }>()
const video = computed(() => props.source?.MediaStreams?.find(stream => stream.Type === 'Video'))
const audios = computed(() => props.source?.MediaStreams?.filter(stream => stream.Type === 'Audio') || [])
const audioIndex = ref(0)
watch(audios, tracks => { audioIndex.value = Math.max(0, tracks.findIndex(track => track.IsDefault)) }, { immediate: true })
const audio = computed(() => audios.value[audioIndex.value])
const tags = computed(() => [resolution(video.value), video.value?.Codec?.toUpperCase(), audio.value?.Codec?.toUpperCase(), videoRange(video.value)].filter((v): v is string => !!v))
const pictureTags = computed(() => [resolution(video.value) || '清晰度未知', videoRange(video.value).toUpperCase()].filter(Boolean))
const codecTags = computed(() => [video.value?.Codec?.toUpperCase(), audio.value?.Codec?.toUpperCase()].filter((v): v is string => !!v))
const value = (v: unknown) => v === undefined || v === null || v === '' ? '—' : String(v)
const yesNo = (v?: boolean | null) => v == null ? '—' : v ? '是' : '否'
const bits = (v?: number | null) => v ? `${v} bit` : '—'
const bitrate = (v?: number | null) => !v ? '—' : v >= 1e6 ? `${Number((v / 1e6).toFixed(2))} Mbps` : `${Number((v / 1000).toFixed(1))} Kbps`
function videoRows(v: EmbyStream): [string, string][] {
  return [
    ['标题', value(v.DisplayTitle || v.Title)], ['编解码器', value(v.Codec?.toUpperCase())],
    ['杜比配置', value(v.ExtendedVideoSubTypeDescription)], ['编码配置', value(v.Profile)], ['等级', value(v.Level)],
    ['分辨率', v.Width && v.Height ? `${v.Width} × ${v.Height}` : '—'], ['宽高比', value(v.AspectRatio)],
    ['隔行', yesNo(v.IsInterlaced)], ['帧率', value(v.AverageFrameRate || v.RealFrameRate)], ['比特率', bitrate(v.BitRate)],
    ['视频范围', value(videoRange(v))], ['基色', value(v.ColorPrimaries)], ['色域', value(v.ColorSpace)],
    ['色彩转换', value(v.ColorTransfer)], ['位深度', bits(v.BitDepth)], ['像素格式', value(v.PixelFormat)], ['参考帧', value(v.RefFrames)],
  ]
}
function audioRows(v: EmbyStream): [string, string][] {
  return [
    ['标题', value(v.DisplayTitle)], ['内嵌标题', value(v.Title)], ['语言', value(v.DisplayLanguage || v.Language)],
    ['编解码器', value(v.Codec?.toUpperCase())], ['编码配置', value(v.Profile)], ['布局', value(v.ChannelLayout)],
    ['频道', v.Channels ? `${v.Channels} ch` : '—'], ['采样率', v.SampleRate ? `${v.SampleRate.toLocaleString()} Hz` : '—'],
    ['位深度', bits(v.BitDepth)], ['比特率', bitrate(v.BitRate)], ['默认', yesNo(v.IsDefault)],
  ]
}
// Movie details omit unknown fields while keeping meaningful false/zero values.
const visibleRows = (rows: [string, string][]) => props.compact ? rows : rows.filter(([, content]) => content !== '—' && content.trim() !== '')
const videoFields = computed(() => video.value ? visibleRows(videoRows(video.value)) : [])
const audioFields = computed(() => audio.value ? visibleRows(audioRows(audio.value)) : [])
</script>

<template>
  <section class="media-tonal-panel media-version-panel overflow-hidden" :class="[compact ? 'media-version-compact' : 'media-version-movie', { 'is-expanded': expanded }]">
    <button v-if="compact" :id="`${panelId}-button`" type="button" class="media-tonal-toggle version-summary" :aria-expanded="expanded" :aria-controls="panelId" @click="emit('toggle')">
      <span class="version-summary-label">版本 {{ number }}</span>
      <span class="version-summary-tags"><span v-for="tag in tags" :key="tag">{{ tag }}</span><span v-if="!tags.length">媒体参数暂未提供</span></span>
      <span class="version-summary-file">{{ source?.Container?.toLowerCase() || '格式未知' }}<span>{{ mediaSize(source?.Size) }}</span></span>
      <svg aria-hidden="true" class="version-summary-chevron" :class="{ 'rotate-180': expanded }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5"><path d="m5 8 5 5 5-5" /></svg>
    </button>
    <button v-else :id="`${panelId}-button`" type="button" class="media-tonal-toggle movie-version-toggle" :aria-expanded="expanded" :aria-controls="panelId" @click="emit('toggle')">
      <span class="movie-version-copy">
        <span class="movie-version-main"><span class="movie-version-number">版本 {{ number }}</span><span class="movie-version-picture"><span v-for="(tag, index) in pictureTags" :key="index">{{ tag }}</span></span></span>
        <span class="movie-version-codecs"><span v-for="(tag, index) in codecTags" :key="index">{{ tag }}</span><span v-if="!codecTags.length">编码信息待提供</span></span>
      </span>
      <span class="movie-version-file"><span>{{ source?.Container?.toUpperCase() || '格式未知' }}</span><span class="movie-version-size">{{ mediaSize(source?.Size) }}</span></span>
      <svg aria-hidden="true" class="movie-version-chevron" :class="{ 'rotate-180': expanded }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5"><path d="m5 8 5 5 5-5" /></svg>
    </button>
    <div v-if="expanded" :id="panelId" role="region" :aria-labelledby="`${panelId}-button`" :class="compact ? 'media-tonal-divider border-t border-slate-100 dark:border-slate-800 px-4 pb-5 pt-4 sm:px-5' : 'movie-version-details'">
      <div class="mb-4 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs leading-6"><span class="shrink-0 text-slate-400 dark:text-slate-500">Emby 条目</span><a :href="version.url" target="_blank" rel="noopener noreferrer" class="min-w-0 break-all text-blue-600 dark:text-amber-500 hover:underline">{{ version.name }}<svg aria-hidden="true" class="ml-1 inline h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg></a></div>
      <p v-if="!compact && source?.Name?.trim()" class="movie-version-source"><span>来源</span>{{ source.Name }}</p>
      <div class="grid gap-4 lg:grid-cols-2" :class="{ 'movie-version-parameters': !compact }">
        <section class="min-w-0" :class="compact ? 'media-tonal-inset rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4' : 'movie-parameter-group'" aria-label="视频参数">
          <h4 class="mb-3 flex items-center gap-2 text-xs font-semibold text-slate-800 dark:text-slate-200"><svg class="h-4 w-4 text-blue-500 dark:text-slate-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="6" width="12" height="12" rx="2"/><path d="m15 10 6-3v10l-6-3"/></svg>视频</h4>
          <dl v-if="videoFields.length" class="media-tonal-rows" :class="{ 'divide-y divide-slate-200 dark:divide-slate-700/70': compact }"><div v-for="[label, content] in videoFields" :key="label" class="flex items-start justify-between gap-3 text-xs leading-5" :class="compact ? 'py-2.5' : 'py-1.5'"><dt class="shrink-0 text-slate-400 dark:text-slate-500">{{ label }}</dt><dd class="min-w-0 break-words text-right font-medium text-slate-700 dark:text-slate-300 [overflow-wrap:anywhere]">{{ content }}</dd></div></dl><p v-else class="py-4 text-xs leading-6 text-slate-400 dark:text-slate-500">Emby 尚未提供视频流信息。</p>
        </section>
        <section class="min-w-0" :class="compact ? 'media-tonal-inset rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4' : 'movie-parameter-group'" aria-label="音频参数">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><h4 class="flex items-center gap-2 text-xs font-semibold text-slate-800 dark:text-slate-200"><svg class="h-4 w-4 text-blue-500 dark:text-slate-400" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m11 4-5 5H3v6h3l5 5V4ZM15 8a6 6 0 0 1 0 8M18 5a10 10 0 0 1 0 14"/></svg>音频</h4><select v-if="audios.length > 1" v-model.number="audioIndex" aria-label="选择音轨" class="media-tonal-chip max-w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-[11px] text-slate-600 dark:text-slate-400"><option v-for="(track, index) in audios" :key="index" :value="index">音轨 {{ index + 1 }} · {{ track.Language || track.Codec || '未知' }}{{ track.IsDefault ? ' · 默认' : '' }}</option></select></div>
          <dl v-if="audioFields.length" class="media-tonal-rows" :class="{ 'divide-y divide-slate-200 dark:divide-slate-700/70': compact }"><div v-for="[label, content] in audioFields" :key="label" class="flex items-start justify-between gap-3 text-xs leading-5" :class="compact ? 'py-2.5' : 'py-1.5'"><dt class="shrink-0 text-slate-400 dark:text-slate-500">{{ label }}</dt><dd class="min-w-0 break-words text-right font-medium text-slate-700 dark:text-slate-300 [overflow-wrap:anywhere]">{{ content }}</dd></div></dl><p v-else class="py-4 text-xs leading-6 text-slate-400 dark:text-slate-500">Emby 尚未提供音频流信息。</p>
        </section>
      </div>
      <button v-if="!compact" type="button" class="movie-version-close" :aria-controls="panelId" :aria-label="`收起版本 ${number} 的参数`" @click="emit('toggle')">收起参数</button>
    </div>
  </section>
</template>

<style scoped>
.media-version-panel { border: 0; box-shadow: none; }
.media-version-movie { border-radius: 12px; }
.movie-version-toggle { display: grid; grid-template-columns: minmax(0, 1fr) auto 16px; align-items: center; gap: 16px; width: 100%; min-height: 64px; padding: 12px 20px; border: 0; background: transparent; color: var(--md-sys-color-on-surface); text-align: left; cursor: pointer; transition: background-color 160ms ease; }
.movie-version-copy, .movie-version-main { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 16px; min-width: 0; }
.movie-version-main { gap: 6px 12px; }
.movie-version-number { color: var(--detail-media-muted); font-size: 11px; line-height: 20px; white-space: nowrap; }
.movie-version-picture { display: flex; flex-wrap: wrap; gap: 4px 8px; min-width: 0; overflow-wrap: anywhere; font-size: 14px; font-weight: 600; line-height: 22px; }
.movie-version-codecs { display: flex; flex-wrap: wrap; gap: 4px 10px; min-width: 0; overflow-wrap: anywhere; color: var(--detail-media-muted); font-size: 12px; line-height: 20px; }
.movie-version-file { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 2px 10px; min-width: 0; max-width: 160px; overflow-wrap: anywhere; color: var(--detail-media-muted); font-size: 12px; line-height: 20px; }
.movie-version-file > span { min-width: 0; }
.movie-version-size { flex-shrink: 0; color: var(--md-sys-color-on-surface-variant); font-weight: 500; font-variant-numeric: tabular-nums; white-space: nowrap; }
.movie-version-chevron { width: 16px; height: 16px; color: var(--detail-media-muted); transition: transform 160ms ease; }
.movie-version-toggle:focus-visible { outline: 2px solid var(--md-sys-color-primary); outline-offset: -2px; }
.movie-version-details { padding: 4px 20px 12px; }
.movie-version-source { display: flex; align-items: baseline; gap: 12px; margin: -8px 0 20px; overflow-wrap: anywhere; color: var(--md-sys-color-on-surface-variant); font-size: 12px; line-height: 20px; }
.movie-version-source > span { flex-shrink: 0; color: var(--detail-media-muted); }
.movie-version-parameters { gap: 24px 40px; }
.movie-parameter-group h4 { color: var(--md-sys-color-on-surface); }
.movie-parameter-group dd { color: var(--md-sys-color-on-surface-variant); }
.movie-version-close { min-height: 44px; margin-top: 12px; padding: 0 8px; border: 0; border-radius: 8px; background: transparent; color: var(--md-sys-color-primary); font-size: 12px; cursor: pointer; }
.movie-version-close:focus-visible { outline: 2px solid var(--md-sys-color-primary); outline-offset: -2px; }
@media (hover: hover) { .movie-version-close:hover { background: var(--detail-media-chip); } }
.media-version-compact { border: 0; border-radius: 8px; box-shadow: none; }
.version-summary { display: grid; grid-template-columns: auto minmax(0, 1fr) auto 16px; align-items: center; gap: 12px; width: 100%; min-height: 48px; padding: 10px 12px; border: 0; background: transparent; color: var(--md-sys-color-on-surface); text-align: left; cursor: pointer; }
.version-summary-label { font-size: 12px; font-weight: 600; white-space: nowrap; }
.version-summary-tags { display: flex; flex-wrap: wrap; gap: 3px 10px; min-width: 0; color: var(--detail-media-muted); font-size: 11px; line-height: 18px; }
.version-summary-file { display: flex; gap: 8px; color: var(--detail-media-muted); font-size: 11px; line-height: 18px; white-space: nowrap; }
.version-summary-chevron { width: 16px; height: 16px; color: var(--detail-media-muted); transition: transform 160ms ease; }
.version-summary:focus-visible { outline: 2px solid var(--md-sys-color-primary); outline-offset: -2px; }
@media (max-width: 639px) {
  .movie-version-toggle { grid-template-columns: minmax(0, 1fr) auto 16px; gap: 4px 10px; min-height: 76px; padding: 12px 16px; }
  .movie-version-copy { display: contents; }
  .movie-version-main { grid-column: 1 / 3; grid-row: 1; gap: 4px 10px; }
  .movie-version-codecs { grid-column: 1; grid-row: 2; gap: 4px 6px; font-size: 11px; }
  .movie-version-file { grid-column: 2; grid-row: 2; max-width: 110px; gap: 2px 6px; font-size: 11px; }
  .movie-version-chevron { grid-column: 3; grid-row: 1 / 3; }
  .movie-version-details { padding-right: 16px; padding-left: 16px; }
  .version-summary { grid-template-columns: auto minmax(0, 1fr) 16px; gap: 2px 10px; }
  .version-summary-tags { grid-column: 2; grid-row: 1 / 3; }
  .version-summary-file { grid-column: 1; grid-row: 2; gap: 6px; font-size: 10px; }
  .version-summary-chevron { grid-column: 3; grid-row: 1 / 3; }
}
@media (prefers-reduced-motion: reduce) { .version-summary-chevron, .movie-version-chevron, .movie-version-toggle { transition: none; } }
</style>
