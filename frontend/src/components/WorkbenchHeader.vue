<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import CinemaLogo from './CinemaLogo.vue'
import ThemeSwitcher from './ThemeSwitcher.vue'
import HeaderAccountMenu from './HeaderAccountMenu.vue'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { websiteUnread, refreshWebsiteUnread, useWebsiteNotificationSync } from '../composables/useWebsiteNotifications'
import { rememberedDashboardPaths } from '../utils/dashboardPreferences'
import { adminAccountGroups, isAccountLinkActive } from '../utils/accountNavigation'

import { useSlidingIndicator } from '../composables/useSlidingIndicator'

const props = defineProps<{ overBackdrop?: boolean; tonalSurface?: boolean }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const notificationController = new AbortController()
useWebsiteNotificationSync(() => { void refreshWebsiteUnread(notificationController.signal) })
let notificationTimer: ReturnType<typeof setInterval> | undefined
let scrollFrame: number | undefined
let compactViewport: MediaQueryList | undefined
function smoothstep(value: number) { return value * value * (3 - 2 * value) }
function updateHeaderScroll() {
  if (!props.tonalSurface) {
    scrollY.value = window.scrollY
    return
  }
  const threshold = compactViewport?.matches ? 64 : 96
  const raw = props.overBackdrop ? Math.min(Math.max(window.scrollY / threshold, 0), 1) : 1
  const progress = smoothstep(raw)
  const edge = smoothstep(Math.min(Math.max((raw - 0.55) / 0.45, 0), 1))
  // Scroll position is the animation clock: no time-based transition or trailing interpolation.
  header.value?.style.setProperty('--header-surface-progress', progress.toFixed(4))
  header.value?.style.setProperty('--header-edge-progress', edge.toFixed(4))
  header.value?.style.setProperty('--header-active-opacity', (0.45 + progress * 0.55).toFixed(4))
}
function onScroll() {
  if (!props.tonalSurface) {
    scrollY.value = window.scrollY
    return
  }
  if (scrollFrame !== undefined) return
  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = undefined
    updateHeaderScroll()
  })
}
onMounted(() => {
  compactViewport = window.matchMedia('(max-width: 639px)')
  compactViewport.addEventListener('change', onScroll)
  window.addEventListener('scroll', onScroll, { passive: true })
  updateHeaderScroll()
  void refreshWebsiteUnread(notificationController.signal)
  notificationTimer = setInterval(() => { void refreshWebsiteUnread(notificationController.signal) }, 60000)
})
onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
  compactViewport?.removeEventListener('change', onScroll)
  if (scrollFrame !== undefined) cancelAnimationFrame(scrollFrame)
  notificationController.abort()
  clearInterval(notificationTimer)
})
const header = ref<HTMLElement>()
const accountButton = ref<HTMLButtonElement>()
const accountOpen = ref(false)
const scrollY = ref(0)
watch(() => [props.tonalSurface, props.overBackdrop], onScroll, { flush: 'post' })
const highlightOverBackdrop = computed(() => props.overBackdrop && !props.tonalSurface && scrollY.value < 64)
const headerStyle = computed(() => {
  if (props.tonalSurface) return {}
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
const managementActive = computed(() => isPortalAdmin.value && adminAccountGroups.some(group =>
  group.links.some(link => isAccountLinkActive(route, link))))

const navContainerRef = ref<HTMLElement>()
const { indicatorStyle: navIndicatorStyle } = useSlidingIndicator(
  navContainerRef, 
  '.top-link.selected', 
  [movieActive, tvActive, updatesActive], 
  { persistKey: '__nav_indicator_pos' }
)

function closePanels() { accountOpen.value = false }

function toggleAccount() { accountOpen.value = !accountOpen.value }

function isWithinAccount(target: EventTarget | null) {
  return target instanceof Node && (accountButton.value?.contains(target) || header.value?.querySelector('#header-management')?.contains(target))
}
function onPointerDown(event: PointerEvent) {
  if (!isWithinAccount(event.target)) closePanels()
}
function onFocusOut(event: FocusEvent) {
  if (isWithinAccount(event.relatedTarget)) return
  // A child panel may move focus after Vue reveals its parent in the same update.
  void nextTick(() => { if (!isWithinAccount(document.activeElement)) closePanels() })
}
function activateAccountMenu(element: Element) {
  element.removeAttribute('inert')
  element.removeAttribute('aria-hidden')
}
function deactivateAccountMenu(element: Element) {
  element.setAttribute('inert', '')
  element.setAttribute('aria-hidden', 'true')
}
function onEscape(event: KeyboardEvent) {
  if (event.defaultPrevented || event.key !== 'Escape' || !accountOpen.value) return
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
  <header ref="header" class="workbench-header fixed top-0 z-40 w-full shrink-0 border-b transition-[border-color] duration-200" :class="{ 'over-backdrop': highlightOverBackdrop, 'backdrop-at-top': highlightOverBackdrop && scrollY <= 0, 'tonal-surface': tonalSurface, 'immersive-surface': tonalSurface && overBackdrop }" :style="tonalSurface ? undefined : { borderColor: headerStyle.borderColor }" @focusout="onFocusOut">
    <span v-if="tonalSurface" class="header-surface pointer-events-none absolute inset-0" aria-hidden="true"></span>
    <span v-else class="header-background pointer-events-none absolute inset-0 transition-[background-color,backdrop-filter] duration-200" :style="headerStyle" aria-hidden="true"></span>
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
        <RouterLink to="/notifications" class="icon-button desktop-action relative" :aria-label="websiteUnread ? `站内通知，${websiteUnread} 条未读` : '站内通知'">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4" /></svg>
          <span v-if="websiteUnread" class="absolute -right-1 -top-1 min-w-4 rounded-full bg-amber-500 px-1 text-center text-[10px] leading-4 text-white">{{ websiteUnread > 99 ? '99+' : websiteUnread }}</span>
        </RouterLink>
        <RouterLink to="/search" class="icon-button" :class="{ selected: route.path === '/search' }" aria-label="搜索影视" :aria-current="route.path === '/search' ? 'page' : undefined">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg>
        </RouterLink>
        <div class="hidden sm:block"><ThemeSwitcher /></div>
        <button ref="accountButton" type="button" class="icon-button relative" :class="{ selected: managementActive || accountOpen }" :aria-label="websiteUnread ? `更多功能，${websiteUnread} 条未读通知` : '更多功能'" :aria-expanded="accountOpen" aria-controls="header-management" @click="toggleAccount">
          <svg class="h-5 w-5 sm:hidden" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/></svg>
          <svg class="hidden h-6 w-6 sm:block" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="9" r="3"/><path d="M5.5 18a6.7 6.7 0 0 1 13 0"/></svg>
          <span v-if="websiteUnread" class="absolute right-0 top-0 h-2 w-2 rounded-full bg-amber-500 sm:hidden" aria-hidden="true"></span>
        </button>
      </div>
      <Transition name="account-menu-motion" @before-enter="activateAccountMenu" @before-leave="deactivateAccountMenu">
        <HeaderAccountMenu v-if="accountOpen" @close="closePanels" @logout="emit('logout')" />
      </Transition>
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
.workbench-header.tonal-surface {
  --header-tonal-bg: 248 250 252;
  --header-tonal-alpha: 0.9;
  --header-surface-progress: 1;
  --header-edge-progress: 1;
  --header-active-opacity: 1;
  background: transparent;
  border-color: transparent;
  transition: none;
}
.dark .workbench-header.tonal-surface {
  --header-tonal-bg: 24 25 28;
  --header-tonal-alpha: 0.88;
}
.workbench-header.immersive-surface {
  --header-surface-progress: 0;
  --header-edge-progress: 0;
  --header-active-opacity: 0.45;
}
.header-surface {
  background: rgb(var(--header-tonal-bg) / var(--header-tonal-alpha));
  -webkit-backdrop-filter: blur(16px);
  backdrop-filter: blur(16px);
  opacity: var(--header-surface-progress);
}
.workbench-header.tonal-surface::after {
  content: '';
  position: absolute;
  inset: auto 0 -1px;
  height: 1px;
  background: color-mix(in srgb, var(--md-sys-color-outline-variant) 60%, transparent);
  opacity: var(--header-edge-progress);
  pointer-events: none;
}
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
.workbench-header.over-backdrop .nav-indicator { background: rgb(255 255 255 / 8%); }
.workbench-header.over-backdrop .nav-indicator::after { height: 2px; opacity: 0.75; box-shadow: none; }
.workbench-header.backdrop-at-top .top-link { text-shadow: 0 1px 3px rgb(0 0 0 / 65%); }
.workbench-header.backdrop-at-top .icon-button svg { filter: drop-shadow(0 1px 2px rgb(0 0 0 / 65%)); }
.workbench-header.over-backdrop .top-link:hover,
.workbench-header.over-backdrop .icon-button:hover,
.workbench-header.over-backdrop .icon-button.selected { background: rgb(15 23 42 / 25%); }
.dark .workbench-header.over-backdrop .top-link:hover,
.dark .workbench-header.over-backdrop .icon-button:hover,
.dark .workbench-header.over-backdrop .icon-button.selected { background: rgb(var(--color-slate-950) / 25%); }
.workbench-header.over-backdrop .top-link.selected { background: transparent; }
.workbench-header.tonal-surface .top-link,
.workbench-header.tonal-surface .icon-button { color: var(--md-sys-color-on-surface-variant); }
.workbench-header.tonal-surface .top-link.selected,
.workbench-header.tonal-surface .top-link:hover,
.workbench-header.tonal-surface .icon-button:hover,
.workbench-header.tonal-surface .icon-button.selected { color: var(--md-sys-color-on-surface); }
.workbench-header.tonal-surface .nav-indicator {
  background: color-mix(in srgb, var(--md-sys-color-secondary-container) calc(var(--header-active-opacity) * 100%), transparent);
}
.workbench-header.tonal-surface .top-link:hover,
.workbench-header.tonal-surface .icon-button:hover,
.workbench-header.tonal-surface .icon-button.selected { background: var(--md-sys-color-secondary-container); }
.workbench-header.tonal-surface .top-link.selected { color: var(--md-sys-color-on-secondary-container); }
.workbench-header.tonal-surface .nav-indicator::after { background: rgb(var(--color-accent)); height: 3px; opacity: 1; box-shadow: none; }
.workbench-header.tonal-surface .top-link { text-shadow: none; }
.workbench-header.tonal-surface .icon-button svg { filter: none; }
.workbench-header.tonal-surface :is(a, button):focus-visible { outline-color: rgb(var(--color-accent)); }
a:focus-visible, button:focus-visible { outline: 2px solid rgb(var(--color-accent)); outline-offset: 3px; }
.account-menu-motion-enter-active, .account-menu-motion-leave-active { transition: transform 140ms ease; }
.account-menu-motion-enter-active :deep(.account-surface), .account-menu-motion-leave-active :deep(.account-surface) { transition: opacity 140ms ease; }
.account-menu-motion-enter-from, .account-menu-motion-leave-to { transform: translateY(-4px); }
.account-menu-motion-enter-from :deep(.account-surface), .account-menu-motion-leave-to :deep(.account-surface) { opacity: 0; }
.account-menu-motion-leave-active { pointer-events: none; }
@media (prefers-reduced-motion: reduce) {
  .account-menu-motion-enter-active, .account-menu-motion-leave-active { transition: none; }
  .account-menu-motion-enter-active :deep(.account-surface), .account-menu-motion-leave-active :deep(.account-surface) { transition: none; }
  .account-menu-motion-enter-from, .account-menu-motion-leave-to { transform: none; }
  .workbench-header.tonal-surface,
  .dark .workbench-header.tonal-surface { --header-tonal-alpha: 0.95; }
  .header-surface { -webkit-backdrop-filter: none; backdrop-filter: none; }
  .workbench-header.tonal-surface :is(.top-link, .icon-button) { transition: none; }
}
@media (min-width: 640px) { .top-link { padding-inline: 1.25rem; } }
@media (max-width: 639px) {
  .desktop-action { display: none; }
}
@media (max-width: 420px) {
  .header-inner { gap: 0.25rem; padding-inline: 0.75rem; }
  .top-link { padding-inline: 0.5rem; }
  .icon-button { width: 2rem; height: 2rem; }
}
</style>
