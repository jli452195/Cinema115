import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { createED2KSTRMAPI } from '../lib/ed2k-strm-api'
import type { ED2KSTRMResource, ED2KSTRMSettings, ED2KSTRMState } from '../types/ed2kSTRM'
import { ed2kLinks, ed2kResourceActive, ed2kResourceKey, ED2K_IMPORT_LIMIT } from '../utils/ed2kSTRM'
import { validSTRMDirectory } from '../utils/strmDirectory'

export function useED2KSTRM(token: () => string) {
  const state = ref<ED2KSTRMState>(), draft = ref<ED2KSTRMSettings>(), saved = ref<ED2KSTRMSettings>()
  const loading = ref(true), refreshing = ref(false), busy = ref(''), error = ref(''), notice = ref(''), input = ref(''), query = ref('')
  const lifecycle = new AbortController(), api = createED2KSTRMAPI(token, lifecycle.signal)
  let timer: number | undefined, pending: Promise<void> | undefined, generation = 0
  const settingsDirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== JSON.stringify(saved.value))
  const needsSettingsSave = computed(() => !!draft.value && (!state.value?.settingsSaved || settingsDirty.value))
  const settingsValid = computed(() => !!draft.value?.baseUrl.trim() && validSTRMDirectory(draft.value.outputDir.trim()))
  const canStart = computed(() => !!state.value?.accountReady && state.value.settingsSaved)
  const links = computed(() => ed2kLinks(input.value))
  const canImport = computed(() => canStart.value && !settingsDirty.value && !busy.value && links.value.length > 0 && links.value.length <= ED2K_IMPORT_LIMIT)
  const resources = computed(() => {
    const needle = query.value.trim().toLocaleLowerCase()
    return (state.value?.resources || []).filter(resource => !needle || [resource.name, resource.hash, resource.sha1, resource.strmPath].some(value => value?.toLocaleLowerCase().includes(needle)))
  })
  const activeCount = computed(() => state.value?.resources.filter(ed2kResourceActive).length || 0)
  const readyCount = computed(() => state.value?.resources.filter(resource => !!resource.strmPath).length || 0)

  function acceptSettings(settings: ED2KSTRMSettings) {
    saved.value = { ...settings }
    draft.value = { ...settings }
  }
  async function refresh() {
    if (pending) return pending
    const current = generation
    refreshing.value = true
    pending = (async () => {
      try {
        const value = await api.state()
        if (lifecycle.signal.aborted || current !== generation) return
        const preserveDraft = settingsDirty.value
        state.value = { ...value, resources: value.resources || [] }
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
    timer = window.setTimeout(async () => { await refresh(); poll() }, activeCount.value ? 4000 : 30000)
  }
  async function act(key: string, operation: () => Promise<void>) {
    if (busy.value) return
    window.clearTimeout(timer)
    busy.value = key; error.value = ''; notice.value = ''; generation++
    await pending
    try { await operation() }
    catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'ED2K STRM 操作失败' }
    finally { busy.value = ''; poll() }
  }
  async function saveSettings() {
    if (!draft.value || !settingsValid.value || !needsSettingsSave.value) return
    const settings = { baseUrl: draft.value.baseUrl.trim(), outputDir: draft.value.outputDir.trim() }
    await act('settings', async () => {
      const value = await api.saveSettings(settings)
      if (lifecycle.signal.aborted) return
      acceptSettings(value)
      if (state.value) { state.value.settings = value; state.value.settingsSaved = true }
      notice.value = '设置已保存，后续任务使用此设置。'
      await refresh()
    })
  }
  function resetSettings() { if (saved.value) acceptSettings(saved.value) }
  async function importLinks() {
    if (!canImport.value) return
    const values = [...links.value]
    await act('imports', async () => {
      await api.imports(values)
      if (lifecycle.signal.aborted) return
      input.value = ''
      notice.value = `已接收 ${values.length} 条 ED2K，后台将核验文件并生成 STRM；同一资源会复用已有任务。`
      await refresh()
    })
  }
  async function resourceAction(resource: ED2KSTRMResource, action: 'check' | 'restore') {
    if (!canStart.value) return
    await act(`${action}:${ed2kResourceKey(resource)}`, async () => {
      await api.resource(resource, action)
      if (lifecycle.signal.aborted) return
      notice.value = action === 'restore' ? '已请求恢复缓存，后台将优先复用文件及秒传资料。' : '已请求核验缓存文件，结果会自动更新。'
      await refresh()
    })
  }
  async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
  onMounted(async () => { document.addEventListener('visibilitychange', visible); await refresh(); poll() })
  onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
  return { state, draft, loading, refreshing, busy, error, notice, input, query, links, resources, activeCount, readyCount, settingsDirty, needsSettingsSave, settingsValid, canStart, canImport, refresh, saveSettings, resetSettings, importLinks, resourceAction }
}
