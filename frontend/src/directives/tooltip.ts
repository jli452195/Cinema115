import type { ObjectDirective } from 'vue'
import '../styles/tooltip.css'

type TooltipValue = string | { title?: string; text?: string } | undefined
const instances = new WeakMap<HTMLElement, { update: (value: TooltipValue) => void; dispose: () => void }>()
let sequence = 0, closeActive: (() => void) | undefined

// 指令不增加布局容器；仅在展示时创建浮层和页面监听。
export const vTooltip: ObjectDirective<HTMLElement, TooltipValue> = {
  mounted(element, binding) {
    let value = binding.value, panel: HTMLElement | undefined, listeners: AbortController | undefined
    let timer: ReturnType<typeof setTimeout> | undefined, hovered = false
    const id = `app-tooltip-${++sequence}`
    const content = () => typeof value === 'string' ? { text: value } : value
    function cancel() { clearTimeout(timer); timer = undefined }
    function close() {
      cancel(); listeners?.abort(); listeners = undefined
      panel?.remove(); panel = undefined
      const described = (element.getAttribute('aria-describedby') || '').split(/\s+/).filter(token => token && token !== id)
      if (described.length) element.setAttribute('aria-describedby', described.join(' '))
      else element.removeAttribute('aria-describedby')
      if (closeActive === close) closeActive = undefined
    }
    function show() {
      cancel()
      const data = content()
      if (panel || document.hidden || !element.isConnected || (!data?.title && !data?.text)) return
      closeActive?.(); closeActive = close
      panel = document.createElement('div')
      panel.id = id; panel.className = 'app-tooltip'; panel.setAttribute('role', 'tooltip')
      panel.setAttribute('popover', 'manual')
      if (data.title) {
        const title = document.createElement('strong')
        title.className = 'app-tooltip-title'; title.textContent = data.title; panel.append(title)
        panel.classList.add('app-tooltip-rich')
      }
      if (data.text) {
        const text = document.createElement('span')
        text.className = 'app-tooltip-text'; text.textContent = data.text; panel.append(text)
      }
      document.body.append(panel)
      panel.showPopover?.()
      const bounds = element.getBoundingClientRect(), width = panel.offsetWidth, height = panel.offsetHeight
      const top = bounds.bottom + 8 + height <= innerHeight - 12 ? bounds.bottom + 8 : bounds.top - height - 8
      panel.style.left = `${Math.max(12, Math.min(bounds.left + (bounds.width - width) / 2, innerWidth - width - 12))}px`
      panel.style.top = `${Math.max(12, Math.min(top, innerHeight - height - 12))}px`
      element.setAttribute('aria-describedby', [element.getAttribute('aria-describedby'), id].filter(Boolean).join(' '))
      panel.addEventListener('pointerenter', cancel)
      panel.addEventListener('pointerleave', leave)
      listeners = new AbortController()
      const options = { signal: listeners.signal }
      document.addEventListener('scroll', close, { ...options, capture: true, passive: true })
      document.addEventListener('visibilitychange', close, options)
      document.addEventListener('keydown', event => {
        if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() }
      }, { ...options, capture: true })
      window.addEventListener('resize', close, options)
      window.addEventListener('blur', close, options)
    }
    function enter(event: PointerEvent) {
      if (event.pointerType !== 'mouse') return
      hovered = true; cancel(); timer = setTimeout(show, 400)
    }
    function leave() {
      hovered = false; cancel()
      if (!element.matches(':focus-visible')) timer = setTimeout(close, 150)
    }
    function focus() { if (element.matches(':focus-visible')) show() }
    function blur() { if (!hovered) leave() }
    const events = new AbortController(), options = { signal: events.signal }
    element.addEventListener('pointerenter', enter, options)
    element.addEventListener('pointerleave', leave, options)
    element.addEventListener('focusin', focus, options)
    element.addEventListener('focusout', blur, options)
    element.addEventListener('pointerdown', close, options)
    element.addEventListener('click', close, options)
    instances.set(element, {
      update(next) {
        const previous = content()
        value = next
        if (previous?.title !== content()?.title || previous?.text !== content()?.text) close()
      },
      dispose() { close(); events.abort() },
    })
  },
  updated(element, binding) { instances.get(element)?.update(binding.value) },
  beforeUnmount(element) { instances.get(element)?.dispose(); instances.delete(element) },
}
