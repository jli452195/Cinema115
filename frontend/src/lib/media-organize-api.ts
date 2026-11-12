export interface MediaRule {
  id: string; name: string; storage: string; source: string; target: string; failure?: string; sourceId?: string; targetId?: string; failureId?: string; accountId?: string; connectionId?: string; connectionRevision?: number
  classify: boolean; realtimeEnabled: boolean; scheduleEnabled: boolean; intervalSeconds: number; attachmentCleanupEnabled: boolean; attachmentGraceSeconds: number; nextScanAt?: string; deleteDuplicates?: boolean; lastScan?: string
}
export interface MediaIdentity {
  name: string
}
export interface MediaEntry {
  file: { id: string; name: string; sourcePath: string; mediaType: string; season: number; episode: number; episodeExplicit?: boolean; endEpisode?: number; namingValues?: Record<string, string> }
  videoId?: string; target?: string; status: string; message?: string; resultCode?: string
  failure?: { state: string; message?: string }
  p115Move?: { phase: string }
}
export interface MediaGroup { id: string; identity: MediaIdentity; kind?: string; tmdbId?: number; title?: string; year?: string; category?: string; message?: string }
export interface MediaHistoryItem {
  runId: string; fileId: string; name: string; title: string; kind: string; mediaType: string; season: number; episode: number; episodeExplicit?: boolean; attachmentCount: number
  source: string; target?: string; status: string; message?: string; at: string; canDelete: boolean; canRetry: boolean
}
export type MediaHistoryStatus = 'all' | 'running' | 'done' | 'failed'
export interface MediaActivity {
  runId: string; ruleName: string; status: string; message?: string; at: string
  stage?: 'scanning' | 'recognizing' | 'planning' | 'organizing' | 'cleanup'
  current?: string; completed?: number; total?: number; files?: number; startedAt?: string
}
export interface MediaRetryBatch {
  id: string; status: 'running' | 'stopping' | 'completed' | 'stopped' | 'interrupted'
  total: number; pending: number; success: number; failed: number; skipped: number
}
export interface MediaBatchRetryResult { queued: number; skipped: number; reasons: Record<string, number>; batch: MediaRetryBatch | null }
export interface MediaHistory {
  items: MediaHistoryItem[]; page: number; pages: number; total: number
  counts: Record<MediaHistoryStatus, number>; active: boolean; activity: MediaActivity[]; cleanup: MediaCleanup[]; retryBatch?: MediaRetryBatch | null
}
export interface MediaTask extends MediaHistoryItem { rule: Pick<MediaRule, 'name' | 'source' | 'failure'>; group: MediaGroup; entries: MediaEntry[] }
export interface MediaCleanup { runId: string; ruleName: string; files: number; directories: number; failed: number; at: string }
export interface MediaMonitorStatus { mode: 'starting' | 'connecting' | 'realtime' | 'event' | 'unavailable' | 'polling'; message?: string; lastEventAt?: string }
export interface MediaState { rules: MediaRule[]; active: boolean; activeRules: string[]; stoppingRules?: string[]; monitoring?: Record<string, MediaMonitorStatus>; activity?: MediaActivity[] }
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

export async function mediaOrganizeHistoryEvents(token: string, signal: AbortSignal, changed: (event: 'ready' | 'changed') => void): Promise<void> {
  const connection = new AbortController()
  let timeout: ReturnType<typeof setTimeout>
  const alive = () => { clearTimeout(timeout); timeout = setTimeout(() => connection.abort(), 45000) }
  alive()
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  try {
    const response = await fetch('/api/admin/media-organize/history/events', { signal: AbortSignal.any([signal, connection.signal]), cache: 'no-store', headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' } })
    if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new MediaOrganizeRequestError('整理记录通知连接不可用', response.status)
    reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (!signal.aborted) {
      const { done, value } = await reader.read()
      if (done) break
      alive()
      buffer = (buffer + decoder.decode(value, { stream: true })).replace(/\r\n/g, '\n')
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        const event = buffer.slice(0, boundary).split('\n').find(line => line.startsWith('event:'))?.slice(6).trim()
        if (event === 'ready' || event === 'changed') changed(event)
        buffer = buffer.slice(boundary + 2)
        boundary = buffer.indexOf('\n\n')
      }
    }
  } finally {
    clearTimeout(timeout!)
    connection.abort()
    await reader?.cancel().catch(() => {})
    reader?.releaseLock()
  }
}
