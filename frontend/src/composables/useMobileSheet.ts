import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

export type DismissReason = 'button' | 'escape' | 'backdrop' | 'swipe'
export type DismissDecision = 'handled' | 'blocked' | 'dismiss'

interface MobileSheetOptions {
  enabled: () => boolean
  animateBackdrop?: boolean
  closeDisabled: () => boolean
  allowSwipeClose?: () => boolean
  beforeClose?: (reason: DismissReason) => DismissDecision | Promise<DismissDecision>
  commitClose?: () => Promise<void>
  onClosing?: () => void
  onClose: () => void
  onViewportChange?: (height: number) => void
}

interface DragSample { y: number; time: number }
interface SheetDrag {
  pointerId: number
  target: Element
  x: number
  y: number
  baseOffset: number
  offset: number
  started: boolean
  expanded: boolean
  samples: DragSample[]
}

const interactive = 'button, a, input, select, textarea, [contenteditable]:not([contenteditable="false"]), [role="button"], [role="link"], [role="combobox"], [role="tab"], [role="checkbox"], [role="radio"], [role="switch"], [role="slider"], [data-sheet-no-drag]'
let replacementBackdrop: { opacity: number } | undefined

export function useMobileSheet(dialog: Ref<HTMLDialogElement | undefined>, options: MobileSheetOptions) {
  const expanded = ref(false)
  let opened = false, closing = false, notified = false, disposed = false, listening = false, pendingClose = false, committingClose = false
  let element: HTMLDialogElement | undefined
  let mobile: MediaQueryList | undefined, reduced: MediaQueryList | undefined
  let viewport: VisualViewport | null = null
  let drag: SheetDrag | undefined, frame = 0
  let animation: Animation | undefined
  let backdropAnimation: Animation | undefined
  let closeReadyAt = 0
  let suppressedClick: { pointerId: number; x: number; y: number; until: number } | undefined

  const active = () => opened && !!element?.open && options.enabled() && !!mobile?.matches
  const reducedMotion = () => !!reduced?.matches
  const closeBlocked = () => committingClose || options.closeDisabled() || !!element?.hasAttribute('data-dialog-covered')

  function cancelAnimation() {
    const current = animation
    animation = undefined
    current?.cancel()
  }
  function cancelBackdropAnimation() {
    const current = backdropAnimation
    backdropAnimation = undefined
    if (current) { current.onfinish = null; current.cancel() }
  }
  function hasOtherModal() {
    return [...document.querySelectorAll('dialog:modal')].some(dialog => dialog !== element)
  }
  function backdropOpacity() {
    return Number.parseFloat(getComputedStyle(element!, '::backdrop').opacity) || 0
  }
  function moveBackdrop(from: number, to: 0 | 1, timing: KeyframeAnimationOptions) {
    if (!element || typeof KeyframeEffect === 'undefined' || !('pseudoElement' in KeyframeEffect.prototype)) return
    // 先设置最终样式，动画结束后取消动画也不会把遮罩跳回全黑。
    element.toggleAttribute('data-sheet-backdrop-transparent', to === 0)
    cancelBackdropAnimation()
    if (from === to) return
    try {
      const current = element.animate([{ opacity: from }, { opacity: to }], { ...timing, pseudoElement: '::backdrop' })
      if ((current.effect as KeyframeEffect | null)?.pseudoElement !== '::backdrop') {
        current.cancel()
        element.removeAttribute('data-sheet-backdrop-transparent')
        return
      }
      backdropAnimation = current
      current.onfinish = () => { if (backdropAnimation === current) cancelBackdropAnimation() }
    } catch {
      // 不支持伪元素动画时沿用静态遮罩，面板仍正常关闭。
      element.removeAttribute('data-sheet-backdrop-transparent')
    }
  }
  function cancelFrame() { if (frame) cancelAnimationFrame(frame); frame = 0 }
  function clearTransform() {
    if (!element) return
    element.style.removeProperty('transform')
    element.style.removeProperty('will-change')
  }
  function setExpanded(value: boolean) {
    expanded.value = value
    if (element) element.dataset.sheetExpanded = String(value)
  }
  function releaseDrag() {
    const current = drag
    drag = undefined
    cancelFrame()
    if (current?.target.hasPointerCapture(current.pointerId)) current.target.releasePointerCapture(current.pointerId)
    return current
  }
  function visualOffset() {
    if (!element) return 0
    const transform = getComputedStyle(element).transform
    return transform === 'none' ? 0 : new DOMMatrixReadOnly(transform).m42
  }
  async function move(from: number, to: number, duration: number, backdrop?: { from: number; to: 0 | 1 }) {
    cancelAnimation()
    const surface = element
    if (!surface || disposed) return false
    surface.style.transform = `translateY(${to}px)`
    if (reducedMotion() || typeof surface.animate !== 'function') return true
    surface.style.willChange = 'transform'
    const timing = { duration, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'both' as const }
    const current = surface.animate([
      { transform: `translateY(${from}px)` }, { transform: `translateY(${to}px)` },
    ], timing)
    if (backdrop) moveBackdrop(backdrop.from, backdrop.to, timing)
    animation = current
    try { await current.finished } catch { return false }
    if (animation !== current || disposed) return false
    animation = undefined
    current.cancel()
    return true
  }
  function snap(value: boolean) {
    const surface = element
    if (!surface || disposed) return
    // 只在吸附时改一次高度，过渡阶段只移动表面，不逐帧触发文件列表布局。
    const previousTop = surface.getBoundingClientRect().top
    cancelAnimation(); cancelFrame(); clearTransform()
    setExpanded(value)
    if (!active()) return
    const offset = previousTop - surface.getBoundingClientRect().top
    const backdrop = surface.hasAttribute('data-sheet-backdrop-transparent') ? { from: backdropOpacity(), to: 1 as const } : undefined
    void move(offset, 0, 200, backdrop).then(finished => { if (finished && !closing) clearTransform() })
  }
  function notifyClose() {
    if (notified || disposed) return
    notified = true; opened = false; closing = false
    removeListeners()
    options.onClose()
  }
  function performClose() {
    if (disposed || notified || closing || closeBlocked()) return
    releaseDrag()
    if (options.commitClose) {
      // 由路由卸载的弹层在导航提交前保持可见，避免屏外模态拦住底层页面。
      committingClose = true
      if (visualOffset()) snap(expanded.value)
      void options.commitClose().finally(() => { committingClose = false })
      return
    }
    closing = true
    options.onClosing?.()
    if (!active() || reducedMotion()) {
      cancelAnimation(); clearTransform(); notifyClose(); return
    }
    const from = visualOffset(), destination = element!.getBoundingClientRect().height + 32
    const backdrop = options.animateBackdrop ? { from: backdropOpacity(), to: hasOtherModal() ? 1 as const : 0 as const } : undefined
    void move(from, destination, 180, backdrop).then(finished => {
      if (!finished || !closing) return
      if (closeBlocked()) { closing = false; snap(expanded.value); return }
      notifyClose()
    })
  }
  // 返回页内视图必须先于离场；只有明确退出宿主才改变手势和动画状态。
  async function requestClose(reason: DismissReason = 'button'): Promise<DismissDecision> {
    if (disposed || notified || closing || pendingClose || performance.now() < closeReadyAt) return 'blocked'
    const restoreSwipe = () => { if (reason === 'swipe' && !disposed && !closing) snap(expanded.value) }
    if (closeBlocked()) { restoreSwipe(); return 'blocked' }
    if (reason === 'swipe' && options.allowSwipeClose?.() === false) { restoreSwipe(); return 'handled' }
    pendingClose = true
    try {
      const decision = await options.beforeClose?.(reason) ?? 'dismiss'
      if (disposed || notified || closing) return 'blocked'
      if (decision !== 'dismiss' || closeBlocked()) {
        if (decision === 'handled') closeReadyAt = performance.now() + 300
        restoreSwipe()
        return decision === 'handled' ? 'handled' : 'blocked'
      }
      performClose()
      return 'dismiss'
    } catch {
      restoreSwipe()
      return 'blocked'
    } finally { pendingClose = false }
  }
  // 全局关闭只绕过页内返回守卫，仍由宿主的 closeDisabled 和当前动画状态保护。
  function closeDirect(): DismissDecision {
    if (disposed || notified || closing || pendingClose || closeBlocked()) return 'blocked'
    performClose()
    return 'dismiss'
  }
  function toggleExpanded() {
    if (!active() || closing || pendingClose || notified || element?.hasAttribute('data-dialog-covered')) return
    releaseDrag()
    snap(!expanded.value)
  }

  function updateViewport() {
    if (!element) return
    const height = viewport?.height ?? window.innerHeight
    if (options.enabled()) options.onViewportChange?.(height)
    if (!active()) {
      element.style.removeProperty('--sheet-viewport-height')
      element.style.removeProperty('--sheet-viewport-top')
      element.style.removeProperty('--sheet-viewport-bottom')
      return
    }
    const top = viewport?.offsetTop ?? 0
    const bottom = Math.max(0, window.innerHeight - height - top)
    element.style.setProperty('--sheet-viewport-top', `${top}px`)
    element.style.setProperty('--sheet-viewport-height', `${Math.max(1, height)}px`)
    element.style.setProperty('--sheet-viewport-bottom', `${bottom}px`)
  }
  function viewportChanged() {
    releaseDrag()
    // 键盘、旋转和浏览器工具栏变化不作为关闭手势，也不打断已经确认的离场。
    if (!closing) { cancelAnimation(); clearTransform() }
    updateViewport()
  }
  function presentationChanged() {
    if (!opened) return
    if (options.enabled()) addListeners()
    if (closing && active()) { updateViewport(); return }
    releaseDrag(); cancelAnimation(); clearTransform()
    if (!active()) {
      cancelBackdropAnimation()
      element?.removeAttribute('data-sheet-backdrop-transparent')
      setExpanded(false)
      if (closing) {
        closing = false
        if (!closeBlocked()) notifyClose()
      }
    }
    updateViewport()
    if (!options.enabled()) removeListeners()
  }
  function motionChanged() {
    if (!reducedMotion()) return
    releaseDrag(); cancelAnimation(); cancelBackdropAnimation(); clearTransform()
    if (closing) {
      closing = false
      if (!closeBlocked()) { notifyClose(); return }
    }
    element?.removeAttribute('data-sheet-backdrop-transparent')
  }
  function captureClick(event: MouseEvent) {
    const click = suppressedClick
    suppressedClick = undefined
    if (!click || performance.now() > click.until || Math.abs(event.clientX - click.x) > 8 || Math.abs(event.clientY - click.y) > 8) return
    if (event instanceof PointerEvent && event.pointerId !== click.pointerId) return
    event.preventDefault(); event.stopImmediatePropagation()
  }
  function removeListeners() {
    mobile?.removeEventListener('change', presentationChanged)
    reduced?.removeEventListener('change', motionChanged)
    viewport?.removeEventListener('resize', viewportChanged)
    viewport?.removeEventListener('scroll', viewportChanged)
    window.removeEventListener('resize', viewportChanged)
    element?.removeEventListener('click', captureClick, true)
    listening = false
  }
  function addListeners() {
    if (listening || !element) return
    listening = true
    mobile = window.matchMedia('(max-width: 639px)')
    reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
    viewport = window.visualViewport
    mobile.addEventListener('change', presentationChanged)
    reduced.addEventListener('change', motionChanged)
    viewport?.addEventListener('resize', viewportChanged)
    viewport?.addEventListener('scroll', viewportChanged)
    window.addEventListener('resize', viewportChanged)
    element.addEventListener('click', captureClick, true)
  }
  function open() {
    if (disposed || opened) return
    element = dialog.value
    if (!element?.open) return
    opened = true; closing = false; notified = false
    cancelAnimation(); clearTransform()
    if (!options.enabled()) return
    addListeners(); setExpanded(false); updateViewport()
    if (active()) {
      // 父子层直接交接遮罩；同一渲染批次替换加载面板时延续原遮罩透明度。
      const backdrop = options.animateBackdrop ? { from: hasOtherModal() ? 1 : replacementBackdrop?.opacity ?? 0, to: 1 as const } : undefined
      if (backdrop) replacementBackdrop = undefined
      void move(element.getBoundingClientRect().height + 32, 0, 220, backdrop).then(finished => { if (finished && !closing) clearTransform() })
    }
  }

  function sample(event: PointerEvent, current: SheetDrag) {
    const time = performance.now()
    current.samples.push({ y: event.clientY, time })
    current.samples = current.samples.filter(point => time - point.time <= 120)
  }
  function onPointerDown(event: PointerEvent) {
    if (!active() || closing || committingClose || pendingClose || notified || drag || performance.now() < closeReadyAt || !event.isPrimary || event.button !== 0 || element?.hasAttribute('data-dialog-covered')) return
    if (!(event.target instanceof Element) || event.target.closest(interactive) || !(event.currentTarget instanceof Element)) return
    // 抓住进入或回弹中的表面时冻结当前视觉位置，后续位移从这里连续跟手。
    const baseOffset = visualOffset()
    cancelAnimation(); cancelFrame()
    if (baseOffset) element!.style.transform = `translateY(${baseOffset}px)`
    else clearTransform()
    const target = event.currentTarget
    drag = { pointerId: event.pointerId, target, x: event.clientX, y: event.clientY, baseOffset, offset: baseOffset, started: false, expanded: expanded.value, samples: [] }
    sample(event, drag)
    try { target.setPointerCapture(event.pointerId) } catch { drag = undefined; snap(expanded.value) }
  }
  function onPointerMove(event: PointerEvent) {
    const current = drag
    if (!current || event.pointerId !== current.pointerId) return
    if (!active() || element?.hasAttribute('data-dialog-covered')) { onPointerCancel(); return }
    const delta = event.clientY - current.y, horizontal = event.clientX - current.x
    if (!current.started) {
      if (Math.max(Math.abs(delta), Math.abs(horizontal)) < 6) return
      if (Math.abs(horizontal) > Math.abs(delta)) { releaseDrag(); if (current.baseOffset) snap(current.expanded); return }
      current.started = true
    }
    if (event.cancelable) event.preventDefault()
    sample(event, current)
    const downward = options.allowSwipeClose?.() === false ? Math.min(36, delta * .25) : delta
    current.offset = current.baseOffset + (delta < 0 ? Math.max(-36, delta * .35) : downward)
    if (!frame) frame = requestAnimationFrame(() => {
      frame = 0
      if (drag !== current || !element) return
      element.style.willChange = 'transform'
      element.style.transform = `translateY(${current.offset}px)`
    })
  }
  function onPointerUp(event: PointerEvent) {
    const current = drag
    if (!current || event.pointerId !== current.pointerId) return
    sample(event, current)
    releaseDrag()
    if (!current.started) { if (current.baseOffset) snap(current.expanded); return }
    if (event.cancelable) event.preventDefault()
    suppressedClick = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, until: performance.now() + 300 }
    const delta = event.clientY - current.y, first = current.samples[0]!, last = current.samples.at(-1)!
    const speed = last.time > first.time ? (last.y - first.y) / (last.time - first.time) : 0
    const shouldClose = delta >= 110 || delta > 64 && speed > .65
    if (shouldClose && options.allowSwipeClose?.() !== false) { void requestClose('swipe'); return }
    if (element?.hasAttribute('data-dialog-covered')) { snap(current.expanded); return }
    snap(delta <= -18 ? true : current.expanded && delta >= 18 ? false : current.expanded)
  }
  function onPointerCancel(event?: PointerEvent) {
    if (!drag || event && event.pointerId !== drag.pointerId) return
    const current = releaseDrag()!
    if ((current.started || current.baseOffset) && !closing) snap(current.expanded)
  }

  watch(options.enabled, presentationChanged)
  watch(options.closeDisabled, disabled => {
    if (!disabled || !closing) return
    closing = false
    snap(expanded.value)
  })
  onBeforeUnmount(() => {
    if (options.animateBackdrop && active() && !hasOtherModal()) {
      const previous = replacementBackdrop = { opacity: backdropOpacity() }
      queueMicrotask(() => { if (replacementBackdrop === previous) replacementBackdrop = undefined })
    }
    disposed = true; opened = false
    releaseDrag(); cancelAnimation(); cancelBackdropAnimation(); removeListeners(); clearTransform()
    element?.style.removeProperty('--sheet-viewport-height')
    element?.style.removeProperty('--sheet-viewport-bottom')
    element?.style.removeProperty('--sheet-viewport-top')
    if (element) delete element.dataset.sheetExpanded
  })

  return { open, close: () => requestClose('button'), closeDirect, requestClose, expanded, toggleExpanded, onPointerDown, onPointerMove, onPointerUp, onPointerCancel }
}
