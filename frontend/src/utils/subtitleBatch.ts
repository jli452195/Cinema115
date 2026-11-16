import { analyzeSubtitle, episodeNumber, hasAmbiguousSubtitleEpisode, type SubtitleMatchRow, type SubtitleMatchVideo } from './subtitleImport'
import { subtitleLanguages, subtitleTargetName } from './subtitleLibrary'

export type BatchSubtitleRow = SubtitleMatchRow & { versionId?: string; path?: string; file: { name: string; sourceKey?: string; sourceLabel?: string } }
export interface SubtitleBatchGroup { key: string; label: string; rows: BatchSubtitleRow[] }
export type SubtitleBatchMode = 'episode' | 'sequence' | 'language'
export interface SubtitleBatchOptions { mode: SubtitleBatchMode; season: string; startVideoId: string; language: string; onlyUnknownLanguage: boolean; selectMatched: boolean; protectedKeys?: ReadonlySet<string> }
export interface SubtitleBatchChange { key: string; videoId: string; language: string; selected: boolean }
export interface SubtitleBatchPreview { changes: SubtitleBatchChange[]; errors: string[]; warnings: string[]; skipped: number }
const natural = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
const normalized = (name: string) => name.replace(/\\/g, '/')
const directory = (name: string) => normalized(name).split('/').slice(0, -1).join('/')
const basename = (name: string) => normalized(name).split('/').pop() || name
const special = (name: string) => /(?:^|[ ._\[【(-])(?:SP|OVA|OAD|NCOP|NCED|OP|ED|PV)(?:\d|\b)|特典|特别篇|特別篇|总集篇|總集篇/i.test(basename(name))

export function subtitleBatchGroups(rows: BatchSubtitleRow[]): SubtitleBatchGroup[] {
  const groups = new Map<string, SubtitleBatchGroup>()
  for (const row of rows) {
    const folder = directory(row.path || row.file.name)
    const key = JSON.stringify([row.versionId ? `library:${row.versionId}` : row.file.sourceKey || 'local', folder, row.language, row.version || ''])
    let group = groups.get(key)
    if (!group) {
      const lang = subtitleLanguages.find(item => item.value === row.language)?.label || '未指定'
      group = { key, label: [row.file.sourceLabel || row.sourceLabel || '本地导入', folder, lang, row.version].filter(Boolean).join(' · '), rows: [] }
      groups.set(key, group)
    }
    group.rows.push(row)
  }
  return [...groups.values()]
}

export function subtitleVideoEpisode(video: SubtitleMatchVideo) {
  // 整理面板中编辑后的季集是权威值，非法值不能回退到原文件名。
  if ('season' in video || 'episode' in video) {
    return Number.isInteger(video.season) && video.season! >= 0 && video.season! <= 1000 && Number.isInteger(video.episode) && video.episode! >= 1 && video.episode! <= 10000
      ? { season: video.season!, episode: video.episode! } : undefined
  }
  return episodeNumber(video.name)
}

export function subtitleBatchVideos(videos: SubtitleMatchVideo[], season: string) {
  if (season === '') return []
  return videos.filter(video => {
    const number = subtitleVideoEpisode(video)
    return number && String(number.season ?? Number(season)) === season
  }).sort((a, b) => subtitleVideoEpisode(a)!.episode - subtitleVideoEpisode(b)!.episode || natural.compare(a.name, b.name))
}

export function previewSubtitleBatch(rows: BatchSubtitleRow[], allRows: BatchSubtitleRow[], videos: SubtitleMatchVideo[], options: SubtitleBatchOptions): SubtitleBatchPreview {
  const result: SubtitleBatchPreview = { changes: [], errors: [], warnings: [], skipped: 0 }
  if (!rows.length) return result
  const units = new Map<string, BatchSubtitleRow[]>()
  for (const row of rows) {
    const key = row.pairKey || row.key
    const unit = units.get(key) || []
    unit.push(row); units.set(key, unit)
  }
  const ordered = [...units.values()].sort((a, b) => natural.compare(a[0].file.name, b[0].file.name))
  const targets = subtitleBatchVideos(videos, options.season)
  const targetIndex = new Map<number, SubtitleMatchVideo[]>()
  for (const video of targets) {
    const episode = subtitleVideoEpisode(video)!.episode
    const list = targetIndex.get(episode) || []; list.push(video); targetIndex.set(episode, list)
  }
  const eligible = ordered.filter(unit => !special(unit[0].file.name))
  if (options.mode !== 'language') {
    if (!targets.length) result.errors.push('请选择有有效季集编号的目标季。')
    if (eligible.some(unit => hasAmbiguousSubtitleEpisode(unit[0].file.name))) result.errors.push('存在多集范围或小数集号，请逐项指定，不参与批量匹配。')
    if (options.mode === 'sequence') {
      if (!targets.some(video => video.id === options.startVideoId)) result.errors.push('请选择起始视频。')
      if ([...targetIndex.values()].some(list => list.length > 1)) result.errors.push('目标季包含同集多个视频版本，请先明确视频版本，不能按顺序匹配。')
      const known = eligible.map(unit => episodeNumber(unit[0].file.name))
      // 可识别的缺集不能降级成位置配对，否则会把缺集之后的字幕整体错移。
      if (known.some((number, i) => i > 0 && number && known[i - 1] && (number.episode !== known[i - 1]!.episode + 1 || number.season !== known[i - 1]!.season))) result.errors.push('字幕集号不连续、重复或跨季，请改用按集号匹配。')
      if (targets.some((video, i) => i > 0 && subtitleVideoEpisode(video)!.episode !== subtitleVideoEpisode(targets[i - 1])!.episode + 1)) result.errors.push('目标视频存在缺集，请改用按集号匹配。')
      if (known.some(number => number?.season !== undefined && String(number.season) !== options.season)) result.errors.push('字幕季号与目标季不一致，请改用按集号匹配或逐项指定。')
      result.warnings.push('按顺序只依据自然排序，无法验证实际内容；请核对完整预览，尤其注意缺集、不同剪辑及集号偏移。')
      const remaining = targets.length - Math.max(0, targets.findIndex(video => video.id === options.startVideoId))
      if (remaining !== eligible.length) result.warnings.push(`字幕共 ${eligible.length} 组，起始位置后视频共 ${remaining} 个，数量不一致；超出的字幕保持未匹配。`)
    }
  }
  let sequence = targets.findIndex(video => video.id === options.startVideoId)
  for (const unit of ordered) {
    if (unit[0].pairKey) {
      const pair = allRows.filter(row => row.pairKey === unit[0].pairKey)
      if (pair.some(row => /\.idx$/i.test(row.file.name)) && (pair.length !== 2 || unit.length !== 2 || !pair.some(row => /\.sub$/i.test(row.file.name)))) {
        result.errors.push('IDX/SUB 配对不完整或分属不同语言、版本，请先修正配对。'); continue
      }
    }
    const excluded = special(unit[0].file.name)
    let candidate = ''
    if (options.mode === 'sequence' && !excluded) candidate = targets[sequence++]?.id || ''
    if (options.mode === 'episode' && !excluded) {
      // 与导入时保持相同的源集号/目标集号歧义保护，不把发布第十三集盲配成目标第十三集。
      candidate = analyzeSubtitle(unit[0].file.name, targets, Number(options.season), 'tv', { sourceEpisodes: true }).videoId
    }
    const protectedUnit = unit.some(row => !!row.videoId || options.protectedKeys?.has(row.key))
    // 已有映射及用户手动清空的项目都保留；整对字幕必须一并保留。
    for (const row of unit) {
      const videoId = options.mode === 'language' || excluded || protectedUnit ? row.videoId : candidate
      const language = options.language !== '__keep' && (!options.onlyUnknownLanguage || !row.language) ? options.language : row.language
      const selected = options.selectMatched && !!videoId ? true : row.selected
      if (videoId !== row.videoId || language !== row.language || selected !== row.selected) result.changes.push({ key: row.key, videoId, language, selected })
      if (options.mode !== 'language' && (excluded || protectedUnit || !videoId)) result.skipped++
    }
    if (options.mode === 'sequence' && candidate && unit.some(row => row.videoId && row.videoId !== candidate)) result.errors.push('已有对应关系与本次顺序不一致，请调整起始视频或使用按集号匹配；原映射不会覆盖。')
  }
  // 只检查最终勾选且本次变更触及的目标；未选的候选版本不会写出文件，不应阻止操作。
  const updates = new Map(result.changes.map(change => [change.key, change]))
  const outputs = new Map<string, { key: string; changed: boolean }>()
  for (const row of allRows) {
    const change = updates.get(row.key), videoId = change?.videoId ?? row.videoId, language = change?.language ?? row.language
    if (!videoId || !(change?.selected ?? row.selected)) continue
    const key = `${videoId}/${subtitleTargetName('', row.file.name, language, row.version)}`.toLowerCase()
    const existing = outputs.get(key)
    if (existing && (change || existing.changed)) result.errors.push('本次操作会产生重复的字幕目标文件，请拆分语言或版本后再应用。')
    outputs.set(key, { key: row.key, changed: !!change })
  }
  result.errors = [...new Set(result.errors)]
  return result
}
