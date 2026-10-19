import type { AnimeCatalogStatus, AnimeSourceProvider } from '../types/animeCatalog'
import { animeEditorEntry } from './animeEditorEntry'

const labels = { draft: '草稿', paused: '已暂停', enabled: '订阅中', archived: '已归档' } as const
export function animeCatalogSubscriptionLabel(state?: AnimeCatalogStatus) {
  if (!state?.subscriptions.length) return ''
  return state.subscriptions.length > 1 ? `${state.subscriptions.length} 个配置` : labels[state.subscriptions[0]!.status]
}
export function animeCatalogSubscriptionTarget(state: AnimeCatalogStatus | undefined, bangumiId: string, provider: AnimeSourceProvider, itemId: string, returnTo: string) {
  const chapterId = state?.bangumiId || bangumiId
  if (!state?.subscriptions.length) return animeEditorEntry(chapterId, provider, itemId, returnTo)
  return {
    path: state.subscriptions.length === 1 ? `/anime/subscriptions/${encodeURIComponent(state.subscriptions[0]!.id)}` : '/anime/subscriptions',
    query: { ...(chapterId ? { bangumiId: chapterId } : {}), returnTo },
    state: { animeCatalogDirect: state.subscriptions.length === 1 },
  }
}
export function animeCatalogCardLabel(title: string, state?: AnimeCatalogStatus) {
  const label = animeCatalogSubscriptionLabel(state)
  return label ? `${title}，${label}，查看订阅配置` : `${title}，配置订阅`
}
