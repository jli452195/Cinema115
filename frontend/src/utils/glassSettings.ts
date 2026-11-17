export interface GlassSettings {
  blur: number
  saturate: number
  opacity: number
  rim: number
  refraction: boolean
}

export const glassPresets = [
  { value: 'liquid', label: 'TG 液态玻璃', settings: { blur: 22, saturate: 175, opacity: 42, rim: 80, refraction: false } },
  { value: 'frosted', label: '传统毛玻璃', settings: { blur: 24, saturate: 110, opacity: 80, rim: 35, refraction: false } },
  { value: 'clear', label: '清透玻璃', settings: { blur: 16, saturate: 190, opacity: 30, rim: 90, refraction: false } },
] as const
export type GlassPreset = typeof glassPresets[number]['value']
export const defaultGlassSettings: GlassSettings = { ...glassPresets[0].settings }
export const glassStorageKey = 'cinema115-glass-settings'

export const glassSliders = [
  { key: 'blur', label: '背景模糊', min: 0, max: 24, unit: 'px' },
  { key: 'saturate', label: '色彩饱和度', min: 100, max: 220, unit: '%' },
  { key: 'opacity', label: '底色浓度', min: 25, max: 100, unit: '%' },
  { key: 'rim', label: '边缘高光', min: 0, max: 100, unit: '%' },
] as const
export type GlassSliderKey = typeof glassSliders[number]['key']

// 只接受有限数值和明确的布尔值，损坏的本机配置不能写入任意 CSS。
export function normalizeGlassSettings(raw: unknown): GlassSettings {
  const source = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw as Record<string, unknown> : {}
  const value = { ...defaultGlassSettings }
  for (const slider of glassSliders) {
    const number = source[slider.key]
    value[slider.key] = typeof number === 'number' && Number.isFinite(number)
      ? Math.round(Math.min(slider.max, Math.max(slider.min, number)))
      : defaultGlassSettings[slider.key]
  }
  value.refraction = source.refraction === true
  return value
}

export function identifyGlassPreset(settings: GlassSettings): GlassPreset | 'custom' {
  return glassPresets.find(preset => glassSliders.every(slider => preset.settings[slider.key] === settings[slider.key]))?.value || 'custom'
}

export function glassCssVariables(settings: GlassSettings): Record<string, string> {
  return {
    '--user-glass-blur': `${settings.blur}px`,
    '--user-glass-saturate': (settings.saturate / 100).toFixed(2),
    '--user-glass-opacity': (settings.opacity / 100).toFixed(2),
    '--user-glass-rim': (settings.rim / 100).toFixed(2),
  }
}

export function glassCssSnippet(settings: GlassSettings): string {
  return `/* Cinema115 玻璃外观参数；在项目公共玻璃样式中使用。 */\n:root {\n${Object.entries(glassCssVariables(settings)).map(([key, value]) => `  ${key}: ${value};`).join('\n')}\n}`
}
