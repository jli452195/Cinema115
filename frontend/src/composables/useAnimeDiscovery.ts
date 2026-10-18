import { ref, watch } from 'vue'
import { clearPortalSession, portalToken, portalUser } from './usePortalAuth'
import type { AnimeCacheMeta } from '../types/anime'

// Public metadata stays in bounded memory only. Never persist tokens or query results.
const cache = new Map<string, { data: unknown; expiresAt: number }>()
const scrolls = new Map<string, number>()
let generation = 0
let lastQuery: Record<string, string> | undefined
let cooldownTimer: ReturnType<typeof setTimeout> | undefined
export const animeCooldownUntil = ref(0)

watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => {
  generation++
  cache.clear(); scrolls.clear(); lastQuery = undefined
  clearTimeout(cooldownTimer); animeCooldownUntil.value = 0
}, { flush: 'sync' })

export function animeRememberedQuery() { return lastQuery ? { ...lastQuery } : undefined }
export function rememberAnimeQuery(query: Record<string, string>) { lastQuery = { ...query } }
export function rememberAnimeScroll(key: string, top: number) {
  scrolls.delete(key); scrolls.set(key, top)
  if (scrolls.size > 32) scrolls.delete(scrolls.keys().next().value!)
}
export function animeScroll(key: string) { return scrolls.get(key) ?? 0 }
export function cachedAnime<T>(key: string): { data: T; fresh: boolean } | undefined {
  const entry = cache.get(key)
  if (!entry) return
  cache.delete(key); cache.set(key, entry)
  return { data: entry.data as T, fresh: entry.expiresAt > Date.now() }
}

function setCooldown(until: number | null | undefined) {
  if (!until || !Number.isFinite(until) || until <= Date.now()) return
  const deadline = Math.max(until, animeCooldownUntil.value)
  animeCooldownUntil.value = deadline
  clearTimeout(cooldownTimer)
  cooldownTimer = setTimeout(() => { animeCooldownUntil.value = 0 }, Math.min(deadline - Date.now(), 2147483647))
}

export function animeFailure(error: unknown) {
  return error instanceof Error ? error.message : '读取失败，请稍后重试'
}
export function animeCooldownMessage() {
  return `Bangumi 暂时限流，请在 ${new Date(animeCooldownUntil.value).toLocaleTimeString('zh-CN', { hour12: false })} 后重试`
}
export function animeImageURL(value: string) {
  try { const url = new URL(value); return url.protocol === 'https:' ? url.href : '' } catch { return '' }
}

export async function requestAnime<T>(key: string, path: string, token: string, signal: AbortSignal, body?: unknown): Promise<T> {
  if (!token || token !== portalToken.value || portalUser.value?.role !== 'admin') throw new DOMException('Session changed', 'AbortError')
  if (animeCooldownUntil.value > Date.now()) throw new Error(animeCooldownMessage())
  const epoch = generation
  const response = await fetch(`/api/admin/anime/discovery/${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.any([signal, AbortSignal.timeout(30000)]),
  })
  const data = await response.json().catch(() => null)
  if (signal.aborted || epoch !== generation) throw new DOMException('Request cancelled', 'AbortError')
  if (!response.ok || !data) {
    if (response.status === 401) clearPortalSession()
    if (data?.code === 'SOURCE_COOLDOWN') setCooldown(data.details?.retryAfterAt || Date.now() + Math.max(1, Number(response.headers.get('Retry-After')) || 30) * 1000)
    throw new Error(data?.code === 'SOURCE_COOLDOWN' ? animeCooldownMessage() : data?.error || `读取失败（HTTP ${response.status}）`)
  }
  const metadata: AnimeCacheMeta = data.metadata || data
  setCooldown(metadata.issue?.retryAfterAt)
  if (data.relations) setCooldown(data.relations.issue?.retryAfterAt)
  // A stale relation must not become fresh just because the subject is fresh.
  const rawExpires = Math.min(metadata.expiresAt, data.relations?.expiresAt ?? Infinity)
  const expiresAt = !metadata.stale && !data.relations?.stale && Number.isFinite(rawExpires) ? rawExpires : 0
  cache.delete(key); cache.set(key, { data, expiresAt })
  while (cache.size > 32) cache.delete(cache.keys().next().value!)
  return data as T
}
