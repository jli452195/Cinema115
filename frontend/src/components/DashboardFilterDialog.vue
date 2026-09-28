<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { DashboardGenre } from '../utils/dashboardPreferences'

interface Option { id: string; name: string }
interface FilterValues { genre: string; region: string; year: string; sort: string }

const props = defineProps<{
  genres: DashboardGenre[]
  regions: Option[]
  sorts: Option[]
  genre: string
  region: string
  year: string
  sort: string
}>()
const emit = defineEmits<{ close: []; apply: [value: FilterValues] }>()

const dialog = ref<HTMLDialogElement>()
const yearGrid = ref<HTMLElement>()
const draftGenre = ref(props.genre)
const draftRegion = ref(props.region)
const draftYear = ref(props.year)
const draftSort = ref(props.sort)
const currentYear = new Date().getFullYear()
const allYears = Array.from({ length: currentYear - 1899 }, (_, index) => String(currentYear - index))
const decades = Array.from({ length: Math.floor(currentYear / 10) - 189 }, (_, index) => Math.floor(currentYear / 10) * 10 - index * 10)
const changed = computed(() => draftGenre.value !== props.genre || draftRegion.value !== props.region || draftYear.value !== props.year || draftSort.value !== props.sort)
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow

function chip(active: boolean) {
  return active ? 'border-amber-400 bg-amber-50 text-amber-700' : 'border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950 hover:text-slate-900 dark:hover:text-slate-100 dark:text-slate-100'
}
function close() { emit('close') }
function reset() {
  draftGenre.value = ''
  draftRegion.value = ''
  draftYear.value = ''
  draftSort.value = 'popular'
  yearGrid.value?.scrollTo({ top: 0, behavior: 'smooth' })
}
function apply() {
  if (!changed.value) { close(); return }
  emit('apply', { genre: draftGenre.value, region: draftRegion.value, year: draftYear.value, sort: draftSort.value })
}
function scrollToYear(value: string, behavior: ScrollBehavior = 'smooth') {
  const container = yearGrid.value
  const target = container?.querySelector<HTMLElement>(`[data-year="${value}"]`)
  if (!container || !target) return
  container.scrollTo({ top: target.offsetTop - container.clientHeight / 2 + target.clientHeight / 2, behavior })
}

onMounted(() => {
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
  void nextTick(() => { if (draftYear.value) scrollToYear(draftYear.value, 'auto') })
})
onBeforeUnmount(() => {
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="dashboard-filter-title" class="fixed inset-x-0 bottom-0 top-auto m-0 max-h-[92dvh] w-full max-w-none overflow-hidden rounded-t-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-0 text-slate-700 dark:text-slate-300 shadow-2xl backdrop:bg-slate-950/40 sm:inset-0 sm:m-auto sm:max-w-xl sm:rounded-lg" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <form method="dialog" class="flex max-h-[92dvh] flex-col" @submit.prevent="apply">
        <header class="flex shrink-0 items-center justify-between gap-4 border-b border-slate-100 dark:border-slate-800 px-5 py-4">
          <h2 id="dashboard-filter-title" class="font-semibold text-slate-900 dark:text-slate-100">筛选</h2>
          <div class="flex items-center gap-2"><button type="button" class="rounded-md px-2 py-1.5 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 dark:hover:bg-slate-800" @click="reset">重置</button><button type="button" autofocus aria-label="关闭筛选" class="flex h-8 w-8 items-center justify-center rounded-md text-xl text-slate-400 dark:text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 dark:hover:bg-slate-800" @click="close">×</button></div>
        </header>

        <div class="min-h-0 min-w-0 flex-1 overflow-y-auto px-5">
          <fieldset class="min-w-0 border-b border-slate-100 dark:border-slate-800 py-5">
            <legend class="mb-3 text-sm font-semibold text-slate-900 dark:text-slate-100">类型</legend>
            <div class="flex flex-wrap gap-2"><button type="button" :aria-pressed="draftGenre === ''" class="min-h-10 rounded-md border px-3 py-2 text-sm" :class="chip(draftGenre === '')" @click="draftGenre = ''">全部</button><button v-for="item in genres" :key="item.id" type="button" :aria-pressed="draftGenre === String(item.id)" class="min-h-10 rounded-md border px-3 py-2 text-sm" :class="chip(draftGenre === String(item.id))" @click="draftGenre = String(item.id)">{{ item.name }}</button></div>
          </fieldset>

          <fieldset class="min-w-0 border-b border-slate-100 dark:border-slate-800 py-5">
            <legend class="mb-3 text-sm font-semibold text-slate-900 dark:text-slate-100">地区</legend>
            <div class="grid grid-cols-4 gap-2"><button v-for="item in regions" :key="item.id" type="button" :aria-pressed="draftRegion === item.id" class="h-10 rounded-md border px-2 text-sm" :class="chip(draftRegion === item.id)" @click="draftRegion = item.id">{{ item.name }}</button></div>
          </fieldset>

          <fieldset class="min-w-0 border-b border-slate-100 dark:border-slate-800 py-5">
            <legend class="mb-3 text-sm font-semibold text-slate-900 dark:text-slate-100">排序</legend>
            <div class="grid grid-cols-3 gap-2"><button v-for="item in sorts" :key="item.id" type="button" :aria-pressed="draftSort === item.id" class="h-10 rounded-md border px-2 text-sm" :class="chip(draftSort === item.id)" @click="draftSort = item.id">{{ item.name }}</button></div>
          </fieldset>

          <fieldset class="min-w-0 py-5">
            <legend class="mb-3 text-sm font-semibold text-slate-900 dark:text-slate-100">年份</legend>
            <div class="mb-3 flex min-w-0 max-w-full gap-2 overflow-x-auto pb-1">
              <button type="button" :aria-pressed="draftYear === ''" class="h-9 shrink-0 rounded-md border px-3 text-sm" :class="chip(draftYear === '')" @click="draftYear = ''; yearGrid?.scrollTo({ top: 0, behavior: 'smooth' })">全部</button>
              <button v-for="decade in decades" :key="decade" type="button" class="h-9 shrink-0 rounded-md border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950" @click="scrollToYear(String(Math.min(currentYear, decade + 9)))">{{ decade }}年代</button>
            </div>
            <div ref="yearGrid" class="year-grid relative grid w-full min-w-0 max-w-full grid-cols-4 gap-2 overflow-y-auto pr-1">
              <button v-for="item in allYears" :key="item" type="button" :data-year="item" :aria-pressed="draftYear === item" class="h-10 rounded-md border text-sm tabular-nums" :class="chip(draftYear === item)" @click="draftYear = item">{{ item }}</button>
            </div>
          </fieldset>
        </div>

        <footer class="flex shrink-0 gap-3 border-t border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-5 py-4 pb-[max(1rem,env(safe-area-inset-bottom))]">
          <button type="button" class="h-11 flex-1 rounded-md border border-slate-200 dark:border-slate-800 text-sm text-slate-600 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 dark:bg-slate-950" @click="close">取消</button>
          <button type="submit" class="h-11 flex-[2] rounded-md bg-slate-900 text-sm font-medium text-white hover:bg-slate-800">{{ changed ? '应用筛选' : '完成' }}</button>
        </footer>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.year-grid { max-height: 17rem; overscroll-behavior: contain; scrollbar-gutter: stable; }
</style>
