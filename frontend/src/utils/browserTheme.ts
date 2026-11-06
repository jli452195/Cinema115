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

/** 先让快照清理后的页面获得绘制机会，再请求原生系统栏换色。 */
export function waitForThemePaint(): Promise<void> {
  if (document.visibilityState === 'hidden') return Promise.resolve()
  return new Promise(resolve => {
    let frame: number | undefined
    const finish = () => {
      if (frame !== undefined) cancelAnimationFrame(frame)
      document.removeEventListener('visibilitychange', onVisibilityChange)
      resolve()
    }
    const onVisibilityChange = () => {
      // 后台页面会暂停动画帧，不能因此一直等不到主题同步。
      if (document.visibilityState === 'hidden') finish()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(finish)
    })
  })
}

