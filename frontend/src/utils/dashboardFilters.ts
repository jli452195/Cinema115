import type { DashboardKind } from './dashboardPreferences'

export const dashboardKindOptions = [
  { value: 'movie', label: '电影' },
  { value: 'tv', label: '剧集' },
] as const

const allCategories = { value: '', label: '全部' }
const animation = { value: 'animation', label: '动漫' }
const documentary = { value: 'documentary', label: '纪录' }
export const dashboardCategoryOptions = {
  movie: [allCategories, { value: 'film', label: '电影' }, animation, documentary],
  tv: [allCategories, { value: 'series', label: '电视剧' }, animation, documentary, { value: 'variety', label: '综艺' }],
}

export const dashboardRegions = [{ id: '', name: '全部' }, { id: 'CN', name: '大陆' }, { id: 'HK', name: '香港' }, { id: 'TW', name: '台湾' }, { id: 'JP', name: '日本' }, { id: 'KR', name: '韩国' }, { id: 'TH', name: '泰国' }, { id: 'western', name: '欧美' }]
export const dashboardSorts = [{ id: 'popular', name: '热门' }, { id: 'latest', name: '最新' }, { id: 'rating', name: 'TMDB 评分' }]
export const defaultDashboardRefinements = { genre: '', region: '', year: '', sort: 'popular' }
export type DashboardFilters = { category: string } & typeof defaultDashboardRefinements

const excludedGenres: Readonly<Partial<Record<string, readonly number[]>>> = {
  film: [16, 99],
  series: [16, 99, 10764, 10767, 10763],
  animation: [16],
  documentary: [99],
  variety: [10764, 10767],
}

// 类别已隐含或明确排除的题材不再作为额外条件。
export function isDashboardGenreCompatible(category: string, id: number) {
  return !excludedGenres[category]?.includes(id)
}

export function readDashboardFilters(kind: DashboardKind, query: Record<string, unknown>): DashboardFilters {
  const category = dashboardCategoryOptions[kind].find(item => item.value === query.category)?.value ?? ''
  const { genre, region, year, sort } = query
  return {
    category,
    genre: typeof genre === 'string' && /^[1-9]\d{0,4}$/.test(genre) && isDashboardGenreCompatible(category, Number(genre)) ? genre : '',
    region: dashboardRegions.find(item => item.id === region)?.id ?? '',
    year: typeof year === 'string' && /^\d{4}$/.test(year) && Number(year) >= 1900 && Number(year) <= new Date().getFullYear() ? year : '',
    sort: dashboardSorts.find(item => item.id === sort)?.id ?? defaultDashboardRefinements.sort,
  }
}
