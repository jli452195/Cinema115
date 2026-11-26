<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { IconChevronDown, IconChevronRight, IconCloudDownload, IconFile, IconAdjustmentsHorizontal } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import SegmentedControl from './SegmentedControl.vue'
import ED2KFileRow from './ED2KFileRow.vue'
import { applyRangeSelection } from '../utils/organizeSelection'
import { filterTelegramFiles, groupTelegramFiles, type TelegramFile, type TelegramFileGroup } from '../utils/telegramFiles'
import { defaultTelegramFileFilters, resolveTelegramFileFilters, type TelegramFileFilterField } from '../utils/telegramFileFilters'
import { useTelegramFileActions } from '../composables/useTelegramFileActions'
import { copyText } from '../utils/clipboard'
import { useToast } from '../composables/useToast'
import { useHorizontalScrollTrack } from '../composables/useHorizontalScrollTrack'

const props = defineProps<{ files: TelegramFile[]; query: string; token: string; signal: AbortSignal }>()
const emit = defineEmits<{ settings: [] }>()
const actions = useTelegramFileActions(props.token, props.signal), toast = useToast()
const filterValues = ref(defaultTelegramFileFilters())
const sort = ref<'episode' | 'size' | 'latest'>('episode'), view = ref<'files' | 'versions'>('files')
const selected = ref(new Set<string>())
const expandedGroups = ref(new Set<string>())
const anchor = ref('')
const filtersOpen = ref(false), filtersID = useId(), groupIDPrefix = useId()
const filterBar = ref<HTMLElement>()
const filtering = computed(() => resolveTelegramFileFilters(props.files, filterValues.value, props.query))
const filterCount = computed(() => filtering.value.facets.filter(facet => facet.value !== 'all').length)
// 手机端规格筛选条仅做横向溢出渐隐与聚焦/展开居中，不启用鼠标拖动以保持桌面换行与下拉交互。
const filterBarTrack = useHorizontalScrollTrack(filterBar, {
  deps: [() => filtering.value.facets, filterCount],
  mouseDrag: false,
  itemSelector: ':scope > .select-menu, :scope > .ed-reset',
})
const outputLabel = computed(() => actions.targets.value.find(target => target.value === actions.targetID.value)?.label)
const cloudReady = actions.cloudTarget.canSubmit
const cloudTargetLabel = computed(() => actions.cloudTarget.target.value?.name)
const cloudSingleLocked = computed(() => actions.cloudTarget.targets.value.length === 1 && !!actions.cloudTarget.selectedID.value && !actions.cloudTarget.reconfirmMessage.value)
const strmSingleLocked = computed(() => actions.targets.value.length === 1 && !!actions.targetID.value)
const visible = computed(() => filterTelegramFiles(filtering.value.files, { sort: sort.value }))
const groups = computed(() => view.value === 'versions' ? groupTelegramFiles(visible.value) : [{ id: 'files', title: '', files: visible.value }])
// 当前屏幕渲染且可见的行顺序：按版本视图只包含已展开分组，避免 Shift 连选跨入折叠组。
const renderedRows = computed(() => view.value === 'versions' ? groups.value.flatMap(group => expandedGroups.value.has(group.id) ? group.files : []) : visible.value)
const chosen = computed(() => props.files.filter(file => selected.value.has(file.id)))
const selectedVisibleCount = computed(() => visible.value.reduce((count, file) => count + Number(selected.value.has(file.id)), 0))
const hiddenCount = computed(() => chosen.value.length - selectedVisibleCount.value)
const allSelected = computed(() => !!visible.value.length && selectedVisibleCount.value === visible.value.length)
const partiallySelected = computed(() => selectedVisibleCount.value > 0 && !allSelected.value)
let requestedTargets = false
watch(() => filtering.value.values, values => {
  if (filtering.value.facets.some(facet => filterValues.value[facet.key] !== facet.value)) filterValues.value = values
}, { immediate: true })
watch(() => props.files, files => {
  const ids = new Set(files.map(file => file.id))
  selected.value = new Set([...selected.value].filter(id => ids.has(id)))
  if (files.length && !requestedTargets) { requestedTargets = true; void actions.loadTargets() }
}, { immediate: true })
watch(view, () => {
  anchor.value = ''
  expandedGroups.value.clear()
})
watch(renderedRows, rows => {
  if (anchor.value && !rows.some(file => file.id === anchor.value)) anchor.value = ''
})
const groupPanelID = (id: string) => `${groupIDPrefix}-${encodeURIComponent(id)}`
const isGroupExpanded = (id: string) => expandedGroups.value.has(id)
const groupSelectedCount = (group: TelegramFileGroup) => group.files.reduce((count, file) => count + Number(selected.value.has(file.id)), 0)
const groupChecked = (group: TelegramFileGroup) => group.files.length > 0 && groupSelectedCount(group) === group.files.length
const groupPartial = (group: TelegramFileGroup) => {
  const count = groupSelectedCount(group)
  return count > 0 && count < group.files.length
}
function toggleGroupExpanded(id: string) {
  if (expandedGroups.value.has(id)) expandedGroups.value.delete(id)
  else expandedGroups.value.add(id)
  anchor.value = ''
}
function toggleGroupSelection(group: TelegramFileGroup) {
  if (actions.busy.value || !group.files.length) return
  // 部分选中时先补全整组，再点击取消；只作用当前筛选下属于该版本的成员。
  const nextSelected = !groupChecked(group)
  for (const file of group.files) {
    if (nextSelected) selected.value.add(file.id)
    else selected.value.delete(file.id)
  }
  anchor.value = ''
}
function toggle(file: TelegramFile, shiftKey = false) {
  if (actions.busy.value) return
  const nextSelected = !selected.value.has(file.id)
  if (shiftKey) {
    applyRangeSelection(renderedRows.value, anchor.value, file.id, item => {
      if (nextSelected) selected.value.add(item.id)
      else selected.value.delete(item.id)
    })
  } else if (nextSelected) {
    selected.value.add(file.id)
  } else {
    selected.value.delete(file.id)
  }
  anchor.value = file.id
}
function selectVisible(checked: boolean) {
  if (actions.busy.value) return
  for (const file of visible.value) {
    if (checked) selected.value.add(file.id)
    else selected.value.delete(file.id)
  }
  anchor.value = ''
}
async function copy(files: TelegramFile[]) {
  try { await copyText(files.map(file => file.url).join('\n')); toast.success(`已复制 ${files.length} 条 ED2K 链接`) }
  catch { toast.error('复制失败，请再次尝试') }
}
function setFilter(field: TelegramFileFilterField, value: string) { filterValues.value = { ...filtering.value.values, [field]: value } }
function reset() { filterValues.value = defaultTelegramFileFilters() }
function resolveFilterBarItem(target: HTMLElement) {
  return target.closest<HTMLElement>('.select-menu, .ed-reset')
}
function handleFilterBarFocusIn(event: FocusEvent) {
  filterBarTrack.handleFocusIn(event, resolveFilterBarItem)
}
function handleFilterBarClick(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Element)) return
  const control = target.closest<HTMLElement>('.select-menu-control')
  if (!control) return
  const item = resolveFilterBarItem(control)
  if (!item) return
  // 待 SelectMenu 刷新展开状态后居中当前触发器，弹层会通过捕获阶段的 scroll 事件自动跟随定位。
  void nextTick(() => {
    if (control.getAttribute('aria-expanded') === 'true') filterBarTrack.centerElement(item, 'smooth')
  })
}
const batchBar = computed(() => ({
  visible: chosen.value.length > 0 || actions.cloudBatchRunning.value,
  chosenCount: chosen.value.length,
  hiddenCount: hiddenCount.value,
  busy: actions.busy.value,
  cloudBatchRunning: actions.cloudBatchRunning.value,
  canCloud: cloudReady.value && chosen.value.some(actions.cloudEligible) && !actions.busy.value,
  canStrm: chosen.value.some(actions.strmEligible) && !actions.busy.value,
  cloudTargetName: cloudTargetLabel.value || '',
  strmTargetName: outputLabel.value || '',
  clear: () => { if (!actions.busy.value) { selected.value.clear(); anchor.value = '' } },
  stop: () => actions.stop(),
  copySelected: () => { if (chosen.value.length) void copy(chosen.value) },
  downloadSelected: () => { if (chosen.value.length) void actions.download(chosen.value) },
  generateSelected: () => { if (chosen.value.length) void actions.generate(chosen.value) },
}))
defineExpose({ batchBar })
</script>

<template>
  <section class="ed-workspace" aria-label="ED2K 文件">
    <div class="ed-toolbar">
      <button type="button" class="ed-tool-button ed-filter-toggle" :class="{ 'is-active': filterCount }" aria-label="规格筛选" :aria-expanded="filtersOpen" :aria-controls="filtersID" @click="filtersOpen = !filtersOpen"><IconAdjustmentsHorizontal :size="16" aria-hidden="true" /><span>规格筛选</span><span v-if="filterCount" class="ed-filter-count">{{ filterCount }}</span></button>
      <SelectMenu v-model="sort" :options="[{ value: 'episode', label: '季集顺序' }, { value: 'size', label: '大小优先' }, { value: 'latest', label: '最新发布' }]" label="文件排序" density="compact" :menu-min-width="156" />
      <SegmentedControl v-model="view" :options="[{ value: 'files', label: '文件列表', title: '文件列表' }, { value: 'versions', label: '按版本', title: '按版本分组' }]" label="ED2K 展示方式" density="compact" appearance="subtle"><template #option="{ option }">{{ option.value === 'files' ? '文件' : '版本' }}</template></SegmentedControl>
    </div>
    <div
      v-if="filtersOpen"
      :id="filtersID"
      ref="filterBar"
      class="ed-filterbar"
      :class="{ 'has-scroll-left': filterBarTrack.canScrollLeft.value, 'has-scroll-right': filterBarTrack.canScrollRight.value }"
      @scroll.passive="filterBarTrack.updateScrollEdges"
      @focusin="handleFilterBarFocusIn"
      @click="handleFilterBarClick"
    >
      <template v-for="facet in filtering.facets" :key="facet.key">
        <SelectMenu v-if="facet.options.length > 1" :class="{ 'is-active': facet.value !== 'all' }" :model-value="facet.value" :options="facet.options" :label="facet.label" density="compact" :menu-min-width="210" @update:model-value="setFilter(facet.key, $event)"><template #trigger><span class="ed-facet-label">{{ facet.selectedLabel }}</span></template></SelectMenu>
      </template>
      <button v-if="filterCount" type="button" class="ed-reset" @click="reset">重置</button>
    </div>
    <div class="ed-selection">
      <label :title="`全选当前筛选的 ${visible.length} 个文件`"><input type="checkbox" :checked="allSelected" :indeterminate.prop="partiallySelected" :disabled="actions.busy.value || !visible.length" aria-label="选择当前筛选文件" @change="selectVisible(($event.target as HTMLInputElement).checked)" /><span>全选</span><span>显示 {{ visible.length }} / {{ files.length }} 个文件</span></label>
      <div class="ed-target-controls">
        <span v-if="actions.cloudTarget.loading.value && !actions.cloudTarget.loaded.value" class="ed-target-static"><IconCloudDownload :size="15" aria-hidden="true" /><span class="ed-target-prefix">云下载</span><span>读取中…</span></span>
        <button v-else-if="actions.cloudTarget.error.value" type="button" class="ed-target-action is-error" :disabled="actions.busy.value || actions.cloudTarget.loading.value" :title="actions.cloudTarget.error.value" @click="actions.cloudTarget.load()"><IconCloudDownload :size="15" aria-hidden="true" /><span>云下载目录重试</span></button>
        <span v-else-if="cloudSingleLocked" class="ed-target-static" :title="actions.cloudTarget.target.value?.targetPath"><IconCloudDownload :size="15" aria-hidden="true" /><span class="ed-target-prefix">云下载到</span><span class="ed-target-value">{{ cloudTargetLabel }}</span></span>
        <SelectMenu
          v-else-if="actions.cloudTarget.targets.value.length"
          class="ed-target-menu"
          :class="{ 'needs-attention': !!actions.cloudTarget.reconfirmMessage.value }"
          :model-value="actions.cloudTarget.selectedID.value"
          :options="actions.cloudTarget.options.value"
          :placeholder="actions.cloudTarget.reconfirmMessage.value ? '请重新确认' : '请选择'"
          label="云下载目标目录"
          density="compact"
          :menu-min-width="196"
          :disabled="actions.busy.value || actions.cloudTarget.loading.value"
          :title="actions.cloudTarget.target.value?.targetPath || '选择云下载目录'"
          @update:model-value="actions.cloudTarget.select"
        >
          <template #trigger><span class="ed-target-trigger"><IconCloudDownload :size="15" aria-hidden="true" /><span class="ed-target-prefix">云下载到</span><span class="ed-target-value">{{ cloudTargetLabel || (actions.cloudTarget.reconfirmMessage.value ? '请重新确认' : '请选择') }}</span></span></template>
        </SelectMenu>
        <RouterLink v-else to="/115/cloud-download" class="ed-target-action" @click="emit('settings')"><IconCloudDownload :size="15" aria-hidden="true" /><span>配置云下载目录</span></RouterLink>

        <span v-if="actions.loading.value && !actions.targets.value.length" class="ed-target-static"><IconFile :size="15" aria-hidden="true" /><span class="ed-target-prefix">STRM</span><span>读取中…</span></span>
        <button v-else-if="actions.setupError.value" type="button" class="ed-target-action is-error" :disabled="actions.busy.value" :title="actions.setupError.value" @click="actions.loadTargets()"><IconFile :size="15" aria-hidden="true" /><span>STRM 目录重试</span></button>
        <span v-else-if="strmSingleLocked" class="ed-target-static" :title="outputLabel"><IconFile :size="15" aria-hidden="true" /><span class="ed-target-prefix">STRM 输出到</span><span class="ed-target-value">{{ outputLabel }}</span></span>
        <SelectMenu
          v-else-if="actions.targets.value.length"
          v-model="actions.targetID.value"
          class="ed-target-menu"
          :options="actions.targets.value"
          placeholder="请选择"
          label="ED2K STRM 输出目录"
          density="compact"
          :menu-min-width="196"
          :disabled="actions.busy.value || actions.loading.value"
          :title="outputLabel || '选择 STRM 输出目录'"
        >
          <template #trigger><span class="ed-target-trigger"><IconFile :size="15" aria-hidden="true" /><span class="ed-target-prefix">STRM 输出到</span><span class="ed-target-value">{{ outputLabel || '请选择' }}</span></span></template>
        </SelectMenu>
        <RouterLink v-else to="/115/ed2k-strm" class="ed-target-action" @click="emit('settings')"><IconFile :size="15" aria-hidden="true" /><span>配置 STRM 目录</span></RouterLink>
      </div>
    </div>
    <p v-if="actions.cloudTarget.reconfirmMessage.value" role="status" class="ed-target-hint is-warning">{{ actions.cloudTarget.reconfirmMessage.value }}</p>
    <p v-if="actions.cloudTarget.error.value" role="alert" class="ed-target-hint is-error">{{ actions.cloudTarget.error.value }}</p>
    <p v-if="actions.setupError.value" role="status" class="ed-target-hint is-error">{{ actions.setupError.value }}</p>
    <p v-if="actions.notice.value" role="status" class="ed-notice">{{ actions.notice.value }}<button v-if="actions.pendingSTRM.value" type="button" :disabled="actions.busy.value" @click="actions.retrySTRM()">重试原 STRM 批次（{{ actions.pendingSTRM.value.files.length }} 个文件）</button><RouterLink to="/115/ed2k-strm" @click="emit('settings')">STRM 任务</RouterLink><RouterLink to="/115/cloud-download" @click="emit('settings')">云下载</RouterLink></p>
    <div v-if="!visible.length" class="ed-empty"><p>{{ files.length ? '没有匹配的文件' : '暂未取得 ED2K 文件，可继续加载或查看下方清单。' }}</p><button v-if="files.length" type="button" @click="reset">重置规格筛选</button></div>
    <div v-for="group in groups" v-else :key="group.id" class="ed-group">
      <section v-if="view === 'versions'" class="ed-group-card" :class="{ 'is-expanded': isGroupExpanded(group.id) }">
        <div class="ed-group-header">
          <label class="ed-group-check" @click.stop><input type="checkbox" :checked="groupChecked(group)" :indeterminate.prop="groupPartial(group)" :disabled="actions.busy.value || !group.files.length" :aria-label="`选择版本 ${group.title}`" @change="toggleGroupSelection(group)" /></label>
          <button type="button" class="ed-group-toggle" :aria-expanded="isGroupExpanded(group.id)" :aria-controls="groupPanelID(group.id)" @click="toggleGroupExpanded(group.id)">
            <component :is="isGroupExpanded(group.id) ? IconChevronDown : IconChevronRight" :size="15" class="ed-group-chevron" aria-hidden="true" />
            <strong class="ed-group-name">{{ group.title }}</strong>
            <span class="ed-group-count"><template v-if="groupSelectedCount(group)">已选 {{ groupSelectedCount(group) }} / </template>{{ group.files.length }} 个文件</span>
          </button>
        </div>
        <ul v-if="isGroupExpanded(group.id)" :id="groupPanelID(group.id)" class="ed-files ed-group-files" aria-label="ED2K 文件列表"><ED2KFileRow v-for="file in group.files" :key="file.id" :file="file" :selected="selected.has(file.id)" :disabled="actions.busy.value" :cloud="actions.cloudStatus(file)" :cloud-disabled="!cloudReady" :strm="actions.strmStatus(file)" @select="toggle(file, $event)" @copy="copy([file])" @download="actions.download([file])" @generate="actions.generate([file])" /></ul>
      </section>
      <ul v-else class="ed-files" aria-label="ED2K 文件列表"><ED2KFileRow v-for="file in group.files" :key="file.id" :file="file" :selected="selected.has(file.id)" :disabled="actions.busy.value" :cloud="actions.cloudStatus(file)" :cloud-disabled="!cloudReady" :strm="actions.strmStatus(file)" @select="toggle(file, $event)" @copy="copy([file])" @download="actions.download([file])" @generate="actions.generate([file])" /></ul>
    </div>
  </section>
</template>

<style scoped>
.ed-toolbar > :deep(.segmented-control) { flex: none; }
.ed-facet-label { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ed-workspace { --ed-seg-indicator: rgb(255 255 255 / 92%); --ed-seg-shadow: 0 1px 3px rgb(15 23 42 / 10%), 0 0 0 1px rgb(15 23 42 / 4%); min-width: 0; }
.dark .ed-workspace { --ed-seg-indicator: rgb(255 255 255 / 14%); --ed-seg-shadow: inset 0 0 0 1px rgb(255 255 255 / 12%); }
.ed-selection { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; min-height: 36px; font-size: 12px; color: var(--search-dialog-muted); }
.ed-selection label { display: inline-flex; align-items: center; gap: 8px; min-height: 34px; }
.ed-selection input { width: 15px; height: 15px; accent-color: var(--search-dialog-primary); }
button:disabled { opacity: .45; cursor: default; }
/* 统一规格筛选、排序下拉与文件/版本分段器为全站低饱和灰蓝圆胶囊体系，消除方圆混杂与框套框。 */
.ed-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 4px; }
.ed-toolbar > :deep(.select-menu) { max-width: 150px; }
.ed-toolbar > :deep(.segmented-control) { margin-left: auto; padding: 3px; border-radius: 999px; background: var(--search-dialog-secondary-background); box-shadow: none; }
/* 视图分段滑块通过局部主题变量控制深浅底色，提高选择器权重避免被通用分段控件深色规则覆盖。 */
.ed-workspace .ed-toolbar > :deep(.segmented-control-subtle .segmented-control-indicator) { border-radius: 999px; background: var(--ed-seg-indicator); box-shadow: var(--ed-seg-shadow); }
.ed-workspace .ed-toolbar > :deep(.segmented-control-subtle .segmented-control-option) { min-height: 30px; border-radius: 999px; padding: 4px 12px; }
.ed-workspace .ed-toolbar > :deep(.segmented-control-subtle .segmented-control-option[aria-pressed="true"]) { color: var(--search-dialog-text); }
.ed-toolbar > :deep(.select-menu .select-menu-control), .ed-filterbar :deep(.select-menu .select-menu-control) { min-height: 36px; padding: 6px 12px; border: 0; border-radius: 999px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; font-weight: 500; line-height: 18px; box-shadow: none; transform: none; }
.ed-toolbar > :deep(.select-menu .select-menu-control:hover:not(:disabled)), .ed-filterbar :deep(.select-menu .select-menu-control:hover:not(:disabled)) { background: var(--search-dialog-secondary-hover); box-shadow: none; transform: none; }
.ed-toolbar > :deep(.select-menu .select-menu-control:active:not(:disabled)), .ed-filterbar :deep(.select-menu .select-menu-control:active:not(:disabled)) { background: var(--search-dialog-secondary-pressed); transform: none; }
.ed-filterbar :deep(.select-menu.is-active .select-menu-control) { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }
.ed-filterbar :deep(.select-menu.is-active .select-menu-control:hover:not(:disabled)) { background: var(--search-dialog-action-hover); }
.ed-workspace :deep(.select-menu-panel) { border-color: var(--search-dialog-card-border); border-radius: 14px; }
.ed-workspace :deep(.select-menu-option) { min-height: 34px; padding: 6px 10px; border-radius: 10px; font-size: 13px; line-height: 1.4; }
.ed-tool-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 6px 13px; border: 0; border-radius: 999px; background: var(--search-dialog-secondary-background); color: var(--search-dialog-text); font-size: 12px; font-weight: 500; line-height: 18px; transition: background-color .15s ease, color .15s ease; }
.ed-tool-button.is-active { color: var(--search-dialog-primary); background: var(--search-dialog-action-background); }
.ed-filter-count { display: inline-flex; align-items: center; justify-content: center; min-width: 18px; height: 18px; padding: 0 5px; border-radius: 999px; background: color-mix(in srgb, var(--search-dialog-primary) 16%, transparent); font-size: 11px; font-weight: 600; font-variant-numeric: tabular-nums; line-height: 1; }
/* 目录选择器一步直达：触发器直接承载 SelectMenu，去掉中间折叠条与多余硬框。 */
.ed-target-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; margin-left: auto; min-width: 0; }
.ed-target-menu { max-width: 220px; }
.ed-target-controls :deep(.ed-target-menu .select-menu-control) { min-height: 32px; padding: 4px 10px; gap: 5px; border: 0; border-radius: 999px; background: transparent; color: var(--search-dialog-text); font-size: 12px; font-weight: 500; line-height: 18px; box-shadow: none; transform: none; }
.ed-target-controls :deep(.ed-target-menu .select-menu-control:hover:not(:disabled)) { background: var(--search-dialog-secondary-hover); box-shadow: none; transform: none; }
.ed-target-controls :deep(.ed-target-menu .select-menu-control[aria-expanded="true"]) { background: var(--search-dialog-action-background); color: var(--search-dialog-primary); }
.ed-target-controls :deep(.ed-target-menu.needs-attention .select-menu-control) { background: rgb(217 119 6 / .12); color: #b45309; }
.dark .ed-workspace .ed-target-controls :deep(.ed-target-menu.needs-attention .select-menu-control) { color: #fcd34d; }
.ed-target-trigger, .ed-target-static, .ed-target-action { display: inline-flex; align-items: center; gap: 5px; min-width: 0; }
.ed-target-static, .ed-target-action { max-width: 220px; min-height: 32px; padding: 4px 8px; border-radius: 999px; font-size: 12px; line-height: 18px; color: var(--search-dialog-text); }
.ed-target-action { color: var(--search-dialog-primary); text-decoration: none; white-space: nowrap; transition: background-color .15s ease; }
.ed-target-action.is-error { color: #b91c1c; }
.dark .ed-workspace .ed-target-action.is-error { color: #fca5a5; }
.ed-target-prefix { flex: none; color: var(--search-dialog-muted); font-size: 11px; font-weight: 400; }
.ed-target-value { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.ed-target-trigger svg, .ed-target-static svg, .ed-target-action svg { flex: none; color: var(--search-dialog-primary); }
.ed-target-action.is-error svg { color: inherit; }
.ed-target-hint { margin: 2px 0 6px; font-size: 12px; line-height: 1.5; }
.ed-target-hint.is-warning { color: #b45309; }
.dark .ed-workspace .ed-target-hint.is-warning { color: #fcd34d; }
.ed-target-hint.is-error { color: #b91c1c; }
.dark .ed-workspace .ed-target-hint.is-error { color: #fca5a5; }
.ed-filterbar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; padding: 4px 0 6px; }
.ed-filterbar :deep(.select-menu) { max-width: 180px; }
.ed-reset { min-height: 34px; padding-inline: 10px; border-radius: 999px; font-size: 12px; font-weight: 600; color: var(--search-dialog-primary); }
.ed-notice a { color: var(--search-dialog-primary); }
.ed-files { min-width: 0; border: 1px solid var(--search-dialog-card-border); border-radius: 12px; background: var(--search-dialog-card); overflow: clip; }
.ed-group { min-width: 0; margin-top: 8px; }
.ed-group-card { min-width: 0; border: 1px solid var(--search-dialog-card-border); border-radius: 12px; background: var(--search-dialog-card); overflow: clip; }
.ed-group-header { display: flex; align-items: center; gap: 4px; min-height: 44px; padding: 4px 12px 4px 6px; }
.ed-group-card.is-expanded .ed-group-header { border-bottom: 1px solid var(--search-dialog-card-border); }
.ed-group-check { display: grid; flex: none; place-items: center; width: 34px; height: 36px; cursor: pointer; }
.ed-group-check input { width: 15px; height: 15px; accent-color: var(--search-dialog-primary); cursor: inherit; }
.ed-group-toggle { display: flex; flex: 1; min-width: 0; align-items: center; gap: 8px; min-height: 36px; padding: 4px 2px; border: 0; border-radius: 8px; background: transparent; color: var(--search-dialog-text); font-size: 12px; text-align: left; cursor: pointer; }
.ed-group-chevron { flex: none; color: var(--search-dialog-muted); }
.ed-group-name { flex: 1; min-width: 0; overflow-wrap: anywhere; font-weight: 500; }
.ed-group-count { flex: none; font-size: 11px; color: var(--search-dialog-muted); font-variant-numeric: tabular-nums; white-space: nowrap; }
.ed-group-files { border: 0; border-radius: 0; background: transparent; }
.ed-notice { display: flex; flex-wrap: wrap; gap: 5px 12px; padding: 10px; margin-bottom: 8px; border-radius: 10px; background: var(--search-dialog-tonal); font-size: 12px; overflow-wrap: anywhere; }
.ed-empty { text-align: center; padding: 24px 12px; color: var(--search-dialog-muted); font-size: 12px; }
.ed-empty button { margin-top: 8px; color: var(--search-dialog-primary); }
button:focus-visible, input:focus-visible, summary:focus-visible, a:focus-visible { outline: 2px solid var(--search-dialog-primary); outline-offset: 2px; }
@media (min-width: 640px) {
  .ed-workspace :deep(.select-menu-option .select-menu-label) { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
}
@media (hover: hover) {
  .ed-tool-button:hover:not(:disabled), .ed-target-action:hover:not(:disabled), .ed-group-toggle:hover { background: var(--search-dialog-secondary-hover); }
  .ed-tool-button.is-active:hover:not(:disabled) { background: var(--search-dialog-action-hover); }
  .ed-reset:hover:not(:disabled) { background: var(--search-dialog-action-background); }
}
@media (min-width: 640px) and (max-width: 760px) {
  .ed-filterbar :deep(.select-menu) { max-width: calc(50% - 3px); flex: 1 1 125px; }
  .ed-selection label { min-height: 36px; }
}
@media (max-width: 639px) {
  /* 手机端去胖胶囊：工具栏改轻平铺、规格筛选改单行横滑、双目录合并为细分割轻通条。 */
  .ed-toolbar { gap: 4px; margin-bottom: 2px; }
  .ed-filter-toggle { min-width: 44px; min-height: 44px; padding-inline: 10px; background: transparent; font-size: 13px; }
  .ed-filter-toggle.is-active { background: var(--search-dialog-action-background); }
  .ed-filter-toggle > span:first-of-type { display: none; }
  .ed-toolbar > :deep(.select-menu) { flex: 0 1 auto; min-width: 0; max-width: 144px; }
  .ed-toolbar > :deep(.select-menu .select-menu-control) { min-height: 44px; padding: 8px 8px; gap: 4px; background: transparent; font-size: 13px; }
  /* 收紧分段器的可见底槽和滑块，同时保留完整的按钮触控区域。 */
  .ed-workspace .ed-toolbar > :deep(.segmented-control) { margin-left: auto; padding: 2px; background: transparent; }
  .ed-workspace .ed-toolbar > :deep(.segmented-control::before) { content: ''; position: absolute; inset: 6px 0; z-index: -1; border-radius: inherit; background: var(--search-dialog-secondary-background); pointer-events: none; }
  .ed-workspace .ed-toolbar > :deep(.segmented-control-subtle .segmented-control-indicator) { background: transparent; box-shadow: none; }
  .ed-workspace .ed-toolbar > :deep(.segmented-control-subtle .segmented-control-indicator::before) { content: ''; position: absolute; inset: 6px 0; border-radius: inherit; background: var(--ed-seg-indicator); box-shadow: var(--ed-seg-shadow); }
  .ed-workspace .ed-toolbar > :deep(.segmented-control-subtle.segmented-control-compact .segmented-control-option) { min-height: 44px; padding: 4px 12px; font-size: 13px; }
  .ed-workspace :deep(.select-menu-option) { min-height: 44px; font-size: 13px; }
  .ed-filterbar {
    flex-wrap: nowrap; overflow-x: auto; overflow-y: hidden; overscroll-behavior-x: contain; gap: 6px; padding: 2px 0 6px; scrollbar-width: none; -webkit-overflow-scrolling: touch;
    -webkit-mask-image: linear-gradient(to right, var(--scroll-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--scroll-fade-right, #000));
    mask-image: linear-gradient(to right, var(--scroll-fade-left, #000), #000 26px, #000 calc(100% - 26px), var(--scroll-fade-right, #000));
  }
  .ed-filterbar::-webkit-scrollbar { display: none; }
  .ed-filterbar.has-scroll-left { --scroll-fade-left: transparent; }
  .ed-filterbar.has-scroll-right { --scroll-fade-right: transparent; }
  .ed-filterbar :deep(.select-menu) { flex: none; max-width: none; }
  .ed-workspace .ed-filterbar :deep(.select-menu .select-menu-control) { position: relative; isolation: isolate; min-height: 44px; padding: 5px 10px; gap: 4px; background: transparent; font-size: 12px; }
  /* 手机端展开态与已选态统一落在内缩 32px 轻胶囊上，避免外层 44px outline 套住内缩底色产生双层空隙。 */
  .ed-workspace .ed-filterbar :deep(.select-menu.is-active .select-menu-control::before),
  .ed-workspace .ed-filterbar :deep(.select-menu .select-menu-control[aria-expanded="true"]::before) { content: ''; position: absolute; inset: 6px 0; z-index: -1; border-radius: inherit; background: var(--search-dialog-action-background); pointer-events: none; }
  .ed-workspace .ed-filterbar :deep(.select-menu .select-menu-control[aria-expanded="true"]) { color: var(--search-dialog-primary); }
  .ed-workspace .ed-filterbar :deep(.select-menu .select-menu-control[aria-expanded="true"]:not(:focus-visible)) { outline: none; }
  .ed-workspace .ed-filterbar :deep(.select-menu .select-menu-control[aria-expanded="true"]::before) { background: var(--search-dialog-action-hover); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--search-dialog-primary) 42%, transparent); }
  .ed-filterbar :deep(.select-menu-control:focus-visible), .ed-filterbar .ed-reset:focus-visible, .ed-target-controls :deep(.select-menu-control:is(:focus-visible, [aria-expanded="true"])), .ed-target-action:focus-visible { outline-offset: -2px; }
  .ed-reset { flex: none; min-height: 44px; padding-inline: 10px; }
  .ed-selection { gap: 4px 8px; }
  .ed-selection label { min-height: 36px; }
  .ed-target-controls { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); width: 100%; margin-left: 0; gap: 0; border: 1px solid var(--search-dialog-card-border); border-radius: 12px; background: var(--search-dialog-card); overflow: clip; }
  .ed-target-controls > :nth-child(2) { border-left: 1px solid var(--search-dialog-card-border); }
  .ed-target-menu, .ed-target-static, .ed-target-action { width: 100%; max-width: none; }
  .ed-target-controls :deep(.ed-target-menu .select-menu-control), .ed-target-static, .ed-target-action { min-height: 44px; padding: 6px 10px; border-radius: 0; background: transparent; font-size: 12px; }
  .ed-group-header { padding: 2px 10px 2px 2px; }
  .ed-group-check { width: 44px; height: 44px; }
  .ed-group-toggle { min-height: 44px; }
}
@media (max-width: 359px) {
  .ed-target-controls :deep(.ed-target-menu .select-menu-control), .ed-target-static, .ed-target-action { padding-inline: 7px; font-size: 11px; }
}
@media (forced-colors: active) { .ed-filterbar { -webkit-mask-image: none; mask-image: none; } }
@media (prefers-reduced-motion: reduce) { .ed-tool-button, .ed-target-action { transition: none; } }
</style>
