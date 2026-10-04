// 可见停留只是已查看的近似条件；后台、失焦和快速滚过都不累计时间。
export function observeNotificationVisibility(element: Element, seen: () => void) {
  if (typeof IntersectionObserver === 'undefined') return () => {}
  let visible = false, done = false
  let timer: ReturnType<typeof setTimeout> | undefined
  function reset() {
    clearTimeout(timer)
    timer = undefined
    if (!visible || done || document.hidden || !document.hasFocus()) return
    timer = setTimeout(() => {
      timer = undefined
      if (!visible || document.hidden || !document.hasFocus()) return
      done = true
      seen()
    }, 1000)
  }
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) if (entry.target === element) {
      visible = entry.isIntersecting && entry.intersectionRatio >= 0.5
      reset()
    }
  }, { threshold: [0, 0.5] })
  observer.observe(element)
  document.addEventListener('visibilitychange', reset)
  window.addEventListener('focus', reset)
  window.addEventListener('blur', reset)
  return () => {
    done = true
    clearTimeout(timer)
    observer.disconnect()
    document.removeEventListener('visibilitychange', reset)
    window.removeEventListener('focus', reset)
    window.removeEventListener('blur', reset)
  }
}
