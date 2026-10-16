import { ref, watch } from 'vue'
import { portalToken } from './usePortalAuth'
import { createPosterCache } from './useLazyPoster'
import type { EmbyMissingPage, EmbyMissingExcludedPage } from '../types/emby'

export const MISSING_PAGE_SIZE = 24
export type MissingMode = 'results' | 'excluded'
type PageData = EmbyMissingPage | EmbyMissingExcludedPage
interface ViewState { initialized: boolean; search: string; draft: string; offset: number; top: number }
interface Entry { data: PageData; savedAt: number; top: number }
const ttl = 120_000, capacity = 8
let owner = ''
const posters = createPosterCache(() => owner)
let session: ReturnType<typeof createSession> | undefined
function key(mode: MissingMode, search: string, offset: number) { return JSON.stringify([mode, search, offset]) }
function emptyView(): ViewState { return { initialized: false, search: '', draft: '', offset: 0, top: 0 } }

function createSession() {
  const pages = new Map<string, Entry>(), excludedCount = ref<number>()
  const views: Record<MissingMode, ViewState> = { results: emptyView(), excluded: emptyView() }
  let idle: ReturnType<typeof setTimeout> | undefined
  let syncedAt: string | undefined
  const state = {
    views, posters, excludedCount, lastMode: 'results' as MissingMode,
    activate() { clearTimeout(idle) },
    release() {
      posters.cancelPending()
      clearTimeout(idle)
      idle = setTimeout(() => {
        if (session !== state) return
        pages.clear(); posters.clear()
      }, ttl)
    },
    read(mode: MissingMode, search: string, offset: number) {
      const address = key(mode, search, offset), entry = pages.get(address)
      if (!entry) return
      pages.delete(address); pages.set(address, entry)
      return { ...entry, fresh: Date.now() - entry.savedAt < ttl }
    },
    write(mode: MissingMode, search: string, data: PageData) {
      if (mode === 'results') state.observeSync((data as EmbyMissingPage).syncedAt || '')
      const address = key(mode, search, data.offset), previous = pages.get(address)
      pages.delete(address); pages.set(address, { data, savedAt: Date.now(), top: previous?.top || 0 })
      while (pages.size > capacity) pages.delete(pages.keys().next().value!)
      excludedCount.value = mode === 'excluded' ? (data as EmbyMissingExcludedPage).count : (data as EmbyMissingPage).excludedCount
    },
    remember(mode: MissingMode, search: string, offset: number, draft: string, top: number) {
      Object.assign(views[mode], { initialized: true, search, offset, draft, top })
      const entry = pages.get(key(mode, search, offset))
      if (entry) entry.top = top
      state.lastMode = mode
    },
    invalidate() { pages.clear() },
    observeSync(value: string) {
      if (syncedAt !== undefined && syncedAt !== value) pages.clear()
      syncedAt = value
    },
    dispose() { clearTimeout(idle); pages.clear(); posters.clear() },
  }
  return state
}

// A single authenticated browser session owns at most eight data pages and 48 posters.
// No result data, search history or credentials are written to browser storage.
export function getEmbyMissingSession(token: string) {
  if (!session || owner !== token) {
    session?.dispose(); owner = token; session = createSession()
  }
  return session
}

export function invalidateEmbyMissingCache() { session?.invalidate() }
export function observeEmbyMissingSync(syncedAt: string) { session?.observeSync(syncedAt) }

// This also runs when logout happens on another route, after the view unmounted.
watch(portalToken, () => { session?.dispose(); session = undefined; owner = '' }, { flush: 'sync' })
