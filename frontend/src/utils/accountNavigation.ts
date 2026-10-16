import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { managementCategories, resourceSources } from './managementFeatures'

export const accountIcons = {
  user: 'M20 21v-2a7 7 0 0 0-14 0v2M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z',
  playback: 'M10 8.5 15.5 12 10 15.5V8.5ZM21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z',
  follows: 'm12 20-8-8a5 5 0 0 1 8-6 5 5 0 0 1 8 6l-8 8Z',
  management: 'M9 6h11M9 12h11M9 18h11M3 6l1 1 2-2M3 12l1 1 2-2M3 18l1 1 2-2',
  invite: 'M15 21v-2a6 6 0 0 0-12 0v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8ZM19 8v6M16 11h6',
  drive: 'M5 4h14l3 12H2L5 4ZM2 16v4h20v-4M17 18h1',
  resources: 'm12 3 9 5-9 5-9-5 9-5ZM3 12l9 5 9-5M3 16l9 5 9-5',
  telegram: 'm21 3-5 18-5-7-8-4 18-7ZM11 14 21 3',
  logs: 'M14 3H5v18h14V8l-5-5ZM14 3v5h5M8 12h8M8 16h5',
  emby: 'M3 4h18v13H3V4ZM8 21h8M12 17v4M10 8l5 2.5-5 2.5V8Z',
  bell: 'M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4',
  logout: 'M9 4H4v16h5M9 12h12m-4-4 4 4-4 4',
  cloud: 'M6 19a5 5 0 0 1-1-9.9A7 7 0 0 1 18.7 8a5.5 5.5 0 0 1-.2 11H6Z',
  share: 'M3 4h18v16H3V4Zm7 4 6 4-6 4V8Z',
  download: 'M12 3v11m0 0 4-4m-4 4-4-4M5 17v4h14v-4',
  organize: 'M4 5h6l2 2h8v12H4V5ZM8 12h8m-4-3v6',
  folder: 'M4 5h6l2 2h8v12H4V5Zm4 8h8m-3-3 3 3-3 3',
  library: 'M4 4h4v16H4V4Zm7 0h4v16h-4V4Zm6 1 3-1 3 15-3 1-3-15Z',
  missing: 'M9 4h11M9 12h11M9 20h11M3 4l1 1 2-2M3 12h3M3 20l1 1 2-2',
  proxy: 'M3 7h15m-3-3 3 3-3 3M21 17H6m3-3-3 3 3 3',
  cache: 'M20 7V3l-3 3a8 8 0 1 0 3 10M20 7h-4M12 8v4l3 2',
  users: 'M15 21v-2a6 6 0 0 0-12 0v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8ZM17 4a4 4 0 0 1 0 8m4 9v-2a6 6 0 0 0-4-5.7',
  film: 'M3 3h18v18H3V3ZM7 3v18M17 3v18M3 8h4M3 16h4M17 8h4M17 16h4',
  search: 'M17 10.5a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0Zm-1 5.5 4.5 4.5',
  link: 'm10 13 4-4M8 16l-1 1a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0m4 2 1-1a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0',
} as const

export interface AccountLink {
  label: string
  to: string
  icon: keyof typeof accountIcons
  children?: AccountLink[]
}

export const adminAccountGroups: { label: string; links: AccountLink[] }[] = [
  { label: '管理', links: [
    { label: '追更管理', to: '/follows/manage', icon: 'management' },
    { label: '用户与邀请', to: '/users', icon: 'users', children: managementCategories.users.features.map(feature => ({ label: feature.title, to: feature.to, icon: feature.menuIcon })) },
    { label: '查看日志', to: '/logs', icon: 'logs' },
  ] },
  { label: '服务', links: [
    { label: '115 网盘', to: '/115', icon: 'drive', children: managementCategories.p115.features.map(feature => ({ label: feature.title, to: feature.to, icon: feature.menuIcon })) },
    { label: '资源配置', to: '/resources', icon: 'resources', children: resourceSources.map(source => ({ label: source.title, to: source.settings, icon: source.menuIcon })) },
    { label: 'Telegram 配置', to: '/telegram', icon: 'telegram' },
    { label: 'Emby', to: '/emby', icon: 'emby', children: managementCategories.emby.features.map(feature => ({ label: feature.title, to: feature.to, icon: feature.menuIcon })) },
  ] },
]

export function isManagementLinkActive(route: RouteLocationNormalizedLoaded, to: string) {
  return route.meta.managementSection
    ? route.meta.managementSection === to
    : route.path === to || route.path.startsWith(`${to}/`)
}

export function isAccountLinkActive(route: RouteLocationNormalizedLoaded, link: AccountLink) {
  return isManagementLinkActive(route, link.to)
    || !!link.children?.some(child => isManagementLinkActive(route, child.to))
}
