import type { TelegramChannelLink, TelegramChannelMetadata, TelegramChannelResource } from '../types/telegramUser'

export interface TelegramFileMetadata extends TelegramChannelMetadata {
  season?: number
  episodeNumber?: number
  resolution?: string
  dynamicRange?: string
  source?: string
  platform?: string
  videoCodec?: string
  bitDepth?: string
  frameRate?: string
  audio?: string
  subtitles?: string
  releaseGroup?: string
}

export interface TelegramFileSource {
  id: string
  title: string
  url: string
  postedAt: string
  channelTitle: string
}

// 外部清单只展开一次，原帖来源由页面聚合后附加，避免重复解析整页文件。
export interface TelegramFileResource extends TelegramChannelResource {
  fileSources?: TelegramFileSource[]
}

export interface TelegramFile {
  id: string
  url: string
  name: string
  hash: string
  sizeBytes: string
  metadata: TelegramFileMetadata
  sources: TelegramFileSource[]
  season?: number
  episodeNumber?: number
  version: string
  versionKey: string
}

export interface TelegramFileFilter {
  query?: string
  season?: string | number
  resolution?: string
  dynamicRange?: string
  sort?: 'episode' | 'size' | 'latest'
}

export interface TelegramFileGroup { id: string; title: string; files: TelegramFile[] }

type FileLink = TelegramChannelLink & { file?: { name: string; hash: string; sizeBytes: string } }
type FileResource = TelegramChannelResource & { version?: string }
const episodePattern = /(?:^|[^a-z0-9])S(\d{1,2})[ ._-]*E(\d{1,5}(?:\.\d{1,3})?)(?=$|[^a-z0-9])/ig
const episodeRangePattern = /(?:^|[^a-z0-9])S(\d{1,2})[ ._-]*E\d{1,5}(?:\.\d{1,3})?[ ._]*[-~～至][ ._]*(?:S\d{1,2}[ ._-]*)?E?\d{1,5}(?:\.\d{1,3})?(?=$|[^a-z0-9])/ig
const videoExtension = /\.(?:mkv|mp4|m4v|avi|mov|ts|m2ts|wmv|mpg|mpeg|webm)$/i
const naturalCompare = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' }).compare

function sizeKey(value: string): string { return value.replace(/^0+(?=\d)/, '') }

function ed2kFile(link: FileLink): { name: string; hash: string; sizeBytes: string } | undefined {
  const match = /^ed2k:\/\/\|file\|([^|\r\n]+)\|(\d+)\|([a-f0-9]{32})\|(?:[^\r\n]*\|)?\/$/i.exec(link.url)
  if (!match) return undefined
  let name = match[1]!
  // 文件名仅按路径解码一次，字面量加号不按查询参数转换为空格。
  try { name = decodeURIComponent(name) } catch { /* 无效转义保留原始文件名。 */ }
  const hash = match[3]!.toLowerCase(), sizeBytes = sizeKey(match[2]!)
  // URL 是文件身份的共同依据，服务端字段只补充同一文件的显示名称。
  const supplied = link.file
  if (supplied && supplied.hash.toLowerCase() === hash && sizeKey(String(supplied.sizeBytes)) === sizeBytes && supplied.name) name = supplied.name
  return { name, hash, sizeBytes }
}

function numbering(name: string, metadata: TelegramFileMetadata): { season?: number; episodeNumber?: number } {
  const ranges = [...name.matchAll(episodeRangePattern), ...(metadata.episode || '').matchAll(episodeRangePattern)]
  if (ranges.length) {
    // 范围表示包含多个发布集数，不能取起点冒充单集编号。
    return { season: Number.isFinite(metadata.season) ? metadata.season : Number(ranges[0]![1]), episodeNumber: undefined }
  }
  const matches = [...name.matchAll(episodePattern)]
  const declared = [...(metadata.episode || '').matchAll(episodePattern)]
  const explicit = matches.length === 1 ? matches[0] : declared.length === 1 ? declared[0] : undefined
  const season = Number.isFinite(metadata.season) ? metadata.season : explicit ? Number(explicit[1]) : undefined
  const episodeNumber = Number.isFinite(metadata.episodeNumber) ? metadata.episodeNumber : explicit ? Number(explicit[2]) : undefined
  return { season, episodeNumber }
}

function metadataFor(link: TelegramChannelLink, item: TelegramChannelResource): TelegramFileMetadata {
  // 多文件帖子不向文件继承父级季集或总体积；单文件可复用现有卡片字段。
  const metadata: TelegramFileMetadata = { ...(item.links.length === 1 ? item.metadata : undefined), ...link.metadata }
  if (metadata.specs) metadata.specs = [...metadata.specs]
  if (!metadata.resolution) metadata.resolution = metadata.specs?.find(value => /^\d{3,4}[pi]$/i.test(value))
  if (!metadata.dynamicRange) metadata.dynamicRange = metadata.specs?.find(value => /^(?:SDR|HDR(?:10\+?)?|DV|DoVi|HLG)(?:[ +/].*)?$/i.test(value))
  return metadata
}

function versionFor(name: string, item: FileResource, metadata: TelegramFileMetadata): { version: string; versionKey: string } {
  // 明确版本标题优先；缺少时只去掉集号与视频后缀，保留发布组、平台等差异。
  const stem = (item.version || name).replace(videoExtension, '').replace(episodeRangePattern, (_value, season: string) => `.S${season}`).replace(episodePattern, (_value, season: string) => `.S${season}`)
  const version = stem.replace(/[ ._-]+/g, ' ').trim() || name
  return { version, versionKey: versionIdentity(version, metadata) }
}

function versionIdentity(version: string, metadata: TelegramFileMetadata): string {
  const fields = [metadata.resolution, metadata.dynamicRange, metadata.source, metadata.platform, metadata.videoCodec, metadata.bitDepth, metadata.frameRate, metadata.audio, metadata.subtitles, metadata.releaseGroup]
  return [version.toLocaleLowerCase(), ...fields.map(value => value?.toLocaleLowerCase() || '')].join('|')
}

function completeMetadata(previous: TelegramFileMetadata, incoming: TelegramFileMetadata): TelegramFileMetadata {
  // 相同文件的后续来源只补齐缺失字段，冲突声明不覆盖已有展示信息。
  const result = { ...previous } as Record<string, unknown>
  for (const [field, value] of Object.entries(incoming)) {
    if (result[field] === undefined || result[field] === '' || Array.isArray(result[field]) && !(result[field] as unknown[]).length) result[field] = Array.isArray(value) ? [...value] : value
  }
  return result as TelegramFileMetadata
}

export function telegramFileSource(item: TelegramChannelResource): TelegramFileSource {
  return { id: item.id, title: item.title, url: item.sourceUrl, postedAt: item.postedAt, channelTitle: item.channel.title }
}

function appendSources(target: TelegramFileSource[], incoming: readonly TelegramFileSource[]) {
  for (const source of incoming) if (!target.some(entry => entry.id === source.id || source.url && entry.url === source.url)) target.push(source)
}

/** 将频道消息和已解析版本统一展开为文件；不把消息正文作为文件的搜索内容。 */
export function telegramFiles(resources: readonly TelegramFileResource[]): TelegramFile[] {
  const byFile = new Map<string, TelegramFile>()
  for (const item of resources) for (const link of item.links) {
    if (link.type !== 'ed2k') continue
    const parsed = ed2kFile(link)
    if (!parsed) continue
    const id = `${parsed.hash}:${parsed.sizeBytes}`, sources = item.fileSources?.length ? item.fileSources : [telegramFileSource(item)]
    const previous = byFile.get(id)
    if (previous) {
      appendSources(previous.sources, sources)
      previous.metadata = completeMetadata(previous.metadata, metadataFor(link, item))
      Object.assign(previous, numbering(previous.name, previous.metadata))
      previous.versionKey = versionIdentity(previous.version, previous.metadata)
      continue
    }
    const metadata = metadataFor(link, item)
    const coordinate = numbering(parsed.name, metadata)
    if (!metadata.episode && coordinate.season !== undefined && coordinate.episodeNumber !== undefined) metadata.episode = `S${String(coordinate.season).padStart(2, '0')}E${String(coordinate.episodeNumber).padStart(2, '0')}`
    const uniqueSources: TelegramFileSource[] = []
    appendSources(uniqueSources, sources)
    byFile.set(id, { id, url: link.url, ...parsed, metadata, sources: uniqueSources, ...coordinate, ...versionFor(parsed.name, item, metadata) })
  }
  return [...byFile.values()]
}

function fileTime(file: TelegramFile): number {
  return Math.max(0, ...file.sources.map(source => Date.parse(source.postedAt) || 0))
}

function compareOptional(a?: number, b?: number): number {
  if (a === undefined) return b === undefined ? 0 : 1
  return b === undefined ? -1 : a - b
}

function compareEpisode(a: TelegramFile, b: TelegramFile): number {
  return compareOptional(a.season, b.season) || compareOptional(a.episodeNumber, b.episodeNumber) || naturalCompare(a.version, b.version) || naturalCompare(a.name, b.name) || a.id.localeCompare(b.id)
}

/** 筛选后的数组独立排序，不修改缓存中的文件或来源。 */
export function filterTelegramFiles(files: readonly TelegramFile[], filter: TelegramFileFilter = {}): TelegramFile[] {
  const words = (filter.query || '').trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  const season = filter.season === undefined || filter.season === '' || filter.season === 'all' ? undefined : filter.season
  const filtered = files.filter(file => {
    if (season === 'unknown' ? file.season !== undefined : season !== undefined && file.season !== Number(season)) return false
    if (filter.resolution && filter.resolution !== 'all' && file.metadata.resolution?.toLocaleLowerCase() !== filter.resolution.toLocaleLowerCase()) return false
    if (filter.dynamicRange && filter.dynamicRange !== 'all' && file.metadata.dynamicRange?.toLocaleLowerCase() !== filter.dynamicRange.toLocaleLowerCase()) return false
    const searchable = [file.name, file.version, ...Object.values(file.metadata).flatMap(value => Array.isArray(value) ? value : [value])].join(' ').toLocaleLowerCase()
    return words.every(word => searchable.includes(word))
  })
  return filtered.sort((a, b) => {
    if (filter.sort === 'latest') return fileTime(b) - fileTime(a) || compareEpisode(a, b)
    if (filter.sort === 'size') {
      const left = BigInt(a.sizeBytes), right = BigInt(b.sizeBytes)
      if (left !== right) return left > right ? -1 : 1
    }
    return compareEpisode(a, b)
  })
}

export function groupTelegramFiles(files: readonly TelegramFile[]): TelegramFileGroup[] {
  const groups = new Map<string, TelegramFileGroup>()
  for (const file of files) {
    const previous = groups.get(file.versionKey)
    if (previous) previous.files.push(file)
    else groups.set(file.versionKey, { id: file.versionKey, title: file.version, files: [file] })
  }
  return [...groups.values()]
}
