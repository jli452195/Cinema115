import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

export interface DirectoryShareConfig {
  id: string; accountId: string; sourceCID: string; sourcePath: string
  stage: string; running: boolean; queued: boolean; hasBatch: boolean
  lastError: string; logTaskID?: string; logAttempt?: number; generationTaskId?: string
  shareUrl?: string; batchDirectoryName?: string; batchMoved?: number; cleanupIndex?: number
  endedBatches?: { logTaskId: string; directoryId?: string; directoryName?: string; shareUrl?: string; accountId: string; cleaned: boolean; endedAt: string }[]
}

export const directoryShareActive = (value?: DirectoryShareConfig | null) => !!value && (value.running || value.queued || (value.hasBatch && !value.lastError && !['blocked', 'sharing_legacy'].includes(value.stage)))

export function useDirectoryShare(token: string, configurationId: () => string | undefined = () => undefined) {
  const lifecycle = new AbortController()
  const monitor = ref<DirectoryShareConfig>()
  const accountId = ref('')
  const accountName = ref('')
  const accountReady = ref(false)
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
    if (!monitor.value) return '请先配置分享目录'
    if (['blocked', 'sharing_legacy'].includes(monitor.value.stage)) return '当前批次需要人工核对，请查看目录分享'
    if (bindingChanged.value) return '分享 STRM 账号已变化，请重新选择目录'
    if (!accountReady.value) return '请在 115 分享 STRM 中配置已登录账号'
    if (active.value) return '当前任务已提交，正在后台处理'
    return ''
  })
  const label = computed(() => submitting.value ? '提交中…' : active.value ? '目录任务处理中' : monitor.value?.hasBatch ? '继续本次目录分享' : '分享配置目录')
  const stageText = computed(() => {
    const value = monitor.value
    if (!value) return '尚未配置分享目录'
    if (value.lastError) return value.lastError
    if (value.stage === 'ended') return '上次任务已结束，可修改目录或重新发起分享'
    if (value.queued && ['waiting', 'empty', 'completed'].includes(value.stage)) return '已排队，等待后台处理'
    const stages: Record<string, string> = { waiting: '等待手动分享', empty: '目录为空', moving: '正在搬入临时目录', sharing: '正在创建分享', configuring: '正在配置分享', verifying: '正在等待并校验分享', generating: '公共 STRM 任务处理中，审核等待及重试见分享日志', cleaning: '正在清理临时目录', checking_share: '正在结束已清理的旧任务', completed: '分享及 STRM 生成完成', blocked: '需要人工核对', error: '任务未完成' }
    return stages[value.stage] || '任务处理中'
  })

  async function refresh() {
    const version = ++revision
    try {
      const signal = AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)])
      const [data, settings, accounts] = await Promise.all([
        p115Request<{ monitors: DirectoryShareConfig[] }>(token, 'folder-monitors', { signal }),
        p115Request<{ settings: { accountId: string } }>(token, 'share/automation-config', { signal }),
        p115Request<{ accounts: P115Account[] }>(token, 'accounts', { signal }),
      ])
      if (lifecycle.signal.aborted || version !== revision) return
      const id = configurationId()
      monitor.value = id ? data.monitors.find(value => value.id === id) : data.monitors.length === 1 ? data.monitors[0] : undefined
      accountId.value = settings.settings.accountId || ''
      const bound = monitor.value?.hasBatch ? monitor.value.accountId : accountId.value
      const account = accounts.accounts.find(value => value.id === bound)
      accountName.value = account?.name || '未配置'
      accountReady.value = !!account?.configured
      error.value = ''
    } catch (reason) {
      if (!lifecycle.signal.aborted && version === revision) error.value = reason instanceof Error ? reason.message : '读取目录分享失败'
    } finally { if (!lifecycle.signal.aborted) loading.value = false }
  }

  async function submit() {
    if (unavailable.value || !monitor.value) return
    submitting.value = true; notice.value = ''; error.value = ''
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
    if (!window.confirm('结束本次目录任务并解除配置占用？不会删除或移回网盘文件，不会取消分享或删除已生成的 STRM。剩余临时文件需自行整理，原位置保存在历史记录中。')) return
    submitting.value = true; error.value = ''; notice.value = ''
    try {
      await p115Request(token, `folder-monitors/${monitor.value.id}/end`, {
        method: 'POST', body: JSON.stringify({ taskId: monitor.value.logTaskID || '' }),
        signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
      })
      notice.value = '本次任务已结束，目录配置已解锁。若要重新处理剩余文件，请先移回配置目录。'
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
  return { monitor, accountId, accountName, loading, submitting, error, notice, active, bindingChanged, unavailable, label, stageText, refresh, submit, canEnd, endBatch }
}
