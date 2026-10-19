import { watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeProviderID, AnimeCatalog, AnimeSourceProvider } from '../types/animeCatalog'
import type { MikanEntry } from '../types/animeSubscription'

const catalogs = new Map<string, AnimeCatalog>()
const queries = new Map<AnimeProviderID, Record<string, string>>()
const drafts = new Map<AnimeProviderID, string>()
const mikanEntries = new Map<string, { entry: MikanEntry; until: number }>()
let generation = 0
watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { generation++; catalogs.clear(); queries.clear(); drafts.clear(); mikanEntries.clear() }, { flush: 'sync' })
export function mikanEntrySnapshot(token: string, id: string) {
  if (!token || token !== portalToken.value || portalUser.value?.role !== 'admin') return
  const saved = mikanEntries.get(id)
  if (saved && saved.until > Date.now()) return structuredClone(saved.entry)
  mikanEntries.delete(id)
}
// The clicked card's read is handed to the editor in memory, never in the URL
// or history state. Saving still verifies the source on the server.
export async function requestMikanCatalogEntry(token: string, id: string, signal: AbortSignal) {
  const cached = mikanEntrySnapshot(token, id)
  if (cached) return cached
  const epoch = generation
  const result = await animeConfigRequest<{ entry: MikanEntry; expiresAt?: number }>(token, `sources/mikan/${id}`, { signal })
  if (signal.aborted || epoch !== generation) throw new DOMException('Request cancelled', 'AbortError')
  if (result.entry?.id !== id || !result.entry.title || !Array.isArray(result.entry.groups) || !Array.isArray(result.entry.bangumiIds)) throw new Error('蜜柑条目无法核对，请重新选择')
  const until = Math.min(Date.now() + 60000, Number(result.expiresAt) || Infinity)
  mikanEntries.delete(id); mikanEntries.set(id, { entry: structuredClone(result.entry), until })
  while (mikanEntries.size > 24) mikanEntries.delete(mikanEntries.keys().next().value!)
  return result.entry
}
export function catalogSnapshot(key: string) { return catalogs.get(key) }
export function catalogQuery(provider: AnimeProviderID) { return { ...queries.get(provider) } }
export function rememberCatalogQuery(provider: AnimeProviderID, query: Record<string, string>) { queries.set(provider, { ...query }) }
export function catalogDraft(provider: AnimeProviderID) { return drafts.get(provider) }
export function rememberCatalogDraft(provider: AnimeProviderID, value: string) { drafts.set(provider, value) }
export async function requestCatalog(token: string, provider: AnimeSourceProvider, key: string, body: unknown, signal: AbortSignal) {
  const epoch = generation
  const result = await animeConfigRequest<AnimeCatalog>(token, `catalog/${provider}`, { method: 'POST', body, signal })
  if (signal.aborted || epoch !== generation || token !== portalToken.value || portalUser.value?.role !== 'admin') throw new DOMException('Request cancelled', 'AbortError')
  if (result.provider !== provider || !Array.isArray(result.days) || !Array.isArray(result.items)) throw new Error(`${provider === 'anibt' ? 'AniBT' : provider === 'garden' ? 'Anime Garden' : '蜜柑'}返回的列表格式不正确`)
  catalogs.delete(key); catalogs.set(key, result)
  while (catalogs.size > 24) catalogs.delete(catalogs.keys().next().value!)
  return result
}
