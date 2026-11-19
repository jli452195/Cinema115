import type { RouteLocationNormalized } from 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    defaultSidebarCollapsed?: boolean
    mobileOverlay?: true | ((route: RouteLocationNormalized) => boolean)
  }
}
