import { animeConfigRequest } from './anime-config-api'
import type { ResourceSearchSpec } from '../utils/resourceSpecFilters'

const originNames: Record<string, string> = { dmhy: '动漫花园', mikan: '蜜柑计划', moe: '萌番组', ani: 'ANi' }
export interface GardenSearchGroup { id: string; name: string; origin: string; originName: string; kind: string }
interface GardenResource {
  provider: string; providerId: string; title: string; size: number; createdAt: string; magnet: string
  publisher: { id: number; name: string }; fansub?: { id: number; name: string } | null
}
export async function searchGardenResources(token: string, input: { query: string; fallback: string; group: string; groupName: string; page: number; refresh: boolean }, signal: AbortSignal) {
  const data = await animeConfigRequest<{ items: GardenResource[]; specs?: Record<string, ResourceSearchSpec>; groups: GardenSearchGroup[]; query: string; page: number; hasMore: boolean; cached: boolean; fetchedAt: string; expiresAt: string }>(token, 'resources/garden/search', { method: 'POST', body: input, signal })
  if (!Array.isArray(data.items) || !Array.isArray(data.groups) || typeof data.query !== 'string' || data.page !== input.page || typeof data.hasMore !== 'boolean') throw new Error('Garden 搜索结果无法读取，请重试')
  return { ...data, group: input.group, attempts: [], items: data.items.map(item => {
    const person = item.fansub || item.publisher
    const spec = data.specs?.[`${item.provider}:${item.providerId}`]
    const size = item.size >= 1024 ** 3 ? `${(item.size / 1024 ** 3).toFixed(2)} GB` : `${(item.size / 1024 ** 2).toFixed(1)} MB`
    return { id: `${item.provider}:${item.providerId}`, spec, title: item.title, size,
      updatedAt: new Date(item.createdAt).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }),
      magnet: item.magnet, url: `https://animes.garden/detail/${item.provider}/${item.providerId}`, torrentUrl: '', resolution: spec?.resolution?.toUpperCase() || '未知',
      groupId: `${item.provider}_${item.fansub ? 'f' : 'p'}${person.id}`,
      originName: originNames[item.provider] || item.provider,
      publisher: item.publisher.name, fansub: item.fansub?.name || '',
    }
  }) }
}
