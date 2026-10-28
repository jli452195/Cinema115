import { ArchiveReader, type LibarchiveWasm } from 'libarchive-wasm'
import { sha256 } from 'js-sha256'
import type { LocalSubtitle } from './subtitleImport'

export type ArchiveKind = 'rar' | '7z' | 'zip'
export interface ArchiveLimits { bytes: number; files: number }
export interface ArchiveResult { files: LocalSubtitle[]; ignored: number }
export const archiveErrors = {
  encrypted: '不支持加密压缩包，请在电脑上解压后选择字幕。',
  split: '不支持分卷压缩包，请在电脑上解压后选择字幕。',
  invalid: '压缩包损坏或格式不受支持，请核对文件完整性。',
  limit: '每次最多 100 个字幕，单个 16 MiB、合计 64 MiB。',
}
class ArchiveInputError extends Error {}
const reject = (message = archiveErrors.invalid): never => { throw new ArchiveInputError(message) }
const starts = (bytes: Uint8Array, signature: number[]) => signature.every((value, i) => bytes[i] === value)

// Reject volume and encryption flags independently of names and selected entries.
// In particular, an archive with an encrypted non-subtitle is still rejected.
function validateArchive(bytes: Uint8Array, kind: Exclude<ArchiveKind, 'zip'>) {
  if (kind === '7z') {
    if (bytes.length < 32 || !starts(bytes, [0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c])) reject()
    return
  }
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const rar5 = starts(bytes, [0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x01, 0])
  if (!rar5 && !starts(bytes, [0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0])) reject()
  let offset = rar5 ? 8 : 7, blocks = 0
  while (offset < bytes.length) {
    if (++blocks > 4000) reject('压缩包条目过多，最多支持 2000 个条目。')
    if (!rar5) {
      if (offset + 7 > bytes.length) reject()
      const type = bytes[offset + 2], flags = view.getUint16(offset + 3, true), size = view.getUint16(offset + 5, true)
      if (type === 0x7b) break
      if (size < 7 || offset + size > bytes.length) reject()
      if (type === 0x74 && flags & 3) reject(archiveErrors.split)
      if (flags & 4) reject(archiveErrors.encrypted)
      let dataSize = 0
      if (flags & 0x8000) {
        if (size < 11) reject()
        dataSize = view.getUint32(offset + 7, true)
        if (type === 0x74 && flags & 0x100) {
          if (size < 40) reject()
          dataSize += view.getUint32(offset + 32, true) * 0x100000000
        }
      }
      offset += size + dataSize
    } else {
      if (offset + 5 > bytes.length) reject()
      let cursor = offset + 4
      const vint = (end: number) => {
        let value = 0, factor = 1
        for (let i = 0; i < 8 && cursor < end; i++) {
          const byte = bytes[cursor++]
          value += (byte & 0x7f) * factor
          if (!Number.isSafeInteger(value)) reject()
          if (!(byte & 0x80)) return value
          factor *= 128
        }
        return reject()
      }
      const size = vint(bytes.length), end = cursor + size
      if (size < 2 || end > bytes.length) reject()
      const type = vint(end), flags = vint(end)
      const extraSize = flags & 1 ? vint(end) : 0, dataSize = flags & 2 ? vint(end) : 0
      if (extraSize > end - cursor) reject()
      if (flags & 0x18) reject(archiveErrors.split)
      if (type === 4) reject(archiveErrors.encrypted)
      if ((type === 1 || type === 5) && vint(end - extraSize) & 1) reject(archiveErrors.split)
      if ((type === 2 || type === 3) && extraSize) {
        cursor = end - extraSize
        while (cursor < end) {
          const recordSize = vint(end), recordEnd = cursor + recordSize
          if (!recordSize || recordEnd > end) reject()
          if (vint(recordEnd) === 1) reject(archiveErrors.encrypted)
          cursor = recordEnd
        }
      }
      offset = end + dataSize
    }
    if (!Number.isSafeInteger(offset) || offset > bytes.length) reject()
  }
}

export function extractSubtitleArchive(bytes: Uint8Array, kind: Exclude<ArchiveKind, 'zip'>, module: LibarchiveWasm, limits: ArchiveLimits): ArchiveResult {
  validateArchive(bytes, kind)
  let reader: ArchiveReader | undefined, buffer = 0
  const files: LocalSubtitle[] = []
  let total = 0, ignored = 0, entries = 0
  try {
    reader = new ArchiveReader(module, new Int8Array(bytes.buffer, bytes.byteOffset, bytes.byteLength))
    buffer = module.module._malloc(65536)
    if (!buffer) reject('本地解压内存不足，请减少字幕文件后重试。')
    for (const entry of reader.entries()) {
      if (++entries > 2000) reject('压缩包条目过多，最多支持 2000 个条目。')
      if (entry.isEncrypted() || reader.hasEncryptedData()) reject(archiveErrors.encrypted)
      const name = entry.getPathname().replace(/\\/g, '/')
      if (entry.getFiletype() === 'Directory') continue
      if (!/\.(srt|ass|ssa|vtt|idx|sub)$/i.test(name) || name.startsWith('__MACOSX/')) { ignored++; continue }
      if (!name || name.length > 2048 || /^(?:\/|[a-z]:)/i.test(name) || name.split('/').includes('..') || /[\x00\r\n]/.test(name)) reject('压缩包中包含无效文件路径。')
      if (entry.getFiletype() !== 'File' || entry.getSymlinkTarget() || entry.getHardlinkTarget()) reject('压缩包中的字幕必须是普通文件。')
      const size = entry.getSize()
      if (!Number.isSafeInteger(size) || size <= 0 || size > 16 * 1024 * 1024 || total + size > limits.bytes || files.length >= limits.files) reject(archiveErrors.limit)
      const data = new Uint8Array(size)
      let written = 0
      for (;;) {
        // Reading to EOF also checks CRCs. The scratch buffer bounds each output allocation.
        const remaining = size - written
        if (remaining === 0) break
        const read = module.read_data(reader.archive, buffer, Math.min(65536, remaining))
        if (read <= 0) break
        if (written + read > size) reject('压缩包实际解压大小超过声明大小。')
        data.set(module.module.HEAPU8.subarray(buffer, buffer + read), written)
        written += read
      }
      entry.readCalled = true
      if (written !== size) reject('压缩包文件大小或完整性校验失败。')
      total += written
      files.push({ name, data, sha256: sha256(data) })
    }
    if (reader.hasEncryptedData()) reject(archiveErrors.encrypted)
    if (module.error_string(reader.archive)) reject()
    return { files, ignored }
  } catch (reason) {
    if (reason instanceof ArchiveInputError) throw reason
    const message = reason instanceof Error ? reason.message : ''
    if (/encrypt|passphrase|password/i.test(message)) reject(archiveErrors.encrypted)
    if (/volume|split|multivolume/i.test(message)) reject(archiveErrors.split)
    return reject()
  } finally {
    if (buffer) module.module._free(buffer)
    reader?.free()
  }
}
