<script setup lang="ts">
import { IconFolder, IconLoader2 } from '@tabler/icons-vue'
import DownloadOrganizeDialog from './DownloadOrganizeDialog.vue'
import { copyText } from '../utils/clipboard'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import MikanFilterSelect from './MikanFilterSelect.vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { useStickySurface } from '../composables/useStickySurface'
import { useCloudDownloadBatch } from '../composables/useCloudDownloadBatch'
import { magnetKey } from '../lib/cloud-download-batch'
import mikanMascot from '../assets/mikan/mikan-pic.svg?no-inline'
import mikanWordmark from '../assets/mikan/mikan-text.svg?no-inline'
import AnimeProviderBrand from './AnimeProviderBrand.vue'
import { createDialogLifecycle } from '../lib/dialog-lifecycle'

import { useAnimeResourceSearch } from '../composables/useAnimeResourceSearch'
import { useAnimeResourceActions } from '../composables/useAnimeResourceActions'
import type { AnimeResourceItem as Item, AnimeResourceBangumi as Bangumi } from '../types/animeResourceSearch'

const props = withDefaults(defineProps<{ token: string; title: string; originalTitle: string; provider?: 'mikan' | 'garden'; animationLibrary?: boolean; themeColors?: Record<string, string>; tmdbId?: number; kind?: 'movie' | 'tv'; date?: string; seasons?: { number: number; name: string; episodeCount: number }[] }>(), { provider: 'mikan' })
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const viewportStyle = ref<Record<string, string>>({})
const dialogStyle = computed(() => ({ ...props.themeColors, ...viewportStyle.value }))
const shortViewport = ref(false)
const scrollBody = ref<HTMLElement>()
const filterAnchor = ref<HTMLElement>()
const filtersStuck = useStickySurface(scrollBody, filterAnchor)
const copied = ref('')
const copyError = ref('')
const brokenCovers = ref<Record<string, boolean>>({})
const coverRetries = ref<Record<string, number>>({})
const lifecycle = new AbortController()
const batch = useCloudDownloadBatch(props.token, lifecycle.signal)
const { mode: batchMode, running: batchRunning, selected: batchSelected, notice: batchNotice, count: batchCount } = batch

const searchState = useAnimeResourceSearch(props, lifecycle.signal, {
  batchMode,
  blocked: () => actions.busy.value,
  resetResultPosition,
  appendedResults: focusAppendedResults,
})
const { isGarden, interactionBusy, query, group, groups, result, busy, loadingMessage, error, filter, resolution, specOptions, specActive, resetSpecFilters, visible, selectedBangumi, bangumis, expansions, groupOptions, resolutionOptions, filtered, siteURL, specChecked, toggleSpecOption, search, searchGarden, selectGroup, useTitle, backToSearch, loadBangumi, selectBangumi } = searchState
const actions = useAnimeResourceActions({ ...props, searching: () => busy.value, filtered: () => filtered.value, groups: () => groups.value }, lifecycle.signal, batch)
const { preparingItem, singleSubmitting, submissions, submission, canSelect, availableSelection, allFilteredSelected, hiddenSelectionCount, toggleResource, allowReselectAfterCheck, organizeEntries, organizingBatch, organizeHiddenCount, prepareNotices, openBatchOrganize, openOrganize, organizeUncertain, organized, submit } = actions
const providerName = computed(() => isGarden.value ? 'Garden' : '蜜柑')
const itemStrip = ref<HTMLElement>()
const activeBangumiId = computed(() => selectedBangumi.value?.id || 'all')
const { indicatorStyle: itemIndicatorStyle } = useSlidingIndicator(itemStrip, '[aria-pressed="true"]', [activeBangumiId, bangumis])
const hasBrokenCovers = computed(() => bangumis.value.some(card => brokenCovers.value[card.id]))
const dialogLifecycle = createDialogLifecycle()
function selectCard(item: Item, event: MouseEvent) {
  if (!batchMode.value || (event.target as HTMLElement).closest('button, a, input, textarea, label, summary')) return
  toggleResource(item)
}
async function startBatch() {
  if (interactionBusy.value || singleSubmitting.value) return
  const task = batch.submit()
  await nextTick()
  dialog.value?.querySelector<HTMLElement>('.mikan-batch-actions button')?.focus({ preventScroll: true })
  await task
  await nextTick()
  // 队列结束会移除停止按钮；将焦点留在稳定的摘要上，避免回落到页面。
  if (document.activeElement === document.body) dialog.value?.querySelector<HTMLElement>('.mikan-batch-count')?.focus({ preventScroll: true })
}
function closeDialog() { if (organizeEntries.value.length) return; batch.stop(); emit('close') }
function refreshCurrent() { retryCovers(); searchState.refreshCurrent() }
async function focusAppendedResults(previousVisible: number) {
  await nextTick()
  dialog.value?.querySelectorAll<HTMLElement>('.mikan-resource')[previousVisible]?.querySelector<HTMLElement>('button, a')?.focus({ preventScroll: true })
}
function retryCovers() {
  for (const card of bangumis.value) {
    if (!brokenCovers.value[card.id]) continue
    coverRetries.value[card.id] = Date.now()
    delete brokenCovers.value[card.id]
  }
}
function coverSource(card: Bangumi) {
  // 只允许本站封面接口，错误或旧版远程地址不回退为浏览器直连。
  if (!/^\/api\/mikan\/image\/\d{6}\/[a-zA-Z0-9_-]+\.(?:jpg|jpeg|png|webp)$/i.test(card.coverUrl)) return ''
  return card.coverUrl + (coverRetries.value[card.id] ? `?retry=${coverRetries.value[card.id]}` : '')
}
async function resetResultPosition() {
  await nextTick()
  const body = dialog.value?.querySelector<HTMLElement>('.mikan-body')
  const anchor = filterAnchor.value
  if (body && anchor) body.scrollTop = Math.min(body.scrollTop, anchor.offsetTop)
}
async function showMore() {
  const previousCount = Math.min(visible.value, filtered.value.length)
  visible.value += 50
  await nextTick()
  const row = dialog.value?.querySelectorAll<HTMLElement>('.mikan-resource')[previousCount]
  row?.querySelector<HTMLElement>('button, a')?.focus({ preventScroll: true })
  const body = dialog.value?.querySelector<HTMLElement>('.mikan-body')
  const controls = dialog.value?.querySelector<HTMLElement>('.mikan-filter-controls')
  if (body && row) {
    const contentTop = controls && getComputedStyle(controls).position === 'sticky' ? controls.getBoundingClientRect().bottom : body.getBoundingClientRect().top
    body.scrollTop += row.getBoundingClientRect().top - contentTop - 12
  }
}
async function copy(item: Item) {
  copyError.value = ''; copied.value = ''
  try { await copyText(item.magnet, dialog.value); copied.value = item.id }
  catch { copied.value = ''; copyError.value = item.id }
}
function syncVisualViewport() {
  const viewport = window.visualViewport
  shortViewport.value = (viewport?.height ?? window.innerHeight) < 520
  viewportStyle.value = {
    '--mikan-visible-height': `${viewport?.height ?? window.innerHeight}px`,
    '--mikan-visible-top': `${viewport?.offsetTop ?? 0}px`,
    '--mikan-visible-bottom': `${viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0}px`,
  }
  if (shortViewport.value && dialog.value?.contains(document.activeElement) && document.activeElement instanceof HTMLInputElement) {
    void nextTick(() => (document.activeElement as HTMLInputElement)?.scrollIntoView({ block: 'nearest' }))
  }
}
onMounted(() => {
  syncVisualViewport()
  window.visualViewport?.addEventListener('resize', syncVisualViewport)
  window.visualViewport?.addEventListener('scroll', syncVisualViewport)
  window.addEventListener('resize', syncVisualViewport)
  dialogLifecycle.acquire()
  dialog.value?.showModal()
  void search()
})
onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', syncVisualViewport)
  window.visualViewport?.removeEventListener('scroll', syncVisualViewport)
  window.removeEventListener('resize', syncVisualViewport)
  lifecycle.abort()
  dialog.value?.close()
  dialogLifecycle.release()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :style="dialogStyle" aria-labelledby="mikan-search-title" aria-describedby="mikan-work-title" class="mikan-dialog media-search-dialog" :class="{ 'mikan-short-viewport': shortViewport }" @cancel.prevent="closeDialog">
      <div class="mikan-layout">
        <header class="mikan-header" data-mikan-brand>
          <div v-if="!isGarden" class="mikan-brand" aria-hidden="true"><img :src="mikanMascot" alt="" width="400" height="400" draggable="false" /></div>
          <div class="mikan-heading">
            <h2 id="mikan-search-title"><span class="sr-only">{{ providerName }}搜索</span><AnimeProviderBrand v-if="isGarden" provider="garden" /><img v-else :src="mikanWordmark" alt="" width="117" height="29" draggable="false" /></h2>
            <p id="mikan-work-title" :title="title" data-mikan-brand-title>{{ title }}</p>
          </div>
          <button autofocus type="button" :aria-label="batchRunning ? `停止提交并关闭${providerName}搜索` : `关闭${providerName}搜索`" class="mikan-icon-button" @click="closeDialog"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        </header>
        <div ref="scrollBody" class="mikan-body">
          <form class="mikan-search-form" @submit.prevent="search()">
            <input v-model="query" :aria-label="`${providerName}搜索词`" :maxlength="isGarden ? 128 : 200" :readonly="interactionBusy" :aria-busy="interactionBusy" class="mikan-input" placeholder="输入片名、原名或其他译名" />
            <button :disabled="interactionBusy || !query.trim()" class="mikan-button mikan-primary-button">搜索</button>
            <button type="button" :disabled="interactionBusy || (!selectedBangumi && !query.trim())" aria-label="强制刷新" title="强制刷新，重新读取原站资源" class="mikan-tool-button mikan-refresh-button" @click="refreshCurrent"><svg aria-hidden="true" :class="{ 'mikan-loading-spinner': busy }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg><span>强制刷新</span></button>
          </form>
          <div class="mikan-search-tools">
            <button type="button" :disabled="interactionBusy" class="mikan-tool-button" @click="useTitle(title)">用中文标题</button>
            <button v-if="originalTitle && originalTitle !== title" type="button" :disabled="interactionBusy" class="mikan-tool-button" @click="useTitle(originalTitle)">用原名</button>
            <a :href="siteURL" target="_blank" rel="noopener noreferrer" class="mikan-tool-button mikan-site-link"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg>{{ selectedBangumi ? '番剧原页' : `${providerName}原站` }}</a>
          </div>
          <section v-if="bangumis.length" class="mikan-item-picker" aria-label="蜜柑相关番剧">
            <div class="mikan-picker-heading">
              <h3>影视条目</h3>
              <span>{{ bangumis.length }} 个番剧<span class="mikan-picker-hint">，全部条目显示关键词搜索结果</span></span>
              <button v-if="hasBrokenCovers" type="button" class="mikan-tool-button mikan-cover-retry" @click="retryCovers">重试封面</button>
            </div>
            <nav ref="itemStrip" aria-label="蜜柑影视条目" :aria-busy="interactionBusy" class="mikan-item-strip">
              <div class="mikan-item-indicator" :style="itemIndicatorStyle" aria-hidden="true"></div>
              <button type="button" class="mikan-item-card mikan-all-items" :aria-disabled="interactionBusy" :aria-pressed="activeBangumiId === 'all'" aria-label="全部条目：显示关键词搜索结果" title="恢复关键词搜索结果与筛选" @click="backToSearch">
                <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></svg>
                <span><strong>全部条目</strong><small>关键词搜索结果</small></span>
              </button>
              <button v-for="card in bangumis" :key="card.id" type="button" :aria-disabled="interactionBusy" :aria-pressed="activeBangumiId === card.id" :aria-label="`查看番剧：${card.title}`" :title="card.title" class="mikan-item-card" @click="selectBangumi(card)">
                <span class="mikan-item-cover">
                  <img v-if="coverSource(card) && !brokenCovers[card.id]" :src="coverSource(card)" alt="" loading="lazy" referrerpolicy="no-referrer" @error="brokenCovers[card.id] = true" />
                  <svg v-else aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="4" y="3" width="16" height="18" rx="2" /><path d="m9 8 6 4-6 4Z" /></svg>
                </span>
                <span class="mikan-item-name"><strong>{{ card.title }}</strong><small>蜜柑番剧资源</small></span>
              </button>
            </nav>
          </section>
          <div v-if="!result && (busy || error)" class="mikan-initial-feedback">
            <p v-if="busy" role="status" class="mikan-loading">
              <svg aria-hidden="true" class="mikan-loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M20 12a8 8 0 1 1-4-6.9" /></svg>
              <span>{{ loadingMessage }}</span>
            </p>
            <div v-else-if="error" class="mikan-initial-error">
              <div role="alert"><strong>搜索暂时失败</strong><p class="mikan-load-error">{{ error }}</p></div>
              <button type="button" :disabled="!query.trim()" class="mikan-button mikan-outlined-button" @click="search(true)">重新搜索</button>
            </div>
          </div>
          <p v-if="result && !selectedBangumi && result.attempts.length > 1" role="status" class="mikan-search-note">中文标题未找到资源，已尝试原名：{{ result.query }}</p>
          <div v-if="result" ref="filterAnchor" class="mikan-filter-anchor search-filter-anchor" aria-hidden="true"></div>
          <section v-if="result" :aria-label="`${providerName}结果筛选`" class="mikan-filter-controls search-filter-surface" :class="{ 'is-stuck': filtersStuck && !shortViewport }">
            <div class="mikan-filter-row">
              <span class="mikan-filter-label" aria-hidden="true">{{ isGarden ? '发布方' : '字幕组' }}</span>
              <MikanFilterSelect :model-value="group" :options="groupOptions" :label="`${providerName}${isGarden ? '发布方' : '字幕组'}`" :disabled="interactionBusy" @update:model-value="selectGroup" />
            </div>
            <div v-if="!batchMode" class="mikan-filter-row">
              <span class="mikan-filter-label" aria-hidden="true">分辨率</span>
              <MikanFilterSelect v-model="resolution" :options="resolutionOptions" :label="`${providerName}分辨率`" :disabled="interactionBusy" />
            </div>
            <input v-model="filter" :aria-label="`筛选${providerName}结果`" :placeholder="isGarden ? '已加载结果内筛选：合集 / 第二季 / 简繁…' : '结果内筛选：合集 / 第二季 / 简繁…'" :readonly="interactionBusy" class="mikan-input mikan-result-input" />
          </section>
          <template v-if="result">
            <div class="mikan-result-summary">
              <p>匹配 {{ filtered.length }} / {{ selectedBangumi || isGarden ? '已加载' : '已获取' }} {{ result.items.length }} 条<span v-if="isGarden || result.more?.length">，仅筛选已加载资源</span></p>
              <p v-if="busy" role="status" class="mikan-loading" :title="loadingMessage"><svg aria-hidden="true" class="mikan-loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M20 12a8 8 0 1 1-4-6.9" /></svg><span>更新中，暂显旧结果</span></p>
              <details v-else-if="error" role="alert" class="mikan-load-error mikan-error-details"><summary><span>更新失败，已保留原结果</span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 6 6 6-6 6" /></svg></summary><p>{{ error }}</p></details>
              <button type="button" class="mikan-tool-button mikan-batch-entry" :disabled="interactionBusy" :aria-pressed="batchMode" @click="batchMode ? batch.exit() : batchMode = true"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="6" height="6" rx="1.5" /><path d="M13 7h8M13 17h8m-17 0 2 2 4-5" /></svg>{{ batchMode ? '退出选择' : '批量选择' }}</button>
            </div>
            <div v-if="batchMode" class="mikan-selection-tools">
              <label class="mikan-select-all"><input type="checkbox" :checked="allFilteredSelected" :indeterminate="!allFilteredSelected && availableSelection.some(item => batchSelected.has(magnetKey(item.magnet)))" :disabled="interactionBusy || !availableSelection.length" @change="batch.selectAll(availableSelection)" /><span>全选当前筛选</span><small>{{ availableSelection.length }} 项</small></label>
              <div v-if="specOptions.length" class="mikan-spec-options" role="group" aria-label="资源规格筛选">
                <span class="mikan-spec-label">筛选</span>
                <label v-for="option in specOptions" :key="`${option.group}:${option.value}`" class="mikan-spec-chip"><input type="checkbox" :aria-label="`筛选${option.label}`" :checked="specChecked(option)" :disabled="interactionBusy" @change="toggleSpecOption(option)" /><span>{{ option.label }}</span></label>
                <button type="button" class="mikan-tool-button" aria-label="重置规格筛选" :disabled="interactionBusy || !specActive" @click="resetSpecFilters">重置</button>
              </div>
              <button type="button" class="mikan-tool-button" :disabled="interactionBusy || !batchCount" @click="batch.clear">清空选择</button>
            </div>
            <p v-if="!result.items.length" class="mikan-empty">{{ isGarden ? 'Garden 未找到资源，可修改片名、尝试原名或调整来源及发布方。' : selectedBangumi ? '该番剧当前没有可读取的资源，可返回全部条目或访问原站。' : bangumis.length ? '关键词未找到磁力资源，可点击上方相关番剧继续查看。' : '蜜柑未找到资源，可修改片名、尝试其他译名或访问原站。' }}</p>
            <p v-else-if="!filtered.length" class="mikan-empty">没有符合筛选条件的资源，请调整关键词或规格筛选。</p>
            <ul class="mikan-resource-list" :aria-label="`${providerName}资源列表`">
              <li v-for="item in filtered.slice(0, visible)" :key="item.id" class="mikan-resource" :class="{ 'mikan-selectable': batchMode, 'mikan-resource-selected': batchMode && batchSelected.has(magnetKey(item.magnet)) }" @click="selectCard(item, $event)">
                <label v-if="batchMode" class="mikan-resource-checkbox" @click.stop><input type="checkbox" :checked="batchSelected.has(magnetKey(item.magnet))" :disabled="interactionBusy || !canSelect(item)" :aria-label="`选择资源：${item.title}`" @change="toggleResource(item)" /><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="5" /><path v-if="batchSelected.has(magnetKey(item.magnet))" d="m7 12 3 3 7-7" /></svg></label>
                <div class="mikan-resource-main">
                  <div class="mikan-resource-title">
                    <h4>{{ item.title }}</h4>
                    <button type="button" class="mikan-copy-button" @click="copy(item)"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V4H4v12h4" /></svg>{{ copied === item.id ? '已复制' : '复制磁力' }}</button>
                  </div>
                  <div class="mikan-resource-meta"><span class="mikan-resolution">{{ item.resolution }}</span><span>{{ item.size }}</span><span :title="'原站时间：' + item.updatedAt">{{ item.updatedAt }}</span><template v-if="isGarden"><span>{{ item.originName }}</span><span :title="`发布者：${item.publisher}`">{{ item.fansub || item.publisher }}</span></template></div>
                  <div class="mikan-resource-actions">
                    <a :href="item.url" target="_blank" rel="noopener noreferrer" class="mikan-button mikan-outlined-button" :aria-label="isGarden ? '查看Garden详情' : '查看蜜柑原帖'"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg><span class="mikan-action-label-desktop">{{ isGarden ? '查看详情' : '查看原帖' }}</span><span class="mikan-action-label-mobile">{{ isGarden ? '详情' : '原帖' }}</span></a>
                    <button v-if="tmdbId && !batchMode && (isGarden || item.torrentUrl)" type="button" class="mikan-button mikan-outlined-button" aria-label="115 整理" :aria-busy="preparingItem === item.id" :disabled="interactionBusy || singleSubmitting || submission(item)?.success || submission(item)?.uncertain" @click="openOrganize(item, $event)"><component :is="preparingItem === item.id ? IconLoader2 : IconFolder" :size="16" aria-hidden="true" :class="{ 'mikan-loading-spinner': preparingItem === item.id }" /><span class="mikan-action-label-desktop">{{ preparingItem === item.id ? '解析中…' : '115 整理' }}</span><span class="mikan-action-label-mobile">{{ preparingItem === item.id ? '解析…' : '整理' }}</span></button>
                    <button type="button" :disabled="interactionBusy || submission(item)?.busy || submission(item)?.success || submission(item)?.uncertain" class="mikan-button mikan-primary-button" aria-label="115 云下载" @click="submit(item)"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M6 17a4 4 0 0 1-1-7.8A6 6 0 0 1 17 8a4.5 4.5 0 0 1 1 9M12 10v11m-4-4 4 4 4-4" /></svg><template v-if="submission(item)?.busy || submission(item)?.success || submission(item)?.uncertain"><span>{{ submission(item)?.busy ? '提交中' : submission(item)?.success ? '已提交' : '待核对' }}</span></template><template v-else><span class="mikan-action-label-desktop">115 云下载</span><span class="mikan-action-label-mobile">云下载</span></template></button>
                  </div>
                </div>
                <div v-if="copyError === item.id" role="alert" class="mikan-notice mikan-warning"><p>浏览器未允许复制，请手动复制下方磁力：</p><textarea :value="item.magnet" readonly aria-label="手动复制磁力" @focus="($event.target as HTMLTextAreaElement).select()" /></div>
                <p v-if="preparingItem === item.id" role="status" class="mikan-notice garden-prepare-notice">{{ isGarden ? '读取 Garden 文件清单…' : '正在解析蜜柑种子清单…' }}</p>
                <p v-if="prepareNotices[item.id]" role="status" class="mikan-notice garden-prepare-notice">{{ prepareNotices[item.id] }} <button type="button" class="mikan-tool-button" :disabled="interactionBusy || submission(item)?.busy || submission(item)?.success || submission(item)?.uncertain" @click="submit(item)">普通离线</button></p>
                <p v-if="copied === item.id" role="status" class="sr-only">磁力已复制</p>
                <p v-if="submission(item)?.message" role="status" class="mikan-notice" :class="submission(item)?.success ? 'mikan-success' : submission(item)?.uncertain ? 'mikan-warning' : 'mikan-error'">{{ submission(item)?.message }}<RouterLink v-if="submission(item)?.needsConfig || submission(item)?.uncertain" :to="submission(item)?.uncertain && submissions[magnetKey(item.magnet)]?.organize ? '/115/download-organize' : '/115/cloud-download'" @click="closeDialog">{{ submission(item)?.uncertain ? '核对任务' : '前往配置' }}</RouterLink></p>
              </li>
            </ul>
            <div v-if="filtered.length > visible" class="mikan-list-end"><button type="button" class="mikan-button mikan-outlined-button" @click="showMore">显示更多资源</button><span>还有 {{ filtered.length - visible }} 条，筛选包含已获取的全部结果</span></div>
            <div v-if="isGarden && result.hasMore && filtered.length <= visible" class="mikan-list-end"><button type="button" class="mikan-button mikan-outlined-button" :disabled="interactionBusy" @click="searchGarden(false, group, (result.page || 1) + 1)">读取更多搜索结果</button><p v-if="error" role="status" class="mikan-load-error">{{ error }}</p></div>
            <section v-if="selectedBangumi && expansions.length" aria-label="加载更多番剧资源" class="mikan-expansions">
              <p>部分字幕组还有更多资源，可继续读取。</p>
              <div>
                <template v-for="more in expansions" :key="more.groupId"><a v-if="more.take > 10000" :href="siteURL" target="_blank" rel="noopener noreferrer" class="mikan-button mikan-outlined-button">原站查看：{{ groups.find(g => g.id === more.groupId)?.name || more.groupId }}</a><button v-else type="button" :disabled="interactionBusy" class="mikan-button mikan-outlined-button" @click="loadBangumi(selectedBangumi!, false, more)">加载更多：{{ groups.find(g => g.id === more.groupId)?.name || more.groupId }}</button></template>
              </div>
            </section>
          </template>
        </div>
        <footer v-if="batchMode" class="mikan-batch-footer" aria-label="批量云下载操作">
          <div class="mikan-batch-count" tabindex="-1" aria-live="polite" aria-atomic="true"><strong>{{ batchRunning ? `已处理 ${batch.processed.value} / ${batch.total.value}` : `已选 ${batchCount} 项` }}</strong><span v-if="!batchRunning && hiddenSelectionCount">含 {{ hiddenSelectionCount }} 项不在当前筛选</span><span v-else-if="!batchRunning">保存至 115 已配置目录</span><span v-else>逐条提交到 115</span></div>
          <div class="mikan-batch-actions"><button v-if="batchRunning" type="button" class="mikan-button mikan-outlined-button" @click="batch.stop">停止提交</button><template v-else><button v-if="tmdbId" type="button" class="mikan-button mikan-outlined-button" :disabled="interactionBusy || singleSubmitting || !batchCount" @click="openBatchOrganize"><IconFolder :size="16" aria-hidden="true" />批量整理（{{ batchCount }}）</button><button type="button" class="mikan-button mikan-primary-button" :aria-label="`提交到 115（${batchCount}）`" :disabled="interactionBusy || singleSubmitting || !batchCount" @click="startBatch"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 17a4 4 0 0 1-1-7.8A6 6 0 0 1 17 8a4.5 4.5 0 0 1 1 9M12 10v11m-4-4 4 4 4-4" /></svg><span class="mikan-action-label-desktop">提交到 115（{{ batchCount }}）</span><span class="mikan-action-label-mobile">云下载（{{ batchCount }}）</span></button></template></div>
          <div v-if="batchRunning" class="mikan-batch-progress" role="progressbar" :aria-valuenow="batch.processed.value" :aria-valuemax="batch.total.value" aria-valuemin="0" aria-label="云下载提交进度"><span :style="{ width: `${batch.total.value ? batch.processed.value / batch.total.value * 100 : 0}%` }"></span></div>
          <p v-if="batchNotice" role="status" class="mikan-batch-notice">{{ batchNotice }}<RouterLink v-if="batch.needsConfig.value" to="/115/cloud-download" @click="closeDialog">前往配置</RouterLink></p>
          <div v-if="!batchRunning && batch.uncheckedCount.value" class="mikan-batch-check"><RouterLink to="/115/cloud-download" target="_blank" rel="noopener noreferrer">核对 {{ batch.uncheckedCount.value }} 项任务</RouterLink><button type="button" class="mikan-tool-button" @click="allowReselectAfterCheck">已核对，允许重选</button></div>
        </footer>
        <footer v-else class="search-log-footer"><span>115 云下载保存至已配置的下载目录。</span><RouterLink :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click="closeDialog">查看日志</RouterLink></footer>
      </div>
    </dialog>
  </Teleport>
  <DownloadOrganizeDialog :animation-library="animationLibrary" :original-title="originalTitle" v-if="organizeEntries.length" :token="token" :entries="organizeEntries" :batch="organizingBatch" :hidden-count="organizeHiddenCount" :tmdb-id="tmdbId!" :kind="kind || 'tv'" :title="title" :date="date || ''" :seasons="seasons || []" :theme-colors="themeColors" @close="organizeEntries = []" @submitted="organized" @uncertain="organizeUncertain" />
</template>

<style scoped>
.garden-prepare-notice { color: var(--search-dialog-muted, #64748b); }

.mikan-dialog {
  --mikan-max-height: 90dvh;
  --mikan-success: #246044; --mikan-success-bg: #e6f2e9;
  --mikan-warning: #765500; --mikan-warning-bg: #fff1cd;
  --mikan-error: #a12b2b; --mikan-error-bg: #ffe9e7;
  margin: auto; padding: 0; width: min(1080px, calc(100% - 48px)); max-width: none; max-height: var(--mikan-max-height);
  border-radius: 28px; color: var(--search-dialog-text); color-scheme: light;
  overflow: hidden;
}
.dark .mikan-dialog { --mikan-success: #a5d8b8; --mikan-success-bg: #223d31; --mikan-warning: #eed085; --mikan-warning-bg: #42371e; --mikan-error: #ffb4ab; --mikan-error-bg: #502c2b; color-scheme: dark; }
.mikan-layout { display: flex; flex-direction: column; max-height: var(--mikan-max-height); }
.mikan-header { display: flex; flex-shrink: 0; align-items: center; gap: 14px; padding: 24px 24px 12px; }
.mikan-brand { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border-radius: 14px; background: var(--search-dialog-card); }
.mikan-brand img { width: 40px; height: 40px; object-fit: contain; }
.mikan-heading { flex: 1; min-width: 0; }
.mikan-heading h2 { display: flex; align-items: center; height: 28px; margin: 0; }
.mikan-heading h2 img { width: auto; height: 22px; }
.mikan-heading p { margin: 4px 0 0; color: var(--search-dialog-muted); font-size: 13px; line-height: 20px; overflow-wrap: anywhere; }
.mikan-icon-button { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border: 0; border-radius: 50%; background: transparent; color: var(--search-dialog-muted); }
.mikan-icon-button svg { width: 24px; height: 24px; }
.mikan-body { position: relative; min-height: 0; padding: 0 24px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.mikan-search-form { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.mikan-search-form > input { flex: 1; width: 0; min-width: 100px; }
.mikan-search-form > .mikan-primary-button { min-width: 64px; flex-shrink: 0; }
.mikan-input { min-width: 0; min-height: 44px; padding: 10px 12px; border: 0; border-radius: 12px; background: var(--search-dialog-card); color: var(--search-dialog-text); font-size: 16px; line-height: 24px; }
.mikan-input::placeholder { color: var(--search-dialog-muted); opacity: 1; }
.mikan-input[readonly] { cursor: wait; }
.mikan-input:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: -2px; }
.mikan-search-tools { display: flex; flex-wrap: wrap; align-items: center; gap: 0 4px; }
.mikan-tool-button { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; gap: 6px; min-height: 44px; padding: 0 10px; border: 0; border-radius: 24px; background: transparent; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; text-decoration: none; white-space: nowrap; }
.mikan-tool-button svg { width: 18px; height: 18px; margin: 0; }
.mikan-site-link { margin-left: auto; }
.mikan-cover-retry { min-height: 32px; }
.mikan-search-note { margin: 8px 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.mikan-item-picker { min-width: 0; margin-top: 8px; }
.mikan-picker-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 8px; }
.mikan-picker-heading h3 { margin: 0; color: var(--search-dialog-text); font-size: 13px; line-height: 20px; font-weight: 600; }
.mikan-picker-heading > span { color: var(--search-dialog-muted); font-size: 11px; }
.mikan-picker-heading > button { margin-left: auto; }
.mikan-item-strip { position: relative; isolation: isolate; display: flex; gap: 10px; padding: 4px 2px 12px; overflow-x: auto; scrollbar-color: var(--search-dialog-outline) transparent; scrollbar-width: thin; }
.mikan-item-strip::-webkit-scrollbar { height: 4px; }
.mikan-item-strip::-webkit-scrollbar-thumb { border-radius: 999px; background: var(--search-dialog-outline); }
.mikan-item-indicator { position: absolute; z-index: 1; border-radius: 16px; background: var(--search-dialog-action-background); pointer-events: none; }
.mikan-item-card { position: relative; display: flex; flex-shrink: 0; align-items: center; gap: 10px; width: 220px; min-height: 92px; padding: 8px; border: 2px solid transparent; border-radius: 16px; background: var(--search-dialog-card); color: var(--search-dialog-text); text-align: left; cursor: pointer; }
.mikan-item-card > svg, .mikan-item-card > span { position: relative; z-index: 2; }
.mikan-item-card[aria-pressed="true"] { color: var(--search-dialog-on-tonal); }
.mikan-item-card[aria-disabled="true"] { cursor: wait; }
.mikan-item-card:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: -2px; }
.mikan-item-cover { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 70px; overflow: hidden; border-radius: 8px; background: color-mix(in srgb, var(--search-dialog-outline) 20%, transparent); color: var(--search-dialog-muted); }
.mikan-item-cover img { width: 100%; height: 100%; object-fit: cover; }
.mikan-item-cover svg { width: 24px; height: 24px; }
.mikan-item-name { min-width: 0; padding-right: 4px; }
.mikan-item-card strong { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; overflow: hidden; font-size: 12px; line-height: 18px; font-weight: 600; overflow-wrap: anywhere; }
.mikan-item-card small { display: block; margin-top: 4px; color: var(--search-dialog-muted); font-size: 10px; line-height: 16px; }
.mikan-all-items { width: 160px; padding: 12px; }
.mikan-all-items > svg { width: 26px; height: 26px; flex-shrink: 0; }
.mikan-initial-feedback { margin-top: 12px; }
.mikan-initial-error { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; padding: 16px; border-radius: 16px; background: var(--search-dialog-secondary-background); }
.mikan-initial-error > div { flex: 1; min-width: min(100%, 180px); overflow-wrap: anywhere; }
.mikan-initial-error strong { display: block; margin-bottom: 4px; color: var(--search-dialog-text); font-size: 13px; font-weight: 600; }
.mikan-loading { display: flex; align-items: flex-start; gap: 6px; margin: 0; color: var(--search-dialog-primary); font-size: 12px; line-height: 18px; }
.mikan-loading-spinner { width: 16px; height: 16px; flex-shrink: 0; margin-top: 1px; animation: mikan-spin 1s linear infinite; }
.mikan-load-error { margin: 0; color: var(--mikan-error); font-size: 12px; line-height: 18px; }
.mikan-error-details summary { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; list-style: none; cursor: pointer; }
.mikan-error-details summary::-webkit-details-marker { display: none; }
.mikan-error-details summary svg { width: 12px; height: 12px; flex-shrink: 0; }
.mikan-error-details[open] summary svg { transform: rotate(90deg); }
.mikan-error-details p { max-width: 100%; padding-bottom: 8px; overflow-wrap: anywhere; }
.mikan-filter-anchor { margin-top: 8px; }
.mikan-filter-controls { position: sticky; top: 0; z-index: 3; display: grid; min-width: 0; gap: 4px; margin: 0 -24px; padding: 0 24px 8px; }
.mikan-short-viewport .mikan-filter-controls { position: static; }
.mikan-filter-row { display: flex; min-width: 0; align-items: center; gap: 10px; }
.mikan-filter-row > nav { flex: 1; }
.mikan-filter-label { flex-shrink: 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-result-input { width: 100%; min-height: 40px; padding-block: 8px; font-size: 16px; }
.mikan-result-summary { display: flex; min-height: 32px; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 2px 12px; margin: 8px 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-result-summary p { margin: 0; }
.mikan-result-summary .mikan-loading { font-size: 11px; }
.mikan-resource-list { display: grid; gap: 10px; margin: 0; padding: 0; list-style: none; }
.mikan-resource { min-width: 0; padding: 16px 20px; border: 1px solid var(--search-dialog-card-border); border-radius: 20px; background: var(--search-dialog-card); }
.mikan-resource.mikan-selectable { position: relative; cursor: pointer; }
.mikan-resource-selected { background: var(--search-dialog-tonal); }
.mikan-selectable .mikan-resource-title { padding-left: 36px; }
.mikan-resource-checkbox { position: absolute; top: 12px; left: 12px; display: grid; place-items: center; width: 44px; height: 44px; color: var(--search-dialog-muted); cursor: pointer; }
.mikan-resource-checkbox input { position: absolute; inset: 0; width: 100%; height: 100%; margin: 0; opacity: 0; cursor: inherit; }
.mikan-resource-checkbox svg { width: 24px; height: 24px; pointer-events: none; }
.mikan-resource-checkbox:has(input:checked) { color: var(--search-dialog-primary); }
.mikan-resource-checkbox:has(input:disabled) { opacity: .45; cursor: default; }
.mikan-resource-checkbox:has(input:focus-visible) { outline: 2px solid var(--search-dialog-primary); outline-offset: -2px; border-radius: 12px; }
.mikan-selection-tools { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin: -4px 0 4px; }
.mikan-spec-options { display: flex; flex: 1; flex-wrap: wrap; min-width: 0; align-items: center; gap: 4px 6px; }
.mikan-spec-label { color: var(--search-dialog-muted); font-size: 11px; }
.mikan-spec-chip { display: inline-flex; align-items: center; gap: 6px; min-height: 36px; padding: 4px 8px; border-radius: 10px; background: var(--search-dialog-card); color: var(--search-dialog-muted); font-size: 11px; white-space: nowrap; cursor: pointer; }
.mikan-spec-chip input { width: 14px; height: 14px; accent-color: var(--search-dialog-primary); }
.mikan-spec-chip:has(input:checked) { background: var(--search-dialog-action-background); color: var(--search-dialog-on-tonal); }
.mikan-spec-chip:has(input:focus-visible) { outline: 2px solid var(--search-dialog-primary); outline-offset: 1px; }
.mikan-spec-chip:has(input:disabled) { opacity: .5; cursor: default; }
.mikan-select-all { display: inline-flex; align-items: center; gap: 8px; min-height: 44px; color: var(--search-dialog-text); font-size: 12px; cursor: pointer; }
.mikan-select-all input { width: 18px; height: 18px; accent-color: var(--search-dialog-primary); }
.mikan-select-all small { color: var(--search-dialog-muted); font-size: 11px; }
.mikan-result-summary .mikan-batch-entry { min-height: 32px; padding-inline: 8px; color: var(--search-dialog-primary); }
.mikan-batch-footer { display: grid; grid-template-columns: minmax(0, 1fr) auto; flex-shrink: 0; align-items: center; gap: 4px 12px; padding: 12px 24px 16px; background: var(--search-dialog-container); box-shadow: 0 -4px 16px rgb(0 0 0 / 3%); }
.mikan-batch-count { display: flex; flex-direction: column; min-width: 0; gap: 2px; }
.mikan-batch-count:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 3px; border-radius: 4px; }
.mikan-batch-count strong { color: var(--search-dialog-text); font-size: 13px; line-height: 20px; font-weight: 600; }
.mikan-batch-count > span { color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.mikan-batch-actions { display: flex; align-items: center; gap: 4px; }
.mikan-batch-progress { grid-column: 1 / -1; height: 3px; overflow: hidden; border-radius: 4px; background: var(--search-dialog-outline); }
.mikan-batch-progress > span { display: block; height: 100%; background: var(--search-dialog-primary); transition: width .2s; }
.mikan-batch-notice { grid-column: 1 / -1; max-height: 72px; margin: 4px 0 0; overflow: auto; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.mikan-batch-notice a { display: inline-flex; min-height: 32px; align-items: center; margin-left: 8px; color: var(--search-dialog-primary); }
.mikan-batch-check { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; }
.mikan-batch-check > a { display: inline-flex; min-height: 44px; align-items: center; color: var(--search-dialog-primary); font-size: 11px; }
.mikan-resource-main { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 8px 16px; }
.mikan-resource-title { grid-column: 1 / -1; min-width: 0; line-height: 23px; }
.mikan-resource-title h4 { display: inline; margin: 0; color: var(--search-dialog-text); font-size: 14px; line-height: 23px; font-weight: 600; overflow-wrap: anywhere; }
.mikan-copy-button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 40px; margin-left: 8px; padding: 6px 10px; border: 0; border-radius: 20px; background: transparent; color: var(--search-dialog-primary); font-size: 11px; vertical-align: middle; cursor: pointer; }
.mikan-copy-button svg { width: 14px; height: 14px; flex-shrink: 0; }
.mikan-resource-meta { display: flex; min-width: 0; flex-wrap: wrap; align-items: center; gap: 6px 12px; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-resource-meta > span { overflow-wrap: anywhere; }
.mikan-resolution { padding: 3px 8px; border-radius: 8px; background: var(--search-dialog-secondary-background); font-weight: 550; }
.mikan-resource-actions { display: flex; align-items: center; gap: 6px; }
.mikan-action-label-mobile { display: none; }
.mikan-resource-actions button:disabled { cursor: default; opacity: .45; }
.mikan-resource-actions :is(a, button):focus-visible, .mikan-copy-button:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
.mikan-empty { margin: 0; padding: 36px 16px; color: var(--search-dialog-muted); font-size: 12px; line-height: 22px; text-align: center; }
.mikan-notice { margin: 8px 0 0; padding: 12px 14px; border-radius: 12px; font-size: 12px; line-height: 22px; overflow-wrap: anywhere; }
.mikan-notice p { margin: 0; }
.mikan-notice a { margin-left: 8px; color: inherit; text-decoration: underline; text-underline-offset: 3px; }
.mikan-notice textarea { display: block; width: 100%; margin-top: 8px; padding: 8px; border: 1px solid var(--search-dialog-outline); border-radius: 8px; background: var(--search-dialog-card); color: var(--search-dialog-text); font-size: 16px; }
.mikan-success { color: var(--mikan-success); background: var(--mikan-success-bg); }
.mikan-warning { color: var(--mikan-warning); background: var(--mikan-warning-bg); }
.mikan-error { color: var(--mikan-error); background: var(--mikan-error-bg); }
.mikan-list-end { display: flex; flex-direction: column; align-items: center; gap: 8px; margin-top: 20px; }
.mikan-list-end > span, .mikan-expansions > p { margin: 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-expansions { margin-top: 20px; }
.mikan-expansions > div { display: flex; flex-wrap: wrap; gap: 4px 8px; margin-top: 8px; }
.mikan-dialog button { cursor: pointer; }
.mikan-dialog button:disabled { cursor: default; opacity: .45; }
.mikan-dialog button:focus-visible, .mikan-dialog a:focus-visible, .mikan-dialog summary:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 3px; }
@keyframes mikan-spin { to { transform: rotate(360deg); } }
@media (hover: hover) {
  .mikan-icon-button:hover, .mikan-tool-button:hover:not(:disabled) { background: var(--search-dialog-secondary-hover); }
  .mikan-copy-button:hover { background: var(--search-dialog-secondary-hover); }
  .mikan-item-card:hover:not([aria-disabled="true"]):not([aria-pressed="true"]) { background: var(--search-dialog-secondary-hover); }
  .mikan-item-strip:has(.mikan-item-card[aria-pressed="true"]:hover:not([aria-disabled="true"])) > .mikan-item-indicator { background: var(--search-dialog-action-hover); }
}
@media (max-width: 799px) {
  .mikan-resource-main { grid-template-columns: minmax(0, 1fr) auto; }
  .mikan-resource-title { display: contents; }
  .mikan-resource-title h4 { display: block; grid-column: 1 / -1; }
  .mikan-selectable .mikan-resource-title h4 { padding-left: 36px; }
  .mikan-resource-meta { grid-column: 1; grid-row: 2; }
  .mikan-copy-button { grid-column: 2; grid-row: 2; margin: 0; padding-inline: 6px; }
  .mikan-resource-actions { grid-column: 1 / -1; justify-content: flex-end; }
  .mikan-action-label-desktop { display: none; }
  .mikan-action-label-mobile { display: inline; }
}
@media (max-width: 639px) {
  .mikan-dialog { --mikan-max-height: calc(min(100dvh, var(--mikan-visible-height, 100dvh)) - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 24px); inset-block: calc(var(--mikan-visible-top, 0px) + env(safe-area-inset-top)) calc(var(--mikan-visible-bottom, 0px) + env(safe-area-inset-bottom)); width: calc(100% - 24px); border-radius: 24px; }
  .mikan-header { gap: 10px; padding: 16px 16px 8px; }
  .mikan-heading h2 { height: 26px; }
  .mikan-heading h2 img { height: 20px; }
  .mikan-heading p { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; font-size: 12px; line-height: 18px; }
  .mikan-body { padding: 0 16px 16px; scrollbar-gutter: auto; }
  .mikan-search-form > input { min-height: 48px; }
  .mikan-refresh-button { width: 48px; height: 48px; padding: 0; }
  .mikan-refresh-button > span { display: none; }
  .mikan-search-tools { flex-wrap: wrap; gap: 0; }
  .mikan-tool-button { min-height: 48px; padding-inline: 8px; }
  .mikan-filter-controls { margin-inline: -16px; padding-inline: 16px; }
  .mikan-result-input { min-height: 44px; }
  .mikan-resource { padding: 16px; border-radius: 18px; }
  .mikan-resource-checkbox { top: 8px; left: 8px; width: 48px; height: 48px; }
  .mikan-selection-tools .mikan-select-all { min-height: 48px; }
  .mikan-selection-tools { display: grid; grid-template-columns: minmax(0, 1fr) auto; }
  .mikan-spec-options { grid-column: 1 / -1; grid-row: 2; }
  .mikan-spec-chip { min-height: 44px; }
  .mikan-batch-footer { grid-template-columns: minmax(0, 1fr); padding: 8px 16px 12px; gap: 4px 8px; }
  .mikan-batch-actions .mikan-button { flex: 1; }
  .mikan-batch-count strong { font-size: 12px; }
  .mikan-batch-count > span { font-size: 10px; line-height: 16px; }
  .mikan-batch-actions .mikan-button { padding-inline: 10px; font-size: 11px; white-space: nowrap; }
  .mikan-copy-button { min-height: 48px; }
  .mikan-resource-actions { display: grid; grid-auto-flow: column; grid-auto-columns: minmax(0, 1fr); }
  .mikan-resource-actions > :is(a, button) { min-width: 0; padding-inline: 6px; font-size: 11px; }
  .mikan-resource-actions > :is(a, button) > span { white-space: nowrap; }
  .mikan-cover-retry { min-height: 32px; }
  .mikan-picker-hint { display: none; }
  .mikan-picker-heading > span { font-size: 10px; }
  .mikan-item-strip { padding-top: 0; }
  .mikan-item-card { width: 176px; min-height: 80px; gap: 8px; }
  .mikan-item-cover { width: 40px; height: 58px; }
  .mikan-all-items { flex-direction: column; justify-content: center; gap: 4px; width: 112px; padding: 8px; text-align: center; }
  .mikan-all-items > svg { width: 20px; height: 20px; }
  .mikan-all-items small { margin-top: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .mikan-batch-progress > span { transition: none; }
  .mikan-item-indicator { transition: none !important; }
  .mikan-loading-spinner { animation: none; }
}
</style>
