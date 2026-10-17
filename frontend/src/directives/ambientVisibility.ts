import type { ObjectDirective } from 'vue'

// One observer and visibility listener for all cards. No per-card timers or
// scroll handlers; hidden v-show cards are also removed from animation work.
const cards = new Map<HTMLElement, boolean>()
let observer: IntersectionObserver | undefined
function update(element: HTMLElement, intersecting: boolean) {
 cards.set(element, intersecting)
 element.toggleAttribute('data-ambient-running', intersecting && !document.hidden)
}
function onVisibility() { for (const [element, intersecting] of cards) update(element, intersecting) }
export const vAmbientVisibility: ObjectDirective<HTMLElement> = {
 mounted(element) {
  element.setAttribute('data-ambient-host', '')
  if (!cards.size) {
   document.addEventListener('visibilitychange', onVisibility)
   if ('IntersectionObserver' in window) observer = new IntersectionObserver(entries => {
    for (const entry of entries) if (cards.has(entry.target as HTMLElement)) update(entry.target as HTMLElement, entry.isIntersecting)
   })
  }
  update(element, !observer)
  observer?.observe(element)
 },
 unmounted(element) {
  observer?.unobserve(element)
  cards.delete(element)
  if (!cards.size) {
   observer?.disconnect(); observer = undefined
   document.removeEventListener('visibilitychange', onVisibility)
  }
 },
}
