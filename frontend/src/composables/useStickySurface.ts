import { ref, watch, type Ref } from 'vue'

/** Paint a sticky toolbar only after its in-flow anchor reaches the scroll edge. */
export function useStickySurface(container: Ref<HTMLElement | undefined>, anchor: Ref<HTMLElement | undefined>) {
  const stuck = ref(false)

  watch([container, anchor], ([root, target], _, onCleanup) => {
    stuck.value = false
    if (!root || !target) return
    // Older browsers still need an opaque surface over scrolling content.
    if (typeof IntersectionObserver === 'undefined') {
      stuck.value = true
      return
    }
    const observer = new IntersectionObserver(([entry]) => {
      if (entry?.rootBounds) stuck.value = entry.boundingClientRect.top < entry.rootBounds.top
    }, { root, threshold: 1 })
    observer.observe(target)
    onCleanup(() => observer.disconnect())
  }, { immediate: true, flush: 'post' })

  return stuck
}
