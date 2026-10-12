<script setup lang="ts">
import { copyText } from '../utils/clipboard'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import MikanFilterSelect from './MikanFilterSelect.vue'
import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import mikanMascot from '../assets/mikan/mikan-pic.svg?no-inline'
import mikanWordmark from '../assets/mikan/mikan-text.svg?no-inline'

const props = defineProps<{ token: string; title: string; originalTitle: string; themeColors?: Record<string, string> }>()
const emit = defineEmits<{ close: [] }>()
interface Item { id: string; title: string; size: string; updatedAt: string; magnet: string; url: string; torrentUrl: string; resolution: string; groupId?: string }
interface Group { id: string; name: string }
interface Bangumi { id: string; title: string; coverUrl: string; url: string }
interface Expansion { groupId: string; take: number }
interface Result { items: Item[]; groups: Group[]; bangumis?: Bangumi[]; bangumi?: Bangumi; more?: Expansion[]; query: string; group: string; cached: boolean; fetchedAt: string; expiresAt: string; attempts: { query: string; count: number; cached: boolean }[] }
const dialog = ref<HTMLDialogElement>()
const viewportStyle = ref<Record<string, string>>({})
const dialogStyle = computed(() => ({ ...props.themeColors, ...viewportStyle.value }))
const shortViewport = ref(false)
const query = ref(props.title || props.originalTitle)
const group = ref('')
const groups = ref<Group[]>([])
const result = ref<Result>()
const busy = ref(false)
const retainedItems = ref<Item[]>()
const loadingMessage = ref('')
const error = ref('')
const filter = ref('')
const resolution = ref('全部')
const visible = ref(50)
const copied = ref('')
const copyError = ref('')
const selectedBangumi = ref<Bangumi>()
const brokenCovers = ref<Record<string, boolean>>({})
const coverRetries = ref<Record<string, number>>({})
const searchSnapshot = ref<{ result: Result; query: string; group: string; groups: Group[]; filter: string; resolution: string; visible: number }>()
// 字幕组检索只更新资源；保留同一关键词的番剧入口，避免顶部条目收缩。
const bangumis = ref<Bangumi[]>([])
const itemStrip = ref<HTMLElement>()
const activeBangumiId = computed(() => selectedBangumi.value?.id || 'all')
const { indicatorStyle: itemIndicatorStyle } = useSlidingIndicator(itemStrip, '[aria-pressed="true"]', [activeBangumiId, bangumis])
const hasBrokenCovers = computed(() => bangumis.value.some(card => brokenCovers.value[card.id]))
const expansions = computed(() => (result.value?.more || []).filter(item => !group.value || item.groupId === group.value))
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const submissions = ref<Record<string, { busy?: boolean; success?: boolean; message?: string; needsConfig?: boolean }>>({})
const resolutions = computed(() => ['全部', ...['2160P', '1080P', '720P', '480P', '未知'].filter(value => result.value?.items.some(item => item.resolution === value))])
const groupOptions = computed(() => [{ value: '', label: '全部字幕组' }, ...groups.value.map(item => ({ value: item.id, label: item.name }))])
const resolutionOptions = computed(() => resolutions.value.map(value => ({ value, label: value === '全部' ? '全部分辨率' : value })))
const filtered = computed(() => {
  if (busy.value && retainedItems.value) return retainedItems.value
  const words = filter.value.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
  return (result.value?.items || []).filter(item => (!result.value?.bangumi || !group.value || item.groupId === group.value) && (resolution.value === '全部' || item.resolution === resolution.value) && words.every(word => item.title.toLocaleLowerCase().includes(word)))
})
const siteURL = computed(() => {
  if (selectedBangumi.value) return selectedBangumi.value.url
  const params = new URLSearchParams({ searchstr: result.value?.query || query.value })
  if (result.value?.group) params.set('subgroupid', result.value.group)
  return `https://mikanani.me/Home/Search?${params}`
})
watch([filter, resolution], () => { visible.value = 50 }, { flush: 'sync' })

async function search(refresh = false, selectedGroup = '') {
  if (busy.value || !query.value.trim()) return
  const previousGroup = group.value
  const previousBangumi = selectedBangumi.value
  retainedItems.value = filtered.value
  loadingMessage.value = selectedGroup ? `正在读取 ${groups.value.find(item => item.id === selectedGroup)?.name || '该字幕组'} 的搜索结果…` : '正在搜索蜜柑…'
  busy.value = true
  error.value = ''
  group.value = selectedGroup
  // 保留列表、番剧和筛选节点，避免弹窗收缩、滚动跳动及选中按钮失焦。
  try {
    const q = query.value.trim()
    // 只有默认中文标题搜索才自动使用原名兜底，手动改词和字幕组筛选保留用户意图。
    const fallback = q === props.title.trim() && !selectedGroup ? props.originalTitle : ''
    const response = await fetch('/api/admin/mikan/search', {
      method: 'POST', cache: 'no-store',
      headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: q, fallback, group: selectedGroup, refresh }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(75000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用蜜柑搜索' : data?.error || `蜜柑搜索失败（HTTP ${response.status}）`)
    if (!selectedGroup || data.query !== result.value?.query) bangumis.value = data.bangumis || []
    result.value = data as Result
    selectedBangumi.value = undefined
    query.value = data.query
    if (!selectedGroup) groups.value = data.groups
    searchSnapshot.value = undefined
    visible.value = 50
    resolution.value = '全部'
    await resetResultPosition()
  } catch (e) {
    group.value = previousGroup
    selectedBangumi.value = previousBangumi
    if (!lifecycle.signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑搜索超时，请稍后重试' : e instanceof Error ? e.message : '蜜柑搜索失败，请稍后重试'
  } finally { busy.value = false; retainedItems.value = undefined }
}
function selectGroup(value: string) {
  if (busy.value) return
  if (selectedBangumi.value) { group.value = value; visible.value = 50 }
  else void search(false, value)
}
function useTitle(value: string) { if (busy.value) return; query.value = value; void search() }
function refreshCurrent() {
  retryCovers()
  if (selectedBangumi.value) void loadBangumi(selectedBangumi.value, true)
  else void search(true, group.value)
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
  const anchor = dialog.value?.querySelector<HTMLElement>('.mikan-filter-controls')
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
function backToSearch() {
  if (busy.value || !searchSnapshot.value) return
  const saved = searchSnapshot.value
  result.value = saved.result
  query.value = saved.query
  group.value = saved.group
  groups.value = saved.groups
  filter.value = saved.filter
  resolution.value = saved.resolution
  visible.value = saved.visible
  selectedBangumi.value = undefined
  searchSnapshot.value = undefined
  error.value = ''
  void resetResultPosition()
}
async function loadBangumi(card: Bangumi, refresh = false, expansion?: Expansion) {
  if (busy.value) return
  const previousBangumi = selectedBangumi.value
  const previousSnapshot = searchSnapshot.value
  retainedItems.value = filtered.value
  if (!selectedBangumi.value && result.value) {
    // 进入番剧前保留搜索结果及筛选，返回时无需重新搜索。
    searchSnapshot.value = { result: result.value, query: query.value, group: group.value, groups: groups.value, filter: filter.value, resolution: resolution.value, visible: visible.value }
  }
  selectedBangumi.value = card
  loadingMessage.value = expansion ? '正在加载该字幕组的更多资源…' : `正在读取 ${card.title} 的番剧资源…`
  busy.value = true
  error.value = ''
  try {
    const response = await fetch('/api/admin/mikan/bangumi', {
      method: 'POST', cache: 'no-store',
      headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: card.id, group: expansion?.groupId || '', take: expansion?.take || 0, refresh }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(75000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用蜜柑搜索' : data?.error || `蜜柑番剧读取失败（HTTP ${response.status}）`)
    const next = data as Result
    if (expansion && result.value) {
      // 原站返回该字幕组的累积列表；替换该组并按磁力去重，保留其他组及其加载进度。
      const rows = [...result.value.items.filter(item => item.groupId !== expansion.groupId), ...next.items]
      result.value = { ...result.value, items: [...new Map(rows.map(item => [item.id, item])).values()].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)), more: [...(result.value.more || []).filter(item => item.groupId !== expansion.groupId), ...(next.more || [])] }
    } else {
      result.value = next
      groups.value = next.groups
      if (previousBangumi?.id !== card.id) filter.value = ''
      group.value = ''
      resolution.value = '全部'
      visible.value = 50
      await resetResultPosition()
    }
  } catch (e) {
    selectedBangumi.value = previousBangumi
    searchSnapshot.value = previousSnapshot
    if (!lifecycle.signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑番剧读取超时，可重试或返回搜索结果' : e instanceof Error ? e.message : '蜜柑番剧读取失败，请稍后重试'
  } finally { busy.value = false; retainedItems.value = undefined }
}
function selectBangumi(card: Bangumi) {
  if (selectedBangumi.value?.id !== card.id) void loadBangumi(card)
}
async function copy(item: Item) {
  copyError.value = ''; copied.value = ''
  try { await copyText(item.magnet, dialog.value); copied.value = item.id }
  catch { copied.value = ''; copyError.value = item.id }
}
async function submit(item: Item) {
  if (busy.value || submissions.value[item.id]?.busy || submissions.value[item.id]?.success) return
  submissions.value[item.id] = { busy: true }
  try {
    // 复用云下载功能明确绑定的账号和目录，不选择默认账号，也不改写配置。
    const config = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', {
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    if (!config.accountId || !config.targetCID) throw new Error('请先配置主账号，并在“115 云下载”保存下载目录')
    const receipt = await submitCloudDownload(props.token, config.accountId, item.magnet, config.targetCID, lifecycle.signal)
    submissions.value[item.id] = { success: true, message: cloudDownloadNotice(receipt, config.targetPath) }
  } catch (e) {
    if (lifecycle.signal.aborted) return
    const message = e instanceof Error ? e.message : '提交失败'
    submissions.value[item.id] = { message, needsConfig: /请先.*(?:选择|配置)|尚未登录|账号.*不存在|配置|凭据/.test(message) }
  }
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
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
  void search()
})
onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', syncVisualViewport)
  window.visualViewport?.removeEventListener('scroll', syncVisualViewport)
  window.removeEventListener('resize', syncVisualViewport)
  lifecycle.abort()
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  previousFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :style="dialogStyle" aria-labelledby="mikan-search-title" aria-describedby="mikan-work-title" class="mikan-dialog media-search-dialog" :class="{ 'mikan-short-viewport': shortViewport }" @cancel.prevent="emit('close')">
      <div class="mikan-layout">
        <header class="mikan-header" data-mikan-brand>
          <div class="mikan-brand" aria-hidden="true"><img :src="mikanMascot" alt="" width="400" height="400" draggable="false" /></div>
          <div class="mikan-heading">
            <h2 id="mikan-search-title"><span class="sr-only">蜜柑搜索</span><img :src="mikanWordmark" alt="" width="117" height="29" draggable="false" /></h2>
            <p id="mikan-work-title" :title="title" data-mikan-brand-title>{{ title }}</p>
          </div>
          <button autofocus type="button" aria-label="关闭蜜柑搜索" class="mikan-icon-button" @click="emit('close')"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
        </header>
        <div class="mikan-body">
          <form class="mikan-search-form" @submit.prevent="search()">
            <input v-model="query" aria-label="蜜柑搜索词" maxlength="200" :readonly="busy" :aria-busy="busy" class="mikan-input" placeholder="输入片名、原名或其他译名" />
            <button :disabled="busy || !query.trim()" class="mikan-button mikan-primary-button">搜索</button>
            <button type="button" :disabled="busy || (!selectedBangumi && !query.trim())" aria-label="强制刷新" title="强制刷新，重新读取原站资源" class="mikan-tool-button mikan-refresh-button" @click="refreshCurrent"><svg aria-hidden="true" :class="{ 'mikan-loading-spinner': busy }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg><span>强制刷新</span></button>
          </form>
          <div class="mikan-search-tools">
            <button type="button" :disabled="busy" class="mikan-tool-button" @click="useTitle(title)">用中文标题</button>
            <button v-if="originalTitle && originalTitle !== title" type="button" :disabled="busy" class="mikan-tool-button" @click="useTitle(originalTitle)">用原名</button>
            <a :href="siteURL" target="_blank" rel="noopener noreferrer" class="mikan-tool-button mikan-site-link"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg>{{ selectedBangumi ? '番剧原页' : '蜜柑原站' }}</a>
          </div>
          <section v-if="bangumis.length" class="mikan-item-picker" aria-label="蜜柑相关番剧">
            <div class="mikan-picker-heading">
              <h3>影视条目</h3>
              <span>{{ bangumis.length }} 个番剧<span class="mikan-picker-hint">，全部条目显示关键词搜索结果</span></span>
              <button v-if="hasBrokenCovers" type="button" class="mikan-tool-button mikan-cover-retry" @click="retryCovers">重试封面</button>
            </div>
            <nav ref="itemStrip" aria-label="蜜柑影视条目" :aria-busy="busy" class="mikan-item-strip">
              <div class="mikan-item-indicator" :style="itemIndicatorStyle" aria-hidden="true"></div>
              <button type="button" class="mikan-item-card mikan-all-items" :aria-disabled="busy" :aria-pressed="activeBangumiId === 'all'" aria-label="全部条目：显示关键词搜索结果" title="恢复关键词搜索结果与筛选" @click="backToSearch">
                <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></svg>
                <span><strong>全部条目</strong><small>关键词搜索结果</small></span>
              </button>
              <button v-for="card in bangumis" :key="card.id" type="button" :aria-disabled="busy" :aria-pressed="activeBangumiId === card.id" :aria-label="`查看番剧：${card.title}`" :title="card.title" class="mikan-item-card" @click="selectBangumi(card)">
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
            <p v-else-if="error" role="alert" class="mikan-load-error">{{ error }}</p>
          </div>
          <p v-if="result && !selectedBangumi && result.attempts.length > 1" role="status" class="mikan-search-note">中文标题未找到资源，已尝试原名：{{ result.query }}</p>
          <section v-if="result" aria-label="蜜柑结果筛选" class="mikan-filter-controls">
            <div class="mikan-filter-row">
              <span class="mikan-filter-label" aria-hidden="true">字幕组</span>
              <MikanFilterSelect :model-value="group" :options="groupOptions" label="蜜柑字幕组" :disabled="busy" @update:model-value="selectGroup" />
            </div>
            <div class="mikan-filter-row">
              <span class="mikan-filter-label" aria-hidden="true">分辨率</span>
              <MikanFilterSelect v-model="resolution" :options="resolutionOptions" label="蜜柑分辨率" :disabled="busy" />
            </div>
            <input v-model="filter" aria-label="筛选蜜柑结果" placeholder="结果内筛选：合集 / 第二季 / 简繁…" :readonly="busy" class="mikan-input mikan-result-input" />
          </section>
          <template v-if="result">
            <div class="mikan-result-summary">
              <p>匹配 {{ filtered.length }} / {{ selectedBangumi ? '已加载' : '已获取' }} {{ result.items.length }} 条<span v-if="result.more?.length">，仅筛选已加载资源</span></p>
              <p v-if="busy" role="status" class="mikan-loading" :title="loadingMessage"><svg aria-hidden="true" class="mikan-loading-spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M20 12a8 8 0 1 1-4-6.9" /></svg><span>更新中，暂显旧结果</span></p>
              <details v-else-if="error" role="alert" class="mikan-load-error mikan-error-details"><summary><span>更新失败，已保留原结果</span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 6 6 6-6 6" /></svg></summary><p>{{ error }}</p></details>
              <p v-else>时间从新到旧</p>
            </div>
            <p v-if="!result.items.length" class="mikan-empty">{{ selectedBangumi ? '该番剧当前没有可读取的资源，可返回全部条目或访问原站。' : bangumis.length ? '关键词未找到磁力资源，可点击上方相关番剧继续查看。' : '蜜柑未找到资源，可修改片名、尝试其他译名或访问原站。' }}</p>
            <p v-else-if="!filtered.length" class="mikan-empty">没有符合筛选条件的资源，请调整关键词或分辨率。</p>
            <ul class="mikan-resource-list" aria-label="蜜柑资源列表">
              <li v-for="item in filtered.slice(0, visible)" :key="item.id" class="mikan-resource">
                <div class="mikan-resource-main">
                  <div class="mikan-resource-title">
                    <h4>{{ item.title }}</h4>
                    <button type="button" class="mikan-copy-button" @click="copy(item)"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V4H4v12h4" /></svg>{{ copied === item.id ? '已复制' : '复制磁力' }}</button>
                  </div>
                  <div class="mikan-resource-meta"><span class="mikan-resolution">{{ item.resolution }}</span><span>{{ item.size }}</span><span :title="'原站时间：' + item.updatedAt">{{ item.updatedAt }}</span></div>
                  <div class="mikan-resource-actions">
                    <a :href="item.url" target="_blank" rel="noopener noreferrer" class="mikan-button mikan-outlined-button" aria-label="查看蜜柑原帖"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M14 4h6v6m0-6L10 14M10 4H4v16h16v-6" /></svg><span class="mikan-action-label-desktop">查看原帖</span><span class="mikan-action-label-mobile">原帖</span></a>
                    <a v-if="item.torrentUrl" :href="item.torrentUrl" target="_blank" rel="noopener noreferrer" class="mikan-button mikan-outlined-button" aria-label="下载种子"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m-4-4 4 4 4-4M4 17v4h16v-4" /></svg><span class="mikan-action-label-desktop">下载种子</span><span class="mikan-action-label-mobile">种子</span></a>
                    <button type="button" :disabled="busy || submissions[item.id]?.busy || submissions[item.id]?.success" class="mikan-button mikan-primary-button" aria-label="115 云下载" @click="submit(item)"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M6 17a4 4 0 0 1-1-7.8A6 6 0 0 1 17 8a4.5 4.5 0 0 1 1 9M12 10v11m-4-4 4 4 4-4" /></svg><template v-if="submissions[item.id]?.busy || submissions[item.id]?.success"><span class="mikan-action-label-desktop">{{ submissions[item.id]?.busy ? '提交中…' : '已提交' }}</span><span class="mikan-action-label-mobile">{{ submissions[item.id]?.busy ? '提交中' : '已提交' }}</span></template><template v-else><span class="mikan-action-label-desktop">115 云下载</span><span class="mikan-action-label-mobile">云下载</span></template></button>
                  </div>
                </div>
                <div v-if="copyError === item.id" role="alert" class="mikan-notice mikan-warning"><p>浏览器未允许复制，请手动复制下方磁力：</p><textarea :value="item.magnet" readonly aria-label="手动复制磁力" @focus="($event.target as HTMLTextAreaElement).select()" /></div>
                <p v-if="copied === item.id" role="status" class="sr-only">磁力已复制</p>
                <p v-if="submissions[item.id]?.message" role="status" class="mikan-notice" :class="submissions[item.id]?.success ? 'mikan-success' : 'mikan-error'">{{ submissions[item.id]?.message }}<RouterLink v-if="submissions[item.id]?.needsConfig" to="/115/cloud-download" @click="emit('close')">前往配置</RouterLink></p>
              </li>
            </ul>
            <div v-if="filtered.length > visible" class="mikan-list-end"><button type="button" class="mikan-button mikan-outlined-button" @click="showMore">显示更多资源</button><span>还有 {{ filtered.length - visible }} 条，筛选包含已获取的全部结果</span></div>
            <section v-if="selectedBangumi && expansions.length" aria-label="加载更多番剧资源" class="mikan-expansions">
              <p>部分字幕组还有更多资源，可继续读取。</p>
              <div>
                <template v-for="more in expansions" :key="more.groupId"><a v-if="more.take > 10000" :href="siteURL" target="_blank" rel="noopener noreferrer" class="mikan-button mikan-outlined-button">原站查看：{{ groups.find(g => g.id === more.groupId)?.name || more.groupId }}</a><button v-else type="button" :disabled="busy" class="mikan-button mikan-outlined-button" @click="loadBangumi(selectedBangumi!, false, more)">加载更多：{{ groups.find(g => g.id === more.groupId)?.name || more.groupId }}</button></template>
              </div>
            </section>
          </template>
        </div>
        <footer class="mikan-footer"><span>115 云下载保存至已配置的下载目录。</span><RouterLink :to="{ path: '/logs', query: { module: '蜜柑', level: 'ALL' } }" @click="emit('close')">查看日志</RouterLink></footer>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.mikan-dialog {
  --mikan-max-height: 90dvh;
  --mikan-success: #246044; --mikan-success-bg: #e6f2e9;
  --mikan-warning: #765500; --mikan-warning-bg: #fff1cd;
  --mikan-error: #a12b2b; --mikan-error-bg: #ffe9e7;
  margin: auto; padding: 0; width: min(1080px, calc(100% - 48px)); max-width: none; max-height: var(--mikan-max-height);
  border: 0; border-radius: 28px; background: color-mix(in srgb, var(--search-dialog-container) 94%, transparent);
  -webkit-backdrop-filter: blur(16px); backdrop-filter: blur(16px); color: var(--search-dialog-text); color-scheme: light;
  overflow: hidden; box-shadow: 0 16px 48px rgb(0 0 0 / 20%);
}
.dark .mikan-dialog { --mikan-success: #a5d8b8; --mikan-success-bg: #223d31; --mikan-warning: #eed085; --mikan-warning-bg: #42371e; --mikan-error: #ffb4ab; --mikan-error-bg: #502c2b; color-scheme: dark; }
.mikan-dialog::backdrop { background: rgb(2 6 23 / 32%); }
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
.mikan-loading { display: flex; align-items: flex-start; gap: 6px; margin: 0; color: var(--search-dialog-primary); font-size: 12px; line-height: 18px; }
.mikan-loading-spinner { width: 16px; height: 16px; flex-shrink: 0; margin-top: 1px; animation: mikan-spin 1s linear infinite; }
.mikan-load-error { margin: 0; color: var(--mikan-error); font-size: 12px; line-height: 18px; }
.mikan-error-details summary { display: inline-flex; align-items: center; gap: 4px; min-height: 32px; list-style: none; cursor: pointer; }
.mikan-error-details summary::-webkit-details-marker { display: none; }
.mikan-error-details summary svg { width: 12px; height: 12px; flex-shrink: 0; }
.mikan-error-details[open] summary svg { transform: rotate(90deg); }
.mikan-error-details p { max-width: 100%; padding-bottom: 8px; overflow-wrap: anywhere; }
.mikan-filter-controls { position: sticky; top: 0; z-index: 3; display: grid; min-width: 0; gap: 4px; margin: 8px -24px 0; padding: 0 24px 8px; border-bottom: 1px solid var(--search-dialog-outline); background: var(--search-dialog-container); }
.mikan-short-viewport .mikan-filter-controls { position: static; }
.mikan-filter-row { display: flex; min-width: 0; align-items: center; gap: 10px; }
.mikan-filter-row > nav { flex: 1; }
.mikan-filter-label { flex-shrink: 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-result-input { width: 100%; min-height: 40px; padding-block: 8px; font-size: 16px; }
.mikan-result-summary { display: flex; min-height: 32px; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 2px 12px; margin: 8px 0; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-result-summary p { margin: 0; }
.mikan-result-summary .mikan-loading { font-size: 11px; }
.mikan-resource-list { display: grid; gap: 10px; margin: 0; padding: 0; list-style: none; }
.mikan-resource { min-width: 0; padding: 16px 20px; border: 1px solid color-mix(in srgb, var(--search-dialog-outline) 35%, transparent); border-radius: 20px; background: var(--search-dialog-card); }
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
.mikan-footer { display: flex; flex-shrink: 0; flex-wrap: wrap; align-items: center; gap: 0 12px; padding: 8px 24px 12px; color: var(--search-dialog-muted); font-size: 11px; line-height: 18px; }
.mikan-footer a { display: inline-flex; min-height: 32px; align-items: center; color: var(--search-dialog-primary); }
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
  .mikan-copy-button { min-height: 48px; }
  .mikan-resource-actions { justify-content: stretch; }
  .mikan-resource-actions > :is(a, button) { flex: 1; min-width: 0; padding-inline: 6px; font-size: 11px; }
  .mikan-resource-actions > :is(a, button) > span { white-space: nowrap; }
  .mikan-footer { padding: 4px 16px 8px; font-size: 10px; line-height: 16px; }
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
  .mikan-item-indicator { transition: none !important; }
  .mikan-loading-spinner { animation: none; }
}
@media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
  .mikan-dialog { background: var(--search-dialog-container); -webkit-backdrop-filter: none; backdrop-filter: none; }
}
</style>
