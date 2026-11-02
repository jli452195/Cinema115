export interface MediaRule {
  id: string; name: string; storage: string; source: string; target: string; failure?: string; sourceId?: string; targetId?: string; failureId?: string; accountId?: string; connectionId?: string; connectionRevision?: number
  classify: boolean; enabled: boolean; intervalMinutes: number; lastScan?: string
}
export interface MediaEntry {
  file: { id: string; name: string; sourcePath: string; mediaType: string; season: number; episode: number }
  group: string; videoId?: string; relative?: string; target?: string; status: string; message?: string
  failure?: { state: string; parentId?: string; reason: string; message?: string }
}
export interface MediaGroup { id: string; identity: { name: string; year?: string }; kind?: string; tmdbId?: number; title?: string; category?: string; message?: string }
export interface MediaHistoryItem {
  runId: string; fileId: string; name: string; title: string; kind: string; mediaType: string; season: number; episode: number; attachmentCount: number
  source: string; target?: string; status: string; message?: string; at: string; canDelete: boolean
}
export type MediaHistoryStatus = 'all' | 'running' | 'done' | 'attention'
export interface MediaActivity { runId: string; ruleName: string; status: string; message?: string; at: string }
export interface MediaHistory {
  items: MediaHistoryItem[]; page: number; pages: number; total: number
  counts: Record<MediaHistoryStatus, number>; active: boolean; activity: MediaActivity[]; cleanup: MediaCleanup[]
}
export interface MediaTask extends MediaHistoryItem { rule: MediaRule; group: MediaGroup; entries: MediaEntry[] }
export interface MediaCleanup { runId: string; ruleName: string; files: number; directories: number; failed: number; at: string }
export interface MediaMonitorStatus { mode: 'starting' | 'realtime' | 'polling'; message?: string; lastEventAt?: string; lastFallbackAt?: string }
export interface MediaState { rules: MediaRule[]; active: boolean; activeRules: string[]; monitoring?: Record<string, MediaMonitorStatus> }
export interface MediaLocalDirectoryPage {
  path: string; parentPath: string | null; directories: { name: string; path: string }[]; truncated: boolean
}
export async function mediaOrganizeRequest<T>(token: string, path = '', init: RequestInit = {}): Promise<T> {
  const signal = AbortSignal.any([...(init.signal ? [init.signal] : []), AbortSignal.timeout(75000)])
  const response = await fetch(`/api/admin/media-organize${path}`, { ...init, signal, cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || `请求失败（HTTP ${response.status}）`)
  return data as T
}
