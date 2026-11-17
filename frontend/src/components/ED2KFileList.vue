<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { IconCopy, IconDownload, IconFile, IconAdjustmentsHorizontal, IconFolder } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import SegmentedControl from './SegmentedControl.vue'
import ED2KFileRow from './ED2KFileRow.vue'
import { filterTelegramFiles, groupTelegramFiles, type TelegramFile } from '../utils/telegramFiles'
import { useTelegramFileActions } from '../composables/useTelegramFileActions'
import { copyText } from '../utils/clipboard'
import { useToast } from '../composables/useToast'

const props = defineProps<{ files: TelegramFile[]; query: string; token: string; signal: AbortSignal }>()
const emit = defineEmits<{ settings: [] }>()
const actions = useTelegramFileActions(props.token, props.signal), toast = useToast()
const season = ref('all'), resolution = ref('all'), range = ref('all'), platform = ref('all'), releaseGroup = ref('all')
const sort = ref<'episode' | 'size' | 'latest'>('episode'), view = ref<'files' | 'versions'>('files')
const selected = ref(new Set<string>())
const filtersOpen = ref(false), outputOpen = ref(false), filtersID = useId(), outputID = useId()
const filterCount = computed(() => [season, resolution, range, platform, releaseGroup].filter(value => value.value !== 'all').length)
const outputLabel = computed(() => actions.targets.value.find(target => target.value === actions.targetID.value)?.label)
const options = (field: 'resolution' | 'dynamicRange' | 'platform' | 'releaseGroup', label: string) => [{ value: 'all', label }, ...[...new Set(props.files.map(file => file.metadata[field]).filter((value): value is string => !!value))].sort().map(value => ({ value, label: value }))]
const seasons = computed(() => [{ value: 'all', label: '全部季' }, ...[...new Set(props.files.map(file => file.season).filter((value): value is number => value !== undefined))].sort((a, b) => a - b).map(value => ({ value: String(value), label: value === 0 ? '特别篇' : `第 ${value} 季` }))])
const visible = computed(() => filterTelegramFiles(props.files, { query: props.query, season: season.value, resolution: resolution.value, dynamicRange: range.value, sort: sort.value }).filter(file => (platform.value === 'all' || file.metadata.platform === platform.value) && (releaseGroup.value === 'all' || file.metadata.releaseGroup === releaseGroup.value)))
const groups = computed(() => view.value === 'versions' ? groupTelegramFiles(visible.value) : [{ id: 'files', title: '', files: visible.value }])
const chosen = computed(() => props.files.filter(file => selected.value.has(file.id)))
const selectedVisibleCount = computed(() => visible.value.reduce((count, file) => count + Number(selected.value.has(file.id)), 0))
const hiddenCount = computed(() => chosen.value.length - selectedVisibleCount.value)
const allSelected = computed(() => !!visible.value.length && selectedVisibleCount.value === visible.value.length)
const partiallySelected = computed(() => selectedVisibleCount.value > 0 && !allSelected.value)
let requestedTargets = false
watch(() => props.files, files => {
  const ids = new Set(files.map(file => file.id))
  selected.value = new Set([...selected.value].filter(id => ids.has(id)))
  if (season.value !== 'all' && !files.some(file => String(file.season) === season.value)) season.value = 'all'
  for (const [filter, field] of [[resolution, 'resolution'], [range, 'dynamicRange'], [platform, 'platform'], [releaseGroup, 'releaseGroup']] as const) {
    if (filter.value !== 'all' && !files.some(file => file.metadata[field] === filter.value)) filter.value = 'all'
  }
  if (files.length && !requestedTargets) { requestedTargets = true; void actions.loadTargets() }
}, { immediate: true })
function toggle(file: TelegramFile) {
  if (actions.busy.value) return
  if (selected.value.has(file.id)) selected.value.delete(file.id)
  else selected.value.add(file.id)
}
function selectVisible(checked: boolean) {
  if (actions.busy.value) return
  for (const file of visible.value) {
    if (checked) selected.value.add(file.id)
    else selected.value.delete(file.id)
  }
}
async function copy(files: TelegramFile[]) {
  try { await copyText(files.map(file => file.url).join('\n')); toast.success(`已复制 ${files.length} 条 ED2K 链接`) }
  catch { toast.error('复制失败，请再次尝试') }
}
function reset() { season.value = resolution.value = range.value = platform.value = releaseGroup.value = 'all' }
async function generate(files: TelegramFile[]) {
  if (!actions.targetID.value) outputOpen.value = true
  await actions.generate(files)
}
</script>

<template>
  <section class="ed-workspace" aria-label="ED2K 文件">
    <div class="ed-toolbar">
      <button type="button" class="ed-tool-button ed-filter-toggle" :class="{ 'is-active': filterCount }" aria-label="规格筛选" :aria-expanded="filtersOpen" :aria-controls="filtersID" @click="filtersOpen = !filtersOpen"><IconAdjustmentsHorizontal :size="16" aria-hidden="true" /><span>规格筛选</span><span v-if="filterCount">{{ filterCount }}</span></button>
      <SelectMenu v-model="sort" :options="[{ value: 'episode', label: '季集顺序' }, { value: 'size', label: '大小优先' }, { value: 'latest', label: '最新发布' }]" label="文件排序" density="compact" />
      <SegmentedControl v-model="view" :options="[{ value: 'files', label: '文件列表' }, { value: 'versions', label: '按版本' }]" label="ED2K 展示方式" density="compact" appearance="neutral"><template #option="{ option }">{{ option.value === 'files' ? '文件' : '版本' }}</template></SegmentedControl>
    </div>
    <div v-if="filtersOpen" :id="filtersID" class="ed-filterbar">
      <SelectMenu v-if="seasons.length > 1" v-model="season" :options="seasons" label="筛选季" density="compact" />
      <SelectMenu v-if="options('resolution', '').length > 1" v-model="resolution" :options="options('resolution', '全部清晰度')" label="筛选清晰度" density="compact" />
      <SelectMenu v-if="options('dynamicRange', '').length > 1" v-model="range" :options="options('dynamicRange', '全部动态范围')" label="筛选动态范围" density="compact" />
      <SelectMenu v-if="options('platform', '').length > 1" v-model="platform" :options="options('platform', '全部平台')" label="筛选平台" density="compact" />
      <SelectMenu v-if="options('releaseGroup', '').length > 1" v-model="releaseGroup" :options="options('releaseGroup', '全部发布组')" label="筛选发布组" density="compact" />
      <button v-if="filterCount" type="button" class="ed-reset" @click="reset">重置</button>
    </div>
    <div class="ed-selection">
      <label :title="`全选当前筛选的 ${visible.length} 个文件`"><input type="checkbox" :checked="allSelected" :indeterminate.prop="partiallySelected" :disabled="actions.busy.value || !visible.length" aria-label="选择当前筛选文件" @change="selectVisible(($event.target as HTMLInputElement).checked)" /><span>全选</span><span>显示 {{ visible.length }} / {{ files.length }} 个文件</span></label>
      <span v-if="chosen.length" class="ed-selected-count">已选 {{ chosen.length }}<span v-if="hiddenCount">（{{ hiddenCount }} 项在其他筛选中）</span></span>
      <button v-if="chosen.length" type="button" :disabled="actions.busy.value" @click="selected.clear()">清空选择</button>
      <button type="button" class="ed-output-toggle" :title="outputLabel || '选择 STRM 输出目录'" :aria-expanded="outputOpen" :aria-controls="outputID" @click="outputOpen = !outputOpen"><IconFolder :size="15" aria-hidden="true" /><span>{{ outputLabel || '输出目录' }}</span></button>
      <div v-if="chosen.length" class="ed-batch-actions">
        <button v-if="actions.cloudBatchRunning.value" type="button" @click="actions.stop()">停止后续提交</button>
        <button type="button" :disabled="!chosen.length" @click="copy(chosen)"><IconCopy :size="14" aria-hidden="true" />复制所选</button>
        <button type="button" :disabled="!chosen.some(actions.cloudEligible) || actions.busy.value" @click="actions.download(chosen)"><IconDownload :size="14" aria-hidden="true" />云下载所选</button>
        <button type="button" class="ed-primary" :disabled="!chosen.some(actions.strmEligible) || actions.busy.value" @click="generate(chosen)"><IconFile :size="14" aria-hidden="true" />STRM 所选</button>
      </div>
    </div>
    <div v-if="outputOpen" :id="outputID" class="ed-output">
      <span>STRM 输出到</span>
      <span v-if="actions.loading.value">正在读取目录…</span>
      <SelectMenu v-else-if="actions.targets.value.length" v-model="actions.targetID.value" :options="actions.targets.value" placeholder="请选择输出目录" label="ED2K STRM 输出目录" density="compact" :disabled="actions.busy.value" />
      <RouterLink v-else to="/115/ed2k-strm" @click="emit('settings')">配置输出目录</RouterLink>
      <button v-if="actions.setupError.value" type="button" @click="actions.loadTargets()">重试读取目录</button>
      <span v-if="actions.setupError.value" role="status">{{ actions.setupError.value }}</span>
    </div>
    <p v-if="actions.notice.value" role="status" class="ed-notice">{{ actions.notice.value }}<button v-if="actions.pendingSTRM.value" type="button" :disabled="actions.busy.value" @click="actions.retrySTRM()">重试原 STRM 批次（{{ actions.pendingSTRM.value.files.length }} 个文件）</button><RouterLink to="/115/ed2k-strm" @click="emit('settings')">STRM 任务</RouterLink><RouterLink to="/115/cloud-download" @click="emit('settings')">云下载</RouterLink></p>
    <div v-if="!visible.length" class="ed-empty"><p>{{ files.length ? '没有匹配的文件' : '暂未取得 ED2K 文件，可继续加载或查看下方清单。' }}</p><button v-if="files.length" type="button" @click="reset">重置规格筛选</button></div>
    <div v-for="group in groups" v-else :key="group.id" class="ed-group">
      <details v-if="view === 'versions'" open>
        <summary class="ed-group-title"><strong>{{ group.title }}</strong><span>{{ group.files.length }} 个文件</span></summary>
        <ul class="ed-files" aria-label="ED2K 文件列表"><ED2KFileRow v-for="file in group.files" :key="file.id" :file="file" :selected="selected.has(file.id)" :disabled="actions.busy.value" :cloud="actions.cloudStatus(file)" :strm="actions.strmStatus(file)" @select="toggle(file)" @copy="copy([file])" @download="actions.download([file])" @generate="generate([file])" /></ul>
      </details>
      <ul v-else class="ed-files" aria-label="ED2K 文件列表"><ED2KFileRow v-for="file in group.files" :key="file.id" :file="file" :selected="selected.has(file.id)" :disabled="actions.busy.value" :cloud="actions.cloudStatus(file)" :strm="actions.strmStatus(file)" @select="toggle(file)" @copy="copy([file])" @download="actions.download([file])" @generate="generate([file])" /></ul>
    </div>
  </section>
</template>

<style scoped>
.ed-workspace { min-width: 0; }.ed-filterbar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 8px; }.ed-filterbar :deep(.select-menu) { max-width: 170px; }.ed-filterbar :deep(.segmented-control) { margin-left: auto; }.ed-selection { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 12px; min-height: 40px; font-size: 12px; color: var(--search-dialog-muted); }.ed-selection label { display: inline-flex; align-items: center; gap: 8px; min-height: 36px; }.ed-selection input { width: 15px; height: 15px; accent-color: var(--search-dialog-primary); }.ed-selected-count { color: var(--search-dialog-primary); }.ed-selection button { font-size: 11px; }.ed-batch-actions { display: flex; flex-wrap: wrap; gap: 5px; margin-left: auto; }.ed-batch-actions button { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-height: 32px; padding: 5px 9px; border-radius: 7px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); }.ed-batch-actions button.ed-primary { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }button:disabled { opacity: .45; cursor: default; }
.ed-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 4px; }.ed-toolbar > :deep(.select-menu) { max-width: 150px; }.ed-toolbar > :deep(.segmented-control) { margin-left: auto; }.ed-tool-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 6px 10px; border-radius: 8px; background: var(--search-dialog-secondary-background); font-size: 12px; }.ed-tool-button.is-active { color: var(--search-dialog-primary); background: var(--search-dialog-action-background); }.ed-output-toggle { display: inline-flex; align-items: center; justify-content: flex-end; gap: 5px; min-width: 0; max-width: 160px; min-height: 36px; margin-left: auto; color: var(--search-dialog-primary); }.ed-output-toggle span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.ed-output-toggle svg { flex: none; }.ed-filterbar { margin-top: 8px; padding: 8px; border-radius: 8px; background: var(--search-dialog-card); }.ed-reset { min-height: 36px; padding-inline: 8px; font-size: 12px; color: var(--search-dialog-primary); }
.ed-output { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; min-height: 40px; margin-bottom: 8px; font-size: 11px; color: var(--search-dialog-muted); }.ed-output :deep(.select-menu) { max-width: 220px; }.ed-output a, .ed-output button, .ed-notice a { color: var(--search-dialog-primary); }.ed-files { min-width: 0; border: 1px solid var(--search-dialog-card-border); border-radius: 10px; background: var(--search-dialog-card); overflow: clip; }.ed-group { min-width: 0; margin-top: 8px; }.ed-group-title { display: flex; align-items: center; gap: 12px; padding: 10px 4px; cursor: pointer; font-size: 12px; }.ed-group-title strong { min-width: 0; overflow-wrap: anywhere; font-weight: 500; }.ed-group-title span { flex: none; color: var(--search-dialog-muted); }.ed-notice { display: flex; flex-wrap: wrap; gap: 5px 12px; padding: 10px; margin-bottom: 8px; border-radius: 8px; background: var(--search-dialog-tonal); font-size: 12px; overflow-wrap: anywhere; }.ed-empty { text-align: center; padding: 24px 12px; color: var(--search-dialog-muted); font-size: 12px; }.ed-empty button { margin-top: 8px; color: var(--search-dialog-primary); }
button:focus-visible, input:focus-visible, summary:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (max-width: 760px) { .ed-filterbar :deep(.select-menu) { max-width: calc(50% - 3px); flex: 1 1 125px; }.ed-filterbar :deep(.segmented-control) { margin-left: 0; }.ed-batch-actions { width: 100%; margin-left: 0; }.ed-batch-actions button { flex: 1; min-height: 44px; padding-inline: 6px; }.ed-selection label, .ed-selection > button { min-height: 44px; }.ed-output { margin-block: 8px; } }
@media (max-width: 639px) { .ed-toolbar > :deep(.select-menu) { flex: 1; max-width: 130px; }.ed-filter-toggle { min-width: 44px; min-height: 44px; padding-inline: 10px; }.ed-filter-toggle > span:first-of-type { display: none; }.ed-output-toggle { max-width: 115px; }.ed-toolbar > :deep(.segmented-control) { margin-left: auto; }.ed-output { padding: 8px; border-radius: 8px; background: var(--search-dialog-card); }.ed-reset { min-height: 44px; } }
@media (max-width: 400px) { .ed-toolbar > :deep(.select-menu) { min-width: 110px; }.ed-toolbar > :deep(.segmented-control) { margin-left: auto; } }
</style>
