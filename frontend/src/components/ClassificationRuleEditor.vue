<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconArrowDown, IconArrowUp, IconDeviceFloppy, IconGripVertical, IconPlus, IconTrash, IconX } from '@tabler/icons-vue'
import SortableList from './SortableList.vue'
import SelectMenu from './SelectMenu.vue'
import P115ActionDialog from './P115ActionDialog.vue'
import ResponsiveEditorPanel from './ResponsiveEditorPanel.vue'
import { cachedGenres } from '../utils/dashboardPreferences'
import { classificationConditions as conditions, classificationDirectory } from '../utils/classificationDocument'
import type { ClassificationCondition, ClassificationKind, ClassificationRule, ClassificationSection } from '../types/classification'

const model = defineModel<ClassificationSection>({ required: true })
const props = defineProps<{ kind: ClassificationKind; disabled?: boolean; invalidKey?: string; canSave?: boolean; saving?: boolean; pending?: boolean; error?: string }>()
const emit = defineEmits<{ save: [] }>()
const id = `classification-${useId()}`, root = ref<HTMLElement>(), detail = ref<HTMLElement>(), active = ref(''), deleting = ref<ClassificationRule>(), announcement = ref('')
let newRule = 0
const custom = ref<Partial<Record<ClassificationCondition, string>>>({}), customError = ref('')
const countries = [['CN', '中国大陆'], ['HK', '中国香港'], ['TW', '中国台湾'], ['JP', '日本'], ['KR', '韩国'], ['US', '美国'], ['GB', '英国'], ['CA', '加拿大'], ['FR', '法国'], ['DE', '德国'], ['AU', '澳大利亚'], ['TH', '泰国'], ['IN', '印度'], ['IT', '意大利'], ['ES', '西班牙']]
const languages = [['zh', '汉语'], ['cn', '粤语'], ['ja', '日语'], ['ko', '韩语'], ['en', '英语'], ['fr', '法语'], ['de', '德语'], ['es', '西班牙语'], ['it', '意大利语'], ['th', '泰语'], ['hi', '印地语']]
const genres = cachedGenres(props.kind).genres
const rows = computed({ get: () => model.value.rules, set: rules => { if (!props.disabled) { model.value = { ...model.value, rules }; announcement.value = '规则顺序已调整' } } })
const selected = computed(() => rows.value.find(rule => rule.key === active.value))
const viewport = window.matchMedia('(min-width: 1280px)'), desktop = ref(viewport.matches), opened = ref(false)
if (desktop.value) active.value = rows.value[0]?.key || ''
async function resize() {
  const editing = detail.value?.contains(document.activeElement)
  desktop.value = viewport.matches; opened.value = false
  if (desktop.value && !selected.value) active.value = rows.value[0]?.key || ''
  if (editing) { await nextTick(); if (desktop.value) detail.value?.querySelector<HTMLInputElement>('input')?.focus({ preventScroll: true }); else focusRule() }
}
onMounted(() => viewport.addEventListener('change', resize))
onBeforeUnmount(() => viewport.removeEventListener('change', resize))
const ruleOptions = computed(() => rows.value.map((rule, index) => ({ value: rule.key, label: `${index + 1}. ${rule.name || '新规则'}` })))
const options = (field: ClassificationCondition) => field === 'genres' ? genres.map(item => ({ value: String(item.id), label: item.name })) : (field === 'languages' ? languages : countries).map(([value, label]) => ({ value: value!, label: `${label} ${value}` }))
const valueLabel = (field: ClassificationCondition, value: string | number) => options(field).find(item => item.value === String(value))?.label || String(value)
const summary = (rule: ClassificationRule) => conditions.filter(({ key }) => rule.match[key]?.length).map(({ key, label }) => `${label}：${rule.match[key]!.map(value => valueLabel(key, value)).join('、')}`).join('；') || '待设置匹配条件'
const conditionOptions = (rule: ClassificationRule, current?: ClassificationCondition) => conditions.filter(({ key }) => key === current || !rule.match[key]).map(({ key, label }) => ({ value: key, label }))
const availableConditions = (rule: ClassificationRule) => [{ value: '', label: '添加条件' }, ...conditionOptions(rule)]
const availableValues = (rule: ClassificationRule, field: ClassificationCondition) => [{ value: '', label: '选择匹配值' }, ...options(field).filter(item => !rule.match[field]?.map(String).includes(item.value))]

function update(rule: ClassificationRule, changes: Partial<ClassificationRule>) {
  if (!props.disabled) model.value = { ...model.value, rules: rows.value.map(item => item.key === rule.key ? { ...item, ...changes } : item) }
}
function values(rule: ClassificationRule, field: ClassificationCondition, next?: (string | number)[]) {
  const match = { ...rule.match }
  if (next) match[field] = next
  else delete match[field]
  update(rule, { match })
}
function changeCondition(rule: ClassificationRule, previous: ClassificationCondition, next: string) {
  const field = next as ClassificationCondition
  if (props.disabled || field === previous || rule.match[field] || !conditions.some(item => item.key === field)) return
  const match = { ...rule.match }
  delete match[previous]; match[field] = []
  custom.value = {}; customError.value = ''
  update(rule, { match })
  void nextTick(() => detail.value?.querySelector<HTMLButtonElement>(`[data-condition="${field}"] .select-menu-trigger`)?.focus({ preventScroll: true }))
}
function addValue(rule: ClassificationRule, field: ClassificationCondition, raw: string) {
  if (!raw.trim() || props.disabled) return
  let value: string | number = raw.trim()
  if (field === 'genres') {
    if (!/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value))) { customError.value = '类型 ID 请输入正整数'; return }
    value = Number(value)
  } else {
    if (!/^[a-z]{2}$/i.test(value)) { customError.value = '国家或语言请输入两位字母代码'; return }
    value = field === 'languages' ? value.toLowerCase() : value.toUpperCase()
  }
  const previous = rule.match[field] || []
  if (!previous.includes(value)) values(rule, field, [...previous, value])
  custom.value[field] = ''; customError.value = ''
}
async function expand(key: string) {
  if (props.disabled) return
  active.value = key; opened.value = true; custom.value = {}; customError.value = ''
  await nextTick()
  if (detail.value?.parentElement) detail.value.parentElement.scrollTop = 0
  detail.value?.querySelector<HTMLInputElement>('input')?.focus({ preventScroll: true })
}
function move(rule: ClassificationRule, delta: number) {
  const index = rows.value.findIndex(item => item.key === rule.key), target = index + delta
  if (props.disabled || target < 0 || target >= rows.value.length) return
  const rules = [...rows.value]; rules.splice(index, 1); rules.splice(target, 0, rule); rows.value = rules
  announcement.value = `${rule.name || '新规则'}已移至第 ${target + 1} 位`
  void nextTick(() => root.value?.querySelector<HTMLButtonElement>(`[data-rule-key="${rule.key}"] .cr-toggle`)?.focus({ preventScroll: true }))
}
async function add() {
  if (props.disabled) return
  const key = `${id}-new-${++newRule}`
  rows.value = [...rows.value, { key, name: '', match: {} }]
  await expand(key)
}
function remove() {
  if (deleting.value && !props.disabled) {
    const index = rows.value.findIndex(item => item.key === deleting.value!.key)
    rows.value = rows.value.filter(item => item.key !== deleting.value!.key)
    if (active.value === deleting.value.key) { active.value = desktop.value ? (rows.value[index] || rows.value[index - 1])?.key || '' : ''; opened.value = false; custom.value = {}; customError.value = '' }
    announcement.value = '规则已删除，保存后生效'
    void nextTick(focusRule)
  }
  deleting.value = undefined
}
function focusRule() { (root.value?.querySelector<HTMLButtonElement>(`[data-rule-key="${active.value}"] .cr-toggle`) || root.value?.querySelector<HTMLButtonElement>('.cr-heading button'))?.focus({ preventScroll: true }) }
defineExpose({ reveal: async (key?: string) => {
  if (key) await expand(key)
  else opened.value = false
  await nextTick()
  const input = key ? detail.value?.querySelector<HTMLInputElement>('input') : root.value?.querySelector<HTMLInputElement>('.cr-fallback input')
  input?.focus(); input?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
} })
</script>

<template>
  <section ref="root" class="classification-editor" :class="{ 'cr-desktop': desktop }" aria-label="分类规则编辑">
    <div class="cr-overview">
    <header class="cr-heading"><div><strong>匹配顺序<span>{{ rows.length }} 条规则</span></strong><p>从上到下匹配，首条命中后停止</p></div><button class="cr-button" type="button" :disabled="disabled" @click="add"><IconPlus :size="16" aria-hidden="true" />添加规则</button></header>
    <SortableList v-model="rows" :item-key="item => item.key" direction="vertical" class="cr-list" :disabled="disabled" @end="(_, changed) => { if (changed) announcement = '规则顺序已调整' }">
      <template #item="{ item, index }">
        <article class="cr-rule" data-drag-handle :class="{ 'is-open': active === item.key && (desktop || opened), 'is-invalid': invalidKey === item.key }" :data-rule-key="item.key" @click="expand(item.key)">
          <div class="cr-row">
            <span class="cr-grip" aria-hidden="true"><IconGripVertical :size="15" /></span>
            <span class="cr-priority" aria-label="优先级">{{ String(index + 1).padStart(2, '0') }}</span>
            <button type="button" class="cr-toggle" :disabled="disabled" :aria-current="desktop && active === item.key ? 'true' : undefined" :aria-expanded="desktop ? undefined : opened && active === item.key" :aria-haspopup="desktop ? undefined : 'dialog'" @click.stop="expand(item.key)" @keydown.alt.up.prevent="move(item, -1)" @keydown.alt.down.prevent="move(item, 1)">
              <span class="cr-name">{{ item.name || '新规则' }}</span><code class="cr-path" :title="classificationDirectory(kind, item.name)">{{ classificationDirectory(kind, item.name || '待填写分类') }}</code><span class="cr-summary" :title="summary(item)">{{ summary(item) }}</span>
            </button>
            <div class="cr-order" data-drag-disabled="true" @click.stop><button type="button" class="cr-icon" :disabled="disabled || index === 0" :aria-label="`上移${item.name || '新规则'}`" @click="move(item, -1)"><IconArrowUp :size="15" aria-hidden="true" /></button><button type="button" class="cr-icon" :disabled="disabled || index === rows.length - 1" :aria-label="`下移${item.name || '新规则'}`" @click="move(item, 1)"><IconArrowDown :size="15" aria-hidden="true" /></button></div>
          </div>
        </article>
      </template>
      <template #empty><p class="cr-empty">暂无匹配规则，全部使用兜底目录。</p></template>
    </SortableList>
    <div class="cr-fallback"><div><strong>兜底分类</strong><span>所有规则未命中时使用</span></div><label><input :value="model.fallback" :disabled="disabled" :aria-label="`${kind === 'movie' ? '电影' : '剧集'}兜底分类`" :placeholder="kind === 'movie' ? '例如 其他电影' : '例如 其他剧集'" @input="model = { ...model, fallback: ($event.target as HTMLInputElement).value }" /><code>{{ classificationDirectory(kind, model.fallback || '待填写分类') }}</code></label></div>
    </div>
    <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    <ResponsiveEditorPanel v-if="selected && (desktop || opened)" :desktop="desktop" :title="selected.name || '新规则'" :content-key="selected.key" description="" :busy="saving" @close="opened = false">
      <template v-if="!desktop" #actions><SelectMenu :model-value="active" :options="ruleOptions" label="切换编辑规则" :disabled="disabled" @change="expand" /></template>
      <div ref="detail" class="classification-editor cr-detail">
        <div class="cr-fields"><label>分类名称<input :value="selected.name" :disabled="disabled" placeholder="例如 日本动画" @input="update(selected, { name: ($event.target as HTMLInputElement).value })" /></label><div class="cr-generated"><span>生成目录</span><code>{{ classificationDirectory(kind, selected.name || '待填写分类') }}</code></div></div>
        <div class="cr-condition-heading"><div><strong>匹配条件</strong><p>各条件同时满足，同项任选其一</p></div><SelectMenu v-if="availableConditions(selected).length > 1" model-value="" :options="availableConditions(selected)" label="添加匹配条件" variant="anime" :disabled="disabled" @change="value => { if (value) values(selected!, value as ClassificationCondition, []) }" /></div>
        <div v-for="field in conditions.filter(field => !!selected!.match[field.key])" :key="`${selected.key}-${field.key}`" class="cr-condition" :data-condition="field.key">
          <SelectMenu class="cr-condition-field" :model-value="field.key" :options="conditionOptions(selected, field.key)" :label="`${field.label}字段`" variant="anime" :disabled="disabled" @change="value => changeCondition(selected!, field.key, value)" />
          <div class="cr-condition-content">
            <div class="cr-values"><button v-for="value in selected.match[field.key]" :key="value" class="cr-chip" type="button" :disabled="disabled" :aria-label="`移除${valueLabel(field.key, value)}`" @click="values(selected, field.key, selected.match[field.key]!.filter(item => item !== value))">{{ valueLabel(field.key, value) }}<IconX :size="12" aria-hidden="true" /></button><SelectMenu model-value="" :options="availableValues(selected, field.key)" :label="`添加${field.label}`" variant="anime" :disabled="disabled" @change="value => addValue(selected!, field.key, value)" /></div>
            <div class="cr-add-value"><span v-if="field.key === 'countries'" class="cr-condition-note">{{ kind === 'movie' ? '电影按制作国家匹配' : '剧集按原产地匹配' }}</span><details class="cr-custom"><summary :aria-disabled="disabled || undefined" :aria-label="`手动输入${field.label}${field.key === 'genres' ? ' ID' : '代码'}`" @click="disabled && $event.preventDefault()">手动输入</summary><form @submit.prevent="addValue(selected!, field.key, custom[field.key] || '')"><input v-model="custom[field.key]" :disabled="disabled" :aria-label="`自定义${field.label}${field.key === 'genres' ? ' ID' : '代码'}`" :placeholder="field.key === 'genres' ? '类型 ID' : '两位字母代码'" :inputmode="field.key === 'genres' ? 'numeric' : 'text'" :maxlength="field.key === 'genres' ? 10 : 2" /><button class="cr-icon" type="submit" :disabled="disabled || !custom[field.key]?.trim()" :aria-label="`添加自定义${field.label}`"><IconPlus :size="15" aria-hidden="true" /></button></form></details></div>
          </div>
          <button type="button" class="cr-icon cr-remove-condition" :disabled="disabled" :aria-label="`移除${field.label}条件`" @click="values(selected, field.key)"><IconX :size="14" aria-hidden="true" /></button>
        </div>
        <p v-if="!Object.keys(selected.match).length" class="cr-placeholder">添加至少一项条件，才会参与匹配。</p>
        <p v-if="customError || !desktop && error" class="cr-error" role="alert">{{ customError || error }}</p>
        <footer class="cr-detail-footer"><span>按作品类型和分类名称生成目录</span><button class="cr-delete" type="button" :disabled="disabled" @click="deleting = selected"><IconTrash :size="14" aria-hidden="true" />删除规则</button></footer>
      </div>
      <template v-if="!desktop" #footer><button class="cr-button" type="button" :disabled="saving" @click="opened = false">完成编辑</button><button class="cr-button cr-primary" type="button" :disabled="saving || !pending && (disabled || !canSave)" @click="emit('save')"><IconDeviceFloppy :size="15" aria-hidden="true" />{{ saving ? '保存中…' : pending ? '确认保存结果' : '保存全部规则' }}</button></template>
    </ResponsiveEditorPanel>
    <aside v-else-if="desktop" class="cr-editor-empty">添加一条规则后，在这里设置匹配条件。</aside>
    <P115ActionDialog v-if="deleting" title="删除分类规则？" @close="deleting = undefined"><p>删除“{{ deleting.name || '新规则' }}”后，作品继续匹配后续规则。保存后生效。</p><template #footer><button class="cr-button" type="button" @click="deleting = undefined">取消</button><button class="cr-button cr-delete" type="button" @click="remove">删除规则</button></template></P115ActionDialog>
  </section>
</template>

<style scoped>
.classification-editor { --ae-surface: #fff; --ae-ink: #1e293b; --ae-muted: #475569; --ae-inset: #f1f5f9; --ae-blue: #2563eb; color: rgb(var(--color-slate-700)); }
.cr-overview { min-width: 0; }
.cr-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.cr-heading strong { display: flex; align-items: baseline; gap: 8px; font-size: 13px; color: rgb(var(--color-slate-800)); }
.cr-heading strong span { color: rgb(var(--color-slate-500)); font-size: 11px; font-weight: 400; }
.cr-heading p { margin-top: 4px; font-size: 11px; color: rgb(var(--color-slate-500)); line-height: 1.7; }
.cr-placeholder, .cr-empty { font-size: 12px; color: rgb(var(--color-slate-500)); }
.cr-button { display: inline-flex; justify-content: center; align-items: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 8px; background: rgb(148 163 184 / .12); color: rgb(var(--color-slate-700)); font-size: 12px; white-space: nowrap; }
.cr-primary { margin-left: auto; background: #2563eb; color: #fff; }
.cr-list { display: flex; flex-direction: column; align-items: stretch; gap: 4px; }.cr-list :deep(.drag-list-item) { width: 100%; }
.cr-rule { width: 100%; min-width: 0; border-radius: 9px; transition: background-color .15s; }
.cr-rule.is-open { background: #e8f0fd; }
.cr-rule.is-invalid { outline: 1px solid #dc2626; outline-offset: -1px; }
.cr-row { display: flex; align-items: center; min-height: 80px; padding: 8px; gap: 8px; }
.cr-icon { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; width: 30px; height: 30px; border-radius: 6px; color: rgb(var(--color-slate-500)); }.cr-icon:hover:not(:disabled) { background: rgb(148 163 184 / .15); color: #2563eb; }
.cr-grip { display: flex; align-items: center; flex-shrink: 0; color: #94a3b8; }
.cr-priority { display: grid; place-items: center; width: 22px; flex-shrink: 0; color: rgb(var(--color-slate-500)); font-size: 12px; font-variant-numeric: tabular-nums; }
.cr-toggle { display: grid; flex: 1; min-width: 0; gap: 3px; padding: 2px 0; text-align: left; border-radius: 5px; cursor: inherit; }
.cr-name, .cr-path, .cr-summary { display: block; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cr-name { font-size: 14px; font-weight: 600; line-height: 20px; color: rgb(var(--color-slate-800)); }
.cr-path { font-size: 11px; line-height: 16px; color: rgb(var(--color-slate-600)); }
.cr-summary { font-size: 11px; line-height: 16px; color: rgb(var(--color-slate-500)); }
.cr-order { display: flex; flex-direction: column; flex-shrink: 0; }
.cr-detail { display: grid; gap: 16px; }.cr-fields { display: grid; gap: 13px; }.cr-fields label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; font-weight: 500; color: rgb(var(--color-slate-600)); }
.cr-generated { display: grid; gap: 5px; font-size: 12px; color: rgb(var(--color-slate-500)); }.cr-generated code { font-size: 13px; color: rgb(var(--color-slate-700)); overflow-wrap: anywhere; }.cr-fallback code { display: block; margin-top: 6px; font-size: 11px; color: rgb(var(--color-slate-500)); overflow-wrap: anywhere; }
.classification-editor input { min-width: 0; width: 100%; min-height: 36px; padding: 7px 10px; border: 1px solid rgb(148 163 184 / .3); border-radius: 7px; background: #fff; font-size: 13px; font-weight: 400; color: rgb(var(--color-slate-800)); }.classification-editor input::placeholder { color: rgb(var(--color-slate-400)); }
.cr-condition-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: rgb(var(--color-slate-500)); }.cr-condition-heading strong { font-size: 13px; font-weight: 600; color: rgb(var(--color-slate-800)); }.cr-condition-heading p { margin-top: 4px; font-size: 11px; line-height: 1.7; }
.cr-condition { display: grid; grid-template-columns: 124px minmax(0, 1fr) 26px; align-items: start; gap: 8px 10px; min-width: 0; padding-top: 12px; border-top: 1px solid rgb(148 163 184 / .16); }
.cr-condition-field { min-width: 0; }.cr-condition-content { display: grid; gap: 3px; min-width: 0; }.cr-remove-condition { width: 26px; height: 32px; }.cr-condition-note { align-self: center; font-size: 11px; line-height: 1.7; color: rgb(var(--color-slate-500)); }
.cr-values { display: flex; align-items: center; flex-wrap: wrap; gap: 5px; }.cr-chip { display: inline-flex; align-items: center; gap: 6px; min-height: 28px; padding: 3px 8px; border-radius: 6px; background: #e9eef5; color: #334155; font-size: 12px; }.cr-chip:hover:not(:disabled) { background: #dbe7f8; }
.cr-add-value { display: flex; align-items: flex-start; flex-wrap: wrap; gap: 6px 12px; min-width: 0; }.classification-editor :deep(.select-menu-trigger) { min-height: 32px; padding: 5px 9px; gap: 8px; font-size: 12px; }
.cr-custom { min-width: 0; }.cr-custom summary { display: flex; align-items: center; min-height: 32px; list-style: none; color: rgb(var(--color-slate-500)); font-size: 12px; cursor: pointer; }.cr-custom summary::-webkit-details-marker { display: none; }.cr-custom summary[aria-disabled=true] { opacity: .4; cursor: default; }.cr-custom summary:hover:not([aria-disabled=true]) { color: #2563eb; }.cr-custom form { display: flex; align-items: center; width: 185px; max-width: 100%; margin-top: 6px; }.cr-custom form input { min-height: 32px; font-size: 12px; }
.cr-detail-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; font-size: 11px; color: rgb(var(--color-slate-500)); }.cr-delete { display: inline-flex; align-items: center; gap: 5px; min-height: 36px; color: #b91c1c; font-size: 12px; }
.cr-fallback { display: flex; align-items: center; gap: 16px; margin-top: 20px; padding: 0 8px; }.cr-fallback > div { display: flex; flex: 1; flex-direction: column; gap: 4px; }.cr-fallback strong { font-size: 12px; font-weight: 500; color: rgb(var(--color-slate-700)); }.cr-fallback span { font-size: 11px; color: rgb(var(--color-slate-500)); }.cr-fallback label { flex: 1.3; min-width: 0; }.cr-empty { padding: 14px 8px; }.cr-error { font-size: 12px; line-height: 1.8; color: #b91c1c; overflow-wrap: anywhere; }
.classification-editor button:disabled, .cr-button:disabled { opacity: .4; cursor: default; }.classification-editor :focus-visible, .cr-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.cr-desktop { display: grid; grid-template-columns: minmax(0, .85fr) minmax(0, 1.15fr); align-items: start; gap: 24px; }.cr-desktop .cr-fallback { flex-direction: column; align-items: stretch; gap: 8px; }.cr-editor-empty { display: grid; place-items: center; min-height: 220px; padding: 24px; font-size: 13px; color: rgb(var(--color-slate-500)); }
.dark .classification-editor { --ae-surface: rgb(var(--color-slate-900)); --ae-ink: #e2e8f0; --ae-muted: #cbd5e1; --ae-inset: rgb(var(--color-slate-800)); --ae-blue: #93c5fd; color: rgb(var(--color-slate-200)); }
.dark .cr-rule.is-open { background: #233b5d; }.dark .cr-rule.is-invalid { outline-color: #f87171; }
.dark .cr-name, .dark .classification-editor input, .dark .cr-heading strong, .dark .cr-condition-heading strong { color: rgb(var(--color-slate-100)); }.dark .classification-editor input { background: rgb(var(--color-slate-950)); border-color: #41526a; }.dark .cr-path, .dark .cr-fields label, .dark .cr-generated code, .dark .cr-fallback strong { color: #cbd5e1; }
.dark .cr-chip { color: #dbe4ef; background: #334155; }.dark .cr-chip:hover:not(:disabled) { background: #3a4f6c; }.dark .cr-button:not(.cr-primary) { color: #cbd5e1; }.dark .cr-error, .dark .cr-delete { color: #fca5a5; }.dark .cr-icon:hover:not(:disabled) { color: #93c5fd; }.dark .cr-summary, .dark .cr-priority, .dark .cr-placeholder, .dark .cr-detail-footer, .dark .cr-heading p, .dark .cr-fallback span, .dark .cr-condition-heading, .dark .cr-condition-note, .dark .cr-custom summary, .dark .cr-editor-empty { color: #94a3b8; }.dark .cr-custom summary:hover:not([aria-disabled=true]) { color: #93c5fd; }
@media (hover: hover) and (pointer: fine) { .cr-rule:hover:not(.is-open) { background: rgb(148 163 184 / .09); }.cr-order { opacity: 0; pointer-events: none; }.cr-rule:hover .cr-order, .cr-rule:focus-within .cr-order { opacity: 1; pointer-events: auto; } }
@media (any-pointer: coarse) { .cr-order { opacity: 1; pointer-events: auto; } }
@media (prefers-reduced-motion: reduce) { .cr-rule { transition: none; } }
@media(max-width: 639px) {
  .cr-row { padding: 8px 4px; gap: 5px; }.cr-priority { width: 20px; font-size: 11px; }.cr-grip { width: 12px; }.cr-order { opacity: 1; pointer-events: auto; }.cr-order .cr-icon { width: 30px; height: 30px; }.classification-editor input { min-height: 40px; }.cr-chip { min-height: 36px; }.classification-editor :deep(.select-menu-trigger) { min-height: 40px; }.cr-custom summary { min-height: 40px; }.cr-custom form input { min-height: 40px; }.cr-condition { grid-template-columns: minmax(0, 1fr) 34px; }.cr-condition-field { width: 144px; max-width: 100%; }.cr-condition-content { grid-column: 1 / -1; grid-row: 2; }.cr-remove-condition { grid-column: 2; grid-row: 1; width: 34px; height: 40px; }.cr-fallback { flex-direction: column; align-items: stretch; gap: 10px; padding-inline: 4px; }.cr-heading p { max-width: 180px; }.cr-button { min-height: 42px; }.cr-delete { min-height: 40px; }
}
</style>
