<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { Follow } from '../types/follows'
import { FOLLOW_STATUS_LABELS, isFollowCaughtUp } from '../types/follows'
import FollowDialog from './FollowDialog.vue'
import { tmdbImageURL } from '../utils/tmdbImage'

const props = defineProps<{ item: Follow; admin?: boolean; subscriberCount?: number; busy?: boolean; cancelling?: boolean }>()
const emit = defineEmits<{ edit: []; cancel: [] }>()
const failedImage = ref(false), noteOpen = ref(false)
watch(() => props.item.posterUrl, () => { failedImage.value = false })
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
const statusClass = computed(() => label.value === '已关闭' ? 'bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400' : positive.value ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-500')
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
  <article class="group relative min-w-0 overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-4 transition-all duration-300 ease-out hover:-translate-y-1 hover:border-amber-300/60 hover:shadow-xl hover:shadow-slate-900/10 dark:hover:border-amber-500/40 dark:hover:shadow-black/40" data-follow-card>
    <div v-if="item.posterUrl && !failedImage" class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <!-- 动态环境光背景：利用 CSS 高斯模糊实现纯前端的 MD3 动态取色 -->
      <img :src="tmdbImageURL(item.posterUrl)" alt="" loading="lazy" referrerpolicy="no-referrer" class="absolute -left-1/4 -top-1/4 h-[150%] w-[150%] object-cover blur-3xl opacity-50 saturate-150 transition-opacity duration-500 ease-out group-hover:opacity-70 dark:opacity-40 dark:group-hover:opacity-60 transform-gpu backface-hidden" @error="failedImage = true" />
      <!-- 混合层与底部渐变，保证按钮和文字的可读性 -->
      <div class="absolute inset-0 bg-white/40 transition-colors duration-500 ease-out group-hover:bg-white/20 dark:bg-slate-900/50 dark:group-hover:bg-slate-900/30"></div>
      <div class="absolute inset-0 bg-gradient-to-t from-white via-white/80 to-white/10 dark:from-slate-900 dark:via-slate-900/80 dark:to-slate-900/10"></div>
    </div>
    <div class="relative z-10 flex h-full min-w-0 flex-col">
      <div class="flex min-w-0 items-start gap-3">
        <RouterLink :to="path" :aria-label="`查看${item.title}`" class="h-28 w-[74px] shrink-0 overflow-hidden rounded-lg bg-slate-100 dark:bg-slate-800 shadow-sm"><img v-if="item.posterUrl && !failedImage" :src="tmdbImageURL(item.posterUrl)" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover transition-transform duration-500 ease-out group-hover:scale-110" @error="failedImage = true" /><span v-else class="flex h-full items-center justify-center text-[10px] text-slate-400 dark:text-slate-500">暂无海报</span></RouterLink>
        <div class="min-w-0 flex-1 space-y-2">
          <RouterLink :to="path" :title="item.title" class="line-clamp-2 break-words text-sm font-semibold leading-5 text-slate-900 dark:text-slate-100 hover:text-amber-600">{{ item.title }}</RouterLink>
          <p class="text-[11px] leading-4 text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ item.kind === 'movie' ? '电影' : '剧集' }} · {{ item.date?.slice(0, 4) || '年份待定' }}<span v-if="admin"> · TMDB {{ item.tmdbId }}</span></p>
          <div class="flex flex-wrap gap-1.5 text-[11px]"><span class="rounded px-2 py-0.5" :class="statusClass">{{ label }}</span><span v-if="admin" class="rounded bg-white dark:bg-slate-900/80 px-2 py-0.5 text-slate-600 dark:text-slate-400 dark:text-slate-500 ring-1 ring-slate-200 dark:ring-slate-700/70">{{ subscriberCount || 0 }} 人追更</span></div>
          <p class="text-[11px] leading-4" :class="positive ? 'text-emerald-700' : 'text-slate-600 dark:text-slate-400 dark:text-slate-500'">{{ progress }}</p>
        </div>
      </div>
      <p class="mt-3 text-[11px] text-slate-400 dark:text-slate-500">{{ admin ? '最近申请' : '申请于' }} {{ dateLabel }}</p>
      <button v-if="item.note" type="button" aria-label="查看处理说明" class="mt-3 rounded-lg bg-white/60 dark:bg-slate-900/60 backdrop-blur-md px-3 py-2 text-left text-xs leading-5 text-slate-600 dark:text-slate-400 ring-1 ring-slate-200/50 dark:ring-slate-700/50 hover:bg-white/80 dark:hover:bg-slate-800/80" @click="noteOpen = true"><span class="line-clamp-2 whitespace-pre-wrap break-words">处理说明：{{ item.note }}</span></button>
      <div class="mt-auto flex flex-wrap items-center gap-x-4 gap-y-2 pt-4 text-xs">
        <template v-if="admin"><button type="button" :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="emit('edit')">处理申请</button><RouterLink v-if="item.status !== 'closed'" :to="{ path, query: { botSearch: '1' } }" class="text-blue-600">搜索资源</RouterLink></template>
        <RouterLink :to="path" class="text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:text-blue-600">查看详情</RouterLink>
        <button v-if="!admin" type="button" :disabled="busy" class="text-slate-400 dark:text-slate-500 hover:text-red-600 disabled:opacity-40" @click="emit('cancel')">{{ cancelling ? '取消中…' : '取消追更' }}</button>
      </div>
      <FollowDialog v-if="noteOpen" title="处理说明" @close="noteOpen = false"><p class="mb-3 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ item.title }}</p><p class="whitespace-pre-wrap break-words text-sm leading-7 text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ item.note }}</p></FollowDialog>
    </div>
  </article>
</template>
