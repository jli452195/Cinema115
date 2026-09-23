export const FOLLOW_PAGE_SIZE = 24
export const FOLLOW_STATUS_LABELS = { pending: '待处理', processing: '处理中', caught_up: '已追平', closed: '已关闭', completed: '已完成' }
export interface Follow {
  kind: 'movie' | 'tv'; tmdbId: number; title: string; posterUrl: string; date: string
  status: 'pending' | 'processing' | 'closed' | 'completed'; subscribedAt: string
  effectiveStatus?: keyof typeof FOLLOW_STATUS_LABELS
  note: string
  progress?: { state: 'unknown' | 'absent' | 'waiting' | 'tracking' | 'caught_up' | 'completed'; aired: number; owned: number; checkedAt: number; error?: string }
  libraryState: 'present' | 'absent' | 'unknown' | 'stale'
}
export interface FollowState { subscribed: boolean; request: Follow | null; libraryState: Follow['libraryState'] }
export function isFollowCaughtUp(item: Follow) {
  if (item.effectiveStatus) return item.effectiveStatus === 'caught_up'
  const p = item.progress
  return item.kind === 'tv' && item.status !== 'closed' && item.status !== 'completed' && !!p && !p.error && p.aired > 0 && p.owned >= p.aired && (p.state === 'tracking' || p.state === 'caught_up')
}
export function followLabel(item: Follow) {
  if (isFollowCaughtUp(item)) return '已追平 · 继续追更'
  if (item.progress?.state === 'completed' || item.status === 'completed') return '已完成'
  if (item.status === 'closed') return '已关闭'
  if (item.progress?.error) return '等待复核'
  if (item.progress?.state === 'tracking') return `已入库 ${item.progress.owned}/${item.progress.aired} 集 · 待追齐`
  if (item.progress?.state === 'waiting') return '等待播出'
  if (item.kind === 'movie' && item.libraryState === 'present') return '已入库'
  return { pending: '待处理', processing: '处理中', closed: '已关闭', completed: '已完成' }[item.status] || '待处理'
}
export async function followRequest(token: string, path: string, signal: AbortSignal, method = 'GET') {
  const response = await fetch(`/api/me/follows${path}`, { method, headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || '追更操作失败，请重试')
  return data
}
