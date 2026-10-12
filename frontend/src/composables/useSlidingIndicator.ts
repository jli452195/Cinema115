import { ref, watch, onMounted, onBeforeUnmount, type Ref } from 'vue'

export function useSlidingIndicator(
  containerRef: Ref<HTMLElement | undefined>,
  activeSelector: string,
  dependencies: any[],
  options: { persistKey?: string; debounceMs?: number; insetY?: number; durationMs?: number; easing?: string } = {}
) {
  const transition = ['left', 'top', 'width', 'height']
    .map(property => `${property} ${options.durationMs ?? 300}ms ${options.easing ?? 'cubic-bezier(0.4, 0, 0.2, 1)'}`)
    .join(', ')
  const savedPos = options.persistKey ? (window as any)[options.persistKey] : null
  const indicatorStyle = ref<Record<string, any>>(
    savedPos || { left: '0px', top: '0px', width: '0px', height: '0px', opacity: 0, transition: 'none' }
  )
  const motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
  let resizeObserver: ResizeObserver | undefined
  let restoringSavedPosition = Boolean(savedPos)
  let initialTimer: ReturnType<typeof setTimeout> | undefined

  function activePosition() {
    const activeEl = containerRef.value?.querySelector<HTMLElement>(activeSelector)
    if (!activeEl || !activeEl.offsetWidth || !activeEl.offsetHeight) return null
    const insetY = options.insetY || 0
    return {
      left: `${activeEl.offsetLeft}px`,
      top: `${activeEl.offsetTop + insetY}px`,
      width: `${activeEl.offsetWidth}px`,
      height: `${Math.max(0, activeEl.offsetHeight - insetY * 2)}px`,
    }
  }

  function updateIndicator(animate = true) {
    const position = activePosition()
    if (position) {
      const newStyle = {
        ...position,
        opacity: 1,
        transition: animate && indicatorStyle.value.opacity && !motionQuery.matches
          ? transition
          : 'none'
      }
      indicatorStyle.value = newStyle
      if (options.persistKey) {
        ;(window as any)[options.persistKey] = { ...newStyle, transition: 'none' }
      }
    } else {
      indicatorStyle.value = { ...indicatorStyle.value, opacity: 0, transition: 'none' }
    }
  }

  function onResize() {
    if (restoringSavedPosition) return
    updateIndicator(false)
  }

  function observeContainer() {
    resizeObserver?.disconnect()
    const container = containerRef.value
    if (!container || !resizeObserver) return
    resizeObserver.observe(container)
    container.querySelectorAll<HTMLElement>('button, a').forEach(element => resizeObserver!.observe(element))
  }

  watch(dependencies, () => {
    if (restoringSavedPosition) return
    updateIndicator(true)
    observeContainer()
  }, { deep: true, flush: 'post' })

  // 条件挂载的筛选条首次出现时直接定位，不从上次已隐藏的位置滑入。
  watch(containerRef, () => {
    if (restoringSavedPosition) return
    updateIndicator(false)
    observeContainer()
  }, { flush: 'post' })

  onMounted(() => {
    window.addEventListener('resize', onResize, { passive: true })
    motionQuery.addEventListener('change', onResize)
    resizeObserver = new ResizeObserver(() => {
      const position = activePosition()
      // 选中切换已写入目标位置时不打断动画；仅在布局尺寸变化后重新定位。
      if (position && Object.entries(position).some(([key, value]) => indicatorStyle.value[key] !== value)) {
        updateIndicator(false)
      }
    })
    if (savedPos) {
      // 先渲染缓存位置，再保留导航栏原有的滑入过渡。
      initialTimer = setTimeout(() => {
        restoringSavedPosition = false
        updateIndicator(true)
        observeContainer()
      }, 30)
    } else {
      updateIndicator(false)
      observeContainer()
    }
  })

  onBeforeUnmount(() => {
    window.removeEventListener('resize', onResize)
    motionQuery.removeEventListener('change', onResize)
    resizeObserver?.disconnect()
    clearTimeout(initialTimer)
  })

  return { indicatorStyle, updateIndicator }
}
