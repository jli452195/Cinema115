import type { OrganizeRow } from '../types/manualOrganize'

export interface OrganizeMappingSource { episode: number; endEpisode?: number }
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
