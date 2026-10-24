import type { NamingData, NamingKind, NamingTemplate } from '../types/naming'

export function cloneNamingTemplate(template: NamingTemplate): NamingTemplate {
  const copy: NamingTemplate = JSON.parse(JSON.stringify(template))
  copy.directories ||= []
  return copy
}

// Ignore omitted optional JSON properties when comparing the same saved rule.
export function namingSignature(template: NamingTemplate) {
  const layer = (blocks: NamingTemplate['filename']) => blocks.map(block => [block.field, block.separator || '', block.prefix || '', block.suffix || '', block.pad || 0])
  return JSON.stringify([template.kind, (template.directories || []).map(layer), layer(template.filename)])
}

export function namingSample(kind: NamingKind, sample: string): NamingData {
  const values: Record<string, string> = {
    title: kind === 'movie' ? '示例电影' : '示例剧集', originalTitle: 'Original Title', englishTitle: 'Example Title',
    year: '2026', tmdbId: '123456',
  }
  if (sample !== 'minimal') Object.assign(values, {
    resolution: '2160p', source: 'WEB-DL', platform: 'NF', videoCodec: 'HEVC', bitDepth: '10bit',
    dynamicRange: 'DV HDR10', frameRate: '24fps', audioCodec: 'DDP', channels: '5.1', audioEffect: 'Atmos',
    languages: '简繁', subtitleMode: '外挂', releaseGroup: '字幕组', edition: '特别版', custom: '收藏版',
  })
  if (kind === 'tv') values.episodeTitle = '示例集名'
  return {
    values, originalName: kind === 'movie' ? 'Example.Movie.2026.mkv' : 'Example.Series.S01E13.mkv',
    ...(kind === 'tv' ? { season: 1, episode: 13, ...(sample === 'range' ? { endEpisode: 14 } : {}) } : {}),
  }
}
