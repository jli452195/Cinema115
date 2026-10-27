import { p115Request } from '../lib/p115-api'
import type { DriveFile } from '../types/manualOrganize'
import { isSubtitleArchive, maxTotalBytes, readLocalSubtitles, type LocalSubtitle } from './subtitleImport'

export interface SubtitleSourceFile extends DriveFile { parentId: string; sourcePath: string }
interface SourcePlan { id: string; files: SubtitleSourceFile[] }
export interface SubtitleReadProgress { phase: 'prepare' | 'download' | 'extract'; name: string; index: number; count: number; loaded: number; total: number }

// Check both the response headers and the actual streamed bytes before creating
// a File. A gateway error or an oversized response never reaches the extractor.
export async function readSubtitleResponse(response: Response, expected: number, signal: AbortSignal, progress: (loaded: number) => void): Promise<Uint8Array> {
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || `字幕来源下载失败（HTTP ${response.status}）`)
  }
  if (!Number.isSafeInteger(expected) || expected <= 0 || expected > maxTotalBytes || !response.body) throw new Error('字幕来源大小或响应无效。')
  const length = response.headers.get('content-length')
  if (length !== null && Number(length) !== expected) { await response.body.cancel(); throw new Error('字幕来源大小已变化，请重新选择。') }
  const reader = response.body.getReader()
  const bytes = new Uint8Array(expected)
  let written = 0, completed = false
  const abort = () => { void reader.cancel(signal.reason).catch(() => {}) }
  signal.addEventListener('abort', abort, { once: true })
  try {
    signal.throwIfAborted()
    for (;;) {
      const { value, done } = await reader.read()
      signal.throwIfAborted()
      if (done) { completed = true; break }
      if (written + value.length > expected) throw new Error('字幕来源实际大小超过限制。')
      bytes.set(value, written); written += value.length; progress(written)
    }
    if (written !== expected) throw new Error('字幕来源下载不完整，请重试。')
    return bytes
  } finally {
    signal.removeEventListener('abort', abort)
    if (!completed) await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export async function readDriveSubtitles(token: string, accountId: string, jobId: string, selected: SubtitleSourceFile[], signal: AbortSignal, progress: (value: SubtitleReadProgress) => void): Promise<{ files: LocalSubtitle[]; ignored: number }> {
  const prefix = 'manual-organize/subtitles/sources'
  progress({ phase: 'prepare', name: '', index: 0, count: selected.length, loaded: 0, total: 0 })
  const plan = await p115Request<SourcePlan>(token, prefix, { method: 'POST', body: JSON.stringify({ accountId, jobId, files: selected }), signal: AbortSignal.any([signal, AbortSignal.timeout(130000)]) })
  const files: LocalSubtitle[] = []
  let ignored = 0, total = 0
  try {
    if (plan.files.length !== selected.length) throw new Error('字幕来源列表已变化，请重新选择。')
    for (const [index, source] of plan.files.entries()) {
      signal.throwIfAborted()
      const expected = selected.find(file => file.id === source.id)
      if (!expected || expected.name !== source.name || expected.size !== source.size || expected.parentId !== source.parentId) throw new Error('字幕来源文件已变化，请重新选择。')
      const report = (phase: SubtitleReadProgress['phase'], loaded = 0) => progress({ phase, name: source.name, index: index + 1, count: plan.files.length, loaded, total: source.size })
      report('download')
      const downloadSignal = AbortSignal.any([signal, AbortSignal.timeout(130000)])
      let bytes: Uint8Array
      try {
        const response = await fetch(`/api/admin/p115/${prefix}/${encodeURIComponent(plan.id)}/files/${encodeURIComponent(source.id)}?accountId=${encodeURIComponent(accountId)}`, { headers: { Authorization: `Bearer ${token}` }, cache: 'no-store', signal: downloadSignal })
        bytes = await readSubtitleResponse(response, source.size, downloadSignal, loaded => report('download', loaded))
      } catch (reason) {
        signal.throwIfAborted()
        throw new Error(`下载“${source.name}”失败：${downloadSignal.aborted ? '下载超时，请重试' : reason instanceof Error ? reason.message : '网络异常'}`)
      }
      report('extract')
      let result: { files: LocalSubtitle[]; ignored: number }
      try { result = await readLocalSubtitles([new File([new Uint8Array(bytes)], source.name)], signal) }
      catch (reason) { signal.throwIfAborted(); throw new Error(`解析“${source.name}”失败：${reason instanceof Error ? reason.message : '压缩包无效'}`) }
      ignored += result.ignored
      for (const file of result.files) {
        total += file.data.length
        if (files.length >= 100 || total > maxTotalBytes) throw new Error('每次最多 100 个字幕，合计 64 MiB。')
        files.push({ ...file, sourceKey: file.sourceKey || `drive-directory:${source.parentId}`, sourceLabel: `${expected.sourcePath}${isSubtitleArchive(source.name) ? '（压缩包）' : ''}` })
      }
    }
    return { files, ignored }
  } finally {
    // Cleanup is independent of a cancelled download, and remains bounded.
    void p115Request<void>(token, `${prefix}/${encodeURIComponent(plan.id)}?accountId=${encodeURIComponent(accountId)}`, { method: 'DELETE', signal: AbortSignal.timeout(5000) }).catch(() => {})
  }
}
