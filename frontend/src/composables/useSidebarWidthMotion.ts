import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

const DURATION = 300

// Match the reference sidebar's cubic-bezier(0.2, 0, 0, 1).
function ease(progress: number) {
  let low = 0, high = 1
  for (let i = 0; i < 14; i++) {
    const t = (low + high) / 2
    const x = 0.6 * t * (1 - t) ** 2 + t ** 3
    if (x < progress) low = t
    else high = t
  }
  const t = (low + high) / 2
  return t * t * (3 - 2 * t)
}

// One width clock keeps newly mounted headers and viewport backgrounds in sync.
export function useSidebarWidthMotion(
  collapsed: Ref<boolean>, desktop: Ref<boolean>, workspace: Ref<HTMLElement | undefined>,
) {
  const target = () => collapsed.value ? 64 : 240
  const width = ref(target())
  let frame: number | undefined
  let mounted = false
  let motion: MediaQueryList | undefined

  function cancel() {
    if (frame !== undefined) cancelAnimationFrame(frame)
    frame = undefined
  }
  function finish() { cancel(); width.value = target() }
  function change() {
    cancel()
    const destination = target(), start = width.value
    if (!mounted || !workspace.value || !desktop.value || !motion?.matches || document.hidden || start === destination) {
      width.value = destination
      return
    }
    const started = performance.now()
    function tick(time: number) {
      if (!workspace.value) { finish(); return }
      const progress = Math.min(1, Math.max(0, (time - started) / DURATION))
      width.value = start + (destination - start) * ease(progress)
      if (progress < 1) frame = requestAnimationFrame(tick)
      else { frame = undefined; width.value = destination }
    }
    frame = requestAnimationFrame(tick)
  }
  function visibilityChanged() { if (document.hidden) finish() }
  watch([collapsed, desktop], change)
  onMounted(() => {
    motion = window.matchMedia('(prefers-reduced-motion: no-preference)')
    mounted = true
    motion.addEventListener('change', finish)
    window.addEventListener('resize', finish)
    document.addEventListener('visibilitychange', visibilityChanged)
  })
  onBeforeUnmount(() => {
    cancel()
    motion?.removeEventListener('change', finish)
    window.removeEventListener('resize', finish)
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return width
}
