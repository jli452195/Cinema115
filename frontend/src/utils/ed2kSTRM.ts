import type { ED2KSTRMResource } from '../types/ed2kSTRM'

export const ED2K_IMPORT_LIMIT = 50

// 局域网 HTTP 页面也可使用，UUID 中不包含资源或账号信息。
export function ed2kRequestID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6]! & 15) | 64
  bytes[8] = (bytes[8]! & 63) | 128
  const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function ed2kLinks(text: string): string[] {
  return [...new Set(text.match(/ed2k:\/\/\|file\|[^\r\n<>]+?\|\//gi) || [])]
}

export function ed2kResourceKey(resource: ED2KSTRMResource): string {
  return `${resource.hash.toLowerCase()}:${resource.size}`
}

export function ed2kResourceActive(resource: ED2KSTRMResource): boolean {
  return ['queued', 'pending', 'submitting', 'downloading', 'verifying', 'generating', 'recovering', 'restoring', 'running'].includes(resource.status)
}

export function ed2kResourceAttention(resource: ED2KSTRMResource): boolean {
  return ['failed', 'error', 'blocked', 'uncertain', 'conflict', 'interrupted'].includes(resource.status)
}

export function ed2kResourceStatus(resource: ED2KSTRMResource): string {
  return ({ queued: '等待缓存', downloading: '云下载中', recovering: '恢复缓存中', ready: '缓存就绪', failed: '缓存失败', blocked: '需检查', uncertain: '待确认' } as Record<string, string>)[resource.status] || '待检查'
}

export function ed2kResourceLink(resource: ED2KSTRMResource): string {
  return `ed2k://|file|${encodeURIComponent(resource.name)}|${resource.size}|${resource.hash}|/`
}

export function ed2kCacheStatus(resource: ED2KSTRMResource): string {
  if (resource.cacheState) return ({ ready: '缓存可用', available: '缓存可用', missing: '缓存缺失', unknown: '待核验', restoring: '恢复中', recovering: '恢复中' } as Record<string, string>)[resource.cacheState] || resource.cacheState
  if (['ready', 'completed'].includes(resource.status)) return '已记录缓存位置'
  if (resource.status === 'missing') return '缓存缺失'
  if (['recovering', 'restoring'].includes(resource.status)) return '恢复中'
  return '待核验'
}

export function ed2kSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '大小未知'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes, unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${value.toLocaleString('zh-CN', { maximumFractionDigits: unit ? 2 : 0 })} ${units[unit]}`
}

export function ed2kDate(value: ED2KSTRMResource['updatedAt']): string {
  if (!value) return '暂无记录'
  const date = new Date(typeof value === 'number' && value < 1e12 ? value * 1000 : value)
  return Number.isNaN(date.getTime()) ? '暂无记录' : date.toLocaleString('zh-CN', { hour12: false })
}
