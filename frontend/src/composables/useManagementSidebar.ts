import { onBeforeUnmount, onMounted, ref } from 'vue'
// Shared across management routes without keeping their business pages mounted.
export const managementNavigationScroll = { top: 0 }
const preferenceKey = 'cinema115-management-sidebar'
const collapsed = ref(readCollapsedPreference())

function readCollapsedPreference() {
  try { return localStorage.getItem(preferenceKey) === 'collapsed' }
  catch { return false }
}

function toggleCollapsed() {
  collapsed.value = !collapsed.value
  try { localStorage.setItem(preferenceKey, collapsed.value ? 'collapsed' : 'expanded') }
  catch { /* Navigation still works when browser storage is unavailable. */ }
}

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
  return { desktop, collapsed, toggleCollapsed }
}
