import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

const DURATION = 300

// Both layouts share their icon column; CSS animates only labels and spacing.
export function useNavigationMotion(
  collapsed: Ref<boolean>,
  mobile: Ref<boolean>,
  container: Ref<HTMLElement | undefined>,
  onStart: () => void,
  restoreLayout: () => Promise<void>,
) {
  const displayedCollapsed = ref(collapsed.value)
  const transitioning = ref(false)
  let timer: number | undefined
  let mounted = false
  let motion: MediaQueryList | undefined

  function cancel() {
    if (timer !== undefined) window.clearTimeout(timer)
    timer = undefined
  }
  function settle() {
    cancel()
    transitioning.value = false
    void restoreLayout()
  }
  function finish() {
    onStart()
    displayedCollapsed.value = collapsed.value
    settle()
  }
  function change() {
    cancel()
    onStart()
    displayedCollapsed.value = collapsed.value
    transitioning.value = mounted && !!container.value && !mobile.value && !!motion?.matches && !document.hidden
    // Native CSS transitions reverse from their current values on a new target.
    if (transitioning.value) timer = window.setTimeout(settle, DURATION)
    else void restoreLayout()
  }
  function visibilityChanged() { if (document.hidden) finish() }
  watch([collapsed, mobile], change)
  onMounted(() => {
    mounted = true
    motion = window.matchMedia('(prefers-reduced-motion: no-preference)')
    motion.addEventListener('change', finish)
    window.addEventListener('resize', finish)
    document.addEventListener('visibilitychange', visibilityChanged)
  })
  onBeforeUnmount(() => {
    mounted = false
    cancel()
    motion?.removeEventListener('change', finish)
    window.removeEventListener('resize', finish)
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return { displayedCollapsed, transitioning }
}
