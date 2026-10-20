<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconRefresh, IconChevronLeft, IconChevronRight, IconListSearch, IconFolder, IconArrowsHorizontal } from '@tabler/icons-vue'
import { animeSourceReady, animeSpecLabel, type SourceForm } from '../utils/animeSourceForm'
import { sourceNaming } from '../composables/useAnimeNaming'
import { animeNamingFolder, projectAnimeNaming } from '../utils/animeNamingPreview'
import type { AnimeNamingTarget } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; valid: boolean; busy: boolean; disabled: boolean; now: number; remaining: number; settingsAvailable: boolean; namingTarget?: AnimeNamingTarget; namingBusy: boolean; organizePath?: string | null }>()
defineEmits<{ generate: []; load: []; page: [page: number]; select: [id: string, checked: boolean]; dismiss: [id: string]; clearRecovery: [] }>()
const filter = ref('all')
const page = computed(() => props.source.preview?.page)
const summary = computed(() => props.source.preview?.summary)
const mapping = computed(() => {
  if (!props.namingTarget) return { value: null, error: '' }
  try { return { value: sourceNaming(props.source, props.namingTarget), error: '' } }
  catch (reason) { return { value: null, error: (reason as Error).message } }
})
const targetDirectory = computed(() => {
  const folder = props.namingTarget ? animeNamingFolder(props.namingTarget) : ''
  return folder && props.organizePath ? `${props.organizePath.replace(/\/+$/, '')}/${folder}` : props.organizePath || folder
})
const rows = computed(() => (page.value?.items.filter(item => filter.value === 'all' || (filter.value === 'matched' ? item.selectable : item.decision === 'needs_confirmation')) || []).map(item => {
  const naming = props.namingTarget && !props.namingBusy && !mapping.value.error ? projectAnimeNaming(props.namingTarget, mapping.value.value, item.release.parse, props.source.groupName) : null
  const parts = naming?.pathStem?.split('/') || []
  const code = naming?.season != null && naming.episode != null ? `S${String(naming.season).padStart(2, '0')}E${String(naming.episode).padStart(2, '0')}` : ''
  const note = props.namingBusy ? '正在读取 TMDB 资料' : mapping.value.error ? '映射设置待修正' : !props.namingTarget ? '选择 TMDB 后显示名称' : naming?.status === 'ignored' ? '已设为不整理' : naming?.status === 'target_missing' ? `${code} 的 TMDB 资料待核对` : '未设置对应，保留原名'
  return { item, naming, filename: parts.at(-1) || '', seasonFolder: parts.at(-2) || '', note }
}))
const stale = computed(() => !!props.source.preview && !props.valid)
const disabledRows = computed(() => props.disabled || props.busy || !props.valid)
const canLoad = computed(() => props.valid && !!summary.value?.unloadedHistory && props.remaining > 0 && props.source.preview!.take < 2000 && !['no_progress', 'budget_exhausted', 'source_limit'].includes(summary.value?.stopReason || ''))
const reasons: Record<string, string> = { TITLE_SPECIFICATION_UNKNOWN: '所需规格未识别', TITLE_SPECIFICATION_MISMATCH: '规格不匹配', EPISODE_NUMBER_UNRESOLVED: '编号需确认', OUTSIDE_CHAPTER_SCOPE: '不在篇章范围', BELOW_SOURCE_MINIMUM: '低于补番起点', TITLE_RESOLUTION_MISMATCH: '画质不匹配', REQUIRED_KEYWORD_MISSING: '未包含指定词', EXCLUDED_KEYWORD: '命中排除词', HASH_NOT_VERIFIED: '开始订阅后核验资源', SCOPE_UNCONFIRMED: '篇章范围未确认', SOURCE_RULES_INCOMPLETE: '规则尚未完整', RELEASE_VERSION_SUPERSEDED: '同集已有更高版本', RELEASE_VERSION_DUPLICATE: '同集同版本，已保留一条', RELEASE_VERSION_UNCLEAR: '修订版本需确认', RELEASE_EDITION_UNCLEAR: '资源规格冲突，需确认', RELEASE_EDITION_ALREADY_SELECTED: '此集已选定，不再换版' }
function reasonText(code: string, selectable: boolean, decision: string) { return selectable ? '符合规则' : reasons[code] || (decision === 'needs_confirmation' ? '需人工确认' : '已过滤') }
</script>

<template>
  <section class="ae-preview" aria-label="资源预览" :aria-busy="busy">
    <div class="anime-editor-section-heading"><div><h2>匹配预览</h2><p>每个来源的同集同规格只选当前最高版本，选定后不再自动换版。</p></div><button type="button" class="ae-button" :disabled="disabled || busy || !animeSourceReady(source) || !settingsAvailable || source.previewCooldown > now" @click="$emit('generate')"><IconRefresh :size="16" :class="{ 'animate-spin': busy }" />{{ busy ? '处理中…' : source.preview ? '重新预览' : '生成预览' }}</button></div>
    <p v-if="source.previewError" role="status" class="ae-error">{{ source.previewError }}</p>
    <p v-if="source.previewCooldown > now" class="ae-note">来源冷却中，{{ Math.ceil((source.previewCooldown - now) / 1000) }} 秒后可重试。</p>
    <p v-if="stale" class="ae-inline-notice" role="status">{{ summary && (summary.expired || summary.expiresAt <= now) ? '此来源预览已过期。' : '规则或目录已变化。' }}已选记录保留待核对，请重新预览。</p>
    <div v-if="source.recovery.length" class="ae-inline-notice"><p>已恢复 {{ source.recovery.length }} 条历史选择记录，尚未授权执行。重新预览后请手动核对未匹配记录。</p><button type="button" class="ae-link" :disabled="disabled || busy" @click="$emit('clearRecovery')">放弃这些旧记录</button></div>
    <template v-if="summary && page">
      <div class="ae-preview-stats"><span>已读 <b>{{ summary.loadedCount }}</b></span><span>符合 <b>{{ summary.matchedCount }}</b></span><span>需确认 <b>{{ summary.reviewCount }}</b></span><span>{{ valid ? '已选' : '旧选择待核对' }} <b>{{ source.selected.length }}</b></span><span class="ae-preview-age">{{ valid ? `约 ${Math.max(1, Math.ceil((summary.expiresAt - now) / 60000))} 分钟后过期` : '待重新预览' }}</span></div>
      <div class="ae-preview-boundary"><p>{{ summary.stopReason === 'source_limit' ? source.provider === 'garden' ? 'Anime Garden 本次最多预览最近 1000 条资源，窗口外历史未加载，不会自动补齐。' : 'AniBT 仅提供最近 100 条资源，当前窗口以外的历史未加载，不会自动补齐。' : summary.unloadedHistory ? '当前为部分历史，可继续加载；未加载部分不会自动补齐。' : '已到来源当前可见历史末尾，不代表整部动画已经发布完成。' }}</p><p v-if="summary.loadedFirst != null">已加载匹配编号 {{ summary.loadedFirst }} 至 {{ summary.loadedLast }}<span v-if="summary.gapsWithinLoadedMatches.length">；其中缺口：{{ summary.gapsWithinLoadedMatches.map(gap => gap.first === gap.last ? String(gap.first) : `${gap.first}–${gap.last}`).join('、') }}</span>。</p><p v-if="summary.stopReason === 'no_progress'">本次展开无进展，请稍后重新预览。</p></div>
      <div class="ae-preview-destination"><IconFolder :size="17" aria-hidden="true" /><div><p><span>整理目录</span>{{ targetDirectory || '尚未配置' }}</p><small>名称不含扩展名；整理时保留原扩展名，超长名称会按实际文件缩短。</small></div></div>
      <p v-if="mapping.error" class="ae-error" role="status">{{ mapping.error }}</p>
      <div class="ae-preview-tools"><label class="ae-label" :for="`preview-filter-${source.ref}`">显示</label><select :id="`preview-filter-${source.ref}`" v-model="filter"><option value="all">本页全部发布</option><option value="matched">本页符合规则</option><option value="review">本页需确认</option></select><span>共享读取预算剩余 {{ remaining }} 行</span></div>
      <p v-if="rows.length" :id="`preview-scroll-${source.ref}`" class="ae-preview-scroll-hint"><IconArrowsHorizontal :size="14" aria-hidden="true" />可在表格内左右滚动，查看完整名称</p>
      <div class="ae-release-list" role="region" tabindex="0" aria-label="本页资源列表" :aria-describedby="rows.length ? `preview-scroll-${source.ref}` : undefined">
        <p v-if="!page.total" class="ae-preview-empty">来源读取成功，当前没有发布。开始订阅后等待更新。</p>
        <p v-else-if="!rows.length" class="ae-preview-empty">本页没有此类发布，可切换显示条件或翻页。</p>
        <table v-else class="ae-preview-table" role="table" aria-label="原标题与整理名称对照">
          <colgroup><col class="ae-select-column" /><col class="ae-title-column" /><col class="ae-name-column" /><col class="ae-state-column" /></colgroup>
          <thead role="rowgroup"><tr role="row"><th scope="col" role="columnheader"><span class="sr-only">选择</span></th><th scope="col" role="columnheader">原标题</th><th scope="col" role="columnheader">重命名<span class="ae-heading-note">不含扩展名</span></th><th scope="col" role="columnheader">规则结果</th></tr></thead>
          <tbody role="rowgroup"><tr v-for="{ item, naming, filename, seasonFolder, note } in rows" :key="item.release.id" class="ae-release" role="row" :class="{ 'ae-release-review': item.decision === 'needs_confirmation', 'ae-release-selected': source.selected.includes(item.release.id) }">
            <td class="ae-release-select" role="cell"><label><input type="checkbox" :checked="source.selected.includes(item.release.id)" :disabled="disabledRows || !item.selectable" :aria-label="`选择 ${item.release.title}`" @change="$emit('select', item.release.id, ($event.target as HTMLInputElement).checked)" /></label></td>
            <td class="ae-release-original" role="cell"><span class="ae-release-title">{{ item.release.title }}</span><span class="ae-release-meta"><span v-if="item.projection.sourceEpisode != null">发布第 {{ item.projection.sourceEpisode }} 话</span><span v-if="item.release.parse.versionLabel" class="ae-release-version">{{ item.release.parse.versionLabel }}</span><span>{{ animeSpecLabel(item.release.parse.spec) || item.release.parse.resolution || '规格未识别' }}</span><span v-if="item.observation.sizeRaw">{{ item.observation.sizeRaw }}</span></span></td>
            <td class="ae-release-renamed" role="cell"><template v-if="filename"><span class="ae-release-filename">{{ filename }}</span><span class="ae-release-meta"><span>{{ seasonFolder }}</span><span v-if="naming?.episodeName">{{ naming.episodeName }}</span></span></template><span v-else class="ae-rename-pending">{{ note }}</span></td>
            <td class="ae-release-state" role="cell"><span :class="{ 'ae-match': item.selectable }">{{ reasonText(item.reasonCode, item.selectable, item.decision) }}</span><small v-if="filename && !item.selectable">{{ item.decision === 'needs_confirmation' ? '未确认，不会下载' : '已过滤，不会下载' }}</small></td>
          </tr></tbody>
        </table>
      </div>
      <div v-if="!summary.matchedCount && page.total" class="ae-inline-notice">当前已加载内容没有符合规则的资源。请核对规格、编号范围或补番起点，或直接开始订阅，等待后续更新。</div>
      <details v-if="page.pendingSelections.length" class="ae-pending"><summary>{{ page.pendingSelections.length }} 条旧选择待核对</summary><div v-for="pending in page.pendingSelections" :key="pending.releaseId"><span>{{ pending.previousItem?.release.title || '原资源记录' }}<small>{{ pending.reason === 'changed' ? '内容已变化，需重新选择' : '不在本次已加载范围' }}</small></span><button type="button" class="ae-link" :disabled="disabledRows" @click="$emit('dismiss', pending.releaseId)">放弃旧选择</button></div></details>
      <footer class="ae-preview-footer"><div class="ae-page-buttons"><button type="button" class="ae-icon" aria-label="资源上一页" :disabled="disabledRows || page.page <= 1" @click="$emit('page', page.page - 1)"><IconChevronLeft :size="18" /></button><span>{{ page.page }} / {{ Math.max(1, Math.ceil(page.total / page.pageSize)) }}</span><button type="button" class="ae-icon" aria-label="资源下一页" :disabled="disabledRows || page.page * page.pageSize >= page.total" @click="$emit('page', page.page + 1)"><IconChevronRight :size="18" /></button></div><button type="button" class="ae-button" :disabled="disabledRows || !canLoad" @click="$emit('load')">继续加载历史</button></footer>
    </template>
    <div v-else class="ae-preview-empty"><IconListSearch :size="30" aria-hidden="true" /><p>{{ animeSourceReady(source) ? '生成预览后核对匹配结果与历史边界。' : '先选择并确认此来源，再填写补番起点。' }}</p><p>预览成功后，可直接在本页开始订阅。</p></div>
  </section>
</template>
