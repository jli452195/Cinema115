import { watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeProviderID, AnimeCatalog, AnimeSourceProvider } from '../types/animeCatalog'

const catalogs = new Map<string, AnimeCatalog>()
const queries = new Map<AnimeProviderID, Record<string, string>>()
const drafts = new Map<AnimeProviderID, string>()
let generation = 0
watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { generation++; catalogs.clear(); queries.clear(); drafts.clear() }, { flush: 'sync' })
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
