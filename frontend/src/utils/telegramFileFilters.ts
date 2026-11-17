import { matchTelegramFiles, type TelegramFile } from './telegramFiles'

const definitions = [
  { key: 'season', label: '筛选季', allLabel: '全部季' },
  { key: 'resolution', label: '筛选清晰度', allLabel: '全部清晰度' },
  { key: 'dynamicRange', label: '筛选动态范围', allLabel: '全部动态范围' },
  { key: 'platform', label: '筛选平台', allLabel: '全部平台' },
  { key: 'releaseGroup', label: '筛选发布组', allLabel: '全部发布组' },
] as const

export type TelegramFileFilterField = typeof definitions[number]['key']
export type TelegramFileFilterValues = Record<TelegramFileFilterField, string>
export interface TelegramFileFilterOption { value: string; label: string; count: number }
export interface TelegramFileFacet {
  key: TelegramFileFilterField
  label: string
  value: string
  selectedLabel: string
  options: TelegramFileFilterOption[]
}

const compare = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' }).compare

export function defaultTelegramFileFilters(): TelegramFileFilterValues {
  return { season: 'all', resolution: 'all', dynamicRange: 'all', platform: 'all', releaseGroup: 'all' }
}

function fieldValue(file: TelegramFile, field: TelegramFileFilterField): string {
  if (field === 'season') return Number.isFinite(file.season) ? String(file.season) : ''
  return file.metadata[field]?.trim() || ''
}

function valueLabel(field: TelegramFileFilterField, value: string): string {
  return field === 'season' ? value === '0' ? '特别篇' : `第 ${value} 季` : value
}

// 按关键词和上游条件依次计算选项，保留兼容选择，冲突的下游条件恢复为全部。
export function resolveTelegramFileFilters(files: readonly TelegramFile[], filters: TelegramFileFilterValues, query = '') {
  const values = { ...filters }, facets: TelegramFileFacet[] = []
  let remaining = matchTelegramFiles(files, { query })
  for (const { key, label, allLabel } of definitions) {
    const counts = new Map<string, number>()
    for (const file of remaining) {
      const value = fieldValue(file, key)
      if (value) counts.set(value, (counts.get(value) || 0) + 1)
    }
    if (!counts.has(values[key])) values[key] = 'all'
    const value = values[key]
    facets.push({
      key, label, value, selectedLabel: value === 'all' ? allLabel : valueLabel(key, value),
      options: [
        { value: 'all', label: allLabel, count: remaining.length },
        ...[...counts].sort(([left], [right]) => compare(left, right)).map(([value, count]) => ({ value, label: `${valueLabel(key, value)}（${count}）`, count })),
      ],
    })
    if (value !== 'all') remaining = remaining.filter(file => fieldValue(file, key) === value)
  }
  return { values, facets, files: remaining }
}
