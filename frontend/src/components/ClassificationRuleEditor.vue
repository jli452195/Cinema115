<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { IconAdjustmentsHorizontal, IconArrowDown, IconArrowUp, IconBuilding, IconChevronRight, IconDeviceFloppy, IconFolder, IconGripVertical, IconLanguage, IconMapPin, IconPlus, IconTags, IconTrash, IconX } from '@tabler/icons-vue'
import SortableList from './SortableList.vue'
import SelectMenu from './SelectMenu.vue'
import P115ActionDialog from './P115ActionDialog.vue'
import ResponsiveEditorPanel from './ResponsiveEditorPanel.vue'
import { cachedGenres } from '../utils/dashboardPreferences'
import { classificationConditions as conditions } from '../utils/classificationDocument'
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
const conditionIcons = { genres: IconTags, origin_countries: IconMapPin, production_countries: IconBuilding, languages: IconLanguage }
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
const availableConditions = (rule: ClassificationRule) => [{ value: '', label: '添加条件' }, ...conditions.filter(({ key }) => !rule.match[key]).map(({ key, label }) => ({ value: key, label: `${label} ${key}` }))]
const availableValues = (rule: ClassificationRule, field: ClassificationCondition) => [{ value: '', label: '选择添加' }, ...options(field).filter(item => !rule.match[field]?.map(String).includes(item.value))]

function update(rule: ClassificationRule, changes: Partial<ClassificationRule>) {
  if (!props.disabled) model.value = { ...model.value, rules: rows.value.map(item => item.key === rule.key ? { ...item, ...changes } : item) }
}
function values(rule: ClassificationRule, field: ClassificationCondition, next?: (string | number)[]) {
  const match = { ...rule.match }
  if (next) match[field] = next
  else delete match[field]
  update(rule, { match })
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
  rows.value = [...rows.value, { key, name: '', path: '', match: {} }]
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
              <span class="cr-name">{{ item.name || '新规则' }}</span><code class="cr-path" :title="item.path"><IconFolder :size="13" aria-hidden="true" /><span>{{ item.path || '待填写目标目录' }}</span></code><span class="cr-summary" :title="summary(item)"><span v-for="field in conditions.filter(field => item.match[field.key]?.length)" :key="field.key" class="cr-summary-chip"><component :is="conditionIcons[field.key]" :size="12" aria-hidden="true" />{{ item.match[field.key]!.map(value => valueLabel(field.key, value)).join('、') }}</span><span v-if="!conditions.some(field => item.match[field.key]?.length)" class="cr-placeholder">待设置匹配条件</span></span><IconChevronRight :size="16" class="cr-chevron" aria-hidden="true" />
            </button>
            <div class="cr-order" data-drag-disabled="true" @click.stop><button type="button" class="cr-icon" :disabled="disabled || index === 0" :aria-label="`上移${item.name || '新规则'}`" @click="move(item, -1)"><IconArrowUp :size="15" aria-hidden="true" /></button><button type="button" class="cr-icon" :disabled="disabled || index === rows.length - 1" :aria-label="`下移${item.name || '新规则'}`" @click="move(item, 1)"><IconArrowDown :size="15" aria-hidden="true" /></button></div>
          </div>
        </article>
      </template>
      <template #empty><p class="cr-empty">暂无匹配规则，全部使用兜底目录。</p></template>
    </SortableList>
    <div class="cr-fallback"><div><strong><IconFolder :size="15" aria-hidden="true" />兜底目录</strong><span>所有规则未命中时使用</span></div><label><span class="sr-only">{{ kind === 'movie' ? '电影' : '剧集' }}兜底目录</span><input :value="model.fallback" :disabled="disabled" :placeholder="kind === 'movie' ? '例如 电影/其他' : '例如 剧集/其他'" @input="model = { ...model, fallback: ($event.target as HTMLInputElement).value }" /></label></div>
    </div>
    <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    <ResponsiveEditorPanel v-if="selected && (desktop || opened)" :desktop="desktop" :title="`${kind === 'movie' ? '电影' : '剧集'}分类规则`" description="修改保留在当前页面，保存后生效。" :busy="saving" @close="opened = false">
      <template #icon><IconAdjustmentsHorizontal :size="21" aria-hidden="true" /></template>
      <template v-if="!desktop" #actions><SelectMenu :model-value="active" :options="ruleOptions" label="切换编辑规则" :disabled="disabled" @change="expand" /></template>
      <div ref="detail" class="classification-editor cr-detail">
        <div class="cr-fields"><label>规则名称<input :value="selected.name" :disabled="disabled" placeholder="例如 日本动画" @input="update(selected, { name: ($event.target as HTMLInputElement).value })" /></label><label>目标分类目录<input :value="selected.path" :disabled="disabled" :placeholder="kind === 'movie' ? '例如 电影/日本动画' : '例如 剧集/日本动画'" @input="update(selected, { path: ($event.target as HTMLInputElement).value })" /></label></div>
        <div class="cr-condition-heading"><span>不同条件同时满足<br />同项多个值满足任意一个</span><SelectMenu v-if="availableConditions(selected).length > 1" model-value="" :options="availableConditions(selected)" label="添加匹配条件" variant="anime" :menu-min-width="300" :disabled="disabled" @change="value => { if (value) values(selected!, value as ClassificationCondition, []) }" /></div>
        <div v-for="field in conditions.filter(field => !!selected!.match[field.key])" :key="field.key" class="cr-condition">
          <div class="cr-condition-label"><strong><component :is="conditionIcons[field.key]" :size="15" aria-hidden="true" /><span>{{ field.label }}</span><code class="cr-field-key">{{ field.key }}</code></strong><button type="button" class="cr-icon" :disabled="disabled" :aria-label="`移除${field.label}条件`" @click="values(selected, field.key)"><IconX :size="14" aria-hidden="true" /></button></div>
          <div class="cr-values"><button v-for="value in selected.match[field.key]" :key="value" class="cr-chip" type="button" :disabled="disabled" :aria-label="`移除${valueLabel(field.key, value)}`" @click="values(selected, field.key, selected.match[field.key]!.filter(item => item !== value))">{{ valueLabel(field.key, value) }}<IconX :size="12" aria-hidden="true" /></button><span v-if="!selected.match[field.key]?.length" class="cr-placeholder">请选择匹配值</span></div>
          <div class="cr-add-value"><SelectMenu model-value="" :options="availableValues(selected, field.key)" :label="`添加${field.label}`" variant="anime" :disabled="disabled" @change="value => addValue(selected!, field.key, value)" /><form @submit.prevent="addValue(selected!, field.key, custom[field.key] || '')"><input v-model="custom[field.key]" :disabled="disabled" :aria-label="`自定义${field.label}${field.key === 'genres' ? ' ID' : '代码'}`" :placeholder="field.key === 'genres' ? '其他类型 ID' : '其他两位代码'" :inputmode="field.key === 'genres' ? 'numeric' : 'text'" :maxlength="field.key === 'genres' ? 10 : 2" /><button class="cr-icon" type="submit" :disabled="disabled || !custom[field.key]?.trim()" :aria-label="`添加自定义${field.label}`"><IconPlus :size="15" aria-hidden="true" /></button></form></div>
        </div>
        <p v-if="!Object.keys(selected.match).length" class="cr-placeholder">添加至少一项条件，才会参与匹配。</p>
        <p v-if="customError || !desktop && error" class="cr-error" role="alert">{{ customError || error }}</p>
        <footer class="cr-detail-footer"><span>目录相对于整理目标根目录</span><button class="cr-delete" type="button" :disabled="disabled" @click="deleting = selected"><IconTrash :size="14" aria-hidden="true" />删除规则</button></footer>
      </div>
      <template v-if="!desktop" #footer><button class="cr-button" type="button" :disabled="saving" @click="opened = false">完成编辑</button><button class="cr-button cr-primary" type="button" :disabled="saving || !pending && (disabled || !canSave)" @click="emit('save')"><IconDeviceFloppy :size="15" aria-hidden="true" />{{ saving ? '保存中…' : pending ? '确认保存结果' : '保存全部规则' }}</button></template>
    </ResponsiveEditorPanel>
    <aside v-else-if="desktop" class="cr-editor-empty">添加一条规则后，在这里设置匹配条件。</aside>
    <P115ActionDialog v-if="deleting" title="删除分类规则？" @close="deleting = undefined"><p>删除“{{ deleting.name || '新规则' }}”后，作品继续匹配后续规则。保存后生效。</p><template #footer><button class="cr-button" type="button" @click="deleting = undefined">取消</button><button class="cr-button cr-delete" type="button" @click="remove">删除规则</button></template></P115ActionDialog>
  </section>
</template>

<style scoped>
.classification-editor { --ae-surface: #fff; --ae-ink: #1e293b; --ae-muted: #475569; --ae-inset: #edf1f6; --ae-blue: #2563eb; color: rgb(var(--color-slate-700)); }
.cr-overview { min-width: 0; }
.cr-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.cr-heading strong { display: flex; align-items: center; gap: 8px; font-size: 13px; color: rgb(var(--color-slate-800)); }
.cr-heading strong span { padding: 2px 6px; border-radius: 5px; background: #dbe7f7; color: #355886; font-size: 10px; font-weight: 500; }
.cr-heading p { margin-top: 4px; font-size: 11px; color: rgb(var(--color-slate-500)); line-height: 1.7; }
.cr-placeholder, .cr-empty { font-size: 12px; color: rgb(var(--color-slate-500)); }
.cr-button { display: inline-flex; justify-content: center; align-items: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: #dce7f8; color: #244e8b; font-size: 12px; white-space: nowrap; }
.cr-primary { margin-left: auto; background: #2563eb; color: #fff; }
.cr-list { display: flex; flex-direction: column; align-items: stretch; gap: 8px; padding: 8px; border-radius: 14px; background: #e7edf6; }.cr-list :deep(.drag-list-item) { width: 100%; }
.cr-rule { position: relative; width: 100%; min-width: 0; border-radius: 10px; background: #fff; box-shadow: 0 1px 4px rgb(15 23 42 / .04); transition: background-color .15s, box-shadow .15s; }
.cr-rule.is-open { background: #e5efff; box-shadow: 0 0 0 1px #91b7f5, 0 3px 9px rgb(37 99 235 / .09); }
.cr-rule.is-open::before { content: ''; position: absolute; left: 0; top: 14px; bottom: 14px; width: 3px; border-radius: 0 3px 3px 0; background: #2563eb; }
.cr-rule.is-invalid { outline: 1px solid #dc2626; }
.cr-row { display: flex; align-items: center; min-height: 88px; padding: 9px 7px; gap: 7px; }
.cr-icon { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; width: 30px; height: 30px; border-radius: 7px; color: rgb(var(--color-slate-500)); }.cr-icon:hover:not(:disabled) { background: rgb(var(--color-slate-200) / .6); color: #2563eb; }
.cr-grip { display: flex; align-items: center; flex-shrink: 0; color: #94a3b8; }
.cr-priority { display: grid; place-items: center; width: 30px; height: 34px; flex-shrink: 0; border-radius: 8px; background: #e8eef8; color: #52739f; font-size: 12px; font-weight: 600; font-variant-numeric: tabular-nums; }.is-open .cr-priority { background: #2563eb; color: #fff; box-shadow: 0 2px 5px rgb(37 99 235 / .18); }
.cr-toggle { display: grid; flex: 1; min-width: 0; grid-template-columns: minmax(0, 1fr) 16px; align-items: center; gap: 5px 7px; padding: 2px 3px; text-align: left; border-radius: 6px; cursor: inherit; }
.cr-name { font-size: 13px; font-weight: 600; color: rgb(var(--color-slate-800)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cr-path { grid-column: 1; grid-row: 2; display: flex; align-items: center; gap: 5px; font-size: 11px; color: #315f9f; min-width: 0; }.cr-path svg { flex-shrink: 0; }.cr-path > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cr-summary { grid-column: 1; grid-row: 3; display: flex; align-items: center; gap: 4px; height: 22px; overflow: hidden; }.cr-summary-chip { display: inline-flex; align-items: center; gap: 4px; flex-shrink: 0; max-width: 180px; padding: 2px 6px; border-radius: 5px; background: #edf1f7; color: #526681; font-size: 10px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }.cr-summary-chip svg { flex-shrink: 0; }.is-open .cr-summary-chip { background: #cfdffa; color: #244e8b; }
.cr-chevron { grid-column: 2; grid-row: 1 / 4; color: #8393a9; }.is-open .cr-chevron { color: #2563eb; }.cr-order { display: flex; flex-direction: column; }
.cr-detail { display: grid; gap: 14px; }.cr-fields { display: grid; gap: 12px; }.cr-fields label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; font-weight: 500; color: #526681; }
.classification-editor input { min-width: 0; width: 100%; min-height: 36px; padding: 7px 9px; border: 1px solid #ccd8e8; border-radius: 7px; background: #fff; font-size: 13px; font-weight: 400; color: rgb(var(--color-slate-800)); }.classification-editor input::placeholder { color: rgb(var(--color-slate-400)); }
.cr-condition-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; font-size: 11px; line-height: 1.7; color: #526681; }
.cr-condition { display: grid; gap: 9px; padding: 12px; border-radius: 10px; background: #fff; box-shadow: 0 1px 3px rgb(30 58 100 / .04); }
.cr-condition-label { display: flex; justify-content: space-between; align-items: center; gap: 3px; font-size: 12px; }.cr-condition-label strong { display: flex; align-items: center; flex-wrap: wrap; min-width: 0; gap: 4px 6px; font-weight: 600; color: #355886; }.cr-condition-label strong svg { flex-shrink: 0; color: #4d7bbe; }.cr-condition-label strong > span { white-space: nowrap; }.cr-field-key { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; font-weight: 400; color: #64748b; overflow-wrap: anywhere; }.cr-condition-label .cr-icon { width: 25px; height: 25px; }
.cr-values { display: flex; align-items: center; flex-wrap: wrap; gap: 5px; }.cr-chip { display: inline-flex; align-items: center; gap: 6px; min-height: 28px; padding: 3px 8px; border-radius: 6px; background: #dbeafe; color: #1e40af; font-size: 12px; }
.cr-add-value { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }.cr-add-value form { display: flex; align-items: center; flex: 1; min-width: 100px; max-width: 160px; }.cr-add-value form input { min-height: 32px; border: 1px solid transparent; background: #f1f5fa; font-size: 12px; }.classification-editor :deep(.select-menu-trigger) { min-height: 32px; padding: 5px 8px; gap: 8px; font-size: 12px; }
.cr-detail-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; padding-top: 4px; font-size: 11px; color: rgb(var(--color-slate-500)); }.cr-delete { display: inline-flex; align-items: center; gap: 5px; min-height: 36px; color: #b91c1c; font-size: 12px; }
.cr-fallback { display: flex; align-items: center; gap: 16px; margin-top: 12px; padding: 13px 14px; border-radius: 12px; background: #dfe7f1; }.cr-fallback > div { display: flex; flex: 1; flex-direction: column; gap: 4px; }.cr-fallback strong { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; color: #526681; }.cr-fallback span { font-size: 11px; color: #647892; }.cr-fallback label { flex: 1.3; min-width: 0; }.cr-empty { padding: 14px; }.cr-error { font-size: 12px; line-height: 1.8; color: #b91c1c; overflow-wrap: anywhere; }
.classification-editor button:disabled, .cr-button:disabled { opacity: .4; cursor: default; }.classification-editor :focus-visible, .cr-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.cr-desktop { display: grid; grid-template-columns: minmax(0, .85fr) minmax(0, 1.15fr); align-items: start; gap: 20px; }.cr-desktop .cr-fallback { flex-direction: column; align-items: stretch; gap: 8px; }.cr-editor-empty { display: grid; place-items: center; min-height: 220px; padding: 24px; border-radius: 12px; background: #e7edf6; font-size: 13px; color: #526681; }
.dark .classification-editor { --ae-surface: rgb(var(--color-slate-900)); --ae-ink: #e2e8f0; --ae-muted: #cbd5e1; --ae-inset: rgb(var(--color-slate-800)); --ae-blue: #93c5fd; color: rgb(var(--color-slate-200)); }
.dark .cr-list { background: #1c2533; }.dark .cr-rule { background: #283240; }.dark .cr-rule.is-open { background: #263f63; box-shadow: 0 0 0 1px #5278b0, 0 3px 10px rgb(0 0 0 / .1); }.dark .cr-rule.is-open::before { background: #78a9f8; }.dark .cr-rule.is-invalid { outline-color: #f87171; }
.dark .cr-priority { background: #35465e; color: #b4c7e3; }.dark .is-open .cr-priority { background: #376cc5; color: #fff; }.dark .cr-summary-chip { background: #354357; color: #c2cfe0; }.dark .is-open .cr-summary-chip { background: #345785; color: #d2e3ff; }
.dark .cr-name, .dark .classification-editor input, .dark .cr-heading strong { color: rgb(var(--color-slate-100)); }.dark .classification-editor input { background: rgb(var(--color-slate-950)); border-color: #41526a; }.dark .cr-path, .dark .is-open .cr-chevron, .dark .cr-condition-label strong svg { color: #9cbff0; }
.dark .cr-condition { background: #293442; }.dark .cr-button:not(.cr-primary), .dark .cr-heading strong span { background: #304661; color: #c6dcff; }.dark .cr-fallback, .dark .cr-editor-empty { background: #283342; }.dark .cr-chip { color: #d2e3ff; background: #345785; }
.dark .cr-field-key { color: #a0b0c6; }
.dark .cr-error, .dark .cr-delete { color: #fca5a5; }.dark .cr-icon:hover:not(:disabled) { background: #384860; }.dark .cr-placeholder, .dark .cr-detail-footer, .dark .cr-heading p, .dark .cr-fields label, .dark .cr-fallback strong, .dark .cr-fallback span, .dark .cr-condition-heading, .dark .cr-condition-label strong, .dark .cr-editor-empty { color: #b2c1d5; }
@media (hover: hover) { .cr-rule:hover:not(.is-open) { background: #f4f8ff; box-shadow: 0 2px 7px rgb(30 58 100 / .1); }.dark .cr-rule:hover:not(.is-open) { background: #304158; } }
@media (prefers-reduced-motion: reduce) { .cr-rule { transition: none; } }
@media(max-width: 639px) {
  .cr-list { padding: 6px; }.cr-row { padding: 8px 5px; gap: 4px; }.cr-priority { width: 25px; height: 30px; font-size: 11px; }.cr-grip { width: 12px; }.cr-summary-chip { max-width: 150px; }.cr-toggle { gap: 5px 3px; }.cr-order .cr-icon { width: 30px; height: 30px; }.classification-editor input { min-height: 40px; }.cr-chip { min-height: 36px; }.classification-editor :deep(.select-menu-trigger) { min-height: 40px; }.cr-condition-label .cr-icon { width: 34px; height: 34px; }.cr-fallback { flex-direction: column; align-items: stretch; gap: 10px; padding: 12px; }.cr-heading p { max-width: 180px; }.cr-button { min-height: 42px; }.cr-delete { min-height: 40px; }
}
</style>
