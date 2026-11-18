<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'
import { useMobileSheet } from '../composables/useMobileSheet'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'
import type { DashboardGenre, DashboardKind } from '../utils/dashboardPreferences'
import DashboardYearPicker from './DashboardYearPicker.vue'

interface Option { id: string; name: string }
interface FilterValues { genre: string; region: string; year: string; sort: string }
const props = defineProps<FilterValues & { kind: DashboardKind; recentYears: string[]; genreError?: boolean; genres: DashboardGenre[]; regions: Option[]; sorts: Option[] }>()
const emit = defineEmits<{ close: []; apply: [value: FilterValues]; retryGenres: [] }>()
const dialog = ref<HTMLDialogElement>(), heading = ref<HTMLElement>(), olderYearsButton = ref<HTMLButtonElement>()
const titleId = useId(), hintId = useId(), yearsId = useId()
const draftGenre = ref(props.genre), draftRegion = ref(props.region), draftYear = ref(props.year), draftSort = ref(props.sort)
const showOlderYears = ref(false)
const olderYearSelected = computed(() => !!draftYear.value && !props.recentYears.includes(draftYear.value))
const dialogLifecycle = createDialogLifecycle()
let submitted: FilterValues | undefined
let backdropDown = false
const sheet = useMobileSheet(dialog, {
  enabled: () => true,
  closeDisabled: () => false,
  onClose() {
    if (submitted) emit('apply', submitted)
    else emit('close')
  },
})
function reset() { draftGenre.value = ''; draftRegion.value = ''; draftYear.value = ''; draftSort.value = 'popular'; showOlderYears.value = false }
function selectYear(value: string) {
  draftYear.value = value
  showOlderYears.value = false
  olderYearsButton.value?.focus({ preventScroll: true })
}
function apply() {
  if (submitted) return
  if (draftGenre.value !== props.genre || draftRegion.value !== props.region || draftYear.value !== props.year || draftSort.value !== props.sort) {
    submitted = { genre: draftGenre.value, region: draftRegion.value, year: draftYear.value, sort: draftSort.value }
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
          <div class="catalog-sheet-handle" aria-hidden="true"></div>
          <div class="catalog-dialog-title">
            <div><h2 :id="titleId" ref="heading" tabindex="-1">{{ kind === 'movie' ? '电影' : '剧集' }} · 筛选与排序</h2><p :id="hintId">电影和剧集各自保留筛选条件</p></div>
            <div class="catalog-dialog-tools">
              <button type="button" class="catalog-text-button catalog-dialog-reset" @click="reset">清空</button>
              <button type="button" class="catalog-text-button sr-only focus:not-sr-only" @click="sheet.close()">取消</button>
            </div>
          </div>
        </header>
        <div class="catalog-dialog-body" data-auto-scrollbar>
          <fieldset><legend>排序方式</legend><div class="catalog-dialog-options">
            <button v-for="item in sorts" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftSort === item.id" @click="draftSort = item.id">{{ item.name }}</button>
          </div><p v-if="draftSort === 'rating'" class="catalog-filter-note">评分排序至少需要 100 人评价</p></fieldset>
          <fieldset><legend>题材类型</legend><div class="catalog-dialog-options">
            <button type="button" class="catalog-chip" :aria-pressed="draftGenre === ''" @click="draftGenre = ''">全部题材</button>
            <button v-for="item in genres" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftGenre === String(item.id)" @click="draftGenre = String(item.id)">{{ item.name }}</button>
          </div><p v-if="genreError" class="catalog-filter-note">题材加载失败。<button type="button" @click="emit('retryGenres')">重试</button></p></fieldset>
          <fieldset><legend>制片地区</legend><div class="catalog-dialog-options">
            <button v-for="item in regions" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftRegion === item.id" @click="draftRegion = item.id">{{ item.id ? item.name : '全部地区' }}</button>
          </div></fieldset>
          <fieldset><legend>{{ kind === 'movie' ? '上映年份' : '首播年份' }}</legend><div class="catalog-dialog-options">
            <button type="button" class="catalog-chip" :aria-pressed="draftYear === ''" @click="draftYear = ''">全部年份</button>
            <button v-for="value in recentYears" :key="value" type="button" class="catalog-chip" :aria-pressed="draftYear === value" @click="draftYear = value">{{ value }}</button>
            <button type="button" ref="olderYearsButton" class="catalog-chip catalog-older-years" :class="{ 'is-selected': olderYearSelected }" :aria-expanded="showOlderYears" :aria-controls="yearsId" @click="showOlderYears = !showOlderYears">{{ olderYearSelected ? draftYear + ' 年' : '更多年份' }}<IconChevronDown aria-hidden="true" /></button>
          </div>
          <div v-show="showOlderYears" :id="yearsId" class="catalog-year-options"><DashboardYearPicker v-if="showOlderYears" :model-value="draftYear" @update:model-value="selectYear" /></div></fieldset>
        </div>
        <footer class="catalog-dialog-footer"><button type="submit" class="catalog-apply-button">查看结果</button></footer>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.catalog-filter-dialog { --catalog-dialog-gutter: 22px; position: fixed; inset: 0; width: calc(100% - 24px); max-width: 560px; max-height: min(88dvh, 820px); margin: auto; overflow: hidden; padding: 0; border-radius: 28px; color: var(--filter-text); }
.catalog-filter-dialog[open] { display: flex; flex-direction: column; }
.catalog-filter-form { display: flex; min-height: 0; flex-direction: column; }
.catalog-sheet-handle { display: none; }
.catalog-dialog-heading { flex: none; margin-inline: var(--catalog-dialog-gutter); border-bottom: 1px solid var(--glass-edge); }
.catalog-dialog-title { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 12px 0; }
.catalog-dialog-title h2 { margin: 0; font-size: 16px; font-weight: 700; }
.catalog-dialog-title p { margin: 3px 0 0; color: var(--filter-muted); font-size: 12px; line-height: 18px; }
.catalog-dialog-tools { display: flex; flex: none; align-items: center; }
.catalog-dialog-reset { min-height: 44px; padding-inline: 8px; font-size: 12px; }
.catalog-dialog-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; padding: 12px var(--catalog-dialog-gutter); overscroll-behavior: contain; }
.catalog-dialog-body fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
.catalog-dialog-body fieldset + fieldset { margin-top: 14px; }
.catalog-dialog-body legend { margin-bottom: 4px; padding: 0; color: var(--filter-muted); font-size: 12px; font-weight: 650; }
.catalog-dialog-options { display: flex; flex-wrap: wrap; column-gap: 6px; }
.catalog-dialog-options .catalog-chip { position: relative; isolation: isolate; padding: 0 12px; border-radius: 12px; background: transparent; font-size: 12px; }
.catalog-dialog-options .catalog-chip::before { position: absolute; z-index: -1; inset: 3px 0; border: 1px solid transparent; border-radius: inherit; background: rgb(var(--content-primary) / .075); content: ''; }
.catalog-dialog-options .catalog-chip:is([aria-pressed=true], .is-selected) { color: rgb(var(--selection-content)); }
.catalog-dialog-options .catalog-chip:is([aria-pressed=true], .is-selected)::before { border-color: rgb(var(--action-primary) / .3); background: rgb(var(--selection-surface)); }
.catalog-dialog-options .catalog-chip:active { opacity: .75; }
.catalog-older-years svg { width: 13px; height: 13px; }
.catalog-older-years[aria-expanded=true] svg { transform: rotate(180deg); }
.catalog-year-options { margin-top: 6px; }
.catalog-filter-note { margin: 5px 0 0; color: var(--filter-muted); font-size: 11px; line-height: 18px; }
.catalog-filter-note button { min-height: 32px; padding-inline: 8px; text-decoration: underline; }
.catalog-dialog-footer { flex: none; margin-inline: var(--catalog-dialog-gutter); padding: 12px 0 16px; border-top: 1px solid var(--glass-edge); }
.catalog-apply-button { width: 100%; min-height: 46px; border: 0; border-radius: 16px; background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); font-size: 14px; font-weight: 650; }
.catalog-apply-button:hover { background: rgb(var(--action-primary-hover)); }
.catalog-apply-button:active { opacity: .88; }
@media (max-width: 639px) {
  .catalog-filter-dialog { --catalog-dialog-gutter: 20px; inset: auto 0 var(--sheet-viewport-bottom, 0px); width: 100%; max-width: none; max-height: calc(var(--sheet-viewport-height, 100dvh) - max(32px, calc(var(--app-safe-top) + 8px))); margin: 0; border-width: 1px 0 0; border-radius: 28px 28px 0 0; background: var(--floating-surface-container); backdrop-filter: none; -webkit-backdrop-filter: none; }
  .catalog-filter-dialog::backdrop { background: rgb(0 0 0 / .48); }
  .catalog-dialog-heading { touch-action: none; }
  .catalog-sheet-handle { display: block; width: 36px; height: 4px; margin: 9px auto 0; border-radius: 999px; background: rgb(var(--content-secondary) / .4); }
  .catalog-dialog-title { padding: 5px 0 8px; }
  .catalog-dialog-body fieldset + fieldset { margin-top: 10px; }
  .catalog-dialog-options .catalog-chip { min-height: 40px; }
  .catalog-dialog-options .catalog-chip::before { inset-block: 4px; }
  .catalog-dialog-footer { padding-bottom: max(12px, calc(env(safe-area-inset-bottom) + 8px)); }
}
@media (min-width: 640px) and (prefers-reduced-motion: no-preference) {
  .catalog-filter-dialog[open] { animation: catalog-dialog-enter 180ms cubic-bezier(.2, .8, .2, 1); }
}
@keyframes catalog-dialog-enter { from { opacity: 0; transform: translateY(10px) scale(.985); } to { opacity: 1; transform: none; } }
</style>
