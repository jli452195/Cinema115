import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { adminAccountGroups, isAccountLinkActive, type AccountLink } from './accountNavigation'

export function mobileWorkspaceSection(name: RouteLocationNormalizedLoaded['name']) {
  switch (name) {
    case 'home':
    case 'movie':
    case 'tv': return 'home'
    case 'my-follows':
    case 'follow-management':
    case 'anime-discovery':
    case 'anime-settings':
    case 'anime-subscriptions':
    case 'anime-subscription-new':
    case 'anime-subscription-edit': return 'subscriptions'
    case 'updates': return 'updates'
    default: return undefined
  }
}

export type MobileWorkspaceSection = NonNullable<ReturnType<typeof mobileWorkspaceSection>>

export const mobileWorkspaceLabels: Record<MobileWorkspaceSection, string> = { home: '首页', subscriptions: '订阅', updates: '更新' }

const subscriptionLinks = new Set(['/follows/manage', '/anime'])

export const mobileWorkspaceToolGroups = adminAccountGroups
  .map(({ label, links }) => ({ label, items: links.filter(link => !subscriptionLinks.has(link.to)) }))
  .filter(group => group.items.length > 0)

const mobileWorkspaceTools = mobileWorkspaceToolGroups.flatMap(group => group.items)

export function mobileWorkspaceTool(route: Pick<RouteLocationNormalizedLoaded, 'path' | 'meta'>): AccountLink | undefined {
  // 沿用侧栏的分组规则，涵盖路径不在分类下的子页，例如 /helper115、/gying。
  const target = { path: route.path.toLowerCase(), meta: route.meta }
  return mobileWorkspaceTools.find(tool => isAccountLinkActive(target, tool))
}
