import { watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeProviderID, AnimeCatalog, AnimeCatalogSeasons, AnimeSourceProvider } from '../types/animeCatalog'
import type { MikanEntry } from '../types/animeSubscription'
import { forgetAnimeCatalogStatus } from './useAnimeCatalogStatus'

const catalogs = new Map<string, AnimeCatalog>()
const seasons = new Map<AnimeProviderID, AnimeCatalogSeasons>()
const queries = new Map<AnimeProviderID, Record<string, string>>()
const drafts = new Map<AnimeProviderID, string>()
const mikanEntries = new Map<string, { entry: MikanEntry; until: number }>()
let generation = 0
watch([() => portalUser.value?.id, () => portalUser.value?.role], () => { generation++; catalogs.clear(); seasons.clear(); queries.clear(); drafts.clear(); mikanEntries.clear() }, { flush: 'sync' })
export function catalogSeasonsSnapshot(provider: AnimeProviderID) { return seasons.get(provider) }
export async function requestCatalogSeasons(token: string, provider: AnimeProviderID, signal: AbortSignal, refresh = false) {
  const epoch = generation
  const result = await animeConfigRequest<AnimeCatalogSeasons>(token, `catalog/${provider}/seasons${refresh ? '?refresh=true' : ''}`, { signal })
  if (signal.aborted || epoch !== generation || portalUser.value?.role !== 'admin') throw new DOMException('Request cancelled', 'AbortError')
  if (result.provider !== provider || !Array.isArray(result.seasons) || !result.seasons.length || result.seasons.length > 404 || result.seasons.some(value => typeof value !== 'string' || !/^(20\d{2}|2100)-(01|04|07|10)$/.test(value)) || !Number.isFinite(result.expiresAt)) throw new Error('季度目录暂时无法读取')
  seasons.set(provider, result)
  return result
}
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
  forgetAnimeCatalogStatus('mikan', id)
  const until = Math.min(Date.now() + 60000, Number(result.expiresAt) || Infinity)
  mikanEntries.delete(id); mikanEntries.set(id, { entry: structuredClone(result.entry), until })
  while (mikanEntries.size > 24) mikanEntries.delete(mikanEntries.keys().next().value!)
  return result.entry
}
export function catalogSnapshot(key: string) { return catalogs.get(key) }
export function animeCatalogItemSnapshot(provider: string, id: string): { title: string; coverUrl: string; date: string } | undefined {
  for (const catalog of [...catalogs.values()].reverse()) {
    if (catalog.provider !== provider) continue
    const item = catalog.items.find(item => item.id === id) || catalog.days.map(day => day.items.find(item => item.id === id)).find(item => !!item)
    if (item) return { title: item.title, coverUrl: item.coverUrl, date: 'airDate' in item ? item.airDate : '' }
  }
}
export function catalogQuery(provider: AnimeProviderID) { return { ...queries.get(provider) } }
export function rememberCatalogQuery(provider: AnimeProviderID, query: Record<string, string>) { queries.set(provider, { ...query }) }
export function catalogDraft(provider: AnimeProviderID) { return drafts.get(provider) }
export function rememberCatalogDraft(provider: AnimeProviderID, value: string) { drafts.set(provider, value) }
export async function requestCatalog(token: string, provider: AnimeSourceProvider, key: string, body: { mode: string; season?: string; keyword?: string; refresh: boolean }, signal: AbortSignal) {
  const epoch = generation
  const result = await animeConfigRequest<AnimeCatalog>(token, `catalog/${provider}`, { method: 'POST', body, signal })
  if (signal.aborted || epoch !== generation || portalUser.value?.role !== 'admin') throw new DOMException('Request cancelled', 'AbortError')
  if (result.provider !== provider || !Array.isArray(result.days) || !Array.isArray(result.items)) throw new Error(`${provider === 'anibt' ? 'AniBT' : provider === 'garden' ? 'Anime Garden' : '蜜柑'}返回的列表格式不正确`)
  if (result.season !== (body.mode === 'calendar' ? body.season : '')) throw new Error('返回的季度与请求不符，请重试')
  for (const item of result.items) forgetAnimeCatalogStatus(provider, item.id)
  for (const day of result.days) for (const item of day.items) forgetAnimeCatalogStatus(provider, item.id)
  catalogs.delete(key); catalogs.set(key, result)
  while (catalogs.size > 24) catalogs.delete(catalogs.keys().next().value!)
  return result
}
