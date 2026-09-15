export const FOLLOW_PAGE_SIZE = 24
export interface Follow {
  kind: 'movie' | 'tv'; tmdbId: number; title: string; posterUrl: string; date: string
  status: 'pending' | 'processing' | 'completed'; subscribedAt: string
  note: string
  progress?: { state: 'unknown' | 'absent' | 'waiting' | 'tracking' | 'caught_up' | 'completed'; aired: number; owned: number; checkedAt: number; error?: string }
  libraryState: 'present' | 'absent' | 'unknown' | 'stale'
}
export interface FollowState { subscribed: boolean; request: Follow | null; libraryState: Follow['libraryState'] }
export function followLabel(item: Follow) {
  if (item.progress?.error) return '等待复核'
  if (item.progress?.state === 'completed') return '已完成'
  if (item.progress?.state === 'caught_up') return '已追平 · 继续追更'
  if (item.progress?.state === 'tracking') return `已入库 ${item.progress.owned}/${item.progress.aired} 集 · 待追齐`
  if (item.progress?.state === 'waiting') return '等待播出'
  if (item.kind === 'movie' && item.libraryState === 'present') return '已入库'
  return { pending: '待处理', processing: '处理中', completed: '已完成' }[item.status] || '待处理'
}
export async function followRequest(token: string, path: string, signal: AbortSignal, method = 'GET') {
  const response = await fetch(`/api/me/follows${path}`, { method, headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || '追更操作失败，请重试')
  return data
}
