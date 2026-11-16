import type { AnimeResourceItem, AnimeResourceResult } from '../types/animeResourceSearch'
import type { ResourceSearchSpec } from '../utils/resourceSpecFilters'
import { magnetKey } from './cloud-download-batch'

export interface DmhySearchInput { query: string; fallback: string; group: string; page: number; refresh: boolean }

async function dmhyRequest<T>(token: string, endpoint: 'search' | 'magnet', input: DmhySearchInput | { id: string }, signal: AbortSignal): Promise<T> {
  const response = await fetch(`/api/admin/dmhy/${endpoint}`, {
    method: 'POST', cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(input), signal: AbortSignal.any([signal, AbortSignal.timeout(50000)]),
  })
  const data = await response.json().catch(() => null) as (T & { error?: string }) | null
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用动漫花园' : data?.error || `动漫花园请求失败（HTTP ${response.status}）`)
  return data
}

export async function searchDmhyResources(token: string, input: DmhySearchInput, signal: AbortSignal): Promise<AnimeResourceResult> {
  const data = await dmhyRequest<AnimeResourceResult & { specs?: Record<string, ResourceSearchSpec> }>(token, 'search', input, signal)
  if (!Array.isArray(data.items) || !Array.isArray(data.groups) || typeof data.query !== 'string' || data.page !== input.page || typeof data.hasMore !== 'boolean') throw new Error('动漫花园搜索结果无法读取，请重试')
  return {
    ...data, attempts: data.attempts || [], group: input.group,
    items: data.items.map((item): AnimeResourceItem => {
      // 未识别的分类仍保留在补充结果，避免新分类被客户端丢弃。
      const categoryScope = ['film', 'other'].includes(item.categoryScope || '') ? item.categoryScope : 'supplement'
      const spec = data.specs?.[item.id]
      return { ...item, spec, categoryScope, resolution: spec?.resolution?.toUpperCase() || '未知' }
    }),
  }
}

export async function readDmhyMagnet(token: string, id: string, signal: AbortSignal): Promise<string> {
  if (!/^[1-9][0-9]{0,11}$/.test(id)) throw new Error('动漫花园原帖编号无效')
  const data = await dmhyRequest<{ id: string; magnet: string }>(token, 'magnet', { id }, signal)
  const hash = typeof data.magnet === 'string' ? magnetKey(data.magnet) : ''
  if (data.id !== id || !hash) throw new Error('动漫花园原帖磁力无法核验，请查看原帖')
  const url = new URL(data.magnet)
  if (url.searchParams.getAll('xt').length !== 1 || url.hash) throw new Error('动漫花园原帖磁力无法核验，请查看原帖')
  return `magnet:?xt=urn:btih:${hash}`
}
