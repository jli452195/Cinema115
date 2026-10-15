import { Inflate } from 'fflate'
import { sha256 } from 'js-sha256'
import type { SubtitleVideo } from '../types/subtitleImport'

export const maxFileBytes = 16 * 1024 * 1024
export const maxTotalBytes = 64 * 1024 * 1024
export interface LocalSubtitle { name: string; data: Uint8Array; sha256: string }
export const isSubtitle = (name: string) => /\.(srt|ass|ssa|vtt|idx|sub)$/i.test(name)
const splitArchiveName = /(?:\.part\d+\.rar|\.[rz]\d{2,3}|\.(?:rar|7z|zip)\.\d{3,})$/i
const base = (name: string) => name.replace(/\\/g, '/').split('/').pop() || ''
const stem = (name: string) => base(name).replace(/\.[^.]+$/, '')

export function episodeNumber(name: string): { season?: number; episode: number } | undefined {
  const title = stem(name)
  // Ranges, specials, resolutions, years and release revisions are not episodes.
  if (ambiguousEpisode(title)) return undefined
  const explicit = [...title.matchAll(/(?:^|[^a-z0-9])S(\d{1,3})[ ._-]*E(\d{1,4})(?=$|[^a-z0-9])/gi)]
  if (explicit.length) {
    const keys = new Set(explicit.map(m => `${Number(m[1])}:${Number(m[2])}`))
    return keys.size === 1 ? { season: Number(explicit[0][1]), episode: Number(explicit[0][2]) } : undefined
  }
  const numbers: number[] = []
  for (const pattern of [
    /第\s*(\d{1,4})\s*[集话話]/g,
    /(?:^|[ ._\[【-])EP?(\d{1,4})(?=$|[ ._\]】\[-])/gi,
    /^\s*(\d{1,3})(?=$|[ ._\[【-])/g,
    /[\[【](\d{1,3})[\]】]/g,
    /(?:\s-\s*|_)(\d{1,3})(?=$|[ ._\[【])/g,
  ]) for (const m of title.matchAll(pattern)) numbers.push(Number(m[1]))
  const unique = [...new Set(numbers.filter(n => n > 0 && ![480, 576, 720].includes(n)))]
  return unique.length === 1 ? { episode: unique[0] } : undefined
}

function ambiguousEpisode(title: string) {
  return /(?:S\d+[ ._-]*E\d+|\bEP?\d+|\b\d{1,3})\s*(?:[-~～–+]\s*(?:E(?:P)?)?\d{1,3}(?![\da-z])|E\d{1,4}(?![\da-z]))/i.test(title)
    || /(?:S\d+[ ._-]*E|(?:^|[ _\[【-])(?:EP?)?)(\d{1,3})\.\d/i.test(title)
    || /(?:^|[ ._\[【-])(?:SP|OVA|OAD|NCOP|NCED|OP|ED|PV)(?:\d|\b)|特典|特别篇|特別篇|总集篇|總集篇/i.test(title)
}

export interface SubtitleMatch {
  videoId: string
  confidence: 'high' | 'medium' | 'low' | 'none'
  reason: string
  candidates: { id: string; reason: string; score: number }[]
}

function titleTokens(name: string) {
  return new Set(stem(name).toLowerCase().replace(/\[[^\]]*\]|【[^】]*】/g, ' ').replace(/s\d+e\d+|\b(?:\d{3,4}p|bluray|bdrip|web[ .-]?dl|hevc|x26[45]|h26[45]|flac|aac|chs|cht|eng|ass|srt)\b/g, ' ').match(/[a-z]{3,}|[\u3400-\u9fff]{2,}/g) || [])
}

function rankCandidates(name: string, videos: SubtitleVideo[]) {
  const title = titleTokens(name)
  const resolution = name.match(/\b(2160|1080|720|480)p\b/i)?.[1]
  return videos.map(video => {
    const tokens = titleTokens(video.name)
    const overlap = [...title].filter(token => tokens.has(token)).length
    const quality = !!resolution && video.name.toLowerCase().includes(`${resolution}p`)
    return { id: video.id, score: overlap * 2 + (quality ? 1 : 0), reason: [overlap ? '标题词相近' : '', quality ? '分辨率一致' : ''].filter(Boolean).join('、') || '季集候选，需核对版本' }
  }).sort((a, b) => b.score - a.score)
}

export function analyzeSubtitle(name: string, videos: SubtitleVideo[], season: number): SubtitleMatch {
  const no = (reason: string): SubtitleMatch => ({ videoId: '', confidence: 'none', reason, candidates: [] })
  const exact = videos.filter(video => stem(name).toLowerCase() === stem(video.name).toLowerCase())
  if (exact.length === 1) return { videoId: exact[0].id, confidence: 'high', reason: '文件主名称完全一致', candidates: [] }
  if (ambiguousEpisode(stem(name))) return no('特典或多集范围，请手动指定视频')
  const episode = episodeNumber(name)
  if (episode) {
    if (!Number.isInteger(season) || season < 0 || season > 1000) return no('请填写有效默认季')
    const candidates = videos.filter(video => {
      const target = episodeNumber(video.name)
      return target && target.episode === episode.episode && (target.season ?? season) === (episode.season ?? season)
    })
    const label = `S${String(episode.season ?? season).padStart(2, '0')}E${String(episode.episode).padStart(2, '0')}`
    if (candidates.length === 1) return { videoId: candidates[0].id, confidence: 'high', reason: `${label} 唯一对应${episode.season === undefined ? '（使用默认季）' : ''}`, candidates: [] }
    if (!candidates.length) return no(`${label} 没有对应视频，可能缺集或季不一致`)
    return { videoId: '', confidence: 'medium', reason: `${label} 有 ${candidates.length} 个版本，请确认候选`, candidates: rankCandidates(name, candidates) }
  }
  if (videos.length === 1 && !episodeNumber(videos[0].name) && !ambiguousEpisode(stem(videos[0].name)) && !/S\d+[ ._-]*E\d/i.test(videos[0].name)) {
    return { videoId: '', confidence: 'low', reason: '仅有一个视频，但未识别到集号，请确认', candidates: [{ id: videos[0].id, score: 0, reason: '当前唯一视频' }] }
  }
  return no('未识别到明确集号，请按顺序匹配或手动选择')
}

export function matchSubtitle(name: string, videos: SubtitleVideo[], season: number): string { return analyzeSubtitle(name, videos, season).videoId }

export function subtitlePairKey(name: string): string {
  return /\.(idx|sub)$/i.test(name) ? name.replace(/\\/g, '/').replace(/\.(idx|sub)$/i, '').toLowerCase() : ''
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
    if (flags & 0x2041) throw new Error('不支持加密压缩包，请在电脑上解压后选择字幕。')
    if (view.getUint16(offset - (46 + nameSize + extraSize + commentSize) + 34, true)) throw new Error('不支持分卷压缩包，请在电脑上解压后选择字幕。')
    if (name.endsWith('/') || name.startsWith('__MACOSX/')) continue
    if (!isSubtitle(name)) { ignored++; continue }
    if (!name || name.length > 2048 || /^(?:\/|[a-z]:)/i.test(name) || name.split('/').includes('..') || /[\x00\r\n]/.test(name)) throw new Error('ZIP 中包含无效文件路径。')
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
    if (splitArchiveName.test(file.name)) throw new Error('不支持分卷压缩包，请在电脑上解压后选择字幕。')
    if (!/\.(zip|rar|7z)$/i.test(file.name) && !isSubtitle(file.name)) { ignored++; continue }
    const bytes = new Uint8Array(await file.arrayBuffer())
    signal?.throwIfAborted()
    let result: { files: LocalSubtitle[]; ignored: number }
    if (/\.zip$/i.test(file.name)) result = await readZip(bytes, signal)
    else if (/\.(rar|7z)$/i.test(file.name)) {
      const { readSubtitleArchive } = await import('./subtitleArchiveClient')
      result = await readSubtitleArchive(bytes, /\.rar$/i.test(file.name) ? 'rar' : '7z', { bytes: maxTotalBytes - total, files: 100 - files.length }, signal)
    } else result = { files: [{ name: file.name, data: bytes, sha256: sha256(bytes) }], ignored: 0 }
    ignored += result.ignored
    for (const sub of result.files) {
      total += sub.data.length
      if (!sub.data.length || sub.data.length > maxFileBytes || total > maxTotalBytes || files.length >= 100) throw new Error('每次最多 100 个字幕，单个 16 MiB、合计 64 MiB，不能导入空文件。')
      files.push(sub)
    }
  }
  return { files, ignored }
}
