<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { IconCode, IconDeviceFloppy, IconRefresh, IconRestore } from '@tabler/icons-vue'
import ClassificationRuleEditor from '../components/ClassificationRuleEditor.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { portalUser } from '../composables/usePortalAuth'
import { classificationRequest } from '../lib/classification-api'
import { classificationIssue, classificationSignature, parseClassification, validateClassification, writeClassification, type ClassificationEditorDocument } from '../utils/classificationDocument'
import type { ClassificationConfig, ClassificationDiagnostic, ClassificationKind, ClassificationRecord, ClassificationSettings } from '../types/classification'

const props = defineProps<{ token: string }>()
const YamlSourceEditor = defineAsyncComponent(() => import('../components/YamlSourceEditor.vue'))
const kind = ref<ClassificationKind>('movie'), settings = shallowRef<ClassificationSettings>(), editor = shallowRef<ClassificationEditorDocument>()
const drafts = ref<ClassificationConfig>({ movie: { fallback: '', rules: [] }, tv: { fallback: '', rules: [] } })
const current = computed({ get: () => drafts.value[kind.value], set: value => { drafts.value[kind.value] = value } })
const ruleEditor = ref<InstanceType<typeof ClassificationRuleEditor>>()
const sourceInput = ref<InstanceType<typeof YamlSourceEditor>>(), mode = ref<'visual' | 'source'>('visual')
const documentGeneration = ref(0)
const workingSource = ref(''), parseError = ref(''), error = ref(''), notice = ref('')
const loading = ref(true), saving = ref(false), pending = shallowRef<{ yaml: string; revision: string }>()
const locked = computed(() => loading.value || saving.value || !!pending.value)
const dirty = computed(() => !!settings.value && (workingSource.value !== settings.value.record.yaml || mode.value === 'visual' && !!editor.value && classificationSignature(drafts.value) !== classificationSignature(editor.value.config)))
const fileNotActive = computed(() => !!settings.value && settings.value.record.revision !== settings.value.record.activeRevision)
const issue = computed(() => editor.value ? classificationIssue(drafts.value) : undefined)
const diagnostic = ref<ClassificationDiagnostic>(), invalidKey = ref('')
const dialog = ref<'leave' | 'read' | 'reset' | ''>('')
let lifecycle = new AbortController(), leaveDecision: ((value: boolean) => void) | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])

function setDocument(source: string) {
  documentGeneration.value++
  editor.value = undefined; parseError.value = ''; workingSource.value = source; invalidKey.value = ''; diagnostic.value = undefined
  try {
    const parsed = parseClassification(source)
    editor.value = parsed; drafts.value = structuredClone(parsed.config)
  } catch (reason) { sourceFailure(reason) }
}
function sourceFailure(reason: unknown) {
  mode.value = 'source'; parseError.value = message(reason)
  diagnostic.value = (reason as { diagnostic?: ClassificationDiagnostic }).diagnostic
}
async function locateSource() {
  await nextTick()
  sourceInput.value?.locate(diagnostic.value?.line, diagnostic.value?.column)
}
async function chooseMode(value: 'visual' | 'source') {
  if (locked.value || value === mode.value) return
  error.value = ''; notice.value = ''; diagnostic.value = undefined
  if (value === 'source') {
    try { if (editor.value) workingSource.value = writeClassification(editor.value, drafts.value) }
    catch (reason) { error.value = message(reason); return }
    mode.value = value; parseError.value = ''
    await nextTick(); sourceInput.value?.focus()
  } else {
    setDocument(workingSource.value)
    if (editor.value) {
      mode.value = value
      if (issue.value) { error.value = issue.value.message; await revealProblem() }
    }
    else await locateSource()
  }
}
function editSource(value: string) {
  workingSource.value = value; parseError.value = ''; error.value = ''; notice.value = ''; diagnostic.value = undefined
}
function accept(record: ClassificationRecord) {
  settings.value = { ...settings.value!, record }
  setDocument(record.yaml)
  diagnostic.value = record.diagnostic || diagnostic.value
}
async function read() {
  const session = lifecycle.signal
  loading.value = true; error.value = ''; notice.value = ''
  try {
    const value = await classificationRequest<ClassificationSettings>(props.token, '', { signal: signal() })
    if (session.aborted) return
    settings.value = value; accept(value.record)
  } catch (reason) { if (!session.aborted) error.value = message(reason) }
  finally { if (!session.aborted) loading.value = false }
}
async function revealProblem(field?: string) {
  if (mode.value === 'source') { await locateSource(); return }
  const match = field?.match(/^(movie|tv)(?:\.rules\[(\d+)\])?/)
  const target = match ? match[1] as ClassificationKind : issue.value?.kind
  if (!target) return
  kind.value = target
  invalidKey.value = match ? drafts.value[target].rules[Number(match[2])]?.key || '' : issue.value?.key || ''
  await nextTick(); await ruleEditor.value?.reveal(invalidKey.value)
}
async function save() {
  if (locked.value || !settings.value || !dirty.value) return
  if (mode.value === 'visual' && issue.value) { error.value = issue.value.message; await revealProblem(); return }
  let yaml: string | undefined
  try {
    yaml = mode.value === 'source' ? workingSource.value : writeClassification(editor.value!, drafts.value)
    validateClassification(parseClassification(yaml))
    pending.value = { yaml, revision: settings.value.record.revision }
  } catch (reason) { if (yaml !== undefined) workingSource.value = yaml; sourceFailure(reason); await locateSource(); return }
  await confirmSave()
}
async function confirmSave() {
  const request = pending.value, session = lifecycle.signal
  if (!request || saving.value) return
  let failureField: string | undefined
  saving.value = true; error.value = ''; notice.value = ''; diagnostic.value = undefined
  try {
    const value = await classificationRequest<ClassificationRecord>(props.token, '', { method: 'PUT', body: JSON.stringify(request), signal: signal() })
    if (session.aborted || pending.value !== request) return
    if (value.yaml !== request.yaml || !value.revision || value.activeRevision !== value.revision || value.loadError) throw new Error('保存结果尚未确认，请确认原请求')
    accept(value); pending.value = undefined; notice.value = '电影和剧集分类规则已保存并生效'
  } catch (reason) {
    if (session.aborted || pending.value !== request) return
    const failure = reason as { status?: number; diagnostic?: ClassificationDiagnostic }
    error.value = failure.status === 409 ? '文件已被其他页面或外部修改。本地编辑已保留，请重新读取后再编辑。' : message(reason)
    diagnostic.value = failure.diagnostic
    if (failure.status && failure.status >= 400 && failure.status < 500 && failure.status !== 408 && failure.status !== 429) pending.value = undefined
    failureField = failure.diagnostic?.field
  } finally { if (!session.aborted) saving.value = false }
  if (!session.aborted && failureField) await revealProblem(failureField)
}
async function activateFile() {
  if (locked.value || dirty.value || !settings.value) return
  const session = lifecycle.signal
  loading.value = true; error.value = ''; notice.value = ''
  try {
    const value = await classificationRequest<ClassificationRecord>(props.token, '/reload', { method: 'POST', signal: signal() })
    if (!session.aborted) { accept(value); notice.value = '文件中的分类规则已生效' }
  } catch (reason) {
    if (!session.aborted) { error.value = message(reason); diagnostic.value = (reason as { diagnostic?: ClassificationDiagnostic }).diagnostic }
  } finally { if (!session.aborted) loading.value = false }
}
function reread() { if (!locked.value) dirty.value ? dialog.value = 'read' : void read() }
function decide(value: boolean) {
  const action = dialog.value; dialog.value = ''
  if (action === 'leave') { if (value) pending.value = undefined; leaveDecision?.(value); leaveDecision = undefined }
  if (value && action === 'read') { pending.value = undefined; void read() }
  if (value && action === 'reset' && settings.value) {
    setDocument(settings.value.defaultYAML); error.value = ''; diagnostic.value = undefined
    notice.value = '电影和剧集均已恢复默认，保存后生效'
  }
}
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value || pending.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteUpdate(() => !locked.value)
onBeforeRouteLeave(() => {
  if (saving.value) { error.value = '正在保存，请稍后再离开'; return false }
  if (!dirty.value && !pending.value) return true
  leaveDecision?.(false)
  dialog.value = 'leave'; return new Promise<boolean>(resolve => { leaveDecision = resolve })
})
watch(() => classificationSignature(drafts.value), () => { invalidKey.value = ''; diagnostic.value = undefined; error.value = ''; notice.value = '' }, { flush: 'sync' })
watch([() => portalUser.value?.id, () => portalUser.value?.role], () => {
  lifecycle.abort(); lifecycle = new AbortController(); settings.value = undefined; editor.value = undefined; pending.value = undefined
  drafts.value = { movie: { fallback: '', rules: [] }, tv: { fallback: '', rules: [] } }
  workingSource.value = ''; parseError.value = ''; diagnostic.value = undefined; mode.value = 'visual'; saving.value = false; dialog.value = ''; leaveDecision?.(false); leaveDecision = undefined
  if (portalUser.value?.role === 'admin') void read()
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void read() })
onBeforeUnmount(() => { lifecycle.abort(); window.removeEventListener('beforeunload', beforeUnload); leaveDecision?.(false) })
</script>

<template>
    <div class="classification-settings">
      <header class="cs-toolbar">
        <div v-if="mode === 'visual'" class="cs-kind" role="group" aria-label="作品类型"><button v-for="option in [{ value: 'movie' as const, label: '电影' }, { value: 'tv' as const, label: '剧集' }]" :key="option.value" type="button" :disabled="locked || !settings" :aria-pressed="kind === option.value" @click="kind = option.value">{{ option.label }}</button></div>
        <p v-else class="cs-scope-label">完整分类配置<span>电影和剧集</span></p>
        <div class="cs-tools">
          <div class="cs-mode" role="group" aria-label="编辑模式"><button type="button" :disabled="locked || !settings" :aria-pressed="mode === 'visual'" @click="chooseMode('visual')">可视化</button><button type="button" :disabled="locked || !settings" :aria-pressed="mode === 'source'" @click="chooseMode('source')">YAML</button></div>
          <div class="cs-actions"><button class="cs-button cs-secondary" type="button" :disabled="locked || !settings" @click="dialog = 'reset'"><IconRestore :size="15" aria-hidden="true" />恢复默认</button><button class="cs-button cs-primary" type="button" :disabled="locked || !settings || !dirty" @click="save"><IconDeviceFloppy :size="15" aria-hidden="true" />{{ saving ? '保存中…' : '保存规则' }}</button></div>
        </div>
      </header>
      <p v-if="loading" class="cs-status" role="status">正在读取分类规则…</p>
      <div v-if="settings?.record.loadError && !dirty" class="cs-warning" role="alert"><p>文件未能正常加载：{{ settings.record.loadError }}</p><p>{{ settings.record.activeRevision ? '继续使用最后一次有效规则。' : '保存有效规则后才能启动需要分类的整理任务。' }}请修正规则或恢复默认后保存。</p></div>
      <div v-else-if="fileNotActive" class="cs-status cs-file"><span>文件内容与当前生效规则不同</span><button class="cs-button" type="button" :disabled="locked || dirty" @click="activateFile">应用文件修改</button></div>
      <div v-if="error || parseError || diagnostic" class="cs-feedback" role="alert">
        <p v-if="error || parseError" class="cs-error">{{ error || parseError }}</p>
        <button v-if="diagnostic" class="cs-location cs-error" type="button" :disabled="locked" @click="revealProblem(diagnostic.field)">{{ diagnostic.field }}<template v-if="diagnostic.line">，第 {{ diagnostic.line }} 行，第 {{ diagnostic.column }} 列</template><span v-if="mode === 'source'">定位</span></button>
      </div>
      <template v-if="settings">
        <section v-if="mode === 'source'" class="cs-source" aria-label="分类源码编辑">
          <header><div><IconCode :size="17" aria-hidden="true" /><code>classification.yaml</code></div><span>电影和剧集共用配置</span></header>
          <Suspense><YamlSourceEditor ref="sourceInput" :model-value="workingSource" :disabled="locked" :diagnostic="diagnostic" @update:model-value="editSource" @ready="nextTick(() => diagnostic ? locateSource() : sourceInput?.focus())" /><template #fallback><div class="cs-editor-loading" role="status">正在加载 YAML 编辑器…</div></template></Suspense>
          <p id="classification-source-help">Tab 缩进，Shift + Tab 取消缩进；切换模式不保存，修正后可切回可视化或保存规则。</p>
        </section>
      </template>
      <ClassificationRuleEditor v-if="mode === 'visual' && editor" :key="`${documentGeneration}-${kind}`" ref="ruleEditor" v-model="current" :kind="kind" :disabled="locked" :invalid-key="invalidKey" :can-save="dirty" :saving="saving" :pending="!!pending" :error="error" @save="pending ? confirmSave() : save()" />
      <div class="cs-feedback" aria-live="polite" role="status">
        <template v-if="!error && !parseError"><p v-if="notice">{{ notice }}</p><p v-else-if="settings && dirty">有未保存的修改；保存将同时更新电影和剧集规则</p></template>
        <div class="cs-feedback-actions"><div v-if="pending" class="cs-actions"><button class="cs-button" type="button" :disabled="saving" @click="confirmSave">确认保存结果</button><button class="cs-read" type="button" :disabled="saving" @click="dialog = 'read'">放弃等待并重读</button></div><button v-else class="cs-read" type="button" :disabled="locked" @click="reread"><IconRefresh :size="14" aria-hidden="true" />重新读取文件</button><span>classification.yaml</span></div>
      </div>
    </div>
    <P115ActionDialog v-if="dialog" :title="dialog === 'reset' ? '恢复全部默认分类？' : dialog === 'read' ? '重新读取分类文件？' : pending ? '放弃确认并离开？' : '放弃未保存的修改？'" @close="decide(false)">
      <p v-if="pending">保存结果尚未确认，规则可能已经生效。{{ dialog === 'read' ? '重新读取将放弃本地编辑，以文件当前内容为准。' : '离开后本地编辑会丢失，下次进入将读取文件当前内容。' }}</p>
      <p v-else>{{ dialog === 'reset' ? '电影和剧集规则均会替换为默认配置，保存后才生效。' : dialog === 'read' ? '电影和剧集的本地修改会被文件内容替换。' : '电影和剧集的本地修改尚未保存，离开后会丢失。' }}</p>
      <template #footer><button class="cs-button" type="button" @click="decide(false)">取消</button><button class="cs-button cs-primary" type="button" @click="decide(true)">{{ dialog === 'reset' ? '恢复默认' : dialog === 'read' ? '重新读取' : '放弃并离开' }}</button></template>
    </P115ActionDialog>
</template>

<style scoped>
.classification-settings { display: grid; gap: 18px; }
.cs-toolbar, .cs-tools, .cs-actions { display: flex; align-items: center; }
.cs-toolbar { justify-content: space-between; gap: 12px; flex-wrap: wrap; min-height: 40px; }
.cs-tools { gap: 18px; min-width: 0; }
.cs-actions { gap: 8px; }
.cs-kind { display: inline-flex; gap: 5px; }
.cs-kind button { min-height: 40px; padding: 8px 16px; font-size: 14px; color: rgb(var(--color-slate-500)); }
.cs-kind button[aria-pressed=true] { color: #1d4ed8; font-weight: 600; box-shadow: inset 0 -2px currentColor; }
.cs-mode { display: inline-flex; width: fit-content; gap: 2px; padding: 3px; border-radius: 8px; background: rgb(148 163 184 / .12); }
.cs-mode button { min-height: 30px; padding: 5px 11px; border-radius: 5px; font-size: 12px; color: rgb(var(--color-slate-500)); }
.cs-mode button[aria-pressed=true] { background: #fff; color: rgb(var(--color-slate-800)); }
.cs-scope-label { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; font-size: 14px; font-weight: 600; color: rgb(var(--color-slate-800)); }
.cs-scope-label span { font-size: 12px; font-weight: 400; color: rgb(var(--color-slate-500)); }
.cs-button { display: inline-flex; justify-content: center; align-items: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 8px; background: rgb(148 163 184 / .12); font-size: 12px; white-space: nowrap; color: rgb(var(--color-slate-700)); }
.cs-secondary { background: transparent; }
.cs-primary { background: #2563eb; color: #fff; }
.cs-button:hover:not(:disabled) { filter: brightness(.96); }
.cs-secondary:hover:not(:disabled) { background: rgb(148 163 184 / .1); }
.cs-button:disabled, .cs-mode button:disabled, .cs-kind button:disabled, .cs-read:disabled { opacity: .45; cursor: default; }
.cs-status, .cs-feedback { font-size: 12px; color: rgb(var(--color-slate-500)); line-height: 1.8; }
.cs-feedback { overflow-wrap: anywhere; }
.cs-feedback-actions { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 10px; margin-top: 8px; }
.cs-feedback-actions > span { color: rgb(var(--color-slate-400)); font-size: 11px; }
.cs-read { display: inline-flex; align-items: center; gap: 6px; padding-block: 7px; color: #2563eb; }
.cs-file { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.cs-warning { font-size: 12px; line-height: 1.8; color: #92400e; background: #fffbeb; border-radius: 10px; padding: 12px; overflow-wrap: anywhere; }
.cs-error { color: #b91c1c; }
.classification-settings :focus-visible, .cs-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.dark .cs-mode { background: rgb(148 163 184 / .12); }
.dark .cs-mode button, .dark .cs-kind button { color: rgb(var(--color-slate-400)); }
.dark .cs-mode button[aria-pressed=true] { background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-100)); }
.dark .cs-kind button[aria-pressed=true] { color: #93c5fd; }
.dark .cs-scope-label, .dark .cs-button:not(.cs-primary) { color: rgb(var(--color-slate-200)); }
.dark .cs-status, .dark .cs-feedback { color: rgb(var(--color-slate-400)); }
.dark .cs-read { color: #93c5fd; }
.dark .cs-error { color: #fca5a5; }
.dark .cs-warning { color: #fde68a; background: #451a03; }
@media(max-width: 639px) {
  .cs-toolbar { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 12px 8px; }
  .cs-tools { display: contents; }
  .cs-mode { justify-self: end; }
  .cs-kind button { min-height: 42px; padding-inline: 12px; }
  .cs-mode button { min-height: 36px; padding-inline: 9px; }
  .cs-actions { grid-column: 1 / -1; justify-content: flex-end; gap: 8px; }
  .cs-feedback .cs-actions { justify-content: flex-start; }
  .cs-scope-label { font-size: 13px; gap: 2px 8px; }
  .cs-scope-label span { font-size: 11px; }
  .cs-button { min-height: 42px; padding-inline: 10px; }
  .cs-file { align-items: flex-start; }
  .cs-read { min-height: 42px; }
}
.cs-source { min-width: 0; overflow: hidden; border-radius: 8px; background: #282c34; }
.cs-source > header { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 5px 12px; min-height: 42px; padding: 10px 15px; background: #21252b; font-size: 11px; color: #9da5b4; }.cs-source > header > div { display: flex; align-items: center; gap: 8px; }.cs-source > header code { font-size: 12px; color: #c6cbd3; }
.cs-source > p { padding: 9px 15px; background: #21252b; font-size: 11px; color: #9da5b4; line-height: 1.8; }.cs-location { display: inline-flex; align-items: center; flex-wrap: wrap; gap: 10px; padding-block: 4px; text-align: left; }.cs-location span { text-decoration: underline; }.cs-error { white-space: pre-wrap; }
.cs-editor-loading { display: grid; place-items: center; height: clamp(300px, 55dvh, 560px); font-size: 13px; color: #abb2bf; }
@media(max-width: 639px) { .cs-source > header { padding: 10px; }.cs-source > header > span { font-size: 10px; }.cs-source > p { padding: 10px; }.cs-location { min-height: 42px; } }
</style>
