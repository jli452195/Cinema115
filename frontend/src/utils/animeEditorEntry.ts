import type { AnimeSourceProvider } from '../types/animeCatalog'

// The Bangumi chapter identifies the editor. A discovery site's entry is only
// an initial suggestion, carried in history state so it survives a refresh.
export function animeEditorEntry(bangumiId: string, provider: AnimeSourceProvider, itemId: string, returnTo: string) {
  return { path: '/anime/new', query: bangumiId ? { bangumiId } : {},
    state: { animeSource: { bangumiId, provider, itemId }, animeReturnTo: returnTo } }
}
