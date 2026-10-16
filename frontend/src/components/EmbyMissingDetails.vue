<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { embyRequest, type EmbyMissingPreview, type EmbyMissingSeason, type EmbyMissingSeries } from '../types/emby'
import FollowDialog from './FollowDialog.vue'

const props = defineProps<{ item: EmbyMissingSeries; token: string }>()
const emit = defineEmits<{ close: []; removed: [count: number] }>()
const confirming = ref(false), loading = ref(false), submitting = ref(false), error = ref('')
const preview = ref<EmbyMissingPreview>(), cancelButton = ref<HTMLButtonElement>(), excludeButton = ref<HTMLButtonElement>()
const lifecycle = new AbortController()
let reader: AbortController | undefined
function rangesLabel(season: EmbyMissingSeason) {
  return '缺' + season.ranges.map(range => range.first === range.last ? `第${range.first}集` : `第${range.first}-${range.last}集`).join('、')
}
async function prepare() {
  if (loading.value || submitting.value) return
  confirming.value = true; error.value = ''; preview.value = undefined
  await nextTick(); cancelButton.value?.focus()
  await readPreview()
}
async function readPreview() {
  reader?.abort()
  const current = new AbortController(); reader = current
  loading.value = true; error.value = ''; preview.value = undefined
  try {
    const data: EmbyMissingPreview = await embyRequest(props.token, `missing/preview?${new URLSearchParams({ key: props.item.key })}`, AbortSignal.any([lifecycle.signal, current.signal]))
    if (!current.signal.aborted && !lifecycle.signal.aborted) preview.value = data
  } catch (reason) {
    if (!current.signal.aborted && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取来源失败'
  } finally { if (reader === current) loading.value = false }
}
async function back() {
  if (submitting.value) return
  reader?.abort(); loading.value = false; confirming.value = false; preview.value = undefined; error.value = ''
  await nextTick(); excludeButton.value?.focus()
}
async function submit() {
  if (!preview.value || submitting.value || loading.value) return
  submitting.value = true; error.value = ''
  try {
    const { scope, key, revision } = preview.value
    const result: { excluded: number } = await embyRequest(props.token, 'missing/exclusions', lifecycle.signal, { scope, key, revision })
    if (!lifecycle.signal.aborted) emit('removed', result.excluded)
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      error.value = reason instanceof Error ? reason.message : '排除失败，请重试'
      preview.value = undefined
    }
  } finally { submitting.value = false }
}
onBeforeUnmount(() => { lifecycle.abort(); reader?.abort() })
</script>

<template>
  <FollowDialog :title="confirming ? '确认排除该剧集？' : '缺集明细'" :busy="submitting" @close="emit('close')">
    <template v-if="!confirming">
      <h3 class="break-words text-sm font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ item.title }}</h3>
      <p class="mb-4 mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400">共缺 {{ item.missingCount.toLocaleString() }} 集，涉及 {{ item.seasons.length }} 季</p>
      <div class="space-y-3">
        <section v-for="season in item.seasons" :key="season.season" class="rounded-xl border border-slate-200 p-3 dark:border-slate-700">
          <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-xs">
            <h4 class="font-medium text-slate-800 dark:text-slate-200">第 {{ season.season }} 季</h4><span class="text-slate-500 dark:text-slate-400">截至第 {{ season.lastEpisode }} 集</span>
          </div>
          <p class="mt-2 break-words text-sm leading-7 text-amber-800 dark:text-amber-300">{{ rangesLabel(season) }}</p>
        </section>
      </div>
      <div class="mt-5 border-t border-slate-100 pt-4 dark:border-slate-800">
        <button ref="excludeButton" type="button" class="min-h-11 text-sm text-slate-600 underline underline-offset-4 hover:text-amber-800 dark:text-slate-400 dark:hover:text-amber-300" @click="prepare">从缺集检测移除</button>
        <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">适用于误刮削或残留条目，可在“已排除”中恢复。</p>
      </div>
    </template>
    <template v-else>
      <h3 class="break-words text-sm font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ preview?.title || item.title }}</h3>
      <p class="mt-3 rounded-xl bg-amber-50 p-3 text-sm leading-6 text-amber-900 dark:bg-amber-500/10 dark:text-amber-200">仅从本地缺集检测和统计中排除以下来源，不修改 Emby 数据，不删除 Emby、115 或 STRM 文件。可随时恢复检测。</p>
      <p v-if="loading" role="status" class="mt-4 text-sm text-slate-500 dark:text-slate-400">正在读取本地索引中的具体来源</p>
      <template v-if="preview">
        <p class="mt-4 text-xs leading-6 text-slate-600 dark:text-slate-300">将排除 {{ preview.sources.length }} 个来源条目。推算的 {{ preview.missingCount.toLocaleString() }} 集缺失是检测结果，不是待删除文件数；已入库分集记录不受影响。</p>
        <p v-if="preview.sources.length > 1" class="mt-1 text-xs leading-6 text-slate-500 dark:text-slate-400">此卡片合并了多个来源，确认后将排除下面列出的全部来源。</p>
        <ul class="mt-3 space-y-3" aria-label="待排除来源">
          <li v-for="source in preview.sources" :key="source.seriesId" class="space-y-1 break-all rounded-xl border border-slate-200 p-3 text-xs leading-6 text-slate-600 dark:border-slate-700 dark:text-slate-300">
            <p class="font-medium text-slate-900 dark:text-slate-100">{{ source.title }}</p>
            <p>{{ source.tmdbId ? `TMDB ${source.tmdbId}` : '未关联 TMDB' }}</p>
            <p>Emby Series ID：{{ source.seriesId }}</p>
            <p>来源媒体库 ID：{{ source.libraryId || '索引未记录' }}</p>
            <p>已入库正式分集记录：{{ source.episodeCount.toLocaleString() }} 条</p>
          </li>
        </ul>
        <p class="mt-3 text-xs leading-6 text-slate-500 dark:text-slate-400">排除只针对当前 TMDB 绑定；在 Emby 修正编号并更新索引后，新绑定仍正常参与检测。</p>
      </template>
      <div v-if="error" role="alert" class="mt-4 rounded-xl bg-red-50 p-3 text-sm leading-6 text-red-700 dark:bg-red-500/10 dark:text-red-300">
        <p>{{ error }}</p>
        <button v-if="!preview" type="button" :disabled="loading" class="mt-1 min-h-10 underline disabled:opacity-50" @click="readPreview">重试读取来源</button>
      </div>
      <div class="mt-5 flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
        <button ref="cancelButton" type="button" :disabled="submitting" class="min-h-11 rounded-lg border border-slate-200 px-4 text-sm text-slate-700 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200" @click="back">取消，返回明细</button>
        <button type="button" :disabled="!preview || loading || submitting" class="min-h-11 rounded-lg bg-amber-600 px-4 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50" @click="submit">{{ submitting ? '正在排除' : error && preview ? '重试排除' : '确认排除' }}</button>
      </div>
    </template>
  </FollowDialog>
</template>
