import type { TelegramChannelLink, TelegramChannelMetadata, TelegramChannelResource, TelegramChannelSourceResult } from '../types/telegramUser'
import { normalize115Share } from './shareLink'
import type { TelegramSourceCandidate } from './telegramSources'

interface ShareSource { url: string; label: string }

export interface TelegramShareLink extends TelegramChannelLink {
  id: string
  canonicalURL: string
  directIdentities: string[]
  sources: ShareSource[]
  versions: string[]
}

export interface TelegramShareCard {
  id: string
  title: string
  pageTitle?: string
  channelTitle: string
  postedAt: string
  metadata?: TelegramChannelMetadata
  links: TelegramShareLink[]
}

interface ShareEntry {
  card: Omit<TelegramShareCard, 'links'>
  link: TelegramShareLink
  direct: boolean
}

function combineShareDetails(target: TelegramShareLink, source: TelegramShareLink) {
  target.metadata ??= source.metadata
  for (const reference of source.sources) if (!target.sources.some(item => item.url === reference.url)) target.sources.push(reference)
  for (const title of source.versions) if (!target.versions.includes(title)) target.versions.push(title)
}

export function telegramShareIdentities(link: TelegramShareLink, kind: 'movie' | 'tv', tmdbId: string) {
  return [...link.directIdentities.map(identity => `tg-channel/${identity}`), `tg-channel/${kind}/${tmdbId}/telegraph/${link.canonicalURL}`]
}

export function telegramShareCards(
  resources: readonly TelegramChannelResource[],
  candidates: readonly TelegramSourceCandidate[],
  pages: Readonly<Record<string, TelegramChannelSourceResult>>,
): TelegramShareCard[] {
  const byCode = new Map<string, Map<string, ShareEntry>>()
  function add(card: ShareEntry['card'], link: TelegramChannelLink, sources: ShareSource[], direct: boolean) {
    if (link.type !== '115') return
    const canonical = normalize115Share(link.url)
    if (!canonical) return
    const code = canonical.split('?', 1)[0]!
    let variants = byCode.get(code)
    if (!variants) {
      variants = new Map()
      byCode.set(code, variants)
    }
    const value: TelegramShareLink = {
      ...link, id: canonical, canonicalURL: canonical, directIdentities: direct ? [`${card.id}/${link.url}`] : [],
      sources: [...sources], versions: direct ? [] : [card.title],
    }
    const previous = variants.get(canonical)
    if (previous) {
      combineShareDetails(previous.link, value)
      for (const identity of value.directIdentities) if (!previous.link.directIdentities.includes(identity)) previous.link.directIdentities.push(identity)
    } else variants.set(canonical, { card, link: value, direct })
  }
  // 直链优先保留原帖卡片和既有 STRM 任务标识，清单补充重复分享的来源。
  for (const item of resources) {
    const { id, title, postedAt, metadata } = item
    const card = { id, title, postedAt, metadata, channelTitle: item.channel.title }
    const sources = [{ url: item.sourceUrl, label: item.channel.title }]
    for (const link of item.links) add(card, link, sources, true)
  }
  for (const candidate of candidates) {
    const page = pages[candidate.url]
    if (!page?.shareGroups?.length) continue
    const sources = [{ url: candidate.url, label: 'Telegraph 清单' }, ...candidate.references.map(({ item }) => ({ url: item.sourceUrl, label: item.channel.title }))]
    for (const group of page.shareGroups) {
      const card = {
        id: `${candidate.url}/${group.id}`, title: group.title || page.pageTitle, pageTitle: page.pageTitle,
        postedAt: candidate.item.postedAt, channelTitle: candidate.item.channel.title,
      }
      for (const link of group.links) add(card, link, sources, false)
    }
  }
  const cards = new Map<string, TelegramShareCard>()
  for (const [code, variants] of byCode) {
    const bare = variants.get(code)
    if (bare && variants.size > 1) {
      // 无码条目可携带原帖来源，明确的不同提取码仍各自保留。
      variants.delete(code)
      for (const entry of variants.values()) {
        if (variants.size === 1 && bare.direct && !entry.direct) {
          entry.card = bare.card
          entry.link.id = bare.link.id
          entry.link.directIdentities = bare.link.directIdentities.slice(0, 1)
        }
        combineShareDetails(entry.link, bare.link)
      }
    }
    for (const { card, link } of variants.values()) {
      const current = cards.get(card.id)
      if (current) current.links.push(link)
      else cards.set(card.id, { ...card, links: [link] })
    }
  }
  return [...cards.values()]
}
