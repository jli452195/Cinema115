export interface SubtitleConfig { assrt: { configured: boolean } }
export interface AssrtSubtitle { id: number; title: string; videoName?: string; language?: string; group?: string; format?: string; uploadedAt?: string }
export interface AssrtSearchResult { configured: boolean; items: AssrtSubtitle[]; hasMore: boolean }

export async function subtitleRequest<T>(token: string, path: string, signal: AbortSignal, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/admin/subtitles/${path}`, {
    ...init, cache: 'no-store', signal: AbortSignal.any([signal, AbortSignal.timeout(35000)]),
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  const data = await response.json().catch(() => null)
  signal.throwIfAborted()
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || '读取字幕服务失败')
  return data as T
}
