import type { ResourceSearchSpec } from '../utils/resourceSpecFilters'

export interface AnimeResourceItem { id: string; title: string; size: string; updatedAt: string; magnet: string; url: string; torrentUrl: string; resolution: string; spec?: ResourceSearchSpec; groupId?: string; originName?: string; publisher?: string; fansub?: string }
export interface AnimeResourceGroup { id: string; name: string; origin?: string; originName?: string; kind?: string }
export interface AnimeResourceBangumi { id: string; title: string; coverUrl: string; url: string }
export interface AnimeResourceExpansion { groupId: string; take: number }
export interface AnimeResourceResult { items: AnimeResourceItem[]; groups: AnimeResourceGroup[]; bangumis?: AnimeResourceBangumi[]; bangumi?: AnimeResourceBangumi; more?: AnimeResourceExpansion[]; query: string; group: string; cached: boolean; fetchedAt: string; expiresAt: string; attempts: { query: string; count: number; cached: boolean }[]; page?: number; hasMore?: boolean }
