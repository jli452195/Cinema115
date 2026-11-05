import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Mapping, Settings, State } from '../types/driveSTRM'
import { createDriveSTRMAPI } from '../lib/drive-strm-api'
import { active, pendingMetadata, downloadingMetadata, date, effectiveSchedule, intervalLabel } from '../utils/driveSTRM'

export function useDriveSTRMState(token: () => string) {
  const state = ref<State>()
  const settingsDraft = ref<Settings>(), savedSettings = ref<Settings>()
  const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true)
  const lifecycle = new AbortController()
  let timer: number | undefined, pendingRefresh: Promise<void> | undefined, generation = 0
  const { request, accounts } = createDriveSTRMAPI(token, lifecycle.signal)
  const invalidate = () => { generation++ }
  const attachmentCooling = computed(() => Date.parse(state.value?.attachments?.coolingUntil || '') > Date.now())
  const attachmentMessage = computed(() => !state.value?.mappings.some(pendingMetadata) ? '' : attachmentCooling.value
    ? `附件下载已暂缓，预计 ${date(state.value?.attachments?.coolingUntil)} 后先尝试一个附件，成功后恢复。${state.value?.attachments?.lastError || ''}`
    : state.value?.attachments?.probing ? '附件下载正在尝试恢复，成功后继续处理待补文件。' : '')
  const settingsDirty = computed(() => JSON.stringify(settingsDraft.value) !== JSON.stringify(savedSettings.value))
  const needsSettingsSave = computed(() => !!settingsDraft.value && (!state.value?.settingsSaved || settingsDirty.value))
  const settingsReady = computed(() => !!state.value?.settingsSaved && !settingsDirty.value)
  const hasSchedule = computed(() => !!state.value && (state.value.mappings.length
    ? state.value.mappings.some(mappingHasSchedule)
    : state.value.settings.eventIntervalSeconds > 0))
  const canStart = computed(() => settingsReady.value && !!state.value?.accountReady)
  const startHint = computed(() => !state.value?.settingsSaved ? '请先保存基础设置' : settingsDirty.value ? '请先保存或还原基础设置的修改' : !state.value?.accountReady ? '请先配置并登录主账号' : undefined)
  const strmPreview = computed(() => (settingsDraft.value?.baseUrl.trim().replace(/\/+$/, '') || 'http://NAS_IP:8080') + '/api/drive115/ecjq9ichcb40lzlvx/123456789/0123456789ABCDEF0123456789ABCDEF01234567/1073741824/Example.S01E01.mkv')
  const changedAccount = (m: Mapping) => !!state.value && m.accountId !== state.value.primaryAccountId
  const eventConsumerErrors = computed(() => Object.entries(state.value?.events?.consumers || {}).filter(([, value]) => value.lastError).map(([name, value]) => ({ name, label: name === 'strm' ? 'STRM' : name === 'organize' ? '自动整理' : name, message: value.lastError })))
  const failure = (e: unknown) => e instanceof Error && e.name === 'TimeoutError' ? '请求超时，请重新加载确认结果后再重试' : e instanceof Error ? e.message : '操作失败'
  function acceptSettings(value: Settings) {
    const settings = { ...value }
    settings.metadataImageMode ||= 'cdn'
    settings.metadataConcurrency ??= 4
    settings.metadataBatchIntervalSeconds ??= 3
    savedSettings.value = { ...settings }; settingsDraft.value = settings
  }
  function refresh(): Promise<void> {
    if (pendingRefresh) return pendingRefresh
    if (document.hidden || lifecycle.signal.aborted) return Promise.resolve()
    pendingRefresh = (async () => {
      while (!lifecycle.signal.aborted) {
        const version = generation
        try {
          const next = await request<State>()
          if (version === generation) {
            if (!settingsDraft.value || !settingsDirty.value) acceptSettings(next.settings)
            state.value = next; error.value = ''; return
          }
        } catch (e) {
          if (version === generation) { if (!lifecycle.signal.aborted) error.value = failure(e); return }
        }
      }
    })().finally(() => { pendingRefresh = undefined; loading.value = false })
    return pendingRefresh
  }
  function poll() {
    window.clearTimeout(timer)
    if (lifecycle.signal.aborted || document.hidden) return
    const running = state.value?.mappings.some(m => active(m) || downloadingMetadata(m))
    if (!running && !state.value?.mappings.some(m => m.enabled || pendingMetadata(m))) return
    timer = window.setTimeout(async () => { await refresh(); poll() }, running ? 4000 : 30000)
  }
  async function act(key: string, fn: () => Promise<void>, dialog = false) {
    if (busy.value) return
    window.clearTimeout(timer)
    busy.value = key; (dialog ? dialogError : error).value = ''; notice.value = ''
    try { await fn() } catch (e) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = failure(e) }
    finally { busy.value = ''; poll() }
  }
  async function saveSettings() {
    if (!needsSettingsSave.value) return
    await act('settings', async () => {
      const input = { ...settingsDraft.value! }
      const settings = await request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(input) })
      generation++; acceptSettings(settings)
      if (state.value) { state.value.settings = settings; state.value.settingsSaved = true }
      notice.value = '基础设置已保存；图片策略用于后续任务，降低并发或延长取链间隔也会约束待补附件。已有 STRM 可通过完整扫描更新。'
      await refresh()
    })
  }
  function resetSettings() { if (savedSettings.value) acceptSettings(state.value?.settings || savedSettings.value) }
  function mappingHasSchedule(m: Mapping) {
    const schedule = effectiveSchedule(m)
    return state.value!.settings.eventIntervalSeconds > 0 || schedule.reconcileEnabled || schedule.fullScanEnabled
  }

  function eventLabel() {
    const seconds = state.value!.settings.eventIntervalSeconds
    return seconds === 0 ? '事件已关闭' : seconds < 60 ? `事件每 ${seconds} 秒` : `事件每 ${seconds / 60} 分钟`
  }

  function scheduleLabel(m: Mapping, type: 'reconcile' | 'fullScan') {
    const schedule = effectiveSchedule(m)
    return schedule[type === 'reconcile' ? 'reconcileEnabled' : 'fullScanEnabled']
      ? intervalLabel(schedule[type === 'reconcile' ? 'reconcileIntervalHours' : 'fullScanIntervalHours']) : '关闭'
  }

  async function run(m: Mapping, full: boolean) {
    if (!canStart.value) return
    await act(m.id, async () => { await request('/' + m.id + (full ? '/scan' : '/check'), { method: 'POST' }); generation++; notice.value = full ? '已安排完整扫描，将补齐本地缺少的文件' : '已请求检查生活事件，变更将由后台处理'; await refresh() })
  }

  async function toggle(m: Mapping, enabled: boolean) {
    if (enabled && !canStart.value) return
    await act(m.id, async () => { await request('/' + m.id + '/monitor', { method: 'PUT', body: JSON.stringify({ enabled }) }); generation++; notice.value = enabled ? '自动监控已开启' : '自动监控已暂停，当前扫描会继续完成'; await refresh() })
  }

  async function stop(m: Mapping) {
    await act(m.id, async () => { await request('/' + m.id + '/stop', { method: 'POST' }); generation++; notice.value = '已请求停止，可稍后完整扫描补齐'; await refresh() })
  }

  function status(m: Mapping) { return m.status === 'running' ? (m.mode === 'changes' ? '正在检查变更' : m.mode === 'tree' ? '正在核对目录' : '正在完整扫描') : m.status === 'stopped' ? '已停止' : m.lastError ? '需检查' : m.queued ? '等待扫描' : pendingMetadata(m) ? attachmentCooling.value ? '附件冷却中' : downloadingMetadata(m) ? '附件补齐中' : '附件待重试' : !m.enabled ? '已暂停' : mappingHasSchedule(m) ? '监控中' : '仅手动' }

  async function visible() { window.clearTimeout(timer); if (!document.hidden) await refresh(); poll() }
  onMounted(async () => { document.addEventListener('visibilitychange', visible); await refresh(); poll() })
  onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
  return {
    state, settingsDraft, busy, error, dialogError, notice, loading,
    settingsDirty, needsSettingsSave, canStart, hasSchedule, startHint, strmPreview,
    changedAccount, eventConsumerErrors, attachmentMessage, refresh, saveSettings, resetSettings,
    eventLabel, scheduleLabel, run, toggle, stop, status, request, accounts, act, invalidate,
  }
}

export type DriveSTRMState = ReturnType<typeof useDriveSTRMState>
