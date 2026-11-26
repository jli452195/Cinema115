import { onBeforeUnmount, onMounted, ref, watch, type Ref, type WatchSource } from 'vue'

interface HorizontalScrollTrackOptions {
  /** 触发重新检测溢出与布局同步的响应式依赖（如选项列表）。 */
  deps?: WatchSource<unknown>[]
  /** 查找当前选中项的选择器，设置后会在初始化及变更时自动居中。 */
  selectedSelector?: string
  /** 该值变化时使用平滑滚动居中（会尊重 prefers-reduced-motion）。 */
  smoothOn?: WatchSource<unknown>
  /** 是否在桌面端溢出时启用鼠标按住横向拖动，默认开启。 */
  mouseDrag?: boolean
  /** 参与宽度监测与聚焦居中的轨道子项选择器。 */
  itemSelector?: string
}

/**
 * 共享横向筛选轨道交互：
 * - 检测左右实际溢出并驱动边缘渐隐状态
 * - 将选中或聚焦项在当前轨道内居中（首尾贴边、绝不触发外层纵向滚动）
 * - 可选的桌面端鼠标横向拖动（仅溢出时生效，超过阈值才抑制末尾 click）
 */
export function useHorizontalScrollTrack(
  containerRef: Ref<HTMLElement | undefined>,
  options: HorizontalScrollTrackOptions = {},
) {
  const {
    deps = [],
    selectedSelector,
    smoothOn,
    mouseDrag = true,
    itemSelector = ':scope > button',
  } = options

  const canScrollLeft = ref(false)
  const canScrollRight = ref(false)
  const isDragging = ref(false)

  let resizeObserver: ResizeObserver | undefined
  let motionQuery: MediaQueryList | undefined
  let activeContainer: HTMLElement | undefined
  let lastLayout = ''
  let drag: { pointerId: number; startX: number; startLeft: number } | undefined
  let suppressClick = false

  function prefersReducedMotion() {
    return !!motionQuery?.matches
  }

  function findSelectedElement(container: HTMLElement) {
    if (!selectedSelector) return undefined
    return container.querySelector<HTMLElement>(selectedSelector) ?? undefined
  }

  function measureLayout(container: HTMLElement) {
    const selected = findSelectedElement(container)
    return [container.clientWidth, container.scrollWidth, selected?.offsetLeft ?? '', selected?.offsetWidth ?? ''].join(':')
  }

  function updateScrollEdges() {
    const container = containerRef.value
    if (!container) {
      canScrollLeft.value = false
      canScrollRight.value = false
      return
    }
    const maximum = Math.max(0, container.scrollWidth - container.clientWidth)
    const left = Math.max(0, Math.min(maximum, container.scrollLeft))
    canScrollLeft.value = left > 1
    canScrollRight.value = maximum - left > 1
  }

  function centerElement(element: HTMLElement | null | undefined, behavior: ScrollBehavior = 'instant') {
    const container = containerRef.value
    if (!container || !element || !container.clientWidth || !container.contains(element)) return
    const maximum = Math.max(0, container.scrollWidth - container.clientWidth)
    if (maximum <= 1) {
      updateScrollEdges()
      return
    }
    const containerRect = container.getBoundingClientRect()
    const itemRect = element.getBoundingClientRect()
    // 统一按相对视口坐标换算轨道内偏移，兼容嵌套子元素且绝不调用 scrollIntoView。
    const itemLeft = itemRect.left - containerRect.left + container.scrollLeft
    const targetLeft = itemLeft - (container.clientWidth - itemRect.width) / 2
    const clamped = Math.max(0, Math.min(maximum, targetLeft))
    container.scrollTo({
      left: clamped,
      behavior: behavior === 'smooth' && !prefersReducedMotion() ? 'smooth' : 'instant',
    })
    updateScrollEdges()
  }

  function centerSelected(behavior: ScrollBehavior = 'instant') {
    const container = containerRef.value
    if (!container) return
    lastLayout = measureLayout(container)
    const selected = findSelectedElement(container)
    if (selected) centerElement(selected, behavior)
    updateScrollEdges()
  }

  function observeItems(container: HTMLElement) {
    if (!resizeObserver) return
    resizeObserver.disconnect()
    resizeObserver.observe(container)
    container.querySelectorAll<HTMLElement>(itemSelector).forEach(item => resizeObserver!.observe(item))
  }

  function handleResize() {
    const container = containerRef.value
    if (!container) return
    // 选中项切换已记录最新布局时，避免尺寸回调打断正在进行的平滑居中。
    if (selectedSelector && measureLayout(container) === lastLayout) {
      updateScrollEdges()
      return
    }
    if (selectedSelector) centerSelected('instant')
    else updateScrollEdges()
  }

  function disconnectObservers() {
    resizeObserver?.disconnect()
    activeContainer = undefined
  }

  function attachContainer(container: HTMLElement | undefined) {
    if (container === activeContainer) return
    stopDrag(true)
    disconnectObservers()
    lastLayout = ''
    if (!container) {
      canScrollLeft.value = false
      canScrollRight.value = false
      return
    }
    activeContainer = container
    if (typeof ResizeObserver !== 'undefined') {
      if (!resizeObserver) resizeObserver = new ResizeObserver(handleResize)
      observeItems(container)
    }
    if (selectedSelector) centerSelected('instant')
    else updateScrollEdges()
  }

  function stopDrag(clearClick = false) {
    drag = undefined
    isDragging.value = false
    if (clearClick) suppressClick = false
    if (typeof window === 'undefined') return
    window.removeEventListener('pointermove', moveDrag, true)
    window.removeEventListener('pointerup', endDrag, true)
    window.removeEventListener('pointercancel', endDrag, true)
    window.removeEventListener('blur', cancelDrag)
  }

  function cancelDrag() {
    stopDrag(true)
  }

  function onPointerdown(event: PointerEvent) {
    if (!mouseDrag) return
    stopDrag(true)
    const container = containerRef.value
    // 仅在桌面鼠标且轨道实际溢出时接管拖动，触摸与手写笔保持原生滚动。
    if (event.pointerType !== 'mouse' || event.button !== 0 || !container || container.scrollWidth - container.clientWidth <= 1) return
    container.scrollTo({ left: container.scrollLeft, behavior: 'instant' })
    drag = { pointerId: event.pointerId, startX: event.clientX, startLeft: container.scrollLeft }
    window.addEventListener('pointermove', moveDrag, { capture: true, passive: false })
    window.addEventListener('pointerup', endDrag, true)
    window.addEventListener('pointercancel', endDrag, true)
    window.addEventListener('blur', cancelDrag)
  }

  function moveDrag(event: PointerEvent) {
    const container = containerRef.value
    if (!drag || drag.pointerId !== event.pointerId || !container) return
    if (!(event.buttons & 1)) {
      stopDrag()
      return
    }
    const distance = event.clientX - drag.startX
    if (!isDragging.value && Math.abs(distance) <= 4) return
    isDragging.value = true
    suppressClick = true
    event.preventDefault()
    container.scrollLeft = drag.startLeft - distance
    updateScrollEdges()
  }

  function endDrag(event: PointerEvent) {
    if (drag?.pointerId === event.pointerId) stopDrag(event.type === 'pointercancel')
  }

  function onClickCapture(event: MouseEvent) {
    if (!mouseDrag || !suppressClick || event.detail === 0) return
    suppressClick = false
    event.preventDefault()
    event.stopImmediatePropagation()
  }

  function onDragstart(event: DragEvent) {
    if (mouseDrag) event.preventDefault()
  }

  function moveFocus(event: KeyboardEvent) {
    if (event.altKey || event.ctrlKey || event.metaKey || !(event.target instanceof HTMLButtonElement)) return
    const container = containerRef.value
    if (!container) return
    const buttons = [...container.querySelectorAll<HTMLButtonElement>(':scope > button:not(:disabled)')]
    const index = buttons.indexOf(event.target)
    if (index < 0 || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const next = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? buttons.length - 1
        : (index + (event.key === 'ArrowRight' ? 1 : buttons.length - 1)) % buttons.length
    const target = buttons[next]
    if (!target) return
    // 方向键仅移动焦点并在当前轨道内居中，不触发外层弹窗纵向跳动。
    target.focus({ preventScroll: true })
    centerElement(target, 'smooth')
  }

  function handleFocusIn(event: FocusEvent, resolveTarget?: (target: HTMLElement) => HTMLElement | null) {
    const container = containerRef.value
    const target = event.target
    if (!container || !(target instanceof HTMLElement) || !target.matches(':focus-visible')) return
    const item = resolveTarget ? resolveTarget(target) : target
    if (item && container.contains(item)) centerElement(item, 'smooth')
  }

  function handleMotionChange() {
    if (selectedSelector && motionQuery?.matches) centerSelected('instant')
  }

  const watchSources: WatchSource<unknown>[] = smoothOn ? [smoothOn, ...deps] : [...deps]
  if (watchSources.length) {
    watch(watchSources, (values, previous) => {
      const container = containerRef.value
      if (!container) return
      observeItems(container)
      if (selectedSelector) {
        const shouldSmooth = Boolean(smoothOn && previous && values[0] !== previous[0])
        centerSelected(shouldSmooth ? 'smooth' : 'instant')
      } else {
        updateScrollEdges()
      }
    }, { deep: true, flush: 'post' })
  }

  watch(containerRef, container => {
    attachContainer(container)
  }, { flush: 'post' })

  onMounted(() => {
    if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
      motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
      motionQuery.addEventListener('change', handleMotionChange)
    }
    attachContainer(containerRef.value)
  })

  onBeforeUnmount(() => {
    stopDrag(true)
    disconnectObservers()
    motionQuery?.removeEventListener('change', handleMotionChange)
  })

  return {
    canScrollLeft,
    canScrollRight,
    isDragging,
    updateScrollEdges,
    centerElement,
    centerSelected,
    moveFocus,
    handleFocusIn,
    onPointerdown,
    onClickCapture,
    onDragstart,
  }
}
