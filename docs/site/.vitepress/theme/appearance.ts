import { nextTick, type Ref } from 'vue'

interface ThemeTransition {
  ready: Promise<void>
  finished: Promise<void>
  skipTransition(): void
}

// 沿用产品的 400ms 圆形软边转场；文档只适配 VitePress 的主题状态。
export function createAppearanceToggle(isDark: Ref<boolean>) {
  let active: ThemeTransition | undefined
  let request = 0
  let desired = isDark.value
  let pending = false

  return async (event?: MouseEvent) => {
    const id = ++request
    desired = !(pending ? desired : isDark.value)
    pending = true
    const target = desired
    const bounds = event?.currentTarget instanceof HTMLElement ? event.currentTarget.getBoundingClientRect() : undefined
    const pointer = event && (event.detail > 0 || ('pointerType' in event && Boolean(event.pointerType)))
    const x = Math.max(0, Math.min(pointer ? event.clientX : bounds ? bounds.left + bounds.width / 2 : innerWidth / 2, innerWidth))
    const y = Math.max(0, Math.min(pointer ? event.clientY : bounds ? bounds.top + bounds.height / 2 : innerHeight / 2, innerHeight))
    const apply = async () => {
      if (id === request) isDark.value = target
      await nextTick()
    }
    const root = document.documentElement
    document.dispatchEvent(new CustomEvent('wiki-theme-transition', {
      detail: { phase: 'start', id },
    }))
    let anchor: HTMLElement | undefined
    let transition: ThemeTransition | undefined
    try {
      const previous = active
      if (previous) {
        previous.skipTransition()
        await previous.finished.catch(() => {})
      }
      if (id !== request) return

      // 暂停轮播后解码配套图片；两张图与页面一起捕获进根快照，共享同一个蒙版。
      // 只等待当前页与已打开的大图，避免其他轮播项的慢图阻塞主题切换。
      const images = [...document.querySelectorAll<HTMLImageElement>(
        '.showcase-slide[aria-hidden="false"] img[data-theme-paired], .showcase-preview[open] img[data-theme-paired]',
      )]
      let timeout: ReturnType<typeof setTimeout> | undefined
      const decoded = await Promise.race([
        Promise.all(images.map(image => image.decode().then(() => true, () => false))).then(results => results.every(Boolean)),
        new Promise<false>(resolve => { timeout = setTimeout(() => resolve(false), 2000) }),
      ])
      clearTimeout(timeout)
      if (id !== request) return
      await nextTick()
      if (id !== request) return

      const doc = document as Document & { startViewTransition?: (update: () => Promise<void>) => ThemeTransition }
      if (!decoded || isDark.value === target || !doc.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches) {
        await apply()
        return
      }

      const soft = typeof CSS.registerProperty === 'function'
        && CSS.supports('mask-image', 'radial-gradient(circle 1px at 0px 0px, #000 60%, transparent 100%)')
      const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y))
      root.style.setProperty('--theme-reveal-origin', `${x}px ${y}px`)
      root.style.setProperty('--theme-reveal-radius', `${soft ? (radius + 1) / 0.6 : radius}px`)
      root.dataset.themeReveal = target ? 'expand' : 'contract'
      root.dataset.themeRevealMask = soft ? 'soft' : 'clip'
      root.classList.add('theme-reveal')

      // 固定锚点将点击坐标转换到实际快照平面，兼容移动浏览器地址栏偏移。
      anchor = document.createElement('div')
      anchor.setAttribute('aria-hidden', 'true')
      anchor.style.cssText = `position:fixed;left:${x}px;top:${y}px;width:1px;height:1px;pointer-events:none;view-transition-name:theme-reveal-anchor;`
      root.appendChild(anchor)
      transition = doc.startViewTransition(apply)
      active = transition
      void transition.ready.then(() => {
        const anchorStyle = getComputedStyle(root, '::view-transition-group(theme-reveal-anchor)')
        if (parseFloat(anchorStyle.width) !== 1 || parseFloat(anchorStyle.height) !== 1) return
        const snapshot = getComputedStyle(root, '::view-transition-group(root)')
        const matrix = (style: CSSStyleDeclaration) => new DOMMatrix(style.transform === 'none' ? undefined : style.transform)
        const point = matrix(snapshot).inverse().transformPoint(matrix(anchorStyle).transformPoint({ x: 0, y: 0 }))
        if (!Number.isFinite(point.x) || !Number.isFinite(point.y)) return
        const width = parseFloat(snapshot.width) || innerWidth
        const height = parseFloat(snapshot.height) || innerHeight
        const distance = Math.hypot(Math.max(point.x, width - point.x), Math.max(point.y, height - point.y))
        root.style.setProperty('--theme-reveal-origin', `${point.x}px ${point.y}px`)
        root.style.setProperty('--theme-reveal-radius', `${soft ? (distance + 1) / 0.6 : distance}px`)
      }).catch(() => {})
      await transition.finished
    } catch {
      await apply()
    } finally {
      anchor?.remove()
      if ((transition && active === transition) || (id === request && !active)) {
        active = undefined
        root.classList.remove('theme-reveal')
        delete root.dataset.themeReveal
        delete root.dataset.themeRevealMask
        root.style.removeProperty('--theme-reveal-origin')
        root.style.removeProperty('--theme-reveal-radius')
      }
      if (id === request) pending = false
      document.dispatchEvent(new CustomEvent('wiki-theme-transition', {
        detail: { phase: 'end', id },
      }))
    }
  }
}
