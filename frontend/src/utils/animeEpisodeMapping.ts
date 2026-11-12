import type { AnimeEpisodeSuggestion, AnimeNamingTarget } from '../types/animeSubscription'
import type { SourceForm } from './animeSourceForm'

export function firstAnimeSourceOffset(source: SourceForm, target?: AnimeNamingTarget, mapping?: AnimeEpisodeSuggestion): { offset?: number; message: string } {
  const unresolved = { message: '未能确定首集对应，请手动填写集数偏移。' }
  if (!target || source.episodePattern.trim() || !source.sample || source.sample.source.expiresAt <= Date.now()) return unresolved
  const releases = source.sample.releases.filter(item => item.parse.kind === 'episode')
  const first = mapping && mapping.season === target.season && ['verified', 'partial'].includes(mapping.status) && mapping.first != null && mapping.first > 0 && mapping.targetFirst != null && mapping.targetFirst > 0
    ? { bangumiNumber: mapping.first, tmdbEpisode: mapping.targetFirst } : undefined
  const candidates = new Map<number, number>()
  // 只对照本篇首集，不把最近窗口的最小集号当作起点，也不检查后续标题。
  for (const item of releases) {
    const number = item.parse.episode
    if (number == null) continue
    if (first && (number === 1 || number === first.bangumiNumber)) {
      const offset = number === 1 ? first.tmdbEpisode - 1 : first.tmdbEpisode - number
      if (offset > -99999 && offset < 99999) candidates.set(offset, number)
    }
    if (!first && mapping?.status !== 'conflict' && !target.groupId && number === 1 && target.seasons.some(season => season.number === target.season && season.episodes?.some(episode => episode.number === 1))) {
      const seasons = [...item.title.matchAll(/\bS(\d{1,3})\s*E0*1\b|\bSeason\s*(\d{1,3})\b|第\s*(\d{1,3}|[一二三四五六七八九十]{1,3})\s*季/gi)].map(match => seasonNumber(match[1] || match[2] || match[3]!))
      if (seasons.some(season => season !== target.season)) return unresolved
      if (seasons.length) candidates.set(0, 1)
    }
  }
  if (candidates.size !== 1) return unresolved
  const [offset, resource] = candidates.entries().next().value!
  const episode = resource + offset
  if (episode < 1) return unresolved
  const coordinate = `S${String(target.season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
  return { offset, message: `自动计算：资源 ${resource} → ${coordinate}，偏移 ${offset}` }
}

function seasonNumber(value: string): number {
  if (/^\d+$/.test(value)) return Number(value)
  const digits = '零一二三四五六七八九'
  if (!value.includes('十')) return value.length === 1 ? digits.indexOf(value) : NaN
  const parts = value.split('十'), [tens = '', units = ''] = parts
  return parts.length === 2 && tens.length <= 1 && units.length <= 1
    ? (tens ? digits.indexOf(tens) : 1) * 10 + (units ? digits.indexOf(units) : 0) : NaN
}
