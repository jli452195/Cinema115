<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconChevronDown, IconX } from '@tabler/icons-vue'
import { useMobileSheet } from '../composables/useMobileSheet'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
import { dashboardCategoryOptions, dashboardKindOptions, dashboardRegions as regions, dashboardSorts as sorts, defaultDashboardRefinements, isDashboardGenreCompatible, type DashboardFilters } from '../utils/dashboardFilters'
import type { DashboardGenre, DashboardKind } from '../utils/dashboardPreferences'
import DashboardYearPicker from './DashboardYearPicker.vue'
import SegmentedControl from './SegmentedControl.vue'

type FilterSelection = DashboardFilters & { kind: DashboardKind }
const props = defineProps<{
  kind: DashboardKind
  initialFilters: Record<DashboardKind, DashboardFilters>
  recentYears: string[]
  genresByKind: Record<DashboardKind, DashboardGenre[]>
  genreErrors: Record<DashboardKind, boolean>
}>()
const emit = defineEmits<{ close: []; apply: [value: FilterSelection]; retryGenres: [kind: DashboardKind] }>()
const dialog = ref<HTMLDialogElement>(), heading = ref<HTMLElement>(), olderYearsButton = ref<HTMLButtonElement>()
const body = ref<HTMLElement>(), yearPanel = ref<HTMLElement>()
const titleId = useId(), hintId = useId(), libraryLabelId = useId(), categoryHintId = useId(), yearsId = useId()
const kindLabels: Record<DashboardKind, string> = { movie: '电影库', tv: '剧集库' }
// 片库上下文仅在筛选弹窗内补充“库”字，避免与电影细分分类重名。
const kindOptions = dashboardKindOptions.map(item => ({ ...item, label: kindLabels[item.value] }))
const initialKind = props.kind
const initialSelection = { ...props.initialFilters[initialKind] }
const drafts = ref({ movie: { ...props.initialFilters.movie }, tv: { ...props.initialFilters.tv } })
const draftKind = ref(initialKind)
const draft = computed(() => drafts.value[draftKind.value])
// 保留共享分类常量，仅在弹窗内将 film 显示为“常规电影”。
const categoryOptions = computed(() =>
  dashboardCategoryOptions[draftKind.value].map(item => (item.value === 'film' ? { ...item, label: '常规电影' } : item))
)
const showFilmHint = computed(() => draftKind.value === 'movie' && draft.value.category === 'film')
const genres = computed(() => props.genresByKind[draftKind.value].filter(item => isDashboardGenreCompatible(draft.value.category, item.id)))
const genreOptions = computed(() => [{ value: '', label: '全部题材' }, ...genres.value.map(item => ({ value: String(item.id), label: item.name }))])
const sortOptions = sorts.map(item => ({ value: item.id, label: item.name }))
const regionOptions = regions.map(item => ({ value: item.id, label: item.id ? item.name : '全部地区' }))
const yearOptions = computed(() => [{ value: '', label: '全部年份' }, ...props.recentYears.map(value => ({ value, label: value }))])
const showOlderYears = ref(false)
const olderYearSelected = computed(() => !!draft.value.year && !props.recentYears.includes(draft.value.year))
const dialogLifecycle = createDialogLifecycle()
let submitted: FilterSelection | undefined
let backdropDown = false
const sheet = useMobileSheet(dialog, {
  enabled: () => true,
  closeDisabled: () => false,
  onClose() {
    if (submitted) emit('apply', submitted)
    else emit('close')
  },
})
function selectKind(value: DashboardKind) {
  if (value === draftKind.value) return
  draftKind.value = value
  showOlderYears.value = false
}
function selectCategory(value: string) {
  draft.value.category = value
  if (!isDashboardGenreCompatible(value, Number(draft.value.genre))) draft.value.genre = ''
}
// 仅重置排序、题材、地区与年份，保留当前片库与细分分类。
function reset() {
  Object.assign(draft.value, defaultDashboardRefinements)
  showOlderYears.value = false
}
async function selectYear(value: string) {
  draft.value.year = value
  showOlderYears.value = false
  await nextTick()
  const selected = olderYearsButton.value?.parentElement?.querySelector<HTMLButtonElement>('button[aria-pressed="true"]')
  const target = selected ?? olderYearsButton.value
  target?.focus({ preventScroll: true })
}
async function toggleOlderYears() {
  showOlderYears.value = !showOlderYears.value
  if (!showOlderYears.value) return
  await nextTick()
  if (!body.value || !yearPanel.value) return
  // 只在正文里露出年份面板，不让页面或弹层外壳跟着移动。
  const overflow = yearPanel.value.getBoundingClientRect().bottom - body.value.getBoundingClientRect().bottom + 12
  if (overflow > 0) body.value.scrollBy({ top: overflow, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })
}
function apply() {
  if (submitted) return
  const keys = Object.keys(initialSelection) as (keyof DashboardFilters)[]
  if (draftKind.value !== initialKind || keys.some(key => draft.value[key] !== initialSelection[key])) {
    submitted = { kind: draftKind.value, ...draft.value }
  }
  sheet.close()
}
function isBackdrop(event: MouseEvent) {
  if (event.target !== dialog.value) return false
  const rect = dialog.value.getBoundingClientRect()
  return event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom
}
function backdropClick(event: MouseEvent) {
  if (backdropDown && isBackdrop(event)) sheet.close()
  backdropDown = false
}
onMounted(() => {
  dialogLifecycle.acquire()
  dialog.value?.showModal()
  heading.value?.focus({ preventScroll: true })
  sheet.open()
})
onBeforeUnmount(() => { dialog.value?.close(); dialogLifecycle.release() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="catalog-filter-dialog catalog-filter-theme floating-surface" :aria-labelledby="titleId" :aria-describedby="hintId" @cancel.prevent="sheet.close()" @pointerdown="backdropDown = isBackdrop($event)" @click="backdropClick">
      <form class="catalog-filter-form" @submit.prevent="apply">
        <header class="catalog-dialog-heading" @pointerdown="sheet.onPointerDown" @pointermove="sheet.onPointerMove" @pointerup="sheet.onPointerUp" @pointercancel="sheet.onPointerCancel" @lostpointercapture="sheet.onPointerCancel">
          <div class="catalog-dialog-title">
            <h2 :id="titleId" ref="heading" tabindex="-1">筛选</h2>
            <button type="button" class="catalog-dialog-close" aria-label="关闭筛选" @click="sheet.close()"><IconX :size="20" aria-hidden="true" /></button>
          </div>
          <p :id="hintId" class="sr-only">确定后应用筛选；关闭放弃未提交的条件。重置仅清除排序、题材、地区与年份。</p>
        </header>
        <div ref="body" class="catalog-dialog-body" data-auto-scrollbar>
          <fieldset>
            <legend :id="libraryLabelId">当前片库</legend>
            <SegmentedControl :model-value="draftKind" :options="kindOptions" label="当前片库" appearance="filter-row" @update:model-value="selectKind" />
          </fieldset>
          <fieldset>
            <legend class="catalog-section-legend">
              <span>细分分类</span>
              <span v-if="showFilmHint" :id="categoryHintId" class="catalog-legend-note">不含动画和纪录片</span>
            </legend>
            <SegmentedControl :model-value="draft.category" :options="categoryOptions" label="细分分类" appearance="filter-row" :aria-describedby="showFilmHint ? categoryHintId : undefined" @update:model-value="selectCategory" />
          </fieldset>
          <fieldset>
            <legend>排序方式</legend>
            <SegmentedControl v-model="draft.sort" :options="sortOptions" label="排序方式" appearance="filter-row" />
            <p v-if="draft.sort === 'rating'" class="catalog-filter-note">评分排序至少需要 100 人评价</p>
          </fieldset>
          <fieldset>
            <legend>题材类型</legend>
            <SegmentedControl v-model="draft.genre" :options="genreOptions" label="题材类型" appearance="filter-row" />
            <p v-if="genreErrors[draftKind]" class="catalog-filter-note">暂时无法更新题材，已显示本地选项。<button type="button" @click="emit('retryGenres', draftKind)">重试</button></p>
          </fieldset>
          <fieldset>
            <legend>制片地区</legend>
            <SegmentedControl v-model="draft.region" :options="regionOptions" label="制片地区" appearance="filter-row" />
          </fieldset>
          <fieldset>
            <legend>{{ draftKind === 'movie' ? '上映年份' : '首播年份' }}</legend>
            <SegmentedControl v-model="draft.year" :options="yearOptions" label="年份" appearance="filter-row">
              <template #after>
                <button ref="olderYearsButton" type="button" class="filter-chip catalog-older-years" :class="{ 'is-selected': olderYearSelected }" :aria-expanded="showOlderYears" :aria-controls="yearsId" @click="toggleOlderYears">{{ olderYearSelected ? draft.year + ' 年' : '更多年份' }}<IconChevronDown aria-hidden="true" /></button>
              </template>
            </SegmentedControl>
            <div v-show="showOlderYears" :id="yearsId" ref="yearPanel" class="catalog-year-options"><DashboardYearPicker v-if="showOlderYears" :model-value="draft.year" @update:model-value="selectYear" /></div>
          </fieldset>
        </div>
        <footer class="catalog-dialog-footer">
          <button type="button" class="catalog-reset-button" :aria-describedby="hintId" @click="reset">重置</button>
          <button type="submit" class="catalog-apply-button">确定</button>
        </footer>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.catalog-filter-dialog {
  --catalog-dialog-gutter: 18px;
  --filter-row-gutter: var(--catalog-dialog-gutter);
  box-sizing: border-box;
  position: fixed;
  inset: 0;
  width: calc(100% - 24px);
  max-width: 560px;
  max-height: min(88dvh, 820px);
  margin: auto;
  padding: 0;
  overflow: hidden;
  border: 0;
  border-radius: 22px;
  background: rgb(var(--surface-card));
  color: rgb(var(--content-primary));
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
}
.catalog-filter-dialog[open] { display: flex; flex-direction: column; }
.catalog-filter-form { display: flex; flex: 1 1 auto; min-width: 0; min-height: 0; flex-direction: column; }
.catalog-dialog-heading { flex: none; }
.catalog-dialog-title { position: relative; display: flex; min-height: 54px; align-items: center; justify-content: center; padding-inline: 60px; }
.catalog-dialog-title h2 { margin: 0; font-size: 16px; font-weight: 700; }
.catalog-dialog-close { position: absolute; inset-inline-end: 10px; top: 5px; display: grid; width: 44px; height: 44px; place-items: center; border: 0; border-radius: 50%; background: transparent; color: inherit; }
.catalog-dialog-close:active { background: rgb(var(--content-primary) / .06); }
.catalog-dialog-body { flex: 1 1 auto; min-height: 0; overflow-x: hidden; overflow-y: auto; padding: 4px 0 20px; overscroll-behavior-y: contain; }
.catalog-dialog-body fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
.catalog-dialog-body fieldset + fieldset { margin-top: 16px; }
.catalog-dialog-body legend { margin: 0 var(--catalog-dialog-gutter) 4px; padding: 0; color: rgb(var(--content-secondary)); font-size: 13px; font-weight: 500; line-height: 20px; }
.catalog-section-legend { display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px 8px; }
.catalog-legend-note { font-size: 11px; font-weight: 400; }
.catalog-older-years svg { width: 14px; height: 14px; transition: transform 160ms ease; }
.catalog-older-years[aria-expanded=true] svg { transform: rotate(180deg); }
.catalog-year-options { margin: 10px var(--catalog-dialog-gutter) 0; padding: 0 12px 8px; border-radius: 12px; background: rgb(var(--surface-hover)); }
.catalog-filter-note { margin: 4px var(--catalog-dialog-gutter) 0; color: rgb(var(--content-secondary)); font-size: 11px; line-height: 18px; }
.catalog-filter-note button { min-height: 32px; padding-inline: 8px; color: rgb(var(--selection-content)); text-decoration: underline; }
.catalog-dialog-footer { display: grid; flex: none; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding: 10px 16px 20px; }
.catalog-dialog-footer > button { min-height: 46px; border: 0; border-radius: 12px; font-size: 15px; font-weight: 600; }
.catalog-reset-button { background: rgb(var(--surface-hover)); color: rgb(var(--content-secondary)); }
.catalog-apply-button { background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); }
.catalog-dialog-footer > button:active { opacity: .85; }
@media (hover: hover) {
  .catalog-dialog-close:hover { background: rgb(var(--content-primary) / .06); }
  .catalog-reset-button:hover { color: rgb(var(--content-primary)); }
  .catalog-apply-button:hover { background: rgb(var(--action-primary-hover)); }
}
@media (max-width: 639px) {
  .catalog-filter-dialog {
    --sheet-top-gap: max(32px, calc(var(--app-safe-top, 0px) + 8px));
    --sheet-available: max(0px, calc(var(--sheet-viewport-height, 100dvh) - var(--sheet-top-gap)));
    inset: auto 0 var(--sheet-viewport-bottom, 0px);
    width: 100%;
    max-width: none;
    /* 按方案固定为视口的 85%，切库和展开年份只改变正文滚动范围。 */
    height: min(calc(var(--sheet-viewport-height, 100dvh) * .85), var(--sheet-available));
    max-height: var(--sheet-available);
    margin: 0;
    padding-inline: var(--app-safe-left, 0px) var(--app-safe-right, 0px);
    border-radius: 22px 22px 0 0;
    box-shadow: 0 -4px 24px rgb(0 0 0 / .14);
  }
  .catalog-filter-dialog::backdrop { background: rgb(15 23 42 / .45); }
  .catalog-dialog-heading { touch-action: none; }
  .catalog-dialog-footer { padding-bottom: max(20px, calc(var(--app-safe-bottom, 0px) - var(--sheet-viewport-bottom, 0px) + 12px)); }
}
.dark .catalog-filter-dialog::backdrop { background: rgb(0 0 0 / .62); }
@media (min-width: 640px) and (prefers-reduced-motion: no-preference) {
  .catalog-filter-dialog[open] { animation: catalog-dialog-enter 180ms cubic-bezier(.2, .8, .2, 1); }
}
@media (prefers-reduced-motion: reduce) { .catalog-older-years svg { transition: none; } }
@keyframes catalog-dialog-enter { from { opacity: 0; transform: translateY(10px) scale(.985); } to { opacity: 1; transform: none; } }
</style>
