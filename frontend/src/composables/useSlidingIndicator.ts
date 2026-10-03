import { ref, watch, onMounted, onBeforeUnmount, nextTick, type Ref } from 'vue'

export function useSlidingIndicator(
  containerRef: Ref<HTMLElement | undefined>,
  activeSelector: string,
  dependencies: any[],
  options: { persistKey?: string; debounceMs?: number } = {}
) {
  const savedPos = options.persistKey ? (window as any)[options.persistKey] : null
  const indicatorStyle = ref<Record<string, any>>(
    savedPos || { left: '0px', top: '0px', width: '0px', height: '0px', opacity: 0, transition: 'none' }
  )

  function updateIndicator(animate = true) {
    if (!containerRef.value) return
    const activeEl = containerRef.value.querySelector(activeSelector) as HTMLElement

    if (activeEl) {
      const newStyle = {
        left: `${activeEl.offsetLeft}px`,
        top: `${activeEl.offsetTop}px`,
        width: `${activeEl.offsetWidth}px`,
        height: `${activeEl.offsetHeight}px`,
        opacity: 1,
        transition: animate ? 'all 300ms cubic-bezier(0.4, 0, 0.2, 1)' : 'none'
      }
      indicatorStyle.value = newStyle
      if (options.persistKey) {
        ;(window as any)[options.persistKey] = { ...newStyle, transition: 'none' }
      }
    } else {
      indicatorStyle.value.opacity = 0
    }
  }

  function onResize() {
    updateIndicator(false)
  }

  watch(dependencies, async () => {
    await nextTick()
    updateIndicator(true)
  }, { deep: true })

  onMounted(() => {
    window.addEventListener('resize', onResize, { passive: true })
    const isFirstLoad = options.persistKey && !(window as any)[options.persistKey]
    
    if (isFirstLoad || !options.persistKey) {
      // 没有任何缓存位置时，瞬间定位
      setTimeout(() => updateIndicator(false), 20)
    } else {
      // 渲染缓存的位置后，开启过渡滑向新位置
      setTimeout(() => updateIndicator(true), 30)
    }
  })

  onBeforeUnmount(() => {
    window.removeEventListener('resize', onResize)
  })

  return { indicatorStyle, updateIndicator }
}
