export interface AnimeIssue { code: string; retryAfterAt: number | null }
export interface AnimeCacheMeta {
  fetchedAt: number
  expiresAt: number
  stale: boolean
  dataVersion: string
  issue: AnimeIssue | null
}
export interface AnimeCardItem {
  id: string
  title: string
  originalTitle: string
  coverUrl: string
  date: string
  weekday: number | null
  url: string
}
export interface AnimeCalendar extends AnimeCacheMeta { days: { weekday: number; items: AnimeCardItem[] }[] }
export interface AnimeSearch extends AnimeCacheMeta { items: AnimeCardItem[]; total: number; limit: number; offset: number }
export interface AnimeSubject extends AnimeCardItem { synopsis: string; platform: string; episodes: number | null; totalEpisodes: number | null }
export interface AnimeRelations extends AnimeCacheMeta { items: (AnimeCardItem & { relation: string })[] }
export interface AnimeDetailState { current: AnimeCardItem; history: AnimeCardItem[]; relationsOpen: boolean; relationsLimit: number; synopsisOpen: boolean; top: number }
export interface AnimeSubjectResult { subject: AnimeSubject; metadata: AnimeCacheMeta; relations?: AnimeRelations }
export interface AnimeChapterEpisode {
  id: string; number: number; chapterNumber: number | null; title: string; originalTitle: string; date: string
}
export interface AnimeChapterEpisodes extends AnimeCacheMeta {
  items: AnimeChapterEpisode[]; total: number; limit: number; offset: number; maxItems: number
}
