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
    title: kind === 'movie' ? '示例电影' : '示例剧集',
    year: '2026', tmdbId: '123456',
  }
  if (sample !== 'minimal') Object.assign(values, {
    originalTitle: 'Original Title', englishTitle: 'Example Title',
    resolution: '2160p', source: 'BluRay REMUX', sourceMedium: 'BluRay', sourceModifier: 'REMUX', videoCodec: 'HEVC', bitDepth: '10bit',
    dynamicRange: 'DV HDR10', dvProfile: 'P7', dvLayer: 'FEL', hdrBaseLayer: 'HDR10', dvMetadata: 'RPU', frameRate: '23.976fps', scanType: 'Progressive', frameRateMode: 'CFR', container: 'MKV', audioCodec: 'TrueHD', channels: '7.1', audioEffect: 'Atmos',
    languages: 'zh-CN+zh-TW', subtitleMode: 'softsub', releaseGroup: '发布组', edition: '特别版',
  })
  if (kind === 'tv') values.episodeTitle = '示例集名'
  return {
    values, originalName: kind === 'movie' ? 'Example.Movie.2026.mkv' : 'Example.Series.S01E13.mkv',
    ...(kind === 'tv' ? { season: 1, episode: 13, ...(sample === 'range' ? { endEpisode: 14 } : {}) } : {}),
  }
}
