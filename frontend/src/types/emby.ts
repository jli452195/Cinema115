import { catalogAPI } from '../composables/usePortalAuth'
export interface EmbyItem { id: string; type: string; name: string; libraryId: string; season: number; episode: number; endEpisode: number }
export interface EmbyMatch { kind: 'tv' | 'movie'; tmdbId: string; state: 'present' | 'absent' | 'unknown' | 'stale'; items: EmbyItem[]; episodeCount: number }
export interface EmbyMissingSeason { season: number; lastEpisode: number; ownedCount: number; missingCount: number; ranges: { first: number; last: number }[] }
export interface EmbyMissingSeries { key: string; tmdbId: string; title: string; posterUrl: string; missingCount: number; seasons: EmbyMissingSeason[] }
export interface EmbyMissingNotice { code: string; level: 'info' | 'warning'; message: string; count?: number }
export interface EmbyMissingPage { items: EmbyMissingSeries[]; ready: boolean; configured: boolean; syncedAt: string; notices: EmbyMissingNotice[]; checkedSeries: number; excludedCount: number; summary: { series: number; seasons: number; episodes: number }; total: number; offset: number; nextOffset: number; hasMore: boolean }
export interface EmbyMissingSource { seriesId: string; tmdbId: string; title: string; libraryId: string; episodeCount: number }
export interface EmbyMissingPreview { scope: string; key: string; revision: string; title: string; missingCount: number; sources: EmbyMissingSource[] }
export interface EmbyMissingExclusion extends EmbyMissingSource { id: string; excludedAt: string }
export interface EmbyMissingExcludedPage { service: string; items: EmbyMissingExclusion[]; count: number; total: number; offset: number; nextOffset: number; hasMore: boolean }
export interface EmbyConfig { url: string; configured: boolean; libraryIds: string[]; serverId: string; serverName: string; webhookPath: string; webhookBaseUrl: string }
export interface EmbyStatus { status: string; configured: boolean; count: number; scanned: number; syncedAt: string; lastError: string; webhookAt: string; webhookEvent: string; webhookError: string; pending: number; failed: number; connected: boolean; connectionError: string; eventError: string; metadataPending: number; metadataFailed: number; updatesReady: boolean; updatesError: string; updatesStatus: 'waiting' | 'building' | 'ready' | 'failed' }
export async function embyRequest(token: string, path: string, signal: AbortSignal, body?: object) {
  const response = await fetch(path === 'library-check' || path.startsWith('media/') ? catalogAPI(`emby/${path}`) : `/api/admin/emby/${path}`, { method: body === undefined ? 'GET' : 'POST', signal: AbortSignal.any([signal, AbortSignal.timeout(90000)]), headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || `Emby 请求失败（HTTP ${response.status}）`)
  return data
}
