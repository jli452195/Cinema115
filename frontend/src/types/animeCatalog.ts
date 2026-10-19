export type AnimeProviderID = 'anibt' | 'mikan' | 'garden'
export const animeProviders = [
  { id: 'anibt', name: 'AniBT', available: false },
  { id: 'mikan', name: '蜜柑', available: true },
  { id: 'garden', name: 'Anime Garden', available: false },
] as const

// This DTO belongs to Mikan. Other providers keep their own card fields.
export interface MikanCatalogItem { id: string; title: string; coverUrl: string; url: string; updatedDate: string }
export interface MikanCatalog {
  provider: 'mikan'; season: string; days: { day: number; items: MikanCatalogItem[] }[]; items: MikanCatalogItem[]
  fetchedAt: number; expiresAt: number
}
