import type { OrganizeRow } from '../types/manualOrganize'

export interface GardenMappingSource { episode: number; endEpisode: number }
const cleanName = (name: string) => name.replace(/[\x00-\x1f/\\:*?"<>|]/g, '_').replace(/^[ .]+|[ .]+$/g, '')
const pad = (n: number) => String(n).padStart(2, '0')

export function gardenTargetName(row: OrganizeRow, title: string, date: string, kind: string) {
  if (kind === 'tv' && (!Number.isInteger(row.season) || row.season < 0 || !Number.isInteger(row.episode) || row.episode < 1 || row.endEpisode !== '' && row.endEpisode !== 0 && (!Number.isInteger(row.endEpisode) || row.endEpisode < row.episode))) return '请填写目标季集'
  let name = cleanName(title)
  if (kind === 'movie') { if (date.length >= 4) name += ` (${date.slice(0, 4)})` }
  else name += ` - S${pad(row.season)}E${pad(row.episode)}${Number(row.endEpisode) > row.episode ? `-E${pad(Number(row.endEpisode))}` : ''}`
  if (cleanName(row.suffix.trim())) name += ` - ${cleanName(row.suffix.trim())}`
  return name + (row.name.match(/\.[^.]+$/)?.[0].toLowerCase() || '')
}

// Called only by an explicit bulk action. Suggestions always derive from the
// original publisher number, preserving both gaps and multi-episode files.
export function applyGardenMapping(rows: OrganizeRow[], sources: Record<string, GardenMappingSource>, season: number, from: number, to: number, sequential = false) {
  if (!Number.isInteger(from) || from < 1 || from > 10000 || !Number.isInteger(to) || to < 1 || to > 10000) throw new Error('请填写有效的发布起始集和目标起始集')
  const changes: { row: OrganizeRow; episode: number; endEpisode: number | '' }[] = []
  let next = to
  for (const row of rows.filter(row => row.selected)) {
    const source = sources[row.id]
    if (!sequential && (!source || source.episode < 1)) continue
    const width = source && source.endEpisode > source.episode ? source.endEpisode - source.episode : 0
    const episode = sequential ? next : source.episode - from + to
    if (!Number.isInteger(season) || season < 0 || season > 1000 || !Number.isInteger(episode) || episode < 1 || episode + width > 10000) throw new Error('偏移后的季集号超出范围，请调整起始集')
    changes.push({ row, episode, endEpisode: width ? episode + width : '' })
    next = episode + width + 1
  }
  const undo = changes.map(({ row }) => ({ id: row.id, season: row.season, episode: row.episode, endEpisode: row.endEpisode }))
  for (const change of changes) Object.assign(change.row, { season, episode: change.episode, endEpisode: change.endEpisode })
  return undo
}
