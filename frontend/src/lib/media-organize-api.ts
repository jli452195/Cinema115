export interface MediaRule {
  id: string; name: string; storage: string; source: string; target: string; failure?: string; sourceId?: string; targetId?: string; failureId?: string; accountId?: string
  classify: boolean; enabled: boolean; intervalMinutes: number; lastScan?: string
}
export interface MediaEntry {
  file: { id: string; name: string; sourcePath: string; mediaType: string; season: number; episode: number }
  group: string; relative?: string; target?: string; status: string; message?: string
  failure?: { state: string; parentId?: string; reason: string; message?: string }
}
export interface MediaGroup { id: string; identity: { name: string; year?: string }; kind?: string; tmdbId?: number; title?: string; category?: string; message?: string }
export interface MediaRun { id: string; rule: MediaRule; status: string; message?: string; createdAt: string; updatedAt: string; groups: MediaGroup[] | null; entries: MediaEntry[] | null; entryCount: number; pendingCount: number; readyCount: number; failureCount: number }
export interface MediaHistoryItem {
  runId: string; fileId: string; name: string; title: string; kind: string; mediaType: string; season: number; episode: number
  source: string; target?: string; status: string; message?: string; at: string
}
export interface MediaHistory { items: MediaHistoryItem[]; page: number; pages: number; total: number }
export interface MediaState { rules: MediaRule[]; runs: MediaRun[]; history: MediaHistory }
export async function mediaOrganizeRequest<T>(token: string, path = '', init: RequestInit = {}): Promise<T> {
  const signal = AbortSignal.any([...(init.signal ? [init.signal] : []), AbortSignal.timeout(75000)])
  const response = await fetch(`/api/admin/media-organize${path}`, { ...init, signal, cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' } })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || `请求失败（HTTP ${response.status}）`)
  return data as T
}
