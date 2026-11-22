import { episodeNumber, isSubtitle, type SubtitleMatchRow, type SubtitleMatchVideo } from './subtitleImport'
import { subtitleMetadata, subtitleTargetName } from './subtitleNaming'

export type SubtitlePresentationRow = SubtitleMatchRow & {
  versionId?: string
  path?: string
  file: SubtitleMatchRow['file'] & { sourceKey?: string; sourceLabel?: string }
}

export interface SubtitleVariantGroup<Row extends SubtitlePresentationRow = SubtitlePresentationRow> {
  key: string
  scopeKey: string
  hint: string
  label: string
  rows: Row[]
  covered: number
  total: number
  // 保留未覆盖的视频原名，避免同集不同视频版本被合并。
  missing: string[]
}

export interface SubtitleOutputConflict {
  key: string
  videoId: string
  rowKeys: string[]
  name: string
}

const normalized = (path: string) => path.replace(/\\/g, '/')
const basename = (path: string) => normalized(path).split('/').pop() || ''
const directory = (path: string) => normalized(path).split('/').slice(0, -1).join('/')
const nonVariant = /^(?:MULTI|DUAL|UTF|UTFBOM|ANSI|ASCII|GBK|BIG|HANS|HANT|CHN|JAP|ESP|FIL|YUE|CMN|NAN|WUU|HAK|AAC|FLAC|ALAC|EAC|DDP|DTS|TRUEHD|OPUS|PCM|AVC|HEVC|XVID|DIVX|HDR|HDRIP|SDR|DOVI|UHD|FHD|BDRIP|BLURAY|DVD|DVDRIP|WEB|WEBDL|WEBRIP|HDTV|REMUX|RAW|LOSSLESS|PROPER|REPACK|FINAL|REV|REVISION|VER|VERSION)$/

export function subtitleVariantHint(name: string): string {
  const file = basename(name)
  if (!isSubtitle(file)) return ''
  // 只认紧邻扩展名、由句点独立分隔的大写字母；不越过语言等后缀找标记。
  const hint = file.match(/\.([A-Z]{3,12})\.[^.]+$/)?.[1] || ''
  if (!hint || nonVariant.test(hint) || isSubtitle(`.${hint}`)) return ''
  const metadata = subtitleMetadata(hint)
  if (metadata.language || metadata.variants.length || metadata.flags.length) return ''
  // 两字母标记不识别；三字母语言别名沿用标准归一化，避免把 FRA、NLD 等当成版本。
  if (hint.length === 3 && new Intl.Locale(hint.toLowerCase()).language.length === 2) return ''
  // 纯十六进制片段更可能是校验值；数字、年份、编码和修订号已被字母规则排除。
  return /^[A-F]{4,}$/.test(hint) ? '' : hint
}

export function subtitleScopeKey(row: Pick<SubtitlePresentationRow, 'versionId' | 'path' | 'file'>): string {
  // 库版本 ID 属于来源身份；用户填写的输出版本和语言均不能改变此范围。
  const source = row.versionId ? ['library', row.versionId] : ['file', row.file.sourceKey || 'local']
  return JSON.stringify([source, directory(row.path || row.file.name)])
}

export function subtitleVariantHints(rows: readonly SubtitleMatchRow[]): Map<string, string> {
  const hints = new Map(rows.map(row => [row.key, subtitleVariantHint(row.file.name)]))
  const pairs = new Map<string, SubtitleMatchRow[]>()
  for (const row of rows) if (row.pairKey) { const pair = pairs.get(row.pairKey) || []; pair.push(row); pairs.set(row.pairKey, pair) }
  for (const pair of pairs.values()) {
    const known = new Set(pair.map(row => hints.get(row.key)).filter(Boolean))
    // 已确认的 IDX/SUB 配对共享标记，允许扩展名前的大小写不同，不扩大到其他文件。
    if (known.size === 1 && pair.length === 2 && pair.some(row => /\.idx$/i.test(row.file.name)) && pair.some(row => /\.sub$/i.test(row.file.name))) {
      const hint = [...known][0]!
      for (const row of pair) hints.set(row.key, hint)
    }
  }
  return hints
}

function videoSeason(video: SubtitleMatchVideo): number | undefined {
  // 当前编辑字段优先；季号缺失或非法时不回退到旧文件名。
  if ('season' in video || 'episode' in video) return Number.isInteger(video.season) && video.season! >= 0 && video.season! <= 1000 ? video.season : undefined
  return episodeNumber(video.name)?.season
}

const seasonLabel = (season: number) => `S${String(season).padStart(2, '0')}`

export function subtitleVariantGroups<Row extends SubtitlePresentationRow>(rows: readonly Row[], videos: readonly SubtitleMatchVideo[]): SubtitleVariantGroup<Row>[] {
  const hints = subtitleVariantHints(rows)
  const videoIndex = new Map<string, SubtitleMatchVideo>()
  for (const video of videos) if (video.id.trim() && !videoIndex.has(video.id)) videoIndex.set(video.id, video)
  const currentVideos = [...videoIndex.values()]
  const seasons = new Map(currentVideos.map(video => [video.id, videoSeason(video)]))
  const groups = new Map<string, SubtitleVariantGroup<Row>>()
  for (const row of rows) {
    const scopeKey = subtitleScopeKey(row), hint = hints.get(row.key) || ''
    const key = JSON.stringify([scopeKey, hint])
    let group = groups.get(key)
    if (!group) {
      // 目录仍保留在行数据中供详情展示，短标签只使用来源显示名与文件名标记。
      const sourceLabel = row.file.sourceLabel || row.sourceLabel || (row.versionId ? '字幕库' : '本地导入')
      group = { key, scopeKey, hint, label: [sourceLabel, hint ? `文件名标记 ${hint}` : '未识别标记'].join(' · '), rows: [], covered: 0, total: 0, missing: [] }
      groups.set(key, group)
    }
    group.rows.push(row)
  }
  for (const group of groups.values()) {
    // 覆盖只信当前视频中的实际关联，不推测字幕集号，也不把字幕文件数当作集数。
    const linked = new Set(group.rows.map(row => row.videoId).filter(id => videoIndex.has(id)))
    const linkedSeasons = new Set<number>()
    let unknownSeason = false
    for (const id of linked) {
      const season = seasons.get(id)
      if (season === undefined) unknownSeason = true
      else linkedSeasons.add(season)
    }
    const known = [...linkedSeasons].sort((a, b) => a - b)
    const targets = linked.size && !unknownSeason ? currentVideos.filter(video => linkedSeasons.has(seasons.get(video.id)!)) : currentVideos
    let scopeLabel = known.length > 1 ? `跨季 ${known.map(seasonLabel).join(' / ')}` : known.length ? seasonLabel(known[0]) : ''
    if (!linked.size || unknownSeason) scopeLabel = [scopeLabel, linked.size ? '含季号不明，按所选视频' : '未关联，按所选视频'].filter(Boolean).join(' · ')
    if (!currentVideos.length) scopeLabel = '暂无所选视频'
    group.label += ` · ${scopeLabel}`
    group.covered = linked.size
    group.total = targets.length
    group.missing = targets.filter(video => !linked.has(video.id)).map(video => video.name)
  }
  return [...groups.values()]
}

export function subtitleOutputConflicts(rows: readonly SubtitleMatchRow[]): SubtitleOutputConflict[] {
  const outputs = new Map<string, SubtitleOutputConflict>()
  for (const row of rows) {
    if (!row.selected || !row.videoId.trim()) continue
    const name = subtitleTargetName('', row.file.name, row.language, row.version)
    // 视频 ID 是不透明标识，仅对最终输出名忽略大小写。
    const key = JSON.stringify([row.videoId, name.toLowerCase()])
    let output = outputs.get(key)
    if (!output) {
      output = { key, videoId: row.videoId, rowKeys: [], name }
      outputs.set(key, output)
    }
    output.rowKeys.push(row.key)
  }
  return [...outputs.values()].filter(output => output.rowKeys.length > 1)
}
