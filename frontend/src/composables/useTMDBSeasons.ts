import { catalogAPI } from './usePortalAuth'
import { onBeforeUnmount, ref } from 'vue'
import type { ScheduledEpisode } from '../types/seasonLibrary'
import { createRequestQueue } from './requestQueue'

export interface CacheInfo { fetchedAt?: number; cacheStale?: boolean }
export interface TMDBEpisode extends ScheduledEpisode { id: number; name: string; overview: string; runtime: number; rating: number; votes: number; stillUrl: string; sourceSeason: number; sourceNumber: number }
export interface SeasonDetail extends CacheInfo { id: number; number: number; name: string; overview: string; episodes: TMDBEpisode[]; numberingRevision?: number }
export interface Schedule extends CacheInfo { number: number; episodes: ScheduledEpisode[]; numberingRevision?: number }
interface State<T> { loading: boolean; error: string; data?: T }
const queueTMDBRequest = createRequestQueue(4)

export function useTMDBSeasons(token: () => string, id: () => string, revision: () => number = () => 0) {
  const seasons = ref<Record<number, State<SeasonDetail>>>({})
  const schedules = ref<Record<number, State<Schedule>>>({})
  const controllers = new Map<string, AbortController>()
  let epoch = 0
  async function request(path: string, signal: AbortSignal) {
    const response = await fetch(catalogAPI(`tmdb/tv/${id()}/${path}${path.includes('?') ? '&' : '?'}numberingRevision=${revision()}`), { signal: AbortSignal.any([signal, AbortSignal.timeout(25000)]), headers: { Authorization: `Bearer ${token()}` } })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(data?.error || 'TMDB 请求失败')
    return data
  }
  function reset() {
    epoch++
    for (const controller of controllers.values()) controller.abort()
    controllers.clear(); seasons.value = {}; schedules.value = {}
  }
  async function prime(signal: AbortSignal) {
    const version = epoch
    try {
      const result = await request('schedules', AbortSignal.any([signal, AbortSignal.timeout(5000)]))
      if (signal.aborted || version !== epoch || !Array.isArray(result.seasons)) return
      for (const data of result.seasons as Schedule[]) {
        const current = schedules.value[data.number]
        if (current?.loading || (current?.data?.fetchedAt || 0) > (data.fetchedAt || 0)) continue
        if (Number.isInteger(data.number) && Array.isArray(data.episodes) && (data.numberingRevision || 0) === revision()) schedules.value[data.number] = { loading: false, error: '', data }
      }
    } catch { /* 本地缓存不可用时，仍可按季加载，不阻断详情页。 */ }
  }
  async function load(number: number, type: 'season' | 'schedule', force = false) {
    const states = type === 'season' ? seasons : schedules
    if (states.value[number]?.loading || (!force && states.value[number]?.data)) return
    const controller = new AbortController(), key = `${type}:${number}`
    const path = `${key.replace(':', '/')}${force ? '?refresh=1' : ''}`
    controllers.set(key, controller)
    states.value[number] = { ...states.value[number], loading: true, error: '' }
    try {
      const data = await queueTMDBRequest(controller.signal, () => request(path, controller.signal))
      if (controller.signal.aborted) return
      if (data.number !== number || !Array.isArray(data.episodes) || (data.numberingRevision || 0) !== revision()) throw new Error('季集方案已变化，请刷新详情')
      states.value[number] = { loading: false, error: '', data }
      // 先显示缓存，再更新过期数据；刷新失败时保留可见内容。
      if (data.cacheStale && !force) void load(number, type, true)
    } catch (e) {
      if (!controller.signal.aborted) states.value[number] = { ...states.value[number], loading: false, error: e instanceof Error ? e.message : '分集加载失败' }
    } finally {
      if (controllers.get(key) === controller) controllers.delete(key)
    }
  }
  function loadSeason(number: number, force = false) { return load(number, 'season', force) }
  function loadSchedule(number: number, force = false) { return load(number, 'schedule', force) }
  onBeforeUnmount(reset)
  return { seasons, schedules, reset, prime, loadSeason, loadSchedule }
}
