// 影视卡片的通用数据；详情路径由媒体类型和 TMDB ID 确定。
export interface MediaCardItem {
  id: number
  kind: 'tv' | 'movie'
  title: string
  posterUrl: string
  date: string
  rating: number
  votes: number
  originalTitle?: string
  overview?: string
}
