import re0Logo from '../assets/re0/logo-re0-square.webp'
import telegramLogo from '../assets/telegram/telegram.svg'
import gyingLogo from '../assets/gying/logo-gying.png'
import mikanLogo from '../assets/mikan/mikan-pic.svg?no-inline'
import gardenLogo from '../assets/anime-providers/garden.svg'
import woniuLogo from '../assets/woniu.svg'

export const defaultDetailSearchOrder = ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'woniu'] as const
export type DetailSearchSourceID = typeof defaultDetailSearchOrder[number]
export type DetailSearchAvailability = Partial<Record<DetailSearchSourceID, boolean>>
export interface DetailSearchSource { id: DetailSearchSourceID; name: string; label: string; logo: string; animationOnly?: boolean }

export const detailSearchSources: readonly DetailSearchSource[] = [
  { id: 're0', name: 'RE0', label: 'RE0 搜索', logo: re0Logo },
  { id: 'bot', name: 'Bot', label: 'Bot 搜索', logo: telegramLogo },
  { id: 'channels', name: 'TG 频道', label: 'TG 频道搜索', logo: telegramLogo },
  { id: 'gying', name: '观影', label: '观影搜索', logo: gyingLogo },
  { id: 'mikan', name: 'Mikan', label: 'Mikan 搜索', logo: mikanLogo, animationOnly: true },
  { id: 'garden', name: 'Garden', label: 'Garden 搜索', logo: gardenLogo, animationOnly: true },
  { id: 'woniu', name: '蜗牛', label: '蜗牛搜索', logo: woniuLogo },
]

export function visibleDetailSearchSources(order: readonly DetailSearchSourceID[], availability: DetailSearchAvailability, animation: boolean): DetailSearchSource[] {
  return order.flatMap(id => {
    const source = detailSearchSources.find(source => source.id === id)
    if (!source || (source.animationOnly ? !animation : availability[id] !== true)) return []
    return [source]
  })
}
