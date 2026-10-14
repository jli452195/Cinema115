import type { SeasonDetail } from '../composables/useTMDBSeasons'

export interface NumberingInfo { groupId: string; name: string; type: number; revision: number; savedAt?: number }
export interface EpisodeGroupSummary { id: string; name: string; description: string; type: number; groupCount: number; episodeCount: number }
export interface EpisodeGroup extends EpisodeGroupSummary { seasons: SeasonDetail[] }
export interface NumberingChoices {
  current: NumberingInfo; snapshot: EpisodeGroup | null
  defaultSeasons: { number: number; name: string; episodeCount: number }[]
  defaultEpisodeCount: number; groups: EpisodeGroupSummary[]
  defaultError?: string; groupsError?: string
}
export interface NumberingPreview { group: EpisodeGroup; fingerprint: string; changed: boolean }
export const episodeGroupTypes: Record<number, string> = { 1: '首播顺序', 2: '绝对顺序', 3: 'DVD 顺序', 4: '数字发行', 5: '故事篇章', 6: '制片顺序', 7: '电视播出' }
export function numberingLabel(value?: NumberingInfo) { return value?.groupId ? `${value.name}（${episodeGroupTypes[value.type] || '剧集组'}）` : 'TMDB 默认季集' }
export function episodeCode(season: number, episode: number) { return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}` }

export function numberingChanges(before: EpisodeGroup, after: EpisodeGroup) {
  const flatten = (group: EpisodeGroup) => new Map(group.seasons.flatMap(season => season.episodes.map(episode => [episode.id, { ...episode, season: season.number }] as const)))
  const old = flatten(before), next = flatten(after)
  const added: string[] = [], removed: string[] = [], changed: string[] = []
  for (const [id, ep] of next) {
    const previous = old.get(id), label = `${episodeCode(ep.season, ep.number)} ${ep.name}`
    if (!previous) added.push(`新增：${label}`)
    else {
      const fields: string[] = []
      if (previous.season !== ep.season || previous.number !== ep.number) fields.push('编排编号')
      if ((previous.name || '') !== (ep.name || '')) fields.push('标题')
      if ((previous.date || '') !== (ep.date || '')) fields.push(`播出日期 ${previous.date || '未知'} 改为 ${ep.date || '未知'}`)
      if ((previous.runtime || 0) !== (ep.runtime || 0)) fields.push(`时长 ${previous.runtime || '未知'} 改为 ${ep.runtime || '未知'}`)
      if (previous.sourceSeason !== ep.sourceSeason || previous.sourceNumber !== ep.sourceNumber) fields.push('TMDB 原编号')
      if ((previous.overview || '') !== (ep.overview || '')) fields.push('简介')
      if ((previous.stillUrl || '') !== (ep.stillUrl || '')) fields.push('截图')
      if (fields.length) changed.push(`调整：${episodeCode(previous.season, previous.number)} ${previous.name} 改为 ${label}（${fields.join('；')}）`)
    }
  }
  for (const [id, ep] of old) if (!next.has(id)) removed.push(`移除：${episodeCode(ep.season, ep.number)} ${ep.name}`)
  return { added, removed, changed }
}
