export async function telegramRequest<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/admin/telegram/${path}`, {
    ...init,
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  if (!data) throw new Error('接口未返回有效数据，请检查后端服务')
  return data as T
}
