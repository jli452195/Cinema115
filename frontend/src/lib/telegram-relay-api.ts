export async function telegramRelayRequest<T>(token: string, path: string, signal: AbortSignal, body?: object, method?: string): Promise<T> {
  const response = await fetch(`/api/admin/telegram-relay${path}`, {
    method: method || (body === undefined ? 'GET' : 'POST'),
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    cache: 'no-store', signal: AbortSignal.any([signal, AbortSignal.timeout(path === '/peers' || path === '/rules' ? 45000 : 15000)]),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (response.status === 204) return undefined as T
  const value = await response.json().catch(() => null)
  if (!response.ok) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : value?.error || `请求失败（HTTP ${response.status}）`)
  if (!value) throw new Error('接口未返回有效数据，请检查后端服务')
  return value as T
}
