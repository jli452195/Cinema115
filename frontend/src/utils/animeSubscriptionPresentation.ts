import type { AnimeCardItem } from '../types/anime'
import type { AnimeSubscriptionView } from '../types/animeSubscription'

export function subscriptionStatus(status: string) {
  return ({ paused: '已暂停', enabled: '已启用', archived: '已归档' } as Record<string, string>)[status] || '未知状态'
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
export function subscriptionLifecycle(view: AnimeSubscriptionView, now = Date.now()) {
  const life = view.lifecycle
  if (view.status === 'archived') return ({ complete: '齐集整理完成', inactive: '长期停更归档', manual: '手动归档' } as Record<string, string>)[life?.archiveReason || ''] || ''
  if (view.status !== 'enabled' || !life?.quietSince) return ''
  const days = Math.floor((now - life.quietSince) / 86400000)
  return days >= 14 ? `${days} 天无新匹配资源${days >= 30 ? '，每日检查' : ''}` : ''
}
export function animeLocalTime(value?: number) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '尚无记录' }
