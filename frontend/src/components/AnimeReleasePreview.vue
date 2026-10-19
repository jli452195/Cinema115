<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconRefresh, IconChevronLeft, IconChevronRight, IconListSearch } from '@tabler/icons-vue'
import { animeSourceReady, animeSpecLabel, type SourceForm } from '../utils/animeSourceForm'
const props = defineProps<{ source: SourceForm; valid: boolean; busy: boolean; disabled: boolean; now: number; remaining: number; settingsAvailable: boolean }>()
defineEmits<{ generate: []; load: []; page: [page: number]; select: [id: string, checked: boolean]; dismiss: [id: string]; clearRecovery: [] }>()
const filter = ref('all')
const page = computed(() => props.source.preview?.page)
const summary = computed(() => props.source.preview?.summary)
const rows = computed(() => page.value?.items.filter(item => filter.value === 'all' || (filter.value === 'matched' ? item.selectable : item.decision === 'needs_confirmation')) || [])
const stale = computed(() => !!props.source.preview && !props.valid)
const disabledRows = computed(() => props.disabled || props.busy || !props.valid)
const canLoad = computed(() => props.valid && !!summary.value?.unloadedHistory && props.remaining > 0 && props.source.preview!.take < 2000 && !['no_progress', 'budget_exhausted', 'source_limit'].includes(summary.value?.stopReason || ''))
const reasons: Record<string, string> = { TITLE_SPECIFICATION_UNKNOWN: '所需规格未识别', TITLE_SPECIFICATION_MISMATCH: '规格不匹配', EPISODE_NUMBER_UNRESOLVED: '编号需确认', OUTSIDE_CHAPTER_SCOPE: '不在篇章范围', BELOW_SOURCE_MINIMUM: '低于补番起点', TITLE_RESOLUTION_MISMATCH: '画质不匹配', REQUIRED_KEYWORD_MISSING: '未包含指定词', EXCLUDED_KEYWORD: '命中排除词', HASH_NOT_VERIFIED: '保存配置后仍需核验资源', SCOPE_UNCONFIRMED: '篇章范围未确认', SOURCE_RULES_INCOMPLETE: '规则尚未完整' }
function reasonText(code: string, selectable: boolean, decision: string) { return selectable ? '符合规则' : reasons[code] || (decision === 'needs_confirmation' ? '需人工确认' : '已过滤') }
</script>

<template>
  <section class="ae-preview" aria-label="资源预览" :aria-busy="busy">
    <div class="anime-editor-section-heading"><div><h2>匹配预览</h2><p>核对已读取的发布与编号，不按集号合并版本。</p></div><button type="button" class="ae-button" :disabled="disabled || busy || !animeSourceReady(source) || !settingsAvailable || source.previewCooldown > now" @click="$emit('generate')"><IconRefresh :size="16" :class="{ 'animate-spin': busy }" />{{ busy ? '处理中…' : source.preview ? '重新预览' : '生成预览' }}</button></div>
    <p v-if="source.previewError" role="status" class="ae-error">{{ source.previewError }}</p>
    <p v-if="source.previewCooldown > now" class="ae-note">来源冷却中，{{ Math.ceil((source.previewCooldown - now) / 1000) }} 秒后可重试。</p>
    <p v-if="stale" class="ae-inline-notice" role="status">{{ summary && (summary.expired || summary.expiresAt <= now) ? '此来源预览已过期。' : '规则或目录已变化。' }}已选记录保留待核对，请重新预览。</p>
    <div v-if="source.recovery.length" class="ae-inline-notice"><p>已恢复 {{ source.recovery.length }} 条历史选择记录，尚未授权执行。重新预览后请手动核对未匹配记录。</p><button type="button" class="ae-link" :disabled="disabled || busy" @click="$emit('clearRecovery')">放弃这些旧记录</button></div>
    <template v-if="summary && page">
      <div class="ae-preview-stats"><span>已读 <b>{{ summary.loadedCount }}</b></span><span>符合 <b>{{ summary.matchedCount }}</b></span><span>需确认 <b>{{ summary.reviewCount }}</b></span><span>{{ valid ? '已选' : '旧选择待核对' }} <b>{{ source.selected.length }}</b></span><span class="ae-preview-age">{{ valid ? `约 ${Math.max(1, Math.ceil((summary.expiresAt - now) / 60000))} 分钟后过期` : '待重新预览' }}</span></div>
      <div class="ae-preview-boundary"><p>{{ summary.stopReason === 'source_limit' ? 'AniBT 仅提供最近 100 条资源，当前窗口以外的历史未加载，不会自动补齐。' : summary.unloadedHistory ? '当前为部分历史，可继续加载；未加载部分不会自动补齐。' : '已到来源当前可见历史末尾，不代表整部动画已经发布完成。' }}</p><p v-if="summary.loadedFirst != null">已加载匹配编号 {{ summary.loadedFirst }} 至 {{ summary.loadedLast }}<span v-if="summary.gapsWithinLoadedMatches.length">；其中缺口：{{ summary.gapsWithinLoadedMatches.map(gap => gap.first === gap.last ? String(gap.first) : `${gap.first}–${gap.last}`).join('、') }}</span>。</p><p v-if="summary.stopReason === 'no_progress'">本次展开无进展，请稍后重新预览。</p></div>
      <div class="ae-preview-tools"><label class="ae-label" :for="`preview-filter-${source.ref}`">显示</label><select :id="`preview-filter-${source.ref}`" v-model="filter"><option value="all">本页全部发布</option><option value="matched">本页符合规则</option><option value="review">本页需确认</option></select><span>共享读取预算剩余 {{ remaining }} 行</span></div>
      <div class="ae-release-list" role="region" tabindex="0" aria-label="本页资源列表">
        <p v-if="!page.total" class="ae-preview-empty">来源读取成功，当前没有发布。可以保存配置，之后再预览。</p>
        <p v-else-if="!rows.length" class="ae-preview-empty">本页没有此类发布，可切换显示条件或翻页。</p>
        <label v-for="item in rows" :key="item.release.id" class="ae-release" :class="{ 'ae-release-review': item.decision === 'needs_confirmation' }">
          <input type="checkbox" :checked="source.selected.includes(item.release.id)" :disabled="disabledRows || !item.selectable" :aria-label="`选择 ${item.release.title}`" @change="$emit('select', item.release.id, ($event.target as HTMLInputElement).checked)" />
          <span class="ae-release-content"><span class="ae-release-title">{{ item.release.title }}</span><span class="ae-release-meta"><span v-if="item.projection.sourceEpisode != null">第 {{ item.projection.sourceEpisode }} 话<span v-if="item.projection.chapterEpisode != null"> / 篇章第 {{ item.projection.chapterEpisode }} 集</span></span><span>{{ animeSpecLabel(item.release.parse.spec) || item.release.parse.resolution || '规格未识别' }}</span><span v-if="item.observation.sizeRaw">{{ item.observation.sizeRaw }}</span><span :class="{ 'ae-match': item.selectable }">{{ reasonText(item.reasonCode, item.selectable, item.decision) }}</span></span></span>
        </label>
      </div>
      <div v-if="!summary.matchedCount && page.total" class="ae-inline-notice">当前已加载内容没有符合规则的资源。请核对规格、编号范围或补番起点，也可以先保存配置。</div>
      <details v-if="page.pendingSelections.length" class="ae-pending"><summary>{{ page.pendingSelections.length }} 条旧选择待核对</summary><div v-for="pending in page.pendingSelections" :key="pending.releaseId"><span>{{ pending.previousItem?.release.title || '原资源记录' }}<small>{{ pending.reason === 'changed' ? '内容已变化，需重新选择' : '不在本次已加载范围' }}</small></span><button type="button" class="ae-link" :disabled="disabledRows" @click="$emit('dismiss', pending.releaseId)">放弃旧选择</button></div></details>
      <footer class="ae-preview-footer"><div class="ae-page-buttons"><button type="button" class="ae-icon" aria-label="资源上一页" :disabled="disabledRows || page.page <= 1" @click="$emit('page', page.page - 1)"><IconChevronLeft :size="18" /></button><span>{{ page.page }} / {{ Math.max(1, Math.ceil(page.total / page.pageSize)) }}</span><button type="button" class="ae-icon" aria-label="资源下一页" :disabled="disabledRows || page.page * page.pageSize >= page.total" @click="$emit('page', page.page + 1)"><IconChevronRight :size="18" /></button></div><button type="button" class="ae-button" :disabled="disabledRows || !canLoad" @click="$emit('load')">继续加载历史</button></footer>
    </template>
    <div v-else class="ae-preview-empty"><IconListSearch :size="30" aria-hidden="true" /><p>{{ animeSourceReady(source) ? '生成预览后核对匹配结果与历史边界。' : '先选择并确认此来源，再填写补番起点。' }}</p><p>预览不可用时，仍可保存完整的暂停配置或草稿。</p></div>
  </section>
</template>
