import { watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { animeSubscriptionsVersion } from './useAnimeSubscriptionBrowser'
import { animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeCatalogStatus, AnimeSourceProvider } from '../types/animeCatalog'

const cache = new Map<string, { data: AnimeCatalogStatus; until: number }>()
let generation = 0
watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role, animeSubscriptionsVersion], () => {
  generation++; cache.clear()
}, { flush: 'sync' })

export function forgetAnimeCatalogStatus(provider: AnimeSourceProvider, id: string) { cache.delete(`${provider}:${id}`) }
export function animeCatalogStatusSnapshot(provider: AnimeSourceProvider, ids: string[]) {
  const result: Record<string, AnimeCatalogStatus> = {}
  if (!portalToken.value || portalUser.value?.role !== 'admin') return result
  for (const id of ids) {
    const saved = cache.get(`${provider}:${id}`)
    if (saved && saved.until > Date.now()) result[id] = structuredClone(saved.data)
  }
  return result
}
// Site content lasts longer than local subscription status. Only visible cards
// are checked, in local batches, without fetching any site's entry pages.
export async function requestAnimeCatalogStatuses(token: string, provider: AnimeSourceProvider, ids: string[], signal: AbortSignal, force = false) {
  const epoch = generation
  const allowed = () => !signal.aborted && epoch === generation && token && token === portalToken.value && portalUser.value?.role === 'admin'
  if (!allowed()) throw new DOMException('Request cancelled', 'AbortError')
  const unique = [...new Set(ids)]
  const missing = unique.filter(id => force || (cache.get(`${provider}:${id}`)?.until || 0) <= Date.now())
  const result: Record<string, AnimeCatalogStatus> = {}
  for (const id of unique) {
    const saved = cache.get(`${provider}:${id}`)
    if (saved && !missing.includes(id)) result[id] = structuredClone(saved.data)
  }
  for (let offset = 0; offset < missing.length; offset += 200) {
    const batch = missing.slice(offset, offset + 200)
    const data = await animeConfigRequest<{ items: AnimeCatalogStatus[] }>(token, 'catalog-status', { method: 'POST', body: { provider, itemIds: batch }, signal })
    if (!allowed()) throw new DOMException('Request cancelled', 'AbortError')
    if (!Array.isArray(data.items) || data.items.length !== batch.length || new Set(data.items.map(item => item.itemId)).size !== batch.length || data.items.some(item => !batch.includes(item.itemId) || item.bangumiId !== null && !/^[1-9]\d{0,11}$/.test(item.bangumiId) || !Array.isArray(item.subscriptions) || item.subscriptions.some(sub => !/^[a-zA-Z0-9_-]{1,128}$/.test(sub.id) || !['paused', 'enabled', 'archived'].includes(sub.status)))) throw new Error('订阅状态暂时无法核对')
    for (const item of data.items) {
      const key = `${provider}:${item.itemId}`
      cache.delete(key); cache.set(key, { data: structuredClone(item), until: Date.now() + 30000 })
      result[item.itemId] = item
    }
    while (cache.size > 600) cache.delete(cache.keys().next().value!)
  }
  return result
}
