import { computed, ref } from 'vue'
import type { Mapping } from '../types/driveSTRM'
import type { DriveSTRMState } from './useDriveSTRMState'
import { directoryInput, initialSchedule, effectiveSchedule } from '../utils/driveSTRM'

export function useDriveSTRMEditor(remote: DriveSTRMState) {
  const { state, busy, dialogError, canStart, changedAccount, request, accounts, act, notice, refresh, invalidate } = remote
  const editor = ref<Mapping>(), deleting = ref<Mapping>()
  const sourceDialog = ref(false), outputDialog = ref(false)
  let snapshot = '', scheduleSnapshot = ''
  const directoryDirty = computed(() => !!editor.value && JSON.stringify(directoryInput(editor.value)) !== snapshot)
  const scheduleDirty = computed(() => !!editor.value && JSON.stringify(editor.value.schedule) !== scheduleSnapshot)
  const editorDirty = computed(() => directoryDirty.value || scheduleDirty.value)
  const editorRunning = computed(() => !!editor.value?.id && !!state.value?.mappings.some(m => m.id === editor.value!.id && (m.status === 'running' || m.queued)))
  const directoryReadOnly = computed(() => editorRunning.value || !canStart.value)
  const savesDirectory = computed(() => !!editor.value && (!editor.value.id || directoryDirty.value))
  const editorSaveLabel = computed(() => !editor.value?.id ? '保存目录' : directoryDirty.value ? scheduleDirty.value ? '保存更改' : '保存目录' : '保存自动任务')
  const canSaveEditor = computed(() => !!editor.value && (!editor.value.id || editorDirty.value) && (savesDirectory.value
    ? !directoryReadOnly.value && !!editor.value.name.trim() && !!editor.value.sourceCID && !!editor.value.outputDir && !changedAccount(editor.value)
    : scheduleDirty.value))
  function edit(m?: Mapping) {
    if (busy.value || !m && !canStart.value) return false
    if (editorDirty.value && !window.confirm('放弃未保存的修改？')) return false
    editor.value = m ? { ...m, stats: { ...m.stats } } : { id: '', name: '', accountId: state.value?.primaryAccountId || '', sourceCID: '', sourcePath: '', outputDir: '', enabled: true, status: 'idle', queued: false, schedule: initialSchedule(), metadataPending: 0, metadataFailed: 0, stats: { videos: 0, written: 0, unchanged: 0, metadataWritten: 0, failed: 0 } }
    editor.value.schedule = effectiveSchedule(editor.value)
    snapshot = JSON.stringify(directoryInput(editor.value)); scheduleSnapshot = JSON.stringify(editor.value.schedule)
    dialogError.value = ''
    return true
  }

  function closeEditor() { if (!busy.value && (!editorDirty.value || window.confirm('放弃未保存的修改？'))) editor.value = undefined }

  async function selectSource() {
    if (directoryReadOnly.value) return
    await act('source', async () => {
      const data = await accounts()
      const primary = data.accounts.find(a => a.primary && a.configured)
      if (!primary) throw new Error('请先在 115 助手中配置并登录主账号')
      if (editor.value) {
        if (editor.value.accountId !== primary.id) { editor.value.sourceCID = ''; editor.value.sourcePath = '' }
        editor.value.accountId = primary.id
      }
      sourceDialog.value = true
    }, true)
  }

  function pickedSource(value: { id: string; path: string; name: string }) {
    if (editor.value) { editor.value.sourceCID = value.id; editor.value.sourcePath = value.path; if (!editor.value.name) editor.value.name = value.name }
    sourceDialog.value = false
  }

  async function save() {
    if (!canSaveEditor.value || !editor.value) return
    await act('save', async () => {
      const m = editor.value!
      const directory = savesDirectory.value
      if (directory) {
        await request(m.id ? '/' + m.id : '', { method: m.id ? 'PUT' : 'POST', body: JSON.stringify({ ...directoryInput(m), ...(!m.id || scheduleDirty.value ? { schedule: { ...m.schedule } } : {}) }) })
      } else {
        await request('/' + m.id + '/schedule', { method: 'PUT', body: JSON.stringify({ schedule: { ...m.schedule } }) })
      }
      invalidate(); editor.value = undefined
      notice.value = directory ? '目录设置已保存；新目录或来源变更将完整扫描，自动任务按各自周期执行。' : '目录自动任务已保存；后续按新周期执行，当前任务会继续完成。'
      await refresh()
    }, true)
  }

  async function remove() {
    await act('delete', async () => { await request('/' + deleting.value!.id, { method: 'DELETE' }); invalidate(); deleting.value = undefined; notice.value = '目录配置已删除，本地与网盘文件均已保留'; await refresh() }, true)
  }

  function requestDelete(m: Mapping) {
    deleting.value = m; dialogError.value = ''
  }
  return {
    editor, deleting, sourceDialog, outputDialog, editorDirty, editorRunning,
    directoryReadOnly, editorSaveLabel, canSaveEditor, edit, closeEditor, selectSource, pickedSource,
    save, remove, requestDelete,
  }
}
