<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { IconArrowLeft, IconArrowRight, IconCheck, IconPlus, IconTrash, IconX, IconLock } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import SortableList from './SortableList.vue'
import { cloneNamingTemplate } from '../utils/namingTemplate'
import type { NamingBlock, NamingField, NamingTemplate } from '../types/naming'

const model = defineModel<NamingTemplate>({ required: true })
const props = defineProps<{ fields: NamingField[]; defaults: NamingTemplate; disabled?: boolean }>()
const root = ref<HTMLElement>(), active = ref(-1), selected = ref(''), announcement = ref('')
const id = `naming-${useId()}`
const groups = [{ id: 'work', label: '作品信息' }, { id: 'episode', label: '季集信息' }, { id: 'video', label: '视频规格' }, { id: 'audio', label: '音频规格' }, { id: 'release', label: '字幕与发布' }]
const layers = computed(() => [...model.value.directories.map((blocks, index) => ({ index, label: index === 0 ? '作品目录' : model.value.kind === 'tv' && index === 1 ? '季目录' : `目录层 ${index + 1}`, blocks })), { index: -1, label: '文件名', blocks: model.value.filename }])
const activeLayer = computed(() => layers.value.find(layer => layer.index === active.value) || layers.value[layers.value.length - 1]!)
const selectedBlock = computed(() => activeLayer.value.blocks.find(block => block.field === selected.value))
const selectedField = computed(() => props.fields.find(field => field.id === selected.value))
const selectedIndex = computed(() => activeLayer.value.blocks.findIndex(block => block.field === selected.value))
const label = (field: string) => props.fields.find(item => item.id === field)?.label || field
const library = computed(() => groups.map(group => ({ ...group, blocks: props.fields.filter(field => field.group === group.id).map(field => defaultBlock(field.id)) })).filter(group => group.blocks.length))
const unavailable = (block: NamingBlock) => activeLayer.value.blocks.some(item => item.field === block.field) || activeLayer.value.blocks.length >= 32

function defaultBlock(field: string): NamingBlock {
  const format = [...props.defaults.directories.flat(), ...props.defaults.filename].find(block => block.field === field)
  return format ? { ...format } : { field, separator: '.' }
}

function editLayer(index: number, change: (blocks: NamingBlock[]) => void) {
  if (props.disabled) return
  const next = cloneNamingTemplate(model.value)
  const blocks = index === -1 ? next.filename : next.directories[index]
  if (!blocks) return
  change(blocks); model.value = next
}
function activate(index: number) { active.value = index; selected.value = '' }
function choose(index: number, field: string) { const same = active.value === index && selected.value === field; active.value = index; selected.value = same ? '' : field }
function add(field: string, layer = active.value) {
  editLayer(layer, blocks => {
    if (blocks.some(block => block.field === field) || blocks.length >= 32) return
    blocks.push(defaultBlock(field))
    announcement.value = `已将${label(field)}添加到${layers.value.find(item => item.index === layer)?.label}`
  })
}
async function focusField(field: string) {
  await nextTick()
  root.value?.querySelector<HTMLButtonElement>(`[data-layer="${active.value}"][data-field="${CSS.escape(field)}"]`)?.focus({ preventScroll: true })
}
function patch(change: Partial<NamingBlock>) {
  editLayer(active.value, blocks => { const block = blocks.find(block => block.field === selected.value); if (block) Object.assign(block, change) })
}
function formatInput(event: Event, field: 'separator' | 'prefix' | 'suffix') {
  if (event instanceof InputEvent && event.isComposing) return
  patch({ [field]: (event.target as HTMLInputElement).value })
}
function move(step: number, field = selected.value, restoreFocus = false) {
  editLayer(active.value, blocks => {
    const index = blocks.findIndex(block => block.field === field), target = index + step
    if (index < 0 || target < 0 || target >= blocks.length) return
    const [block] = blocks.splice(index, 1); blocks.splice(target, 0, block!)
    announcement.value = `${label(field)}已${step < 0 ? '前移' : '后移'}`
  })
  if (restoreFocus) void focusField(field)
}
function remove(field = selected.value) {
  const previous = activeLayer.value.blocks.findIndex(block => block.field === field)
  editLayer(active.value, blocks => { const index = blocks.findIndex(block => block.field === field); if (index >= 0) blocks.splice(index, 1) })
  selected.value = ''
  announcement.value = `已移除${label(field)}`
  const next = activeLayer.value.blocks[Math.min(previous, activeLayer.value.blocks.length - 1)]
  if (next) void focusField(next.field)
  else void nextTick(() => root.value?.querySelector<HTMLButtonElement>(`[data-layer-target="${active.value}"]`)?.focus({ preventScroll: true }))
}
function addDirectory() {
  if (props.disabled || model.value.directories.length >= 4) return
  const next = cloneNamingTemplate(model.value); next.directories.push([]); model.value = next
  activate(next.directories.length - 1)
}
function removeDirectory(index: number) {
  if (props.disabled) return
  const next = cloneNamingTemplate(model.value); next.directories.splice(index, 1); model.value = next
  activate(-1)
}
function setLayer(index: number, blocks: NamingBlock[]) {
  if (props.disabled || blocks.length > 32 || new Set(blocks.map(block => block.field)).size !== blocks.length) return
  const previous = layers.value.find(layer => layer.index === index)?.blocks || []
  const added = blocks.find(block => !previous.some(item => item.field === block.field))
  editLayer(index, target => target.splice(0, target.length, ...blocks))
  if (active.value !== index) selected.value = ''
  active.value = index
  if (added) { selected.value = added.field; announcement.value = `已添加${label(added.field)}`; void focusField(added.field) }
}
function sorted(index: number, block: NamingBlock) {
  active.value = index; selected.value = block.field
  announcement.value = `已调整${label(block.field)}的位置`
  void focusField(block.field)
}
function chipKey(event: KeyboardEvent, layer: number, field: string) {
  if (props.disabled) return
  if (event.key === 'Delete' || event.key === 'Backspace') { event.preventDefault(); active.value = layer; remove(field) }
  if (event.altKey && ['ArrowLeft', 'ArrowRight'].includes(event.key)) { event.preventDefault(); active.value = layer; move(event.key === 'ArrowLeft' ? -1 : 1, field, true) }
}
</script>

<template>
  <div ref="root" class="nt-editor" :aria-disabled="disabled || undefined">
    <div class="nt-canvas">
      <section v-for="layer in layers" :key="layer.index" class="nt-layer" :aria-label="layer.label">
        <header class="nt-layer-heading"><button type="button" :data-layer-target="layer.index" :disabled="disabled" :aria-pressed="active === layer.index" @click="activate(layer.index)">{{ layer.label }}<span v-if="active === layer.index">正在编辑</span></button><button v-if="layer.index >= 0" type="button" class="nt-icon-button" :disabled="disabled" :aria-label="`删除${layer.label}`" @click="removeDirectory(layer.index)"><IconTrash :size="15" aria-hidden="true" /></button></header>
        <div class="nt-chips-row">
          <SortableList class="nt-chips" item-class="!rounded-full" :model-value="layer.blocks" :item-key="block => block.field" :group="id" put :disabled="disabled" direction="horizontal" :accepts="block => layer.blocks.length < 32 && !layer.blocks.some(item => item.field === block.field)" :aria-label="`${layer.label}字段排序`" @update:model-value="setLayer(layer.index, $event)" @end="(block, changed) => changed && sorted(layer.index, block)">
            <template #item="{ item: block }">
              <button type="button" class="nt-chip" data-drag-handle :class="{ selected: active === layer.index && selected === block.field }" :data-layer="layer.index" :data-field="block.field" :disabled="disabled" :aria-label="`编辑${layer.label}的${label(block.field)}`" :aria-expanded="active === layer.index && selected === block.field" :aria-controls="active === layer.index && selected === block.field ? `${id}-format` : undefined" @click="choose(layer.index, block.field)" @keydown="chipKey($event, layer.index, block.field)">
                <span class="nt-chip-label">{{ label(block.field) }}</span>
              </button>
            </template>
            <template #empty><button type="button" class="nt-empty" :disabled="disabled" @click="activate(layer.index)">选择此层后，从字段库添加字段</button></template>
          </SortableList>
          <span v-if="layer.index === -1" class="nt-extension"><IconLock :size="13" aria-hidden="true" />原扩展名</span>
        </div>
        <div v-if="active === layer.index && selectedBlock" :id="`${id}-format`" class="nt-format" role="region" :aria-label="`${selectedField?.label}字段格式`">
          <header><strong>{{ selectedField?.label }}格式</strong><button type="button" class="nt-icon-button" aria-label="收起字段格式" @click="selected = ''"><IconX :size="15" aria-hidden="true" /></button></header>
          <div class="nt-format-grid">
            <label>前置分隔符<input :value="selectedBlock.separator || ''" :disabled="disabled" maxlength="64" placeholder="无" @input="formatInput($event, 'separator')" @compositionend="formatInput($event, 'separator')" /></label>
            <label>前缀<input :value="selectedBlock.prefix || ''" :disabled="disabled" maxlength="64" placeholder="无" @input="formatInput($event, 'prefix')" @compositionend="formatInput($event, 'prefix')" /></label>
            <label>后缀<input :value="selectedBlock.suffix || ''" :disabled="disabled" maxlength="64" placeholder="无" @input="formatInput($event, 'suffix')" @compositionend="formatInput($event, 'suffix')" /></label>
            <div v-if="selectedField?.numeric" class="nt-format-number"><span>数字补零</span><SelectMenu :model-value="String(selectedBlock.pad || 0)" :options="[0, 1, 2, 3, 4, 5, 6].map(value => ({ value: String(value), label: value ? `${value} 位` : '不补零' }))" label="数字补零" :disabled="disabled" @change="patch({ pad: Number($event) })" /></div>
          </div>
          <footer><div class="nt-format-actions"><button type="button" :disabled="disabled || selectedIndex <= 0" @click="move(-1)"><IconArrowLeft :size="15" aria-hidden="true" />前移</button><button type="button" :disabled="disabled || selectedIndex >= activeLayer.blocks.length - 1" @click="move(1)">后移<IconArrowRight :size="15" aria-hidden="true" /></button></div><button type="button" :disabled="disabled" @click="remove()"><IconTrash :size="15" aria-hidden="true" />移除字段</button></footer>
        </div>
      </section>
      <button type="button" class="nt-add-layer" :disabled="disabled || model.directories.length >= 4" @click="addDirectory"><IconPlus :size="16" aria-hidden="true" />添加目录层<span>{{ model.directories.length }}/4</span></button>
      <p class="nt-hint">扩展名跟随原文件。英文名、原语种名缺失时使用作品名；其他空字段连同前后缀、分隔符省略。文件名可不含作品名，剧集须保留季集编号。</p>
    </div>
    <aside class="nt-library" aria-label="命名字段库">
      <header><h2>字段库</h2><p>添加到<span>{{ activeLayer.label }}</span></p></header>
      <section v-for="group in library" :key="group.id"><h3>{{ group.label }}</h3><SortableList class="nt-library-fields" item-class="!rounded-full" :model-value="group.blocks" :item-key="block => block.field" :group="id" pull="clone" :sort="false" :disabled="disabled" :item-disabled="unavailable" :clone="block => ({ ...block })" :aria-label="`${group.label}字段库`"><template #item="{ item: block }"><button type="button" data-drag-handle :disabled="disabled || unavailable(block)" :aria-label="`添加${label(block.field)}到${activeLayer.label}`" @click="add(block.field)"><IconCheck v-if="activeLayer.blocks.some(item => item.field === block.field)" :size="13" aria-hidden="true" /><IconPlus v-else :size="13" aria-hidden="true" />{{ label(block.field) }}</button></template></SortableList></section>
      <p class="nt-hint">字段可点击添加，也可拖入。点击已添加字段编辑格式，按住拖动调整顺序；手机长按后拖动。</p>
    </aside>
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
  </div>
</template>

<style scoped>
.nt-editor { display: grid; grid-template-columns: minmax(0, 1fr) 280px; align-items: start; gap: 22px; color: rgb(var(--color-slate-700)); }
.nt-canvas, .nt-library { min-width: 0; }
.nt-layer { margin-bottom: 12px; padding: 14px; border-radius: 13px; background: #fff; border: 1px solid transparent; box-shadow: 0 2px 12px rgb(15 23 42 / .035); }
.nt-layer-heading { display: flex; justify-content: space-between; align-items: center; gap: 8px; margin-bottom: 9px; }
.nt-layer-heading > button:first-child { display: flex; align-items: center; gap: 10px; min-height: 32px; text-align: left; font-size: 13px; font-weight: 600; }
.nt-layer-heading > button > span { font-size: 11px; font-weight: 400; color: #2563eb; }
.nt-icon-button { display: grid; width: 30px; min-height: 30px; place-items: center; color: rgb(var(--color-slate-400)); border-radius: 7px; }
.nt-icon-button:not(:disabled):hover { color: rgb(var(--color-slate-700)); background: rgb(var(--color-slate-100)); }
.nt-chips-row { min-width: 0; }.nt-chips { gap: 6px; padding: 2px 0; }
.nt-chip, .nt-extension { display: inline-flex; align-items: center; gap: 5px; min-height: 33px; padding: 6px 9px; border-radius: 8px; font-size: 12px; background: rgb(var(--color-slate-100)); max-width: 100%; }
.nt-chip { justify-content: center; min-height: 34px; padding: 6px 12px; border: 1px solid transparent; border-radius: 999px; text-align: left; font-weight: 500; transition: background-color .15s, color .15s, border-color .15s; }
.nt-chip.selected { color: #1d4ed8; background: rgb(59 130 246 / .09); border-color: rgb(59 130 246 / .3); }
.nt-chip-label { overflow-wrap: anywhere; }
.nt-extension { color: rgb(var(--color-slate-400)); background: transparent; font-size: 11px; padding: 5px 0 0; min-height: 25px; }
.nt-empty { min-height: 44px; font-size: 12px; color: rgb(var(--color-slate-500)); text-align: left; }
.nt-format { padding-top: 14px; margin-top: 12px; border-top: 1px solid rgb(var(--color-slate-100)); }
.nt-format > header { display: flex; justify-content: space-between; align-items: center; font-size: 12px; margin-bottom: 10px; }.nt-format strong { font-weight: 500; }
.nt-format-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }.nt-format-grid label, .nt-format-number { display: flex; min-width: 0; flex-direction: column; gap: 6px; font-size: 11px; color: rgb(var(--color-slate-500)); }
.nt-format input { width: 100%; min-height: 36px; padding: 6px 9px; border-radius: 8px; color: rgb(var(--color-slate-800)); background: rgb(var(--color-slate-50)); border: 1px solid rgb(var(--color-slate-200)); font-size: 13px; }
.nt-format-number { grid-column: 1 / -1; max-width: 150px; }.nt-format-number :deep(button) { min-height: 36px; height: 36px; font-size: 12px; }
.nt-format footer, .nt-format-actions { display: flex; align-items: center; gap: 8px; }.nt-format footer { justify-content: space-between; margin-top: 12px; }.nt-format footer button { display: inline-flex; min-height: 34px; align-items: center; gap: 5px; padding: 5px 7px; border-radius: 7px; color: rgb(var(--color-slate-500)); font-size: 12px; }.nt-format footer button:not(:disabled):hover { background: rgb(var(--color-slate-100)); color: rgb(var(--color-slate-800)); }
.nt-add-layer { display: flex; align-items: center; gap: 7px; min-height: 40px; padding: 6px 4px; font-size: 12px; color: rgb(var(--color-slate-500)); }.nt-add-layer > span { margin-left: 4px; font-size: 11px; color: rgb(var(--color-slate-400)); }
.nt-hint { font-size: 11px; line-height: 1.8; color: rgb(var(--color-slate-500)); margin-top: 8px; }
.nt-library { padding: 15px 17px; border-radius: 13px; background: #fff; box-shadow: 0 2px 12px rgb(15 23 42 / .035); }.nt-library > header { margin-bottom: 15px; }.nt-library h2 { font-size: 13px; font-weight: 600; }.nt-library > header p { display: flex; gap: 7px; margin-top: 5px; font-size: 11px; color: rgb(var(--color-slate-500)); }.nt-library > header p span { color: #2563eb; }.nt-library section { margin-top: 13px; }.nt-library h3 { margin-bottom: 7px; color: rgb(var(--color-slate-500)); font-size: 11px; }.nt-library-fields { gap: 6px; min-height: 30px; }.nt-library-fields button { display: inline-flex; align-items: center; gap: 4px; min-height: 30px; padding: 5px 9px; border-radius: 999px; background: rgb(var(--color-slate-100)); font-size: 11px; }.nt-library-fields button:disabled { opacity: .45; }
.nt-editor button:disabled { cursor: default; }.nt-canvas button:disabled { opacity: .45; }.nt-editor :focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.dark .nt-editor { color: rgb(var(--color-slate-200)); }.dark .nt-layer, .dark .nt-library { background: rgb(var(--color-slate-900)); }.dark .nt-chip { background: rgb(var(--color-slate-800)); }.dark .nt-chip.selected { color: #93c5fd; background: rgb(59 130 246 / .13); border-color: rgb(96 165 250 / .4); }.dark .nt-layer-heading > button > span, .dark .nt-library > header p span { color: #93c5fd; }.dark .nt-format { border-color: rgb(var(--color-slate-800)); }.dark .nt-format input { color: rgb(var(--color-slate-100)); background: rgb(var(--color-slate-950)); border-color: rgb(var(--color-slate-700)); }.dark .nt-library-fields button, .dark .nt-icon-button:not(:disabled):hover, .dark .nt-format footer button:not(:disabled):hover { background: rgb(var(--color-slate-800)); }.dark .nt-format footer button:not(:disabled):hover, .dark .nt-icon-button:not(:disabled):hover { color: rgb(var(--color-slate-100)); }
.dark .nt-hint, .dark .nt-empty, .dark .nt-add-layer, .dark .nt-format-grid label, .dark .nt-format-number, .dark .nt-library h3, .dark .nt-library > header p, .dark .nt-format footer button { color: rgb(var(--color-slate-400)); }
@media(max-width: 1000px) { .nt-editor { grid-template-columns: minmax(0, 1fr); gap: 14px; }.nt-library { padding: 14px; }.nt-library section { display: grid; grid-template-columns: 85px minmax(0, 1fr); align-items: start; gap: 8px; }.nt-library h3 { margin-top: 6px; } }
@media(hover: hover) and (pointer: fine) { .nt-chip:not(:disabled):not(.selected):hover { background: rgb(var(--color-slate-200) / .7); color: rgb(var(--color-slate-800)); }.nt-library-fields button:not(:disabled):hover { background: rgb(var(--color-slate-200) / .7); }.dark .nt-chip:not(:disabled):not(.selected):hover { background: rgb(var(--color-slate-700)); color: rgb(var(--color-slate-100)); }.dark .nt-library-fields button:not(:disabled):hover { background: rgb(var(--color-slate-700)); } }
@media(prefers-reduced-motion: reduce) { .nt-chip { transition: none; } }
@media(max-width: 639px) { .nt-layer { padding: 12px; }.nt-chip { min-height: 42px; }.nt-layer-heading > button:first-child, .nt-icon-button { min-height: 40px; }.nt-icon-button { width: 36px; }.nt-format-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }.nt-format-grid > label:first-child { grid-column: 1 / -1; }.nt-format input { min-height: 42px; font-size: 16px; }.nt-format footer button { min-height: 42px; }.nt-library section { display: block; }.nt-library-fields button { min-height: 42px; padding-inline: 10px; }.nt-layer-heading > button > span { font-size: 10px; } }
</style>
