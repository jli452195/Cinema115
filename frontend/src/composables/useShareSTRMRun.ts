import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useToast } from './useToast'
import { normalize115Share } from '../utils/shareLink'
import { cancelShareRun, newShareRunID, savedSTRMSettings, ShareRunError, submitShareRun, waitForShareRun, type ShareRun } from '../utils/shareGeneration'

export interface ShareSTRMLink {
  url: string
  password?: string
}

export interface ShareSTRMResource {
  id: string
  identity: string
  load: () => Promise<ShareSTRMLink[]>
}

export interface ShareSTRMStatus {
  message: string
  success: boolean
}

const runStorageKey = 'cinema115.share-strm-runs.v2'
type SavedRun = { key: string; id: string; time: number; taskIDs?: string[] }

function savedRuns(): SavedRun[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(runStorageKey) || '[]')
    return Array.isArray(value) ? value.filter((entry): entry is SavedRun => typeof entry?.key === 'string' && /^[a-f0-9]{64}$/.test(entry.key) && /^[a-f0-9]{32}$/.test(entry.id) && Date.now() - entry.time < 86400000).slice(-100) : []
  } catch { return [] }
}

function rememberRun(key: string, id?: string, taskIDs?: string[]) {
  const entries = savedRuns().filter(entry => entry.key !== key)
  if (id) entries.push({ key, id, time: Date.now(), taskIDs })
  try { localStorage.setItem(runStorageKey, JSON.stringify(entries.slice(-100))) } catch { /* 分享日志仍可查询后台任务。 */ }
}

async function identityHash(identity: string) {
  const bytes = new TextEncoder().encode(identity)
  if (globalThis.crypto?.subtle) {
    const digest = await globalThis.crypto.subtle.digest('SHA-256', bytes)
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  }
  // Web Crypto is unavailable on some HTTP LAN origins. Keep the persisted
  // key non-plaintext there too; multiple seeded FNV rounds provide a stable
  // 64-character fingerprint for task association (not authentication).
  let output = ''
  for (let seed = 0; seed < 8; seed++) {
    let hash = (0x811c9dc5 ^ seed) >>> 0
    for (const byte of bytes) hash = Math.imul(hash ^ byte, 0x01000193) >>> 0
    output += hash.toString(16).padStart(8, '0')
  }
  return output
}

function runMessage(run: ShareRun): ShareSTRMStatus {
  if (run.selectedTaskIds?.length) run = { ...run, items: run.items.filter(item => run.selectedTaskIds!.includes(item.taskId)) }
  // 合并批次里的其他来源失败，不应把当前已成功的资源显示为失败。
  if (['success', 'failed'].includes(run.status) && run.items.length) run = { ...run, status: run.items.every(item => item.status === 'success') ? 'success' : 'failed' }
  let written = 0, metadata = 0, metadataFailed = 0, conflictRenamed = 0
  for (const entry of run.items) {
    written += entry.result?.count ?? 0
    metadata += entry.result?.metadataCount ?? 0
    metadataFailed += entry.result?.metadataFailed ?? 0
    conflictRenamed += entry.result?.conflictRenamed ?? 0
  }
  const current = run.items.findIndex(entry => entry.status === 'running')
  const failures = run.items.flatMap((entry, index) => entry.error ? [`链接 ${index + 1}：${entry.error}`] : [])
  const cached = run.items.length > 0 && run.items.every(entry => entry.result?.cached)
  const detail = `${metadata || metadataFailed ? `，元数据成功 ${metadata} 个、失败 ${metadataFailed} 个` : ''}${conflictRenamed ? `，同名另存 ${conflictRenamed} 个` : ''}`
  let message = current >= 0 ? `${current + 1}/${run.items.length} · ${run.items[current]?.message}` : '已提交后台，等待执行'
  if (run.status === 'queued' && run.readyAt && Date.parse(run.readyAt) > Date.now()) message = `收集中，还剩约 ${Math.ceil((Date.parse(run.readyAt) - Date.now()) / 1000)} 秒，可继续选择其他资源`
  if (run.status === 'success') message = cached ? `本地文件完整：已复用 ${written} 个 STRM${detail} · ${run.items[0]?.result?.directory ?? ''}` : `生成成功：已处理 ${written} 个 STRM${detail}${metadataFailed ? '；再次提交会继续重试元数据' : ''} · ${run.items[0]?.result?.directory ?? ''}`
  else if (run.status === 'failed') message = `新增 ${written} 个 STRM；${failures.join('；')}`
  else if (run.status === 'cancelled') message = `后台任务已停止，新增 ${written} 个 STRM；请在分享日志核对已转存目录。`
  else if (run.status === 'cancelling') message = '正在停止后台任务，等待保存处理结果…'
  return { message, success: run.status === 'success' }
}

export function useShareSTRMRun(token: string, signal: AbortSignal, options: { scope?: string; beforeLoad?: () => Promise<void> } = {}) {
  const toast = useToast(), router = useRouter()
  const logAction = { label: '查看日志', onClick: () => { void router.push('/115/share/logs') } }
  const pending = ref<Record<string, boolean>>({})
  const preparing = ref(0)
  const busy = computed(() => Object.values(pending.value).some(Boolean))
  const runIDs = ref<Record<string, string>>({})
  const activeRunID = computed(() => Object.values(runIDs.value)[0] || '')
  const stopping = ref(false)
  const statuses = ref<Record<string, ShareSTRMStatus>>({})
  // 取链接串行，后台提交完成即释放准备队列，不等待 STRM 执行完毕。
  let preparation: Promise<void> = Promise.resolve()
  const scope = options.scope ? `${options.scope}/${newShareRunID()}` : undefined

  async function follow(resource: ShareSTRMResource, id: string, key: string, taskIDs?: string[], notify = false) {
    pending.value[resource.id] = true; runIDs.value[resource.id] = id
    try {
      const run = await waitForShareRun(token, id, signal, run => { statuses.value[resource.id] = runMessage({ ...run, selectedTaskIds: taskIDs?.length ? taskIDs : run.selectedTaskIds }) })
      rememberRun(key)
      if (notify && !signal.aborted) {
        const status = runMessage({ ...run, selectedTaskIds: taskIDs?.length ? taskIDs : run.selectedTaskIds })
        toast[status.success ? 'success' : run.status === 'cancelled' ? 'warning' : 'error'](status.message, { title: status.success ? 'STRM 生成完成' : 'STRM 任务结束', action: logAction })
      }
    } catch (error) {
      if (!signal.aborted) {
        if (error instanceof ShareRunError && error.status === 404) rememberRun(key)
        statuses.value[resource.id] = { message: `暂时无法查询进度：${error instanceof Error ? error.message : '网络异常'}。后台任务不会因页面断开而取消，请查看分享日志或 Telegram 通知。`, success: false }
        toast.warning(statuses.value[resource.id]!.message, { title: 'STRM 进度暂不可用', action: logAction })
      }
    } finally {
      delete pending.value[resource.id]; delete runIDs.value[resource.id]
    }
  }

  async function restore(resources: ShareSTRMResource[]) {
    for (const resource of resources) {
      if (signal.aborted) return
      if (pending.value[resource.id]) continue
      const key = await identityHash(resource.identity)
      if (signal.aborted || pending.value[resource.id]) continue
      const existing = savedRuns().find(entry => entry.key === key)
      if (existing) void follow(resource, existing.id, key, existing.taskIDs)
    }
  }

  async function submit(resource: ShareSTRMResource) {
    if (signal.aborted || pending.value[resource.id] || statuses.value[resource.id]?.success) return
    pending.value[resource.id] = true
    preparing.value++
    const update = (message: string, success = false) => { statuses.value[resource.id] = { message, success } }
    update('已选择，等待搜索或前面的资源取链完成…')
    const prepare = async () => {
      let following = false, submitting = false
      try {
        signal.throwIfAborted()
        await options.beforeLoad?.()
        signal.throwIfAborted()
        const key = await identityHash(resource.identity)
        const existing = savedRuns().find(entry => entry.key === key)
        if (existing) { following = true; void follow(resource, existing.id, key, existing.taskIDs); return true }
        update('正在读取生成配置…')
        const settings = await savedSTRMSettings(token, signal)
        update('正在获取并校验 115 分享链接…')
        const loaded = await resource.load()
        const unique = new Map<string, ShareSTRMLink>()
        for (const link of loaded) {
          const url = normalize115Share(link.url)
          if (!url) continue
          const value = { url, password: link.password?.trim() || '' }
          unique.set(url + '\u0000' + value.password, value)
        }
        if (!unique.size) throw new Error('没有可处理的 115 分享链接')
        signal.throwIfAborted()
        const id = newShareRunID()
        rememberRun(key, id)
        update('正在提交全部链接到后台…')
        submitting = true
        const run = await submitShareRun(token, id, [...unique.values()].map(link => ({ ...settings, mode: '', url: link.url, password: link.password, cid: '0' })), scope)
        rememberRun(key, run.id, run.selectedTaskIds)
        if (!signal.aborted) {
          statuses.value[resource.id] = runMessage(run)
          following = true
          void follow(resource, run.id, key, run.selectedTaskIds, true)
        }
        return true
      } catch (error) {
        // 关闭弹窗只取消尚未提交的准备；已经发出的提交仍需显示真实结果。
        if (signal.aborted && !submitting) return false
        const message = `${error instanceof Error ? error.message : '提交失败'}${submitting ? '；若未收到确认，请重新打开弹窗查询原任务或查看分享日志。' : ''}`
        if (!signal.aborted) update(message)
        throw new Error(message)
      } finally {
        preparing.value--
        if (!following) delete pending.value[resource.id]
      }
    }
    const submission = preparation.then(prepare, prepare)
    toast.promise(submission, {
      loading: '正在准备分享 STRM…',
      success: (accepted: boolean) => accepted
        ? { message: '已提交后台', description: '可关闭窗口，STRM 任务会继续执行。', action: logAction, duration: 3000 }
        : { message: '已取消准备', type: 'info', description: '本次资源尚未提交后台。', duration: 3000 },
      error: (error: unknown) => ({ message: 'STRM 提交失败', description: error instanceof Error ? error.message : '请稍后重试', action: logAction, duration: 5000 }),
    })
    // 页面点击调用无需处理异常；Promise Toast 接收真实错误，准备队列继续执行。
    preparation = submission.then(() => {}, () => {})
    await preparation
  }

  async function stop(resourceID?: string) {
    const ids = resourceID ? [runIDs.value[resourceID]].filter((id): id is string => !!id) : [...new Set(Object.values(runIDs.value))]
    if (!ids.length || stopping.value) return
    stopping.value = true
    try { await Promise.all(ids.map(id => cancelShareRun(token, id))) }
    catch (error) {
      for (const [resource, id] of Object.entries(runIDs.value)) if (ids.includes(id)) statuses.value[resource] = { message: error instanceof Error ? error.message : '停止任务失败', success: false }
      toast.error(error instanceof Error ? error.message : '停止任务失败', { title: '停止 STRM 任务失败', action: logAction })
    } finally {
      stopping.value = false
    }
  }

  return { busy, pending, preparing, runIDs, activeRunID, stopping, statuses, submit, restore, stop }
}
