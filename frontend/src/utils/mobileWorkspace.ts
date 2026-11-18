import type { RouteLocationNormalizedLoaded } from 'vue-router'

export function mobileWorkspaceSection(name: RouteLocationNormalizedLoaded['name']) {
  switch (name) {
    case 'home':
    case 'movie':
    case 'tv': return 'home'
    case 'my-follows':
    case 'follow-management':
    case 'anime-discovery':
    case 'anime-subscriptions':
    case 'anime-subscription-new':
    case 'anime-subscription-edit': return 'subscriptions'
    case 'updates': return 'updates'
    default: return undefined
  }
}

export type MobileWorkspaceSection = NonNullable<ReturnType<typeof mobileWorkspaceSection>>
