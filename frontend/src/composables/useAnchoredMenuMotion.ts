import { onBeforeUnmount } from 'vue'
import { animateFloatingPanel, readFloatingPanelMotion } from '../lib/floatingPanelMotion'

type PanelState = ReturnType<typeof readFloatingPanelMotion>
type Motion = { animation: Animation; panel?: HTMLElement; complete: () => void }

export function useAnchoredMenuMotion(options: { compact: () => boolean; immediate: () => boolean }) {
  const running = new Map<Element, Motion>()
  const interrupted = new WeakMap<Element, PanelState>()
  let handoff: PanelState | undefined

  function cancel(element: Element, snapshot = true) {
    const motion = running.get(element)
    if (!motion) return
    if (snapshot && motion.panel && element.isConnected) interrupted.set(element, readFloatingPanelMotion(motion.panel))
    running.delete(element)
    delete (element as HTMLElement).dataset.menuMotion
    motion.animation.cancel()
  }

  function finish(element?: Element | null) {
    if (element) running.get(element)?.complete()
    else [...running.values()].forEach(motion => motion.complete())
  }

  function release(element: Element) {
    const panel = running.get(element)?.panel
    if (panel && element.isConnected) {
      const current = readFloatingPanelMotion(panel)
      handoff = current
      // 只交给本次 Vue 更新挂载的新菜单，后续打开从初始状态开始。
      queueMicrotask(() => { if (handoff === current) handoff = undefined })
    }
    cancel(element, false)
  }

  function run(element: Element, opening: boolean, done: () => void) {
    const menu = element as HTMLElement
    cancel(menu)
    const from = interrupted.get(menu) ?? (opening ? handoff : undefined)
    interrupted.delete(menu)
    handoff = undefined
    if (options.immediate() || matchMedia('(prefers-reduced-motion: reduce)').matches) { done(); return }

    const panel = options.compact() ? menu.querySelector<HTMLElement>('[data-menu-panel]') ?? undefined : undefined
    const animation = panel ? animateFloatingPanel(panel, opening, 'top', from) : menu.animate(opening
      ? [{ transform: 'translateY(-4px)', opacity: 0 }, { transform: 'translateY(0px)', opacity: 1 }]
      : [{ transform: 'translateY(0px)', opacity: 1 }, { transform: 'translateY(-4px)', opacity: 0 }],
    { duration: 140, easing: 'ease', fill: 'both' })
    const motion: Motion = {
      animation, panel,
      complete() {
        if (running.get(menu) !== motion) return
        cancel(menu, false)
        done()
      },
    }
    running.set(menu, motion)
    menu.dataset.menuMotion = opening ? 'entering' : 'leaving'
    void animation.finished.then(motion.complete, () => {})
  }

  onBeforeUnmount(() => { for (const element of running.keys()) cancel(element, false) })

  return {
    enter: (element: Element, done: () => void) => run(element, true, done),
    leave: (element: Element, done: () => void) => run(element, false, done),
    cancel,
    release,
    finish,
  }
}
