import type { ResourceSearchConfig, ResourceType } from './resourceSearch'
export type { ResourceType as GyingResourceType, ResourceLink as GyingResourceLink, ResourceSearchItem as GyingSearchItem, ResourceSearchAttempt as GyingSearchAttempt, ResourceSearchResult as GyingSearchResponse } from './resourceSearch'
export interface GyingConfigView extends ResourceSearchConfig { resourceTypes: ResourceType[] }

export async function gyingRequest<T>(token: string, path: string, signal: AbortSignal, method = 'GET', body?: object): Promise<T> {
  const response = await fetch(`/api/admin/gying/${path}`, {
    method,
    signal: AbortSignal.any([signal, AbortSignal.timeout(path === 'search' ? 125000 : 95000)]),
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `观影请求失败（HTTP ${response.status}）`)
  return data as T
}
