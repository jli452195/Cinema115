<script setup lang="ts">
import SelectMenu from './SelectMenu.vue'
import PaginationControl from './PaginationControl.vue'
import SubtitleBatchPanel from './SubtitleBatchPanel.vue'
import type { SubtitleBatchChange } from '../utils/subtitleBatch'
import { computed, ref, watch } from 'vue'
import { subtitleLanguages, subtitleTargetName } from '../utils/subtitleLibrary'
import type { SubtitleMatchRow, SubtitleMatchVideo } from '../utils/subtitleImport'

const props = defineProps<{ rows: SubtitleMatchRow[]; videos: SubtitleMatchVideo[]; targetNames: Record<string, string>; disabled?: boolean; kind?: 'movie' | 'tv'; protectedKeys?: ReadonlySet<string> }>()
const emit = defineEmits<{ 'batch-start': []; 'batch-end': [] }>()
const language = ref('all'), batchOpen = ref(false), batchNotice = ref('')
type BatchSnapshot = { row: SubtitleMatchRow; before: SubtitleBatchChange; after: SubtitleBatchChange }
const undo = ref<BatchSnapshot[]>([])
const state = (row: SubtitleMatchRow): SubtitleBatchChange => ({ key: row.key, videoId: row.videoId, language: row.language, selected: row.selected })
function applyBatch(changes: SubtitleBatchChange[]) {
  if (props.disabled) return
  const index = new Map(props.rows.map(row => [row.key, row]))
  undo.value = changes.flatMap(change => { const row = index.get(change.key); return row ? [{ row, before: state(row), after: change }] : [] })
  batchOpen.value = false
  emit('batch-start')
  try { for (const item of undo.value) Object.assign(item.row, item.after) } finally { emit('batch-end') }
  batchNotice.value = '已应用批量设置，共修改 ' + undo.value.length + ' 个字幕。'
}
function undoBatch() {
  if (props.disabled) return
  let restored = 0
  emit('batch-start')
  try {
    for (const item of undo.value) {
      // 撤销只恢复仍与本次应用结果一致的项目，不覆盖之后的手动编辑。
      if (props.rows.includes(item.row) && JSON.stringify(state(item.row)) === JSON.stringify(item.after)) { Object.assign(item.row, item.before); restored++ }
    }
  } finally { emit('batch-end') }
  batchNotice.value = '已撤销 ' + restored + ' 项，其后手动编辑的项目保持不变。'
  undo.value = []
}
const page = ref(1), pageSize = 25
const filtered = computed(() => props.rows.filter(row => language.value === 'all' || row.language === language.value))
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const visible = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
// 每页复用选项和文件名索引，避免字幕与视频数量相乘后反复创建相同数据。
const videoOptions = computed(() => [{ value: '', label: '请选择对应视频' }, ...props.videos.map(video => ({ value: video.id, label: video.name }))])
const videoNames = computed(() => new Map(props.videos.map(video => [video.id, video.name])))
const languageOptions = subtitleLanguages.map(lang => ({ value: lang.value, label: lang.label }))
const availableLanguages = computed(() => subtitleLanguages.filter(lang => props.rows.some(row => row.language === lang.value)))
const filterOptions = computed(() => [{ value: 'all', label: '全部' }, ...availableLanguages.value.map(lang => ({ value: lang.value, label: lang.label }))])
const pairs = computed(() => {
  const index = new Map<string, SubtitleMatchRow[]>()
  for (const row of props.rows) if (row.pairKey) { const group = index.get(row.pairKey) || []; group.push(row); index.set(row.pairKey, group) }
  return index
})
watch(availableLanguages, options => { if (language.value !== 'all' && !options.some(option => option.value === language.value)) language.value = 'all' })
watch(language, () => { page.value = 1 })
watch(pages, value => { page.value = Math.min(page.value, value) })
function sync(row: SubtitleMatchRow) { if (row.pairKey) for (const other of pairs.value.get(row.pairKey) || []) if (other !== row) Object.assign(other, { videoId: row.videoId, language: row.language, version: row.version, selected: row.selected }) }
function selectFiltered() { for (const row of filtered.value) { row.selected = !!row.videoId; sync(row) } }
</script>

<template>
  <div class="subtitle-match">
    <div class="match-filters"><label>语言 <SelectMenu v-model="language" label="筛选字幕语言" :disabled="disabled" :options="filterOptions" /></label><button type="button" :disabled="disabled" @click="selectFiltered">选择当前语言全部已匹配项</button><button type="button" :disabled="disabled" @click="rows.forEach(row => row.selected = false)">清空选择</button><button type="button" class="match-batch-button" :disabled="disabled || !filtered.length" :aria-expanded="batchOpen" @click="batchOpen = !batchOpen">批量匹配 / 设置语言</button></div>
    <p v-if="batchNotice" class="match-target" role="status">{{ batchNotice }} <button v-if="undo.length" type="button" :disabled="disabled" @click="undoBatch">撤销本次批量操作</button></p>
    <SubtitleBatchPanel v-if="batchOpen" :rows="filtered" :all-rows="rows" :videos="videos" :target-names="targetNames" :kind="kind" :protected-keys="protectedKeys" :disabled="disabled" @apply="applyBatch" @close="batchOpen = false" />
    <p v-if="pages > 1" class="match-target" role="status">共 {{ filtered.length }} 个字幕，每页 {{ pageSize }} 个；选择和编辑会保留。</p>
    <ul class="match-files"><li v-for="row in visible" :key="row.key">
      <label class="match-name"><input v-model="row.selected" type="checkbox" :disabled="disabled" :aria-label="`选择字幕 ${row.file.name}`" @change="sync(row)" /><span>{{ row.file.name }}<small v-if="row.sourceLabel">{{ row.sourceLabel }}</small></span></label>
      <div class="match-mapping"><SelectMenu v-model="row.videoId" :label="`${row.file.name} 对应视频`" :disabled="disabled" @change="sync(row)" :options="videoOptions" /><SelectMenu v-model="row.language" :label="`${row.file.name} 语言`" :disabled="disabled" @change="sync(row)" :options="languageOptions" /><input v-if="row.version !== undefined" v-model="row.version" :aria-label="`${row.file.name} 版本`" :disabled="disabled" maxlength="30" placeholder="版本（可选）" @input="sync(row)" /></div>
      <p v-if="row.videoId" class="match-target">整理后：{{ subtitleTargetName(targetNames[row.videoId] || videoNames.get(row.videoId) || '', row.file.name, row.language, row.version) }}</p><p v-else class="match-target">{{ row.reason }}</p>
    </li></ul>
    <PaginationControl v-if="pages > 1" :page="page" :pages="pages" :disabled="!!disabled" label="字幕列表分页" @change="page = $event" />
  </div>
</template>

<style scoped>
.subtitle-match { min-width:0;font-size:13px; }
.match-filters,.match-mapping { display:flex;align-items:center;gap:8px;flex-wrap:wrap; }
.match-filters { margin:12px 0; }
input:not([type="checkbox"]) { min-height:36px;padding:6px 10px;border:1px solid rgb(148 163 184 / .25);border-radius:9px;background:var(--floating-surface-container,#fff);color:inherit; }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }
button:disabled { opacity:.45; }.match-batch-button { color:light-dark(#1d4ed8,#93c5fd);background:rgb(59 130 246 / .1); }
.match-files { list-style:none;margin:0;padding:0; }
.match-files li { padding:12px 0;border-bottom:1px solid rgb(148 163 184 / .14); }
.match-name { display:flex;align-items:flex-start;gap:8px;overflow-wrap:anywhere; }
.match-name span { min-width:0; }
.match-name small { display:block;margin-top:3px;font-size:11px;color:var(--floating-surface-muted,#64748b); }
.match-name input { flex:none;margin-top:3px;accent-color:#2563eb; }
.match-mapping { margin:8px 0 0 23px; }
.match-mapping > :first-child { flex:1;min-width:0; }
.match-mapping input { width:130px;min-width:0; }
.match-target { margin:6px 0 0 23px;font-size:12px;color:var(--floating-surface-muted,#64748b);overflow-wrap:anywhere; }
@media(max-width:639px) { input:not([type="checkbox"]),button { min-height:44px;font-size:16px; } .match-mapping > :first-child { flex-basis:100%;width:100%; } .match-mapping input { flex:1;width:110px; } }
</style>
