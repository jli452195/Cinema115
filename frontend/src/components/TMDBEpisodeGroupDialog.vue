<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import type { NumberingChoices, NumberingInfo, NumberingPreview } from '../types/tvNumbering'
import { episodeCode, episodeGroupTypes, numberingChanges, numberingLabel } from '../types/tvNumbering'

const props = defineProps<{ token: string; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: []; saved: [value: NumberingInfo] }>()
const dialog = ref<HTMLDialogElement>(), headingID = useId(), radioName = useId()
const choices = ref<NumberingChoices>(), draftID = ref(''), preview = ref<NumberingPreview>()
const loading = ref(false), previewLoading = ref(false), saving = ref(false), error = ref(''), previewError = ref(''), conflict = ref(false)
const limits = ref<Record<number, number>>({})
const lifecycle = new AbortController()
let previewController: AbortController | undefined
let loadEpoch = 0
let backdropPointerDown = false
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const previousOverflow = document.body.style.overflow
const candidates = computed(() => {
  const items = [...(choices.value?.groups || [])], snapshot = choices.value?.snapshot
  if (snapshot && !items.some(item => item.id === snapshot.id)) items.unshift(snapshot)
  return items
})
const isCurrent = computed(() => draftID.value === choices.value?.current.groupId)
const canSave = computed(() => !!choices.value && !loading.value && !previewLoading.value && !saving.value && !conflict.value && !previewError.value && (draftID.value ? !!preview.value?.fingerprint && (!isCurrent.value || preview.value.changed) : !!choices.value.current.groupId))
const changes = computed(() => choices.value?.snapshot && preview.value && isCurrent.value && preview.value.fingerprint ? numberingChanges(choices.value.snapshot, preview.value.group) : undefined)
const selectedName = computed(() => draftID.value ? preview.value?.group.name || candidates.value.find(item => item.id === draftID.value)?.name || '剧集组' : 'TMDB 默认季集')
function close() { if (!saving.value) emit('close') }
function backdropClick(event: MouseEvent) {
  const outside = backdropPointerDown && event.target === dialog.value
  backdropPointerDown = false
  if (outside) close()
}
async function request<T>(path: string, init: RequestInit = {}, signal = lifecycle.signal): Promise<T> {
  const response = await fetch(`/api/admin/tmdb/tv/${props.tmdbId}/${path}`, { ...init, headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(50000)]) })
  const body = await response.json().catch(() => null)
  if (!response.ok || !body) { const failure = new Error(body?.error || `请求失败（HTTP ${response.status}）`); Object.assign(failure, { status: response.status }); throw failure }
  return body
}
async function load() {
  const epoch = ++loadEpoch
  previewController?.abort()
  previewLoading.value = false
  loading.value = true; choices.value = undefined; error.value = ''; preview.value = undefined; previewError.value = ''; limits.value = {}
  try {
    const data = await request<NumberingChoices>('episode-groups')
    if (lifecycle.signal.aborted || epoch !== loadEpoch) return
    conflict.value = false
    choices.value = data; draftID.value = data.current.groupId || ''
    if (data.snapshot) preview.value = { group: data.snapshot, fingerprint: '', changed: false }
    loading.value = false
    await nextTick()
    if (lifecycle.signal.aborted || epoch !== loadEpoch) return
    dialog.value?.querySelector<HTMLInputElement>('input:checked')?.focus()
  } catch (e) { if (!lifecycle.signal.aborted && epoch === loadEpoch) error.value = e instanceof Error ? e.message : '读取方案失败' }
  finally { if (!lifecycle.signal.aborted && epoch === loadEpoch) loading.value = false }
}
async function select(id: string) {
  previewController?.abort(); previewLoading.value = false
  draftID.value = id; preview.value = undefined; previewError.value = ''; error.value = ''; limits.value = {}
  if (!id) return
  if (id === choices.value?.current.groupId && choices.value.snapshot) {
    preview.value = { group: choices.value.snapshot, fingerprint: '', changed: false }; return
  }
  await checkUpstream()
}
async function checkUpstream() {
  if (!draftID.value || saving.value) return
  previewController?.abort()
  const controller = new AbortController(); previewController = controller
  const targetID = draftID.value
  previewLoading.value = true; previewError.value = ''
  try {
    const data = await request<NumberingPreview>(`episode-groups/${encodeURIComponent(targetID)}`, {}, AbortSignal.any([lifecycle.signal, controller.signal]))
    if (!controller.signal.aborted && !lifecycle.signal.aborted && draftID.value === targetID) preview.value = data
  } catch (e) { if (!controller.signal.aborted && !lifecycle.signal.aborted && draftID.value === targetID) previewError.value = e instanceof Error ? e.message : '预览失败' }
  finally { if (previewController === controller) previewLoading.value = false }
}
async function save() {
  if (!canSave.value || !choices.value) return
  saving.value = true; error.value = ''
  try {
    const result = await request<NumberingInfo>('numbering', { method: 'PUT', body: JSON.stringify({ groupId: draftID.value, revision: choices.value.current.revision, fingerprint: preview.value?.fingerprint || '' }) })
    if (!lifecycle.signal.aborted) emit('saved', result)
  } catch (e) {
    if (!lifecycle.signal.aborted) {
      error.value = e instanceof Error ? e.message : '保存失败'
      // A lost response may have committed. Re-read before any subsequent write.
      conflict.value = true
    }
  } finally { if (!lifecycle.signal.aborted) saving.value = false }
}
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal(); void load() })
onBeforeUnmount(() => { lifecycle.abort(); previewController?.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected && previousFocus !== document.body) previousFocus.focus({ preventScroll: true }) })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-labelledby="headingID" class="numbering-dialog" @cancel.prevent="close" @pointerdown="event => { backdropPointerDown = event.target === dialog }" @click="backdropClick">
      <header class="numbering-header">
        <div class="min-w-0"><h2 :id="headingID" class="text-base font-semibold">设置季集方案</h2><p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-400">{{ title }}</p></div>
        <button type="button" aria-label="关闭季集方案" :disabled="saving" class="numbering-close" @click="close"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
      </header>
      <div class="numbering-body">
        <p class="text-xs leading-5 text-slate-500 dark:text-slate-400">保存后，详情、入库统计、追更与整理统一使用此方案。Emby 的季集编号按所选方案解释，请核对资源编排。</p>
        <p v-if="error" role="alert" class="numbering-error">{{ error }}<button v-if="!saving" class="ml-2 underline" @click="load">读取最新方案</button></p>
        <p v-if="loading" role="status" class="py-12 text-center text-sm text-slate-500">正在读取季集方案…</p>
        <template v-else-if="choices">
          <p v-if="choices.groupsError || choices.defaultError" role="status" class="mt-3 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ choices.groupsError }} {{ choices.defaultError }}<button type="button" class="ml-2 underline" :disabled="saving" @click="load">重新读取</button></p>
          <div class="mt-4 rounded-xl bg-slate-50 px-3 py-2 text-xs leading-5 dark:bg-slate-800/60">
            <span class="text-slate-500 dark:text-slate-400">当前生效：</span>{{ numberingLabel(choices.current) }}
            <span v-if="choices.current.groupId && choices.current.savedAt" class="block text-slate-500 dark:text-slate-400">已保存 {{ new Date(choices.current.savedAt * 1000).toLocaleString('zh-CN') }}，普通刷新保留此编排。</span>
          </div>
          <div class="numbering-grid">
            <fieldset class="min-w-0 space-y-2" :disabled="saving || conflict">
              <legend class="mb-2 text-xs font-semibold text-slate-500 dark:text-slate-400">选择方案</legend>
              <label class="numbering-option" :class="{ selected: draftID === '' }">
                <input type="radio" :name="radioName" value="" :checked="draftID === ''" @change="select('')" />
                <span class="min-w-0"><span class="block text-sm font-medium">TMDB 默认季集</span><span class="mt-1 block text-xs text-slate-500 dark:text-slate-400">{{ choices.defaultError ? '默认季集资料暂时不可用' : `${choices.defaultSeasons.filter(s => s.number > 0).length} 季，共 ${choices.defaultEpisodeCount} 集` }}</span></span>
              </label>
              <label v-for="group in candidates" :key="group.id" class="numbering-option" :class="{ selected: draftID === group.id }">
                <input type="radio" :name="radioName" :value="group.id" :checked="draftID === group.id" @change="select(group.id)" />
                <span class="min-w-0"><span class="block break-words text-sm font-medium">{{ group.name }}</span><span class="mt-1 block text-xs text-slate-500 dark:text-slate-400">{{ episodeGroupTypes[group.type] || '剧集组' }} · {{ group.groupCount }} 组，共 {{ group.episodeCount }} 集</span><span v-if="choices.current.groupId === group.id" class="mt-1 block text-[11px] text-blue-600 dark:text-blue-400">当前生效</span></span>
              </label>
              <p v-if="!candidates.length && !choices.groupsError" class="px-1 text-xs leading-5 text-slate-500 dark:text-slate-400">TMDB 暂未提供其他剧集组。</p>
            </fieldset>
            <section class="numbering-preview" aria-label="季集方案预览" :aria-busy="previewLoading">
              <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-semibold">{{ selectedName }}</h3><button v-if="draftID && isCurrent" type="button" class="text-xs text-blue-600 disabled:opacity-50 dark:text-blue-400" :disabled="previewLoading || saving || conflict" @click="checkUpstream">查看 TMDB 更新</button></div>
              <p v-if="previewLoading" role="status" class="mt-4 text-xs text-slate-500">正在读取 TMDB 分集…</p>
              <p v-if="previewError" role="alert" class="numbering-error">{{ previewError }}<button type="button" class="ml-2 underline" :disabled="previewLoading || saving || conflict" @click="checkUpstream">重试</button></p>
              <template v-if="draftID && preview && !previewLoading">
                <p v-if="!preview.fingerprint" class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">以下为已保存编排。点击“查看 TMDB 更新”可预览上游变化。</p>
                <div v-else-if="isCurrent" class="mt-3 rounded-lg bg-blue-50 p-3 text-xs leading-5 text-blue-800 dark:bg-blue-950/40 dark:text-blue-200" role="status">
                  <template v-if="!preview.changed">当前编排与 TMDB 一致。</template>
                  <template v-else>TMDB 资料有变化，保存后才会更新已选方案。<template v-if="changes">新增 {{ changes.added.length }} 集，移除 {{ changes.removed.length }} 集，调整 {{ changes.changed.length }} 集。</template></template>
                  <details v-if="changes && (changes.added.length || changes.removed.length || changes.changed.length)" class="mt-2"><summary class="cursor-pointer">查看变化</summary><ul class="mt-2 space-y-1"><li v-for="(line, index) in [...changes.added, ...changes.removed, ...changes.changed].slice(0, 100)" :key="index">{{ line }}</li></ul></details>
                </div>
                <p v-if="preview.group.description" class="mt-3 whitespace-pre-line break-words text-xs leading-5 text-slate-500 dark:text-slate-400">{{ preview.group.description }}</p>
                <details v-for="season in preview.group.seasons" :key="season.number" class="numbering-season" :open="preview.group.seasons.length === 1">
                  <summary class="cursor-pointer text-xs font-semibold">{{ season.name || `第 ${season.number} 季` }} · {{ season.episodes.length }} 集</summary>
                  <ol class="mt-3 divide-y divide-slate-100 dark:divide-slate-800">
                    <li v-for="episode in season.episodes.slice(0, limits[season.number] || 50)" :key="episode.id" class="py-2 text-xs leading-5"><span class="mr-2 font-medium tabular-nums">{{ episodeCode(season.number, episode.number) }}</span>{{ episode.name || '暂无标题' }}<span v-if="episode.sourceNumber > 0" class="block text-[11px] text-slate-500 dark:text-slate-400">TMDB 原编号 {{ episodeCode(episode.sourceSeason, episode.sourceNumber) }}</span></li>
                  </ol>
                  <button v-if="season.episodes.length > (limits[season.number] || 50)" type="button" class="mt-2 text-xs text-blue-600 dark:text-blue-400" @click="limits[season.number] = (limits[season.number] || 50) + 100">显示更多分集</button>
                </details>
              </template>
              <template v-else-if="!draftID">
                <p class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">使用 TMDB 默认季集，后续随常规资料刷新更新。</p>
                <ul class="mt-3 space-y-2"><li v-for="season in choices.defaultSeasons" :key="season.number" class="numbering-season text-xs">{{ season.name || `第 ${season.number} 季` }} · {{ season.episodeCount }} 集</li></ul>
              </template>
            </section>
          </div>
        </template>
      </div>
      <footer class="numbering-footer"><button type="button" class="numbering-secondary" :disabled="saving" @click="close">取消</button><button type="button" class="numbering-primary" :disabled="!canSave" @click="save">{{ saving ? '正在保存…' : !draftID && choices?.current.groupId ? '恢复默认并保存' : isCurrent && preview?.changed ? '更新已保存编排' : '保存为全站方案' }}</button></footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.numbering-dialog { @apply m-auto flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-4xl flex-col overflow-hidden rounded-2xl border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-200; }
.numbering-header { @apply flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 px-5 py-4 dark:border-slate-800; }
.numbering-close { @apply shrink-0 rounded-lg p-2 text-slate-500 hover:bg-slate-100 disabled:opacity-40 dark:hover:bg-slate-800; }
.numbering-close svg { @apply h-5 w-5; }
.numbering-body { @apply min-h-0 overflow-y-auto p-5; overscroll-behavior: contain; -webkit-overflow-scrolling: touch; }
.numbering-grid { @apply mt-5 grid items-start gap-5 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]; }
.numbering-option { @apply flex cursor-pointer items-start gap-3 rounded-xl border border-slate-200 p-3 transition-colors hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800/60; }
.numbering-option.selected { @apply border-blue-500 bg-blue-50/60 dark:border-blue-500 dark:bg-blue-950/30; }
.numbering-option input { @apply mt-1 h-4 w-4 shrink-0 accent-blue-600; }
.numbering-option:focus-within { @apply outline outline-2 outline-offset-2 outline-blue-500; }
.numbering-preview { @apply min-w-0 rounded-xl border border-slate-200 p-4 dark:border-slate-700; }
.numbering-season { @apply mt-3 rounded-lg border border-slate-100 p-3 dark:border-slate-800; }
.numbering-error { @apply mt-3 rounded-lg bg-red-50 p-3 text-xs leading-5 text-red-700 dark:bg-red-950/30 dark:text-red-300; }
.numbering-footer { @apply flex shrink-0 flex-wrap justify-end gap-3 border-t border-slate-100 px-5 py-4 dark:border-slate-800; padding-bottom: max(1rem, env(safe-area-inset-bottom)); }
.numbering-primary { @apply rounded-lg bg-blue-600 px-4 py-2 text-xs font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50; }
.numbering-secondary { @apply rounded-lg border border-slate-200 px-4 py-2 text-xs hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:hover:bg-slate-800; }
</style>
