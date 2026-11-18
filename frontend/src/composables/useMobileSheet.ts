import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

interface MobileSheetOptions {
  enabled: () => boolean
  closeDisabled: () => boolean
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

export function useMobileSheet(dialog: Ref<HTMLDialogElement | undefined>, options: MobileSheetOptions) {
  const expanded = ref(false)
  let opened = false, closing = false, notified = false, disposed = false, listening = false
  let element: HTMLDialogElement | undefined
  let mobile: MediaQueryList | undefined, reduced: MediaQueryList | undefined
  let viewport: VisualViewport | null = null
  let drag: SheetDrag | undefined, frame = 0
  let animation: Animation | undefined
  let suppressedClick: { pointerId: number; x: number; y: number; until: number } | undefined

  const active = () => opened && !!element?.open && options.enabled() && !!mobile?.matches
  const reducedMotion = () => !!reduced?.matches
  const closeBlocked = () => options.closeDisabled() || !!element?.hasAttribute('data-dialog-covered')

  function cancelAnimation() {
    const current = animation
    animation = undefined
    current?.cancel()
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
  async function move(from: number, to: number, duration: number) {
    cancelAnimation()
    const surface = element
    if (!surface || disposed) return false
    surface.style.transform = `translateY(${to}px)`
    if (reducedMotion() || typeof surface.animate !== 'function') return true
    surface.style.willChange = 'transform'
    const current = surface.animate([
      { transform: `translateY(${from}px)` }, { transform: `translateY(${to}px)` },
    ], { duration, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'both' })
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
    void move(offset, 0, 200).then(finished => { if (finished && !closing) clearTransform() })
  }
  function notifyClose() {
    if (notified || disposed) return
    notified = true; opened = false; closing = false
    removeListeners()
    options.onClose()
  }
  function close() {
    if (disposed || notified || closing || closeBlocked()) return
    releaseDrag()
    closing = true
    options.onClosing?.()
    if (!active() || reducedMotion()) {
      cancelAnimation(); clearTransform(); notifyClose(); return
    }
    const from = visualOffset(), destination = element!.getBoundingClientRect().height + 32
    void move(from, destination, 180).then(finished => {
      if (!finished || !closing) return
      if (closeBlocked()) { closing = false; snap(expanded.value); return }
      notifyClose()
    })
  }
  function toggleExpanded() {
    if (!active() || closing || notified || element?.hasAttribute('data-dialog-covered')) return
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
    releaseDrag(); cancelAnimation(); clearTransform()
    if (closing) {
      closing = false
      if (!closeBlocked()) notifyClose()
    }
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
    if (active()) void move(element.getBoundingClientRect().height + 32, 0, 220).then(finished => { if (finished && !closing) clearTransform() })
  }

  function sample(event: PointerEvent, current: SheetDrag) {
    const time = performance.now()
    current.samples.push({ y: event.clientY, time })
    current.samples = current.samples.filter(point => time - point.time <= 120)
  }
  function onPointerDown(event: PointerEvent) {
    if (!active() || closing || notified || drag || !event.isPrimary || event.button !== 0 || element?.hasAttribute('data-dialog-covered')) return
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
    current.offset = current.baseOffset + (delta < 0 ? Math.max(-36, delta * .35) : delta)
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
    if (shouldClose && !closeBlocked()) { close(); return }
    if (shouldClose || element?.hasAttribute('data-dialog-covered')) { snap(current.expanded); return }
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
    disposed = true; opened = false
    releaseDrag(); cancelAnimation(); removeListeners(); clearTransform()
    element?.style.removeProperty('--sheet-viewport-height')
    element?.style.removeProperty('--sheet-viewport-bottom')
    element?.style.removeProperty('--sheet-viewport-top')
    if (element) delete element.dataset.sheetExpanded
  })

  return { open, close, expanded, toggleExpanded, onPointerDown, onPointerMove, onPointerUp, onPointerCancel }
}
