import type { AnimeResourceResult } from '../types/animeResourceSearch'
import type { DownloadFiles, DownloadResourceInput } from '../types/organize'
import type { ResourceSearchSpec } from '../utils/resourceSpecFilters'
import { p115Request } from './p115-api'

interface MikanSearchInput { query: string; fallback: string; group: string; refresh: boolean }
interface MikanBangumiInput { id: string; group: string; take: number; refresh: boolean }

async function requestMikanResources(token: string, endpoint: 'search' | 'bangumi', input: MikanSearchInput | MikanBangumiInput, signal: AbortSignal): Promise<AnimeResourceResult> {
  const response = await fetch(`/api/admin/mikan/${endpoint}`, {
    method: 'POST', cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
    signal: AbortSignal.any([signal, AbortSignal.timeout(75000)]),
  })
  const data = await response.json().catch(() => null) as (AnimeResourceResult & { specs?: Record<string, ResourceSearchSpec>; error?: string }) | null
  const action = endpoint === 'search' ? '蜜柑搜索' : '蜜柑番剧读取'
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用蜜柑搜索' : data?.error || `${action}失败（HTTP ${response.status}）`)
  return {
    ...data,
    items: data.items.map(item => {
      const spec = data.specs?.[item.id]
      return { ...item, spec, resolution: spec?.resolution?.toUpperCase() || '未知' }
    }),
  }
}

export function searchMikanResources(token: string, input: MikanSearchInput, signal: AbortSignal) {
  return requestMikanResources(token, 'search', input, signal)
}

export function readMikanBangumiResources(token: string, input: MikanBangumiInput, signal: AbortSignal) {
  return requestMikanResources(token, 'bangumi', input, signal)
}

export function loadAnimeOrganizeFiles(token: string, source: DownloadResourceInput, signal: AbortSignal) {
  return p115Request<DownloadFiles>(token, 'download-organize/files', {
    method: 'POST', body: JSON.stringify(source),
    signal: AbortSignal.any([signal, AbortSignal.timeout(50000)]),
  })
}

