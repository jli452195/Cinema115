<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { IconChevronDown, IconPhotoOff, IconRefresh } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import AnimeDownloadJobs from './AnimeDownloadJobs.vue'
import AnimeSubscriptionActivation from './AnimeSubscriptionActivation.vue'
import AnimeOrganizationSettings from './AnimeOrganizationSettings.vue'
import { useAnimeSubscriptionActions } from '../composables/useAnimeSubscriptionActions'
import { useAnimeLocalPolling } from '../composables/useAnimeLocalPolling'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { animeImageURL } from '../composables/useAnimeDiscovery'
import { cachedSubscription, readSubscription, rememberSubscriptionState, subscriptionState } from '../composables/useAnimeSubscriptionBrowser'
import { AnimeConfigError, animeConfigMessage } from '../lib/anime-config-api'
import { animeLocalTime, subscriptionSources, subscriptionStatus } from '../utils/animeSubscriptionPresentation'
import { animeSpecLabel, animeProviderName, animeOriginLabel } from '../utils/animeSourceForm'
import type { AnimeSubscriptionView } from '../types/animeSubscription'

const props = defineProps<{ token: string; id: string; returnPath: string; initial?: AnimeSubscriptionView }>()
const emit = defineEmits<{ close: [] }>()
const ownerToken = props.token, ownerID = portalUser.value?.id
const shell = ref<InstanceType<typeof AnimeFloatingDialog>>()
const view = ref<AnimeSubscriptionView>(), loading = ref(false), error = ref(''), missing = ref(false), fetchedAt = ref(0), imageFailed = ref(false)
const expanded = ref<string[]>([])
const jobsVersion = ref(0), confirmRemove = ref(false), removeButton = ref<HTMLButtonElement>(), removeConfirm = ref<HTMLButtonElement>()
const { loading: acting, error: actionError, notice: actionNotice, pending: pendingAction, locked, execute, confirmPending } = useAnimeSubscriptionActions(props.token, props.id, async () => { confirmRemove.value = false; jobsVersion.value++; await load(true) })
const sources = computed(() => view.value ? subscriptionSources(view.value) : [])
const pendingCount = computed(() => (view.value?.form?.formSelections || []).reduce((n, item) => n + new Set([...(item.selectedReleaseIds || []), ...(item.unresolvedReleaseIds || [])]).size, 0))
const allowed = (action: string) => !!view.value?.permittedActions?.some(item => item.name === action && item.allowed)
const canEdit = computed(() => view.value && !view.value.firstEnabledAt && ['draft', 'paused'].includes(view.value.status) && !missing.value && !locked.value)
let controller: AbortController | undefined, disposed = false, refreshing = false, top = 0, userScrolled = false, restore = false, restoreVersion = 0
let restoreFrame: number | undefined
const stateKey = () => `detail:${props.id}`
function saveState() {
  if (ownerToken === portalToken.value && ownerID === portalUser.value?.id && portalUser.value?.role === 'admin') rememberSubscriptionState(stateKey(), { top, expanded: expanded.value })
}
function onScroll(value: number) { if (restore) return; top = value; userScrolled = true; saveState() }
async function restorePosition(value: number) {
  const epoch = ++restoreVersion
  restore = true
  await nextTick(); if (disposed || epoch !== restoreVersion) return
  shell.value?.scrollTo(value)
  if (restoreFrame !== undefined) cancelAnimationFrame(restoreFrame)
  restoreFrame = requestAnimationFrame(() => { restoreFrame = requestAnimationFrame(() => { restoreFrame = undefined; if (epoch === restoreVersion) restore = false }) })
}
async function load(force = false, silent = false) {
  controller?.abort(); const active = new AbortController(); controller = active
  if (!silent) { loading.value = false; error.value = '' }; missing.value = false
  if (!props.token || portalUser.value?.role !== 'admin') { view.value = undefined; return }
  const key = `subscriptions/${encodeURIComponent(props.id)}`, cached = cachedSubscription<AnimeSubscriptionView>(key)
  if (!force) { view.value = cached?.data || props.initial; fetchedAt.value = cached?.fetchedAt || 0 }
  if (cached?.fresh && !force) return
  refreshing = true; if (!silent) loading.value = true
  try {
    const data = await readSubscription<AnimeSubscriptionView>(key, props.token, active.signal)
    if (disposed || active.signal.aborted || controller !== active) return
    view.value = data; fetchedAt.value = Date.now(); error.value = ''
    if (!userScrolled) void restorePosition(top)
  } catch (reason) {
    if (disposed || active.signal.aborted || controller !== active || reason instanceof DOMException && reason.name === 'AbortError') return
    missing.value = reason instanceof AnimeConfigError && reason.status === 404
    if (missing.value) view.value = undefined
    error.value = missing.value ? '这条订阅不存在。' : animeConfigMessage(reason)
  } finally { if (!disposed && controller === active) { loading.value = false; refreshing = false } }
}
watch(() => [props.id, props.token, portalUser.value?.id, portalUser.value?.role], () => {
  const state = subscriptionState(stateKey()); expanded.value = [...state.expanded]; top = state.top; userScrolled = false; imageFailed.value = false
  if (!state.saved) expanded.value = [props.initial?.sources.find(s => s.role === 'main')?.id || 'main']
  void load(); void restorePosition(top)
}, { immediate: true })
function toggle(id: string) { expanded.value = expanded.value.includes(id) ? expanded.value.filter(value => value !== id) : [...expanded.value, id]; saveState() }
function isExpanded(source: AnimeSubscriptionView['sources'][number]) { return expanded.value.includes(source.id) || source.role === 'main' && expanded.value.includes('main') }
function toggleSource(source: AnimeSubscriptionView['sources'][number]) {
  if (source.role === 'main' && expanded.value.includes('main')) { expanded.value = expanded.value.filter(id => id !== 'main'); saveState() }
  else toggle(source.id)
}
function sourceState(source: AnimeSubscriptionView['sources'][number]) {
  if (['removed', 'archived'].includes(view.value?.status || '')) return '已停止'
  if (view.value?.status === 'paused' && view.value.firstEnabledAt) return '已暂停'
  if (!source.runWhenEnabled) return '保留配置'
  if (source.configuredState === 'draft') return '来源待配置'
  if (view.value?.status === 'enabled' && source.configuredState === 'enabled') return source.health === 'unavailable' ? '来源暂不可用' : source.health === 'cooldown' ? '来源冷却中' : '检查更新中'
  return '准备参与订阅'
}
function control(action: string, label: string) { if (view.value) void execute(`subscriptions/${encodeURIComponent(props.id)}/${action}`, { expectedRevision: view.value.revision }, label) }
async function askRemove() { confirmRemove.value = true; await nextTick(); removeConfirm.value?.focus() }
function cancelRemove() { confirmRemove.value = false; removeButton.value?.focus() }
useAnimeLocalPolling(() => load(true, true), () => controller?.abort(), () => !refreshing && !locked.value)
onBeforeUnmount(() => { saveState(); disposed = true; controller?.abort(); if (restoreFrame !== undefined) cancelAnimationFrame(restoreFrame) })
</script>

<template>
  <AnimeFloatingDialog ref="shell" title="订阅配置详情" close-label="关闭订阅详情" @close="emit('close')" @scroll="onScroll">
    <div class="as-detail" :aria-busy="loading">
      <div v-if="view" class="as-detail-heading">
        <div class="as-detail-poster"><img v-if="animeImageURL(view.entry.metadata?.coverUrl || '') && !imageFailed" :src="animeImageURL(view.entry.metadata.coverUrl)" :alt="view.entry.title" referrerpolicy="no-referrer" @error="imageFailed = true" /><IconPhotoOff v-else :size="28" aria-hidden="true" /></div>
        <div class="min-w-0"><span class="as-state" :data-status="view.status">{{ subscriptionStatus(view.status) }}</span><h3>{{ view.entry.title }}</h3><p v-if="view.entry.originalTitle !== view.entry.title">{{ view.entry.originalTitle }}</p><p>{{ view.entry.metadata?.airDate || '首播日期未收录' }}<span v-if="view.entry.bangumiId"> · Bangumi {{ view.entry.bangumiId }}</span></p><p>配置版本 {{ view.revision }} · 保存于 {{ animeLocalTime(view.updatedAt) }}</p></div>
      </div>
      <div class="as-detail-feedback" role="status"><span v-if="error">{{ error }}{{ view ? ' 已保留上次配置。' : '' }}</span><span v-else-if="loading">{{ view ? '正在核对最新配置…' : '正在读取订阅配置…' }}</span><span v-else-if="fetchedAt">读取于 {{ animeLocalTime(fetchedAt) }}</span><button v-if="!missing" type="button" class="as-text-button" :disabled="loading" @click="load(true)"><IconRefresh :size="14" aria-hidden="true" />{{ error ? '重试' : '刷新配置' }}</button></div>
      <template v-if="view">
        <section class="as-detail-section"><h4>本地篇章</h4><p class="as-muted">{{ view.entry.editionKey === 'original' ? '原始篇章' : view.entry.editionKey || '尚未设置篇章标识' }}</p><p class="as-muted">{{ view.firstEnabledAt ? `首次启用于 ${animeLocalTime(view.firstEnabledAt)}` : '仅保存订阅配置，尚未开始检查更新与下载。' }}</p></section>
        <div class="as-actions" aria-label="订阅操作">
          <button v-if="allowed('pause') && view.status === 'enabled'" type="button" class="as-action" :disabled="locked" @click="control('pause', '暂停订阅')">暂停订阅</button>
          <button v-if="allowed('resume')" type="button" class="as-action as-action-primary" :disabled="locked" @click="control('resume', '恢复订阅')">恢复订阅</button>
          <button v-if="allowed('archive') && view.status !== 'archived'" type="button" class="as-action" :disabled="locked" @click="control('archive', '归档订阅')">归档</button>
          <button v-if="allowed('unarchive') && view.status === 'archived'" type="button" class="as-action" :disabled="locked" @click="control('unarchive', '取消归档')">取消归档</button>
          <button v-if="allowed('remove') && view.status !== 'removed' && !confirmRemove" ref="removeButton" type="button" class="as-text-button as-danger" :disabled="locked" @click="askRemove">移除订阅</button>
        </div>
        <div v-if="confirmRemove" class="as-inline-confirm" @keydown.esc.stop.prevent="cancelRemove"><p>确认移除此订阅？停止后续下载并保留执行记录，不删除网盘文件。</p><div class="as-actions"><button ref="removeConfirm" type="button" class="as-action as-danger" :disabled="locked" @click="control('remove', '移除订阅')">确认移除</button><button type="button" class="as-action" :disabled="acting" @click="cancelRemove">取消</button></div></div>
        <div class="as-operation-feedback" role="status"><p v-if="actionError">{{ actionError }}</p><p v-else-if="acting">正在记录操作…</p><p v-else-if="actionNotice">{{ actionNotice }}</p><template v-if="pendingAction && !acting"><p>“{{ pendingAction.label }}”结果尚未确认，已保留原请求。核对时继续使用原请求标识和内容。</p><button type="button" class="as-text-button" @click="confirmPending">核对原操作</button></template><button v-if="actionError && !pendingAction" type="button" class="as-text-button" :disabled="loading" @click="load(true)">重载最新配置</button></div>
        <p v-if="view.firstEnabledAt && ['paused', 'archived'].includes(view.status)" class="as-muted">已停止新提交；已被 115 接收的任务继续只读追踪，不删除网盘文件。</p>
        <AnimeSubscriptionActivation v-if="allowed('enable')" :token="token" :view="view" :locked="locked" @enable="body => execute(`subscriptions/${encodeURIComponent(id)}/enable`, body, '启用订阅')" />
        <AnimeDownloadJobs :token="token" :id="id" :locked="locked" :version="jobsVersion" @changed="load(true, true)" />
        <AnimeOrganizationSettings v-if="!['removed', 'archived'].includes(view.status)" :token="token" :view="view" :locked="locked" @changed="load(true, true)" />
        <section class="as-detail-section"><h4>RSS 来源 <span>{{ sources.length }}</span></h4><p v-if="!sources.length" class="as-muted">还没有配置 RSS 来源，可进入编辑补充。</p>
          <div class="as-source-list"><section v-for="source in sources" :key="source.id" class="as-source">
            <button type="button" :aria-expanded="isExpanded(source)" :aria-controls="`source-${source.id}`" @click="toggleSource(source)"><span><span class="as-source-role">{{ source.role === 'main' ? '主 RSS' : `备用 ${source.position}` }}</span><strong>{{ source.groupName || '字幕组待配置' }}</strong><span class="as-muted">{{ sourceState(source) }}</span></span><IconChevronDown :size="18" aria-hidden="true" :class="{ 'rotate-180': isExpanded(source) }" /></button>
            <dl v-show="isExpanded(source)" :id="`source-${source.id}`" class="as-source-fields">
              <div><dt>资源站点</dt><dd>{{ animeProviderName(source.provider) }}<span v-if="animeOriginLabel(source)"> · {{ animeOriginLabel(source) }}</span></dd></div>
              <div><dt>发布范围</dt><dd>{{ source.binding.scope?.kind === 'range' ? `第 ${source.binding.scope.first} 至 ${source.binding.scope.last} 话` : source.binding.scope?.kind === 'confirmed_chapter' ? source.binding.scope.evidence || '已确认整个来源属于本篇章' : '尚未确认' }}</dd></div>
              <div><dt>篇章编号</dt><dd>{{ source.binding.chapterCorrespondence ? `发布 ${source.binding.chapterCorrespondence.sourceEpisode} → 篇章 ${source.binding.chapterCorrespondence.chapterEpisode}` : '尚未设置对应关系' }}</dd></div>
              <div><dt>资源规格</dt><dd>{{ source.rules.specMode === 'profiles' ? source.rules.specProfiles?.map(animeSpecLabel).join('、') : source.rules.specMode === 'any' ? '全部规格' : source.rules.resolution === 'any' ? '不限画质' : source.rules.resolution || '尚未设置' }}</dd></div>
              <div><dt>历史策略</dt><dd>{{ source.rules.initialMode === 'new_only' ? '仅后续新发布' : source.rules.initialMode === 'selected_history' ? '手动预选历史资源' : `从发布第 ${source.rules.minimumEpisode ?? '未设置'} 话开始` }}</dd></div>
              <div v-if="source.rules.include.length"><dt>包含关键词</dt><dd>{{ source.rules.include.join('、') }}</dd></div><div v-if="source.rules.exclude.length"><dt>排除关键词</dt><dd>{{ source.rules.exclude.join('、') }}</dd></div>
              <div><dt>最近检查</dt><dd>{{ source.checkedAt ? animeLocalTime(source.checkedAt) : '尚未执行' }}<span v-if="source.health && source.health !== 'ok'" class="as-muted"> · {{ source.health === 'unavailable' ? '来源不可用' : source.health === 'cooldown' ? '来源冷却中' : '等待检查' }}</span></dd></div>
            </dl>
          </section></div>
        </section>
        <p v-if="pendingCount && !view.firstEnabledAt" class="as-saved-selections">已保存 {{ pendingCount }} 条待核对预选。尚未下载，执行前需要重新预览。</p>
        <p v-if="view.readiness && !view.readiness.ready" class="as-muted mt-4">配置尚未完整，可在编辑页补充来源或下载目录。</p>
        <RouterLink v-if="canEdit" :to="{ path: `/anime/subscriptions/${encodeURIComponent(id)}/edit`, query: { returnTo: returnPath } }" class="as-edit-link">编辑订阅配置</RouterLink>
      </template>
    </div>
  </AnimeFloatingDialog>
</template>
