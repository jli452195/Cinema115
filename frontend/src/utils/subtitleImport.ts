import { Inflate } from 'fflate'
import { sha256 } from 'js-sha256'
import type { SubtitleVideo } from '../types/subtitleImport'

export const maxFileBytes = 16 * 1024 * 1024
export const maxTotalBytes = 64 * 1024 * 1024
export interface LocalSubtitle { name: string; data: Uint8Array; sha256: string }
export const isSubtitle = (name: string) => /\.(srt|ass|ssa|vtt|idx|sub)$/i.test(name)
const base = (name: string) => name.replace(/\\/g, '/').split('/').pop() || ''
const stem = (name: string) => base(name).replace(/\.[^.]+$/, '')

export function episodeNumber(name: string): { season?: number; episode: number } | undefined {
  const title = stem(name)
  // Multi-episode files need explicit selection.
  if (/S\d+E\d+(?:E\d+|[- ]E?\d+)/i.test(title)) return undefined
  let match = title.match(/S(\d{1,3})[ ._-]*E(\d{1,4})(?!\d)/i)
  if (match) return { season: Number(match[1]), episode: Number(match[2]) }
  match = title.match(/(?:第\s*(\d{1,4})\s*[集话話]|(?:^|[ ._\[-])(?:EP?|ep?)\s*(\d{1,4})(?!\d)|^\s*(\d{1,3})(?:\D|$))/)
  if (match) return { episode: Number(match[1] || match[2] || match[3]) }
  match = title.match(/[\[【](\d{1,3})[\]】]/)
  return match ? { episode: Number(match[1]) } : undefined
}

export function matchSubtitle(name: string, videos: SubtitleVideo[], season: number): string {
  const exact = videos.filter(video => stem(name).toLowerCase() === stem(video.name).toLowerCase())
  if (exact.length === 1) return exact[0].id
  const episode = episodeNumber(name)
  if (episode) {
    const candidates = videos.filter(video => {
      const target = episodeNumber(video.name)
      return target && target.episode === episode.episode && (target.season ?? season) === (episode.season ?? season)
    })
    return candidates.length === 1 ? candidates[0].id : ''
  }
  return videos.length === 1 && !episodeNumber(videos[0].name) && !/S\d{1,3}[ ._-]*E\d/i.test(videos[0].name) ? videos[0].id : ''
}

export function subtitleLanguage(name: string): string {
  if (/双语|雙語|中英|chs[._-]eng|cht[._-]eng/i.test(name)) return 'zh-en'
  if (/简体|簡體|简中|簡中|chs|zh[._-]cn|sc\b/i.test(name)) return 'zh-CN'
  if (/繁体|繁體|繁中|cht|zh[._-]tw|tc\b/i.test(name)) return 'zh-TW'
  if (/英文|(?:^|[._ -])(?:eng|en)(?:[._ -]|$)/i.test(name)) return 'en'
  return ''
}

const crcTable = new Uint32Array(256).map((_, n) => {
  let c = n
  for (let i = 0; i < 8; i++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(data: Uint8Array) { let c = 0xffffffff; for (const b of data) c = crcTable[(c ^ b) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }

// Read the central directory before inflating. Stream bounded compressed chunks,
// check actual output size and CRC, and never interpret archive paths as disk paths.
async function readZip(bytes: Uint8Array, signal?: AbortSignal): Promise<{ files: LocalSubtitle[]; ignored: number }> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  let end = -1
  for (let p = bytes.length - 22; p >= Math.max(0, bytes.length - 65557); p--) {
    if (view.getUint32(p, true) === 0x06054b50 && p + 22 + view.getUint16(p + 20, true) === bytes.length) { end = p; break }
  }
  if (end < 0) throw new Error('ZIP 不完整或格式不受支持，请在电脑上解压后选择字幕文件。')
  const count = view.getUint16(end + 10, true), centralSize = view.getUint32(end + 12, true), centralOffset = view.getUint32(end + 16, true)
  if (view.getUint16(end + 4, true) || view.getUint16(end + 6, true) || view.getUint16(end + 8, true) !== count || count > 2000 || centralOffset + centralSize !== end) throw new Error('不支持分卷、ZIP64 或超过 2000 个条目的压缩包。')
  const files: LocalSubtitle[] = []
  let offset = centralOffset, total = 0, ignored = 0
  for (let i = 0; i < count; i++) {
    signal?.throwIfAborted()
    if (offset + 46 > end || view.getUint32(offset, true) !== 0x02014b50) throw new Error('ZIP 目录损坏。')
    const flags = view.getUint16(offset + 8, true), method = view.getUint16(offset + 10, true), crc = view.getUint32(offset + 16, true)
    const compressed = view.getUint32(offset + 20, true), size = view.getUint32(offset + 24, true), nameSize = view.getUint16(offset + 28, true)
    const extraSize = view.getUint16(offset + 30, true), commentSize = view.getUint16(offset + 32, true), local = view.getUint32(offset + 42, true)
    const next = offset + 46 + nameSize + extraSize + commentSize
    if (next > end) throw new Error('ZIP 目录不完整。')
    const name = new TextDecoder(flags & 0x800 ? 'utf-8' : 'gb18030').decode(bytes.subarray(offset + 46, offset + 46 + nameSize)).replace(/\\/g, '/')
    offset = next
    if (name.endsWith('/') || name.startsWith('__MACOSX/')) continue
    if (!isSubtitle(name)) { ignored++; continue }
    if (name.startsWith('/') || name.split('/').includes('..') || /[\x00\r\n]/.test(name)) throw new Error('ZIP 中包含无效文件路径。')
    if (flags & 1) throw new Error('暂不支持加密 ZIP，请先在电脑上解压。')
    if (method !== 0 && method !== 8) throw new Error('ZIP 压缩方法不受支持，请先在电脑上解压。')
    if (!size || size > maxFileBytes || total + size > maxTotalBytes || files.length >= 100) throw new Error('每次最多 100 个字幕，单个 16 MiB、合计 64 MiB。')
    if (local + 30 > centralOffset || view.getUint32(local, true) !== 0x04034b50 || view.getUint16(local + 8, true) !== method || view.getUint16(local + 6, true) & 1) throw new Error('ZIP 文件头无效。')
    const start = local + 30 + view.getUint16(local + 26, true) + view.getUint16(local + 28, true)
    if (start + compressed > centralOffset) throw new Error('ZIP 文件内容不完整。')
    const data = new Uint8Array(size)
    let written = 0
    if (method === 0) {
      if (compressed !== size) throw new Error('ZIP 文件大小不一致。')
      data.set(bytes.subarray(start, start + compressed)); written = size
    } else {
      const inflater = new Inflate(chunk => {
        if (written + chunk.length > size) throw new Error('ZIP 实际解压大小超过声明大小。')
        data.set(chunk, written); written += chunk.length
      })
      for (let p = start; p < start + compressed; p += 4096) {
        signal?.throwIfAborted()
        const last = Math.min(p + 4096, start + compressed)
        inflater.push(bytes.subarray(p, last), last === start + compressed)
        if ((p - start) % 131072 === 0) await new Promise(resolve => setTimeout(resolve, 0))
      }
    }
    if (written !== size || crc32(data) !== crc) throw new Error(`字幕校验失败：${name}`)
    total += size
    files.push({ name, data, sha256: sha256(data) })
  }
  if (offset !== end) throw new Error('ZIP 目录大小不一致。')
  return { files, ignored }
}

export async function readLocalSubtitles(input: File[], signal?: AbortSignal): Promise<{ files: LocalSubtitle[]; ignored: number }> {
  const files: LocalSubtitle[] = []
  let ignored = 0, total = 0
  if (input.length > 100 || input.reduce((sum, file) => sum + file.size, 0) > maxTotalBytes) throw new Error('每次选择的文件不能超过 100 个或 64 MiB。')
  for (const file of input) {
    signal?.throwIfAborted()
    if (/\.(rar|7z)$/i.test(file.name)) throw new Error('RAR / 7z 请先在电脑上解压，再选择字幕文件。')
    if (!/\.zip$/i.test(file.name) && !isSubtitle(file.name)) { ignored++; continue }
    const bytes = new Uint8Array(await file.arrayBuffer())
    const result = /\.zip$/i.test(file.name) ? await readZip(bytes, signal) : { files: [{ name: file.name, data: bytes, sha256: sha256(bytes) }], ignored: 0 }
    ignored += result.ignored
    for (const sub of result.files) {
      total += sub.data.length
      if (!sub.data.length || sub.data.length > maxFileBytes || total > maxTotalBytes || files.length >= 100) throw new Error('每次最多 100 个字幕，单个 16 MiB、合计 64 MiB，不能导入空文件。')
      files.push(sub)
    }
  }
  return { files, ignored }
}
