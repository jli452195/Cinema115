import { watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { animeConfigRequest } from '../lib/anime-config-api'

// Local configuration reads only. Writes keep their own idempotency and receipt handling.
const cache = new Map<string, { data: unknown; fetchedAt: number; version: number }>()
const states = new Map<string, { top: number; expanded: string[] }>()
let version = 0
let lastQuery: Record<string, string> | undefined
let lastID = ''
function boundedSet<T>(map: Map<string, T>, key: string, value: T) {
  map.delete(key); map.set(key, value)
  while (map.size > 40) map.delete(map.keys().next().value!)
}
watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => {
  version++; cache.clear(); states.clear(); lastQuery = undefined; lastID = ''
}, { flush: 'sync' })

export function invalidateAnimeSubscriptions(id?: string) {
  version++
  if (id) cache.delete(`subscriptions/${encodeURIComponent(id)}`)
}
export function animeSubscriptionQuery() { return lastQuery ? { ...lastQuery } : undefined }
export function animeSubscriptionLocation() { return lastQuery ? { path: lastID ? `/anime/subscriptions/${encodeURIComponent(lastID)}` : '/anime/subscriptions', query: { ...lastQuery } } : undefined }
export function rememberSubscriptionQuery(query: Record<string, string>, id = '') {
  if (!portalToken.value || portalUser.value?.role !== 'admin') return
  lastQuery = { ...query }; lastID = id
}
export function subscriptionState(key: string) { return { ...(states.get(key) || { top: 0, expanded: [] }), saved: states.has(key) } }
export function rememberSubscriptionState(key: string, state: { top: number; expanded: string[] }) {
  if (!portalToken.value || portalUser.value?.role !== 'admin') return
  boundedSet(states, key, { top: state.top, expanded: [...state.expanded] })
}
export function cachedSubscription<T>(key: string) {
  const item = cache.get(key)
  if (!item) return
  boundedSet(cache, key, item)
  return { data: item.data as T, fetchedAt: item.fetchedAt, fresh: item.version === version && Date.now() - item.fetchedAt < 30000 }
}
export async function readSubscription<T>(key: string, token: string, signal: AbortSignal) {
  const epoch = version
  const data = await animeConfigRequest<T>(token, key, { signal })
  if (signal.aborted || epoch !== version) throw new DOMException('Configuration changed', 'AbortError')
  boundedSet(cache, key, { data, fetchedAt: Date.now(), version })
  return data
}

// Return targets are restricted to read-only anime routes, including their URL context.
export function animeReturnPath(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length > 2048 || !/^\/anime(?:\/subscriptions(?:\/[A-Za-z0-9_-]+)?)?(?:\?|$)/.test(value)) return
  try {
    const url = new URL(value, 'http://anime.local')
    if (url.origin !== 'http://anime.local' || url.hash || !/^\/anime(?:\/subscriptions(?:\/[A-Za-z0-9_-]+)?)?$/.test(url.pathname)) return
    return url.pathname + url.search
  } catch { return }
}
