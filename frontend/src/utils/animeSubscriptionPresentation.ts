import type { AnimeCardItem } from '../types/anime'
import type { AnimeSourceCard, AnimeSourceProgress, AnimeSubscriptionView } from '../types/animeSubscription'

export function subscriptionStatus(status: string) {
  return ({ paused: '已暂停', enabled: '已启用', archived: '已归档' } as Record<string, string>)[status] || '未知状态'
}
export function subscriptionCard(view: Pick<AnimeSubscriptionView, 'entry'>): AnimeCardItem {
  return { id: view.entry.bangumiId || view.entry.id, title: view.entry.title, originalTitle: view.entry.originalTitle,
    coverUrl: view.entry.metadata?.coverUrl || '', date: view.entry.metadata?.airDate || '', weekday: null, url: '' }
}
export function sourceProgressDescription(source: Pick<AnimeSourceCard, 'groupName' | 'progress'>) {
  const p = source.progress
  const state = p?.state === 'complete' ? '已完成' : p?.state === 'ongoing' ? '进行中' : p?.state === 'missing' ? `缺 ${p.missingCount} 集${p.missing?.length ? `（${p.missing.map(gap => gap.first === gap.last ? `第 ${gap.first} 集` : `第 ${gap.first} 至 ${gap.last} 集`).join('、')}）` : ''}` : '状态待更新'
  return `${source.groupName || '未命名字幕组'}：${state}`
}
interface SourceProgressSegment { first: number; last: number; state: 'organized' | 'missing' | 'unchecked' }
function progressRangesLabel(ranges: { first: number; last: number }[]) {
  return ranges.slice(0, 4).map(range => range.first === range.last ? String(range.first) : `${range.first}–${range.last}`).join('、') + (ranges.length > 4 ? '等' : '')
}
export function sourceProgressView(progress?: AnimeSourceProgress, pending = false) {
  const segments: SourceProgressSegment[] = []
  if (pending || !progress || progress.state === 'unknown') return { state: 'unknown', count: 0, total: 0, missing: 0, segments, label: pending ? '保存后更新进度' : '等待检查', status: '', coverage: '', detail: '', hint: '' }
  const count = progress.organizedCount, missing = progress.missingCount, published = count + missing
  const total = progress.targetCount || published
  let cursor = progress.firstEpisode
  // 后端已合并并排序缺集区间，只按区间补齐已整理段，避免逐集创建元素。
  if (published && (!missing || progress.missing?.length)) {
    for (const gap of progress.missing || []) {
      if (cursor < gap.first) segments.push({ first: cursor, last: gap.first - 1, state: 'organized' })
      segments.push({ ...gap, state: 'missing' })
      cursor = gap.last + 1
    }
    if (cursor <= progress.publishedThrough) segments.push({ first: cursor, last: progress.publishedThrough, state: 'organized' })
    cursor = progress.publishedThrough + 1
  }
  const end = progress.firstEpisode + total - 1
  if (cursor <= end) segments.push({ first: cursor, last: end, state: 'unchecked' })
  const state = published ? progress.state : 'unknown'
  const label = total ? `${count} / ${total} 集${!progress.targetCount ? ' · 当前更新' : ''}` : '等待发布'
  const gaps = progressRangesLabel(progress.missing || [])
  const status = !published ? '' : state === 'complete' ? '已齐集' : missing ? `待整理 ${missing} 集` : ''
  const organized = progressRangesLabel(segments.filter(segment => segment.state === 'organized'))
  const coverage = [organized ? `已整理：${organized}` : '', gaps ? `待整理：${gaps}` : ''].filter(Boolean).join(' · ')
  const detail = published || progress.firstEpisode > progress.publishedThrough ? `从第 ${progress.firstEpisode} 集起 · 已检查到第 ${progress.publishedThrough} 集${progress.targetCount ? '' : ' · 当前更新范围'}` : ''
  const hint = [detail, coverage].filter(Boolean).join('；')
  return { state, count, total, missing, segments, label, status, coverage, detail, hint }
}
export function subscriptionLifecycle(view: AnimeSubscriptionView, now = Date.now()) {
  const life = view.lifecycle
  if (view.status === 'archived') return ({ complete: '齐集整理完成', inactive: '长期停更归档', manual: '手动归档' } as Record<string, string>)[life?.archiveReason || ''] || ''
  if (view.status !== 'enabled' || !life?.quietSince) return ''
  const days = Math.floor((now - life.quietSince) / 86400000)
  return days >= 14 ? `${days} 天无新匹配资源${days >= 30 ? '，每日检查' : ''}` : ''
}
export function animeLocalTime(value?: number) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : '尚无记录' }
