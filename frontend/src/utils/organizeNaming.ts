import type { OrganizeRow, OrganizeSubtitle } from '../types/manualOrganize'

export interface OrganizeMappingSource { episode: number; endEpisode?: number }
const cleanName = (name: string) => name.replace(/[\x00-\x1f/\\:*?"<>|]/g, '_').replace(/^[ .]+|[ .]+$/g, '')
const pad = (n: number) => String(n).padStart(2, '0')

export function organizeTargetName(row: OrganizeRow, title: string, date: string, kind: string) {
  if (kind === 'tv' && (!Number.isInteger(row.season) || row.season < 0 || !Number.isInteger(row.episode) || row.episode < 1 || row.endEpisode !== '' && row.endEpisode !== 0 && (!Number.isInteger(row.endEpisode) || row.endEpisode < row.episode))) return '请填写目标季集'
  let name = cleanName(title)
  if (kind === 'movie') { if (date.length >= 4) name += ` (${date.slice(0, 4)})` }
  else name += ` - S${pad(row.season)}E${pad(row.episode)}${Number(row.endEpisode) > row.episode ? `-E${pad(Number(row.endEpisode))}` : ''}`
  if (cleanName(row.suffix.trim())) name += ` - ${cleanName(row.suffix.trim())}`
  return name + (row.titleOnly ? '' : row.name.match(/\.[^.]+$/)?.[0].toLowerCase() || '')
}

export function organizeSubtitleName(videoName: string, subtitle: OrganizeSubtitle) {
  if (videoName === '请填写目标季集') return videoName
  return videoName.replace(/\.[^.]+$/, '') + (subtitle.tail || '') + (subtitle.file.name.match(/\.[^.]+$/)?.[0].toLowerCase() || '')
}

// Offset uses original publisher numbers; sequential numbering keeps the
// currently edited spans. Both actions validate all rows before changing any.
export function applyOrganizeMapping(rows: OrganizeRow[], sources: Record<string, OrganizeMappingSource>, season: number, from: number, to: number, sequential = false) {
  if (!Number.isInteger(from) || from < 1 || from > 10000 || !Number.isInteger(to) || to < 1 || to > 10000) throw new Error('请填写有效的发布起始集和目标起始集')
  const changes: { row: OrganizeRow; episode: number; endEpisode: number | '' }[] = []
  let next = to
  for (const row of rows.filter(row => row.selected)) {
    const source = sequential ? row : sources[row.id]
    if (!sequential && (!source || source.episode < 1)) continue
    const end = Number(source?.endEpisode ?? 0)
    if (!Number.isInteger(end) || end < 0 || end > 10000 || end > 0 && (!Number.isInteger(source.episode) || source.episode < 1 || end < source.episode)) throw new Error('起止集范围无效，请先校正连集编号')
    const width = end > 0 && end > source.episode ? end - source.episode : 0
    const episode = sequential ? next : source.episode - from + to
    if (!Number.isInteger(season) || season < 0 || season > 1000 || !Number.isInteger(episode) || episode < 1 || episode + width > 10000) throw new Error('偏移后的季集号超出范围，请调整起始集')
    changes.push({ row, episode, endEpisode: width ? episode + width : '' })
    next = episode + width + 1
  }
  const undo = changes.map(({ row }) => ({ id: row.id, season: row.season, episode: row.episode, endEpisode: row.endEpisode }))
  for (const change of changes) Object.assign(change.row, { season, episode: change.episode, endEpisode: change.endEpisode })
  return undo
}
