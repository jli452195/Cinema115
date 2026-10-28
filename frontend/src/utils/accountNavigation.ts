import type { RouteLocationNormalizedLoaded } from 'vue-router'
import {
  IconUserCircle, IconPlayerPlay, IconHeart, IconListCheck, IconUserPlus,
  IconCloud, IconStack2, IconBrandTelegram, IconNotes, IconDeviceTv,
  IconBell, IconLogout, IconShare, IconCloudDownload, IconFolderCog,
  IconFolderShare, IconBooks, IconListSearch, IconArrowsShuffle,
  IconDatabaseCog, IconUsers, IconMovie, IconSearch, IconLink,
  IconLayoutSidebar, IconCalendar, IconTemplate, IconSubtitles,
  IconChevronRight, IconChevronLeft, IconX,
} from '@tabler/icons-vue'
import { managementCategories, resourceSources } from './managementFeatures'

// Official Tabler Icons components (MIT): https://tabler.io/icons
// Named imports keep only the icons used by our navigation in the production bundle.
export const accountIcons = {
  user: IconUserCircle,
  playback: IconPlayerPlay,
  follows: IconHeart,
  anime: IconCalendar,
  naming: IconTemplate,
  subtitles: IconSubtitles,
  management: IconListCheck,
  invite: IconUserPlus,
  drive: IconCloud,
  resources: IconStack2,
  telegram: IconBrandTelegram,
  logs: IconNotes,
  emby: IconDeviceTv,
  bell: IconBell,
  logout: IconLogout,
  cloud: IconCloud,
  share: IconShare,
  download: IconCloudDownload,
  organize: IconFolderCog,
  folder: IconFolderShare,
  library: IconBooks,
  missing: IconListSearch,
  proxy: IconArrowsShuffle,
  cache: IconDatabaseCog,
  users: IconUsers,
  film: IconMovie,
  search: IconSearch,
  link: IconLink,
  sidebar: IconLayoutSidebar,
  chevron: IconChevronRight,
  back: IconChevronLeft,
  close: IconX,
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
    { label: '动漫订阅', to: '/anime', icon: 'anime' },
    { label: '命名模板', to: '/settings/naming', icon: 'naming' },
    { label: '自动整理', to: '/media/organize', icon: 'organize' },
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
