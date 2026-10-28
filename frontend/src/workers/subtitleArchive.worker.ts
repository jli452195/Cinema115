import { libarchiveWasm } from 'libarchive-wasm'
import wasmURL from 'libarchive-wasm/dist/libarchive.wasm?url'
import { extractSubtitleArchive, type ArchiveKind, type ArchiveLimits } from '../utils/subtitleArchive'
import { readSubtitleZip } from '../utils/subtitleImport'

const worker = self as unknown as {
  onmessage: ((event: MessageEvent<{ bytes: Uint8Array; kind: ArchiveKind; limits: ArchiveLimits }>) => void) | null
  postMessage: (value: unknown, transfer: Transferable[]) => void
}
worker.onmessage = async ({ data }) => {
  try {
    if (data.kind === 'zip') {
      const result = await readSubtitleZip(data.bytes, undefined, data.limits)
      worker.postMessage({ result }, result.files.map(file => file.data.buffer as ArrayBuffer))
      return
    }
    const module = await libarchiveWasm({ locateFile: () => wasmURL })
    const result = extractSubtitleArchive(data.bytes, data.kind, module, data.limits)
    worker.postMessage({ result }, result.files.map(file => file.data.buffer as ArrayBuffer))
  } catch (reason) {
    worker.postMessage({ error: reason instanceof Error ? reason.message : '压缩包读取失败。' }, [])
  }
}
