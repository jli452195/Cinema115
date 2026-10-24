import type { AnimeSpec } from '../types/animeSubscription'

export interface ResourceSearchSpec extends AnimeSpec { bitrate?: string }
export const resourceSpecFields = [
  { key: 'codec', label: '编码', labels: { h264: 'H.264 / AVC', h265: 'H.265 / HEVC', av1: 'AV1', vp9: 'VP9' } },
  { key: 'bitDepth', label: '位深', labels: { '8': '8bit', '10': '10bit', '12': '12bit' } },
  { key: 'bitrate', label: '码率', labels: {} },
  { key: 'languages', label: '字幕', labels: { 'zh-Hans': '简体', 'zh-Hant': '繁体', ja: '日语', en: '英语' } },
  { key: 'subtitleMode', label: '字幕形式', labels: { hard: '内嵌', soft: '内封', external: '外挂' } },
] as const
export type ResourceSpecKey = typeof resourceSpecFields[number]['key']
export type ResourceSpecSelection = Record<ResourceSpecKey, string>
const values = (spec: ResourceSearchSpec | undefined, key: ResourceSpecKey): string[] => {
  const value = spec?.[key]
  return Array.isArray(value) ? value : value ? [value] : []
}
export function resourceSpecOptions(specs: (ResourceSearchSpec | undefined)[], field: typeof resourceSpecFields[number], selected: string) {
  const labels = field.labels as Record<string, string>
  const found = new Set(specs.flatMap(spec => values(spec, field.key)))
  if (specs.some(spec => !values(spec, field.key).length)) found.add('unknown')
  if (selected) found.add(selected)
  return [{ value: '', label: `全部${field.label}` }, ...[...found].sort((a, b) => a.localeCompare(b, 'zh-CN', { numeric: true })).map(value => ({ value, label: value === 'unknown' ? '未标注' : labels[value] || value }))]
}
export function matchesResourceSpec(spec: ResourceSearchSpec | undefined, selection: ResourceSpecSelection) {
  return resourceSpecFields.every(field => !selection[field.key] || (selection[field.key] === 'unknown' ? !values(spec, field.key).length : values(spec, field.key).includes(selection[field.key])))
}
