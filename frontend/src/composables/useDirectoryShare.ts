import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'
import type { STRMSettings } from '../utils/shareGeneration'

export interface DirectoryShareConfig {
  id: string; accountId: string; sourceCID: string; sourcePath: string
  stage: string; running: boolean; queued: boolean; hasBatch: boolean
  lastError: string; logTaskID?: string; logAttempt?: number; generationTaskId?: string
  recoveryMessage?: string
  enabled?: boolean; intervalSeconds?: number; schedulePaused?: string; scheduleMessage?: string
  lastScanAt?: string; nextScanAt?: string; trigger?: string; batchCount?: number
  recentRuns?: { taskId: string; trigger: string; status: string; message: string; shareUrl?: string; time: string }[]
  shareUrl?: string; batchDirectoryName?: string; batchMoved?: number; cleanupIndex?: number
  endedBatches?: { logTaskId: string; directoryId?: string; directoryName?: string; shareUrl?: string; accountId: string; cleaned: boolean; endedAt: string }[]
}

export const directoryShareActive = (value?: DirectoryShareConfig | null) => !!value && (value.running || value.queued || (value.hasBatch && !value.lastError && !['blocked', 'sharing_legacy'].includes(value.stage)))

export function useDirectoryShare(token: string) {
  const lifecycle = new AbortController()
  const monitor = ref<DirectoryShareConfig>()
  const accountId = ref('')
  const accountReady = ref(false)
  const generation = ref<STRMSettings>()
  const loading = ref(true)
  const submitting = ref(false)
  const error = ref('')
  const notice = ref('')
  let timer: ReturnType<typeof setTimeout> | undefined
  let revision = 0
  const active = computed(() => directoryShareActive(monitor.value))
  const canEnd = computed(() => !!monitor.value && !monitor.value.running && !loading.value && !submitting.value && (monitor.value.hasBatch || monitor.value.queued || ['blocked', 'sharing_legacy'].includes(monitor.value.stage)))
  const bindingChanged = computed(() => !!monitor.value && !monitor.value.hasBatch && monitor.value.accountId !== accountId.value)
  const unavailable = computed(() => {
    if (loading.value || submitting.value) return '正在读取或提交任务'
    if (error.value) return '请先刷新配置和任务状态'
    if (!monitor.value) return '请先选择监控目录'
    if (['blocked', 'recovering', 'creating', 'sharing_legacy'].includes(monitor.value.stage) || (monitor.value.stage === 'error' && monitor.value.hasBatch)) return '当前批次需要核对，请先核对残留或结束任务'
    if (bindingChanged.value) return '主账号已变化，请重新选择目录'
    if (!accountReady.value) return '请在 115 助手中配置并登录主账号'
    if (active.value) return '当前任务已提交，正在后台处理'
    return ''
  })
  const label = computed(() => submitting.value ? '提交中…' : active.value ? '任务处理中' : monitor.value?.hasBatch ? '重试本次任务' : '立即运行')
  const stageText = computed(() => {
    const value = monitor.value
    if (!value) return '尚未选择监控目录'
    if (value.lastError) return value.lastError
    if (value.stage === 'creating') return '正在创建临时目录'
    if (value.stage === 'recovering') return '正在核对残留目录'
    if (!value.queued && value.stage === 'waiting' && value.enabled) return value.scheduleMessage || '等待定时检查'
    if (value.stage === 'ended') return value.recoveryMessage || '上次任务已结束，可修改目录或重新发起分享'
    if (value.queued && ['waiting', 'empty', 'completed'].includes(value.stage)) return '已排队，等待后台处理'
    const stages: Record<string, string> = { waiting: '等待处理', empty: '目录为空', moving: '正在准备本批文件', sharing: '正在创建分享', configuring: '正在配置分享', verifying: '正在校验分享文件', generating: '正在生成分享 STRM，详细进度见生成日志', cleaning: '正在将本批源文件移入 115 回收站', checking_share: '正在确认本批清理结果', completed: '分享及 STRM 生成完成', blocked: '需要人工核对', error: '任务未完成' }
    return stages[value.stage] || '任务处理中'
  })

  async function refresh() {
    const version = ++revision
    try {
      const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
      const [data, settings, accounts] = await Promise.all([
        p115Request<{ monitors: DirectoryShareConfig[] }>(token, 'folder-monitors', { signal }),
        p115Request<{ settings: { accountId: string; generation: STRMSettings } }>(token, 'share/automation-config', { signal }),
        p115Request<{ accounts: P115Account[] }>(token, 'accounts', { signal }),
      ])
      if (lifecycle.signal.aborted || version !== revision) return
      monitor.value = data.monitors.length === 1 ? data.monitors[0] : undefined
      accountId.value = settings.settings.accountId || ''
      generation.value = settings.settings.generation
      const bound = monitor.value?.hasBatch ? monitor.value.accountId : accountId.value
      const account = accounts.accounts.find(value => value.id === bound)
      accountReady.value = !!account?.primary && !!account.configured
      error.value = ''
    } catch (reason) {
      if (!lifecycle.signal.aborted && version === revision) error.value = reason instanceof Error ? reason.message : '读取目录分享失败'
    } finally { if (!lifecycle.signal.aborted && version === revision) loading.value = false }
  }

  function invalidate() { revision++ }

  async function submit() {
    if (unavailable.value || !monitor.value) return
    submitting.value = true; notice.value = ''; error.value = ''
    invalidate()
    try {
      const result = await p115Request<{ accepted: boolean; taskId: string }>(token, `folder-monitors/${monitor.value.id}/share`, {
        method: 'POST', body: '{}', signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
      })
      notice.value = result.accepted ? '已提交后台，离开页面后继续处理。' : '本目录已有任务，已关联当前处理进度。'
      await refresh()
    } catch (reason) {
      if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '提交目录分享失败'
    } finally { submitting.value = false }
  }

  async function endBatch() {
    if (!canEnd.value || !monitor.value) return
    if (!window.confirm('结束本次任务并关闭自动监控？现有网盘文件、分享和 STRM 会保留。若要重新处理剩余文件，请移回监控目录；保留位置可在最近记录中查看。')) return
    submitting.value = true; error.value = ''; notice.value = ''
    invalidate()
    try {
      await p115Request(token, `folder-monitors/${monitor.value.id}/end`, {
        method: 'POST', body: JSON.stringify({ taskId: monitor.value.logTaskID || '' }),
        signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
      })
      notice.value = '本次任务已结束，自动监控已关闭。保留文件位置可在最近记录中查看。'
      await refresh()
    } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '结束任务失败' }
    finally { submitting.value = false }
  }

  async function poll() {
    if (!submitting.value) await refresh()
    if (!lifecycle.signal.aborted) timer = setTimeout(poll, active.value ? 3000 : 10000)
  }
  onMounted(() => { void poll() })
  onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(timer) })
  return { monitor, accountId, accountReady, generation, loading, submitting, error, notice, active, bindingChanged, unavailable, label, stageText, refresh, invalidate, submit, canEnd, endBatch }
}
