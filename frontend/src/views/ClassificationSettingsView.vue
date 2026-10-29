<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { IconDeviceFloppy, IconRefresh, IconRestore } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import ClassificationRuleEditor from '../components/ClassificationRuleEditor.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { portalUser } from '../composables/usePortalAuth'
import { classificationRequest } from '../lib/classification-api'
import { classificationIssue, classificationSignature, parseClassification, writeClassification, type ClassificationEditorDocument } from '../utils/classificationDocument'
import type { ClassificationConfig, ClassificationDiagnostic, ClassificationKind, ClassificationRecord, ClassificationSettings } from '../types/classification'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const kind = ref<ClassificationKind>('movie'), settings = shallowRef<ClassificationSettings>(), editor = shallowRef<ClassificationEditorDocument>()
const drafts = ref<ClassificationConfig>({ movie: { fallback: '', rules: [] }, tv: { fallback: '', rules: [] } })
const current = computed({ get: () => drafts.value[kind.value], set: value => { drafts.value[kind.value] = value } })
const ruleEditor = ref<InstanceType<typeof ClassificationRuleEditor>>()
const documentGeneration = ref(0)
const baseline = ref(''), workingSource = ref(''), parseError = ref(''), error = ref(''), notice = ref('')
const loading = ref(true), saving = ref(false), pending = shallowRef<{ yaml: string; revision: string }>()
const locked = computed(() => loading.value || saving.value || !!pending.value)
const dirty = computed(() => !!editor.value && (classificationSignature(drafts.value) !== baseline.value || workingSource.value !== settings.value?.record.yaml))
const fileNotActive = computed(() => !!settings.value && settings.value.record.revision !== settings.value.record.activeRevision)
const issue = computed(() => editor.value ? classificationIssue(drafts.value) : undefined)
const diagnostic = ref<ClassificationDiagnostic>(), invalidKey = ref('')
const dialog = ref<'leave' | 'read' | 'reset' | ''>('')
let lifecycle = new AbortController(), leaveDecision: ((value: boolean) => void) | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])

function setDocument(source: string) {
  documentGeneration.value++
  editor.value = undefined; parseError.value = ''; workingSource.value = source; invalidKey.value = ''
  try {
    const parsed = parseClassification(source)
    editor.value = parsed; drafts.value = parsed.config
  } catch (reason) { parseError.value = message(reason) }
}
function accept(record: ClassificationRecord) {
  settings.value = { ...settings.value!, record }
  setDocument(record.yaml)
  baseline.value = classificationSignature(drafts.value)
  diagnostic.value = record.diagnostic
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
  const match = field?.match(/^(movie|tv)(?:\.rules\[(\d+)\])?/)
  const target = match ? match[1] as ClassificationKind : issue.value?.kind
  if (!target) return
  kind.value = target
  invalidKey.value = match ? drafts.value[target].rules[Number(match[2])]?.key || '' : issue.value?.key || ''
  await nextTick(); await ruleEditor.value?.reveal(invalidKey.value)
}
async function save() {
  if (locked.value || !editor.value || !settings.value || !dirty.value) return
  if (issue.value) { error.value = issue.value.message; await revealProblem(); return }
  try {
    pending.value = { yaml: writeClassification(editor.value, drafts.value), revision: settings.value.record.revision }
    await confirmSave()
  } catch (reason) { error.value = message(reason) }
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
  baseline.value = ''; workingSource.value = ''; saving.value = false; dialog.value = ''; leaveDecision?.(false); leaveDecision = undefined
  if (portalUser.value?.role === 'admin') void read()
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void read() })
onBeforeUnmount(() => { lifecycle.abort(); window.removeEventListener('beforeunload', beforeUnload); leaveDecision?.(false) })
</script>

<template>
  <ManagementLayout title="分类规则" description="按作品信息匹配分类目录，供自动整理使用。作品目录和文件名沿用命名模板。" content-class="max-w-3xl" @logout="$emit('logout')">
    <div class="classification-settings">
      <header class="cs-toolbar">
        <div class="cs-kind" role="group" aria-label="作品类型"><button v-for="option in [{ value: 'movie' as const, label: '电影' }, { value: 'tv' as const, label: '剧集' }]" :key="option.value" type="button" :disabled="locked" :aria-pressed="kind === option.value" @click="kind = option.value">{{ option.label }}</button></div>
        <div class="cs-actions"><button class="cs-button" type="button" :disabled="locked || !settings" @click="dialog = 'reset'"><IconRestore :size="15" aria-hidden="true" />恢复默认</button><button class="cs-button cs-primary" type="button" :disabled="locked || !editor || !dirty" @click="save"><IconDeviceFloppy :size="15" aria-hidden="true" />{{ saving ? '保存中…' : '保存规则' }}</button></div>
      </header>
      <p v-if="loading" class="cs-status" role="status">正在读取分类规则…</p>
      <div v-if="settings?.record.loadError || parseError" class="cs-warning" role="alert"><p>文件未能正常加载：{{ settings?.record.loadError || parseError }}</p><p>{{ settings?.record.activeRevision ? '继续使用最后一次有效规则。' : '保存有效规则后才能启动需要分类的整理任务。' }}可恢复默认后保存。</p></div>
      <div v-else-if="fileNotActive" class="cs-status cs-file"><span>文件内容与当前生效规则不同</span><button class="cs-button" type="button" :disabled="locked || dirty" @click="activateFile">应用文件修改</button></div>
      <ClassificationRuleEditor v-if="editor" :key="`${documentGeneration}-${kind}`" ref="ruleEditor" v-model="current" :kind="kind" :disabled="locked" :invalid-key="invalidKey" :can-save="dirty" :saving="saving" :pending="!!pending" :error="error" @save="pending ? confirmSave() : save()" />
      <div class="cs-feedback" aria-live="polite" role="status">
        <p v-if="error" class="cs-error">{{ error }}</p><p v-else-if="notice">{{ notice }}</p><p v-else-if="editor">{{ dirty ? '有未保存的修改；保存将同时更新电影和剧集规则' : '电影和剧集共用一份分类文件' }}</p>
        <p v-if="diagnostic" class="cs-error">{{ diagnostic.field }}<template v-if="diagnostic.line">，第 {{ diagnostic.line }} 行，第 {{ diagnostic.column }} 列</template></p>
        <div class="cs-feedback-actions"><div v-if="pending" class="cs-actions"><button class="cs-button" type="button" :disabled="saving" @click="confirmSave">确认保存结果</button><button class="cs-read" type="button" :disabled="saving" @click="dialog = 'read'">放弃等待并重读</button></div><button v-else class="cs-read" type="button" :disabled="locked" @click="reread"><IconRefresh :size="14" aria-hidden="true" />重新读取文件</button><span>classification.yaml</span></div>
      </div>
    </div>
    <P115ActionDialog v-if="dialog" :title="dialog === 'reset' ? '恢复全部默认分类？' : dialog === 'read' ? '重新读取分类文件？' : pending ? '放弃确认并离开？' : '放弃未保存的修改？'" @close="decide(false)">
      <p v-if="pending">保存结果尚未确认，规则可能已经生效。{{ dialog === 'read' ? '重新读取将放弃本地编辑，以文件当前内容为准。' : '离开后本地编辑会丢失，下次进入将读取文件当前内容。' }}</p>
      <p v-else>{{ dialog === 'reset' ? '电影和剧集规则均会替换为默认配置，保存后才生效。' : dialog === 'read' ? '电影和剧集的本地修改会被文件内容替换。' : '电影和剧集的本地修改尚未保存，离开后会丢失。' }}</p>
      <template #footer><button class="cs-button" type="button" @click="decide(false)">取消</button><button class="cs-button cs-primary" type="button" @click="decide(true)">{{ dialog === 'reset' ? '恢复默认' : dialog === 'read' ? '重新读取' : '放弃并离开' }}</button></template>
    </P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.classification-settings { display: grid; gap: 18px; }.cs-toolbar, .cs-actions { display: flex; align-items: center; gap: 8px; }.cs-toolbar { justify-content: space-between; flex-wrap: wrap; gap: 12px; }.cs-kind { display: flex; gap: 3px; padding: 3px; border-radius: 10px; background: rgb(var(--color-slate-200) / .65); }.cs-kind button { min-height: 34px; padding: 6px 24px; border-radius: 7px; font-size: 13px; color: rgb(var(--color-slate-500)); }.cs-kind button[aria-pressed=true] { background: #fff; color: #1d4ed8; box-shadow: 0 1px 4px rgb(15 23 42 / .08); }.cs-button { display: inline-flex; justify-content: center; align-items: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: rgb(148 163 184 / .12); font-size: 12px; white-space: nowrap; color: rgb(var(--color-slate-700)); }.cs-primary { background: #2563eb; color: #fff; }.cs-button:hover:not(:disabled) { filter: brightness(.96); }.cs-button:disabled, .cs-kind button:disabled, .cs-read:disabled { opacity: .45; cursor: default; }.cs-status, .cs-feedback { font-size: 12px; color: rgb(var(--color-slate-500)); line-height: 1.8; }.cs-feedback { overflow-wrap: anywhere; }.cs-feedback-actions { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 10px; margin-top: 8px; }.cs-feedback-actions > span { color: rgb(var(--color-slate-400)); font-size: 11px; }.cs-read { display: inline-flex; align-items: center; gap: 6px; padding-block: 7px; color: #2563eb; }.cs-file { display: flex; align-items: center; justify-content: space-between; gap: 12px; }.cs-warning { font-size: 12px; line-height: 1.8; color: #92400e; background: #fffbeb; border-radius: 10px; padding: 12px; overflow-wrap: anywhere; }.cs-error { color: #b91c1c; }.classification-settings :focus-visible, .cs-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.dark .cs-kind { background: rgb(var(--color-slate-900)); }.dark .cs-kind button { color: rgb(var(--color-slate-400)); }.dark .cs-kind button[aria-pressed=true] { background: rgb(var(--color-slate-800)); color: #93c5fd; }.dark .cs-button:not(.cs-primary) { color: rgb(var(--color-slate-200)); }.dark .cs-status, .dark .cs-feedback { color: rgb(var(--color-slate-400)); }.dark .cs-read { color: #93c5fd; }.dark .cs-error { color: #fca5a5; }.dark .cs-warning { color: #fde68a; background: #451a03; }
@media(max-width: 639px) { .cs-toolbar { gap: 8px; }.cs-kind button { min-height: 40px; padding-inline: 12px; }.cs-button { min-height: 42px; padding-inline: 10px; }.cs-actions { gap: 5px; }.cs-file { align-items: flex-start; }.cs-read { min-height: 42px; } }
</style>
