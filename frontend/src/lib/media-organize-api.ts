export interface MediaRule {
  id: string; name: string; storage: string; source: string; target: string; failure?: string; sourceId?: string; targetId?: string; failureId?: string; accountId?: string; connectionId?: string; connectionRevision?: number
  classify: boolean; realtimeEnabled: boolean; scheduleEnabled: boolean; intervalSeconds: number; attachmentCleanupEnabled: boolean; attachmentGraceSeconds: number; nextScanAt?: string; deleteDuplicates?: boolean; lastScan?: string
}
export interface MediaIdentity {
  name: string; year?: string; kind?: string; tmdbId?: number; season?: number; parentName?: string; yearFromParent?: boolean; seasonExplicit?: boolean
  alternativeName?: string; alternativeYear?: string; contentType?: string; conflict?: string
}
export interface MediaEntry {
  file: { id: string; name: string; sourcePath: string; mediaType: string; season: number; episode: number; endEpisode?: number; namingValues?: Record<string, string> }
  group: string; videoId?: string; relative?: string; target?: string; status: string; message?: string; resultCode?: string
  failure?: { state: string; parentId?: string; reason: string; message?: string }
  p115Move?: { phase: string }
}
export interface MediaGroup { id: string; identity: MediaIdentity; kind?: string; tmdbId?: number; title?: string; year?: string; category?: string; message?: string; matchedSeason?: number }
export interface MediaHistoryItem {
  runId: string; fileId: string; name: string; title: string; kind: string; mediaType: string; season: number; episode: number; attachmentCount: number
  source: string; target?: string; status: string; message?: string; at: string; canDelete: boolean; canRetry: boolean
}
export type MediaHistoryStatus = 'all' | 'running' | 'done' | 'failed'
export interface MediaActivity { runId: string; ruleName: string; status: string; message?: string; at: string }
export interface MediaHistory {
  items: MediaHistoryItem[]; page: number; pages: number; total: number
  counts: Record<MediaHistoryStatus, number>; active: boolean; activity: MediaActivity[]; cleanup: MediaCleanup[]
}
export interface MediaTask extends MediaHistoryItem { rule: MediaRule; group: MediaGroup; entries: MediaEntry[] }
export interface MediaCleanup { runId: string; ruleName: string; files: number; directories: number; failed: number; at: string }
export interface MediaMonitorStatus { mode: 'starting' | 'connecting' | 'realtime' | 'event' | 'unavailable' | 'polling'; message?: string; lastEventAt?: string }
export interface MediaState { rules: MediaRule[]; active: boolean; activeRules: string[]; stoppingRules?: string[]; monitoring?: Record<string, MediaMonitorStatus> }
export interface MediaLocalDirectoryPage {
  path: string; parentPath: string | null; directories: { name: string; path: string }[]; truncated: boolean
}
export class MediaOrganizeRequestError extends Error {
  constructor(message: string, readonly status: number) { super(message); this.name = 'MediaOrganizeRequestError' }
}
export async function mediaOrganizeRequest<T>(token: string, path = '', init: RequestInit = {}): Promise<T> {
  const signal = AbortSignal.any([...(init.signal ? [init.signal] : []), AbortSignal.timeout(75000)])
  const response = await fetch(`/api/admin/media-organize${path}`, { ...init, signal, cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new MediaOrganizeRequestError(data?.error || `请求失败（HTTP ${response.status}）`, response.status)
  return data as T
}
