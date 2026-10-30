import type { OrganizeFile, OrganizeRow } from '../types/organize'

export interface OrganizeMappingSource { episode: number }
export function organizeFileRow(file: OrganizeFile, selected: boolean): OrganizeRow {
  return { ...file, selected, episodeSpan: file.endEpisode ? file.endEpisode - file.episode + 1 : 1 }
}
// Keep the parsed span independent of editable target numbers. A joined file
// names only its start, while the next file still advances by the whole span.
export function applyOrganizeMapping(rows: OrganizeRow[], sources: Record<string, OrganizeMappingSource>, season: number, from: number, to: number, sequential = false) {
  if (!Number.isInteger(from) || from < 1 || from > 10000 || !Number.isInteger(to) || to < 1 || to > 10000) throw new Error('请填写有效的发布起始集和目标起始集')
  const changes: { row: OrganizeRow; episode: number }[] = []
  let next = to
  for (const row of rows.filter(row => row.selected)) {
    const source = sources[row.id]
    if (!sequential && (!source || source.episode < 1)) continue
    const width = row.episodeSpan - 1
    if (!Number.isInteger(width) || width < 0 || width >= 10000) throw new Error('连集跨度无效，请重新读取文件清单')
    const episode = sequential ? next : source!.episode - from + to
    if (!Number.isInteger(season) || season < 0 || season > 1000 || !Number.isInteger(episode) || episode < 1 || episode + width > 10000) throw new Error('偏移后的季集号超出范围，请调整起始集')
    changes.push({ row, episode })
    next = episode + width + 1
  }
  const undo = changes.map(({ row }) => ({ id: row.id, season: row.season, episode: row.episode }))
  for (const change of changes) Object.assign(change.row, { season, episode: change.episode })
  return undo
}
