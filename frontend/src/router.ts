import { createRouter, createWebHistory } from 'vue-router'
import DashboardView from './views/DashboardView.vue'
import SearchView from './views/SearchView.vue'
import CollectionView from './views/CollectionView.vue'
import PersonView from './views/PersonView.vue'
import MediaDetailView from './views/MediaDetailView.vue'
import HomeView from './views/HomeView.vue'
import P115View from './views/P115View.vue'
import ShareView from './views/ShareView.vue'
import TelegramView from './views/TelegramView.vue'
import LogsView from './views/LogsView.vue'
import EmbyView from './views/EmbyView.vue'

// 使用路径路由，NAS 由 Go 返回前端入口，直接访问和刷新均可恢复页面。
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: DashboardView, props: { kind: 'tv' } },
    { path: '/tv', name: 'tv', component: DashboardView, props: { kind: 'tv' } },
    { path: '/movie', name: 'movie', component: DashboardView, props: { kind: 'movie' } },
    { path: '/search', name: 'search', component: SearchView },
    { path: '/person/:tmdbId', name: 'person', component: PersonView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/collection/:tmdbId', name: 'collection', component: CollectionView, props: route => ({ tmdbId: String(route.params.tmdbId) }) },
    { path: '/tv/:tmdbId', name: 'tv-detail', component: MediaDetailView, props: route => ({ kind: 'tv', tmdbId: String(route.params.tmdbId) }) },
    { path: '/movie/:tmdbId', name: 'movie-detail', component: MediaDetailView, props: route => ({ kind: 'movie', tmdbId: String(route.params.tmdbId) }) },
    { path: '/115', name: 'p115-category', component: HomeView, props: { category: 'p115' }, meta: { categoryPage: true } },
    { path: '/115/login', name: 'p115', component: P115View },
    { path: '/115/share', name: 'share', component: ShareView },
    { path: '/telegram', name: 'telegram-category', component: HomeView, props: { category: 'telegram' }, meta: { categoryPage: true } },
    { path: '/telegram/bot', name: 'telegram', component: TelegramView },
    { path: '/logs', name: 'logs', component: LogsView },
    { path: '/emby', name: 'emby-category', component: HomeView, props: { category: 'emby' }, meta: { categoryPage: true } },
    { path: '/emby/library', name: 'emby', component: EmbyView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: (_to, _from, savedPosition) => savedPosition ?? { top: 0 },
})

// 兼容旧看板搜索地址，搜索统一进入独立页面。
router.beforeEach(to => {
  if (['/', '/movie', '/tv'].includes(to.path) && typeof to.query.q === 'string' && to.query.q.trim()) {
    return { path: '/search', query: { q: to.query.q.trim(), scope: to.path === '/movie' ? 'movie' : 'tv' }, replace: true }
  }
})
