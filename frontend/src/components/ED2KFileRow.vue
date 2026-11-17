<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { IconAlertCircle, IconCheck, IconChevronDown, IconCopy, IconDownload, IconExternalLink, IconFile, IconLoader2 } from '@tabler/icons-vue'
import type { TelegramFile } from '../utils/telegramFiles'
import type { TelegramFileActionStatus } from '../composables/useTelegramFileActions'

const props = defineProps<{ file: TelegramFile; selected: boolean; disabled?: boolean; cloud?: TelegramFileActionStatus; strm?: TelegramFileActionStatus }>()
defineEmits<{ select: []; copy: []; download: []; generate: [] }>()
const expanded = ref(false), detailsID = useId()
const specs = computed(() => {
  const metadata = props.file.metadata
  if (metadata.source || metadata.videoCodec || metadata.audio) return [metadata.resolution, metadata.dynamicRange, metadata.source, metadata.videoCodec, metadata.bitDepth, metadata.audio].filter(Boolean)
  return metadata.specs || [metadata.resolution, metadata.dynamicRange].filter(Boolean)
})
const extra = computed(() => [...new Set([props.file.metadata.platform, props.file.metadata.releaseGroup, props.file.metadata.frameRate, props.file.metadata.subtitles].filter(Boolean))].join(' · '))
const cloudLabel = computed(() => props.cloud?.busy ? '提交中' : props.cloud?.success ? '已提交' : props.cloud?.uncertain ? '待核对' : '云下载')
const strmLabel = computed(() => props.strm?.busy ? '提交中' : props.strm?.success ? '已接收' : props.strm?.uncertain ? '待核对' : 'STRM')
const issue = computed(() => [props.cloud, props.strm].filter(status => status?.message && !status.success && !status.busy).map(status => status!.message).join('；'))
function date(value: string) { const result = new Date(value); return Number.isFinite(result.valueOf()) ? result.toLocaleDateString('zh-CN') : '' }
function size() {
  if (props.file.metadata.size) return props.file.metadata.size
  let value = Number(props.file.sizeBytes), index = 0
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++ }
  return `${Number(value.toFixed(2))} ${units[index]}`
}
</script>

<template>
  <li class="ed-file" :class="{ 'is-selected': selected }">
    <label class="ed-check"><input type="checkbox" :checked="selected" :disabled="disabled" :aria-label="`选择 ${file.metadata.episode || file.name}`" @change="$emit('select')" /></label>
    <button class="ed-information" type="button" :aria-expanded="expanded" :aria-controls="detailsID" :title="file.name" @click="expanded = !expanded">
      <span class="ed-headline"><strong :class="{ 'ed-episode': file.metadata.episode }">{{ file.metadata.episode || file.name }}</strong><span class="ed-specs">{{ specs.join(' · ') }}</span><IconChevronDown :size="13" class="ed-chevron" :class="{ expanded }" aria-hidden="true" /></span>
      <span v-if="extra || file.metadata.episode" class="ed-secondary">{{ extra || file.version }}</span>
    </button>
    <span class="ed-size" :title="`${file.sizeBytes} 字节`">{{ size() }}</span>
    <div class="ed-actions">
      <button type="button" class="ed-icon" :aria-label="`复制 ${file.metadata.episode || file.name} ED2K 链接`" title="复制 ED2K 链接" @click="$emit('copy')"><IconCopy :size="15" aria-hidden="true" /></button>
      <button type="button" :disabled="disabled || cloud?.busy || cloud?.success || cloud?.uncertain" :aria-busy="cloud?.busy" :aria-label="cloudLabel === '云下载' ? '115 云下载' : cloudLabel" :title="cloud?.message || '115 云下载'" @click="$emit('download')"><component :is="cloud?.busy ? IconLoader2 : cloud?.success ? IconCheck : cloud?.uncertain ? IconAlertCircle : IconDownload" :size="15" aria-hidden="true" /><span>{{ cloudLabel }}</span></button>
      <button type="button" class="ed-primary" :disabled="disabled || strm?.busy || strm?.success || strm?.uncertain" :aria-busy="strm?.busy" :aria-label="strmLabel === 'STRM' ? 'ED2K STRM' : strmLabel" :title="strm?.message || '生成 ED2K STRM'" @click="$emit('generate')"><component :is="strm?.busy ? IconLoader2 : strm?.success ? IconCheck : strm?.uncertain ? IconAlertCircle : IconFile" :size="15" aria-hidden="true" /><span>{{ strmLabel }}</span></button>
    </div>
    <p v-if="issue" class="ed-issue" role="status">{{ issue }}</p>
    <div v-if="expanded" :id="detailsID" class="ed-details">
      <p class="ed-filename">{{ file.name }}</p>
      <p>{{ file.sizeBytes }} 字节<span class="ed-hash">ED2K {{ file.hash }}</span></p>
      <p>版本：{{ file.version }}</p>
      <div class="ed-sources"><span>来源（{{ file.sources.length }}）</span><a v-for="source in file.sources" :key="`${source.id}/${source.url}`" :href="source.url" target="_blank" rel="noopener noreferrer">{{ source.channelTitle }}<time :datetime="source.postedAt">{{ date(source.postedAt) }}</time><IconExternalLink :size="12" aria-hidden="true" /></a></div>
    </div>
  </li>
</template>

<style scoped>
.ed-file { display: grid; grid-template-columns: 24px minmax(0, 1fr) 80px auto; align-items: center; column-gap: 10px; min-width: 0; min-height: 60px; padding: 8px 12px; border-bottom: 1px solid var(--search-dialog-card-border); }
.ed-file:last-child { border-bottom: 0; }.ed-file.is-selected { background: var(--search-dialog-tonal); }
.ed-check { display: grid; place-items: center; min-height: 36px; cursor: pointer; }.ed-check input { width: 15px; height: 15px; accent-color: var(--search-dialog-primary); }
.ed-information { min-width: 0; text-align: left; border-radius: 5px; }.ed-headline { display: flex; align-items: center; gap: 9px; min-width: 0; }.ed-headline strong { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 600; }.ed-headline strong.ed-episode { flex-shrink: 0; font-variant-numeric: tabular-nums; color: var(--search-dialog-on-tonal); }
.ed-specs { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }.ed-chevron { flex: none; margin-left: auto; color: var(--search-dialog-muted); }.ed-chevron.expanded { transform: rotate(180deg); }.ed-secondary { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 3px; color: var(--search-dialog-muted); font-size: 11px; line-height: 16px; }
.ed-size { text-align: right; font-variant-numeric: tabular-nums; font-size: 12px; color: var(--search-dialog-muted); white-space: nowrap; }.ed-actions { display: flex; gap: 5px; align-items: center; }.ed-actions button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 32px; padding: 5px 8px; border-radius: 7px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 11px; white-space: nowrap; }.ed-actions button.ed-primary { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }.ed-actions button.ed-icon { padding: 5px; background: transparent; }.ed-actions button:disabled { opacity: .55; cursor: default; }
.ed-issue, .ed-details { grid-column: 2 / -1; min-width: 0; overflow-wrap: anywhere; font-size: 12px; line-height: 1.7; }.ed-issue { padding-top: 5px; color: #b45309; }.dark .ed-issue { color: #fcd34d; }.ed-details { margin-top: 8px; padding: 10px 0 4px; border-top: 1px solid var(--search-dialog-card-border); color: var(--search-dialog-muted); }.ed-filename { color: var(--search-dialog-text); }.ed-hash { margin-left: 16px; }.ed-sources { display: flex; flex-wrap: wrap; gap: 4px 14px; }.ed-sources a { display: inline-flex; align-items: center; gap: 6px; color: var(--search-dialog-primary); }.ed-sources time { color: var(--search-dialog-muted); }
button:focus-visible, input:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (hover: hover) { .ed-actions button:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }.ed-information:hover .ed-headline strong { text-decoration: underline; } }
@media (max-width: 760px) { .ed-file { grid-template-columns: 24px minmax(0, 1fr) auto; gap: 4px 8px; padding: 8px; }.ed-check { grid-row: 1; min-height: 44px; }.ed-information { grid-column: 2 / -1; }.ed-size { grid-column: 1 / 3; text-align: left; }.ed-actions { grid-column: 3; }.ed-actions button { min-height: 44px; min-width: 44px; }.ed-actions button span { display: none; }.ed-specs { font-size: 11px; }.ed-secondary { max-width: 100%; }.ed-hash { display: block; margin-left: 0; }.ed-details, .ed-issue { grid-column: 1 / -1; } }
</style>
