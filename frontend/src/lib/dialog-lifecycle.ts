let scrollLocks = 0
let originalOverflow = ''

export function lockBodyScroll() {
  if (scrollLocks++ === 0) {
    originalOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }
  let released = false
  return () => {
    if (released) return
    released = true
    if (--scrollLocks === 0) document.body.style.overflow = originalOverflow
  }
}

export function restoreDialogFocus(target: HTMLElement | null | undefined) {
  if (!target?.isConnected || target === document.body || !target.getClientRects().length || target.matches(':disabled') || target.closest('[inert], dialog:not([open])')) return false
  target.focus({ preventScroll: true })
  return document.activeElement === target
}

interface DialogLayer { targets: HTMLElement[]; unlock: () => void }
const layers: DialogLayer[] = []

export function createDialogLifecycle() {
  let layer: DialogLayer | undefined
  function acquire(target = document.activeElement instanceof HTMLElement ? document.activeElement : null) {
    if (layer) return
    // 父弹窗先卸载时，子弹窗仍能沿原触发链恢复到页面上的控件。
    const fallback = layers.at(-1)?.targets || []
    layer = { targets: [...new Set([...(target ? [target] : []), ...fallback])], unlock: lockBodyScroll() }
    layers.push(layer)
  }
  function release(returnFocus = true) {
    if (!layer) return
    const current = layer
    const wasTopLayer = layers.at(-1) === current
    layer = undefined
    layers.splice(layers.indexOf(current), 1)
    current.unlock()
    if (returnFocus && wasTopLayer) current.targets.some(restoreDialogFocus)
  }
  return { acquire, release }
}
