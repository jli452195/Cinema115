export interface ResourceType { type: string; label: string }
export interface ResourceSearchConfig {
  baseUrl: string
  username: string
  configured: boolean
  hasSession: boolean
  resourceOrder: string[]
  resourceTypes?: ResourceType[]
}
export interface ResourceLink {
  category: 'pan' | 'torrent'
  type: string
  typeLabel: string
  subType?: string
  subTypeLabel?: string
  url: string
  password?: string
  workTitle?: string
  size?: string
  seeds?: number
  updatedAt?: string
}
export interface ResourceSearchItem {
  id: string
  title: string
  coverUrl?: string
  year?: number
  mediaType: 'movie' | 'tv' | 'anime' | 'other'
  description?: string
  detailStatus?: 'ready' | 'empty' | 'failed'
  links: ResourceLink[]
}
export interface ResourceSearchAttempt {
  method: 'title' | 'original' | 'imdb'
  query: string
  total: number
  usable: number
  failed?: number
  unavailable?: number
  truncated?: boolean
}
export interface ResourceSearchResult {
  query: string
  matchedBy: 'title' | 'original' | 'imdb'
  items: ResourceSearchItem[]
  total: number
  page?: number
  nextPage?: number
  hasMore?: boolean
  truncated?: boolean
  attempts: ResourceSearchAttempt[]
  partial?: boolean
  failedDetails?: number
  unavailableDetails?: number
  cached: boolean
  cacheExpiresAt?: string
}

// A failed page leaves the caller's existing results intact. Entries use site
// IDs, preserving separate seasons/editions and stable first appearance order.
export function mergeResourcePage(previous: ResourceSearchResult, next: ResourceSearchResult): ResourceSearchResult {
  if (next.query !== previous.query || next.matchedBy !== previous.matchedBy || next.page !== (previous.page || 1) + 1) throw new Error('分页与当前搜索不一致，请重新搜索')
  const items = new Map(previous.items.map(item => [item.id, item]))
  for (const item of next.items) {
    const old = items.get(item.id)
    if (!old || item.detailStatus !== 'failed') items.set(item.id, item)
  }
  const attempts = previous.attempts.map(attempt => ({ ...attempt }))
  for (const attempt of next.attempts) {
    const current = attempts.find(value => value.method === attempt.method && value.query === attempt.query)
    if (current) {
      current.total += attempt.total; current.usable += attempt.usable
      current.failed = (current.failed || 0) + (attempt.failed || 0)
      current.truncated = attempt.truncated
    } else attempts.push({ ...attempt })
  }
  return {
    ...next, items: [...items.values()], total: items.size, attempts,
    partial: previous.partial || next.partial,
    failedDetails: (previous.failedDetails || 0) + (next.failedDetails || 0),
    cached: previous.cached && next.cached,
    cacheExpiresAt: previous.cacheExpiresAt && next.cacheExpiresAt ? [previous.cacheExpiresAt, next.cacheExpiresAt].sort()[0] : undefined,
  }
}
