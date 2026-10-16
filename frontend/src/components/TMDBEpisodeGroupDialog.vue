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
const choiceScroll = ref<HTMLElement>(), previewScroll = ref<HTMLElement>()
const choiceTab = ref<HTMLButtonElement>(), previewTab = ref<HTMLButtonElement>()
const mobilePanel = ref<'choices' | 'preview'>('choices'), isMobile = ref(false)
const panelID = useId(), statusID = useId()
const scrollPositions = { choices: 0, preview: 0 }
let mobileQuery: MediaQueryList | undefined
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
const emptyPreview = computed(() => !!draftID.value && !!preview.value?.fingerprint && !preview.value.group.seasons.some(season => season.episodes.length))
const canSave = computed(() => !!choices.value && !loading.value && !previewLoading.value && !saving.value && !conflict.value && !previewError.value && !emptyPreview.value && (draftID.value ? !!preview.value?.fingerprint && (!isCurrent.value || preview.value.changed) : !!choices.value.current.groupId))
const changes = computed(() => choices.value?.snapshot && preview.value && isCurrent.value && preview.value.fingerprint ? numberingChanges(choices.value.snapshot, preview.value.group) : undefined)
const selectedName = computed(() => draftID.value ? preview.value?.group.name || candidates.value.find(item => item.id === draftID.value)?.name || '剧集组' : 'TMDB 默认季集')
const duplicateIDs = computed(() => {
  const result: Record<string, string> = {}
  for (const group of candidates.value) {
    const matches = candidates.value.filter(item => item.name === group.name && item.type === group.type && item.groupCount === group.groupCount && item.episodeCount === group.episodeCount)
    if (matches.length < 2) continue
    let length = 6
    while (length < group.id.length && matches.some(item => item.id !== group.id && item.id.slice(-length) === group.id.slice(-length))) length++
    result[group.id] = group.id.slice(-length)
  }
  return result
})
const actionLabel = computed(() => saving.value ? '正在保存…' : emptyPreview.value ? '不可应用' : !choices.value ? '应用本剧方案' : !draftID.value && choices.value.current.groupId ? '恢复默认方案' : isCurrent.value && preview.value?.changed ? '更新本剧快照' : '应用本剧方案')
const statusMessage = computed(() => {
  if (saving.value) return '正在保存本剧方案，请稍候。'
  if (loading.value) return '正在读取当前方案与候选列表。'
  if (conflict.value) return '请重新读取，核实当前生效方案后再应用。'
  if (error.value) return '方案读取失败，请重新读取。'
  if (previewLoading.value) return '正在读取分集，完成后可核对预览。'
  if (previewError.value) return '分集预览失败，请查看原因后重试。'
  if (emptyPreview.value) return '该方案详情无有效分集数据，无法应用。'
  if (!choices.value) return '读取方案后可查看分集编排。'
  if (!draftID.value && choices.value.current.groupId) return '将恢复 TMDB 默认季集，随常规资料刷新同步。'
  if (isCurrent.value && preview.value?.changed) return '检测到 TMDB 编排更新，应用后更新本剧快照。'
  if (isCurrent.value) return '当前方案未更改。'
  return '正在预览新方案，应用后更新本剧编排。'
})
function rememberScroll() {
  if (!isMobile.value || mobilePanel.value === 'choices') scrollPositions.choices = choiceScroll.value?.scrollTop || 0
  if (!isMobile.value || mobilePanel.value === 'preview') scrollPositions.preview = previewScroll.value?.scrollTop || 0
}
async function switchPanel(panel: 'choices' | 'preview') {
  if (saving.value) return
  rememberScroll()
  mobilePanel.value = panel
  await nextTick()
  const scroll = panel === 'choices' ? choiceScroll.value : previewScroll.value
  if (scroll) scroll.scrollTop = scrollPositions[panel]
  ;(panel === 'choices' ? choiceTab.value : previewTab.value)?.focus({ preventScroll: true })
}
function tabKeydown(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  void switchPanel(event.key === 'Home' ? 'choices' : event.key === 'End' ? 'preview' : mobilePanel.value === 'choices' ? 'preview' : 'choices')
}
async function updateViewport() {
  rememberScroll()
  const focused = document.activeElement
  const hadDialogFocus = focused instanceof HTMLElement && dialog.value?.contains(focused)
  isMobile.value = mobileQuery?.matches || false
  await nextTick()
  if (choiceScroll.value) choiceScroll.value.scrollTop = scrollPositions.choices
  if (previewScroll.value) previewScroll.value.scrollTop = scrollPositions.preview
  if (hadDialogFocus && focused instanceof HTMLElement && (!focused.isConnected || !focused.getClientRects().length)) {
    if (isMobile.value) (mobilePanel.value === 'choices' ? choiceTab.value : previewTab.value)?.focus({ preventScroll: true })
    else if (mobilePanel.value === 'preview') previewScroll.value?.focus({ preventScroll: true })
    else dialog.value?.querySelector<HTMLInputElement>('input:checked')?.focus({ preventScroll: true })
  }
}
async function resetPreviewScroll() {
  scrollPositions.preview = 0
  await nextTick()
  if (previewScroll.value) previewScroll.value.scrollTop = 0
}
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
  mobilePanel.value = 'choices'; scrollPositions.choices = 0; scrollPositions.preview = 0
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
  if (saving.value || conflict.value) return
  previewController?.abort(); previewLoading.value = false
  draftID.value = id; preview.value = undefined; previewError.value = ''; error.value = ''; limits.value = {}
  void resetPreviewScroll()
  if (!id) return
  if (id === choices.value?.current.groupId && choices.value.snapshot) {
    preview.value = { group: choices.value.snapshot, fingerprint: '', changed: false }; return
  }
  await checkUpstream()
}
async function checkUpstream() {
  if (!draftID.value || saving.value || conflict.value) return
  previewController?.abort()
  const controller = new AbortController(); previewController = controller
  const targetID = draftID.value
  previewLoading.value = true; previewError.value = ''
  void resetPreviewScroll()
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
      const reason = e instanceof Error ? e.message : '保存失败'
      error.value = e instanceof Error && 'status' in e ? reason : `响应未确认，本剧方案状态待核实，请重新读取。${reason}`
      // A lost response may have committed. Re-read before any subsequent write.
      conflict.value = true
    }
  } finally { if (!lifecycle.signal.aborted) saving.value = false }
}
onMounted(() => { mobileQuery = window.matchMedia('(max-width: 639px)'); isMobile.value = mobileQuery.matches; mobileQuery.addEventListener('change', updateViewport); document.body.style.overflow = 'hidden'; dialog.value?.showModal(); void load() })
onBeforeUnmount(() => { mobileQuery?.removeEventListener('change', updateViewport); lifecycle.abort(); previewController?.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected && previousFocus !== document.body) previousFocus.focus({ preventScroll: true }) })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-labelledby="headingID" class="media-search-dialog numbering-dialog" @cancel.prevent="close" @pointerdown="event => { backdropPointerDown = event.target === dialog }" @click="backdropClick">
      <header class="numbering-header">
        <div class="min-w-0"><h2 :id="headingID" class="text-base font-semibold">设置季集方案</h2><p class="numbering-muted mt-1 truncate text-xs">{{ title }}</p></div>
        <button type="button" aria-label="关闭季集方案" :disabled="saving" class="numbering-close" @click="close"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="m6 6 12 12M6 18 18 6" /></svg></button>
      </header>
      <div class="numbering-context">
        <p class="numbering-muted text-xs leading-5">应用于本剧的详情、入库、追更与整理；不改名或移动文件。请核对 Emby 资源编号。</p>
        <p v-if="choices" class="numbering-current"><span class="numbering-muted">当前生效：</span>{{ numberingLabel(choices.current) }}<span v-if="choices.current.groupId && choices.current.savedAt" class="numbering-muted numbering-saved-at">已保存 {{ new Date(choices.current.savedAt * 1000).toLocaleString('zh-CN') }}</span></p>
        <p v-if="error" role="alert" class="numbering-error">{{ error }}<button v-if="!saving" type="button" class="ml-2 underline" @click="load">重新读取</button></p>
        <p v-if="choices && (choices.groupsError || choices.defaultError)" role="status" class="numbering-warning">{{ choices.groupsError }} {{ choices.defaultError }}<button type="button" class="ml-2 underline" :disabled="saving" @click="load">重新读取</button></p>
      </div>
      <p v-if="loading" role="status" class="numbering-muted min-h-0 flex-1 py-12 text-center text-sm">正在读取季集方案…</p>
      <div v-else-if="choices" class="numbering-body">
          <div v-if="isMobile" class="numbering-tabs" role="tablist" aria-label="季集方案面板" @keydown="tabKeydown">
            <button :id="`${panelID}-choices-tab`" ref="choiceTab" type="button" role="tab" :aria-selected="mobilePanel === 'choices'" :aria-controls="`${panelID}-choices`" :tabindex="mobilePanel === 'choices' ? 0 : -1" :disabled="saving" @click="switchPanel('choices')">选择方案</button>
            <button :id="`${panelID}-preview-tab`" ref="previewTab" type="button" role="tab" :aria-selected="mobilePanel === 'preview'" :aria-controls="`${panelID}-preview`" :tabindex="mobilePanel === 'preview' ? 0 : -1" :disabled="saving" @click="switchPanel('preview')">分集预览</button>
          </div>
          <div class="numbering-grid">
            <section :id="`${panelID}-choices`" class="numbering-choice-panel" :hidden="isMobile && mobilePanel !== 'choices'" :role="isMobile ? 'tabpanel' : undefined" :aria-labelledby="`${panelID}-choices${isMobile ? '-tab' : '-heading'}`">
              <h3 :id="`${panelID}-choices-heading`" class="numbering-panel-heading">选择方案<span class="numbering-muted font-normal">{{ candidates.length + 1 }} 项</span></h3>
              <fieldset ref="choiceScroll" class="numbering-options numbering-scroll" :aria-labelledby="`${panelID}-choices-heading`" :disabled="saving || conflict">
              <label class="numbering-option" :class="{ selected: draftID === '' }">
                <input type="radio" :name="radioName" value="" :checked="draftID === ''" @change="select('')" />
                <span class="min-w-0"><span class="block text-sm font-medium">TMDB 默认季集</span><span class="numbering-muted mt-1 block text-xs">{{ choices.defaultError ? '默认季集资料暂时不可用' : `${choices.defaultSeasons.filter(s => s.number > 0).length} 季，共 ${choices.defaultEpisodeCount} 集` }}</span><span v-if="!choices.current.groupId" class="numbering-current-badge">当前生效</span></span>
              </label>
              <label v-for="group in candidates" :key="group.id" class="numbering-option" :class="{ selected: draftID === group.id }">
                <input type="radio" :name="radioName" :value="group.id" :checked="draftID === group.id" @change="select(group.id)" />
                <span class="min-w-0"><span class="block break-words text-sm font-medium">{{ group.name }}</span><span class="numbering-muted mt-1 block text-xs">{{ episodeGroupTypes[group.type] || '剧集组' }} · {{ group.groupCount }} 组，共 {{ group.episodeCount }} 集</span><span v-if="duplicateIDs[group.id]" class="numbering-muted mt-1 block break-all text-[11px]">ID 尾号 {{ duplicateIDs[group.id] }}</span><span v-if="!group.episodeCount" class="numbering-muted mt-1 block text-[11px]">暂无分集统计，预览后核对</span><span v-if="choices.current.groupId === group.id" class="numbering-current-badge">当前生效</span></span>
              </label>
              <p v-if="!candidates.length && !choices.groupsError" class="numbering-muted px-1 text-xs leading-5">TMDB 暂未提供其他剧集组。</p>
              </fieldset>
            </section>
            <section :id="`${panelID}-preview`" class="numbering-preview" :hidden="isMobile && mobilePanel !== 'preview'" :role="isMobile ? 'tabpanel' : 'region'" :aria-labelledby="`${panelID}-preview${isMobile ? '-tab' : '-heading'}`" :aria-busy="previewLoading">
              <div class="numbering-preview-heading"><div class="min-w-0"><p class="numbering-muted mb-1 text-[11px]">正在预览</p><h3 :id="`${panelID}-preview-heading`" class="break-words text-sm font-semibold">{{ selectedName }}</h3><p v-if="draftID && preview && !previewLoading" class="numbering-muted mt-1 text-xs">{{ preview.group.groupCount }} 组，共 {{ preview.group.episodeCount }} 集</p></div><button v-if="draftID && isCurrent" type="button" class="numbering-text-button" :disabled="previewLoading || saving || conflict" @click="checkUpstream">查看 TMDB 更新</button></div>
              <div ref="previewScroll" class="numbering-preview-content numbering-scroll" tabindex="0" aria-label="分集编排内容">
              <p v-if="previewLoading" role="status" class="numbering-muted py-8 text-center text-xs">正在读取 TMDB 分集…</p>
              <p v-if="previewError" role="alert" class="numbering-error">{{ previewError }}<button type="button" class="ml-2 underline" :disabled="previewLoading || saving || conflict" @click="checkUpstream">重试</button></p>
              <template v-if="draftID && preview && !previewLoading">
                <p v-if="!preview.fingerprint" class="numbering-muted mb-3 text-xs leading-5">以下为已保存编排。点击“查看 TMDB 更新”可预览上游变化。</p>
                <div v-else-if="isCurrent" class="numbering-update" role="status">
                  <template v-if="!preview.changed">当前编排与 TMDB 一致。</template>
                  <template v-else>TMDB 资料有变化，保存后才会更新已选方案。<template v-if="changes">新增 {{ changes.added.length }} 集，移除 {{ changes.removed.length }} 集，调整 {{ changes.changed.length }} 集。</template></template>
                  <details v-if="changes && (changes.added.length || changes.removed.length || changes.changed.length)" class="mt-2"><summary class="cursor-pointer">查看变化</summary><ul class="mt-2 space-y-1"><li v-for="(line, index) in [...changes.added, ...changes.removed, ...changes.changed].slice(0, 100)" :key="index">{{ line }}</li></ul></details>
                </div>
                <p v-if="emptyPreview" role="status" class="numbering-warning">该方案详情无有效分集数据，无法应用。</p>
                <p v-if="preview.group.description" class="numbering-muted mb-3 whitespace-pre-line break-words text-xs leading-5">{{ preview.group.description }}</p>
                <details v-for="season in preview.group.seasons" :key="season.number" class="numbering-season" :open="preview.group.seasons.length === 1">
                  <summary class="numbering-season-summary"><svg aria-hidden="true" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6"><path d="m7 4 6 6-6 6" /></svg><span class="min-w-0 flex-1 break-words">{{ season.name || `第 ${season.number} 季` }}</span><span class="numbering-muted shrink-0 font-normal">{{ season.episodes.length }} 集</span></summary>
                  <ol class="numbering-episodes">
                    <li v-for="episode in season.episodes.slice(0, limits[season.number] || 50)" :key="episode.id" class="break-words py-2 text-xs leading-5"><span class="mr-2 font-medium tabular-nums">{{ episodeCode(season.number, episode.number) }}</span>{{ episode.name || '暂无标题' }}<span v-if="episode.sourceNumber > 0" class="numbering-muted block text-[11px]">TMDB 原编号 {{ episodeCode(episode.sourceSeason, episode.sourceNumber) }}</span></li>
                  </ol>
                  <button v-if="season.episodes.length > (limits[season.number] || 50)" type="button" class="numbering-text-button mb-3" @click="limits[season.number] = (limits[season.number] || 50) + 100">显示更多分集</button>
                </details>
              </template>
              <template v-else-if="!draftID">
                <p class="numbering-muted mb-3 text-xs leading-5">使用 TMDB 默认季集，后续随常规资料刷新更新。</p>
                <ul><li v-for="season in choices.defaultSeasons" :key="season.number" class="numbering-season flex items-center justify-between gap-3 py-3 text-xs"><span>{{ season.name || `第 ${season.number} 季` }}</span><span class="numbering-muted shrink-0">{{ season.episodeCount }} 集</span></li></ul>
              </template>
              </div>
            </section>
          </div>
      </div>
      <div v-else class="min-h-0 flex-1"></div>
      <footer class="numbering-footer"><p :id="statusID" role="status" class="numbering-muted numbering-footer-status">{{ statusMessage }}</p><div class="numbering-footer-actions"><button type="button" class="numbering-secondary" :disabled="saving" @click="close">取消</button><button v-if="isMobile && choices && mobilePanel === 'choices'" type="button" class="numbering-secondary" :disabled="saving" @click="switchPanel('preview')">查看预览</button><button type="button" class="numbering-primary" :aria-describedby="statusID" :disabled="!canSave" @click="save">{{ actionLabel }}</button></div></footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.numbering-dialog { @apply m-auto flex w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-2xl p-0; max-width: 920px; height: min(760px, calc(100dvh - 2rem)); max-height: calc(100dvh - 2rem); color: var(--search-dialog-text); --floating-surface-glass: rgb(248 250 252 / 82%); --numbering-well: rgb(241 245 249 / 55%); }
.dark .numbering-dialog { --floating-surface-glass: rgb(18 22 30 / 84%); --numbering-well: rgb(10 14 20 / 45%); }
.numbering-header { @apply flex shrink-0 items-center justify-between gap-4 px-5 py-4; border-bottom: 1px solid var(--search-dialog-card-border); }
.numbering-muted { color: var(--search-dialog-muted); }
.numbering-close { @apply shrink-0 rounded-lg p-2 disabled:opacity-40; color: var(--search-dialog-muted); }
.numbering-close:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }
.numbering-close svg { @apply h-5 w-5; }
.numbering-context { @apply shrink-0 space-y-2 px-5 pt-3; max-height: 30%; overflow-y: auto; }
.numbering-current { @apply rounded-lg px-3 py-2 text-xs leading-5; background: var(--search-dialog-card); overflow-wrap: anywhere; }
.numbering-saved-at { @apply ml-2 inline-block text-[11px]; }
.numbering-body { @apply flex min-h-0 flex-1 flex-col overflow-hidden px-5 pb-4 pt-3; }
.numbering-grid { @apply grid min-h-0 flex-1 gap-4 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]; }
.numbering-choice-panel, .numbering-preview { @apply flex min-h-0 min-w-0 flex-col; }
.numbering-choice-panel[hidden], .numbering-preview[hidden] { display: none; }
.numbering-panel-heading { @apply flex shrink-0 items-center justify-between gap-2 px-1 pb-3 text-xs font-semibold; }
.numbering-scroll { @apply min-h-0 overflow-y-auto; overscroll-behavior: contain; scrollbar-gutter: stable; scrollbar-width: thin; -webkit-overflow-scrolling: touch; }
.numbering-options { @apply m-0 min-w-0 flex-1 space-y-2 border-0 p-1 pt-0; }
.numbering-option { @apply flex cursor-pointer items-start gap-3 rounded-xl border p-3 transition-colors; background: var(--search-dialog-card); border-color: var(--search-dialog-card-border); overflow-wrap: anywhere; }
.numbering-option:hover { background: var(--search-dialog-secondary-hover); }
.numbering-option.selected { border-color: var(--search-dialog-primary); background: var(--search-dialog-tonal); box-shadow: inset 0 0 0 .5px var(--search-dialog-primary); }
.numbering-option input { @apply mt-0.5 h-4 w-4 shrink-0; accent-color: var(--search-dialog-primary); }
.numbering-option:has(input:focus-visible) { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
.numbering-option input:focus-visible { outline: none; }
.numbering-options:disabled .numbering-option { @apply cursor-not-allowed opacity-60; }
.numbering-current-badge { @apply mt-1 block text-[11px] font-medium; color: var(--search-dialog-primary); }
.numbering-preview { @apply overflow-hidden rounded-xl border; background: var(--numbering-well); border-color: var(--search-dialog-card-border); }
.numbering-preview-heading { @apply flex shrink-0 flex-wrap items-start justify-between gap-2 px-4 py-3; border-bottom: 1px solid var(--search-dialog-card-border); }
.numbering-preview-content { @apply flex-1 px-4 py-3; }
.numbering-season { border-bottom: 1px solid var(--search-dialog-card-border); }
.numbering-season-summary { @apply flex min-h-11 cursor-pointer items-center gap-2 py-3 text-xs font-semibold; list-style: none; }
.numbering-season-summary::-webkit-details-marker { display: none; }
.numbering-season-summary svg { @apply h-3.5 w-3.5 shrink-0; transition: transform .15s; }
.numbering-season[open] > summary svg { transform: rotate(90deg); }
.numbering-episodes { @apply pl-5; }
.numbering-episodes > li + li { border-top: 1px solid var(--search-dialog-card-border); }
.numbering-error { @apply rounded-lg bg-red-50/90 p-3 text-xs leading-5 text-red-700 dark:bg-red-950/50 dark:text-red-200; overflow-wrap: anywhere; }
.numbering-warning { @apply rounded-lg bg-amber-50/90 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/50 dark:text-amber-200; }
.numbering-update { @apply mb-3 rounded-lg p-3 text-xs leading-5; background: var(--search-dialog-tonal); color: var(--search-dialog-on-tonal); }
.numbering-text-button { @apply min-h-9 shrink-0 text-left text-xs font-medium disabled:opacity-40; color: var(--search-dialog-primary); }
.numbering-text-button:hover:not(:disabled) { text-decoration: underline; text-underline-offset: 3px; }
.numbering-footer { @apply flex shrink-0 items-center justify-between gap-4 px-5 py-3; border-top: 1px solid var(--search-dialog-card-border); padding-bottom: max(.75rem, env(safe-area-inset-bottom)); }
.numbering-footer-status { @apply min-w-0 text-[11px] leading-5; }
.numbering-footer-actions { @apply flex shrink-0 items-center justify-end gap-2; }
.numbering-primary, .numbering-secondary { @apply min-h-10 rounded-full px-4 py-2 text-xs font-medium transition-colors disabled:cursor-not-allowed; }
.numbering-primary { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }
.numbering-primary:hover:not(:disabled) { background: var(--search-dialog-action-hover); }
.numbering-primary:disabled { background: var(--search-dialog-secondary-background); color: var(--search-dialog-muted); opacity: .6; }
.numbering-secondary { color: var(--search-dialog-muted); background: var(--search-dialog-secondary-background); }
.numbering-secondary:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }
.numbering-secondary:disabled { opacity: .5; }
.numbering-dialog :is(button, summary, .numbering-preview-content):focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: -2px; }
.numbering-tabs { @apply mb-3 grid shrink-0 grid-cols-2 rounded-xl p-1; background: var(--search-dialog-secondary-background); }
.numbering-tabs button { @apply min-h-10 rounded-lg px-3 py-2 text-xs font-medium; color: var(--search-dialog-muted); }
.numbering-tabs button[aria-selected='true'] { background: var(--search-dialog-card); color: var(--search-dialog-text); box-shadow: 0 1px 3px rgb(15 23 42 / 8%); }
@media (max-width: 639px) {
  .numbering-header { @apply px-4 py-3; }
  .numbering-context { @apply px-4; }
  .numbering-body { @apply px-4 pb-3; }
  .numbering-panel-heading { @apply pb-2; }
  .numbering-footer { @apply flex-col items-stretch gap-2 px-4; }
  .numbering-footer-actions { @apply w-full; }
  .numbering-primary, .numbering-secondary { @apply min-h-11 px-3; }
}
@media (prefers-reduced-motion: reduce) {
  .numbering-option, .numbering-season-summary svg, .numbering-primary, .numbering-secondary { transition: none; }
}
</style>
