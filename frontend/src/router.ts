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
import TelegramView from './views/TelegramView.vue'
import TelegramUserView from './views/TelegramUserView.vue'
import LogsView from './views/LogsView.vue'
import EmbyView from './views/EmbyView.vue'
import WebsiteUsersView from './views/WebsiteUsersView.vue'
import MyFollowsView from './views/MyFollowsView.vue'
import NotificationsView from './views/NotificationsView.vue'
import FollowManagementView from './views/FollowManagementView.vue'
import UpdatesView from './views/UpdatesView.vue'
import GyingView from './views/GyingView.vue'
import { ensureWebsiteSession, websiteBrowseRoutes } from './composables/useWebsiteAuth'

// 使用路径路由，NAS 由 Go 返回前端入口，直接访问和刷新均可恢复页面。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/notifications', name: 'notifications', component: NotificationsView },
    { path: '/updates', name: 'updates', component: UpdatesView },
    { path: '/follows/manage', name: 'follow-management', component: FollowManagementView },
    { path: '/follows', name: 'my-follows', component: MyFollowsView },
    { path: '/users', name: 'users', component: WebsiteUsersView },
    { path: '/', name: 'home', component: DashboardView, props: { kind: 'tv' } },
    { path: '/tv', name: 'tv', component: DashboardView, props: { kind: 'tv' } },
    { path: '/movie', name: 'movie', component: DashboardView, props: { kind: 'movie' } },
    { path: '/search', name: 'search', component: SearchView },
    { path: '/person/:tmdbId', name: 'person', component: PersonView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/collection/:tmdbId', name: 'collection', component: CollectionView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/tv/:tmdbId', name: 'tv-detail', component: MediaDetailView, props: route => ({ kind: 'tv', tmdbId: String(route.params.tmdbId) }) },
    { path: '/movie/:tmdbId', name: 'movie-detail', component: MediaDetailView, props: route => ({ kind: 'movie', tmdbId: String(route.params.tmdbId) }) },
    { path: '/115', name: 'p115-category', component: HomeView, props: { category: 'p115' }, meta: { categoryPage: true } },
    { path: '/helper115', name: 'helper115', component: P115View },
    { path: '/115/login', redirect: '/helper115' },
    { path: '/115/share', name: 'share', component: ShareView },
    { path: '/115/cloud-download', name: 'cloud-download', component: CloudDownloadView },
    { path: '/telegram', name: 'telegram-category', component: HomeView, props: { category: 'telegram' }, meta: { categoryPage: true } },
    { path: '/telegram/bot', name: 'telegram', component: TelegramView },
    { path: '/telegram/user', name: 'telegram-user', component: TelegramUserView },
    { path: '/logs', name: 'logs', component: LogsView },
    { path: '/gying', name: 'gying', component: GyingView },
    { path: '/emby', name: 'emby-category', component: HomeView, props: { category: 'emby' }, meta: { categoryPage: true } },
    { path: '/emby/library', name: 'emby', component: EmbyView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: (_to, _from, savedPosition) => savedPosition ?? { top: 0 },
})

// 兼容旧看板搜索地址，搜索统一进入独立页面。
router.beforeEach(async to => {
  const user = await ensureWebsiteSession()
  const browsing = websiteBrowseRoutes.has(String(to.name))
  if (user && user.role !== 'admin' && !browsing) return { path: '/', replace: true }
  if (['/', '/movie', '/tv'].includes(to.path) && typeof to.query.q === 'string' && to.query.q.trim()) {
    return { path: '/search', query: { q: to.query.q.trim(), scope: to.path === '/movie' ? 'movie' : 'tv' }, replace: true }
  }
})
