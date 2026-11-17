import type { TelegramChannelResource, TelegramChannelSource } from '../types/telegramUser'

export interface TelegramSourceReference { item: TelegramChannelResource; source: TelegramChannelSource }
export interface TelegramSourceCandidate extends TelegramSourceReference {
  url: string
  references: TelegramSourceReference[]
}

/** 页面身份只由官方域名及路径决定，查询参数、锚点和协议不产生新页面。 */
export function canonicalTelegramSourceURL(raw: string): string | undefined {
  try {
    const value = new URL(raw.trim())
    if (!['http:', 'https:'].includes(value.protocol) || value.hostname !== 'telegra.ph' || value.username || value.password || value.port) return undefined
    const path = decodeURIComponent(value.pathname).slice(1)
    if (!/^[\p{L}\p{N}_.-]{1,256}$/u.test(path) || path === '.' || path === '..') return undefined
    return `https://telegra.ph/${encodeURIComponent(path)}`
  } catch { return undefined }
}

/** 相同清单跨帖子合并，仍保留每个真实帖子的引用，片名不参与页面身份。 */
export function telegramSourceCandidates(resources: TelegramChannelResource[]): TelegramSourceCandidate[] {
  const pages = new Map<string, TelegramSourceCandidate>()
  const references = new Map<string, Set<string>>()
  for (const item of resources) for (const source of item.sources || []) {
    if (source.type !== 'telegraph') continue
    const url = canonicalTelegramSourceURL(source.url)
    if (!url) continue
    let candidate = pages.get(url)
    if (!candidate) {
      candidate = { url, item, source, references: [] }
      pages.set(url, candidate)
      references.set(url, new Set())
    }
    const identity = JSON.stringify([item.channel.id, item.id, source.id])
    if (references.get(url)!.has(identity)) continue
    references.get(url)!.add(identity)
    candidate.references.push({ item, source })
  }
  return [...pages.values()]
}
