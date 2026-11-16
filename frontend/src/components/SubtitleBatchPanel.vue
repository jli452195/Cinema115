<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import SelectMenu from './SelectMenu.vue'
import SegmentedControl from './SegmentedControl.vue'
import PaginationControl from './PaginationControl.vue'
import { subtitleLanguages, subtitleTargetName } from '../utils/subtitleLibrary'
import type { SubtitleMatchVideo } from '../utils/subtitleImport'
import { previewSubtitleBatch, subtitleBatchGroups, subtitleBatchVideos, subtitleVideoEpisode, type BatchSubtitleRow, type SubtitleBatchChange, type SubtitleBatchMode } from '../utils/subtitleBatch'

const props = defineProps<{ rows: BatchSubtitleRow[]; allRows: BatchSubtitleRow[]; videos: SubtitleMatchVideo[]; targetNames: Record<string, string>; protectedKeys?: ReadonlySet<string>; kind?: 'movie' | 'tv'; disabled?: boolean }>()
const emit = defineEmits<{ apply: [changes: SubtitleBatchChange[]]; close: [] }>()
const groups = computed(() => subtitleBatchGroups(props.rows))
const groupKey = ref(groups.value[0]?.key || '')
const group = computed(() => groups.value.find(item => item.key === groupKey.value))
const mode = ref<SubtitleBatchMode>(props.kind === 'movie' ? 'language' : 'episode')
const modes = [{ value: 'episode' as const, label: '按集号匹配' }, { value: 'sequence' as const, label: '按顺序匹配' }, { value: 'language' as const, label: '仅设置语言' }]
const seasons = computed(() => [...new Set(props.videos.map(video => subtitleVideoEpisode(video)?.season).filter((value): value is number => value !== undefined))].sort((a, b) => a - b))
const season = ref(seasons.value.length === 1 ? String(seasons.value[0]) : '')
const seasonOptions = computed(() => [{ value: '', label: '请选择目标季' }, ...seasons.value.map(value => ({ value: String(value), label: value === 0 ? '特别篇' : `第 ${value} 季` }))])
const targets = computed(() => subtitleBatchVideos(props.videos, season.value))
const startVideoId = ref('')
const startOptions = computed(() => [{ value: '', label: '请选择起始视频' }, ...targets.value.map(video => ({ value: video.id, label: props.targetNames[video.id] || video.name }))])
const language = ref('__keep'), onlyUnknownLanguage = ref(true), selectMatched = ref(true), acknowledged = ref(false), page = ref(1)
const languageOptions = [{ value: '__keep', label: '不修改语言' }, ...subtitleLanguages]
const plan = computed(() => previewSubtitleBatch(group.value?.rows || [], props.allRows, props.videos, { mode: mode.value, season: season.value, startVideoId: startVideoId.value, language: language.value, onlyUnknownLanguage: onlyUnknownLanguage.value, selectMatched: selectMatched.value, protectedKeys: props.protectedKeys }))
const groupOptions = computed(() => groups.value.map((item, index) => ({ value: item.key, label: `${index + 1}. ${item.label}（${item.rows.length} 个文件）` })))
const videoNames = computed(() => new Map(props.videos.map(video => [video.id, props.targetNames[video.id] || video.name])))
const natural = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
const preview = computed(() => {
  const changes = new Map(plan.value.changes.map(change => [change.key, change]))
  return [...(group.value?.rows || [])].sort((a, b) => natural.compare(a.file.name, b.file.name)).map(row => {
    const change = changes.get(row.key), videoId = change?.videoId ?? row.videoId, lang = change?.language ?? row.language
    return { key: row.key, name: row.file.name, target: videoId ? subtitleTargetName(videoNames.value.get(videoId) || '', row.file.name, lang, row.version) : '保持未匹配', changed: !!change }
  })
})
const pages = computed(() => Math.max(1, Math.ceil(preview.value.length / 20)))
const visible = computed(() => preview.value.slice((page.value - 1) * 20, page.value * 20))
const canApply = computed(() => !props.disabled && plan.value.changes.length > 0 && !plan.value.errors.length && (!plan.value.warnings.length || acknowledged.value))
watch(groups, value => { if (!value.some(item => item.key === groupKey.value)) groupKey.value = value[0]?.key || '' })
watch(targets, value => { if (!value.some(video => video.id === startVideoId.value)) startVideoId.value = '' })
// 任意预览内容或作用范围变化后都必须重新确认风险，不能沿用旧确认。
watch(plan, () => { acknowledged.value = false; page.value = 1 })
function apply() { if (canApply.value) emit('apply', plan.value.changes) }
</script>

<template>
  <section class="subtitle-batch" aria-label="字幕批量操作">
    <div class="batch-heading"><strong>批量匹配与语言设置</strong><button type="button" :disabled="disabled" @click="emit('close')">取消批量操作</button></div>
    <p class="batch-note">每次只处理一个来源、目录、语言和版本组，覆盖全部分页；已有对应关系及手动清空项不会覆盖。特典不参与正片顺序匹配。</p>
    <label class="batch-group">字幕组 <SelectMenu v-model="groupKey" :options="groupOptions" label="批量操作字幕组" :disabled="disabled" /></label>
    <SegmentedControl v-if="kind !== 'movie'" v-model="mode" :options="modes" label="字幕批量匹配方式" density="compact" :disabled="disabled" />
    <div class="batch-fields">
      <label v-if="mode !== 'language'">目标季 <SelectMenu v-model="season" :options="seasonOptions" label="批量匹配目标季" :disabled="disabled" /></label>
      <label v-if="mode === 'sequence'" class="batch-start">起始视频 <SelectMenu v-model="startVideoId" :options="startOptions" label="批量匹配起始视频" :disabled="disabled" /></label>
      <label>统一语言 <SelectMenu v-model="language" :options="languageOptions" label="批量设置字幕语言" :disabled="disabled" /></label>
    </div>
    <div class="batch-checks"><label v-if="language !== '__keep'"><input v-model="onlyUnknownLanguage" type="checkbox" :disabled="disabled" />仅填写未指定语言</label><label><input v-model="selectMatched" type="checkbox" :disabled="disabled" />同时勾选本组已匹配项</label></div>
    <p v-for="error in plan.errors" :key="error" role="alert" class="batch-error">{{ error }}</p>
    <p v-for="warning in plan.warnings" :key="warning" class="batch-warning">{{ warning }}</p>
    <p class="batch-note" role="status">预览 {{ preview.length }} 个文件，将修改 {{ plan.changes.length }} 项<span v-if="mode !== 'language'">，保留或跳过 {{ plan.skipped }} 项</span>。仅匹配文件对应关系，不校验字幕时间轴。</p>
    <div class="batch-preview"><table><thead><tr><th scope="col">原字幕</th><th scope="col">应用后文件名</th></tr></thead><tbody><tr v-for="row in visible" :key="row.key"><td>{{ row.name }}</td><td>{{ row.target }}<small v-if="!row.changed">（保留）</small></td></tr></tbody></table></div>
    <PaginationControl v-if="pages > 1" :page="page" :pages="pages" :disabled="!!disabled" label="批量预览分页" @change="page = $event" />
    <label v-if="plan.warnings.length" class="batch-acknowledge"><input v-model="acknowledged" type="checkbox" :disabled="disabled" />我已核对对应关系，确认按当前预览应用</label>
    <div class="batch-actions"><button type="button" class="batch-apply" :disabled="!canApply" @click="apply">应用批量设置</button></div>
  </section>
</template>

<style scoped>
.subtitle-batch { padding:14px;margin:12px 0;border:1px solid rgb(59 130 246 / .3);border-radius:12px;background:rgb(59 130 246 / .04);font-size:13px;min-width:0; }
.batch-heading,.batch-fields,.batch-checks,.batch-actions { display:flex;align-items:center;gap:10px;flex-wrap:wrap; }
.batch-heading { justify-content:space-between; }.batch-fields,.batch-checks { margin-top:12px; }
.batch-group,.batch-fields>label { display:flex;align-items:center;gap:8px;min-width:0; }.batch-group { margin:12px 0; }.batch-group :deep(.select-menu) { flex:1;min-width:0; }
.batch-start { flex:1; }.batch-start :deep(.select-menu) { flex:1;min-width:0; }.batch-checks label,.batch-acknowledge { display:flex;align-items:flex-start;gap:6px; }
.batch-acknowledge { margin-top:12px; }.batch-note { margin:8px 0;color:var(--floating-surface-muted,#64748b);font-size:12px;line-height:1.6; }
.batch-error,.batch-warning { margin:8px 0;font-size:12px;line-height:1.6; }.batch-error { color:light-dark(#b91c1c,#fca5a5); }.batch-warning { color:light-dark(#92400e,#fcd34d); }
.batch-preview { overflow:auto;max-height:280px;margin-top:10px;border:1px solid rgb(148 163 184 / .2);border-radius:8px; }
table { width:100%;table-layout:fixed;border-collapse:collapse;font-size:12px; }th,td { width:50%;padding:8px;text-align:left;vertical-align:top;overflow-wrap:anywhere;border-bottom:1px solid rgb(148 163 184 / .15); }th { color:var(--floating-surface-muted,#64748b); }small { color:var(--floating-surface-muted,#64748b); }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }button:disabled { opacity:.45;cursor:default; }.batch-actions { justify-content:flex-end;margin-top:12px; }.batch-apply { color:#fff;background:#2563eb; }input { accent-color:#2563eb; }
@media(max-width:639px) { button { min-height:44px; }.batch-fields>label { width:100%; }.batch-fields :deep(.select-menu) { flex:1;min-width:0; }.batch-group { align-items:flex-start; } }
</style>
