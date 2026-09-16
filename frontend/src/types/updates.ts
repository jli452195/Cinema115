export interface RecentUpdateItem {
  kind: 'movie' | 'tv'
  tmdbId: number
  title: string
  posterUrl: string
  releaseDate: string
  rating: number
  votes: number
  date: string
  updatedAt: string
  season?: number
  episodeLabel?: string
  episodeCount?: number
  metadataMissing?: boolean
}

export interface RecentUpdatesPage {
  items: RecentUpdateItem[]
  ready: boolean
  status: 'waiting' | 'building' | 'ready' | 'failed'
  hasMore: boolean
  nextCursor?: string
  from: string
  to: string
}
