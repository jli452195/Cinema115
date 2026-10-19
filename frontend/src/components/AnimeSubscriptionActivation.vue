<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { IconChevronLeft, IconChevronRight } from '@tabler/icons-vue'
import { animeConfigRequest, animeConfigMessage } from '../lib/anime-config-api'
import type { AnimePreviewPage, AnimePreviewResponse, AnimeSettingsView, AnimeSubscriptionView } from '../types/animeSubscription'

const props = defineProps<{ token: string; view: AnimeSubscriptionView; locked: boolean }>()
const emit = defineEmits<{ enable: [body: unknown] }>()
interface SourcePreview { id: string; name: string; batchId: string; page: AnimePreviewPage; pageNo: number; selected: string[]; dismissed: string[]; error: string; notice?: string }
const previews = ref<SourcePreview[]>([]), loading = ref(false), error = ref(''), opened = ref(false), settingsRevision = ref(0), revision = ref(0)
const totalSelected = computed(() => previews.value.reduce((sum, source) => sum + source.selected.length, 0))
const matched = computed(() => previews.value.reduce((sum, source) => sum + source.page.summary.matchedCount, 0))
const review = computed(() => previews.value.reduce((sum, source) => sum + source.page.summary.reviewCount, 0))
const active = computed(() => props.view.sources.filter(source => source.runWhenEnabled && source.configuredState !== 'removed'))
const stale = computed(() => opened.value && revision.value !== props.view.revision)
const ready = computed(() => !loading.value && !props.locked && !stale.value && previews.value.length === active.value.length && previews.value.length > 0 && !previews.value.some(source => source.error || source.page.pendingSelections?.length) && !(matched.value === 0 && review.value > 0))
const loadedCount = computed(() => previews.value.reduce((sum, source) => sum + source.page.summary.rawCount, 0))
let disposed = false, controller: AbortController | undefined
async function generate() {
  if (loading.value || props.locked) return
  controller?.abort(); const own = new AbortController(); controller = own
  loading.value = true; error.value = ''; opened.value = true; previews.value = []; revision.value = props.view.revision
  try {
    const settings = await animeConfigRequest<AnimeSettingsView>(props.token, 'settings', { signal: own.signal })
    settingsRevision.value = settings.settings.revision
    for (const source of active.value) {
      const response = await animeConfigRequest<AnimePreviewResponse>(props.token, `subscriptions/${encodeURIComponent(props.view.id)}/enable-preview`, { method: 'POST', body: { expectedRevision: revision.value, settingsRevision: settingsRevision.value, sourceId: source.id }, signal: own.signal })
      if (disposed || own.signal.aborted) return
      if (!response.page || response.issues.length) throw new Error('来源预览尚未完整，请检查规则后重新预览。')
      previews.value.push({ id: source.id, name: source.groupName || '字幕组', batchId: response.batchId, page: { ...response.page, items: response.page.items.slice(0, 10), pageSize: 10 }, pageNo: 1, selected: [...response.page.selectedIds], dismissed: [], error: '', notice: response.selectionNotice })
    }
  } catch (reason) { if (!disposed && !own.signal.aborted) error.value = animeConfigMessage(reason) }
  finally { if (!disposed) loading.value = false }
}
function select(source: SourcePreview, id: string, checked: boolean) {
  source.selected = checked ? [...new Set([...source.selected, id])] : source.selected.filter(value => value !== id)
  source.dismissed = checked ? source.dismissed.filter(value => value !== id) : [...new Set([...source.dismissed, id])]
}
async function readPage(source: SourcePreview, number: number) {
  if (loading.value || props.locked || number < 1 || number > Math.max(1, Math.ceil(source.page.total / 10))) return
  loading.value = true; source.error = ''
  try {
    const page = await animeConfigRequest<AnimePreviewPage>(props.token, `previews/${source.batchId}/releases?sourceRef=${source.id}&page=${number}&pageSize=10`, { signal: controller?.signal })
    if (!disposed) { source.page = page; source.pageNo = number }
  } catch (reason) { if (!disposed) source.error = animeConfigMessage(reason) }
  finally { if (!disposed) loading.value = false }
}
async function more(source: SourcePreview) {
  if (loading.value || props.locked || stale.value) return
  loading.value = true; source.error = ''
  const summary = source.page.summary
  try {
    await animeConfigRequest(props.token, `previews/${source.batchId}/selections`, { method: 'POST', body: { sourceRef: source.id, sourcePreviewId: summary.sourcePreviewId, expectedLoadRevision: summary.loadRevision, selectedReleaseIds: source.selected, dismissedReleaseIds: source.dismissed }, signal: controller?.signal })
    const response = await animeConfigRequest<AnimePreviewResponse>(props.token, `previews/${source.batchId}/load`, { method: 'POST', body: { sourceRef: source.id, sourcePreviewId: summary.sourcePreviewId, expectedLoadRevision: summary.loadRevision, nextTake: Math.min(2000 - loadedCount.value + summary.rawCount, Math.max(200, summary.rawCount * 2)) }, signal: controller?.signal })
    if (disposed) return
    if (!response.page || response.issues.length) throw new Error('历史预览尚未完整，请重新核对。')
    source.batchId = response.batchId; source.page = { ...response.page, items: response.page.items.slice(0, 10), pageSize: 10 }; source.pageNo = 1; source.selected = response.page.selectedIds.filter(id => !source.dismissed.includes(id))
  } catch (reason) { if (!disposed) source.error = animeConfigMessage(reason) }
  finally { if (!disposed) loading.value = false }
}
function enable() {
  if (!ready.value) return
  emit('enable', { expectedRevision: revision.value, settingsRevision: settingsRevision.value, sources: previews.value.map(source => ({ batchId: source.batchId, sourceRef: source.id, sourcePreviewId: source.page.summary.sourcePreviewId, loadRevision: source.page.summary.loadRevision, selectedReleaseIds: source.selected, dismissedReleaseIds: source.dismissed })) })
}
onBeforeUnmount(() => { disposed = true; controller?.abort() })
</script>

<template>
  <section class="as-detail-section as-activation" aria-label="首次启用确认">
    <div class="as-section-heading"><h4>开始订阅</h4><button v-if="opened" type="button" class="as-text-button" :disabled="loading || locked" @click="opened = false">收起预览</button></div>
    <p class="as-muted">先核对当前规则与历史选择。保存配置不会开始下载，启用后才提交所选资源并检查新发布。</p>
    <button type="button" class="as-action" :disabled="loading || locked" @click="generate">{{ loading ? '正在读取来源预览…' : opened ? '重新预览' : '预览并启用订阅' }}</button>
    <div v-if="opened" class="as-activation-preview">
      <p v-if="error" class="as-job-reason" role="status">{{ error }}</p>
      <p v-if="stale" class="as-job-reason" role="status">配置版本已变化，请重新预览后再启用。</p>
      <section v-for="source in previews" :key="source.id" class="as-preview-source">
        <h5>{{ source.name }}<span>已选 {{ source.selected.length }} 条</span></h5>
        <p v-if="source.notice" class="as-muted">{{ source.notice }}</p>
        <p class="as-muted">已加载 {{ source.page.summary.loadedCount }} 条资源，匹配 {{ source.page.summary.matchedCount }} 条<span v-if="source.page.summary.reviewCount">，待确认 {{ source.page.summary.reviewCount }} 条</span>。{{ source.page.summary.unloadedHistory ? '仍有未加载历史，不会自动补下。' : '已到当前可见历史末尾。' }}</p>
        <div class="as-preview-items">
          <label v-for="item in source.page.items" :key="item.release.id" class="as-preview-choice"><input type="checkbox" :checked="source.selected.includes(item.release.id)" :disabled="!item.selectable || loading || locked || stale" @change="select(source, item.release.id, ($event.target as HTMLInputElement).checked)" /><span><strong>{{ item.release.title }}</strong><small>{{ item.selectable ? `发布第 ${item.projection.sourceEpisode ?? '?'} 话` : item.decision === 'filtered' ? '不符合当前规则' : '待确认，不会自动下载' }}<span v-if="item.projection.chapterEpisode"> · 篇章第 {{ item.projection.chapterEpisode }} 集</span></small></span></label>
        </div>
        <p v-if="source.error" class="as-job-reason" role="status">{{ source.error }}</p>
        <nav v-if="source.page.total > 10" class="as-job-pagination" :aria-label="`${source.name}历史资源分页`"><button type="button" :aria-disabled="loading || source.pageNo <= 1" aria-label="历史上一页" @click="readPage(source, source.pageNo - 1)"><IconChevronLeft :size="16" aria-hidden="true" /></button><span>{{ source.pageNo }} / {{ Math.ceil(source.page.total / 10) }}</span><button type="button" :aria-disabled="loading || source.pageNo >= Math.ceil(source.page.total / 10)" aria-label="历史下一页" @click="readPage(source, source.pageNo + 1)"><IconChevronRight :size="16" aria-hidden="true" /></button></nav>
        <button v-if="source.page.summary.unloadedHistory && loadedCount < 2000" type="button" class="as-text-button" :disabled="loading || locked || stale" @click="more(source)">展开更多历史</button>
      </section>
      <template v-if="previews.length && !error"><p class="as-muted">{{ totalSelected ? `将下载已选 ${totalSelected} 条资源，其余已加载历史不下载。` : '不下载当前历史，仅等待后续符合规则的新发布。' }}不洗版，同集不同资源可共存。</p><button type="button" class="as-action as-action-primary" :disabled="!ready" @click="enable">{{ !matched && review ? '请先处理待确认资源' : totalSelected ? `启用并下载已选 ${totalSelected} 条` : matched ? '启用订阅' : '启用并等待更新' }}</button></template>
    </div>
  </section>
</template>
