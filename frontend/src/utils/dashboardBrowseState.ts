import { portalUser } from '../composables/usePortalAuth'
import type { MediaCardItem } from '../types/media'

export interface DashboardSnapshot {
  items: MediaCardItem[]
  total: number
  totalPages: number
  summary: string
  cachedAt: number
}
type Position = { left: number; top: number }
const snapshots = new Map<string, DashboardSnapshot>()
const positions = new Map<string, Position>()
const scrollKey = 'cinema115.dashboard.scroll.v1'
export const dashboardCacheLifetime = 60000

export function isDashboardPath(path: string) { return path === '/movie' || path === '/tv' }
export function dashboardBrowseScope() { return `${portalUser.value?.role || ''}:${portalUser.value?.id || ''}` }
function key(path: string) {
  const url = new URL(path, window.location.origin)
  url.searchParams.sort()
  return `${dashboardBrowseScope()}|${url.pathname}${url.search}`
}
function boundedSet<T>(map: Map<string, T>, id: string, value: T, limit: number) {
  map.delete(id); map.set(id, value)
  while (map.size > limit) map.delete(map.keys().next().value!)
}
try {
  const stored: unknown = JSON.parse(sessionStorage.getItem(scrollKey) || '[]')
  if (Array.isArray(stored)) for (const entry of stored.slice(-40)) {
    if (Array.isArray(entry) && typeof entry[0] === 'string' && Number.isFinite(entry[1]?.top) && entry[1].top >= 0 && Number.isFinite(entry[1]?.left) && entry[1].left >= 0) positions.set(entry[0], entry[1])
  }
} catch { /* In-memory restoration still works when browser storage is unavailable. */ }

export function rememberDashboardScroll(path: string, position: Position) {
  boundedSet(positions, key(path), position, 40)
  try { sessionStorage.setItem(scrollKey, JSON.stringify([...positions])) } catch { /* Keep the in-memory position. */ }
}
export function cachedDashboard(path: string) {
  const id = key(path), value = snapshots.get(id)
  if (value) boundedSet(snapshots, id, value, 8)
  return value
}
export function cacheDashboard(path: string, value: DashboardSnapshot) { boundedSet(snapshots, key(path), value, 8) }

// Router scrolling waits for the destination's actual cards, including cold/slow loads.
let navigation: { key: string; ready: Promise<boolean>; finish: (ready: boolean) => void } | undefined
export function prepareDashboardNavigation(path?: string) {
  navigation?.finish(false)
  navigation = undefined
  if (path) {
    let finish!: (ready: boolean) => void
    const ready = new Promise<boolean>(resolve => { finish = resolve })
    navigation = { key: key(path), ready, finish }
  }
}
export function dashboardLayoutReady(path: string) {
  if (navigation?.key === key(path)) navigation.finish(true)
}
export async function dashboardScrollPosition(path: string, saved?: Position | null): Promise<Position | false> {
  const current = navigation, id = key(path)
  if (!current || current.key !== id || !await current.ready || navigation !== current) return false
  return saved || positions.get(id) || { left: 0, top: 0 }
}
