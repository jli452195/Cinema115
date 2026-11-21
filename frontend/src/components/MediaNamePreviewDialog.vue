<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, useId } from 'vue'
import { IconArrowUpRight, IconCheck, IconCopy, IconInfoCircle, IconLoader2, IconSearch } from '@tabler/icons-vue'
import { RouterLink } from 'vue-router'
import FloatingDialog from './FloatingDialog.vue'
import PosterPlaceholder from './PosterPlaceholder.vue'
import { previewMediaName, type MediaNamePreview, type MediaNamePreviewWork } from '../lib/media-organize-api'
import { copyText } from '../utils/clipboard'
import { mediaSpecificationLabels, mediaSpecificationValue } from '../utils/mediaSpecificationLabels'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: [] }>()
const inputID = useId()
const formID = useId()
const input = ref('')
const inputField = ref<HTMLInputElement>()
const viewportStyle = shallowRef<Record<string, string>>()
const pending = ref(false)
const error = ref('')
const preview = shallowRef<MediaNamePreview>()
const posterFailed = ref(false)
const copyState = ref<'copied' | 'failed'>()
let controller: AbortController | undefined
let viewport: VisualViewport | null = null

const seasonEpisode = computed(() => {
  const item = preview.value
  if (item?.season === undefined) return ''
  const season = `S${String(item.season).padStart(2, '0')}`
  if (item.episode === undefined) return season
  const start = `${season}E${String(item.episode).padStart(2, '0')}`
  return item.endEpisode !== undefined && item.endEpisode > item.episode
    ? `${start}–E${String(item.endEpisode).padStart(2, '0')}`
    : start
})

const parsedName = computed(() => {
  const item = preview.value
  if (!item) return ''
  const { title, year, work } = item
  if (work && title === work.title && (!year || year === work.date?.slice(0, 4))) return ''
  return [title, year, work ? '' : seasonEpisode.value].filter(Boolean).join(' · ')
})

const specifications = computed(() => {
  const values = preview.value?.specifications
  if (!values) return []
  return Object.entries(mediaSpecificationLabels).flatMap(([field, label]) => {
    if (values.source && (field === 'sourceMedium' || field === 'sourceModifier')) return []
    const value = values[field]?.trim()
    return value ? [{ field, label, value: mediaSpecificationValue(field, value) }] : []
  })
})

const output = computed(() => {
  const relativePath = preview.value?.relativePath
  if (!relativePath) return
  const boundary = relativePath.lastIndexOf('/')
  return { directory: relativePath.slice(0, Math.max(0, boundary)), filename: relativePath.slice(boundary + 1), relativePath }
})

const poster = computed(() => tmdbImageURL(preview.value?.work?.posterUrl || ''))

function workSummary(work: MediaNamePreviewWork) {
  return [work.kind === 'movie' ? '电影' : '剧集', work.date?.slice(0, 4), `TMDB ${work.id}`].filter(Boolean).join(' · ')
}

function workRoute(work: MediaNamePreviewWork) {
  return { name: work.kind === 'movie' ? 'movie-detail' : 'tv-detail', params: { tmdbId: work.id } }
}

function close() {
  controller?.abort()
  emit('close')
}

function updateViewport() {
  // 软键盘可能只缩小可视区域，保留浏览器双指缩放时的原生定位。
  if (!viewport || Math.abs(viewport.scale - 1) > .01 || viewport.height >= window.innerHeight - 1) {
    viewportStyle.value = undefined
    return
  }
  viewportStyle.value = {
    '--name-preview-viewport-height': `${viewport.height}px`,
    '--name-preview-viewport-top': `${viewport.offsetTop}px`,
    '--name-preview-viewport-bottom': `${Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop)}px`,
  }
}

async function copyOutputPath(relativePath: string) {
  try {
    await copyText(relativePath)
    if (output.value?.relativePath === relativePath) copyState.value = 'copied'
  } catch {
    if (output.value?.relativePath === relativePath) copyState.value = 'failed'
  }
}

async function startPreview() {
  const name = input.value.trim()
  if (pending.value || !name) return
  if (window.matchMedia('(max-width: 639px)').matches) inputField.value?.blur()
  const request = new AbortController()
  controller = request
  pending.value = true
  error.value = ''
  preview.value = undefined
  posterFailed.value = false
  copyState.value = undefined
  try {
    const result = await previewMediaName(props.token, name, request.signal)
    if (!request.signal.aborted) preview.value = result
  } catch (reason) {
    if (!request.signal.aborted) {
      error.value = reason instanceof Error && reason.name === 'TimeoutError' ? '查询超时，请重试。' : reason instanceof Error ? reason.message : '识别测试失败，请重试。'
    }
  } finally {
    pending.value = false
    controller = undefined
  }
}

onMounted(() => {
  viewport = window.visualViewport
  viewport?.addEventListener('resize', updateViewport)
  viewport?.addEventListener('scroll', updateViewport)
  updateViewport()
})

onBeforeUnmount(() => {
  controller?.abort()
  viewport?.removeEventListener('resize', updateViewport)
  viewport?.removeEventListener('scroll', updateViewport)
})
</script>

<template>
  <FloatingDialog class="name-preview-dialog" :style="viewportStyle" title="名称识别测试" close-label="关闭名称识别测试" mobile-presentation="dialog" @close="close">
    <form :id="formID" class="name-preview-form" @submit.prevent="startPreview">
      <label :for="inputID">作品名、视频文件名或相对路径</label>
      <input :id="inputID" ref="inputField" v-model="input" type="text" enterkeyhint="search" spellcheck="false" autocapitalize="off" autocomplete="off" :readonly="pending" placeholder="Sayonara.Lara.S01E05.1080p.BluRay.Remux.strm" @input="preview = undefined; error = ''" />
    </form>

    <p v-if="error" class="mo-error" role="alert">{{ error }}</p>
    <p v-else-if="pending" class="mo-muted name-preview-status" role="status">正在识别名称并查询 TMDB…</p>
    <section v-else-if="preview" class="name-preview-result" aria-label="识别结果" aria-live="polite">
      <div v-if="preview.work" class="name-preview-work">
        <div class="name-preview-poster" aria-hidden="true">
          <img v-if="poster && !posterFailed" :src="poster" alt="" decoding="async" referrerpolicy="no-referrer" @error="posterFailed = true" />
          <PosterPlaceholder v-else :title="preview.work.title" />
        </div>
        <div class="name-preview-work-body">
          <div class="name-preview-title-row">
            <h3 dir="auto">
              <RouterLink :to="workRoute(preview.work)" class="name-preview-link" @click="close">{{ preview.work.title }}<IconArrowUpRight :size="15" aria-hidden="true" /></RouterLink>
            </h3>
            <span v-if="preview.work.kind === 'tv' && seasonEpisode" class="name-preview-episode">{{ seasonEpisode }}</span>
          </div>
          <p class="name-preview-meta">{{ workSummary(preview.work) }}</p>
          <p v-if="preview.work.overview" class="name-preview-overview" dir="auto">{{ preview.work.overview }}</p>
        </div>
      </div>

      <p v-if="parsedName" class="name-preview-parsed"><span>名称解析</span> <bdi>{{ parsedName }}</bdi></p>

      <ul v-if="specifications.length" class="name-preview-specs" aria-label="识别出的媒体规格">
        <li v-for="spec in specifications" :key="spec.field" :title="spec.label">
          <span class="sr-only">{{ spec.label }} </span><bdi>{{ spec.value }}</bdi>
        </li>
      </ul>

      <div v-if="output" class="name-preview-output">
        <div class="name-preview-output-heading">
          <h3>命名预览</h3>
          <button type="button" class="name-preview-copy" :aria-label="copyState === 'copied' ? '已复制预览路径' : '复制预览路径'" @click="copyOutputPath(output.relativePath)">
            <IconCheck v-if="copyState === 'copied'" :size="16" aria-hidden="true" />
            <IconCopy v-else :size="16" aria-hidden="true" />
            {{ copyState === 'copied' ? '已复制' : '复制路径' }}
          </button>
        </div>
        <code class="name-preview-filename" dir="auto">{{ output.filename }}</code>
        <p v-if="output.directory" class="name-preview-directory"><span class="sr-only">目标目录 </span><bdi>{{ output.directory }}/</bdi></p>
        <p v-if="copyState === 'failed'" class="name-preview-meta" role="status">复制失败，可选中文本复制。</p>
      </div>

      <p v-if="preview.message" class="name-preview-message">
        <IconInfoCircle :size="17" aria-hidden="true" />
        <span>{{ preview.message }}</span>
      </p>
    </section>

    <template #footer>
      <div class="mo-footer">
        <button type="button" class="mo-button name-preview-footer-close" @click="close">关闭</button>
        <button :form="formID" type="submit" class="mo-button mo-primary" :disabled="pending || !input.trim()">
          <IconLoader2 v-if="pending" :size="16" class="motion-safe:animate-spin" aria-hidden="true" />
          <IconSearch v-else :size="16" aria-hidden="true" />
          {{ pending ? '查询中…' : '识别并查询 TMDB' }}
        </button>
      </div>
    </template>
  </FloatingDialog>
</template>

<style scoped src="../styles/media-organize.css"></style>
<style scoped>
.name-preview-form { display: grid; gap: 8px; }
.name-preview-form label { font-weight: 600; }
.name-preview-form input {
  width: 100%;
  min-height: var(--dialog-control-height);
  padding: var(--dialog-control-padding);
  border: 1px solid rgb(var(--line-default));
  border-radius: 9px;
  background: rgb(var(--surface-card));
  color: rgb(var(--content-primary));
  font-size: var(--dialog-input-font);
}
.name-preview-form input:focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 1px; }
.name-preview-status, .name-preview-result { margin-top: 18px; }
.name-preview-result {
  display: grid;
  gap: 12px;
  min-width: 0;
  padding-top: 16px;
  border-top: 1px solid rgb(var(--line-default));
  overflow-wrap: anywhere;
}
.name-preview-work { display: grid; grid-template-columns: 86px minmax(0, 1fr); align-items: start; gap: 14px; }
.name-preview-poster { aspect-ratio: 2 / 3; overflow: hidden; border: 1px solid rgb(var(--line-default)); border-radius: 9px; }
.name-preview-poster img { width: 100%; height: 100%; object-fit: cover; }
.name-preview-poster :deep(.poster-placeholder span) { font-size: 2rem; }
.name-preview-work-body { display: grid; gap: 5px; min-width: 0; padding-top: 2px; }
.name-preview-title-row { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 8px; }
.name-preview-title-row h3 { color: rgb(var(--content-primary)); font-size: 19px; font-weight: 650; line-height: 1.3; }
.name-preview-link { text-decoration: none; text-underline-offset: 3px; }
.name-preview-link svg { display: inline-block; margin-inline-start: 4px; color: rgb(var(--action-link)); vertical-align: middle; }
.name-preview-link:hover { text-decoration: underline; }
.name-preview-meta, .name-preview-parsed, .name-preview-directory { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 1.5; }
.name-preview-overview {
  display: -webkit-box;
  margin-top: 3px;
  overflow: hidden;
  color: rgb(var(--content-secondary));
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}
.name-preview-parsed span { margin-inline-end: 5px; font-weight: 550; }
.name-preview-specs { display: flex; flex-wrap: wrap; gap: 6px; }
.name-preview-episode, .name-preview-specs li {
  padding: 3px 9px;
  border-radius: 999px;
  background: rgb(var(--action-link) / .09);
  color: rgb(var(--action-link));
  font-size: 12px;
  font-weight: 550;
  line-height: 1.5;
}
.name-preview-episode { background: rgb(var(--selection-surface)); color: rgb(var(--selection-content)); font-variant-numeric: tabular-nums; }
.name-preview-output, .name-preview-message {
  min-width: 0;
  padding: 9px 12px 11px;
  border: 1px solid rgb(var(--line-default));
  border-radius: 10px;
  background: rgb(var(--surface-card) / .6);
}
.name-preview-output { display: grid; gap: 3px; }
.name-preview-output-heading { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 2px 8px; }
.name-preview-output-heading h3 { color: rgb(var(--content-secondary)); font-size: 12px; font-weight: 600; }
.name-preview-copy {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 30px;
  padding: 2px 7px;
  border-radius: 7px;
  color: rgb(var(--action-link));
  font-size: 12px;
  white-space: nowrap;
}
.name-preview-copy:hover { background: rgb(var(--action-link) / .08); }
.name-preview-filename { display: block; color: rgb(var(--content-primary)); font-size: 13px; font-weight: 550; line-height: 1.5; }
.name-preview-message { display: flex; align-items: flex-start; gap: 8px; color: rgb(var(--content-secondary)); }
.name-preview-message svg { flex: none; margin-top: 2px; }
@media (max-width: 639px) {
  :global(dialog.floating-dialog.name-preview-dialog[open]:not(.bottom-sheet-dialog-mobile)) {
    --name-preview-top-gap: max(12px, var(--app-safe-top));
    --name-preview-bottom-gap: max(12px, var(--app-safe-bottom));
    inset-block-start: calc(var(--name-preview-viewport-top, 0px) + var(--name-preview-top-gap));
    inset-block-end: calc(var(--name-preview-viewport-bottom, 0px) + var(--name-preview-bottom-gap));
    max-height: calc(var(--name-preview-viewport-height, 100dvh) - var(--name-preview-top-gap) - var(--name-preview-bottom-gap));
  }
  .name-preview-result { gap: 10px; padding-top: 12px; }
  .name-preview-work { grid-template-columns: 64px minmax(0, 1fr); gap: 11px; }
  .name-preview-title-row h3 { font-size: 16px; }
  .name-preview-copy { min-height: 44px; }
  .name-preview-footer-close { display: none; }
  .mo-footer .mo-primary { width: 100%; justify-content: center; }
}
</style>
