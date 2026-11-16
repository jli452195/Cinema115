import '../styles/scrollbars.css'

const managedSelector = '[data-auto-scrollbar]'
const nativeSelector = 'textarea, select, pre, code, .cm-editor, [data-scrollbar="native"]'
const scrollKeys = new Set(['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'PageUp', 'PageDown', 'Home', 'End', ' '])
let uninstall: (() => void) | undefined

// 只管理滚动指示的显隐，不拦截滚轮、键盘默认行为或重建滚动容器。
export function installAutoHideScrollbars(): () => void {
  if (uninstall) return uninstall
  const root = document.documentElement
  const hadRootMarker = root.hasAttribute('data-auto-scrollbar')
  root.setAttribute('data-auto-scrollbar', '')
  const system = matchMedia('(pointer: coarse), (forced-colors: active), (prefers-contrast: more), (prefers-reduced-transparency: reduce)')
  const timers = new Map<HTMLElement, number>()
  let edge: HTMLElement | undefined
  let dragging: HTMLElement | undefined
  let frame = 0
  let pointer: PointerEvent | undefined

  function axes(element: HTMLElement) {
    const style = getComputedStyle(element)
    const page = element === document.scrollingElement
    return {
      vertical: (page || /auto|scroll/.test(style.overflowY)) && style.overflowY !== 'hidden' && style.overflowY !== 'clip' && element.scrollHeight > element.clientHeight + 1,
      horizontal: (page || /auto|scroll/.test(style.overflowX)) && style.overflowX !== 'hidden' && style.overflowX !== 'clip' && element.scrollWidth > element.clientWidth + 1,
      rtl: style.direction === 'rtl',
    }
  }
  function eligible(element: Element | null): element is HTMLElement {
    if (!(element instanceof HTMLElement) || !element.matches(managedSelector) || element.closest(nativeSelector) || !element.isConnected || element.classList.contains('is-collapsed')) return false
    const style = getComputedStyle(element)
    if (style.scrollbarWidth === 'none' || !element.getClientRects().length) return false
    const scroll = axes(element)
    return scroll.vertical || scroll.horizontal
  }
  function clearTimer(element: HTMLElement) {
    const timer = timers.get(element)
    if (timer !== undefined) window.clearTimeout(timer)
    timers.delete(element)
  }
  function hide(element: HTMLElement) {
    clearTimer(element)
    element.removeAttribute('data-scrollbar-state')
  }
  function delayHide(element: HTMLElement, delay = 850) {
    clearTimer(element)
    if (element === edge || element === dragging) return
    timers.set(element, window.setTimeout(() => hide(element), delay))
  }
  function reveal(element: HTMLElement, state: string, delay = 850) {
    if (system.matches) return
    clearTimer(element)
    element.setAttribute('data-scrollbar-state', element === dragging ? 'dragging' : element === edge ? 'edge' : state)
    delayHide(element, delay)
  }
  function candidates(target: EventTarget | null) {
    let element = target instanceof Element ? target.closest(managedSelector) : root
    const result: HTMLElement[] = []
    while (element) {
      if (eligible(element)) result.push(element)
      element = element.parentElement?.closest(managedSelector) ?? null
    }
    return result
  }
  function edgeAt(event: PointerEvent) {
    for (const element of candidates(event.target)) {
      const page = element === document.scrollingElement
      const rect = page ? { left: 0, top: 0, right: innerWidth, bottom: innerHeight } : element.getBoundingClientRect()
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) continue
      const scroll = axes(element)
      if ((scroll.vertical && (scroll.rtl ? event.clientX <= rect.left + 14 : event.clientX >= rect.right - 14)) || (scroll.horizontal && event.clientY >= rect.bottom - 14)) return element
    }
  }
  function setEdge(next: HTMLElement | undefined) {
    if (next === edge) return
    const previous = edge
    edge = next
    if (previous) delayHide(previous, 400)
    if (next) reveal(next, 'edge')
  }
  function releaseDrag(event?: PointerEvent) {
    const previous = dragging
    dragging = undefined
    if (previous) setEdge((event || pointer) ? edgeAt((event || pointer)!) : undefined)
    if (previous) reveal(previous, 'active')
  }
  function onPointerMove(event: PointerEvent) {
    if (system.matches || event.pointerType !== 'mouse') return
    pointer = event
    if (frame) return
    frame = requestAnimationFrame(() => {
      frame = 0
      const current = pointer
      if (!current) return
      // 原生滑块可能在浏览器内部处理释放，下一次无按键移动也应解除锁定。
      if (dragging && !current.buttons) releaseDrag(current)
      if (!dragging) setEdge(edgeAt(current))
    })
  }
  function onPointerDown(event: PointerEvent) {
    if (system.matches || event.pointerType !== 'mouse' || event.button !== 0) return
    const target = edgeAt(event)
    if (!target) return
    setEdge(target)
    dragging = target
    reveal(target, 'dragging')
  }
  function onScroll(event: Event) {
    const target = event.target === document ? document.scrollingElement : event.target
    if (eligible(target instanceof Element ? target : null)) reveal(target as HTMLElement, 'active')
  }
  function onKeyDown(event: KeyboardEvent) {
    if (system.matches || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || !scrollKeys.has(event.key)) return
    if (event.target instanceof Element && event.target.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) return
    const target = candidates(event.target)[0]
    if (target) reveal(target, 'key', 1200)
  }
  function reset() {
    edge = undefined
    dragging = undefined
    pointer = undefined
    if (frame) cancelAnimationFrame(frame)
    frame = 0
    for (const element of [...timers.keys()]) hide(element)
    document.querySelectorAll<HTMLElement>(`${managedSelector}[data-scrollbar-state]`).forEach(hide)
  }
  function onVisibility() { if (document.hidden) reset() }
  function onPointerOut(event: PointerEvent) { if (!event.relatedTarget && !dragging) setEdge(undefined) }
  function onToggle(event: Event) {
    if (!(event.target instanceof HTMLDialogElement) || event.target.open) return
    const dialog = event.target
    if (edge && dialog.contains(edge)) { hide(edge); edge = undefined }
    if (dragging && dialog.contains(dragging)) { hide(dragging); dragging = undefined }
    dialog.querySelectorAll<HTMLElement>(`${managedSelector}[data-scrollbar-state]`).forEach(hide)
  }

  document.addEventListener('scroll', onScroll, { capture: true, passive: true })
  document.addEventListener('pointermove', onPointerMove, { passive: true })
  document.addEventListener('pointerdown', onPointerDown, { capture: true, passive: true })
  document.addEventListener('pointerout', onPointerOut, { passive: true })
  document.addEventListener('keydown', onKeyDown)
  document.addEventListener('toggle', onToggle, true)
  document.addEventListener('visibilitychange', onVisibility)
  window.addEventListener('pointerup', releaseDrag, true)
  window.addEventListener('pointercancel', reset, true)
  window.addEventListener('blur', reset)
  system.addEventListener('change', reset)
  uninstall = () => {
    reset()
    document.removeEventListener('scroll', onScroll, true)
    document.removeEventListener('pointermove', onPointerMove)
    document.removeEventListener('pointerdown', onPointerDown, true)
    document.removeEventListener('pointerout', onPointerOut)
    document.removeEventListener('keydown', onKeyDown)
    document.removeEventListener('toggle', onToggle, true)
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('pointerup', releaseDrag, true)
    window.removeEventListener('pointercancel', reset, true)
    window.removeEventListener('blur', reset)
    system.removeEventListener('change', reset)
    if (!hadRootMarker) root.removeAttribute('data-auto-scrollbar')
    uninstall = undefined
  }
  return uninstall
}

if (import.meta.hot) import.meta.hot.dispose(() => uninstall?.())
