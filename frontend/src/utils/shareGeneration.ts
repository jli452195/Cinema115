export interface STRMResult { cached?: boolean; count: number; metadataCount?: number; metadataFailed?: number; conflictRenamed?: number; directory: string; reshareUrl?: string; cleaned?: boolean }
export interface STRMSettings { outputDir: string; baseUrl: string; videoExtensions: string; metadataExtensions: string; minSizeMB: number; recursive: boolean }

export async function savedSTRMSettings(token: string, signal: AbortSignal): Promise<STRMSettings> {
  const response = await fetch('/api/admin/p115/share/automation-config', { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data?.settings?.generation) throw new Error(data?.error || '读取 STRM 生成配置失败')
  const settings = data.settings.generation as STRMSettings
  if (!settings.baseUrl?.trim() || !settings.outputDir?.trim()) throw new Error('请先在「115分享STRM」基础设置中保存播放地址和输出目录')
  return settings
}

export interface ShareRunItem { taskId: string; status: string; message: string; error?: string; result?: STRMResult }
export interface ShareRun { id: string; status: string; items: ShareRunItem[] }
export class ShareRunError extends Error { constructor(message: string, public status: number) { super(message) } }
export function newShareRunID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), n => n.toString(16).padStart(2, '0')).join('')
}
async function runRequest(token: string, path: string, signal: AbortSignal, body?: object): Promise<ShareRun> {
  const response = await fetch('/api/admin/p115/share/generation-runs' + path, {
    method: body === undefined ? 'GET' : 'POST', signal,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new ShareRunError(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `读取后台任务失败（HTTP ${response.status}）`, response.status)
  return data
}
// 提交成功后任务由后端持有，页面卸载只取消后续轮询。
export function submitShareRun(token: string, id: string, inputs: object[]) {
  return runRequest(token, '', AbortSignal.timeout(20000), { id, inputs })
}
export function cancelShareRun(token: string, id: string) {
  return runRequest(token, '/' + encodeURIComponent(id) + '/cancel', AbortSignal.timeout(15000), {})
}
export async function waitForShareRun(token: string, id: string, signal: AbortSignal, progress: (run: ShareRun) => void): Promise<ShareRun> {
  for (;;) {
    signal.throwIfAborted()
    const run = await runRequest(token, '/' + encodeURIComponent(id), AbortSignal.any([signal, AbortSignal.timeout(15000)]))
    progress(run)
    if (!['queued', 'running', 'cancelling'].includes(run.status)) return run
    await new Promise<void>((resolve, reject) => {
      const abort = () => { window.clearTimeout(timer); reject(signal.reason) }
      const timer = window.setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, 1000)
      signal.addEventListener('abort', abort, { once: true })
      if (signal.aborted) { signal.removeEventListener('abort', abort); abort() }
    })
  }
}
export async function generateShareSTRM(token: string, body: object, signal: AbortSignal, progress: (message: string) => void, partial: (url: string) => void = () => {}, accepted: (id: string) => void = () => {}): Promise<STRMResult> {
  signal.throwIfAborted()
  const id = newShareRunID()
  const submitted = await submitShareRun(token, id, [body])
  accepted(submitted.id)
  const run = await waitForShareRun(token, submitted.id, signal, view => progress(view.items[0]?.message ?? '后台任务正在执行'))
  const item = run.items[0]
  if (item?.result?.reshareUrl) partial(item.result.reshareUrl)
  if (run.status !== 'success' || !item?.result) throw new Error(item?.error ?? '后台任务未完成，请查看分享日志')
  return item.result
}
