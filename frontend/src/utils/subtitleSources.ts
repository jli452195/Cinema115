import { maxTotalBytes } from './subtitleImport'

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
