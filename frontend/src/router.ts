import { createRouter, createWebHistory } from 'vue-router'
import DashboardView from './views/DashboardView.vue'
import SearchView from './views/SearchView.vue'
import CollectionView from './views/CollectionView.vue'
import PersonView from './views/PersonView.vue'
import MediaDetailView from './views/MediaDetailView.vue'
import HomeView from './views/HomeView.vue'
import P115View from './views/P115View.vue'
import ShareView from './views/ShareView.vue'
import CloudDownloadView from './views/CloudDownloadView.vue'
import FolderMonitorView from './views/FolderMonitorView.vue'
import ManualOrganizeView from './views/ManualOrganizeView.vue'
import TelegramView from './views/TelegramView.vue'
import TelegramUserView from './views/TelegramUserView.vue'
import LogsView from './views/LogsView.vue'
import EmbyView from './views/EmbyView.vue'
import EmbyMissingView from './views/EmbyMissingView.vue'
import EmbyProxyView from './views/EmbyProxyView.vue'
import EmbyCacheView from './views/EmbyCacheView.vue'
import EmbyUsersView from './views/EmbyUsersView.vue'
import PortalSettingsView from './views/PortalSettingsView.vue'
import RegisterView from './views/RegisterView.vue'
import LoginView from './views/LoginView.vue'
import AdminLoginView from './views/AdminLoginView.vue'
import PersonalPlaybackView from './views/PersonalPlaybackView.vue'
import MyFollowsView from './views/MyFollowsView.vue'
import NotificationsView from './views/NotificationsView.vue'
import FollowManagementView from './views/FollowManagementView.vue'
import UpdatesView from './views/UpdatesView.vue'
import GyingView from './views/GyingView.vue'
import WoniuView from './views/WoniuView.vue'
import NanShareView from './views/NanShareView.vue'
import ResourceConfigView from './views/ResourceConfigView.vue'
import AnimeDiscoveryView from './views/AnimeCatalogView.vue'
import AnimeSubscriptionEditorView from './views/AnimeSubscriptionEditorView.vue'
import AnimeSubscriptionsView from './views/AnimeSubscriptionsView.vue'
import { animeSubscriptionLocation } from './composables/useAnimeSubscriptionBrowser'
import { animeRememberedQuery } from './composables/useAnimeDiscovery'
import { ensurePortalSession, portalBrowseRoutes, portalToken } from './composables/usePortalAuth'

// 使用路径路由，NAS 由 Go 返回前端入口，直接访问和刷新均可恢复页面。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    { path: '/admin/login', name: 'admin-login', component: AdminLoginView, meta: { public: true } },
    { path: '/account/playback', name: 'personal-playback', component: PersonalPlaybackView },
    { path: '/register', name: 'register', component: RegisterView, meta: { public: true } },
    { path: '/notifications', name: 'notifications', component: NotificationsView },
    { path: '/updates', name: 'updates', component: UpdatesView },
    { path: '/follows/manage', name: 'follow-management', component: FollowManagementView },
    { path: '/anime', name: 'anime-discovery', component: AnimeDiscoveryView, beforeEnter: to => {
      const query = animeRememberedQuery()
      if (!Object.keys(to.query).length && query) return { path: '/anime', query, replace: true }
    } },
    { path: '/anime/new', name: 'anime-subscription-new', component: AnimeSubscriptionEditorView, meta: { managementSection: '/anime' } },
    { path: '/anime/subscriptions/:id/edit', name: 'anime-subscription-edit', component: AnimeSubscriptionEditorView, meta: { managementSection: '/anime' } },
    { path: '/anime/subscriptions/:id?', name: 'anime-subscriptions', component: AnimeSubscriptionsView, meta: { managementSection: '/anime' }, beforeEnter: to => {
      const location = animeSubscriptionLocation()
      if (!to.params.id && !Object.keys(to.query).length && location && (location.path !== to.path || Object.keys(location.query).length)) return { ...location, replace: true }
    } },
    { path: '/follows', name: 'my-follows', component: MyFollowsView },
    { path: '/users', name: 'users-category', component: HomeView, props: { category: 'users' }, meta: { categoryPage: true } },
    { path: '/users/list', name: 'user-management', component: EmbyUsersView },
    { path: '/users/invites', name: 'user-invites', component: PortalSettingsView },
    { path: '/', name: 'home', component: DashboardView, props: { kind: 'tv' } },
    { path: '/tv', name: 'tv', component: DashboardView, props: { kind: 'tv' } },
    { path: '/movie', name: 'movie', component: DashboardView, props: { kind: 'movie' } },
    { path: '/search', name: 'search', component: SearchView },
    { path: '/person/:tmdbId', name: 'person', component: PersonView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/collection/:tmdbId', name: 'collection', component: CollectionView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/tv/:tmdbId', name: 'tv-detail', component: MediaDetailView, props: route => ({ kind: 'tv', tmdbId: String(route.params.tmdbId) }) },
    { path: '/movie/:tmdbId', name: 'movie-detail', component: MediaDetailView, props: route => ({ kind: 'movie', tmdbId: String(route.params.tmdbId) }) },
    { path: '/115', name: 'p115-category', component: HomeView, props: { category: 'p115' }, meta: { categoryPage: true } },
    { path: '/115/pool', name: 'p115-pool', redirect: '/helper115#pool' },
    { path: '/helper115', name: 'helper115', component: P115View },
    { path: '/115/login', redirect: '/helper115' },
    { path: '/115/share', name: 'share', component: ShareView },
    { path: '/115/share/generate', name: 'share-generate', component: ShareView, props: { tab: 'generate' } },
    { path: '/115/share/logs', name: 'share-logs', component: ShareView, props: { tab: 'logs' } },
    { path: '/115/cloud-download', name: 'cloud-download', component: CloudDownloadView },
    { path: '/115/folder-monitor', name: 'folder-monitor', component: FolderMonitorView },
    { path: '/115/manual-organize', name: 'manual-organize', component: ManualOrganizeView },
    { path: '/telegram', name: 'telegram-category', component: HomeView, props: { category: 'telegram' }, meta: { categoryPage: true } },
    { path: '/telegram/bot', name: 'telegram', component: TelegramView },
    { path: '/telegram/user', name: 'telegram-user', component: TelegramUserView, meta: { managementSection: '/resources' } },
    { path: '/resources', name: 'resource-config', component: ResourceConfigView, meta: { managementSection: '/resources' } },
    { path: '/logs', name: 'logs', component: LogsView },
    { path: '/gying', name: 'gying', component: GyingView, meta: { managementSection: '/resources' } },
    { path: '/woniu', name: 'woniu', component: WoniuView, meta: { managementSection: '/resources' } },
    { path: '/nanshare', name: 'nanshare', component: NanShareView, meta: { managementSection: '/resources' } },
    { path: '/emby', name: 'emby-category', component: HomeView, props: { category: 'emby' }, meta: { categoryPage: true } },
    { path: '/emby/library', name: 'emby', component: EmbyView },
    { path: '/emby/missing', name: 'emby-missing', component: EmbyMissingView },
    { path: '/emby/proxy', name: 'emby-proxy', component: EmbyProxyView },
    { path: '/emby/cache', name: 'emby-cache', component: EmbyCacheView },
    { path: '/emby/users', redirect: '/users/list' },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: (to, from, savedPosition) => {
    // The missing view restores its per-view/page scroll after installing cached rows.
    if (to.name === 'emby-missing') return false
    // Anime installs cached rows before restoring each filter/page's scroll.
    if (to.name === 'anime-discovery' || to.name === 'anime-subscriptions') return false
    // Changing only the selected date should not jump the update grid back to the top.
    if (to.name === 'updates' && from.name === 'updates') return false
    return savedPosition ?? (to.path === '/helper115' && to.hash === '#pool' ? { el: '#pool', top: 24 } : { top: 0 })
  },
})

// 兼容旧看板搜索地址，搜索统一进入独立页面。
router.beforeEach(async to => {
  if (to.meta.public) return
  const user = await ensurePortalSession()
  const browsing = portalBrowseRoutes.has(String(to.name))
  if (!portalToken.value) return { path: browsing ? '/login' : '/admin/login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath }, replace: true }
  if (user && user.role !== 'admin' && !browsing) return { path: '/', replace: true }
  if (['/', '/movie', '/tv'].includes(to.path) && typeof to.query.q === 'string' && to.query.q.trim()) {
    return { path: '/search', query: { q: to.query.q.trim(), scope: to.path === '/movie' ? 'movie' : 'tv' }, replace: true }
  }
})

// 只允许返回已知的站内业务页面，登录、注册及外部地址不能作为跳转目标。
export function loginReturnPath(value: unknown) {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return '/'
  const target = router.resolve(value)
  if (!target.name || target.meta.public || target.matched.some(record => record.redirect)) return '/'
  return target.fullPath
}
