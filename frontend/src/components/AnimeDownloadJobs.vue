<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { IconChevronDown, IconChevronLeft, IconChevronRight, IconRefresh } from '@tabler/icons-vue'
import AnimeDownloadActions from './AnimeDownloadActions.vue'
import { cachedSubscription, readSubscription, rememberSubscriptionState, subscriptionState } from '../composables/useAnimeSubscriptionBrowser'
import { useAnimeLocalPolling } from '../composables/useAnimeLocalPolling'
import { animeConfigMessage } from '../lib/anime-config-api'
import { animeLocalTime } from '../utils/animeSubscriptionPresentation'
import { animeDownloadState, animeDownloadReason } from '../utils/animeDownloadPresentation'
import type { AnimeDownloadPage } from '../types/animeSubscription'

const props = defineProps<{ token: string; id: string; locked: boolean; version: number }>()
const emit = defineEmits<{ changed: [] }>()
const stateKey = `jobs:${props.id}`, saved = subscriptionState(stateKey)
const page = ref(saved.page || 1), expanded = ref(saved.expanded), result = ref<AnimeDownloadPage>(), loading = ref(false), error = ref('')
const pages = computed(() => Math.max(1, Math.ceil((result.value?.total || 0) / 20)))
let controller: AbortController | undefined, disposed = false, refreshing = false
function save() { rememberSubscriptionState(stateKey, { top: 0, page: page.value, expanded: expanded.value }) }
function toggle(id: string) { expanded.value = expanded.value.includes(id) ? expanded.value.filter(value => value !== id) : [...expanded.value, id]; save() }
function changePage(value: number) { if (value < 1 || value > pages.value || loading.value) return; void load(false, false, value) }
async function load(force = false, silent = false, requestedPage = page.value) {
  controller?.abort(); const active = new AbortController(); controller = active
  const key = `subscriptions/${encodeURIComponent(props.id)}/jobs?page=${requestedPage}&pageSize=20`
  if (!force) { const cached = cachedSubscription<AnimeDownloadPage>(key); if (cached) { result.value = cached.data; page.value = requestedPage; save(); if (cached.fresh) return } }
  refreshing = true; if (!silent) { loading.value = true; error.value = '' }
  try {
    const data = await readSubscription<AnimeDownloadPage>(key, props.token, active.signal)
    if (disposed || active.signal.aborted || controller !== active) return
    result.value = data; page.value = data.page; save(); error.value = ''
    const last = Math.max(1, Math.ceil(data.total / 20))
    if (page.value > last) changePage(last)
  } catch (reason) { if (!disposed && !active.signal.aborted && controller === active) error.value = animeConfigMessage(reason) }
  finally { if (!disposed && controller === active) { loading.value = false; refreshing = false } }
}
function changed() { void load(true, true); emit('changed') }
void load()
watch(() => props.version, () => void load(true, true))
useAnimeLocalPolling(() => load(true, true), () => controller?.abort(), () => !refreshing && !props.locked)
onBeforeUnmount(() => { save(); disposed = true; controller?.abort() })
</script>

<template>
  <section class="as-detail-section as-jobs" aria-label="资源执行记录">
    <div class="as-section-heading"><h4>资源执行记录 <span v-if="result">{{ result.total }} 条</span></h4><button type="button" class="as-text-button" :disabled="loading" aria-label="刷新本地任务记录" @click="load(true)"><IconRefresh :size="15" aria-hidden="true" />刷新记录</button></div>
    <p class="as-muted">每条记录代表一份资源，可保留同集不同字幕组版本。页面刷新只读取本地记录。</p>
    <p v-if="error" class="as-muted" role="status">{{ error }}{{ result ? '，保留上次记录。' : '' }}</p>
    <p v-else-if="loading && !result" class="as-muted" role="status">正在读取执行记录…</p>
    <p v-if="result && !result.items.length" class="as-empty-jobs">尚无下载任务{{ result.total ? '，可切换分页查看。' : '。' }}</p>
    <div v-for="job in result?.items || []" :key="job.id" class="as-job" :data-job-id="job.id">
      <button type="button" class="as-job-row" :aria-expanded="expanded.includes(job.id)" :aria-controls="`job-${job.id}`" @click="toggle(job.id)">
        <span class="as-job-title"><strong :title="job.title">{{ job.title }}</strong><span>{{ job.groupName || '字幕组未记录' }}<span v-if="job.progress != null && job.downloadState === 'downloading'"> · {{ Math.round(job.progress) }}%</span> · {{ animeLocalTime(job.updatedAt) }}</span></span>
        <span class="as-job-phase"><span class="as-state" :data-status="job.blockCode ? 'review' : job.fileState === 'verified' ? 'saved' : job.downloadState">{{ animeDownloadState(job) }}</span><IconChevronDown :size="16" aria-hidden="true" :class="{ 'rotate-180': expanded.includes(job.id) }" /></span>
      </button>
      <div v-show="expanded.includes(job.id)" :id="`job-${job.id}`" class="as-job-detail" role="region" :aria-label="`${job.groupName || '资源'}执行详情`">
        <dl class="as-source-fields">
          <div><dt>执行时路径</dt><dd>{{ job.executionPath }}<span v-if="!job.directoryCid" class="as-muted">（目录尚未确认创建）</span></dd></div>
          <div><dt>115 状态</dt><dd>{{ ({ queued: '待提交', submitting: '提交中', unknown: '未知', accepted: '已接收', downloading: '下载中', completed: '已完成', failed: '失败', blocked: '受阻', cancelled: '已取消' } as Record<string, string>)[job.downloadState] || job.downloadState }}</dd></div>
          <div><dt>文件清单</dt><dd>{{ job.fileState === 'verified' ? `已保存 ${job.files?.filter(file => file.kind === 'file').length ?? 0} 个文件的记录` : job.fileState === 'needs_review' ? '需要处理' : '尚未取得完整清单' }}</dd></div>
          <div><dt>最近结果</dt><dd>{{ animeLocalTime(job.observedAt) }}<span v-if="job.attemptedAt && job.attemptedAt > job.observedAt" class="as-muted"> · 最近尝试 {{ animeLocalTime(job.attemptedAt) }}</span></dd></div>
        </dl>
        <p class="as-muted">路径仅为执行时记录，不跟踪二次整理后的位置。</p>
        <p v-if="job.blockCode" class="as-job-reason">{{ animeDownloadReason(job.blockCode) }}<code>{{ job.blockCode }}</code></p>
        <AnimeDownloadActions :token="token" :subscription-id="id" :job="job" :locked="locked" @changed="changed" />
      </div>
    </div>
    <nav v-if="result && result.total > 20" class="as-job-pagination" aria-label="资源任务分页"><button type="button" :aria-disabled="page <= 1 || loading" aria-label="任务上一页" @click="changePage(page - 1)"><IconChevronLeft :size="17" aria-hidden="true" />上一页</button><span aria-live="polite">{{ page }} / {{ pages }}</span><button type="button" :aria-disabled="page >= pages || loading" aria-label="任务下一页" @click="changePage(page + 1)">下一页<IconChevronRight :size="17" aria-hidden="true" /></button></nav>
  </section>
</template>
