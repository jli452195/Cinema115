// 导航只读取全站数量，避免触发列表详情和媒体库查询。
export async function fetchFollowPendingCount(token: string, signal: AbortSignal): Promise<number> {
  const response = await fetch('/api/admin/follows?countsOnly=1', {
    headers: { Authorization: `Bearer ${token}` }, cache: 'no-store', signal,
  })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw Object.assign(new Error('追更数量读取失败'), { status: response.status })
  const count = data?.counts?.pending
  if (!Number.isSafeInteger(count) || count < 0) throw new Error('追更数量无效')
  return count
}
