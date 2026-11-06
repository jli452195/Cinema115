/** 将持续运动的点阵与表单分层，避免旧主题快照冻结背景。 */
export function prepareThemePatternTransition(origin: { x: number; y: number }) {
  // 不支持插值遮罩的浏览器继续使用整体圆形快照降级。
  if (document.documentElement.dataset.themeRevealMask !== 'soft') return
  const source = document.querySelector<HTMLElement>('[data-theme-pattern] .pattern-bg-canvas')
  const pattern = source?.parentElement
  const foreground = document.querySelector<HTMLElement>('[data-theme-foreground]')
  if (!source || !pattern || !foreground) return

  const root = document.documentElement
  const properties = new Set<string>()
  let frame: number | undefined
  const set = (name: string, value: string) => {
    properties.add(name)
    root.style.setProperty(name, value)
  }
  const oldStyle = getComputedStyle(source)
  set('--theme-pattern-old-image', oldStyle.backgroundImage)
  set('--theme-pattern-size', oldStyle.backgroundSize)
  set('--theme-pattern-mask', oldStyle.maskImage)
  for (const [name, element] of [['pattern', pattern], ['foreground', foreground]] as const) {
    const rect = element.getBoundingClientRect()
    set(`--theme-${name}-origin`, `${origin.x - rect.left}px ${origin.y - rect.top}px`)
  }
  root.classList.add('theme-reveal-pattern')

  return {
    alignOrigins(originForGroup: (name: string) => { x: number; y: number } | undefined) {
      for (const name of ['pattern', 'foreground']) {
        const point = originForGroup(`auth-${name}`)
        if (point) set(`--theme-${name}-origin`, `${point.x}px ${point.y}px`)
      }
    },
    update() {
      set('--theme-pattern-new-image', getComputedStyle(source).backgroundImage)
    },
    play() {
      // 逐帧读取原动画位置，快照结束前始终同步，避免额外动画延长转场或提前停住。
      const sync = () => {
        set('--theme-pattern-position', getComputedStyle(source).backgroundPosition)
        frame = requestAnimationFrame(sync)
      }
      sync()
    },
    cleanup() {
      if (frame !== undefined) cancelAnimationFrame(frame)
      properties.forEach(name => root.style.removeProperty(name))
      root.classList.remove('theme-reveal-pattern')
    },
  }
}
