<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import type { EmbyMatch } from '../types/emby'
import { formatEpisodeRanges, type seasonLibraryProgress } from '../types/seasonLibrary'

const props = defineProps<{
  state?: EmbyMatch['state']; progress?: ReturnType<typeof seasonLibraryProgress>
  loading: boolean; error: string; hasUndated: boolean; stale?: boolean
  ownedEpisodes?: number[]
}>()
const emit = defineEmits<{ load: []; refresh: [] }>()
const root = ref<HTMLElement>()
const visible = ref(false)
let observer: IntersectionObserver | undefined
const reliable = computed(() => props.state === 'present' || props.state === 'absent')
const ownedCount = computed(() => props.progress?.ownedCount ?? new Set(props.ownedEpisodes || []).size)
const ownedLabel = computed(() => props.progress?.ownedLabel ?? formatEpisodeRanges(props.ownedEpisodes || []))
const dateNote = computed(() => {
  if (!props.progress) return props.error ? '播出信息加载失败' : '正在读取播出信息…'
  if (props.progress.undatedCount || props.hasUndated) return props.progress.aired.length ? '部分 TMDB 播出日期未提供' : 'TMDB 播出日期未提供'
  if (props.progress.extraOwned) return '部分入库分集尚无已播出记录'
  return ''
})
const label = computed(() => {
  if (!props.state || props.state === 'unknown') return '入库状态未知'
  if (props.state === 'stale') return '索引待更新'
  if (props.state === 'absent') return '未入库'
  if (ownedCount.value && (!props.progress || props.progress.extraOwned > 0)) return `已入库 ${ownedCount.value} 集`
  if (props.error && !props.progress) return '分集加载失败'
  if (!props.progress) return '正在读取播出进度…'
  if (!props.progress.aired.length) return props.hasUndated ? '播出日期待定' : '尚未播出'
  if (!props.progress.collected) return '未入库'
  return props.progress.missing.length ? `已入库 ${props.progress.collected}/${props.progress.aired.length}` : '已入库'
})
const hint = computed(() => {
  if (!props.progress) return ''
  if (!props.progress.aired.length) return `入库集数：${ownedLabel.value}；暂不判断播出进度`
  if (props.progress.complete) return '整季已入库'
  if (props.progress.caughtUp) return '已追平最新播出'
  return props.progress.missing.length ? `已确认播出的缺失集数：${props.progress.missingLabel}` : '已确认播出的分集均已入库，其他分集日期待定'
})
watchEffect(() => {
  if (visible.value && props.state === 'present' && !props.progress && !props.loading && !props.error) emit('load')
  if (visible.value && props.state === 'present' && props.progress && props.stale && !props.loading && !props.error) emit('refresh')
})
onMounted(() => {
  if (!('IntersectionObserver' in window)) { visible.value = true; return }
  observer = new IntersectionObserver(entries => { visible.value = entries.some(entry => entry.isIntersecting) }, { rootMargin: '120px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <span ref="root" class="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2">
    <span class="inline-flex items-center gap-1.5 whitespace-nowrap text-[11px] font-semibold" :class="reliable && ownedCount ? (progress?.missing.length ? 'text-amber-600' : 'text-emerald-600') : 'text-slate-400'">
      <svg v-if="reliable && ownedCount" class="h-3.5 w-3.5 text-green-500" viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="m8 0 8 8-8 8-8-8z"/><path fill="white" d="m6 4 5 4-5 4z"/></svg>
      <span v-else-if="state === 'absent' || (reliable && progress?.aired.length)" class="h-1 w-1 shrink-0 rounded-full bg-slate-300" aria-hidden="true"></span>
      {{ label }}
    </span>
    <span v-if="state === 'present' && ownedCount && dateNote" class="text-[10px] font-normal text-slate-400">{{ dateNote }}</span>
    <span v-if="state === 'present' && ownedCount && (!progress || progress.extraOwned > 0)" class="min-w-0 break-words text-[10px] font-normal text-slate-500">入库集数：{{ ownedLabel }}</span>
    <span v-if="state === 'present' && progress?.aired.length && progress.collected > 0" class="group/progress relative inline-flex w-44 max-w-full items-center rounded-lg border border-slate-200 bg-slate-100/90 px-2.5 py-2" :title="hint">
      <span role="progressbar" :aria-label="hint" :aria-valuenow="progress.collected" :aria-valuemax="progress.aired.length" :aria-valuemin="0" :aria-valuetext="`已播出 ${progress.aired.length} 集，已入库 ${progress.collected} 集。${hint}`" class="flex h-1.5 w-full overflow-hidden rounded-full bg-slate-300">
        <span v-for="(segment, index) in progress.segments" :key="index" class="h-full min-w-0" :style="{ flex: `${segment.count} 1 0%` }" :class="segment.owned ? 'bg-blue-500' : 'bg-slate-300'"></span>
      </span>
      <span aria-hidden="true" class="pointer-events-none absolute left-1/2 top-1/2 z-10 hidden w-max max-w-[min(28rem,70vw)] -translate-x-1/2 -translate-y-1/2 rounded-md border border-slate-200 bg-white px-2.5 py-1.5 text-center text-[11px] font-medium text-slate-700 shadow-md group-hover/progress:block group-focus-visible/season:block">{{ hint }}</span>
    </span>
    <span v-if="state === 'present' && progress && (loading || error || stale)" class="text-[10px] font-normal text-slate-400" :title="error || '当前先显示缓存播出信息'">{{ error ? '缓存 · 刷新失败' : loading ? '更新中' : '缓存' }}</span>
  </span>
</template>
