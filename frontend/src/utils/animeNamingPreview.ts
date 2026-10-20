import type { AnimeNamingTarget, AnimeSourceMapping } from '../types/animeSubscription'

export interface AnimeNamingParse { kind: string; episode: number | null; rawEpisode: string }
export interface AnimeNamingProjection {
  status: 'unmapped' | 'ignored' | 'target_missing' | 'mapped'
  season: number | null
  episode: number | null
  episodeId?: string
  episodeName?: string
  pathStem?: string
}

// Mirrors model.AnimeFilenamePart and AnimeNamingPlan.Project. Shared fixtures
// exercise both implementations; this display-only projection never authorizes work.
export function animeFilenamePart(value: string): string {
  const space = '[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]'
  const clean = value.replace(/[<>:"/\\|?*\p{Cc}]/gu, '_').replace(new RegExp(`^${space}+|${space}+$`, 'g'), '').replace(/^[. ]+|[. ]+$/g, '')
  return Array.from(clean || '未命名').slice(0, 120).join('')
}

export function animeNamingFolder(target: AnimeNamingTarget): string {
  return `${truncateFilename(animeFilenamePart(target.title), 180)} (${target.year}) {tmdb-${target.tmdbId}}`
}

const encoder = new TextEncoder()
function truncateFilename(value: string, max: number): string {
  const runes = Array.from(value)
  let bytes = encoder.encode(value).length
  while (bytes > max && runes.length) bytes -= encoder.encode(runes.pop()!).length
  return runes.join('').replace(/[. ]+$/g, '')
}

export function animeNamingPath(target: AnimeNamingTarget, season: number, episode: number, group: string, extension = ''): string {
  const suffix = `.S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}.${truncateFilename(animeFilenamePart(group), 80)}`
  const stem = truncateFilename(animeFilenamePart(target.title), 200 - encoder.encode(suffix + extension).length) + suffix
  return `${animeNamingFolder(target)}/Season ${String(season).padStart(2, '0')}/${stem}${extension}`
}

export function projectAnimeNaming(target: AnimeNamingTarget, mapping: AnimeSourceMapping | null, parsed: AnimeNamingParse, group: string): AnimeNamingProjection {
  const empty: AnimeNamingProjection = { status: 'unmapped', season: null, episode: null }
  if (!mapping) return empty
  const raw = parsed.kind === 'episode' && parsed.episode != null ? String(parsed.episode) : parsed.rawEpisode
  const override = mapping.overrides.find(rule => rule.rawKind === parsed.kind && rule.rawNumber === raw)
  if (override?.action === 'ignore') return { ...empty, status: 'ignored' }
  const n = parsed.episode
  const segment = parsed.kind === 'episode' && n != null ? mapping.segments.find(rule => n >= rule.first && (rule.last == null || n <= rule.last)) : undefined
  if (!override && !segment) return empty
  const season = override?.targetSeason ?? segment!.targetSeason
  const episode = override?.targetEpisode ?? n! - segment!.sourceBase + segment!.targetBase
  const metadata = target.seasons.find(item => item.number === season)?.episodes?.find(item => item.number === episode)
  if (!metadata) return { status: 'target_missing', season, episode }
  return {
    status: 'mapped', season, episode, episodeId: metadata.id, episodeName: metadata.name,
    pathStem: animeNamingPath(target, season, episode, group),
  }
}
