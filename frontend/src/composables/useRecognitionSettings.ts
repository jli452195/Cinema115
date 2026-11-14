import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { portalUser } from './usePortalAuth'
import { recognitionRequest } from '../lib/recognition-api'
import type { RecognitionDiagnostic, RecognitionItem, RecognitionKind, RecognitionPreview, RecognitionRecord, RecognitionSettings, RecognitionTexts } from '../types/recognition'

export function useRecognitionSettings(token: () => string) {
  const kind = ref<RecognitionKind>('groups'), settings = shallowRef<RecognitionSettings>()
  const drafts = reactive<RecognitionTexts>({ groups: '', platforms: '' })
  const loading = ref(true), saving = ref(false), importing = ref(false), error = ref(''), notice = ref(''), diagnostic = ref<RecognitionDiagnostic>()
  const dialog = ref<'' | 'read' | 'clear' | 'import' | 'leave'>('')
  const imported = shallowRef<{ kind: RecognitionKind; text: string }>()
  const dirty = computed(() => !!settings.value && (drafts.groups !== settings.value.record.groups || drafts.platforms !== settings.value.record.platforms))
  const locked = computed(() => loading.value || saving.value || importing.value)
  const fileNotActive = computed(() => !!settings.value && settings.value.record.revision !== settings.value.record.activeRevision)
  const samples = ref(''), previewing = ref(false), previewError = ref(''), results = shallowRef<RecognitionItem[]>([])
  let lifecycle = new AbortController(), previewController = new AbortController(), previewSequence = 0, readSequence = 0
  let timer: ReturnType<typeof setTimeout> | undefined, leaveDecision: ((answer: boolean) => void) | undefined
  const message = (reason: unknown) => reason instanceof Error ? reason.message : '操作失败，请重试'
  const signal = () => AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
  const items = computed(() => samples.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean))
  const signature = () => JSON.stringify([drafts.groups, drafts.platforms, items.value])

  function failure(reason: unknown) {
    error.value = message(reason)
    diagnostic.value = (reason as { diagnostic?: RecognitionDiagnostic }).diagnostic
    if (diagnostic.value) kind.value = diagnostic.value.kind
  }
  function validRecord(record: RecognitionRecord | undefined): record is RecognitionRecord {
    return !!record && typeof record.groups === 'string' && typeof record.platforms === 'string' && typeof record.revision === 'string' && typeof record.activeRevision === 'string'
  }
  function accept(record: RecognitionRecord) {
    if (!validRecord(record)) throw new Error('词表读取结果无效，请重试')
    if (settings.value) settings.value = { ...settings.value, record }
    drafts.groups = record.groups; drafts.platforms = record.platforms
    diagnostic.value = record.diagnostic
  }
  async function read() {
    const session = lifecycle.signal, request = ++readSequence
    loading.value = true; error.value = ''; notice.value = ''
    try {
      const value = await recognitionRequest<RecognitionSettings>(token(), '', { signal: signal() })
      if (session.aborted || request !== readSequence) return
      if (!validRecord(value.record)) throw new Error('词表读取结果无效，请重试')
      settings.value = value; accept(value.record)
    } catch (reason) { if (!session.aborted && request === readSequence) failure(reason) }
    finally { if (!session.aborted && request === readSequence) { loading.value = false; schedulePreview() } }
  }
  function reread() { if (locked.value) return; if (dirty.value) dialog.value = 'read'; else void read() }
  async function activateFile() {
    if (locked.value || dirty.value) return
    const session = lifecycle.signal
    loading.value = true; error.value = ''; notice.value = ''
    try {
      const record = await recognitionRequest<RecognitionRecord>(token(), '/reload', { method: 'POST', signal: signal() })
      if (session.aborted) return
      accept(record); notice.value = '文件中的两个词表已生效'
    } catch (reason) { if (!session.aborted) failure(reason) }
    finally { if (!session.aborted) loading.value = false }
  }
  async function save() {
    if (locked.value || !settings.value || !dirty.value) return
    const payload = { groups: drafts.groups, platforms: drafts.platforms }
    const session = lifecycle.signal
    saving.value = true; error.value = ''; notice.value = ''; diagnostic.value = undefined
    try {
      const record = await recognitionRequest<RecognitionRecord>(token(), '', { method: 'PUT', body: JSON.stringify(payload), signal: signal() })
      if (session.aborted) return
      if (record.groups !== payload.groups || record.platforms !== payload.platforms || !record.revision || record.activeRevision !== record.revision) throw new Error('保存结果无效，请重试')
      accept(record); notice.value = '两个词表已保存并生效'
    } catch (reason) {
      if (session.aborted) return
      failure(reason)
    } finally { if (!session.aborted) saving.value = false }
  }
  function invalidatePreview() {
    clearTimeout(timer); previewController.abort(); previewController = new AbortController(); previewSequence++
    results.value = []; previewError.value = ''; previewing.value = false
  }
  function schedulePreview() {
    invalidatePreview()
    if (!settings.value || loading.value || !items.value.length) return
    if (items.value.length > 100) { previewError.value = '一次最多测试 100 行，请减少文件名后重试'; return }
    previewing.value = true
    timer = setTimeout(() => void runPreview(), 300)
  }
  async function runPreview() {
    clearTimeout(timer)
    if (!settings.value || loading.value || !items.value.length) { invalidatePreview(); return }
    if (items.value.length > 100) { previewing.value = false; previewError.value = '一次最多测试 100 行，请减少文件名后重试'; return }
    previewController.abort(); previewController = new AbortController()
    const request = ++previewSequence, current = signature(), session = lifecycle.signal
    const body = { groups: drafts.groups, platforms: drafts.platforms, items: [...items.value] }
    previewing.value = true; previewError.value = ''; results.value = []
    try {
      const value = await recognitionRequest<RecognitionPreview>(token(), '/preview', { method: 'POST', body: JSON.stringify(body), signal: AbortSignal.any([session, previewController.signal, AbortSignal.timeout(10000)]) })
      if (session.aborted || request !== previewSequence || current !== signature()) return
      if (!Array.isArray(value.items) || value.items.length !== body.items.length) throw new Error('识别结果无效，请重试')
      results.value = value.items.map(item => ({ ...item, releaseGroups: item.releaseGroups || [], platforms: item.platforms || [], matches: item.matches || [], conflicts: item.conflicts || [] }))
      diagnostic.value = undefined
    } catch (reason) {
      if (session.aborted || request !== previewSequence || current !== signature()) return
      previewError.value = message(reason)
      const value = (reason as { diagnostic?: RecognitionDiagnostic }).diagnostic
      if (value) diagnostic.value = value
    } finally { if (!session.aborted && request === previewSequence) previewing.value = false }
  }
  async function importFile(file: File) {
    if (locked.value) return
    const target = kind.value, session = lifecycle.signal
    importing.value = true; error.value = ''; notice.value = ''
    try {
      if (file.size > 256 * 1024) throw new Error('TXT 文件不能超过 256 KiB')
      const text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer()).replace(/^\uFEFF/, '')
      if (session.aborted) return
      imported.value = { kind: target, text }; dialog.value = 'import'
    } catch (reason) { if (!session.aborted) error.value = reason instanceof TypeError ? '文件不是有效的 UTF-8 文本，请转换编码后导入' : message(reason) }
    finally { if (!session.aborted) importing.value = false }
  }
  function exportFile() {
    const url = URL.createObjectURL(new Blob([drafts[kind.value]], { type: 'text/plain;charset=utf-8' })), anchor = document.createElement('a')
    anchor.href = url; anchor.download = kind.value === 'groups' ? 'release-groups.txt' : 'platforms.txt'; anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  function decide(answer: boolean) {
    const action = dialog.value; dialog.value = ''
    if (action === 'leave') { leaveDecision?.(answer); leaveDecision = undefined; return }
    if (!answer) { imported.value = undefined; return }
    if (action === 'read') void read()
    if (action === 'clear') { drafts[kind.value] = ''; notice.value = '当前词表已清空，保存后生效' }
    if (action === 'import' && imported.value) { drafts[imported.value.kind] = imported.value.text; kind.value = imported.value.kind; imported.value = undefined; notice.value = 'TXT 已载入草稿，保存后生效' }
  }
  function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value) { event.preventDefault(); event.returnValue = '' } }
  onBeforeRouteLeave(() => {
    if (saving.value || importing.value) { error.value = '操作尚未完成，请稍后离开'; return false }
    if (!dirty.value) return true
    leaveDecision?.(false); dialog.value = 'leave'
    return new Promise<boolean>(resolve => { leaveDecision = resolve })
  })
  watch([() => drafts.groups, () => drafts.platforms, samples], () => { diagnostic.value = undefined; notice.value = ''; schedulePreview() }, { flush: 'sync' })
  watch([() => portalUser.value?.id, () => portalUser.value?.role], () => {
    lifecycle.abort(); invalidatePreview(); lifecycle = new AbortController(); readSequence++
    settings.value = undefined; saving.value = false; loading.value = false; dialog.value = ''; imported.value = undefined; importing.value = false; diagnostic.value = undefined
    drafts.groups = ''; drafts.platforms = ''; results.value = []; error.value = ''; notice.value = ''; leaveDecision?.(false); leaveDecision = undefined
    if (portalUser.value?.role === 'admin') void read()
  })
  onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void read() })
  onBeforeUnmount(() => { lifecycle.abort(); invalidatePreview(); window.removeEventListener('beforeunload', beforeUnload); leaveDecision?.(false) })
  return { kind, settings, drafts, loading, saving, importing, error, notice, diagnostic, dialog, dirty, locked, fileNotActive, samples, previewing, previewError, results, items, reread, activateFile, save, runPreview, importFile, exportFile, decide }
}
