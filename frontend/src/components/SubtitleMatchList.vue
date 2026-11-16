<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { IconAlertTriangle, IconArrowLeft, IconEdit, IconLayersIntersect, IconSettings, IconX } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import PaginationControl from './PaginationControl.vue'
import SubtitleBatchPanel from './SubtitleBatchPanel.vue'
import type { BatchSubtitleRow, SubtitleBatchChange } from '../utils/subtitleBatch'
import { subtitleLanguages, subtitleTargetName } from '../utils/subtitleLibrary'
import { subtitleOutputConflicts, subtitleScopeKey, subtitleVariantGroups, subtitleVariantHint, subtitleVariantHints } from '../utils/subtitlePresentation'
import type { SubtitleMatchRow, SubtitleMatchVideo } from '../utils/subtitleImport'

const props = withDefaults(defineProps<{ rows: SubtitleMatchRow[]; videos: SubtitleMatchVideo[]; targetNames: Record<string, string>; disabled?: boolean; kind?: 'movie' | 'tv'; protectedKeys?: ReadonlySet<string>; batchOpen?: boolean; externalFooter?: boolean }>(), { batchOpen: undefined })
const emit = defineEmits<{ 'batch-start': []; 'batch-end': []; 'update:batchOpen': [open: boolean] }>()
const ownBatchOpen = ref(false)
const batchOpen = computed({ get: () => props.batchOpen ?? ownBatchOpen.value, set: value => { ownBatchOpen.value = value; emit('update:batchOpen', value) } })
const batchPanel = ref<InstanceType<typeof SubtitleBatchPanel>>()
const batchScope = ref(''), inferVersions = ref(false), initialTask = ref<'match' | 'language' | 'version'>('match')
const language = ref('all'), conflictsOnly = ref(false), batchNotice = ref(''), page = ref(1), pageSize = 25
const editingKey = ref(''), lockedOrder = ref<string[]>([])
let editingTrigger: HTMLElement | null = null
type BatchSnapshot = { row: SubtitleMatchRow; before: SubtitleBatchChange; after: SubtitleBatchChange }
const undo = ref<BatchSnapshot[]>([])
const state = (row: SubtitleMatchRow): SubtitleBatchChange => ({ key: row.key, videoId: row.videoId, language: row.language, version: row.version || '', selected: row.selected })
const same = (a: SubtitleBatchChange, b: SubtitleBatchChange) => a.videoId === b.videoId && a.language === b.language && (a.version || '') === (b.version || '') && a.selected === b.selected
function applyChanges(changes: SubtitleBatchChange[], message = '已应用批量设置') {
  if (props.disabled) return
  const index = new Map(props.rows.map(row => [row.key, row]))
  undo.value = changes.flatMap(change => {
    const row = index.get(change.key)
    if (!row) return []
    const after = { ...change, version: change.version ?? row.version ?? '' }
    return same(state(row), after) ? [] : [{ row, before: state(row), after }]
  })
  emit('batch-start')
  try { for (const item of undo.value) Object.assign(item.row, item.after) } finally { emit('batch-end') }
  batchNotice.value = `${message}，修改 ${undo.value.length} 项。`
}
function applyBatch(changes: SubtitleBatchChange[]) { applyChanges(changes); batchOpen.value = false }
function undoBatch() {
  if (props.disabled) return
  let restored = 0
  emit('batch-start')
  try {
    for (const item of undo.value) {
      // 只恢复仍与应用结果一致的项目，保留之后的手动编辑。
      if (props.rows.includes(item.row) && same(state(item.row), item.after)) { Object.assign(item.row, item.before); restored++ }
    }
  } finally { emit('batch-end') }
  batchNotice.value = `已撤销 ${restored} 项，其后手动编辑的项目保持不变。`
  undo.value = []
}
function openBatch(scope = '', retain = false) { batchScope.value = scope; inferVersions.value = retain; initialTask.value = retain ? 'version' : props.kind === 'movie' ? 'language' : 'match'; batchOpen.value = true }
const hints = computed(() => subtitleVariantHints(props.rows))
const variants = computed(() => {
  const pageScopes = new Set(visible.value.map(row => subtitleScopeKey(row)))
  const scopes = new Map<string, Set<string>>()
  for (const row of props.rows) { const hint = hints.value.get(row.key); if (!hint || !pageScopes.has(subtitleScopeKey(row))) continue; const key = subtitleScopeKey(row), found = scopes.get(key) || new Set<string>(); found.add(hint); scopes.set(key, found) }
  // 仅计算当前页涉及的多版本目录；每个目录的覆盖仍包含全部分页的数据。
  const candidates = props.rows.filter(row => (scopes.get(subtitleScopeKey(row))?.size || 0) > 1)
  return subtitleVariantGroups(candidates, props.videos)
})
const variantScopes = computed(() => {
  const scopes = new Map<string, typeof variants.value>()
  for (const group of variants.value.filter(item => item.hint)) { const items = scopes.get(group.scopeKey) || []; items.push(group); scopes.set(group.scopeKey, items) }
  return [...scopes].filter(([, items]) => new Set(items.map(item => item.hint)).size > 1).map(([key, groups]) => ({ key, groups }))
})
const activeScope = ref('')
const currentScope = computed(() => variantScopes.value.find(scope => scope.key === activeScope.value) || variantScopes.value[0])
const scopeOptions = computed(() => variantScopes.value.map((scope, index) => { const row = scope.groups[0]?.rows[0] as BatchSubtitleRow | undefined; const folder = (row?.path || row?.file.name || '').replace(/\\/g, '/').split('/').slice(0, -1).pop() || '根目录'; return { value: scope.key, label: `${index + 1}. ${row?.sourceLabel || '字幕组'} · ${Array.from(folder).slice(0, 28).join('')}` } }))
function selectVariant(scope: string, hint: string) {
  const group = variants.value.find(item => item.scopeKey === scope && item.hint === hint)
  applyChanges(props.rows.filter(row => subtitleScopeKey(row) === scope).map(row => ({ ...state(row), selected: !!row.videoId && hints.value.get(row.key) === hint })), `已仅选 ${hint}，覆盖 ${group?.covered || 0}/${group?.total || 0} 个视频`)
}
const conflicts = computed(() => subtitleOutputConflicts(props.rows))
const conflictKeys = computed(() => new Set(conflicts.value.flatMap(group => group.rowKeys)))
const videoNames = computed(() => new Map(props.videos.map(video => [video.id, video.name])))
const natural = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
const orderedVideos = computed(() => [...props.videos].sort((a, b) => (a.season ?? Infinity) - (b.season ?? Infinity) || (a.episode ?? Infinity) - (b.episode ?? Infinity) || natural.compare(a.name, b.name)))
const videoIndex = computed(() => new Map(orderedVideos.value.map((video, index) => [video.id, index])))
const videoOptions = computed(() => [{ value: '', label: '请选择对应视频' }, ...orderedVideos.value.map(video => ({ value: video.id, label: props.targetNames[video.id] || video.name }))])
const sorted = computed(() => [...props.rows].sort((a, b) => (videoIndex.value.get(a.videoId) ?? Infinity) - (videoIndex.value.get(b.videoId) ?? Infinity) || natural.compare(a.file.name, b.file.name)))
const ordered = computed(() => {
  if (!editingKey.value) return sorted.value
  const rank = new Map(lockedOrder.value.map((key, index) => [key, index]))
  return [...props.rows].sort((a, b) => (rank.get(a.key) ?? Infinity) - (rank.get(b.key) ?? Infinity))
})
const filtered = computed(() => ordered.value.filter(row => row.key === editingKey.value || (language.value === 'all' || row.language === language.value) && (!conflictsOnly.value || conflictKeys.value.has(row.key))))
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const visible = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch(variantScopes, scopes => { if (!scopes.some(scope => scope.key === activeScope.value)) activeScope.value = scopes[0]?.key || '' }, { immediate: true })
const languageOptions = subtitleLanguages.map(lang => ({ value: lang.value, label: lang.label }))
const availableLanguages = computed(() => subtitleLanguages.filter(lang => props.rows.some(row => row.language === lang.value)))
const filterOptions = computed(() => [{ value: 'all', label: '全部语言' }, ...availableLanguages.value.map(lang => ({ value: lang.value, label: lang.label }))])
const pairs = computed(() => {
  const index = new Map<string, SubtitleMatchRow[]>()
  for (const row of props.rows) if (row.pairKey) { const group = index.get(row.pairKey) || []; group.push(row); index.set(row.pairKey, group) }
  return index
})
watch(availableLanguages, options => { if (language.value !== 'all' && !options.some(option => option.value === language.value)) language.value = 'all' })
watch([language, conflictsOnly], () => { page.value = 1; editingKey.value = '' })
watch(pages, value => { page.value = Math.min(page.value, value) })
function sync(row: SubtitleMatchRow) { if (row.pairKey) for (const other of pairs.value.get(row.pairKey) || []) if (other !== row) Object.assign(other, { videoId: row.videoId, language: row.language, version: row.version, selected: row.selected }) }
function selectFiltered() {
  const changes = new Map<string, SubtitleBatchChange>()
  for (const row of filtered.value) for (const member of row.pairKey ? pairs.value.get(row.pairKey) || [row] : [row]) changes.set(member.key, { ...state(member), selected: !!row.videoId })
  applyChanges([...changes.values()], '已全选当前筛选的已匹配字幕')
}
function clearSelected() { applyChanges(props.rows.map(row => ({ ...state(row), selected: false })), '已取消全选') }
function edit(row: SubtitleMatchRow, event: MouseEvent) {
  if (editingKey.value === row.key) { void closeEditor(); return }
  lockedOrder.value = ordered.value.map(item => item.key); editingKey.value = row.key; editingTrigger = event.currentTarget as HTMLElement
}
async function closeEditor() { if (!editingKey.value) return false; editingKey.value = ''; await nextTick(); editingTrigger?.focus({ preventScroll: true }); return true }
function episode(row: SubtitleMatchRow) {
  const video = props.videos.find(item => item.id === row.videoId)
  return video && Number.isInteger(video.season) && Number.isInteger(video.episode) ? `S${String(video.season).padStart(2, '0')} E${String(video.episode).padStart(2, '0')}` : video ? '已匹配' : '未匹配'
}
const missingLabel = (name: string) => { const video = props.videos.find(item => item.name === name); return video && Number.isInteger(video.episode) ? `S${String(video.season).padStart(2, '0')}E${String(video.episode).padStart(2, '0')}` : name.replace(/\\/g, '/').split('/').pop() || name }
const langLabel = (value: string) => subtitleLanguages.find(item => item.value === value)?.label || '未指定语言'
const basename = (name: string) => name.replace(/\\/g, '/').split('/').pop() || name
const output = (row: SubtitleMatchRow) => subtitleTargetName(props.targetNames[row.videoId] || videoNames.value.get(row.videoId) || '', row.file.name, row.language, row.version)
defineExpose({ closeEditor, cancelBatch: () => { batchOpen.value = false }, applyPending: () => batchPanel.value?.apply(), batchCanApply: computed(() => !!batchPanel.value?.canApply), batchCount: computed(() => batchPanel.value?.changeCount || 0), showConflicts: () => { conflictsOnly.value = true; page.value = 1 } })
</script>

<template>
  <div class="subtitle-match">
    <template v-if="batchOpen">
      <button type="button" class="match-back" :disabled="disabled" @click="batchOpen = false"><IconArrowLeft :size="16" aria-hidden="true" />返回字幕选择</button>
      <SubtitleBatchPanel ref="batchPanel" :rows="filtered" :all-rows="rows" :videos="videos" :target-names="targetNames" :kind="kind" :protected-keys="protectedKeys" :disabled="disabled" :external-footer="externalFooter" :initial-task="initialTask" :scope-key="batchScope" :infer-versions="inferVersions" @apply="applyBatch" @close="batchOpen = false" />
    </template>
    <template v-else>
      <section v-if="conflicts.length" class="match-conflicts" aria-label="输出名称冲突">
        <IconAlertTriangle :size="18" aria-hidden="true" /><div><strong>{{ conflicts.length }} 组输出重名</strong><p>同一视频的多个字幕将使用相同文件名。请选择一版，或添加版本标记。</p></div><button type="button" @click="conflictsOnly = !conflictsOnly">{{ conflictsOnly ? '查看全部' : '查看冲突' }}</button>
      </section>
      <label v-if="variantScopes.length > 1" class="variant-scope">当前页的版本组<SelectMenu v-model="activeScope" :options="scopeOptions" label="版本选择来源" :disabled="disabled" /></label>
      <div v-for="scope in currentScope ? [currentScope] : []" :key="scope.key" class="match-variants" aria-label="候选版本">
        <div class="variant-label"><IconLayersIntersect :size="16" aria-hidden="true" /><span>{{ scope.groups[0]?.rows[0]?.sourceLabel || '字幕' }} · {{ scope.groups.length }} 个候选版本</span><small>按文件名标记区分</small></div>
        <div class="variant-actions"><button v-for="group in scope.groups" :key="group.key" type="button" :disabled="disabled" @click="selectVariant(scope.key, group.hint)">仅选 {{ group.hint }} <span>{{ group.covered }}/{{ group.total }}</span></button><button type="button" :disabled="disabled" :data-subtitle-action="`retain:${scope.key}`" @click="openBatch(scope.key, true)">保留多版</button></div>
        <p v-for="group in scope.groups.filter(item => item.missing.length)" :key="group.key" class="variant-missing">{{ group.hint }} 缺少：{{ group.missing.slice(0, 3).map(missingLabel).join('、') }}{{ group.missing.length > 3 ? ` 等 ${group.missing.length} 个视频` : '' }}</p>
      </div>
      <div class="match-filters"><SelectMenu v-model="language" label="筛选字幕语言" :disabled="disabled" :options="filterOptions" /><span class="filter-spacer" /><button type="button" :disabled="disabled" @click="selectFiltered">全选已匹配</button><button type="button" :disabled="disabled" @click="clearSelected">取消全选</button><button type="button" class="match-batch-button" data-subtitle-action="batch" :disabled="disabled || !filtered.length" @click="openBatch()"><IconSettings :size="15" aria-hidden="true" />批量处理</button></div>
      <div v-if="batchNotice" class="match-notice" role="status"><span>{{ batchNotice }}</span><button v-if="undo.length" type="button" :disabled="disabled" @click="undoBatch">撤销</button></div>
      <div v-if="conflictsOnly" class="conflict-filter">仅显示输出重名的字幕<button type="button" aria-label="取消冲突筛选" @click="conflictsOnly = false"><IconX :size="15" aria-hidden="true" /></button></div>
      <p v-if="pages > 1" class="match-count" role="status">共 {{ filtered.length }} 个字幕，每页 {{ pageSize }} 个；选择和编辑会保留。</p>
      <ul class="match-files"><li v-for="row in visible" :key="row.key" :class="{ 'has-conflict': conflictKeys.has(row.key), 'is-editing': editingKey === row.key }">
        <div class="match-row"><input v-model="row.selected" type="checkbox" :disabled="disabled" :aria-label="`选择字幕 ${row.file.name}`" @change="sync(row)" /><div class="match-summary"><div class="match-tags"><strong class="match-episode">{{ episode(row) }}</strong><span v-if="subtitleVariantHint(row.file.name)" class="match-hint">{{ subtitleVariantHint(row.file.name) }}</span><span>{{ langLabel(row.language) }}</span><span v-if="row.version">版本 {{ row.version }}</span><span v-if="row.pairKey">IDX / SUB 联动</span><span v-if="conflictKeys.has(row.key)" class="row-conflict">输出重名</span></div><p class="match-filename" :title="row.file.name">{{ basename(row.file.name) }}</p><p v-if="!row.videoId" class="match-reason">{{ row.reason }}</p></div><button type="button" class="match-edit" :aria-label="`编辑字幕 ${row.file.name}`" :aria-expanded="editingKey === row.key" :disabled="disabled" @click="edit(row, $event)"><IconEdit :size="15" aria-hidden="true" />{{ editingKey === row.key ? '收起' : '编辑' }}</button></div>
        <div v-if="editingKey === row.key" class="match-editor"><div class="match-mapping"><label class="match-video">对应视频<SelectMenu v-model="row.videoId" :label="`${row.file.name} 对应视频`" :disabled="disabled" @change="sync(row)" :options="videoOptions" /></label><label>字幕语言<SelectMenu v-model="row.language" :label="`${row.file.name} 语言`" :disabled="disabled" @change="sync(row)" :options="languageOptions" /></label><label>版本标记<input v-model="row.version" :aria-label="`${row.file.name} 版本`" :disabled="disabled" maxlength="30" placeholder="如 HKG（可选）" @input="sync(row)" /></label></div></div>
        <details class="match-details"><summary>{{ row.videoId ? '查看输出与来源' : '查看来源' }}</summary><p v-if="row.videoId" class="match-target">整理后：{{ output(row) }}</p><p>来源：{{ row.sourceLabel || '本地导入' }}</p><p>{{ row.file.name }}</p></details>
      </li></ul>
      <p v-if="!filtered.length" class="match-count">当前筛选没有字幕。</p>
      <PaginationControl v-if="pages > 1" :page="page" :pages="pages" :disabled="!!disabled" label="字幕列表分页" @change="page = $event; editingKey = ''" />
    </template>
  </div>
</template>

<style scoped>
.subtitle-match { min-width:0;font-size:13px;color:rgb(var(--content-primary)); }
button { display:inline-flex;align-items:center;justify-content:center;gap:6px;min-height:36px;border-radius:8px;padding:6px 10px;background:rgb(var(--surface-hover) / .3);font-weight:500; }
button:hover { background:rgb(var(--surface-hover) / .6); }button:disabled { opacity:.45;cursor:default; }
.match-back { margin:6px 0 12px;background:transparent;color:rgb(var(--content-secondary)); }
.match-conflicts { display:flex;align-items:flex-start;gap:10px;padding:12px;border:1px solid rgb(var(--status-warning) / .25);border-radius:12px;background:rgb(var(--status-warning-surface));color:rgb(var(--status-warning));margin:12px 0; }
.match-conflicts>svg { flex:none;margin-top:2px; }.match-conflicts>div { flex:1;min-width:0; }.match-conflicts p { font-size:12px;line-height:1.6;margin-top:3px; }.match-conflicts button { flex:none;background:transparent; }
.match-variants { padding:10px 12px;border:1px solid rgb(var(--line-default));border-radius:12px;margin:10px 0;background:rgb(var(--surface-card) / .55); }
.variant-scope { display:flex;align-items:center;gap:10px;min-width:0;font-size:12px;color:rgb(var(--content-secondary)); }.variant-scope :deep(.select-menu) { flex:1;min-width:0; }
.variant-label { display:flex;gap:8px;align-items:center;font-size:12px;min-width:0; }.variant-label>span { overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }.variant-label>svg,.variant-label small { flex:none;color:rgb(var(--content-secondary)); }.variant-actions { display:flex;gap:8px;flex-wrap:wrap;margin-top:8px; }.variant-actions button { border:1px solid rgb(var(--line-default));background:transparent; }.variant-actions span { color:rgb(var(--content-secondary));font-size:12px; }.variant-missing { color:rgb(var(--status-warning));font-size:12px;margin-top:7px;overflow-wrap:anywhere; }
.match-filters { display:flex;align-items:center;gap:6px;flex-wrap:wrap;padding:8px 0; }.match-filters :deep(.select-menu) { max-width:170px; }.filter-spacer { flex:1; }.match-batch-button { color:rgb(var(--action-link));background:rgb(var(--action-primary) / .12); }
.match-notice { display:flex;align-items:center;gap:8px;padding:8px 12px;background:rgb(var(--action-primary) / .08);border-radius:8px;margin-bottom:8px;font-size:12px;color:rgb(var(--content-secondary)); }.match-notice>span { flex:1; }.match-notice button { color:rgb(var(--action-link));background:transparent; }
.conflict-filter { display:flex;align-items:center;gap:8px;font-size:12px;color:rgb(var(--status-warning)); }.conflict-filter button { background:transparent; }
.match-count { font-size:12px;color:rgb(var(--content-secondary));margin:6px 0; }
.match-files { list-style:none;margin:0;padding:0; }.match-files li { padding:10px 0;border-bottom:1px solid rgb(var(--line-default) / .7); }.match-row { display:flex;align-items:flex-start;gap:12px; }.match-row>input { flex:none;margin-top:5px;width:16px;height:16px;accent-color:rgb(var(--action-primary)); }.match-summary { flex:1;min-width:0; }.match-tags { display:flex;align-items:center;flex-wrap:wrap;gap:5px 10px;font-size:11px;color:rgb(var(--content-secondary)); }.match-episode { font-size:13px;font-variant-numeric:tabular-nums;color:rgb(var(--content-primary)); }.match-hint { border-radius:4px;padding:1px 6px;color:rgb(var(--action-link));background:rgb(var(--action-primary) / .1);font-weight:600; }.row-conflict { color:rgb(var(--status-warning)); }.match-filename { white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-size:12px;color:rgb(var(--content-secondary));margin-top:5px; }.match-edit { flex:none;background:transparent;color:rgb(var(--content-secondary)); }.match-reason { font-size:12px;margin-top:4px;color:rgb(var(--content-secondary));overflow-wrap:anywhere; }
.match-editor { margin:12px 0 4px 28px;padding:12px;background:rgb(var(--surface-hover) / .22);border-radius:10px; }.match-mapping { display:flex;gap:12px;flex-wrap:wrap; }.match-mapping label { display:flex;flex-direction:column;gap:6px;min-width:0;font-size:11px;color:rgb(var(--content-secondary)); }.match-video { flex:1 1 280px; }.match-mapping input { min-width:0;width:140px;min-height:36px;padding:6px 10px;border:1px solid rgb(var(--line-default));border-radius:8px;background:rgb(var(--surface-elevated));color:rgb(var(--content-primary));font-size:13px; }
.match-details { margin:5px 0 0 28px;color:rgb(var(--content-secondary));font-size:11px; }.match-details summary { cursor:pointer;padding:3px 0;width:fit-content; }.match-details p { margin:4px 0;overflow-wrap:anywhere; }
@media(min-width:640px) { .match-conflicts { align-items:center;padding:9px 12px; }.match-conflicts p { display:inline;margin-left:10px; }.match-conflicts strong { white-space:nowrap; }.match-conflicts>div { display:flex;align-items:center;flex-wrap:wrap; } }
@media(max-width:639px) { .match-conflicts { padding:8px 10px;gap:8px;align-items:center;flex-wrap:nowrap; }.match-conflicts p { display:none; }.match-conflicts strong { font-size:12px; }.match-conflicts button { margin-left:0;padding:0 2px;font-size:11px; }.variant-actions { display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:6px; }.variant-actions button { white-space:nowrap;flex-direction:column;gap:2px;padding:6px 4px;font-size:12px; }.variant-actions>button:last-child { grid-column:1 / -1;flex-direction:row; } button { min-height:44px; }.match-filters { gap:4px; }.match-filters :deep(.select-menu) { max-width:none;width:100%; }.filter-spacer { display:none; }.match-filters>button { flex:1;padding:6px;font-size:12px;white-space:nowrap; }.match-batch-button svg { display:none; }.match-row { gap:8px; }.match-row>input { width:20px;height:20px; }.match-edit { font-size:12px;padding:0 8px; }.match-edit svg { display:none; }.match-editor { margin-left:0; }.match-mapping { gap:10px; }.match-mapping label { width:100%; }.match-mapping input { width:100%;font-size:16px;min-height:44px; }.match-tags { gap:5px 8px; }.variant-label { flex-wrap:wrap; }.variant-label>span { flex:1; }.variant-actions button { flex:1; }.match-details summary { min-height:32px;padding:8px 0; } }
</style>
