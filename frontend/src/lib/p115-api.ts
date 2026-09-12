// 统一处理鉴权和非 JSON 错误，避免代理异常时出现 JSON 解析乱码提示。
export async function p115Request<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/admin/p115/${path}`, {
    ...init,
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok) {
    throw new Error(response.status === 401 ? '管理员登录已过期，请返回首页重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  }
  if (!data) throw new Error('接口未返回有效数据，请检查后端服务')
  return data as T
}
