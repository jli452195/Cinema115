<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { IconAlertCircle, IconCheck, IconChevronDown, IconCopy, IconDownload, IconExternalLink, IconFile, IconLoader2 } from '@tabler/icons-vue'
import type { TelegramFile } from '../utils/telegramFiles'
import type { TelegramFileActionStatus } from '../composables/useTelegramFileActions'

const props = defineProps<{ file: TelegramFile; selected: boolean; disabled?: boolean; cloudDisabled?: boolean; cloud?: TelegramFileActionStatus; strm?: TelegramFileActionStatus }>()
const emit = defineEmits<{ select: [shiftKey: boolean]; copy: []; download: []; generate: [] }>()
function onRowClick(event: MouseEvent) {
  if (props.disabled) return
  // 详情按钮、操作按钮与展开内容只执行自身交互，不连带切换整行勾选。
  if ((event.target as Element).closest('button, input, label, a, .ed-details, .ed-issue')) return
  emit('select', event.shiftKey)
}
function onCheckboxClick(event: MouseEvent) {
  if (props.disabled) return
  emit('select', event.shiftKey)
}
const expanded = ref(false), detailsID = useId()
const specs = computed(() => {
  const metadata = props.file.metadata
  if (metadata.source || metadata.videoCodec || metadata.audio) return [metadata.resolution, metadata.dynamicRange, metadata.source, metadata.videoCodec, metadata.bitDepth, metadata.audio].filter(Boolean)
  return metadata.specs || [metadata.resolution, metadata.dynamicRange].filter(Boolean)
})
const extra = computed(() => [...new Set([props.file.metadata.platform, props.file.metadata.releaseGroup, props.file.metadata.frameRate, props.file.metadata.subtitles].filter(Boolean))].join(' · '))
const secondaryText = computed(() => extra.value || (props.file.metadata.episode ? props.file.version : ''))
const cloudLabel = computed(() => props.cloud?.busy ? '提交中' : props.cloud?.success ? '已提交' : props.cloud?.uncertain ? '待核对' : '云下载')
const strmLabel = computed(() => props.strm?.busy ? '提交中' : props.strm?.success ? '已接收' : props.strm?.uncertain ? '待核对' : 'STRM')
const issue = computed(() => [props.cloud, props.strm].filter(status => status?.message && !status.success && !status.busy).map(status => status!.message).join('；'))
function date(value: string) { const result = new Date(value); return Number.isFinite(result.valueOf()) ? result.toLocaleDateString('zh-CN') : '' }
const sizeText = computed(() => {
  if (props.file.metadata.size) return props.file.metadata.size
  let value = Number(props.file.sizeBytes), index = 0
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++ }
  return `${Number(value.toFixed(2))} ${units[index]}`
})
</script>

<template>
  <li class="ed-file" :class="{ 'is-selected': selected, 'is-disabled': disabled }" @click="onRowClick" @mousedown="event => { if (event.shiftKey && !(event.target as HTMLElement).closest('input')) event.preventDefault() }">
    <label class="ed-check" @click.stop><input type="checkbox" :checked="selected" :disabled="disabled" :aria-label="`选择 ${file.metadata.episode || file.name}`" @click.stop="onCheckboxClick" /></label>
    <div class="ed-information" :title="file.name">
      <span class="ed-headline"><strong :class="{ 'ed-episode': file.metadata.episode }">{{ file.metadata.episode || file.name }}</strong><span v-if="specs.length" class="ed-specs">{{ specs.join(' · ') }}</span></span>
      <span v-if="secondaryText || sizeText" class="ed-secondary" :class="{ 'is-size-only': !secondaryText }"><span v-if="secondaryText" class="ed-secondary-text">{{ secondaryText }}</span><span class="ed-inline-size" :title="`${file.sizeBytes} 字节`">{{ sizeText }}</span></span>
    </div>
    <button type="button" class="ed-expand-toggle" :aria-expanded="expanded" :aria-controls="detailsID" :aria-label="`${expanded ? '收起' : '展开'} ${file.metadata.episode || file.name} 完整文件名与来源`" :title="expanded ? '收起详情' : '查看完整文件名与来源'" @click.stop="expanded = !expanded"><IconChevronDown :size="15" class="ed-chevron" :class="{ expanded }" aria-hidden="true" /></button>
    <span class="ed-size" :title="`${file.sizeBytes} 字节`">{{ sizeText }}</span>
    <div class="ed-actions" @click.stop>
      <button type="button" class="ed-icon" :aria-label="`复制 ${file.metadata.episode || file.name} ED2K 链接`" title="复制 ED2K 链接" @click="$emit('copy')"><IconCopy :size="15" aria-hidden="true" /></button>
      <button type="button" class="ed-action-pill ed-secondary-pill" :disabled="disabled || cloudDisabled || cloud?.busy || cloud?.success || cloud?.uncertain" :aria-busy="cloud?.busy" :aria-label="cloudLabel === '云下载' ? '115 云下载' : cloudLabel" :title="cloud?.message || '115 云下载'" @click="$emit('download')"><component :is="cloud?.busy ? IconLoader2 : cloud?.success ? IconCheck : cloud?.uncertain ? IconAlertCircle : IconDownload" :size="15" :class="{ 'ed-spin': cloud?.busy }" aria-hidden="true" /><span>{{ cloudLabel }}</span></button>
      <button type="button" class="ed-action-pill ed-primary" :disabled="disabled || strm?.busy || strm?.success || strm?.uncertain" :aria-busy="strm?.busy" :aria-label="strmLabel === 'STRM' ? 'ED2K STRM' : strmLabel" :title="strm?.message || '生成 ED2K STRM'" @click="$emit('generate')"><component :is="strm?.busy ? IconLoader2 : strm?.success ? IconCheck : strm?.uncertain ? IconAlertCircle : IconFile" :size="15" :class="{ 'ed-spin': strm?.busy }" aria-hidden="true" /><span>{{ strmLabel }}</span></button>
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
.ed-file { display: grid; grid-template-columns: 24px minmax(0, 1fr) 32px 80px auto; align-items: center; column-gap: 8px; min-width: 0; min-height: 60px; padding: 8px 12px; border-bottom: 1px solid var(--search-dialog-card-border); cursor: pointer; transition: background-color .12s ease; }
.ed-file.is-disabled { cursor: default; }
.ed-file:last-child { border-bottom: 0; }.ed-file.is-selected { background: var(--search-dialog-tonal); }
.ed-check { display: grid; place-items: center; min-height: 36px; cursor: pointer; }.ed-check input { width: 15px; height: 15px; accent-color: var(--search-dialog-primary); }
.ed-information { min-width: 0; text-align: left; }.ed-headline { display: flex; align-items: center; gap: 9px; min-width: 0; }.ed-headline strong { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 600; }.ed-headline strong.ed-episode { flex-shrink: 0; font-variant-numeric: tabular-nums; color: var(--search-dialog-on-tonal); }
.ed-specs { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
.ed-expand-toggle { display: grid; place-items: center; width: 32px; height: 32px; border: 0; border-radius: 999px; background: transparent; color: var(--search-dialog-muted); cursor: pointer; transition: background-color .15s ease, color .15s ease; }
.ed-expand-toggle[aria-expanded="true"] { color: var(--search-dialog-primary); background: var(--search-dialog-action-background); }
.ed-chevron { flex: none; transition: transform .15s ease; }.ed-chevron.expanded { transform: rotate(180deg); }
.ed-secondary { display: flex; align-items: baseline; min-width: 0; margin-top: 3px; color: var(--search-dialog-muted); font-size: 11px; line-height: 16px; }
.ed-secondary.is-size-only { display: none; }
.ed-secondary-text { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ed-inline-size { display: none; flex: none; font-variant-numeric: tabular-nums; white-space: nowrap; }
.ed-size { text-align: right; font-variant-numeric: tabular-nums; font-size: 12px; color: var(--search-dialog-muted); white-space: nowrap; }
/* 统一采用与全站搜索弹窗一致的 Tonal 胶囊按钮，移动端同时保留短文字标签与 44px 触控热区。 */
.ed-actions { display: flex; gap: 6px; align-items: center; }
.ed-actions button { position: relative; isolation: isolate; display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 32px; padding: 5px 11px; border: 0; border-radius: 999px; background: transparent; color: var(--search-dialog-text); font-size: 11px; font-weight: 600; line-height: 18px; white-space: nowrap; }
.ed-actions button::before { content: ''; position: absolute; z-index: -1; inset: 0; border-radius: inherit; background: var(--ed-btn-bg, transparent); box-shadow: var(--ed-btn-ring, none); pointer-events: none; transition: background-color .15s ease; }
.ed-actions button.ed-secondary-pill { --ed-btn-bg: var(--search-dialog-secondary-background); color: var(--search-dialog-text); }
.ed-actions button.ed-primary { --ed-btn-bg: var(--search-dialog-action-background); --ed-btn-ring: inset 0 0 0 1px color-mix(in srgb, var(--search-dialog-primary) 24%, transparent); color: var(--search-dialog-primary); }
.ed-actions button.ed-icon { --ed-btn-bg: transparent; min-width: 32px; padding: 5px; color: var(--search-dialog-muted); }
.ed-actions button:disabled { opacity: .5; cursor: default; }
.ed-actions button svg { flex: none; }
.ed-spin { animation: ed-spin 1s linear infinite; }
@keyframes ed-spin { to { transform: rotate(360deg); } }
.ed-issue, .ed-details { grid-column: 2 / -1; min-width: 0; overflow-wrap: anywhere; font-size: 12px; line-height: 1.7; }.ed-issue { padding-top: 5px; color: #b45309; }.dark .ed-issue { color: #fcd34d; }.ed-details { margin-top: 8px; padding: 10px 0 4px; border-top: 1px solid var(--search-dialog-card-border); color: var(--search-dialog-muted); }.ed-filename { color: var(--search-dialog-text); }.ed-hash { margin-left: 16px; }.ed-sources { display: flex; flex-wrap: wrap; gap: 4px 14px; }.ed-sources a { display: inline-flex; align-items: center; gap: 6px; color: var(--search-dialog-primary); }.ed-sources time { color: var(--search-dialog-muted); }
button:focus-visible, input:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (hover: hover) {
  .ed-file:not(.is-disabled):not(.is-selected):hover { background: var(--search-dialog-secondary-background); }
  .ed-expand-toggle:hover { background: var(--search-dialog-secondary-hover); color: var(--search-dialog-text); }
  .ed-expand-toggle[aria-expanded="true"]:hover { background: var(--search-dialog-action-hover); color: var(--search-dialog-primary); }
  .ed-actions button.ed-secondary-pill:hover:not(:disabled), .ed-actions button.ed-icon:hover:not(:disabled) { --ed-btn-bg: var(--search-dialog-secondary-hover); }
  .ed-actions button.ed-primary:hover:not(:disabled) { --ed-btn-bg: var(--search-dialog-action-hover); }
}
.ed-actions button.ed-secondary-pill:active:not(:disabled), .ed-actions button.ed-icon:active:not(:disabled) { --ed-btn-bg: var(--search-dialog-secondary-pressed); }
.ed-actions button.ed-primary:active:not(:disabled) { --ed-btn-bg: var(--search-dialog-action-pressed); }
@media (max-width: 760px) {
  /* 移动端将大小并入规格行，操作行对齐标题列，详情按钮独立保持 44px 点击高度。 */
  .ed-file { grid-template-columns: 24px minmax(0, 1fr) 44px; align-items: start; gap: 2px 8px; min-height: 0; padding: 10px 8px 6px 10px; }
  .ed-check { grid-row: 1; min-height: 44px; margin-top: -4px; }
  .ed-information { grid-column: 2; padding-top: 2px; }
  .ed-expand-toggle { grid-column: 3; grid-row: 1; width: 44px; height: 44px; margin-top: -4px; }
  .ed-size { display: none; }
  .ed-secondary.is-size-only { display: flex; }
  .ed-inline-size { display: inline-flex; align-items: baseline; }
  .ed-secondary-text + .ed-inline-size::before { content: '·'; margin-inline: 5px; opacity: .7; }
  .ed-actions { grid-column: 2 / -1; flex-wrap: wrap; gap: 2px 8px; margin-top: 2px; padding-right: 2px; }
  .ed-actions button { min-height: 44px; padding: 6px 12px; font-size: 12px; }
  .ed-actions button::before { inset: 5px 0; }
  .ed-actions button.ed-icon { min-width: 40px; padding: 6px; margin-left: -10px; margin-right: auto; }
  .ed-actions button.ed-icon::before { inset: 5px 2px; }
  .ed-specs { font-size: 11px; }
  .ed-secondary { max-width: 100%; }
  .ed-hash { display: block; margin-left: 0; }
  .ed-details, .ed-issue { grid-column: 1 / -1; }
}
@media (max-width: 359px) {
  .ed-file { column-gap: 8px; padding-inline: 8px; }
  .ed-actions { gap: 2px 6px; }
  .ed-actions button.ed-action-pill { padding-inline: 10px; gap: 4px; }
}
@media (prefers-contrast: more) {
  .ed-actions button.ed-secondary-pill { --ed-btn-ring: inset 0 0 0 1px var(--search-dialog-card-border); }
  .ed-actions button.ed-primary { --ed-btn-ring: inset 0 0 0 1px var(--search-dialog-primary); }
}
@media (forced-colors: active) {
  .ed-actions button.ed-action-pill::before { border: 1px solid ButtonText; }
}
@media (prefers-reduced-motion: reduce) {
  .ed-file, .ed-expand-toggle, .ed-chevron, .ed-actions button::before { transition: none; }
  .ed-spin { animation: none; }
}
</style>
