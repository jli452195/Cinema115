import { computed, ref } from 'vue'
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
  description: string
  load: () => Promise<ShareSTRMLink[]>
}

export interface ShareSTRMStatus {
  message: string
  success: boolean
  count?: number
}

const runStorageKey = 'cinema115.share-strm-runs.v2'
type SavedRun = { key: string; id: string; time: number; taskIDs?: string[] }
type TrackedRun = { promise: Promise<ShareRun>; latest?: ShareRun; listeners: Set<(run: ShareRun) => void> }
const trackedRuns = new Map<string, TrackedRun>()

function trackRun(token: string, id: string, progress: (run: ShareRun) => void) {
  const key = JSON.stringify([token, id])
  let tracked = trackedRuns.get(key)
  if (!tracked) {
    const entry: TrackedRun = { promise: undefined!, listeners: new Set() }
    // 已提交任务的轮询独立于搜索弹窗；同一批次只保留一条请求链。
    entry.promise = waitForShareRun(token, id, new AbortController().signal, run => {
      entry.latest = run
      for (const listener of entry.listeners) listener(run)
    }).finally(() => { trackedRuns.delete(key) })
    trackedRuns.set(key, entry)
    tracked = entry
  }
  tracked.listeners.add(progress)
  if (tracked.latest) progress(tracked.latest)
  return tracked.promise.finally(() => { tracked.listeners.delete(progress) })
}

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
  // 部分 HTTP 局域网地址不支持 Web Crypto，用多轮 FNV 保留非明文的任务关联指纹。
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
  return { message, success: run.status === 'success', count: written }
}

export function useShareSTRMRun(token: string, signal: AbortSignal, options: { scope?: string; beforeLoad?: () => Promise<void> } = {}) {
  const toast = useToast()
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

  async function follow(resource: ShareSTRMResource, id: string, key: string, taskIDs?: string[]) {
    pending.value[resource.id] = true; runIDs.value[resource.id] = id
    try {
      const run = await trackRun(token, id, run => { statuses.value[resource.id] = runMessage({ ...run, selectedTaskIds: taskIDs?.length ? taskIDs : run.selectedTaskIds }) })
      rememberRun(key)
      const status = runMessage({ ...run, selectedTaskIds: taskIDs?.length ? taskIDs : run.selectedTaskIds })
      statuses.value[resource.id] = status
      if (!status.success) throw Object.assign(new Error(run.status === 'cancelled' ? '已停止生成 STRM' : status.message), { name: run.status === 'cancelled' ? 'AbortError' : 'Error' })
      return status
    } catch (error) {
      if (error instanceof ShareRunError && error.status === 404) rememberRun(key)
      const message = error instanceof Error ? error.message : '暂时无法获取 STRM 任务结果'
      statuses.value[resource.id] = { message, success: false }
      throw error
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
      if (existing) void follow(resource, existing.id, key, existing.taskIDs).catch(() => {})
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
        if (existing) { following = true; return { completion: follow(resource, existing.id, key, existing.taskIDs) } }
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
        statuses.value[resource.id] = runMessage(run)
        following = true
        return { completion: follow(resource, run.id, key, run.selectedTaskIds) }
      } catch (error) {
        // 关闭弹窗只取消尚未提交的准备；已经发出的提交仍需显示真实结果。
        if (signal.aborted && !submitting) throw Object.assign(new Error('已取消准备 STRM'), { name: 'AbortError' })
        const message = `${submitting ? '提交结果未确认：' : ''}${error instanceof Error ? error.message : '提交失败'}`
        if (!signal.aborted) update(message)
        throw new Error(message)
      } finally {
        preparing.value--
        if (!following) delete pending.value[resource.id]
      }
    }
    const submission = preparation.then(prepare, prepare)
    toast.promise(submission.then(result => result.completion), {
      loading: '正在生成 STRM…',
      description: resource.description,
      classes: { description: 'task-resource-summary' },
      success: (status: ShareSTRMStatus) => `STRM 生成成功 · ${status.count ?? 0} 个文件`,
      error: (error: unknown) => ({ message: error instanceof Error && error.name === 'AbortError' ? error.message : 'STRM 生成失败：' + (error instanceof Error ? error.message : '请稍后重试'), type: error instanceof Error && error.name === 'AbortError' ? 'info' : 'error' }),
      richColors: true,
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
      toast.error(error instanceof Error ? error.message : '停止任务失败', { title: '停止 STRM 任务失败' })
    } finally {
      stopping.value = false
    }
  }

  return { busy, pending, preparing, runIDs, activeRunID, stopping, statuses, submit, restore, stop }
}
