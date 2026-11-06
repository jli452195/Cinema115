interface Point { x: number; y: number }

/** 用浏览器捕获的固定锚点统一视口与主题快照坐标，兼容移动地址栏的偏移。 */
export function prepareThemeTransitionGeometry(origin: Point, softMask: boolean) {
  const root = document.documentElement
  const anchor = document.createElement('div')
  anchor.setAttribute('aria-hidden', 'true')
  anchor.style.cssText = `position:fixed;left:${origin.x}px;top:${origin.y}px;width:1px;height:1px;padding:0;border:0;pointer-events:none;view-transition-name:theme-reveal-anchor;`
  root.appendChild(anchor)

  const group = (name: string) => getComputedStyle(root, `::view-transition-group(${name})`)
  const matrix = (style: CSSStyleDeclaration) => new DOMMatrix(style.transform === 'none' ? undefined : style.transform)
  let capturedOrigin: DOMPoint | undefined

  return {
    align() {
      const anchorStyle = group('theme-reveal-anchor')
      // 未生成锚点快照时保留原始视口坐标，不能使用无效伪元素的默认样式。
      if (parseFloat(anchorStyle.width) !== 1 || parseFloat(anchorStyle.height) !== 1) return
      capturedOrigin = matrix(anchorStyle).transformPoint({ x: 0, y: 0 })
      const rootStyle = group('root')
      const point = this.originForGroup('root')
      if (!point) return
      const width = parseFloat(rootStyle.width) || window.innerWidth
      const height = parseFloat(rootStyle.height) || window.innerHeight
      const farthestCorner = Math.hypot(Math.max(point.x, width - point.x), Math.max(point.y, height - point.y))
      root.style.setProperty('--theme-reveal-origin', `${point.x}px ${point.y}px`)
      root.style.setProperty('--theme-reveal-radius', `${softMask ? (farthestCorner + 1) / 0.6 : farthestCorner}px`)
    },
    originForGroup(name: string): Point | undefined {
      if (!capturedOrigin) return
      const point = matrix(group(name)).inverse().transformPoint(capturedOrigin)
      if (Number.isFinite(point.x) && Number.isFinite(point.y)) return { x: point.x, y: point.y }
    },
    cleanup() {
      anchor.remove()
    },
  }
}
