<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { Follow } from '../types/follows'
import { FOLLOW_STATUS_LABELS, isFollowCaughtUp } from '../types/follows'
import FollowDialog from './FollowDialog.vue'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ item: Follow; admin?: boolean; subscriberCount?: number; busy?: boolean; cancelling?: boolean }>()
const emit = defineEmits<{ edit: []; cancel: [] }>()
const failedImage = ref(false)
const noteOpen = ref(false)
const posterSrc = computed(() => tmdbImageURL(props.item.posterUrl))
watch(posterSrc, () => { failedImage.value = false })
const path = computed(() => `/${props.item.kind}/${props.item.tmdbId}`)
const label = computed(() => {
  const p = props.item.progress
  if (props.admin && props.item.effectiveStatus) return FOLLOW_STATUS_LABELS[props.item.effectiveStatus]
  if (isFollowCaughtUp(props.item)) return '已追平'
  if (p?.state === 'completed' || props.item.status === 'completed') return '已完成'
  if (props.item.status === 'closed') return '已关闭'
  if (p?.error) return '等待复核'
  if (p?.state === 'waiting') return '等待播出'
  if (!props.admin && p?.state === 'tracking') return '待追齐'
  return props.item.status === 'processing' ? '处理中' : '待处理'
})
const positive = computed(() => ['已完成', '已追平'].includes(label.value))
const progress = computed(() => {
  const p = props.item.progress
  if (props.item.status === 'closed') return '已停止资源搜索，仍会确认 Emby 入库'
  if (p?.error) return '等待自动复核入库信息'
  if (props.item.kind === 'tv' && p && p.aired > 0 && p.state !== 'absent') return `已入库 ${p.owned}/${p.aired} 集${isFollowCaughtUp(props.item) ? ' · 继续追更' : ''}`
  if (props.item.libraryState === 'present') return props.item.kind === 'movie' ? '已入库' : '有入库记录'
  if (props.item.libraryState === 'absent') return '未入库'
  return '入库状态待确认'
})
const dateLabel = computed(() => {
  const date = new Date(props.item.subscribedAt)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString()
})
</script>

<template>
  <article class="follow-card relative isolate min-w-0 overflow-hidden rounded-2xl border px-3 pb-1.5 pt-2.5 sm:p-4" data-follow-card>
    <div v-if="posterSrc && !failedImage" class="follow-card-ambient pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <img :src="posterSrc" alt="" loading="lazy" referrerpolicy="no-referrer" class="absolute -left-1/4 -top-1/4 h-[150%] w-[150%] object-cover blur-3xl saturate-150" @error="failedImage = true" />
    </div>
    <div class="relative flex h-full min-w-0 flex-col">
      <div class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] grid-rows-[auto_1fr] items-start gap-x-2.5 gap-y-1 sm:grid-rows-none sm:gap-3">
        <RouterLink :to="path" :aria-label="`查看${item.title}`" class="follow-card-poster row-span-2 aspect-[2/3] w-[60px] overflow-hidden rounded-lg bg-slate-100 shadow-sm dark:bg-slate-800 max-[359px]:w-14 sm:row-span-1 sm:h-28 sm:w-[74px]">
          <img v-if="posterSrc && !failedImage" :src="posterSrc" alt="" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImage = true" />
          <span v-else class="flex h-full items-center justify-center text-[10px]">暂无海报</span>
        </RouterLink>
        <div class="min-w-0 space-y-1 sm:space-y-2">
          <RouterLink :to="path" :title="item.title" class="follow-card-title line-clamp-2 text-sm font-semibold leading-5 [overflow-wrap:anywhere]">{{ item.title }}</RouterLink>
          <p class="text-[11px] leading-4">{{ item.kind === 'movie' ? '电影' : '剧集' }} · {{ item.date?.slice(0, 4) || '年份待定' }}<span v-if="admin"> · TMDB {{ item.tmdbId }}</span></p>
          <div class="follow-card-status flex flex-wrap gap-1.5 text-[11px]">
            <span class="follow-status rounded px-2 py-0.5" :class="{ 'is-closed': label === '已关闭', 'is-positive': positive }">{{ label }}</span>
            <span v-if="admin" class="rounded bg-[rgb(var(--surface-card))] px-2 py-0.5 ring-1 ring-[rgb(var(--line-default))]">{{ subscriberCount ?? 0 }} 人追更</span>
          </div>
          <p class="text-[11px] leading-4" :class="{ 'text-[rgb(var(--status-success))]': positive }">{{ progress }}</p>
        </div>
        <p class="follow-card-date col-start-2 text-[11px] sm:col-span-full">{{ admin ? '最近申请' : '申请于' }} {{ dateLabel }}</p>
      </div>
      <button v-if="item.note" type="button" aria-label="查看处理说明" aria-haspopup="dialog" class="follow-note mt-2 min-h-11 rounded-lg px-2.5 py-1.5 text-left text-xs leading-5 sm:mt-3 sm:min-h-0 sm:px-3 sm:py-2" @click="noteOpen = true">
        <span class="line-clamp-2 whitespace-pre-wrap [overflow-wrap:anywhere]">处理说明：{{ item.note }}</span>
      </button>
      <div class="follow-card-actions mt-auto flex flex-wrap items-center gap-x-4 gap-y-1 pt-0.5 text-xs sm:gap-y-2 sm:pt-4">
        <template v-if="admin">
          <button type="button" :disabled="busy" class="follow-card-primary disabled:opacity-40" @click="emit('edit')">处理申请</button>
          <RouterLink v-if="item.status !== 'closed'" :to="{ path, query: { botSearch: '1' } }" class="follow-card-primary">搜索资源</RouterLink>
        </template>
        <RouterLink :to="path" class="follow-card-details">查看详情</RouterLink>
        <button v-if="!admin" type="button" :disabled="busy" class="follow-card-cancel disabled:opacity-40" @click="emit('cancel')">{{ cancelling ? '取消中…' : '取消追更' }}</button>
      </div>
      <FollowDialog v-if="noteOpen" title="处理说明" @close="noteOpen = false">
        <p class="mb-3 text-sm font-semibold text-[rgb(var(--content-primary))] [overflow-wrap:anywhere]">{{ item.title }}</p>
        <p class="whitespace-pre-wrap text-sm leading-7 text-[rgb(var(--content-secondary))] [overflow-wrap:anywhere]">{{ item.note }}</p>
      </FollowDialog>
    </div>
  </article>
</template>

<style scoped>
.follow-card {
  border-color: rgb(var(--line-default));
  background: rgb(var(--surface-card));
  color: rgb(var(--content-secondary));
}
.follow-card-ambient { border-radius: inherit; }
.follow-card-ambient img { opacity: .5; }
.follow-card-ambient::after {
  /* 渐变覆盖海报颜色，日期和操作区使用卡片底色。 */
  position: absolute;
  inset: 0;
  background: linear-gradient(to top, rgb(var(--surface-card)), rgb(var(--surface-card) / .88) 50%, rgb(var(--surface-card) / .46));
  content: '';
}
.follow-card-title { color: rgb(var(--content-primary)); }
.follow-card-primary { color: rgb(var(--action-link)); }
.follow-status { background: rgb(var(--status-warning-surface)); color: rgb(var(--status-warning)); }
.follow-status.is-positive { background: rgb(var(--status-success-surface)); color: rgb(var(--status-success)); }
.follow-status.is-closed { background: rgb(var(--surface-hover)); color: rgb(var(--content-secondary)); }
.follow-card :is(a, button):focus-visible { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
/* 列表里的说明只复用材质色与边缘，避免随卡片数量增加背景采样。 */
.follow-note { background: var(--glass-inset-surface); box-shadow: inset 0 0 0 1px var(--glass-inset-border); }

@media (max-width: 639px), (pointer: coarse) {
  .follow-card-actions > :is(a, button) { display: inline-flex; min-height: 44px; align-items: center; }
}
@media (hover: hover) {
  .follow-card :is(.follow-card-title, .follow-card-details):hover { color: rgb(var(--action-link)); }
  .follow-card-primary:hover { text-decoration: underline; }
  .follow-card-cancel:not(:disabled):hover { color: rgb(var(--status-danger)); }
  .follow-note:hover { background: var(--glass-pill-hover); }
}
@media (min-width: 640px) and (hover: hover) and (pointer: fine) {
  .follow-card:hover { border-color: rgb(var(--focus) / .4); box-shadow: 0 12px 24px -12px rgb(0 0 0 / .28); }
  @media (prefers-reduced-motion: no-preference) {
    .follow-card { transition: transform .3s ease-out, border-color .3s ease-out, box-shadow .3s ease-out; }
    .follow-card-poster img { transition: transform .5s ease-out; }
    .follow-card:hover { transform: translateY(-4px); }
    .follow-card:hover .follow-card-poster img { transform: scale(1.1); }
  }
}
</style>
