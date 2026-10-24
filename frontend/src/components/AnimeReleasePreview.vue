<script setup lang="ts">
import { computed, nextTick, watch, ref } from 'vue'
import { IconRefresh, IconListSearch, IconFolder } from '@tabler/icons-vue'
import { animeSourceReady, animeSpecLabel, type SourceForm } from '../utils/animeSourceForm'
import { sourceNaming } from '../composables/useAnimeNaming'
import { useAnimeNamingPreview } from '../composables/useAnimeNamingPreview'
import type { AnimeNamingTarget } from '../types/animeSubscription'
import SelectMenu from './SelectMenu.vue'
const props = defineProps<{ token: string; source: SourceForm; valid: boolean; busy: boolean; disabled: boolean; now: number; settingsAvailable: boolean; namingTarget?: AnimeNamingTarget; namingBusy: boolean; organizePath?: string | null; state?: { filter: string; left: number; top: number } }>()
defineEmits<{ generate: [] }>()
const fallbackFilter = ref('all')
const showDetails = ref(false), showExplanation = ref(false)
const filterOptions = [{ value: 'all', label: '全部发布' }, { value: 'matched', label: '符合规则' }, { value: 'filtered', label: '已过滤' }, { value: 'review', label: '需确认' }, { value: 'processed', label: '有下载记录' }]
const filter = computed({ get: () => props.state?.filter || fallbackFilter.value, set: value => { if (props.state) props.state.filter = value; else fallbackFilter.value = value } })
const list = ref<HTMLElement>()
function rememberScroll() { if (props.state && list.value) { props.state.left = list.value.scrollLeft; props.state.top = list.value.scrollTop } }
watch(list, () => { void nextTick(() => { if (list.value && props.state) list.value.scrollTo(props.state.left, props.state.top) }) })
const page = computed(() => props.source.preview?.page)
const summary = computed(() => props.source.preview?.summary)
const mapping = computed(() => {
  if (!props.namingTarget) return { value: null, error: '' }
  try { return { value: sourceNaming(props.source, props.namingTarget), error: '' } }
  catch (reason) { return { value: null, error: (reason as Error).message } }
})
const naming = useAnimeNamingPreview(() => props.token, () => {
  if (!props.namingTarget || props.namingBusy || !mapping.value.value || !page.value?.items.length) return
  return { target: props.namingTarget, mapping: mapping.value.value, group: props.source.groupName, items: page.value.items.map(item => ({ id: item.release.id, title: item.release.title, parse: { kind: item.release.parse.kind, rawEpisode: item.release.parse.rawEpisode || '', episode: item.release.parse.episode } })) }
})
watch(() => props.source.preview, () => naming.refresh())
const targetDirectory = computed(() => props.organizePath || '')
function numberingNote(parsed: { kind: string; episode: number | null }) {
  if (parsed.kind === 'batch') return '合集资源，暂不支持自动整理'
  if (parsed.kind === 'special') return '特殊集资源，暂不支持自动整理'
  if (parsed.kind === 'composite') return '编号含歧义，需确认集号'
  if (parsed.kind !== 'episode' || parsed.episode == null) return '未能识别单集编号'
  return '未设置对应，保留原名'
}
const rows = computed(() => (page.value?.items.filter(item => filter.value === 'matched' ? item.selectable : filter.value === 'filtered' ? item.decision === 'filtered' : filter.value === 'processed' ? !!executionStates[page.value?.localStates?.[item.release.id] || ''] : filter.value === 'review' ? item.decision === 'needs_confirmation' : true) || []).map(item => {
  const projected = naming.items.value[item.release.id]
  const note = props.namingBusy ? '正在读取 TMDB 资料' : mapping.value.error ? '映射设置待修正' : !props.namingTarget ? '选择 TMDB 后显示名称' : item.release.parse.kind !== 'episode' || item.release.parse.episode == null ? numberingNote(item.release.parse) : naming.pending.value ? '正在生成命名预览' : naming.error.value ? '命名预览未加载' : projected?.error || (projected?.status === 'ignored' ? '已设为不整理' : numberingNote(item.release.parse))
  return { item, naming: projected, filename: projected?.result?.name || '', seasonFolder: projected?.result ? [props.organizePath?.replace(/\/+$/, ''), projected.result.directory].filter(Boolean).join('/') || '/' : '', targetPath: projected?.result ? [props.organizePath?.replace(/\/+$/, ''), projected.result.relativePath].filter(Boolean).join('/') : '', note, localState: page.value?.localStates?.[item.release.id] || '', published: publication(item.release.publishedAt) }
}))
const stale = computed(() => !!props.source.preview && !props.valid)
const reasons: Record<string, string> = { REQUIRED_PATTERN_MISSING: '未满足匹配正则', EXCLUDED_PATTERN: '命中排除正则', EPISODE_NUMBER_UNRESOLVED: '编号需确认', OUTSIDE_CHAPTER_SCOPE: '不在篇章范围', BELOW_SOURCE_MINIMUM: '低于补番起点', HASH_NOT_VERIFIED: '后台核验资源', SCOPE_UNCONFIRMED: '篇章范围未确认', SOURCE_RULES_INCOMPLETE: '规则尚未完整', RELEASE_VERSION_SUPERSEDED: '同集已有更高版本', RELEASE_VERSION_DUPLICATE: '同集同版本，已保留一条', RELEASE_VERSION_UNCLEAR: '修订版本需确认', RELEASE_EDITION_UNCLEAR: '资源规格冲突，需确认', RELEASE_EDITION_ALREADY_SELECTED: '此集已选定，不再换版' }
function reasonText(code: string, selectable: boolean, decision: string, parsed: { kind: string; episode: number | null }) { return selectable ? '符合规则' : code === 'EPISODE_NUMBER_UNRESOLVED' ? numberingNote(parsed) : reasons[code] || (decision === 'needs_confirmation' ? '需人工确认' : '已过滤') }
const executionStates: Record<string, string> = { organized: '已整理', original_saved: '已保存', completed: '下载完成', accepted: '已受理', downloading: '下载中', submitting: '提交中', unknown: '待核对', queued: '待提交', selected: '已选定', linked: '已关联', failed: '失败', blocked: '受阻', cancelled: '已停止' }
const states: Record<string, string> = { ...executionStates, filtered: '已过滤', needs_confirmation: '需确认', seen: '待检查', dismissed: '已忽略' }
function stateTone(state: string) { return ['organized', 'original_saved', 'completed'].includes(state) ? 'done' : ['failed', 'blocked', 'unknown', 'needs_confirmation'].includes(state) ? 'attention' : ['accepted', 'downloading', 'submitting', 'queued', 'selected', 'linked'].includes(state) ? 'active' : '' }
function publication(value?: number | null) {
  if (value == null || !Number.isFinite(value)) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return { iso: date.toISOString(), date: date.toLocaleDateString('sv-SE'), time: date.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' }) }
}
</script>

<template>
  <section class="ae-preview" aria-label="资源预览" :aria-busy="busy || naming.pending.value">
    <div class="ae-preview-toolbar"><slot name="source" /><div v-if="summary" class="ae-preview-stats"><span>已读 <b>{{ summary.loadedCount }}</b></span><span>符合 <b>{{ summary.matchedCount }}</b></span><span>需确认 <b>{{ summary.reviewCount }}</b></span></div><button type="button" class="ae-button" :disabled="disabled || busy || !animeSourceReady(source) || !settingsAvailable || source.previewCooldown > now" @click="$emit('generate')"><IconRefresh :size="16" :class="{ 'animate-spin': busy }" />{{ busy ? '处理中…' : source.preview ? '重新预览' : '生成预览' }}</button></div>
    <p v-if="source.previewError" role="status" class="ae-error">{{ source.previewError }}</p>
    <p v-if="source.previewCooldown > now" class="ae-note">来源冷却中，{{ Math.ceil((source.previewCooldown - now) / 1000) }} 秒后可重试。</p>
    <p v-if="stale" class="ae-inline-notice" role="status">{{ summary && (summary.expired || summary.expiresAt <= now) ? '此来源预览已过期。' : '规则或目录已变化。' }}可重新预览查看当前规则，不影响保存配置。</p>
    <template v-if="summary && page">
      <p v-if="mapping.error" class="ae-error" role="status">{{ mapping.error }}</p>
      <p v-if="naming.error.value" class="ae-error" role="status">命名预览：{{ naming.error.value }} <button type="button" class="ae-preview-option" @click="naming.refresh()">重试命名</button></p>
      <div class="ae-preview-tools"><SelectMenu v-model="filter" :options="filterOptions" label="显示" variant="anime" /><div class="ae-preview-destination" aria-label="整理根目录"><IconFolder :size="16" aria-hidden="true" /><p :title="targetDirectory || '尚未配置'">{{ targetDirectory || '尚未配置整理目录' }}</p></div><button type="button" class="ae-preview-option" :aria-pressed="showDetails" @click="showDetails = !showDetails">详细信息</button><button type="button" class="ae-preview-option" :aria-expanded="showExplanation" :aria-controls="`preview-help-${source.ref}`" @click="showExplanation = !showExplanation">说明</button></div>
      <div v-if="showExplanation" :id="`preview-help-${source.ref}`" class="ae-preview-boundary"><p>匹配与命名按当前表单预览；处理状态来自已保存的后台记录，不检查网盘当前位置。同集同规格只选当前最高版本，选定后不自动换版。命名使用公共剧集模板，按发布标题预览，不含扩展名；实际整理以下载文件信息为准。已创建任务保留创建时的模板。</p><p>{{ summary.stopReason === 'source_limit' ? source.provider === 'garden' ? 'Anime Garden 最多预览最近 1000 条。' : 'AniBT 仅提供最近 100 条。' : summary.unloadedHistory ? '已读取本次可获取的历史，受来源或读取上限影响，可能不完整。' : '已到来源当前可见历史末尾，不代表整部动画发布完成。' }}{{ valid ? `约 ${Math.max(1, Math.ceil((summary.expiresAt - now) / 60000))} 分钟后过期。` : '待重新预览。' }}</p><p v-if="summary.loadedFirst != null">匹配编号 {{ summary.loadedFirst }} 至 {{ summary.loadedLast }}<span v-if="summary.gapsWithinLoadedMatches.length">；缺口：{{ summary.gapsWithinLoadedMatches.map(gap => gap.first === gap.last ? String(gap.first) : `${gap.first}–${gap.last}`).join('、') }}</span>。</p><p v-if="summary.stopReason === 'no_progress'">本次展开无进展，请稍后重新预览。</p></div>
      <p :id="`preview-scroll-${source.ref}`" class="sr-only">表格内可左右滚动，查看完整名称。</p>
      <div ref="list" class="ae-release-list" role="region" tabindex="0" aria-label="资源列表" :aria-describedby="rows.length ? `preview-scroll-${source.ref}` : undefined" @scroll="rememberScroll">
        <p v-if="!page.total" class="ae-preview-empty">来源读取成功，当前没有发布。后台按保存的规则检查更新。</p>
        <p v-else-if="!rows.length" class="ae-preview-empty">没有此类发布，可切换显示条件。</p>
        <table v-else class="ae-preview-table" role="table" aria-label="原标题与整理名称对照">
          <colgroup><col class="ae-rule-column" /><col class="ae-state-column" /><col class="ae-feed-column" /><col class="ae-group-column" /><col class="ae-title-column" /><col class="ae-name-column" /><col class="ae-date-column" /></colgroup>
          <thead role="rowgroup"><tr role="row"><th scope="col" class="ae-sticky-rule">规则匹配</th><th scope="col" class="ae-sticky-state">处理状态</th><th scope="col">RSS</th><th scope="col">字幕组</th><th scope="col">原标题</th><th scope="col">重命名<span class="ae-heading-note">不含扩展名</span></th><th scope="col">发布时间</th></tr></thead>
          <tbody role="rowgroup"><tr v-for="{ item, naming, filename, seasonFolder, targetPath, note, localState, published } in rows" :key="item.release.id" class="ae-release" role="row">
            <td class="ae-sticky-rule ae-release-status"><span class="ae-status" :class="item.selectable ? 'done' : item.decision === 'needs_confirmation' ? 'attention' : ''" :title="reasonText(item.reasonCode, item.selectable, item.decision, item.release.parse)">{{ item.selectable ? '符合' : item.decision === 'needs_confirmation' ? '待确认' : '过滤' }}</span></td>
            <td class="ae-sticky-state ae-release-status"><span v-if="states[localState]" class="ae-status" :class="stateTone(localState)">{{ states[localState] }}</span><span v-else class="ae-no-record" :title="localState ? '后台返回了未识别的处理状态' : '尚无后台处理记录；是否下载按已保存规则决定'">{{ localState ? '状态待核对' : '尚未处理' }}</span></td>
            <td class="ae-release-feed">{{ source.role === 'main' ? '主 RSS' : '备用' }}</td>
            <td class="ae-release-group">{{ source.groupName || '未指定' }}</td>
            <td class="ae-release-original" role="cell"><span class="ae-release-title">{{ item.release.title }}</span><span v-if="showDetails" class="ae-release-meta"><span v-if="item.projection.sourceEpisode != null">发布第 {{ item.projection.sourceEpisode }} 话</span><span v-if="item.release.parse.versionLabel" class="ae-release-version">{{ item.release.parse.versionLabel }}</span><span>{{ animeSpecLabel(item.release.parse.spec) || item.release.parse.resolution || '规格未识别' }}</span><span v-if="item.observation.sizeRaw">{{ item.observation.sizeRaw }}</span></span><span v-if="showDetails && !item.selectable" class="ae-release-reason">{{ reasonText(item.reasonCode, false, item.decision, item.release.parse) }}</span></td>
            <td class="ae-release-renamed" role="cell"><template v-if="filename"><span class="ae-release-filename" :title="targetPath">{{ filename }}</span><span v-if="showDetails" class="ae-release-meta"><span>{{ seasonFolder }}</span><span v-if="naming?.episodeName">{{ naming.episodeName }}</span></span></template><span v-else class="ae-rename-pending">{{ note }}</span></td>
            <td class="ae-release-date"><time v-if="published" :datetime="published.iso">{{ published.date }}<span>{{ published.time }}</span></time><span v-else class="ae-no-record">未提供</span></td>
          </tr></tbody>
        </table>
      </div>
      <div v-if="!summary.matchedCount && page.total" class="ae-inline-notice">当前已加载内容没有符合规则的资源。请核对规格、编号范围或补番起点，保存后由后台独立检查更新。</div>
    </template>
    <div v-else class="ae-preview-empty"><IconListSearch :size="30" aria-hidden="true" /><p>{{ animeSourceReady(source) ? '生成预览后核对匹配结果与历史边界。' : '先选择并确认此来源，再填写补番起点。' }}</p><p>预览只核对匹配与命名，保存无需先预览。</p></div>
  </section>
</template>
