<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import DashboardYearPicker from './DashboardYearPicker.vue'
import type { DashboardGenre } from '../utils/dashboardPreferences'

interface Option { id: string; name: string }
interface FilterValues { genre: string; region: string; year: string; sort: string }
const props = defineProps<{ genres: DashboardGenre[]; regions: Option[]; sorts: Option[]; genre: string; region: string; year: string; sort: string }>()
const emit = defineEmits<{ close: []; apply: [value: FilterValues] }>()
const dialog = ref<HTMLDialogElement>()
const draftGenre = ref(props.genre)
const draftRegion = ref(props.region)
const draftYear = ref(props.year)
const draftSort = ref(props.sort)
const changed = computed(() => draftGenre.value !== props.genre || draftRegion.value !== props.region || draftYear.value !== props.year || draftSort.value !== props.sort)
const summary = computed(() => [props.genres.find(item => String(item.id) === draftGenre.value)?.name, props.regions.find(item => item.id === draftRegion.value && item.id)?.name, draftYear.value ? draftYear.value + ' 年' : '', draftSort.value !== 'popular' ? props.sorts.find(item => item.id === draftSort.value)?.name : ''].filter(Boolean).join(' · '))
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
function close() { emit('close') }
function reset() { draftGenre.value = ''; draftRegion.value = ''; draftYear.value = ''; draftSort.value = 'popular' }
function apply() {
  if (!changed.value) { close(); return }
  emit('apply', { genre: draftGenre.value, region: draftRegion.value, year: draftYear.value, sort: draftSort.value })
}
function backdropClick(event: MouseEvent) {
  if (event.target !== dialog.value) return
  const rect = dialog.value.getBoundingClientRect()
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) close()
}
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal() })
onBeforeUnmount(() => { dialog.value?.close(); document.body.style.overflow = previousOverflow; if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true }) })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="catalog-filter-dialog catalog-filter-theme" aria-labelledby="dashboard-filter-title" aria-describedby="dashboard-filter-summary" @cancel.prevent="close" @click="backdropClick">
      <form class="catalog-filter-form" @submit.prevent="apply">
        <header class="catalog-dialog-heading">
          <div><h2 id="dashboard-filter-title">筛选影视</h2><p id="dashboard-filter-summary">{{ summary || '选择题材、地区与年份' }}</p></div>
          <button type="button" autofocus class="catalog-icon-button" aria-label="关闭筛选" @click="close"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        </header>
        <div class="catalog-dialog-body">
          <fieldset><legend>题材</legend><div class="catalog-dialog-options">
            <button type="button" class="catalog-chip" :aria-pressed="draftGenre === ''" @click="draftGenre = ''">全部</button>
            <button v-for="item in genres" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftGenre === String(item.id)" @click="draftGenre = String(item.id)">{{ item.name }}</button>
          </div></fieldset>
          <fieldset><legend>地区</legend><div class="catalog-region-grid">
            <button v-for="item in regions" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftRegion === item.id" @click="draftRegion = item.id">{{ item.name }}</button>
          </div></fieldset>
          <fieldset><legend>年份</legend><DashboardYearPicker v-model="draftYear" /></fieldset>
          <fieldset><legend>排序</legend><div class="catalog-sort-grid">
            <button v-for="item in sorts" :key="item.id" type="button" class="catalog-chip" :aria-pressed="draftSort === item.id" @click="draftSort = item.id">{{ item.name }}</button>
          </div><p v-if="draftSort === 'rating'" class="catalog-rating-note">评分排序至少需要 100 人评价</p></fieldset>
        </div>
        <footer class="catalog-dialog-footer"><button type="button" class="catalog-text-button" @click="reset">重置</button><button type="submit" class="catalog-apply-button">{{ changed ? '应用筛选' : '完成' }}</button></footer>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.catalog-filter-dialog { position: fixed; inset: 0; width: calc(100% - 24px); max-width: 560px; max-height: min(88dvh, 820px); margin: auto; overflow: hidden; padding: 0; border: 1px solid var(--filter-outline); border-radius: 28px; background: var(--filter-surface); color: var(--filter-text); box-shadow: 0 24px 80px rgb(0 0 0 / 20%); }
.catalog-filter-dialog::backdrop { background: rgb(15 20 28 / 32%); backdrop-filter: blur(3px); }
.catalog-filter-dialog[open] { animation: catalog-dialog-enter 220ms cubic-bezier(0.2, 0, 0, 1); }
.catalog-filter-form { display: flex; max-height: min(calc(88dvh - 2px), 818px); flex-direction: column; }
.catalog-dialog-heading { display: flex; align-items: center; justify-content: space-between; flex-shrink: 0; gap: 12px; padding: 18px 16px 16px 22px; }
.catalog-dialog-heading > div { min-width: 0; }
.catalog-dialog-heading h2 { margin: 0; font-size: 18px; font-weight: 650; }
.catalog-dialog-heading p { margin: 5px 0 0; color: var(--filter-muted); font-size: 12px; line-height: 20px; overflow-wrap: anywhere; }
.catalog-dialog-heading svg { width: 20px; height: 20px; }
.catalog-dialog-body { min-height: 0; flex: 1; overflow-y: auto; padding: 0 22px 8px; overscroll-behavior: contain; }
.catalog-dialog-body fieldset { min-width: 0; margin: 0 0 22px; padding: 0; border: 0; }
.catalog-dialog-body legend { margin-bottom: 10px; padding: 0; font-size: 13px; font-weight: 600; }
.catalog-dialog-options { display: flex; flex-wrap: wrap; gap: 5px; }
.catalog-dialog-options .catalog-chip { padding: 0 12px; }
.catalog-region-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 5px; }
.catalog-sort-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 5px; }
.catalog-sort-grid .catalog-chip { padding: 0 4px; font-size: 12px; }
.catalog-rating-note { margin: 8px 0 0; color: var(--filter-muted); font-size: 11px; line-height: 18px; }
.catalog-dialog-footer { display: flex; flex-shrink: 0; align-items: center; gap: 12px; padding: 14px 22px max(14px, env(safe-area-inset-bottom)); border-top: 1px solid var(--filter-outline); background: var(--filter-surface); }
.catalog-dialog-footer .catalog-text-button { min-width: 70px; min-height: 48px; }
.catalog-apply-button { flex: 1; min-height: 48px; border: 0; border-radius: 16px; background: var(--filter-tone); color: var(--filter-on-tone); font-size: 14px; font-weight: 600; }
.catalog-apply-button:active { filter: brightness(0.96); }
@keyframes catalog-dialog-enter { from { opacity: 0; transform: translateY(14px) scale(0.98); } to { opacity: 1; transform: translateY(0) scale(1); } }
@media (max-height: 500px) { .catalog-filter-dialog { max-height: calc(100dvh - 24px); } .catalog-filter-form { max-height: calc(100dvh - 26px); } }
@media (prefers-reduced-motion: reduce) { .catalog-filter-dialog[open] { animation: none; } }
</style>
