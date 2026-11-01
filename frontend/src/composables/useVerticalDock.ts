import { onBeforeUnmount, onMounted, watch, type Ref } from 'vue'

const BASE_SIZE = 36
const MAX_SIZE = 56
const RADIUS = 110
const GAP = 6
// The cosine field over 42px-spaced centres adds at most about 53px.
const MAX_EXTRA_HEIGHT = 65

export function useVerticalDock(
  enabled: Ref<boolean>,
  container: Ref<HTMLElement | undefined>,
  onUpdate: () => void,
) {
  let frame: number | undefined, previousTime: number | undefined
  let hovering = false, mouseY = 0
  let items: HTMLElement[] = [], centres: number[] = [], sizes: number[] = []
  let environment: MediaQueryList | undefined

  function reset() {
    if (frame !== undefined) cancelAnimationFrame(frame)
    frame = undefined
    previousTime = undefined
    hovering = false
    for (const item of items) {
      item.style.removeProperty('width')
      item.style.removeProperty('height')
      item.style.removeProperty('--dock-icon-size')
    }
    sizes = items.map(() => BASE_SIZE)
    onUpdate()
  }

  function measure() {
    const element = container.value
    if (!element) return false
    const nextItems = [...element.querySelectorAll<HTMLElement>('.management-nav-link')]
    if (!nextItems.length) return false
    if (nextItems.length !== items.length || nextItems.some((item, i) => item !== items[i])) {
      reset()
      items = nextItems
      sizes = items.map(() => BASE_SIZE)
    }

    const style = getComputedStyle(element)
    const topInset = parseFloat(style.paddingTop) + parseFloat(style.borderTopWidth)
    const bottomInset = parseFloat(style.paddingBottom) + parseFloat(style.borderBottomWidth)
    const neutralHeight = topInset + bottomInset + items.length * BASE_SIZE + (items.length - 1) * GAP
    const extraHeight = Math.min(MAX_EXTRA_HEIGHT, items.length * (MAX_SIZE - BASE_SIZE))
    const maxHeight = parseFloat(style.maxHeight)
    // Reserve room for the whole wave, including when the base list already fits.
    if (!Number.isFinite(maxHeight) || neutralHeight + extraHeight > maxHeight
      || element.scrollHeight > element.clientHeight + 1) return false

    const bounds = element.getBoundingClientRect()
    const neutralTop = bounds.top + bounds.height / 2 - neutralHeight / 2
    // Cache the neutral centres: animated dimensions never feed back into targets.
    // Do not clear current sizes here, so rapid re-entry continues smoothly.
    centres = items.map((_, i) => neutralTop + topInset + BASE_SIZE / 2 + i * (BASE_SIZE + GAP))
    return true
  }

  function schedule() {
    if (frame === undefined) frame = requestAnimationFrame(animate)
  }

  function animate(time: number) {
    frame = undefined
    if (!enabled.value || !environment?.matches) { reset(); return }
    const elapsed = previousTime === undefined ? 16 : Math.min(time - previousTime, 32)
    previousTime = time
    const damping = 1 - Math.exp(-elapsed / 50)
    let moving = false

    for (let i = 0; i < items.length; i++) {
      const distance = hovering ? Math.abs(centres[i] - mouseY) : Infinity
      const weight = distance < RADIUS ? (1 + Math.cos(distance / RADIUS * Math.PI)) / 2 : 0
      const target = BASE_SIZE + (MAX_SIZE - BASE_SIZE) * weight
      const difference = target - sizes[i]
      const next = Math.abs(difference) > 0.05 ? sizes[i] + difference * damping : target
      if (Math.abs(difference) > 0.05) moving = true
      if (next === sizes[i]) continue
      sizes[i] = next
      if (next === BASE_SIZE) {
        items[i].style.removeProperty('width')
        items[i].style.removeProperty('height')
        items[i].style.removeProperty('--dock-icon-size')
      } else {
        items[i].style.width = `${next.toFixed(3)}px`
        items[i].style.height = `${next.toFixed(3)}px`
        items[i].style.setProperty('--dock-icon-size', `${(next * 19 / BASE_SIZE).toFixed(3)}px`)
      }
    }
    // Read tooltip geometry only after all item dimensions have been written.
    onUpdate()
    if (moving) schedule()
    else previousTime = undefined
  }

  function onPointerMove(event: PointerEvent) {
    if (event.pointerType !== 'mouse') { reset(); return }
    if (!enabled.value || !environment?.matches || document.hidden) return
    if (!hovering) {
      if (!measure()) { reset(); return }
      hovering = true
    }
    mouseY = event.clientY
    schedule()
  }

  function onPointerLeave() {
    hovering = false
    if (sizes.some(size => size !== BASE_SIZE)) schedule()
  }

  function visibilityChanged() { if (document.hidden) reset() }
  watch(enabled, reset)
  onMounted(() => {
    environment = window.matchMedia('(hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference)')
    environment.addEventListener('change', reset)
    window.addEventListener('resize', reset)
    window.addEventListener('blur', reset)
    window.addEventListener('scroll', reset, { passive: true, capture: true })
    document.addEventListener('visibilitychange', visibilityChanged)
  })
  onBeforeUnmount(() => {
    environment?.removeEventListener('change', reset)
    window.removeEventListener('resize', reset)
    window.removeEventListener('blur', reset)
    window.removeEventListener('scroll', reset, true)
    document.removeEventListener('visibilitychange', visibilityChanged)
    reset()
  })
  return { onPointerMove, onPointerLeave, reset }
}
