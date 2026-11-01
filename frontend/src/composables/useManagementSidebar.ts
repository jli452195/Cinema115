import { onBeforeUnmount, onMounted, ref } from 'vue'
// Shared across management routes without keeping their business pages mounted.
export const managementNavigationScroll = { top: 0 }
// App.vue initializes this from the confirmed route before rendering.
const collapsed = ref(true)

export function useManagementSidebar() {
  const desktopQuery = window.matchMedia('(min-width: 1024px)')
  const desktop = ref(desktopQuery.matches)
  function updateViewport() { desktop.value = desktopQuery.matches }
  onMounted(() => {
    desktopQuery.addEventListener('change', updateViewport)
  })
  onBeforeUnmount(() => {
    desktopQuery.removeEventListener('change', updateViewport)
  })
  return { desktop, collapsed }
}
