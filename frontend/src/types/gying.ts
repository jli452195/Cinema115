export interface GyingResourceType {
  type: string
  label: string
}

export interface GyingConfigView {
  baseUrl: string
  username: string
  configured: boolean
  hasSession: boolean
  resourceOrder: string[]
  resourceTypes: GyingResourceType[]
}

export interface GyingResourceLink {
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

export interface GyingSearchItem {
  id: string
  title: string
  coverUrl?: string
  year?: number
  mediaType: 'movie' | 'tv' | 'anime' | 'other'
  description?: string
  links: GyingResourceLink[]
}

export interface GyingSearchAttempt {
  method: 'title' | 'imdb'
  query: string
  total: number
  usable: number
  failed?: number
  unavailable?: number
  truncated?: boolean
}

export interface GyingSearchResponse {
  query: string
  matchedBy: 'title' | 'imdb'
  items: GyingSearchItem[]
  total: number
  truncated?: boolean
  attempts: GyingSearchAttempt[]
  partial?: boolean
  failedDetails?: number
  unavailableDetails?: number
  cached: boolean
  cacheExpiresAt?: string
}

export async function gyingRequest<T>(token: string, path: string, signal: AbortSignal, method = 'GET', body?: object): Promise<T> {
  const response = await fetch(`/api/admin/gying/${path}`, {
    method,
    signal: AbortSignal.any([signal, AbortSignal.timeout(path === 'search' ? 125000 : 95000)]),
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `观影请求失败（HTTP ${response.status}）`)
  return data as T
}
