import { Inflate } from 'fflate'
import { sha256 } from 'js-sha256'
import { maxSubtitleFiles, maxFileBytes, maxTotalBytes, subtitleLimitMessages, subtitleSelectionIssue } from './subtitleLimits'
import { isSubtitleBilingualAlias } from './subtitleNaming'

export { maxFileBytes, maxTotalBytes } from './subtitleLimits'
export interface LocalSubtitle { name: string; data: Uint8Array; sha256: string; sourceKey?: string; sourceLabel?: string; origin?: 'assrt' }
export interface LocalSubtitleSelection { localId: string; file: LocalSubtitle; videoId: string; language: string; version: string }
export interface SubtitleMatchingState { continuousScopes: string[]; manualRows: string[] }
export interface SubtitleMatchRow { key: string; pairKey?: string; sourceLabel?: string; file: { name: string }; videoId: string; language: string; version?: string; selected: boolean; reason: string }
export const isSubtitle = (name: string) => /\.(srt|ass|ssa|vtt|idx|sub)$/i.test(name)
export const isSubtitleArchive = (name: string) => /\.(zip|rar|7z)$/i.test(name)
export const splitSubtitleArchive = /(?:\.part\d+\.rar|\.[rz]\d{2,3}|\.(?:rar|7z|zip)\.\d{3,})$/i
export const formatSubtitleSize = (bytes: number) => bytes >= 1024 * 1024 ? `${(bytes / (1024 * 1024)).toFixed(1)} MiB` : `${(bytes / 1024).toFixed(1)} KiB`
const base = (name: string) => name.replace(/\\/g, '/').split('/').pop() || ''
const stem = (name: string) => base(name).replace(/\.[^.]+$/, '')
// Only shield explicitly labeled audio blocks; arbitrary numbers remain evidence.
const withoutAudioMetadata = (title: string) => title.replace(/(?:^|[^a-z0-9])(?:AAC|FLAC|ALAC|AC-?3|E-?AC-?3|DDP?|DTS(?:-HD)?|TRUEHD|OPUS|AUDIO)[ ._-]*\[[^\[\]]*\]/gi, ' ')
const episodeTitle = (name: string) => withoutAudioMetadata(stem(name))

// 空格裸集号只接受完整的技术后缀；未知文字、裸数字和修订号都保留为歧义。
function technicalEpisodeSuffix(suffix: string) {
  let technical = false
  const remainder = suffix.replace(/\b(?:DVDRip|BDRip|BluRay|WEB[ .-]?DL|WEBRip|HDTV|(?:2160|1080|720|576|480)p|[1-9]\d{2,3}[x×][1-9]\d{2,3}|[xh]26[45]|HEVC(?:-YUV420P10)?|AVC|FLAC|AAC|ALAC|AC3|DTS|OPUS|(?:8|10|12)bit)\b/gi, () => { technical = true; return ' ' })
    .replace(/\b(?:chs|cht|sc|tc|eng|jpn)\b/gi, ' ')
  if (!technical || !/^[\s._()[\]【】-]*$/.test(remainder)) return false
  const brackets: string[] = []
  const openingByClosing: Record<string, string> = { ')': '(', ']': '[', '】': '【' }
  for (const char of remainder) {
    if ('([【'.includes(char)) brackets.push(char)
    else if (char in openingByClosing && brackets.pop() !== openingByClosing[char]) return false
  }
  return brackets.length === 0
}

export function episodeNumber(name: string, workTitles: readonly string[] = []): { season?: number; episode: number } | undefined {
  const title = episodeTitle(name)
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
    /(?:^|[^a-z0-9])#\s*(\d{1,4})(?=$|[^a-z0-9])/gi,
    /(?:^|[ ._\[【-])EP?(\d{1,4})(?=$|[ ._\]】\[-])/gi,
    /^\s*(\d{1,3})(?=$|[ ._\[【-])/g,
    /[\[【](\d{1,3})[\]】]/g,
    /(?:\s-\s*|_)(\d{1,3})(?=$|[ ._\[【])/g,
  ]) for (const m of title.matchAll(pattern)) numbers.push(Number(m[1]))
  if (!numbers.length) {
    const spaced = title.match(/^(.+?)\s+(\d{1,4})(?=$|[\s([【])(.*)$/)
    // 仅在旧规则没有证据时尝试裸集号，避免数字标题或年份抢占明确集号。
    if (spaced) {
      const episode = Number(spaced[2])
      const prefix = spaced[1].replace(/\[[^\]]*\]|【[^】]*】/g, ' ')
      const suffix = isSubtitle(name)
        ? spaced[3].replace(/\.([^.]+)$/, (part, tag: string) => isSubtitleBilingualAlias(tag) ? '' : part)
        : spaced[3]
      // 常见尺寸、分辨率和编码数字不能独立充当新增格式的集号。
      if (/\p{L}/u.test(prefix) && !/\d/.test(spaced[1]) && !/(?:^|[^a-z0-9])(?:v|ver|version|rev|revision)[ ._-]*$/i.test(prefix)
        && episode >= 1 && episode <= 999 && ![240, 264, 265, 360, 480, 540, 576, 640, 704, 720, 768, 800, 848, 852, 854, 960].includes(episode)
        && technicalEpisodeSuffix(suffix)) numbers.push(episode)
    }
    // 只有片名与当前作品一致时，紧贴片名的两位数字才可作为集号。
    if (!numbers.length && workTitles.length && isSubtitle(name)) {
      const subtitleStem = stem(name).toLowerCase()
      const match = subtitleStem.match(/^(.+)(\d{2})$/)
      const titles = workTitles.map(title => title.trim().toLowerCase())
      if (match && !titles.includes(subtitleStem) && titles.includes(match[1])) numbers.push(Number(match[2]))
    }
  }
  const unique = [...new Set(numbers.filter(n => n > 0 && ![480, 576, 720].includes(n)))]
  return unique.length === 1 ? { episode: unique[0] } : undefined
}

function ambiguousEpisode(title: string) {
  return /(?:S\d+[ ._-]*E\d+|(?:^|[^a-z0-9])(?:EP?|#\s*)?\d{1,4})\s*(?:[-~～–+]\s*(?:EP?|#)?\s*\d{1,4}(?![\da-z])|E\d{1,4}(?![\da-z]))/i.test(title)
    || /(?:S\d+[ ._-]*E|(?:^|[ _\[【(-])(?:EP?|#\s*)?)(\d{1,4})\.\d/i.test(title)
    || /(?:^|[ ._\[【-])(?:SP|OVA|OAD|NCOP|NCED|OP|ED|PV)(?:\d|\b)|特典|特别篇|特別篇|总集篇|總集篇/i.test(title)
}

export interface SubtitleMatch {
  videoId: string
  confidence: 'high' | 'medium' | 'low' | 'none'
  reason: string
  candidates: { id: string; reason: string; score: number }[]
}
export interface SubtitleMatchVideo { id: string; name: string; season?: number; episode?: number }
export interface SubtitleSeasonRange { season: number; offset: number; episodeCount: number }
interface SubtitleMatchOptions {
  sourceEpisodes?: boolean
  continuousRanges?: readonly SubtitleSeasonRange[]
  workTitles?: readonly string[]
}

export function continuousSubtitleRanges(seasons: readonly { number: number; episodeCount: number }[], targetSeasons: readonly number[]): SubtitleSeasonRange[] {
  const targets = new Set(targetSeasons.filter(season => Number.isInteger(season) && season >= 1 && season <= 1000))
  const lastSeason = Math.max(0, ...targets)
  const counts = new Map(seasons.map(({ number, episodeCount }) => [number, episodeCount]))
  const ranges: SubtitleSeasonRange[] = []
  let offset = 0
  // 未选中的正式季也占用连续集号；特别篇不进入累计范围。
  for (let season = 1; season <= lastSeason; season++) {
    const episodeCount = counts.get(season)
    if (episodeCount === undefined || !Number.isSafeInteger(episodeCount) || episodeCount < 1 || !Number.isSafeInteger(offset + episodeCount)) return []
    if (targets.has(season)) ranges.push({ season, offset, episodeCount })
    offset += episodeCount
  }
  return ranges
}

export function continuousSubtitleRange(ranges: readonly SubtitleSeasonRange[], episode: number): SubtitleSeasonRange | undefined {
  return ranges.find(range => episode > range.offset && episode <= range.offset + range.episodeCount)
}

function videoEpisode(video: SubtitleMatchVideo) {
  // Editable target numbers are authoritative, even when currently invalid.
  // Videos from completed tasks have no such fields and use their final names.
  if ('season' in video || 'episode' in video) {
    const { season, episode } = video
    if (typeof season !== 'number' || !Number.isInteger(season) || season < 0 || season > 1000 || typeof episode !== 'number' || !Number.isInteger(episode) || episode < 1 || episode > 10000) return undefined
    return { season, episode }
  }
  return episodeNumber(video.name)
}

function titleTokens(name: string) {
  return new Set(stem(name).toLowerCase().replace(/\[[^\]]*\]|【[^】]*】/g, ' ').replace(/s\d+e\d+|\b(?:\d{3,4}p|bluray|bdrip|web[ .-]?dl|hevc|x26[45]|h26[45]|flac|aac|chs|cht|eng|ass|srt)\b/g, ' ').match(/[a-z]{3,}|[\u3400-\u9fff]{2,}/g) || [])
}

function rankCandidates(name: string, videos: SubtitleMatchVideo[]) {
  const title = titleTokens(name)
  const resolution = name.match(/\b(2160|1080|720|480)p\b/i)?.[1]
  return videos.map(video => {
    const tokens = titleTokens(video.name)
    const overlap = [...title].filter(token => tokens.has(token)).length
    const quality = !!resolution && video.name.toLowerCase().includes(`${resolution}p`)
    return { id: video.id, score: overlap * 2 + (quality ? 1 : 0), reason: [overlap ? '标题词相近' : '', quality ? '分辨率一致' : ''].filter(Boolean).join('、') || '季集候选，需核对版本' }
  }).sort((a, b) => b.score - a.score)
}

export function analyzeSubtitle(name: string, videos: SubtitleMatchVideo[], season: number, kind: 'movie' | 'tv' = 'tv', options: SubtitleMatchOptions = {}): SubtitleMatch {
  const no = (reason: string): SubtitleMatch => ({ videoId: '', confidence: 'none', reason, candidates: [] })
  if (kind === 'movie') {
    if (!videos.length) return no('请先选择视频')
    if (videos.length === 1) return { videoId: videos[0].id, confidence: 'low', reason: '已关联当前电影，请确认字幕版本', candidates: [] }
    return { videoId: '', confidence: 'medium', reason: '存在多个电影版本，请指定对应视频', candidates: rankCandidates(name, videos) }
  }
  const exact = videos.filter(video => !('season' in video || 'episode' in video) && stem(name).toLowerCase() === stem(video.name).toLowerCase())
  const episode = episodeNumber(name, options.workTitles)
  const ranges = episode?.season === undefined ? options.continuousRanges : undefined
  if (exact.length === 1 && !ranges) return { videoId: exact[0].id, confidence: 'high', reason: '文件主名称完全一致', candidates: [] }
  if (ambiguousEpisode(episodeTitle(name))) return no('特典或多集范围，请手动指定视频')
  if (episode) {
    const continuous = ranges && continuousSubtitleRange(ranges, episode.episode)
    if (ranges && !continuous) {
      const scope = ranges.length === 1 ? `第 ${ranges[0].season} 季` : '所选季'
      return no(`连续第 ${episode.episode} 集不在${scope}范围内`)
    }
    const targetSeason = episode.season ?? continuous?.season ?? season
    if (!Number.isInteger(targetSeason) || targetSeason < 0 || targetSeason > 1000) return no('请先选择字幕所属季')
    const targetEpisode = episode.episode - (continuous?.offset ?? 0)
    const candidates = videos.filter(video => {
      const target = videoEpisode(video)
      if (!target || (target.season ?? season) !== targetSeason) return false
      // 连续编号仅比较换算后的目标集号，避免上一季的字幕通过视频原始集号再次匹配。
      return target.episode === targetEpisode || !continuous && options.sourceEpisodes && episode.season === undefined && episodeNumber(video.name)?.episode === episode.episode
    })
    const label = `S${String(targetSeason).padStart(2, '0')}E${String(targetEpisode).padStart(2, '0')}`
    if (candidates.length === 1) {
      const target = videoEpisode(candidates[0])!
      const reason = continuous ? `连续第 ${episode.episode} 集唯一对应 ${label}`
        : target.episode !== episode.episode ? `发布第 ${episode.episode} 集唯一对应 S${String(targetSeason).padStart(2, '0')}E${String(target.episode).padStart(2, '0')}` : `${label} 唯一对应${episode.season === undefined ? '（使用所选季）' : ''}`
      return { videoId: candidates[0].id, confidence: 'high', reason, candidates: [] }
    }
    if (!candidates.length) return no(`${label} 没有对应视频，可能缺集或季不一致`)
    return { videoId: '', confidence: 'medium', reason: `${label} 有 ${candidates.length} 个版本，请确认候选`, candidates: rankCandidates(name, candidates) }
  }
  if (videos.length === 1 && !episodeNumber(videos[0].name) && !ambiguousEpisode(episodeTitle(videos[0].name)) && !/S\d+[ ._-]*E\d/i.test(videos[0].name)) {
    return { videoId: '', confidence: 'low', reason: '仅有一个视频，但未识别到集号，请确认', candidates: [{ id: videos[0].id, score: 0, reason: '当前唯一视频' }] }
  }
  return no('未识别到明确集号，请手动选择对应视频')
}

export function subtitlePairKey(name: string): string {
  return /\.(idx|sub)$/i.test(name) ? name.replace(/\\/g, '/').replace(/\.(idx|sub)$/i, '').toLowerCase() : ''
}

const crcTable = new Uint32Array(256).map((_, n) => {
  let c = n
  for (let i = 0; i < 8; i++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(data: Uint8Array) { let c = 0xffffffff; for (const b of data) c = crcTable[(c ^ b) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }

// Read the central directory before inflating. Stream bounded compressed chunks,
// check actual output size and CRC, and never interpret archive paths as disk paths.
export async function readSubtitleZip(bytes: Uint8Array, signal?: AbortSignal, limits = { bytes: maxTotalBytes, files: maxSubtitleFiles }): Promise<{ files: LocalSubtitle[]; ignored: number }> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  let end = -1
  for (let p = bytes.length - 22; p >= Math.max(0, bytes.length - 65557); p--) {
    if (view.getUint32(p, true) === 0x06054b50 && p + 22 + view.getUint16(p + 20, true) === bytes.length) { end = p; break }
  }
  if (end < 0) throw new Error('ZIP 不完整或格式不受支持，请在电脑上解压后选择字幕文件。')
  const count = view.getUint16(end + 10, true), centralSize = view.getUint32(end + 12, true), centralOffset = view.getUint32(end + 16, true)
  if (view.getUint16(end + 4, true) || view.getUint16(end + 6, true) || view.getUint16(end + 8, true) !== count || count > 2000 || centralOffset + centralSize !== end) throw new Error('不支持分卷、ZIP64 或超过 2000 个条目的压缩包。')
  const files: LocalSubtitle[] = []
  let offset = centralOffset, total = 0, ignored = 0, yieldedAt = performance.now()
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
    if (!size) throw new Error(`${name}：${subtitleLimitMessages.empty}`)
    if (size > maxFileBytes) throw new Error(`${name}：${subtitleLimitMessages.file}`)
    if (files.length >= limits.files) throw new Error(subtitleLimitMessages.count)
    if (total + size > limits.bytes) throw new Error(subtitleLimitMessages.total)
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
        // 按处理时长让出执行权，避免每个小字幕都等待一次定时器。
        if (performance.now() - yieldedAt >= 16) { await new Promise(resolve => setTimeout(resolve, 0)); yieldedAt = performance.now() }
      }
    }
    if (written !== size || crc32(data) !== crc) throw new Error(`字幕校验失败：${name}`)
    total += size
    files.push({ name, data, sha256: sha256(data) })
    if (performance.now() - yieldedAt >= 16) { await new Promise(resolve => setTimeout(resolve, 0)); yieldedAt = performance.now() }
  }
  if (offset !== end) throw new Error('ZIP 目录大小不一致。')
  signal?.throwIfAborted()
  return { files, ignored }
}

export async function readLocalSubtitles(input: File[], signal?: AbortSignal): Promise<{ files: LocalSubtitle[]; ignored: number }> {
  const files: LocalSubtitle[] = []
  let ignored = 0, total = 0
  if (input.length > maxSubtitleFiles) throw new Error(`每次最多选择 ${maxSubtitleFiles} 个文件，请减少选择。`)
  if (input.reduce((sum, file) => sum + file.size, 0) > maxTotalBytes) throw new Error('所选文件总大小不能超过 64 MiB。')
  for (const file of input) {
    signal?.throwIfAborted()
    if (splitSubtitleArchive.test(file.name)) throw new Error('不支持分卷压缩包，请在电脑上解压后选择字幕。')
    const archive = isSubtitleArchive(file.name)
    if (!archive && !isSubtitle(file.name)) { ignored++; continue }
    if (!archive && !file.size) throw new Error(`${file.name}：${subtitleLimitMessages.empty}`)
    if (!archive && file.size > maxFileBytes) throw new Error(`${file.name}：${subtitleLimitMessages.file}`)
    const bytes = new Uint8Array(await file.arrayBuffer())
    signal?.throwIfAborted()
    const sourceKey = archive ? `archive:${sha256(bytes)}` : undefined
    let result: { files: LocalSubtitle[]; ignored: number }
    if (archive) {
      const { readSubtitleArchive } = await import('./subtitleArchiveClient')
      result = await readSubtitleArchive(bytes, /\.zip$/i.test(file.name) ? 'zip' : /\.rar$/i.test(file.name) ? 'rar' : '7z', { bytes: maxTotalBytes - total, files: maxSubtitleFiles - files.length }, signal)
    } else result = { files: [{ name: file.name, data: bytes, sha256: sha256(bytes) }], ignored: 0 }
    ignored += result.ignored
    for (const sub of result.files) {
      total += sub.data.length
      if (!sub.data.length) throw new Error(`${sub.name}：${subtitleLimitMessages.empty}`)
      if (sub.data.length > maxFileBytes) throw new Error(`${sub.name}：${subtitleLimitMessages.file}`)
      const issue = subtitleSelectionIssue(files.length + 1, total)
      if (issue) throw new Error(issue)
      files.push({ ...sub, sourceKey, sourceLabel: archive ? file.name : undefined })
    }
  }
  return { files, ignored }
}

export function mergeSubtitleFiles<T extends LocalSubtitle>(current: T[], incoming: LocalSubtitle[], append: boolean): { kept: T[]; added: LocalSubtitle[]; duplicates: number } {
  const kept = append ? current : []
  const key = (file: LocalSubtitle) => `${file.sourceKey || 'local'}\x00${file.name.toLowerCase()}\x00${file.sha256}`
  const seen = new Set(kept.map(key))
  const added: LocalSubtitle[] = []
  let total = kept.reduce((sum, file) => sum + file.data.length, 0), duplicates = 0
  for (const file of incoming) {
    const identity = key(file)
    if (seen.has(identity)) { duplicates++; continue }
    seen.add(identity); total += file.data.length
    const issue = subtitleSelectionIssue(kept.length + added.length + 1, total)
    if (issue) throw new Error(`${issue}也可选择替换当前列表。`)
    added.push(file)
  }
  return { kept, added, duplicates }
}
