export type AnimeProviderID = 'anibt' | 'mikan' | 'garden'
export const animeProviders = [
  { id: 'anibt', name: 'AniBT', available: true },
  { id: 'mikan', name: '蜜柑', available: true },
  { id: 'garden', name: 'Anime Garden', available: true },
] as const

// This DTO belongs to Mikan. Other providers keep their own card fields.
export interface MikanCatalogItem { id: string; title: string; coverUrl: string; url: string; updatedDate: string }
export interface MikanCatalog {
  provider: 'mikan'; season: string; days: { day: number; items: MikanCatalogItem[] }[]; items: MikanCatalogItem[]
  fetchedAt: number; expiresAt: number
}

export type AnimeSourceProvider = AnimeProviderID
export interface AnimeCatalogStatus {
 itemId: string
 bangumiId: string | null
 subscriptions: { id: string; status: 'draft' | 'paused' | 'enabled' | 'archived' }[]
}
export interface AnibtCatalogItem {
 id: string; bangumiId: string; animeId: string; title: string; originalTitle: string; coverUrl: string; format: string
 rating: number | null; episodes: number | null; airingAt: number | null; releaseCount: number; hasRelease: boolean
}
export interface AnibtCatalog {
 provider: 'anibt'; season: string; days: {day: number; items: AnibtCatalogItem[]}[]; items: AnibtCatalogItem[]
 total: number; truncated: boolean; fetchedAt: number; expiresAt: number
}
export interface GardenCatalogItem {
 id: string; bangumiId: string; title: string; originalTitle: string; coverUrl: string; platform: string; airDate: string
}
export interface GardenCatalog {
 provider: 'garden'; season: string; days: {day: number; items: GardenCatalogItem[]}[]; items: GardenCatalogItem[]
 truncated: boolean; fetchedAt: number; expiresAt: number
}
export type AnimeCatalog = MikanCatalog | AnibtCatalog | GardenCatalog
