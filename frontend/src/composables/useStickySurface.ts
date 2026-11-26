import { ref, watch, type Ref } from 'vue'

/** 仅在锚点越过滚动容器顶边后激活吸顶态。 */
export function useStickySurface(container: Ref<HTMLElement | undefined>, anchor: Ref<HTMLElement | undefined>) {
  const stuck = ref(false)

  watch([container, anchor], ([root, target], _, onCleanup) => {
    stuck.value = false
    if (!root || !target) return
    // 不支持 IntersectionObserver 时保守启用吸顶表面。
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
