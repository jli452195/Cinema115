import { onBeforeUnmount } from 'vue'

type Motion = { animations: Animation[]; progress: () => number; complete: () => void }

const clamp = (value: number) => Math.min(1, Math.max(0, value))
const smoothstep = (value: number) => { const u = clamp(value); return u * u * (3 - 2 * u) }
const smooth = (from: number, to: number, value: number) => smoothstep((value - from) / (to - from))
const lerp = (from: number, to: number, progress: number) => from + (to - from) * progress

// 参数对应 Telegram Android 13.2.2 的 SimpleActionBarMenuOverlay：
// https://github.com/DrKLO/Telegram/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/ui/ActionBar/SimpleActionBarMenuOverlay.java
function spring(time: number, damping: number, period: number) {
  if (time <= 0) return 0
  const frequency = 2 * Math.PI / period, root = Math.sqrt(1 - damping * damping)
  return 1 - Math.exp(-damping * frequency * time) * (Math.cos(frequency * root * time) + damping / root * Math.sin(frequency * root * time))
}

function sample(time: number, opening: boolean, from: number, pill: boolean) {
  const width = opening ? time >= .7 ? 1 : from + (1 - from) * spring(time, .78, .30) : from * (1 - smoothstep(time / .17))
  const height = opening ? time >= .7 ? 1 : from + (1 - from) * spring(time - .02, .80, .34) : from * (1 - smoothstep(time / .20))
  const settle = time - .20
  const wobble = !opening && pill && settle > 0 && settle < .45 ? .07 * from * Math.exp(-settle / .11) * Math.sin(2 * Math.PI * settle / .17) : 0
  return { width, height, wobble, progress: clamp(Math.max(width, height)) }
}

export function useAnchoredMenuMotion(options: {
  origin: () => HTMLElement | undefined
  compact: () => boolean
  immediate: () => boolean
}) {
  const running = new Map<HTMLElement, Motion>()
  const interrupted = new WeakMap<HTMLElement, number>()
  let handoff: { from: number; width: number; height: number } | undefined
  let ownedOrigin: HTMLElement | undefined

  function restoreOrigin() {
    ownedOrigin?.removeAttribute('data-menu-origin-owned')
    ownedOrigin = undefined
  }

  function ownOrigin(origin: HTMLElement) {
    if (ownedOrigin !== origin) restoreOrigin()
    ownedOrigin = origin
    origin.setAttribute('data-menu-origin-owned', '')
  }

  function copyOrigin(origin: HTMLElement, target: HTMLElement) {
    const copy = origin.cloneNode(true) as HTMLElement
    const sourceNodes = [...origin.querySelectorAll('*')], copiedNodes = [...copy.querySelectorAll('*')]
    sourceNodes.forEach((node, index) => { if (getComputedStyle(node).display === 'none') copiedNodes[index]?.remove() })
    copy.removeAttribute('data-menu-origin-owned')
    copy.classList.add('menu-origin-copy')
    // 复制的只是原胶囊图像；不复制可操作入口、标识和焦点。
    for (const node of [copy, ...copy.querySelectorAll<HTMLElement>('*')]) {
      node.removeAttribute('id')
      node.removeAttribute('aria-controls')
      node.removeAttribute('aria-expanded')
      node.removeAttribute('data-menu-origin-owned')
      if (node.classList.contains('account-trigger')) node.classList.remove('selected')
      if (node.matches('a, button, input, [tabindex]')) node.tabIndex = -1
    }
    copy.style.width = '100%'
    copy.style.height = '100%'
    target.replaceChildren(copy)
  }

  function cancel(element: Element) {
    const menu = element as HTMLElement, motion = running.get(menu)
    if (!motion) return
    interrupted.set(menu, motion.progress())
    running.delete(menu)
    delete menu.dataset.menuMotion
    motion.animations.forEach(animation => animation.cancel())
  }

  function finish(element?: Element | null) {
    if (!options.compact()) restoreOrigin()
    if (element) running.get(element as HTMLElement)?.complete()
    else [...running.values()].forEach(motion => motion.complete())
  }

  function release(element: Element) {
    const menu = element as HTMLElement, motion = running.get(menu)
    if (!motion) return
    if (menu.isConnected) {
      const current = { from: motion.progress(), width: menu.offsetWidth, height: menu.offsetHeight }
      handoff = current
      queueMicrotask(() => { if (handoff === current) handoff = undefined })
    }
    cancel(menu)
    interrupted.delete(menu)
  }

  function run(element: Element, opening: boolean, done: () => void) {
    const menu = element as HTMLElement
    cancel(menu)
    const compact = options.compact(), origin = options.origin()
    const surface = menu.querySelector<HTMLElement>('[data-menu-surface]')
    const material = menu.querySelector<HTMLElement>('[data-menu-material]')
    const clip = menu.querySelector<HTMLElement>('[data-menu-clip]')
    const content = menu.querySelector<HTMLElement>('[data-menu-content]')
    const image = menu.querySelector<HTMLElement>('[data-menu-origin]')
    const scrim = menu.querySelector<HTMLElement>('[data-menu-scrim]')
    const layers = compact && origin && surface && material && clip && content && image && scrim ? { origin, surface, material, clip, content, image, scrim } : undefined
    if (layers) ownOrigin(layers.origin)
    else restoreOrigin()
    if (options.immediate() || matchMedia('(prefers-reduced-motion: reduce)').matches) {
      if (!opening) restoreOrigin()
      interrupted.delete(menu)
      done()
      return
    }

    let from = interrupted.get(menu) ?? (opening ? 0 : 1)
    interrupted.delete(menu)
    const animations: Animation[] = []
    const pill = !!layers?.origin.classList.contains('glass-pill-surface')
    const duration = layers ? opening ? 700 : pill ? 650 : 200 : 140
    const animate = (target: HTMLElement, frames: Keyframe[], easing = 'linear') => {
      const animation = target.animate(frames, { duration, easing, fill: 'both' })
      animations.push(animation)
      return animation
    }
    let clock: Animation

    if (layers) {
      const width = menu.offsetWidth, height = menu.offsetHeight
      if (opening && handoff && Math.abs(handoff.width - width) < 1 && Math.abs(handoff.height - height) < 1) from = handoff.from
      handoff = undefined
      const source = layers.origin.getBoundingClientRect(), box = menu.getBoundingClientRect()
      const start = { left: source.left - box.left, top: source.top - box.top, width: source.width, height: source.height }
      // 参考原生 reserveGlass 预留整段轨迹；滤镜矩形保持固定，只改变裁剪与细边框。
      const reserve = 4 + .04 * Math.max(width, height, start.width, start.height)
      menu.style.setProperty('--menu-reserve', `${reserve}px`)
      copyOrigin(layers.origin, layers.image)
      Object.assign(layers.image.style, { left: `${start.left}px`, top: `${start.top}px`, width: `${start.width}px`, height: `${start.height}px` })

      const surfaceFrames: Keyframe[] = [], clipFrames: Keyframe[] = [], contentFrames: Keyframe[] = [], imageFrames: Keyframe[] = [], scrimFrames: Keyframe[] = []
      const materialFrames: Keyframe[] = []
      // 每秒 120 个预采样点交给浏览器执行，动画期间没有 JS 逐帧布局测量。
      const steps = Math.ceil(duration / 1000 * 120)
      for (let index = 0; index <= steps; index++) {
        const offset = index / steps, state = sample(offset * duration / 1000, opening, from, pill)
        let left = lerp(start.left, 0, state.width), top = lerp(start.top, 0, state.height)
        let w = lerp(start.width, width, state.width), h = lerp(start.height, height, state.height)
        const bulgeX = w * state.wobble * .25, bulgeY = h * state.wobble * .5
        left -= bulgeX; top += bulgeY; w += bulgeX * 2; h -= bulgeY * 2
        const radius = Math.min(w / 2, h / 2, lerp(start.height / 2, 22, clamp(state.height)))
        const scale = Math.max(.3, w / Math.max(1, width)), m = state.progress
        surfaceFrames.push({ offset, left: `${left}px`, top: `${top}px`, width: `${w}px`, height: `${h}px`, borderRadius: `${radius}px`, opacity: pill ? 1 : clamp(m / .25) })
        clipFrames.push({ offset, clipPath: `inset(${top}px ${width - left - w}px ${height - top - h}px ${left}px round ${radius}px)` })
        materialFrames.push({ offset, clipPath: `inset(${top + reserve}px ${width - left - w + reserve}px ${height - top - h + reserve}px ${left + reserve}px round ${radius}px)`, opacity: pill ? 1 : clamp(m / .25) })
        contentFrames.push({ offset, transform: `translate(${left + w - width}px, ${top}px) scale(${scale})`, opacity: opening ? smooth(.25, .75, m) : smooth(.5, .95, m) })
        const iconScale = 1 - .15 * clamp(m / .3)
        imageFrames.push({ offset, opacity: 1 - clamp(m / (opening ? .3 : .7)), transform: `scale(${iconScale * (1 + state.wobble * .5)}, ${iconScale * (1 - state.wobble)})` })
        scrimFrames.push({ offset, opacity: m })
      }
      clock = animate(layers.surface, surfaceFrames)
      animate(layers.material, materialFrames)
      animate(layers.clip, clipFrames)
      animate(layers.content, contentFrames)
      animate(layers.image, imageFrames)
      animate(layers.scrim, scrimFrames)
    } else {
      clock = animate(menu, opening
        ? [{ transform: 'translateY(-4px)', opacity: 0 }, { transform: 'translateY(0px)', opacity: 1 }]
        : [{ transform: 'translateY(0px)', opacity: 1 }, { transform: 'translateY(-4px)', opacity: 0 }], 'ease')
    }

    const motion: Motion = {
      animations,
      progress: () => {
        const state = sample(Number(clock.currentTime || 0) / 1000, opening, from, pill)
        return Math.max(state.width, state.height)
      },
      complete() {
        if (running.get(menu) !== motion) return
        running.delete(menu)
        delete menu.dataset.menuMotion
        animations.forEach(animation => animation.cancel())
        if (!opening) restoreOrigin()
        done()
      },
    }
    running.set(menu, motion)
    menu.dataset.menuMotion = opening ? 'entering' : 'leaving'
    void Promise.allSettled(animations.map(animation => animation.finished)).then(() => motion.complete())
  }

  onBeforeUnmount(() => {
    for (const menu of running.keys()) cancel(menu)
    restoreOrigin()
  })

  return {
    enter: (element: Element, done: () => void) => run(element, true, done),
    leave: (element: Element, done: () => void) => run(element, false, done),
    cancel,
    release,
    finish,
  }
}
