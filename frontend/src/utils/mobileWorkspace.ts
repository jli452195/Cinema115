import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { adminAccountGroups, isAccountLinkActive, type AccountLink } from './accountNavigation'

export function mobileWorkspaceSection(name: RouteLocationNormalizedLoaded['name']) {
  switch (name) {
    case 'home':
    case 'movie':
    case 'tv': return 'home'
    case 'my-follows':
    case 'follow-management': return 'follows'
    case 'updates': return 'updates'
    default: return undefined
  }
}

export type MobileWorkspaceSection = NonNullable<ReturnType<typeof mobileWorkspaceSection>>

export const mobileWorkspaceLabels: Record<MobileWorkspaceSection, string> = { home: '首页', follows: '追更', updates: '更新' }

export const mobileFollowTabs = [
  { name: 'my-follows', label: '我的追更', to: '/follows' },
  { name: 'follow-management', label: '追更管理', to: '/follows/manage' },
] as const

export const mobileWorkspaceToolGroups = adminAccountGroups
  .map(({ label, links }) => ({ label, items: links.filter(link => link.to !== '/follows/manage') }))
  .filter(group => group.items.length > 0)

const mobileWorkspaceTools = mobileWorkspaceToolGroups.flatMap(group => group.items)

export function mobileWorkspaceTool(route: Pick<RouteLocationNormalizedLoaded, 'path' | 'meta'>): AccountLink | undefined {
  // 沿用侧栏的分组规则，涵盖路径不在分类下的子页，例如 /helper115、/gying。
  const target = { path: route.path.toLowerCase(), meta: route.meta }
  return mobileWorkspaceTools.find(tool => isAccountLinkActive(target, tool))
}
