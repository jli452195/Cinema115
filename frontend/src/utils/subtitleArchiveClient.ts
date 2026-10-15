import type { ArchiveKind, ArchiveLimits, ArchiveResult } from './subtitleArchive'

export function readSubtitleArchive(bytes: Uint8Array, kind: ArchiveKind, limits: ArchiveLimits, signal?: AbortSignal): Promise<ArchiveResult> {
  signal?.throwIfAborted()
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL('../workers/subtitleArchive.worker.ts', import.meta.url), { type: 'module' })
    let settled = false
    const finish = (reason?: unknown, result?: ArchiveResult) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      signal?.removeEventListener('abort', abort)
      worker.terminate()
      if (reason) reject(reason)
      else resolve(result!)
    }
    const abort = () => finish(signal?.reason || new DOMException('已取消读取', 'AbortError'))
    const timer = setTimeout(() => finish(new Error('本地解压超时，请先解压后选择字幕文件。')), 60000)
    signal?.addEventListener('abort', abort, { once: true })
    worker.onmessage = ({ data }: MessageEvent<{ result?: ArchiveResult; error?: string }>) => {
      if (data.error) finish(new Error(data.error))
      else if (data.result) finish(undefined, data.result)
      else finish(new Error('压缩包读取失败。'))
    }
    worker.onerror = event => { event.preventDefault(); finish(new Error('本地解压组件加载或运行失败，请重试选择文件。')) }
    worker.onmessageerror = () => finish(new Error('本地解压结果读取失败。'))
    const transferBytes = bytes.byteOffset === 0 && bytes.byteLength === bytes.buffer.byteLength ? bytes : bytes.slice()
    try { worker.postMessage({ bytes: transferBytes, kind, limits }, [transferBytes.buffer as ArrayBuffer]) }
    catch (reason) { finish(reason) }
  })
}
