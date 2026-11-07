import { defaultDetailSearchOrder, type DetailSearchSourceID } from '../utils/detailSearchSources'

export interface DetailSearchSettings { order: DetailSearchSourceID[] }

export async function detailSearchSettingsRequest(token: string, signal: AbortSignal, order?: readonly DetailSearchSourceID[]): Promise<DetailSearchSettings> {
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(15000)])
  const response = await fetch('/api/admin/resources/search-settings', {
    method: order ? 'PUT' : 'GET', cache: 'no-store', signal: requestSignal,
    headers: { Authorization: `Bearer ${token}`, ...(order ? { 'Content-Type': 'application/json' } : {}) },
    ...(order ? { body: JSON.stringify({ order }) } : {}),
  })
  const data = await response.json().catch(() => null)
  requestSignal.throwIfAborted()
  if (!response.ok) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || `搜索设置请求失败（HTTP ${response.status}）`)
  if (!Array.isArray(data?.order) || data.order.length !== defaultDetailSearchOrder.length
    || new Set(data.order).size !== defaultDetailSearchOrder.length
    || !data.order.every((id: unknown) => defaultDetailSearchOrder.includes(id as DetailSearchSourceID))) throw new Error('搜索设置返回的顺序无效，请重新读取')
  return { order: [...data.order] }
}
