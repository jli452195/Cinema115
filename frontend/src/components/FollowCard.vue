<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { Follow } from '../types/follows'
import FollowDialog from './FollowDialog.vue'

const props = defineProps<{ item: Follow; admin?: boolean; subscriberCount?: number; busy?: boolean; cancelling?: boolean }>()
const emit = defineEmits<{ edit: []; cancel: [] }>()
const failedImage = ref(false), noteOpen = ref(false)
watch(() => props.item.posterUrl, () => { failedImage.value = false })
const path = computed(() => `/${props.item.kind}/${props.item.tmdbId}`)
const label = computed(() => {
  const p = props.item.progress
  if (p?.error) return '等待复核'
  if (p?.state === 'completed' || props.item.status === 'completed') return '已完成'
  if (p?.state === 'caught_up') return '已追平'
  if (p?.state === 'waiting') return '等待播出'
  if (!props.admin && p?.state === 'tracking') return '待追齐'
  return props.item.status === 'processing' ? '处理中' : '待处理'
})
const positive = computed(() => ['已完成', '已追平'].includes(label.value))
const progress = computed(() => {
  const p = props.item.progress
  if (p?.error) return '等待自动复核入库信息'
  if (props.item.kind === 'tv' && p && p.aired > 0 && p.state !== 'absent') return `已入库 ${p.owned}/${p.aired} 集${p.state === 'caught_up' ? ' · 继续追更' : ''}`
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
  <article class="flex min-w-0 flex-col rounded-2xl border border-slate-200 bg-white p-4 transition-shadow hover:shadow-md hover:shadow-slate-900/5" data-follow-card>
    <div class="flex min-w-0 items-start gap-3">
      <RouterLink :to="path" :aria-label="`查看${item.title}`" class="h-28 w-[74px] shrink-0 overflow-hidden rounded-lg bg-slate-100"><img v-if="item.posterUrl && !failedImage" :src="item.posterUrl" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImage = true" /><span v-else class="flex h-full items-center justify-center text-[10px] text-slate-400">暂无海报</span></RouterLink>
      <div class="min-w-0 flex-1 space-y-2">
        <RouterLink :to="path" :title="item.title" class="line-clamp-2 break-words text-sm font-semibold leading-5 text-slate-900 hover:text-amber-600">{{ item.title }}</RouterLink>
        <p class="text-[11px] leading-4 text-slate-400">{{ item.kind === 'movie' ? '电影' : '剧集' }} · {{ item.date?.slice(0, 4) || '年份待定' }}<span v-if="admin"> · TMDB {{ item.tmdbId }}</span></p>
        <div class="flex flex-wrap gap-1.5 text-[11px]"><span class="rounded px-2 py-0.5" :class="positive ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'">{{ label }}</span><span v-if="admin" class="rounded bg-slate-100 px-2 py-0.5 text-slate-500">{{ subscriberCount || 0 }} 人追更</span></div>
        <p class="text-[11px] leading-4" :class="positive ? 'text-emerald-700' : 'text-slate-500'">{{ progress }}</p>
      </div>
    </div>
    <p class="mt-3 text-[11px] text-slate-400">{{ admin ? '最近申请' : '申请于' }} {{ dateLabel }}</p>
    <button v-if="item.note" type="button" aria-label="查看处理说明" class="mt-3 rounded-lg bg-slate-50 px-3 py-2 text-left text-xs leading-5 text-slate-600 hover:bg-slate-100" @click="noteOpen = true"><span class="line-clamp-2 whitespace-pre-wrap break-words">处理说明：{{ item.note }}</span></button>
    <div class="mt-auto flex flex-wrap items-center gap-x-4 gap-y-2 pt-4 text-xs">
      <template v-if="admin"><button type="button" :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="emit('edit')">处理申请</button><RouterLink :to="{ path, query: { botSearch: '1' } }" class="text-blue-600">搜索资源</RouterLink></template>
      <RouterLink :to="path" class="text-slate-500 hover:text-blue-600">查看详情</RouterLink>
      <button v-if="!admin" type="button" :disabled="busy" class="text-slate-400 hover:text-red-600 disabled:opacity-40" @click="emit('cancel')">{{ cancelling ? '取消中…' : '取消追更' }}</button>
    </div>
    <FollowDialog v-if="noteOpen" title="处理说明" @close="noteOpen = false"><p class="mb-3 text-sm font-semibold text-slate-900">{{ item.title }}</p><p class="whitespace-pre-wrap break-words text-sm leading-7 text-slate-600">{{ item.note }}</p></FollowDialog>
  </article>
</template>
