<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconRefresh, IconRestore, IconDeviceFloppy } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import NamingTemplateEditor from '../components/NamingTemplateEditor.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import SelectMenu from '../components/SelectMenu.vue'
import { namingRequest } from '../lib/naming-api'
import { portalUser } from '../composables/usePortalAuth'
import { cloneNamingTemplate, namingSample, namingSignature } from '../utils/namingTemplate'
import type { NamingKind, NamingPreview, NamingRecord, NamingResult, NamingSettings, NamingTemplate } from '../types/naming'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const kind = computed<NamingKind>(() => route.query.kind === 'tv' ? 'tv' : 'movie')
const kindLabel = computed(() => kind.value === 'movie' ? '电影' : '剧集')
const settings = ref<NamingSettings>()
const drafts = ref<Record<NamingKind, NamingTemplate>>({ movie: { kind: 'movie', directories: [], filename: [] }, tv: { kind: 'tv', directories: [], filename: [] } })
const current = computed({ get: () => drafts.value[kind.value], set: value => { drafts.value[kind.value] = value } })
const loading = ref(true), saving = ref(false), error = ref(''), notice = ref('')
const pending = ref<{ kind: NamingKind; body: { revision: number; template: NamingTemplate } }>()
const locked = computed(() => loading.value || saving.value || !!pending.value)
const dirty = (value: NamingKind) => !!settings.value && namingSignature(drafts.value[value]) !== namingSignature(settings.value.records[value].template)
const anyDirty = computed(() => dirty('movie') || dirty('tv'))
const sample = ref('full'), preview = ref<NamingResult>(), previewError = ref(''), previewing = ref(false), templateValid = ref(false)
const sampleOptions = computed(() => [{ value: 'full', label: '完整规格示例' }, { value: 'minimal', label: '缺少规格与外文名' }, ...(kind.value === 'tv' ? [{ value: 'range', label: '连集示例' }] : [])])
const dialog = ref<'leave' | 'reload' | 'reset' | ''>('')
let lifecycle = new AbortController(), previewController = new AbortController(), sequence = 0, readSequence = 0
let previewTimer: ReturnType<typeof setTimeout> | undefined, leaveDecision: ((value: boolean) => void) | undefined
let previewKind: NamingKind | undefined
const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])

async function read(initial = false) {
  const target = kind.value, request = ++readSequence
  loading.value = true; error.value = ''; notice.value = ''
  try {
    const value = await namingRequest<NamingSettings>(props.token, '', { signal: signal() })
    if (lifecycle.signal.aborted || request !== readSequence) return
    if (initial || !settings.value) {
      settings.value = value
      drafts.value = { movie: cloneNamingTemplate(value.records.movie.template), tv: cloneNamingTemplate(value.records.tv.template) }
    } else {
      settings.value.records[target] = value.records[target]
      settings.value.catalog = value.catalog; settings.value.defaults = value.defaults
      drafts.value[target] = cloneNamingTemplate(value.records[target].template)
    }
  } catch (reason) { if (!lifecycle.signal.aborted && request === readSequence) error.value = message(reason) }
  finally { if (!lifecycle.signal.aborted && request === readSequence) { loading.value = false; schedulePreview() } }
}
function chooseKind(value: NamingKind) {
  if (locked.value) return
  void router.replace({ query: { ...route.query, kind: value === 'tv' ? 'tv' : undefined } })
}
function schedulePreview() {
  const request = ++sequence
  clearTimeout(previewTimer); previewController.abort(); previewController = new AbortController()
  if (previewKind !== kind.value) preview.value = undefined
  previewError.value = ''; templateValid.value = false
  previewing.value = !!settings.value && !loading.value
  if (!previewing.value) return
  const template = cloneNamingTemplate(current.value), data = namingSample(kind.value, sample.value)
  const requestSignal = AbortSignal.any([lifecycle.signal, previewController.signal, AbortSignal.timeout(10000)])
  previewTimer = setTimeout(async () => {
    try {
      const value = await namingRequest<NamingPreview>(props.token, '/preview', { method: 'POST', body: JSON.stringify({ template, items: [data] }), signal: requestSignal })
      if (request !== sequence || lifecycle.signal.aborted) return
      const item = value.items?.[0]
      if (value.items?.length !== 1 || !item || !item.result && !item.error) throw new Error('预览结果无效，请重试')
      templateValid.value = true; preview.value = item.result; previewKind = template.kind; previewError.value = item.error || ''
    } catch (reason) { if (request === sequence && !lifecycle.signal.aborted) { preview.value = undefined; previewError.value = message(reason) } }
    finally { if (request === sequence && !lifecycle.signal.aborted) previewing.value = false }
  }, 300)
}
async function save() {
  if (locked.value || !settings.value || !dirty(kind.value) || !templateValid.value || previewing.value) return
  pending.value = { kind: kind.value, body: { revision: settings.value.records[kind.value].revision, template: cloneNamingTemplate(current.value) } }
  await confirmSave()
}
async function confirmSave() {
  const request = pending.value
  if (!request || saving.value) return
  const session = lifecycle.signal
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const value = await namingRequest<NamingRecord>(props.token, `/${request.kind}`, { method: 'PUT', body: JSON.stringify(request.body), signal: signal() })
    if (session.aborted || pending.value !== request) return
    if (value.revision !== request.body.revision + 1 || value.template?.kind !== request.kind || namingSignature(value.template) !== namingSignature(request.body.template)) throw new Error('保存结果尚未确认，请确认原请求')
    settings.value!.records[request.kind] = value; drafts.value[request.kind] = cloneNamingTemplate(value.template)
    pending.value = undefined; notice.value = `${request.kind === 'movie' ? '电影' : '剧集'}模板已保存`
  } catch (reason) {
    if (session.aborted || pending.value !== request) return
    error.value = message(reason)
    const status = (reason as { status?: number })?.status
    if (status && status >= 400 && status < 500 && status !== 408 && status !== 429) pending.value = undefined
  } finally { if (!session.aborted) saving.value = false }
}
function reload() { if (!locked.value) dirty(kind.value) ? dialog.value = 'reload' : void read() }
function decide(value: boolean) {
  const action = dialog.value; dialog.value = ''
  if (action === 'leave') { leaveDecision?.(value); leaveDecision = undefined }
  if (value && action === 'reload') void read()
  if (value && action === 'reset' && settings.value) { current.value = cloneNamingTemplate(settings.value.defaults[kind.value]); notice.value = '已恢复默认规则，保存后更新模板'; error.value = '' }
}
function beforeUnload(event: BeforeUnloadEvent) { if (anyDirty.value || pending.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteUpdate(() => !locked.value)
onBeforeRouteLeave(() => {
  if (saving.value || pending.value) { error.value = '保存结果尚未确认，请先确认原请求'; return false }
  if (!anyDirty.value) return true
  dialog.value = 'leave'; return new Promise<boolean>(resolve => { leaveDecision = resolve })
})
watch([() => namingSignature(current.value), sample, kind], () => { if (settings.value) schedulePreview() }, { flush: 'sync' })
watch(kind, () => { if (sample.value === 'range' && kind.value === 'movie') sample.value = 'full'; error.value = ''; notice.value = '' })
watch([() => portalUser.value?.id, () => portalUser.value?.role], () => {
  lifecycle.abort(); previewController.abort(); clearTimeout(previewTimer); sequence++; readSequence++
  lifecycle = new AbortController(); pending.value = undefined; saving.value = false; settings.value = undefined
  void read(true)
})
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void read(true) })
onBeforeUnmount(() => { lifecycle.abort(); previewController.abort(); clearTimeout(previewTimer); window.removeEventListener('beforeunload', beforeUnload); leaveDecision?.(false) })
</script>

<template>
  <ManagementLayout title="命名模板" description="统一设置电影和剧集的目录、文件名规则，供不同存储与整理功能使用。" @logout="$emit('logout')">
    <div class="naming-settings">
      <header class="ns-toolbar">
        <div class="ns-kind" role="group" aria-label="模板类型"><button v-for="option in [{ value: 'movie' as const, label: '电影' }, { value: 'tv' as const, label: '剧集' }]" :key="option.value" type="button" :disabled="locked" :aria-pressed="kind === option.value" :aria-label="`${option.label}${dirty(option.value) ? '，有未保存修改' : ''}`" @click="chooseKind(option.value)">{{ option.label }}<span v-if="dirty(option.value)" class="ns-dirty" aria-hidden="true"></span></button></div>
        <div class="ns-actions"><button type="button" class="ns-button" :disabled="locked || !settings" @click="dialog = 'reset'"><IconRestore :size="15" aria-hidden="true" />恢复默认</button><button type="button" class="ns-button ns-save" :disabled="locked || !dirty(kind) || !templateValid || previewing" @click="save"><IconDeviceFloppy :size="15" aria-hidden="true" />{{ saving ? '保存中…' : '保存模板' }}</button></div>
      </header>
      <p v-if="loading" class="ns-feedback" role="status">正在读取命名模板…</p>
      <template v-if="settings">
        <NamingTemplateEditor :key="kind" v-model="current" :fields="settings.catalog[kind]" :defaults="settings.defaults[kind]" :disabled="locked" />
        <section class="ns-preview" aria-label="命名预览" :aria-busy="previewing">
          <header><div><h2>路径预览</h2><span>{{ previewing ? '正在生成预览…' : '示例数据' }}</span></div><SelectMenu v-model="sample" :options="sampleOptions" label="预览样例" :disabled="locked" /></header>
          <div class="ns-preview-content" tabindex="0" aria-label="完整目标路径"><code v-if="preview">{{ preview.relativePath }}</code><p v-else-if="previewError" class="ns-preview-error" role="status">{{ previewError }}</p><p v-else class="ns-placeholder">正在生成{{ kindLabel }}命名预览…</p></div>
          <button v-if="previewError && !previewing" type="button" class="ns-preview-retry" @click="schedulePreview"><IconRefresh :size="13" aria-hidden="true" />重新预览</button>
        </section>
      </template>
      <div class="ns-feedback" role="status" aria-live="polite"><p v-if="error">{{ error }}</p><p v-else-if="notice">{{ notice }}</p><p v-else-if="settings">{{ dirty(kind) ? '当前模板有未保存的修改' : '当前模板已保存' }}</p><div class="ns-feedback-actions"><button v-if="pending" type="button" class="ns-button" :disabled="saving" @click="confirmSave">确认保存结果</button><button v-else-if="!loading" type="button" class="ns-reload" @click="reload"><IconRefresh :size="13" aria-hidden="true" />重新读取{{ settings ? '当前模板' : '设置' }}</button></div></div>
      <p class="ns-stage">手动整理、下载整理使用已保存的模板；已创建的任务保持原命名。动漫订阅将在后续步骤接入。</p>
    </div>
    <P115ActionDialog v-if="dialog" :title="dialog === 'leave' ? '放弃未保存的模板修改？' : dialog === 'reload' ? `重新读取${kindLabel}模板？` : `恢复${kindLabel}默认模板？`" @close="decide(false)"><p>{{ dialog === 'leave' ? '电影或剧集模板仍有未保存的修改，离开后将丢弃这些修改。' : dialog === 'reload' ? '当前类型的未保存修改会被替换，另一类型的编辑内容将保留。' : '仅替换当前类型的编辑内容，保存后才会更新服务端模板。' }}</p><template #footer><button type="button" class="ns-button" @click="decide(false)">继续编辑</button><button type="button" class="ns-button ns-save" @click="decide(true)">{{ dialog === 'leave' ? '放弃修改并离开' : dialog === 'reload' ? '重新读取' : '恢复默认' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.naming-settings { min-width: 0; }.ns-toolbar { display: flex; justify-content: space-between; gap: 14px; align-items: center; margin-bottom: 22px; }.ns-kind { display: flex; gap: 4px; padding: 4px; border-radius: 11px; background: rgb(var(--color-slate-200) / .45); }.ns-kind button { display: inline-flex; justify-content: center; align-items: center; gap: 8px; min-width: 76px; min-height: 36px; padding: 6px 15px; font-size: 13px; border-radius: 8px; color: rgb(var(--color-slate-500)); }.ns-kind button[aria-pressed=true] { background: #fff; color: #2563eb; font-weight: 600; box-shadow: 0 1px 5px rgb(15 23 42 / .06); }.ns-dirty { width: 5px; height: 5px; border-radius: 50%; background: #3b82f6; }.ns-actions { display: flex; gap: 8px; }.ns-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: rgb(var(--color-slate-200) / .55); color: rgb(var(--color-slate-700)); font-size: 12px; }.ns-save { color: #fff; background: #2563eb; }.ns-button:hover { filter: brightness(.97); }.ns-button:disabled, .ns-kind button:disabled { opacity: .45; cursor: default; }.ns-preview { position: sticky; bottom: 12px; margin-top: 20px; padding: 14px 17px; border-radius: 13px; background: #fff; box-shadow: 0 3px 24px rgb(15 23 42 / .08); z-index: 4; }.ns-preview > header, .ns-preview > header > div { display: flex; align-items: center; gap: 12px; }.ns-preview > header { justify-content: space-between; gap: 12px; }.ns-preview h2 { font-size: 12px; font-weight: 600; color: rgb(var(--color-slate-700)); }.ns-preview header span { font-size: 11px; color: rgb(var(--color-slate-500)); }.ns-preview :deep([role=combobox]) { min-height: 32px; height: 32px; font-size: 11px; border-color: transparent; background: rgb(var(--color-slate-100)); }.ns-preview-content { margin-top: 12px; overflow-x: auto; padding-bottom: 5px; font-size: 12px; line-height: 1.9; color: rgb(var(--color-slate-700)); }.ns-preview-content code { white-space: pre; font-family: ui-monospace, monospace; }.ns-preview-error { color: #b91c1c; overflow-wrap: anywhere; }.ns-placeholder { color: rgb(var(--color-slate-400)); }.ns-preview-retry, .ns-reload { display: inline-flex; align-items: center; gap: 5px; min-height: 32px; font-size: 11px; color: rgb(var(--color-slate-500)); }.ns-feedback { margin-top: 16px; font-size: 12px; line-height: 1.8; color: rgb(var(--color-slate-500)); overflow-wrap: anywhere; }.ns-feedback-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 4px; }.ns-stage { margin-top: 8px; font-size: 11px; line-height: 1.8; color: rgb(var(--color-slate-400)); }.naming-settings :focus-visible, .ns-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 3px; }
.dark .ns-kind { background: rgb(var(--color-slate-900)); }.dark .ns-kind button[aria-pressed=true] { background: rgb(var(--color-slate-800)); color: #93c5fd; }.dark .ns-button { background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-200)); }.dark .ns-save { background: #2563eb; color: #fff; }.dark .ns-preview { background: rgb(var(--color-slate-900)); box-shadow: 0 3px 24px rgb(0 0 0 / .2); }.dark .ns-preview h2, .dark .ns-preview-content { color: rgb(var(--color-slate-200)); }.dark .ns-preview :deep([role=combobox]) { background: rgb(var(--color-slate-800)); }.dark .ns-preview-error { color: #fca5a5; }
.dark .ns-feedback, .dark .ns-stage, .dark .ns-reload, .dark .ns-preview-retry, .dark .ns-preview header span { color: rgb(var(--color-slate-400)); }
@media(max-width: 639px) { .ns-toolbar { flex-wrap: wrap; margin-bottom: 16px; gap: 12px; }.ns-kind button { min-height: 40px; min-width: 64px; padding-inline: 12px; }.ns-actions { margin-left: auto; }.ns-button { min-height: 42px; padding-inline: 9px; font-size: 11px; }.ns-preview { position: static; padding: 12px; }.ns-preview > header { flex-wrap: wrap; gap: 8px; }.ns-preview > header > div { gap: 6px; flex-wrap: wrap; }.ns-preview :deep([role=combobox]) { min-height: 40px; }.ns-preview header span { font-size: 10px; }.ns-preview-content { font-size: 11px; }.ns-preview-retry, .ns-reload { min-height: 42px; } }
</style>
