<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, nextTick } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import CinemaLogo from './CinemaLogo.vue'
import ThemeSwitcher from './ThemeSwitcher.vue'
import { isPortalAdmin, portalUser } from '../composables/usePortalAuth'
import { websiteUnread, refreshWebsiteUnread } from '../composables/useWebsiteNotifications'
import { rememberedDashboardPaths } from '../utils/dashboardPreferences'

import { useSlidingIndicator } from '../composables/useSlidingIndicator'

const props = defineProps<{ overBackdrop?: boolean }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const notificationController = new AbortController()
let notificationTimer: ReturnType<typeof setInterval> | undefined
function onScroll() { scrollY.value = window.scrollY }
onMounted(() => {
  window.addEventListener('scroll', onScroll, { passive: true })
  onScroll()
  void refreshWebsiteUnread(notificationController.signal)
  notificationTimer = setInterval(() => { void refreshWebsiteUnread(notificationController.signal) }, 60000)
})
onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll); notificationController.abort(); clearInterval(notificationTimer) })
const header = ref<HTMLElement>()
const accountButton = ref<HTMLButtonElement>()
const accountOpen = ref(false)
const scrollY = ref(0)
const highlightOverBackdrop = computed(() => props.overBackdrop && scrollY.value < 64)
const headerStyle = computed(() => {
  const opacity = Math.min(Math.max(scrollY.value / 100, 0), 1)
  return {
    backgroundColor: `rgb(var(--header-bg) / ${opacity * 0.85})`,
    backdropFilter: `blur(${opacity * 12}px)`,
    borderColor: `rgb(var(--header-border) / ${opacity})`
  }
})
const movieActive = computed(() => route.path.startsWith('/movie') || route.path.startsWith('/collection/'))
const tvActive = computed(() => route.path === '/' || route.path.startsWith('/tv'))
const updatesActive = computed(() => route.path === '/updates')
const managementLinks = computed(() => isPortalAdmin.value ? [
  { label: '追更管理', to: '/follows/manage' },
  { label: '注册邀请', to: '/users' },
  { label: '115 网盘', to: '/115' },
  { label: 'Telegram 配置', to: '/telegram' },
  { label: '观影搜索', to: '/gying' },
  { label: 'RE0 资源搜索', to: '/nanshare' },
  { label: '查看日志', to: '/logs' },
  { label: 'Emby', to: '/emby' },
] : [])
const managementActive = computed(() => managementLinks.value.some(link => route.path.startsWith(link.to)))

const navContainerRef = ref<HTMLElement>()
const { indicatorStyle: navIndicatorStyle } = useSlidingIndicator(
  navContainerRef, 
  '.top-link.selected', 
  [movieActive, tvActive, updatesActive], 
  { persistKey: '__nav_indicator_pos' }
)

function closePanels() { accountOpen.value = false }

function toggleAccount() { accountOpen.value = !accountOpen.value }

function onPointerDown(event: PointerEvent) {
  if (!header.value?.contains(event.target as Node)) closePanels()
}
function onFocusOut(event: FocusEvent) {
  if (!header.value?.contains(event.relatedTarget as Node | null)) closePanels()
}
function onEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !accountOpen.value) return
  event.preventDefault()
  const trigger = accountButton.value
  closePanels()
  trigger?.focus()
}
watch(() => route.fullPath, closePanels)
onMounted(() => { document.addEventListener('pointerdown', onPointerDown); document.addEventListener('keydown', onEscape) })
onBeforeUnmount(() => { document.removeEventListener('pointerdown', onPointerDown); document.removeEventListener('keydown', onEscape) })
</script>

<template>
  <header ref="header" class="workbench-header fixed top-0 z-40 w-full shrink-0 border-b transition-[background-color,backdrop-filter,border-color] duration-200" :class="{ 'over-backdrop': highlightOverBackdrop, 'backdrop-at-top': highlightOverBackdrop && scrollY <= 0 }" :style="headerStyle" @focusout="onFocusOut">
    <div class="header-inner relative mx-auto flex h-[var(--workbench-header-height)] max-w-[1600px] items-center gap-2 px-4 sm:gap-5 sm:px-8 lg:px-10">
      <RouterLink to="/" aria-label="Cinema115 首页" class="brand-link mr-auto shrink-0 rounded-xl outline-none">
        <CinemaLogo />
      </RouterLink>
      <nav ref="navContainerRef" aria-label="影视导航" class="relative flex items-center gap-1 sm:gap-2">
        <div class="nav-indicator absolute inset-y-0 z-0 rounded-[0.6rem] bg-[#f1f5f9] dark:bg-slate-800" :style="navIndicatorStyle"></div>
        <RouterLink :to="rememberedDashboardPaths.movie" :aria-current="movieActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: movieActive }">电影</RouterLink>
        <RouterLink :to="rememberedDashboardPaths.tv" :aria-current="tvActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: tvActive }">剧集</RouterLink>
        <RouterLink to="/updates" :aria-current="updatesActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: updatesActive }">更新</RouterLink>
      </nav>
      <div class="flex items-center gap-1 border-l border-slate-200 dark:border-slate-700 pl-2 sm:gap-3 sm:pl-5">
        <RouterLink to="/notifications" class="icon-button relative" :aria-label="websiteUnread ? `站内通知，${websiteUnread} 条未读` : '站内通知'">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4" /></svg>
          <span v-if="websiteUnread" class="absolute -right-1 -top-1 min-w-4 rounded-full bg-amber-500 px-1 text-center text-[10px] leading-4 text-white">{{ websiteUnread > 99 ? '99+' : websiteUnread }}</span>
        </RouterLink>
        <RouterLink to="/search" class="icon-button" :class="{ selected: route.path === '/search' }" aria-label="搜索影视" :aria-current="route.path === '/search' ? 'page' : undefined">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg>
        </RouterLink>
        <ThemeSwitcher />
        <button ref="accountButton" type="button" class="icon-button" :class="{ selected: managementActive || accountOpen }" aria-label="管理菜单" :aria-expanded="accountOpen" aria-controls="header-management" @click="toggleAccount">
          <svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="9" r="3"/><path d="M5.5 18a6.7 6.7 0 0 1 13 0"/></svg>
        </button>
      </div>
      <nav v-if="accountOpen" id="header-management" aria-label="管理功能" class="absolute right-4 top-full mt-2 w-52 rounded-xl border border-slate-200 bg-white p-2 shadow-xl shadow-slate-900/10 dark:border-slate-700/70 dark:bg-slate-900 dark:shadow-black/25 sm:right-8">
        <p class="px-3 py-2 text-xs text-slate-400 dark:text-slate-400">{{ portalUser?.username }} · {{ isPortalAdmin ? '管理员' : '普通用户' }}</p>
        <RouterLink v-if="!isPortalAdmin" to="/account/playback" class="block rounded-lg px-3 py-2.5 text-sm text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800" @click="closePanels">我的播放</RouterLink>
        <RouterLink to="/follows" class="block rounded-lg px-3 py-2.5 text-sm transition-colors hover:bg-slate-100 dark:hover:bg-slate-800/80" :class="route.name === 'my-follows' ? 'bg-amber-50 text-amber-700 dark:bg-amber-400/10 dark:text-amber-300' : 'text-slate-600 dark:text-slate-300'" :aria-current="route.name === 'my-follows' ? 'page' : undefined" @click="closePanels">我的追更</RouterLink>
        <RouterLink v-for="link in managementLinks" :key="link.to" :to="link.to" :aria-current="route.path.startsWith(link.to) ? 'page' : undefined" class="block rounded-lg px-3 py-2.5 text-sm transition-colors hover:bg-slate-100 dark:hover:bg-slate-800/80" :class="route.path.startsWith(link.to) ? 'bg-amber-50 text-amber-700 dark:bg-amber-400/10 dark:text-amber-300' : 'text-slate-600 dark:text-slate-300'" @click="closePanels">{{ link.label }}</RouterLink>
        <div class="my-1 border-t border-slate-100 dark:border-slate-700/60"></div>
        <button type="button" class="w-full rounded-lg px-3 py-2.5 text-left text-sm text-slate-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:text-slate-400 dark:hover:bg-red-400/10 dark:hover:text-red-300" @click="closePanels(); emit('logout')">退出登录</button>
      </nav>
    </div>
  </header>
</template>


<style>
:root {
  --header-bg: 255 255 255;
  --header-border: var(--color-slate-200);
}
.dark {
  --header-bg: var(--color-slate-900);
  --header-border: var(--color-border);
}
</style>
<style scoped>
.top-link { position: relative; border-radius: 0.6rem; padding: 0.6rem 0.65rem; font-size: 0.875rem; font-weight: 600; color: #334155; white-space: nowrap; transition: color 200ms, background-color 200ms; }
.dark .top-link { color: rgb(var(--color-slate-300)); }
.top-link:hover { background: #f8fafc; color: #0f172a; }
.top-link.selected { color: #0f172a; background: transparent; }
.dark .top-link:hover { color: rgb(var(--color-slate-100)); background: rgb(var(--color-slate-800)); }
.dark .top-link.selected { color: rgb(var(--color-slate-100)); background: transparent; }
.nav-indicator::after { content: ''; position: absolute; height: 3px; bottom: -1px; left: 20%; right: 20%; background: rgb(var(--color-accent)); border-radius: 3px; box-shadow: 0 1px 6px rgb(var(--color-accent) / 40%); }
.icon-button { display: flex; height: 2.25rem; width: 2.25rem; align-items: center; justify-content: center; border-radius: 0.6rem; color: #475569; transition: color 200ms, background-color 200ms; }
.dark .icon-button { color: rgb(var(--color-slate-400)); }
.icon-button:hover { background: #f1f5f9; color: #0f172a; }
.dark .icon-button:hover { background: rgb(var(--color-slate-800)); color: rgb(var(--color-slate-100)); }
.icon-button.selected { background: #fffbeb; color: #b45309; }
.dark .icon-button.selected { background: rgb(var(--color-slate-800)); color: rgb(var(--color-accent)); }
.workbench-header.over-backdrop .top-link,
.workbench-header.over-backdrop .icon-button { color: #fff; }
.workbench-header.backdrop-at-top .top-link { text-shadow: 0 1px 3px rgb(0 0 0 / 65%); }
.workbench-header.backdrop-at-top .icon-button svg { filter: drop-shadow(0 1px 2px rgb(0 0 0 / 65%)); }
.workbench-header.over-backdrop .top-link:hover,
.workbench-header.over-backdrop .top-link.selected,
.workbench-header.over-backdrop .icon-button:hover,
.workbench-header.over-backdrop .icon-button.selected { background: rgb(15 23 42 / 25%); }
.dark .workbench-header.over-backdrop .top-link:hover,
.dark .workbench-header.over-backdrop .top-link.selected,
.dark .workbench-header.over-backdrop .icon-button:hover,
.dark .workbench-header.over-backdrop .icon-button.selected { background: rgb(var(--color-slate-950) / 25%); }
a:focus-visible, button:focus-visible { outline: 2px solid rgb(var(--color-accent)); outline-offset: 3px; }
@media (min-width: 640px) { .top-link { padding-inline: 1.25rem; } }
@media (max-width: 420px) {
  .header-inner { gap: 0.25rem; padding-inline: 0.75rem; }
  .top-link { padding-inline: 0.5rem; }
  .icon-button { width: 2rem; height: 2rem; }
}
</style>
