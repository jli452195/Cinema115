import type { AnimeSpec } from '../types/animeSubscription'

export type ResourceSearchSpec = Pick<AnimeSpec, 'resolution' | 'languages' | 'subtitleMode'>
export interface ResourceSpecSelection { resolution: string[]; language: string[]; external: boolean }
export interface ResourceSpecOption { group: keyof ResourceSpecSelection; value: string; label: string }
export function resourceChineseLanguage(spec: ResourceSearchSpec | undefined) {
  const simplified = spec?.languages?.includes('zh-Hans'), traditional = spec?.languages?.includes('zh-Hant')
  return simplified ? traditional ? 'both' : 'simplified' : traditional ? 'traditional' : ''
}
export function resourceSpecOptions(specs: (ResourceSearchSpec | undefined)[]): ResourceSpecOption[] {
  const resolutions = [...new Set(specs.map(spec => spec?.resolution).filter((value): value is string => !!value))].sort((a, b) => b.localeCompare(a, 'en', { numeric: true }))
  const languages = new Set<string>(specs.map(resourceChineseLanguage))
  return [
    ...resolutions.map(value => ({ group: 'resolution' as const, value, label: value.toUpperCase() })),
    ...[{ value: 'simplified', label: '简体' }, { value: 'traditional', label: '繁体' }, { value: 'both', label: '简繁' }].filter(option => languages.has(option.value)).map(option => ({ group: 'language' as const, ...option })),
    ...(specs.some(spec => spec?.subtitleMode === 'external') ? [{ group: 'external' as const, value: 'external', label: '外挂字幕' }] : []),
  ]
}
export function matchesResourceSpec(spec: ResourceSearchSpec | undefined, selection: ResourceSpecSelection) {
  return (!selection.resolution.length || selection.resolution.includes(spec?.resolution || ''))
    && (!selection.language.length || selection.language.includes(resourceChineseLanguage(spec)))
    && (!selection.external || spec?.subtitleMode === 'external')
}
