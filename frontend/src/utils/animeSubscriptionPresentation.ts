import type { AnimeCardItem } from '../types/anime'
import type { AnimeSourceCard, AnimeSubscriptionView } from '../types/animeSubscription'

export function subscriptionStatus(status: string) {
  return ({ paused: '已暂停', enabled: '已启用', archived: '已归档' } as Record<string, string>)[status] || '未知状态'
}
export function subscriptionCard(view: Pick<AnimeSubscriptionView, 'entry'>): AnimeCardItem {
  return { id: view.entry.bangumiId || view.entry.id, title: view.entry.title, originalTitle: view.entry.originalTitle,
    coverUrl: view.entry.metadata?.coverUrl || '', date: view.entry.metadata?.airDate || '', weekday: null, url: '' }
}
export function sourceProgressDescription(source: Pick<AnimeSourceCard, 'groupName' | 'progress'>) {
  const p = source.progress
  const state = p?.state === 'complete' ? '已完成' : p?.state === 'ongoing' ? '进行中' : p?.state === 'missing' ? `缺 ${p.missingCount} 集${p.missing?.length ? `（${p.missing.map(gap => gap.first === gap.last ? `第 ${gap.first} 集` : `第 ${gap.first} 至 ${gap.last} 集`).join('、')}）` : ''}` : p?.state === 'numbering_unknown' ? `已整理 ${p.organizedCount} 集，请确认订阅首话` : '状态待更新'
  return `${source.groupName || '未命名字幕组'}：${state}`
}
export function subscriptionLifecycle(view: AnimeSubscriptionView, now = Date.now()) {
  const life = view.lifecycle
  if (view.status === 'archived') return ({ complete: '齐集整理完成', inactive: '长期停更归档', manual: '手动归档' } as Record<string, string>)[life?.archiveReason || ''] || ''
  if (view.status !== 'enabled' || !life?.quietSince) return ''
  const days = Math.floor((now - life.quietSince) / 86400000)
  return days >= 14 ? `${days} 天无新匹配资源${days >= 30 ? '，每日检查' : ''}` : ''
}
export function animeLocalTime(value?: number) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '尚无记录' }
