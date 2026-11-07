type Color = [number, number, number]

function parseColor(value: string): Color | undefined {
  if (/^#[\da-f]{6}$/i.test(value)) {
    return [1, 3, 5].map(offset => parseInt(value.slice(offset, offset + 2), 16)) as Color
  }
  const channels = value.match(/[\d.]+/g)?.map(Number)
  return channels && channels.length >= 3 ? channels.slice(0, 3) as Color : undefined
}

/** 系统栏与导航背景复用同一色值；浏览器不开放透明区域时使用该纯色。 */
export function browserThemeColor(): Color {
  const style = getComputedStyle(document.documentElement)
  return parseColor(style.getPropertyValue('--app-chrome-color').trim())
    ?? parseColor(style.backgroundColor)
    ?? [248, 250, 252]
}

export function syncBrowserTheme(color = browserThemeColor()) {
  const value = `rgb(${color.map(Math.round).join(' ')})`
  // 手动主题与系统跟随共用同一入口，值相同则不触发系统栏重复更新。
  for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    if (meta.content !== value) meta.content = value
  }
}

