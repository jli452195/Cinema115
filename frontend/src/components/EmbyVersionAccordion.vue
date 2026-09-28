<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { mediaSize, resolution, videoRange, type EmbySource, type EmbyStream, type EmbyVersion } from '../types/embyVersion'

const props = defineProps<{ version: EmbyVersion; source?: EmbySource; number: number; expanded: boolean; panelId: string }>()
const emit = defineEmits<{ toggle: [] }>()
const video = computed(() => props.source?.MediaStreams?.find(stream => stream.Type === 'Video'))
const audios = computed(() => props.source?.MediaStreams?.filter(stream => stream.Type === 'Audio') || [])
const audioIndex = ref(0)
watch(audios, tracks => { audioIndex.value = Math.max(0, tracks.findIndex(track => track.IsDefault)) }, { immediate: true })
const audio = computed(() => audios.value[audioIndex.value])
const tags = computed(() => [resolution(video.value), video.value?.Codec?.toUpperCase(), audio.value?.Codec?.toUpperCase(), videoRange(video.value)].filter((v): v is string => !!v))
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
</script>

<template>
  <section class="overflow-hidden rounded-xl border transition" :class="expanded ? 'border-blue-200 bg-white dark:bg-slate-900 shadow-sm' : 'border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950/80'">
    <button :id="`${panelId}-button`" class="flex w-full items-center justify-between gap-4 p-4 text-left transition hover:bg-blue-50/50 sm:px-5" :aria-expanded="expanded" :aria-controls="panelId" @click="emit('toggle')">
      <span class="min-w-0"><span class="block text-sm font-semibold text-slate-800 dark:text-slate-200">版本 {{ number }}</span><span class="mt-2.5 flex flex-wrap gap-1.5"><span v-for="tag in tags" :key="tag" class="rounded-full bg-blue-50 px-2.5 py-1 text-[11px] font-medium text-blue-600">{{ tag }}</span><span v-if="!tags.length" class="text-xs text-slate-400 dark:text-slate-500">媒体参数暂未提供</span></span></span>
      <span class="flex shrink-0 flex-col items-end gap-3"><span class="text-[11px] text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ source?.Container?.toLowerCase() || '格式未知' }}<span class="ml-2">{{ mediaSize(source?.Size) }}</span></span><svg class="h-4 w-4 text-blue-500 transition-transform" :class="{ 'rotate-180': expanded }" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m5 8 5 5 5-5" /></svg></span>
    </button>
    <div v-if="expanded" :id="panelId" role="region" :aria-labelledby="`${panelId}-button`" class="border-t border-slate-100 dark:border-slate-800 px-4 pb-5 pt-4 sm:px-5">
      <div class="mb-4 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs leading-6"><span class="shrink-0 text-slate-400 dark:text-slate-500">Emby 条目</span><a :href="version.url" target="_blank" rel="noopener noreferrer" class="min-w-0 break-all text-blue-600 hover:underline">{{ version.name }} ↗</a></div>
      <div class="grid gap-4 lg:grid-cols-2">
        <section class="min-w-0 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950/70 p-4" aria-label="视频参数">
          <h4 class="mb-3 flex items-center gap-2 text-xs font-semibold text-slate-800 dark:text-slate-200"><svg class="h-4 w-4 text-blue-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="6" width="12" height="12" rx="2"/><path d="m15 10 6-3v10l-6-3"/></svg>视频</h4>
          <dl v-if="video" class="divide-y divide-slate-200 dark:divide-slate-700/70"><div v-for="[label, content] in videoRows(video)" :key="label" class="flex items-start justify-between gap-3 py-2.5 text-xs leading-5"><dt class="shrink-0 text-slate-400 dark:text-slate-500">{{ label }}</dt><dd class="min-w-0 break-words text-right font-medium text-slate-700 dark:text-slate-300 [overflow-wrap:anywhere]">{{ content }}</dd></div></dl><p v-else class="py-4 text-xs leading-6 text-slate-400 dark:text-slate-500">Emby 尚未提供视频流信息。</p>
        </section>
        <section class="min-w-0 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950/70 p-4" aria-label="音频参数">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><h4 class="flex items-center gap-2 text-xs font-semibold text-slate-800 dark:text-slate-200"><svg class="h-4 w-4 text-blue-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m11 4-5 5H3v6h3l5 5V4ZM15 8a6 6 0 0 1 0 8M18 5a10 10 0 0 1 0 14"/></svg>音频</h4><select v-if="audios.length > 1" v-model.number="audioIndex" aria-label="选择音轨" class="max-w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-2 py-1 text-[11px] text-slate-600 dark:text-slate-400 dark:text-slate-500"><option v-for="(track, index) in audios" :key="index" :value="index">音轨 {{ index + 1 }} · {{ track.Language || track.Codec || '未知' }}{{ track.IsDefault ? ' · 默认' : '' }}</option></select></div>
          <dl v-if="audio" class="divide-y divide-slate-200 dark:divide-slate-700/70"><div v-for="[label, content] in audioRows(audio)" :key="label" class="flex items-start justify-between gap-3 py-2.5 text-xs leading-5"><dt class="shrink-0 text-slate-400 dark:text-slate-500">{{ label }}</dt><dd class="min-w-0 break-words text-right font-medium text-slate-700 dark:text-slate-300 [overflow-wrap:anywhere]">{{ content }}</dd></div></dl><p v-else class="py-4 text-xs leading-6 text-slate-400 dark:text-slate-500">Emby 尚未提供音频流信息。</p>
        </section>
      </div>
    </div>
  </section>
</template>
