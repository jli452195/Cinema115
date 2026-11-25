import { subtitleLanguages } from './subtitleLibrary'

export const mediaSpecificationLabels: Record<string, string> = {
  resolution: '分辨率',
  source: '资源来源',
  sourceMedium: '来源介质',
  sourceModifier: '来源修饰',
  videoCodec: '视频编码',
  encoder: '编码器',
  dynamicRange: '动态范围',
  dvProfile: 'DV Profile',
  dvLayer: 'DV 层',
  hdrBaseLayer: 'HDR 基础层',
  dvMetadata: 'DV 元数据',
  bitDepth: '位深',
  frameRate: '帧率',
  scanType: '扫描方式',
  frameRateMode: '帧率模式',
  audioCodec: '音频编码',
  channels: '声道',
  audioEffect: '音效',
  languages: '字幕语言',
  subtitleMode: '字幕形式',
  platform: '平台',
  releaseGroup: '发布组',
}

const subtitleModes = new Map([['softsub', '内封'], ['hardsub', '内嵌'], ['external', '外挂']])

export function mediaSpecificationValue(field: string, value: string): string {
  if (field === 'languages') {
    return value.split('+').map(code => subtitleLanguages.find(option => option.value === code)?.label || code).join(' / ')
  }
  if (field === 'subtitleMode') return subtitleModes.get(value) || value
  return value
}
