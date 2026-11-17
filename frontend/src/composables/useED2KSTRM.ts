import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { createED2KSTRMAPI } from '../lib/ed2k-strm-api'
import type { ED2KSTRMResource, ED2KSTRMSettings, ED2KSTRMState, ED2KSTRMTarget, ED2KSTRMTask } from '../types/ed2kSTRM'
import { ed2kLinks, ed2kRequestID, ed2kResourceActive, ed2kResourceKey, ed2kResourceLink, ED2K_IMPORT_LIMIT } from '../utils/ed2kSTRM'
import { validSTRMDirectory } from '../utils/strmDirectory'

export function useED2KSTRM(token: () => string) {
  const state = ref<ED2KSTRMState>(), draft = ref<ED2KSTRMSettings>(), saved = ref<ED2KSTRMSettings>()
  const loading = ref(true), refreshing = ref(false), busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), input = ref(''), query = ref(''), targetId = ref('')
  const editor = ref<{ id?: string; name: string; outputDir: string }>(), deleting = ref<ED2KSTRMTarget>()
  const lifecycle = new AbortController(), api = createED2KSTRMAPI(token, lifecycle.signal)
  let timer: number | undefined, pending: Promise<void> | undefined, generation = 0
  let importAttempt: { key: string; requestId: string } | undefined
  const settingsDirty = computed(() => !!draft.value && (['baseUrl', 'cacheAccountId', 'cacheCID', 'cachePath'] as const).some(key => (draft.value?.[key] || '') !== (saved.value?.[key] || '')))
  const needsSettingsSave = computed(() => !!draft.value && (!state.value?.settingsSaved || settingsDirty.value))
  const cacheDirectoryInvalid = computed(() => !!draft.value?.cacheCID && (!state.value?.accountReady || !state.value.primaryAccountId || draft.value.cacheAccountId !== state.value.primaryAccountId))
  const settingsValid = computed(() => !!draft.value?.baseUrl.trim() && !cacheDirectoryInvalid.value)
  const canStart = computed(() => !!state.value?.accountReady && state.value.settingsSaved)
  const canRestore = computed(() => canStart.value && state.value?.cacheReady !== false)
  const links = computed(() => ed2kLinks(input.value))
  const selectedTarget = computed(() => state.value?.targets.find(target => target.id === targetId.value))
  const targetOptions = computed(() => state.value?.targets.map(target => ({ value: target.id, label: target.name })) || [])
  const canImport = computed(() => canRestore.value && !!selectedTarget.value && !settingsDirty.value && !busy.value && links.value.length > 0 && links.value.length <= ED2K_IMPORT_LIMIT)
  const canSaveTarget = computed(() => !!editor.value?.name.trim() && validSTRMDirectory(editor.value.outputDir.trim()))
  const resources = computed(() => {
    const needle = query.value.trim().toLocaleLowerCase()
    return (state.value?.resources || []).filter(resource => !needle || [resource.name, resource.hash, resource.sha1].some(value => value?.toLocaleLowerCase().includes(needle)))
  })
  const active = computed(() => !!state.value?.tasks.some(task => task.status !== 'failed') || !!state.value?.resources.some(ed2kResourceActive))

  function acceptSettings(settings: ED2KSTRMSettings) { saved.value = { ...settings }; draft.value = { ...settings } }
  async function refresh() {
    if (pending) return pending
    const current = generation
    refreshing.value = true
    pending = (async () => {
      try {
        const value = await api.state()
        if (lifecycle.signal.aborted || current !== generation) return
        const preserveDraft = settingsDirty.value
        state.value = value
        if (!value.targets.some(target => target.id === targetId.value)) targetId.value = value.targets.length === 1 ? value.targets[0]!.id : ''
        if (!preserveDraft) acceptSettings(value.settings)
        else saved.value = { ...value.settings }
        error.value = ''
      } catch (reason) {
        if (!lifecycle.signal.aborted && current === generation) error.value = reason instanceof Error ? reason.message : '读取 ED2K STRM 失败'
      } finally {
        if (!lifecycle.signal.aborted) { loading.value = false; refreshing.value = false }
      }
    })().finally(() => { pending = undefined })
    return pending
  }
  function poll() {
    window.clearTimeout(timer)
    if (lifecycle.signal.aborted || document.hidden || busy.value) return
    timer = window.setTimeout(async () => { await refresh(); poll() }, active.value ? 4000 : 30000)
  }
  async function act(key: string, operation: () => Promise<void>, inDialog = false) {
    if (busy.value) return
    window.clearTimeout(timer)
    busy.value = key; error.value = ''; dialogError.value = ''; notice.value = ''; generation++
    await pending
    try { await operation() }
    catch (reason) { if (!lifecycle.signal.aborted) (inDialog ? dialogError : error).value = reason instanceof Error ? reason.message : 'ED2K STRM 操作失败' }
    finally { busy.value = ''; poll() }
  }
  async function saveSettings() {
    if (!draft.value || !settingsValid.value || !needsSettingsSave.value) return
    const settings: ED2KSTRMSettings = {
      baseUrl: draft.value.baseUrl.trim(),
      cacheAccountId: draft.value.cacheCID ? draft.value.cacheAccountId : '',
      cacheCID: draft.value.cacheCID || '',
      cachePath: draft.value.cacheCID ? draft.value.cachePath : '',
    }
    await act('settings', async () => {
      const value = await api.saveSettings(settings)
      if (lifecycle.signal.aborted) return
      acceptSettings(value)
      if (state.value) { state.value.settings = value; state.value.settingsSaved = true }
      notice.value = '基础设置已保存。'
      await refresh()
    })
  }
  function resetSettings() { if (saved.value) acceptSettings(saved.value) }
  function selectCacheDirectory(accountId: string, directory: { id: string; path: string }) {
    if (busy.value || !draft.value) return
    if (!state.value?.accountReady || state.value.primaryAccountId !== accountId) {
      error.value = '主账号已变化，请重新选择缓存目录。'
      return
    }
    draft.value = { ...draft.value, cacheAccountId: accountId, cacheCID: directory.id, cachePath: directory.path }
  }
  function clearCacheDirectory() {
    if (!busy.value && draft.value) draft.value = { ...draft.value, cacheAccountId: '', cacheCID: '', cachePath: '' }
  }
  function editTarget(target?: ED2KSTRMTarget) {
    if (busy.value) return
    dialogError.value = ''
    editor.value = target ? { id: target.id, name: target.name, outputDir: target.outputDir } : { name: '', outputDir: '' }
  }
  function closeEditor() { if (!busy.value) { editor.value = undefined; dialogError.value = '' } }
  async function saveTarget() {
    if (!editor.value || !canSaveTarget.value) return
    const value = { ...editor.value, name: editor.value.name.trim(), outputDir: editor.value.outputDir.trim() }
    await act('target', async () => {
      const target = await api.saveTarget({ name: value.name, outputDir: value.outputDir }, value.id)
      if (lifecycle.signal.aborted) return
      editor.value = undefined; targetId.value = target.id
      notice.value = '输出目录已保存，可在导入时选择，也可在 Telegram 配置中分配给 Bot。'
      await refresh()
    }, true)
  }
  function requestDelete(target: ED2KSTRMTarget) { if (!busy.value) { dialogError.value = ''; deleting.value = target } }
  async function deleteTarget() {
    if (!deleting.value) return
    const id = deleting.value.id
    await act('delete-target', async () => {
      await api.deleteTarget(id)
      if (lifecycle.signal.aborted) return
      deleting.value = undefined
      notice.value = '输出目录配置已删除。已接受的任务继续处理，已有文件与资源保留。'
      await refresh()
    }, true)
  }
  async function importLinks() {
    if (!canImport.value) return
    const values = [...links.value], selected = targetId.value
    const key = JSON.stringify([selected, values])
    if (importAttempt?.key !== key) importAttempt = { key, requestId: ed2kRequestID() }
    const requestId = importAttempt.requestId
    await act('imports', async () => {
      await api.imports({ links: values, targetId: selected, requestId })
      if (lifecycle.signal.aborted) return
      importAttempt = undefined; input.value = ''
      notice.value = `已接收 ${values.length} 条链接，文件资料就绪后写入所选目录。`
      await refresh()
    })
  }
  function prepareResource(resource: ED2KSTRMResource) {
    input.value = ed2kResourceLink(resource)
    notice.value = '已填入资源链接，请选择输出目录并提交生成。'
  }
  async function resourceAction(resource: ED2KSTRMResource, action: 'check' | 'restore') {
    if (action === 'restore' ? !canRestore.value : !canStart.value) return
    await act(`${action}:${ed2kResourceKey(resource)}`, async () => {
      await api.resource(resource, action)
      if (lifecycle.signal.aborted) return
      notice.value = action === 'restore' ? '已请求恢复缓存，后台将优先复用文件及秒传资料。' : '已请求核验缓存文件。'
      await refresh()
    })
  }
  async function taskAction(task: ED2KSTRMTask, action: 'retry' | 'delete') {
    if (task.status !== 'failed') return
    await act(`${action}-task:${task.id}`, async () => {
      if (action === 'retry') await api.retryTask(task.id)
      else await api.deleteTask(task.id)
      if (!lifecycle.signal.aborted) await refresh()
    })
  }
  async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
  onMounted(async () => { document.addEventListener('visibilitychange', visible); await refresh(); poll() })
  onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
  return { state, draft, loading, refreshing, busy, error, dialogError, notice, input, query, targetId, selectedTarget, targetOptions, editor, deleting, links, resources, settingsDirty, needsSettingsSave, settingsValid, cacheDirectoryInvalid, canStart, canRestore, canImport, canSaveTarget, refresh, saveSettings, resetSettings, selectCacheDirectory, clearCacheDirectory, editTarget, closeEditor, saveTarget, requestDelete, deleteTarget, importLinks, prepareResource, resourceAction, taskAction }
}
