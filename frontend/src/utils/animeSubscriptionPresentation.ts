import type { AnimeCardItem } from '../types/anime'
import type { AnimeSubscriptionView } from '../types/animeSubscription'

export function subscriptionStatus(status: string) {
  return ({ paused: '已暂停', enabled: '已启用', archived: '已归档', removed: '已移除' } as Record<string, string>)[status] || '未知状态'
}
export function subscriptionCard(view: AnimeSubscriptionView): AnimeCardItem {
  return { id: view.entry.bangumiId || view.entry.id, title: view.entry.title, originalTitle: view.entry.originalTitle,
    coverUrl: view.entry.metadata?.coverUrl || '', date: view.entry.metadata?.airDate || '', weekday: null, url: '' }
}
export function subscriptionSources(view: AnimeSubscriptionView) { return (view.sources || []).filter(s => s.configuredState !== 'removed').sort((a, b) => a.position - b.position) }
export function subscriptionSummary(view: AnimeSubscriptionView) {
  const sources = subscriptionSources(view), main = sources.find(s => s.role === 'main'), count = sources.filter(s => s.role === 'standby').length
  return `主源：${main?.groupName || '待配置'}${count ? ` · 备用 ${count}` : ''}`
}
export function animeLocalTime(value?: number) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '尚无记录' }
