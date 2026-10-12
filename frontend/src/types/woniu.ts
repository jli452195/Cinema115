import type { ResourceSearchConfig } from './resourceSearch'
export type WoniuConfigView = ResourceSearchConfig
export class WoniuError extends Error {
  constructor(message: string, public code: string, public status: number) { super(message) }
}
export async function woniuRequest<T>(token: string, path: string, signal: AbortSignal, method = 'GET', body?: object): Promise<T> {
  const response = await fetch(`/api/admin/woniu/${path}`, {
    method, signal: AbortSignal.any([signal, AbortSignal.timeout(path === 'search' ? 125000 : 75000)]), cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new WoniuError(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || `蜗牛请求失败（HTTP ${response.status}）`, data?.code || 'UPSTREAM_ERROR', response.status)
  return data as T
}
