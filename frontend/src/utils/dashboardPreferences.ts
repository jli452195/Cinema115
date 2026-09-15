import { reactive } from 'vue'

export type DashboardKind = 'movie' | 'tv'
export interface DashboardGenre { id: number; name: string }

const dashboardPathKeys: Record<DashboardKind, string> = {
  movie: 'cinema115.dashboard.movie.v1',
  tv: 'cinema115.dashboard.tv.v1',
}
const genreCacheKeys: Record<DashboardKind, string> = {
  movie: 'cinema115.tmdb.genres.movie.v1',
  tv: 'cinema115.tmdb.genres.tv.v1',
}
const genreCacheTTL = 30 * 24 * 60 * 60 * 1000

const defaultGenres: Record<DashboardKind, DashboardGenre[]> = {
  movie: [
    { id: 28, name: '动作' }, { id: 12, name: '冒险' }, { id: 16, name: '动画' },
    { id: 35, name: '喜剧' }, { id: 80, name: '犯罪' }, { id: 99, name: '纪录' },
    { id: 18, name: '剧情' }, { id: 10751, name: '家庭' }, { id: 14, name: '奇幻' },
    { id: 36, name: '历史' }, { id: 27, name: '恐怖' }, { id: 10402, name: '音乐' },
    { id: 9648, name: '悬疑' }, { id: 10749, name: '爱情' }, { id: 878, name: '科幻' },
    { id: 10770, name: '电视电影' }, { id: 53, name: '惊悚' }, { id: 10752, name: '战争' },
    { id: 37, name: '西部' },
  ],
  tv: [
    { id: 10759, name: '动作冒险' }, { id: 16, name: '动画' }, { id: 35, name: '喜剧' },
    { id: 80, name: '犯罪' }, { id: 99, name: '纪录' }, { id: 18, name: '剧情' },
    { id: 10751, name: '家庭' }, { id: 10762, name: '儿童' }, { id: 9648, name: '悬疑' },
    { id: 10763, name: '新闻' }, { id: 10764, name: '真人秀' }, { id: 10765, name: '科幻奇幻' },
    { id: 10766, name: '肥皂剧' }, { id: 10767, name: '脱口秀' }, { id: 10768, name: '战争政治' },
    { id: 37, name: '西部' },
  ],
}

function storageGet(key: string) {
  try { return window.localStorage.getItem(key) } catch { return null }
}

function storageSet(key: string, value: string) {
  try { window.localStorage.setItem(key, value) } catch { /* 页面仍可使用内存中的状态。 */ }
}

function readDashboardPath(kind: DashboardKind) {
  const fallback = `/${kind}`
  const value = storageGet(dashboardPathKeys[kind])
  if (!value) return fallback
  try {
    const url = new URL(value, window.location.origin)
    return url.origin === window.location.origin && url.pathname === fallback ? `${url.pathname}${url.search}` : fallback
  } catch {
    return fallback
  }
}

export const rememberedDashboardPaths = reactive<Record<DashboardKind, string>>({
  movie: readDashboardPath('movie'),
  tv: readDashboardPath('tv'),
})

export function rememberDashboardPath(kind: DashboardKind, fullPath: string) {
  const expectedPath = `/${kind}`
  try {
    const url = new URL(fullPath, window.location.origin)
    if (url.origin !== window.location.origin || url.pathname !== expectedPath) return
    const value = `${url.pathname}${url.search}`
    rememberedDashboardPaths[kind] = value
    storageSet(dashboardPathKeys[kind], value)
  } catch { /* 忽略无法解析的路由。 */ }
}

function validGenres(value: unknown): value is DashboardGenre[] {
  return Array.isArray(value) && value.length > 0 && value.every(item =>
    typeof item === 'object' && item !== null &&
    Number.isInteger((item as DashboardGenre).id) && (item as DashboardGenre).id > 0 &&
    typeof (item as DashboardGenre).name === 'string' && (item as DashboardGenre).name.trim() !== '')
}

export function cachedGenres(kind: DashboardKind) {
  const fallback = defaultGenres[kind].map(item => ({ ...item }))
  const value = storageGet(genreCacheKeys[kind])
  if (!value) return { genres: fallback, fresh: false }
  try {
    const parsed = JSON.parse(value) as { updatedAt?: unknown; genres?: unknown }
    if (!validGenres(parsed.genres) || typeof parsed.updatedAt !== 'number') return { genres: fallback, fresh: false }
    return {
      genres: normalizeGenres(kind, parsed.genres),
      fresh: Date.now() - parsed.updatedAt < genreCacheTTL,
    }
  } catch {
    return { genres: fallback, fresh: false }
  }
}

function normalizeGenres(kind: DashboardKind, genres: DashboardGenre[]) {
  const localizedNames = new Map(defaultGenres[kind].map(item => [item.id, item.name]))
  return genres.map(item => ({ id: item.id, name: localizedNames.get(item.id) ?? item.name }))
}

export function cacheGenres(kind: DashboardKind, genres: DashboardGenre[]) {
  if (!validGenres(genres)) return cachedGenres(kind).genres
  const normalized = normalizeGenres(kind, genres)
  storageSet(genreCacheKeys[kind], JSON.stringify({ updatedAt: Date.now(), genres: normalized }))
  return normalized
}
