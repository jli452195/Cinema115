<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import SelectMenu from './SelectMenu.vue'
import SegmentedControl from './SegmentedControl.vue'
import PaginationControl from './PaginationControl.vue'
import { subtitleLanguages, subtitleTargetName } from '../utils/subtitleLibrary'
import { subtitleScopeKey, subtitleVariantHints } from '../utils/subtitlePresentation'
import type { SubtitleMatchVideo } from '../utils/subtitleImport'
import { previewSubtitleBatch, subtitleBatchGroups, subtitleBatchVideos, subtitleVideoEpisode, type BatchSubtitleRow, type SubtitleBatchChange } from '../utils/subtitleBatch'

const props = defineProps<{ rows: BatchSubtitleRow[]; allRows: BatchSubtitleRow[]; videos: SubtitleMatchVideo[]; targetNames: Record<string, string>; protectedKeys?: ReadonlySet<string>; kind?: 'movie' | 'tv'; disabled?: boolean; externalFooter?: boolean; initialTask?: 'match' | 'language' | 'version'; scopeKey?: string; inferVersions?: boolean }>()
const emit = defineEmits<{ apply: [changes: SubtitleBatchChange[]]; close: [] }>()
const task = ref(props.initialTask || (props.kind === 'movie' ? 'language' : 'match'))
const tasks = computed(() => [{ value: 'match' as const, label: '匹配视频', disabled: props.kind === 'movie' }, { value: 'language' as const, label: '设置语言' }, { value: 'version' as const, label: '版本标记' }])
const algorithm = ref<'episode' | 'sequence'>('episode')
const algorithms = [{ value: 'episode' as const, label: '按集号匹配' }, { value: 'sequence' as const, label: '按顺序匹配' }]
const groups = computed(() => subtitleBatchGroups(props.rows))
const groupKey = ref(groups.value[0]?.key || '')
const group = computed(() => groups.value.find(item => item.key === groupKey.value))
const hints = computed(() => subtitleVariantHints(props.allRows))
const scopedRows = computed(() => props.inferVersions ? props.allRows.filter(row => subtitleScopeKey(row) === props.scopeKey && !!hints.value.get(row.key)) : group.value?.rows || [])
const groupOptions = computed(() => groups.value.map((item, index) => ({ value: item.key, label: `${index + 1}. ${Array.from(item.label).slice(0, 54).join('')}（${item.rows.length} 个）` })))
const seasons = computed(() => [...new Set(props.videos.map(video => subtitleVideoEpisode(video)?.season).filter((value): value is number => value !== undefined))].sort((a, b) => a - b))
const season = ref(seasons.value.length === 1 ? String(seasons.value[0]) : '')
const seasonOptions = computed(() => [{ value: '', label: '请选择目标季' }, ...seasons.value.map(value => ({ value: String(value), label: value === 0 ? '特别篇' : `第 ${value} 季` }))])
const targets = computed(() => subtitleBatchVideos(props.videos, season.value))
const startVideoId = ref('')
const startOptions = computed(() => [{ value: '', label: '请选择起始视频' }, ...targets.value.map(video => ({ value: video.id, label: props.targetNames[video.id] || video.name }))])
const language = ref('__keep'), onlyUnknownLanguage = ref(true), selectMatched = ref(true), acknowledged = ref(false), page = ref(1)
const version = ref(''), onlyEmptyVersion = ref(true)
const languageOptions = [{ value: '__keep', label: '请选择字幕语言' }, ...subtitleLanguages]
const plan = computed(() => previewSubtitleBatch(scopedRows.value, props.allRows, props.videos, { mode: props.inferVersions ? 'version' : task.value === 'match' ? algorithm.value : task.value, season: season.value, startVideoId: startVideoId.value, language: !props.inferVersions && task.value === 'language' ? language.value : '__keep', onlyUnknownLanguage: onlyUnknownLanguage.value, selectMatched: selectMatched.value, protectedKeys: props.protectedKeys, version: version.value, onlyEmptyVersion: onlyEmptyVersion.value, inferVersions: props.inferVersions }))
const videoNames = computed(() => new Map(props.videos.map(video => [video.id, props.targetNames[video.id] || video.name])))
const natural = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
const basename = (name: string) => name.replace(/\\/g, '/').split('/').pop() || name
const preview = computed(() => {
  const changes = new Map(plan.value.changes.map(change => [change.key, change]))
  return [...scopedRows.value].sort((a, b) => natural.compare(a.file.name, b.file.name)).map(row => {
    const change = changes.get(row.key), videoId = change?.videoId ?? row.videoId, lang = change?.language ?? row.language
    return { key: row.key, name: basename(row.file.name), path: row.path || row.file.name, target: videoId ? subtitleTargetName(videoNames.value.get(videoId) || '', row.file.name, lang, change?.version ?? row.version) : '保持未匹配', changed: !!change }
  })
})
const markers = computed(() => {
  const counts = new Map<string, number>()
  for (const row of scopedRows.value) if (!row.version) { const hint = hints.value.get(row.key) || ''; if (hint) counts.set(hint, (counts.get(hint) || 0) + 1) }
  return [...counts].map(([hint, count]) => `${hint}（${count} 项）`).join('、')
})
const customCount = computed(() => scopedRows.value.filter(row => row.version).length)
const pages = computed(() => Math.max(1, Math.ceil(preview.value.length / 20)))
const visible = computed(() => preview.value.slice((page.value - 1) * 20, page.value * 20))
const canApply = computed(() => !props.disabled && plan.value.changes.length > 0 && !plan.value.errors.length && (!plan.value.warnings.length || acknowledged.value) && (props.inferVersions || task.value !== 'language' || language.value !== '__keep'))
watch(groups, value => { if (!value.some(item => item.key === groupKey.value)) groupKey.value = value[0]?.key || '' })
watch(targets, value => { if (!value.some(video => video.id === startVideoId.value)) startVideoId.value = '' })
// 范围或预览变化后重新确认风险，不沿用之前的勾选。
watch(plan, () => { acknowledged.value = false; page.value = 1 })
function apply() { if (canApply.value) emit('apply', plan.value.changes) }
defineExpose({ canApply, changeCount: computed(() => plan.value.changes.length), apply })
</script>

<template>
  <section class="subtitle-batch" aria-label="字幕批量操作">
    <div class="batch-heading"><h3>{{ inferVersions ? '保留多个字幕版本' : '批量处理' }}</h3><p>{{ inferVersions ? '确认后写入文件名标记，区分同集的多个版本。' : '先选择字幕组和操作，再核对应用后的结果。' }}</p></div>
    <div v-if="inferVersions" class="batch-retain" role="status"><strong>{{ markers || '当前标记已填写' }}</strong><p>只处理当前来源与目录，不推断语言。<span v-if="customCount">保留 {{ customCount }} 项已有版本标记。</span></p></div>
    <template v-else>
      <label class="batch-group">作用范围<SelectMenu v-model="groupKey" :options="groupOptions" label="批量操作字幕组" :disabled="disabled" /></label>
      <SegmentedControl v-model="task" :options="tasks" label="字幕批量任务" density="compact" :disabled="disabled" />
      <div v-if="task === 'match'" class="batch-fields"><SegmentedControl v-model="algorithm" :options="algorithms" label="字幕批量匹配方式" density="compact" :disabled="disabled" /><label>目标季<SelectMenu v-model="season" :options="seasonOptions" label="批量匹配目标季" :disabled="disabled" /></label><label v-if="algorithm === 'sequence'" class="batch-start">起始视频<SelectMenu v-model="startVideoId" :options="startOptions" label="批量匹配起始视频" :disabled="disabled" /></label></div>
      <div v-else-if="task === 'language'" class="batch-fields"><label>字幕语言<SelectMenu v-model="language" :options="languageOptions" label="批量设置字幕语言" :disabled="disabled" /></label><label class="batch-check"><input v-model="onlyUnknownLanguage" type="checkbox" :disabled="disabled" />仅填写未指定语言</label></div>
      <div v-else class="batch-fields"><label>版本标记<input v-model="version" aria-label="批量设置版本标记" maxlength="30" placeholder="如 HKG，留空可清除" :disabled="disabled" /></label><label class="batch-check"><input v-model="onlyEmptyVersion" type="checkbox" :disabled="disabled" />仅填写空版本</label></div>
    </template>
    <div class="batch-checks"><label><input v-model="selectMatched" type="checkbox" :disabled="disabled" />同时勾选本组已匹配项</label><details v-if="!inferVersions" class="batch-help"><summary>范围与匹配说明</summary><p>{{ group?.label }}</p><p v-if="group?.rows[0]">{{ group.rows[0].path || group.rows[0].file.name }}</p><p>每次只处理一个来源、目录、语言和版本组，覆盖全部分页。已有映射和手动清空项保持不变。特典不参与正片顺序匹配；对应关系不代表时间轴已校验。</p></details></div>
    <p v-for="error in plan.errors" :key="error" role="alert" class="batch-error">{{ error }}</p><p v-for="warning in plan.warnings" :key="warning" class="batch-warning">{{ warning }}</p>
    <label v-if="plan.warnings.length" class="batch-acknowledge"><input v-model="acknowledged" type="checkbox" :disabled="disabled" />我已核对对应关系，确认按当前预览应用</label>
    <div class="batch-preview-heading"><strong>结果预览</strong><span role="status">{{ preview.length }} 个文件 · 将修改 {{ plan.changes.length }} 项</span></div>
    <div class="batch-preview"><div class="batch-table-head"><span>原字幕</span><span>应用后文件名</span></div><div v-for="row in visible" :key="row.key" class="batch-preview-row"><div><span class="batch-mobile-label">原字幕</span><p>{{ row.name }}</p><details v-if="row.path !== row.name"><summary>完整路径</summary><p>{{ row.path }}</p></details></div><div><span class="batch-mobile-label">应用后</span><p>{{ row.target }}</p><small v-if="!row.changed">保持原设置</small></div></div></div>
    <PaginationControl v-if="pages > 1" :page="page" :pages="pages" :disabled="!!disabled" label="批量预览分页" @change="page = $event" />
    <div v-if="!externalFooter" class="batch-actions"><button type="button" :disabled="disabled" @click="emit('close')">取消</button><button type="button" class="batch-apply" :disabled="!canApply" @click="apply">应用设置</button></div>
  </section>
</template>

<style scoped>
.subtitle-batch { min-width:0;font-size:13px;color:rgb(var(--content-primary)); }
.batch-heading { margin-bottom:18px; }.batch-heading h3 { font-size:20px;font-weight:650; }.batch-heading p { margin-top:5px;color:rgb(var(--content-secondary));font-size:12px;line-height:1.6; }
.batch-group { display:flex;align-items:center;gap:12px;margin-bottom:14px;min-width:0;color:rgb(var(--content-secondary));font-size:12px; }.batch-group :deep(.select-menu) { flex:1;min-width:0; }
.batch-fields { display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-top:14px; }.batch-fields>label:not(.batch-check) { display:flex;align-items:center;gap:8px;min-width:0;font-size:12px;color:rgb(var(--content-secondary)); }.batch-start { flex:1; }.batch-start :deep(.select-menu) { flex:1;min-width:0; }
input:not([type="checkbox"]) { min-height:36px;min-width:0;padding:6px 10px;border:1px solid rgb(var(--line-default));border-radius:8px;background:rgb(var(--surface-card));color:rgb(var(--content-primary)); }
.batch-checks { display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin:14px 0; }.batch-checks>label,.batch-check,.batch-acknowledge { display:flex;align-items:center;gap:7px;font-size:12px;color:rgb(var(--content-secondary)); }.batch-checks input,.batch-check input,.batch-acknowledge input { flex:none;accent-color:rgb(var(--action-primary)); }.batch-help { margin-left:auto;font-size:11px;color:rgb(var(--content-secondary)); }.batch-help summary { cursor:pointer; }.batch-help p { margin-top:6px;line-height:1.6;overflow-wrap:anywhere; }
.batch-retain { padding:12px 14px;border:1px solid rgb(var(--action-primary) / .25);border-radius:10px;background:rgb(var(--action-primary) / .06);margin:10px 0 14px;color:rgb(var(--action-link)); }.batch-retain p { margin-top:5px;font-size:12px;line-height:1.6;color:rgb(var(--content-secondary)); }
.batch-error,.batch-warning { padding:9px 12px;margin:8px 0;border-radius:8px;font-size:12px;line-height:1.6;overflow-wrap:anywhere; }.batch-error { color:rgb(var(--status-danger));background:rgb(var(--status-danger-surface)); }.batch-warning { color:rgb(var(--status-warning));background:rgb(var(--status-warning-surface)); }.batch-acknowledge { margin:12px 0;line-height:1.7; }
.batch-preview-heading { display:flex;align-items:center;gap:10px;justify-content:space-between;margin:20px 0 10px; }.batch-preview-heading span { color:rgb(var(--content-secondary));font-size:12px; }.batch-preview { border:1px solid rgb(var(--line-default));border-radius:12px;min-width:0; }.batch-table-head,.batch-preview-row { display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:16px;padding:10px 14px; }.batch-table-head { background:rgb(var(--surface-hover) / .25);color:rgb(var(--content-secondary));font-size:11px;border-radius:12px 12px 0 0; }.batch-preview-row { border-top:1px solid rgb(var(--line-default) / .6);font-size:12px;line-height:1.65; }.batch-preview-row>div { min-width:0;overflow-wrap:anywhere; }.batch-preview-row small,.batch-preview-row details { color:rgb(var(--content-secondary));font-size:11px; }.batch-preview-row summary { cursor:pointer;padding:3px 0; }.batch-mobile-label { display:none; }
button { display:inline-flex;align-items:center;justify-content:center;min-height:36px;padding:6px 12px;border-radius:8px;background:rgb(var(--surface-hover) / .3); }button:disabled { opacity:.45;cursor:default; }.batch-actions { display:flex;justify-content:flex-end;gap:8px;margin-top:16px; }.batch-apply { background:rgb(var(--action-primary));color:#fff; }
@media(max-width:639px) { .batch-heading { margin-bottom:14px; }.batch-heading h3 { font-size:18px; }.batch-group { flex-direction:column;align-items:stretch;gap:6px; }.batch-fields { gap:10px; }.batch-fields>label:not(.batch-check) { flex-direction:column;align-items:stretch;width:100%;gap:6px; }.batch-fields :deep(.segmented-control) { width:100%; }.batch-fields input:not([type="checkbox"]) { width:100%;font-size:16px;min-height:44px; }.batch-help { width:100%;margin-left:0; }.batch-help summary { padding:10px 0; }.batch-preview-heading { flex-wrap:wrap;margin-top:16px; }.batch-table-head { display:none; }.batch-preview-row { display:flex;flex-direction:column;gap:10px;padding:12px; }.batch-preview-row:first-of-type { border-top:0; }.batch-mobile-label { display:block;font-size:11px;color:rgb(var(--content-secondary));margin-bottom:3px; }.batch-check,.batch-checks>label,.batch-acknowledge { min-height:44px; }.batch-checks input,.batch-check input,.batch-acknowledge input { width:18px;height:18px; }button { min-height:44px; } }
</style>
