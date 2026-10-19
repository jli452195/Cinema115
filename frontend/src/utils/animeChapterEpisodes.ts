import type { AnimeChapterEpisode } from '../types/anime'
import type { AnimeNamingTarget } from '../types/animeSubscription'

type TargetEpisode = NonNullable<AnimeNamingTarget['seasons'][number]['episodes']>[number]
export interface ChapterEpisodeReference {
  chapter: AnimeChapterEpisode
  episode?: TargetEpisode
  season?: number
  evidence?: 'date' | 'title' | 'both'
}

function titleKey(title: string) {
  const key = title.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]/gu, '')
  // Generic numbered titles cannot establish identity between two catalogues.
  if (!key || /^[\d一二三四五六七八九十百千]+$/.test(key) || /^(?:(?:episode|ep|第)[\d一二三四五六七八九十百千]+(?:集|话|話)?|[\d]+(?:集|话|話))$/.test(key)) return ''
  return key
}
function dateKey(raw = '') {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw) || raw.startsWith('0000')) return ''
  const value = new Date(raw + 'T00:00:00Z')
  return Number.isFinite(value.valueOf()) && value.toISOString().slice(0, 10) === raw ? raw : ''
}
export const seasonEpisodeLabel = (season: number, episode: number) => `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`

// Reference only. Never infer identity from numeric equality, extrapolate an
// offset, or modify RSS rules and the user's saved naming anchors.
export function chapterEpisodeReferences(chapters: AnimeChapterEpisode[], target?: AnimeNamingTarget): ChapterEpisodeReference[] {
  const season = target?.seasons.find(s => s.number === target.season)
  const episodes = season?.episodes || []
  const dates = new Map<string, TargetEpisode[]>(), titles = new Map<string, TargetEpisode[]>()
  for (const e of episodes) {
    const date = dateKey(e.date), title = titleKey(e.name)
    if (date) dates.set(date, [...(dates.get(date) || []), e])
    if (title) titles.set(title, [...(titles.get(title) || []), e])
  }
  const proposed = chapters.map(chapter => {
    const row: ChapterEpisodeReference = { chapter }
    const date = dateKey(chapter.date)
    const byDate = date ? dates.get(date) || [] : []
    const byTitle = [...new Set([chapter.title, chapter.originalTitle].flatMap(title => titles.get(titleKey(title)) || []))]
    // Conflicting title/date evidence is left unresolved. Exact translated
    // titles may differ, so a unique matching date is also usable evidence.
    const candidates = byTitle.length && byDate.length ? byTitle.filter(e => byDate.includes(e))
      : byTitle.length ? byTitle.filter(e => !date || !dateKey(e.date)) : byDate
    if (candidates.length === 1) {
      row.episode = candidates[0]; row.season = season!.number
      row.evidence = byTitle.includes(candidates[0]) ? byDate.includes(candidates[0]) ? 'both' : 'title' : 'date'
    }
    return row
  })
  const counts = new Map<string, number>()
  for (const row of proposed) if (row.episode) counts.set(row.episode.id, (counts.get(row.episode.id) || 0) + 1)
  return proposed.map(row => row.episode && counts.get(row.episode.id)! > 1 ? { chapter: row.chapter } : row)
}
