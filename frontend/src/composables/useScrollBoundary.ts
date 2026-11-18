import { ref, toValue, watch, type MaybeRefOrGetter } from 'vue'

/** 仅在目标从上方越过固定栏底边时切换，不监听逐帧滚动。 */
export function useScrollBoundary(
  target: MaybeRefOrGetter<HTMLElement | null | undefined>,
  boundary: MaybeRefOrGetter<HTMLElement | null | undefined>,
  enabled: MaybeRefOrGetter<boolean>,
) {
  const passed = ref(false)
  watch(() => [toValue(target), toValue(boundary), toValue(enabled)] as const, ([element, bar, active], _, onCleanup) => {
    passed.value = false
    if (!element || !bar || !active) return
    let observer: IntersectionObserver | undefined
    let previousBottom = -1
    function observe() {
      if (!bar || !element) return
      const bottom = Math.ceil(bar.getBoundingClientRect().bottom)
      if (bottom === previousBottom) return
      previousBottom = bottom
      observer?.disconnect()
      observer = new IntersectionObserver(([entry]) => {
        if (!entry) return
        // 屏幕下方尚未出现的标题不能误判为已经滚过顶栏。
        passed.value = entry.boundingClientRect.bottom < (entry.rootBounds?.top ?? bottom)
      }, { rootMargin: `-${bottom}px 0px 0px 0px`, threshold: 0 })
      observer.observe(element)
    }
    observe()
    const resize = new ResizeObserver(observe)
    resize.observe(bar, { box: 'border-box' })
    onCleanup(() => { observer?.disconnect(); resize.disconnect() })
  }, { immediate: true, flush: 'post' })
  return passed
}
