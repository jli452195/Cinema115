<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import CinemaLogo from './CinemaLogo.vue'
import ThemeSwitcher from './ThemeSwitcher.vue'
import { vTooltip } from '../directives/tooltip'
import { isPortalAdmin } from '../composables/usePortalAuth'
import { openNetworkConnectionTest } from '../composables/useNetworkConnectionTest'
import HeaderAccountMenu from './HeaderAccountMenu.vue'
import HistoryBackButton from './HistoryBackButton.vue'
import ManagementSidebar from './ManagementSidebar.vue'
import { websiteUnread, refreshWebsiteUnread, useWebsiteNotificationSync } from '../composables/useWebsiteNotifications'
import { rememberedDashboardPaths } from '../utils/dashboardPreferences'

import { useSlidingIndicator } from '../composables/useSlidingIndicator'
import { useManagementSidebar } from '../composables/useManagementSidebar'
import { useScrollBoundary } from '../composables/useScrollBoundary'
import { useAnchoredMenuMotion } from '../composables/useAnchoredMenuMotion'

const props = defineProps<{
  overBackdrop?: boolean; tonalSurface?: boolean; sidebarLayout?: boolean; managedSidebar?: boolean; sidebarOpen?: boolean; sidebarControls?: string
  detailNavigation?: { title: string; year: string; fallback: string; titleElement: HTMLElement | null }
}>()
const emit = defineEmits<{ logout: []; toggleSidebar: [event: MouseEvent] }>()
const { desktop } = useManagementSidebar()
const desktopSidebar = computed(() => props.sidebarLayout || !props.managedSidebar && desktop.value)
const navigationSidebar = ref<InstanceType<typeof ManagementSidebar>>()
const navigationOpen = computed(() => props.managedSidebar ? !!props.sidebarOpen : !!navigationSidebar.value?.opened)
const navigationControls = computed(() => props.managedSidebar ? props.sidebarControls : navigationSidebar.value?.drawerID)
function toggleNavigation(event: MouseEvent) {
  closePanels()
  if (props.managedSidebar) emit('toggleSidebar', event)
  else navigationSidebar.value?.toggleDrawer(event)
}
const route = useRoute()
const notificationController = new AbortController()
useWebsiteNotificationSync(() => { void refreshWebsiteUnread(notificationController.signal) })
const standaloneMedia = window.matchMedia('(display-mode: standalone)')
const isStandalone = ref(standaloneMedia.matches)
let notificationTimer: ReturnType<typeof setInterval> | undefined
let scrollFrame: number | undefined
const compactViewport = window.matchMedia('(max-width: 639px)')
const compact = ref(compactViewport.matches)
const mobileDetail = computed(() => compact.value && !!props.detailNavigation)
function smoothstep(value: number) { return value * value * (3 - 2 * value) }
function updateHeaderScroll() {
  if (mobileDetail.value) return
  // 已安装的 PWA 使用与系统栏相同的实色，不随海报和滚动改变透明度。
  if (isStandalone.value) {
    header.value?.style.setProperty('--header-surface-progress', '1')
    header.value?.style.setProperty('--header-edge-progress', '1')
    header.value?.style.setProperty('--header-active-opacity', '1')
    return
  }
  if (!props.tonalSurface) {
    scrollY.value = window.scrollY
    return
  }
  const threshold = compactViewport.matches ? 64 : 96
  const raw = props.overBackdrop ? Math.min(Math.max(window.scrollY / threshold, 0), 1) : 1
  const progress = smoothstep(raw)
  const edge = smoothstep(Math.min(Math.max((raw - 0.55) / 0.45, 0), 1))
  // 滚动位置直接控制透明度，不叠加延时过渡或逐帧改变模糊半径。
  header.value?.style.setProperty('--header-surface-progress', progress.toFixed(4))
  header.value?.style.setProperty('--header-edge-progress', edge.toFixed(4))
  header.value?.style.setProperty('--header-active-opacity', (0.45 + progress * 0.55).toFixed(4))
}
function onScroll() {
  if (isStandalone.value || mobileDetail.value) return
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
function onStandaloneChange() {
  isStandalone.value = standaloneMedia.matches
  updateHeaderScroll()
}
function onCompactChange() {
  compact.value = compactViewport.matches
  accountMenuMotion.finish()
  closePanels()
  void nextTick(() => accountMenuMotion.finish())
  onScroll()
}
function onAccountLayoutChange() {
  if (leavingAccountElement) accountMenuMotion.finish(leavingAccountElement)
  if (accountOpen.value) syncAccountMenuAnchor()
}
onMounted(() => {
  compactViewport.addEventListener('change', onCompactChange)
  standaloneMedia.addEventListener('change', onStandaloneChange)
  window.addEventListener('scroll', onScroll, { passive: true })
  window.addEventListener('resize', onAccountLayoutChange, { passive: true })
  window.visualViewport?.addEventListener('resize', onAccountLayoutChange, { passive: true })
  updateHeaderScroll()
  void refreshWebsiteUnread(notificationController.signal)
  notificationTimer = setInterval(() => { void refreshWebsiteUnread(notificationController.signal) }, 60000)
})
onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
  window.removeEventListener('resize', onAccountLayoutChange)
  window.visualViewport?.removeEventListener('resize', onAccountLayoutChange)
  compactViewport.removeEventListener('change', onCompactChange)
  standaloneMedia.removeEventListener('change', onStandaloneChange)
  if (scrollFrame !== undefined) cancelAnimationFrame(scrollFrame)
  notificationController.abort()
  clearInterval(notificationTimer)
})
const header = ref<HTMLElement>()
const detailTitleVisible = useScrollBoundary(() => props.detailNavigation?.titleElement, header, mobileDetail)
const accountButton = ref<HTMLButtonElement>()
const accountOpen = ref(false)
const closingForTheme = ref(false)
function accountMenuOrigin() {
  return mobileDetail.value ? accountButton.value?.closest<HTMLElement>('.header-actions') || accountButton.value : accountButton.value
}
const accountMenuMotion = useAnchoredMenuMotion({
  origin: accountMenuOrigin,
  compact: () => compactViewport.matches,
  immediate: () => closingForTheme.value,
})
const accountMenuSizes = new WeakMap<Element, string>()
let accountOpenedAt = 0
let accountLeave: Promise<void> | undefined
let finishAccountLeave: (() => void) | undefined
let leavingAccountElement: Element | undefined
const scrollY = ref(0)
watch(() => [props.tonalSurface, props.overBackdrop, mobileDetail.value], onScroll, { flush: 'post' })
const highlightOverBackdrop = computed(() => !isStandalone.value && props.overBackdrop && !props.tonalSurface && scrollY.value < 64)
const headerStyle = computed(() => {
  if (props.tonalSurface) return {}
  if (isStandalone.value) return {
    backgroundColor: 'rgb(var(--header-bg))',
    backdropFilter: 'none',
    opacity: 1,
    borderColor: 'rgb(var(--header-border))',
  }
  const opacity = Math.min(Math.max(scrollY.value / 100, 0), 1)
  return {
    opacity,
    borderColor: `rgb(var(--header-border) / ${opacity})`
  }
})
const movieActive = computed(() => route.path.startsWith('/movie') || route.path.startsWith('/collection/'))
const tvActive = computed(() => route.path === '/' || route.path.startsWith('/tv'))
const updatesActive = computed(() => route.path === '/updates')
const animeActive = computed(() => route.path === '/anime' || route.path.startsWith('/anime/'))
const showAnimeShortcut = computed(() => compact.value && isPortalAdmin.value &&
  (route.name === 'home' || route.name === 'movie' || route.name === 'tv' || updatesActive.value || animeActive.value))

const navContainerRef = ref<HTMLElement>()
const { indicatorStyle: navIndicatorStyle } = useSlidingIndicator(
  navContainerRef, 
  '.top-link.selected', 
  [movieActive, tvActive, updatesActive, animeActive, showAnimeShortcut],
  { persistKey: '__nav_indicator_pos' }
)

function closePanels() { accountOpen.value = false }

async function prepareThemeChange() {
  const restoreFocus = !!header.value?.querySelector('#header-management')?.contains(document.activeElement)
  closingForTheme.value = true
  closePanels()
  try {
    await nextTick()
    // 已开始的退场也主动收尾，主题快照不等待或包含半展开的菜单。
    accountMenuMotion.finish()
    // 也等待已经开始的退场，防止菜单残留被收进主题快照。
    await accountLeave
    if (restoreFocus) accountButton.value?.focus({ preventScroll: true })
  } finally {
    closingForTheme.value = false
  }
}

function completeAccountLeave(element?: Element) {
  if (element) accountMenuMotion.release(element)
  if (element && element !== leavingAccountElement) return
  finishAccountLeave?.()
  finishAccountLeave = undefined
  accountLeave = undefined
  leavingAccountElement = undefined
}
onBeforeUnmount(completeAccountLeave)

function toggleAccount(event: MouseEvent) {
  accountOpen.value = !accountOpen.value
  // 先安排本次更新，再交接仍连接在页面上的旧窗口，快照在这次更新后失效。
  if (accountOpen.value && leavingAccountElement) accountMenuMotion.release(leavingAccountElement)
  if (accountOpen.value && compactViewport.matches && event.isTrusted && event.detail === 0) {
    void nextTick(() => header.value?.querySelector<HTMLElement>('#header-management [data-menu-content] a, #header-management [data-menu-content] button')?.focus({ preventScroll: true }))
  }
}

function isWithinAccount(target: EventTarget | null) {
  return target instanceof Node && (accountButton.value?.contains(target) || header.value?.querySelector('#header-management')?.contains(target))
}
function onPointerDown(event: PointerEvent) {
  if (accountOpen.value && compactViewport.matches && performance.now() - accountOpenedAt < 300) {
    const source = accountMenuOrigin()?.getBoundingClientRect()
    if (source && event.clientX >= source.left && event.clientX <= source.right && event.clientY >= source.top && event.clientY <= source.bottom) {
      event.preventDefault()
      closePanels()
      return
    }
  }
  if (!isWithinAccount(event.target)) closePanels()
}
function onFocusOut(event: FocusEvent) {
  if (isWithinAccount(event.relatedTarget)) return
  // 子面板可能在同一次更新中挂载后才接收焦点。
  void nextTick(() => { if (!isWithinAccount(document.activeElement)) closePanels() })
}
function activateAccountMenu(element: Element) {
  accountOpenedAt = performance.now()
  syncAccountMenuAnchor(element)
  element.removeAttribute('inert')
  element.removeAttribute('aria-hidden')
}
function enterAccountMenu(element: Element, done: () => void) {
  syncAccountMenuAnchor(element)
  accountMenuMotion.enter(element, done)
}
function deactivateAccountMenu(element: Element) {
  completeAccountLeave()
  leavingAccountElement = element
  accountLeave = new Promise(resolve => { finishAccountLeave = resolve })
  element.setAttribute('inert', '')
  element.setAttribute('aria-hidden', 'true')
}
function syncAccountMenuAnchor(element?: Element) {
  const menu = (element || header.value?.querySelector('#header-management')) as HTMLElement | null
  const button = accountMenuOrigin()
  const container = header.value?.querySelector<HTMLElement>('.header-inner')
  if (!menu || !button || !container) return
  if (!compactViewport.matches) {
    if (accountMenuSizes.get(menu)?.startsWith('compact,')) accountMenuMotion.finish(menu)
    accountMenuSizes.set(menu, 'desktop')
    return
  }
  // 工具栏已经避开安全区，相对坐标不再重复加 safe-top。
  const trigger = button.getBoundingClientRect(), box = container.getBoundingClientRect()
  // 保留原生的上移方向，同时为本站较矮的工具栏留出安全区内 12px 顶距。
  const top = Math.max(box.top + 12, trigger.top - 8)
  menu.style.setProperty('--menu-top', `${top - box.top}px`)
  menu.style.setProperty('--menu-right', `${box.right - trigger.right}px`)
  const viewport = window.visualViewport
  const bottom = (viewport?.offsetTop || 0) + (viewport?.height || window.innerHeight)
  menu.style.setProperty('--menu-available-height', `${Math.max(0, bottom - top)}px`)
  if (!menu.isConnected) return
  const size = `compact,${trigger.top},${trigger.right},${menu.offsetWidth},${menu.offsetHeight}`
  // 动画中旋转屏幕或调整安全区时直接落在新布局，避免保留旧的像素边界。
  if (accountMenuSizes.has(menu) && accountMenuSizes.get(menu) !== size) accountMenuMotion.finish(menu)
  accountMenuSizes.set(menu, size)
}
watch(accountOpen, (open, _, onCleanup) => {
  if (!open || !header.value) return
  const update = () => syncAccountMenuAnchor()
  const resize = new ResizeObserver(update)
  resize.observe(header.value, { box: 'border-box' })
  const inner = header.value.querySelector('.header-inner')
  if (inner) resize.observe(inner)
  onCleanup(() => {
    resize.disconnect()
  })
}, { flush: 'post' })
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
  <header ref="header" class="workbench-header fixed top-0 z-40 w-full shrink-0 border-b transition-[border-color] duration-200" :class="{ 'over-backdrop': highlightOverBackdrop, 'backdrop-at-top': highlightOverBackdrop && scrollY <= 0, 'tonal-surface': tonalSurface, 'immersive-surface': !isStandalone && tonalSurface && overBackdrop, 'with-management-sidebar': desktopSidebar, 'mobile-detail': mobileDetail }" :style="tonalSurface ? undefined : { borderColor: headerStyle.borderColor }" @focusout="onFocusOut">
    <span v-if="tonalSurface" class="header-surface pointer-events-none absolute inset-0" aria-hidden="true"></span>
    <span v-else class="header-background pointer-events-none absolute inset-0" :style="headerStyle" aria-hidden="true"></span>
    <div class="header-inner relative flex h-[var(--workbench-toolbar-height)] items-center gap-2 px-4 sm:gap-5 sm:px-6">
      <HistoryBackButton v-if="mobileDetail && detailNavigation" :fallback="detailNavigation.fallback" icon-only class="detail-nav-back glass-pill-surface" />
      <div v-if="mobileDetail && detailNavigation" class="detail-nav-title-slot" aria-hidden="true">
        <div class="detail-nav-title glass-pill-surface" :class="{ 'is-visible': detailTitleVisible }">
          <span class="detail-nav-name">{{ detailNavigation.title }}</span><span v-if="detailNavigation.year" class="detail-nav-year">{{ detailNavigation.year }}</span>
        </div>
      </div>
      <button v-if="!desktopSidebar && !mobileDetail" type="button" :aria-label="navigationOpen ? '关闭侧栏' : '打开侧栏'" v-tooltip="navigationOpen ? '关闭侧栏' : '打开侧栏'" aria-haspopup="dialog" :aria-expanded="navigationOpen" :aria-controls="navigationControls" class="brand-link mr-auto inline-flex min-h-11 shrink-0 items-center rounded-xl outline-none" @click="toggleNavigation">
        <CinemaLogo />
      </button>
      <nav v-if="!mobileDetail" ref="navContainerRef" aria-label="影视导航" class="relative flex items-center gap-1 sm:gap-2" :class="{ 'has-anime-shortcut': showAnimeShortcut }">
        <div class="nav-indicator absolute inset-y-0 z-0 rounded-[0.6rem] bg-[#f1f5f9] dark:bg-slate-800" :style="navIndicatorStyle"></div>
        <RouterLink :to="rememberedDashboardPaths.movie" :aria-current="movieActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: movieActive }">电影</RouterLink>
        <RouterLink :to="rememberedDashboardPaths.tv" :aria-current="tvActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: tvActive }">剧集</RouterLink>
        <RouterLink to="/updates" :aria-current="updatesActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: updatesActive }">更新</RouterLink>
        <RouterLink v-if="showAnimeShortcut" to="/anime" aria-label="动漫订阅" :aria-current="animeActive ? 'page' : undefined" class="top-link relative z-10" :class="{ selected: animeActive }">订阅</RouterLink>
      </nav>
      <div class="header-actions flex items-center gap-1 border-l border-slate-200 dark:border-slate-700 pl-2 sm:gap-3 sm:pl-5" :class="{ 'glass-pill-surface': mobileDetail }">
        <RouterLink to="/notifications" class="icon-button desktop-action relative" v-tooltip="websiteUnread ? `站内通知，${websiteUnread} 条未读` : '站内通知'" :aria-label="websiteUnread ? `站内通知，${websiteUnread} 条未读` : '站内通知'">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4" /></svg>
          <span v-if="websiteUnread" class="absolute -right-1 -top-1 min-w-4 rounded-full bg-amber-500 px-1 text-center text-[10px] leading-4 text-white">{{ websiteUnread > 99 ? '99+' : websiteUnread }}</span>
        </RouterLink>
        <RouterLink to="/search" class="icon-button" :class="{ selected: route.path === '/search' }" v-tooltip="'搜索影视'" aria-label="搜索影视" :aria-current="route.path === '/search' ? 'page' : undefined">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg>
        </RouterLink>
        <button v-if="isPortalAdmin" type="button" class="icon-button connection-test-action" v-tooltip="'服务连通性'" aria-label="服务连通性" aria-haspopup="dialog" @click="closePanels(); openNetworkConnectionTest()">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m13 2-9 12h7l-1 8 10-12h-7l1-8Z" /></svg>
        </button>
        <div class="hidden sm:block"><ThemeSwitcher :before-change="prepareThemeChange" /></div>
        <button ref="accountButton" type="button" class="icon-button account-trigger relative" :class="{ selected: accountOpen }" v-tooltip="accountOpen ? undefined : '账户与更多功能'" :aria-label="websiteUnread ? `更多功能，${websiteUnread} 条未读通知` : '更多功能'" :aria-expanded="accountOpen" aria-controls="header-management" @click="toggleAccount">
          <svg class="h-5 w-5 sm:hidden" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/></svg>
          <svg class="hidden h-6 w-6 sm:block" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="9" r="3"/><path d="M5.5 18a6.7 6.7 0 0 1 13 0"/></svg>
          <span v-if="websiteUnread" class="absolute right-0 top-0 h-2 w-2 rounded-full bg-amber-500 sm:hidden" aria-hidden="true"></span>
        </button>
      </div>
      <Transition :css="false" @before-enter="activateAccountMenu" @enter="enterAccountMenu" @enter-cancelled="accountMenuMotion.cancel" @before-leave="deactivateAccountMenu" @leave="accountMenuMotion.leave" @after-leave="completeAccountLeave" @leave-cancelled="element => { accountMenuMotion.cancel(element); completeAccountLeave(element) }">
        <HeaderAccountMenu v-if="accountOpen" :before-theme-change="prepareThemeChange" @close="closePanels" @logout="emit('logout')" />
      </Transition>
    </div>
  </header>
  <ManagementSidebar v-if="!managedSidebar && !sidebarLayout" ref="navigationSidebar" mobile-only :desktop="desktop" />
</template>


<style>
:root {
  --header-bg: var(--app-chrome-color);
  --header-border: var(--color-slate-200);
}
.dark {
  --header-bg: var(--app-chrome-color);
  --header-border: var(--color-border);
}
</style>
<style scoped>
/* 背景覆盖顶部安全区，工具栏与页面偏移仍共用准确的总高度。 */
.workbench-header { padding-top: var(--app-safe-top); }
.header-inner { padding-left: max(16px, var(--app-safe-left)); padding-right: max(16px, var(--app-safe-right)); }
.with-management-sidebar .header-inner { justify-content: flex-end; }
.workbench-header.over-backdrop { --cinema-logo-ink: #f1f4fc; --cinema-logo-blue: #7595ff; }
.workbench-header.immersive-surface {
  --cinema-logo-ink: color-mix(in srgb, #192235 calc(var(--header-surface-progress) * 100%), #f1f4fc);
  --cinema-logo-blue: color-mix(in srgb, #3165ee calc(var(--header-surface-progress) * 100%), #7595ff);
}
.dark .workbench-header.immersive-surface { --cinema-logo-ink: #f1f4fc; --cinema-logo-blue: #7595ff; }
.workbench-header.tonal-surface {
  --header-tonal-bg: var(--app-chrome-color);
  --header-tonal-alpha: var(--glass-header-alpha);
  --header-surface-progress: 1;
  --header-edge-progress: 1;
  --header-active-opacity: 1;
  background: transparent;
  border-color: transparent;
  transition: none;
}
.dark .workbench-header.tonal-surface {
  --header-tonal-bg: var(--app-chrome-color);
  --header-tonal-alpha: var(--glass-header-alpha);
}
.workbench-header.immersive-surface {
  --header-surface-progress: 0;
  --header-edge-progress: 0;
  --header-active-opacity: 0.45;
}
.header-surface {
  background: rgb(var(--header-tonal-bg) / var(--header-tonal-alpha));
  box-shadow: var(--glass-rim-shadow);
  -webkit-backdrop-filter: var(--glass-header-filter);
  backdrop-filter: var(--glass-header-filter);
  opacity: var(--header-surface-progress);
}
.header-background { background: var(--glass-header-background); box-shadow: var(--glass-rim-shadow); -webkit-backdrop-filter: var(--glass-header-filter); backdrop-filter: var(--glass-header-filter); }
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
.workbench-header.backdrop-at-top .brand-link { filter: drop-shadow(0 1px 2px rgb(0 0 0 / 65%)); }
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
.workbench-header.with-management-sidebar { left: var(--management-sidebar-width); right: 0; width: auto; transition: none; }
.with-management-sidebar :is(.header-background, .header-surface) { position: fixed; inset: 0 0 auto; height: var(--workbench-header-height); }
.workbench-header.with-management-sidebar::after { position: fixed; inset: var(--workbench-header-height) 0 auto; }
a:focus-visible, button:focus-visible { outline: 2px solid rgb(var(--color-accent)); outline-offset: 3px; }
@media (prefers-reduced-motion: reduce) {
  .workbench-header.tonal-surface,
  .dark .workbench-header.tonal-surface { --header-tonal-alpha: max(.95, var(--glass-header-alpha)); }
  .header-surface { -webkit-backdrop-filter: none; backdrop-filter: none; }
  .workbench-header.tonal-surface :is(.top-link, .icon-button) { transition: none; }
}
@media (display-mode: standalone) {
  /* 网页顶栏、安全区和原生栏共用实色，避免半透明合成后出现色差。 */
  .workbench-header.tonal-surface,
  .dark .workbench-header.tonal-surface { --header-tonal-alpha: 1; }
  .header-surface,
  .header-background {
    -webkit-backdrop-filter: none;
    backdrop-filter: none;
    transition: none;
  }
  .header-surface { opacity: 1; }
}
@media (min-width: 640px) {
  .top-link { padding-inline: 1.25rem; }
  .header-inner { padding-left: max(24px, var(--app-safe-left)); padding-right: max(24px, var(--app-safe-right)); }
}
@media (max-width: 767px) {
  .connection-test-action { display: none; }
}
@media (max-width: 639px) {
  .desktop-action { display: none; }
  .has-anime-shortcut .top-link { display: inline-flex; min-width: 44px; min-height: 44px; align-items: center; justify-content: center; padding: 0 7px; }
  [data-menu-origin-owned] { opacity: 0; }
  .account-trigger { transition: color 200ms, background-color 200ms; }
  .account-trigger > svg, .account-trigger > span { transition: transform 140ms ease; }
  .account-trigger:active > svg { transform: scale(.85); }
}
@media (max-width: 420px) {
  .header-inner { gap: 0.25rem; padding-left: max(12px, var(--app-safe-left)); padding-right: max(12px, var(--app-safe-right)); }
  .top-link { padding-inline: 0.5rem; }
  .has-anime-shortcut { gap: 0; }
  .icon-button { width: 2rem; height: 2rem; }
}
/* 详情页手机导航只在小胶囊采样，空白区域允许操作下方正文。 */
.workbench-header.mobile-detail { pointer-events: none; border-color: transparent; }
.mobile-detail::after, .mobile-detail .header-surface, .mobile-detail .header-background { display: none; }
.mobile-detail .header-inner { gap: 8px; padding-inline: max(14px, var(--app-safe-left)) max(14px, var(--app-safe-right)); }
.mobile-detail .detail-nav-back { width: 44px; height: 44px; border-radius: 50%; color: var(--glass-text); pointer-events: auto; }
.detail-nav-title-slot { display: flex; flex: 1; min-width: 0; justify-content: center; }
/* 片名区域随剩余空间伸缩，三个胶囊保持同高，文字和年份整体居中。 */
.detail-nav-title {
  display: inline-flex; flex: 1; width: 100%; max-width: 100%; min-width: 0; height: 44px; align-items: center; justify-content: center; gap: 6px; padding-inline: 12px;
  visibility: hidden; opacity: 0; transform: translateY(-4px); font-size: 14px; font-weight: 600;
  transition: opacity 160ms ease, transform 160ms ease, visibility 160ms;
}
.detail-nav-title.is-visible { visibility: visible; opacity: 1; transform: none; }
.detail-nav-name { overflow: hidden; min-width: 0; text-overflow: ellipsis; white-space: nowrap; }
.detail-nav-year { flex-shrink: 0; color: var(--glass-muted); font-size: 12px; font-weight: 500; font-variant-numeric: tabular-nums; }
.mobile-detail .header-actions { flex: 0 0 auto; gap: 0; height: 44px; padding: 0; border: 0; pointer-events: auto; }
.mobile-detail .header-actions > a[aria-label='搜索影视'] { position: relative; }
.mobile-detail .header-actions > a[aria-label='搜索影视']::after { content: ''; position: absolute; right: 0; width: 1px; height: 16px; background: var(--glass-edge); }
.workbench-header.mobile-detail .icon-button { width: 44px; height: 44px; border-radius: 50%; color: var(--glass-text); }
.workbench-header.mobile-detail .icon-button:hover, .workbench-header.mobile-detail .icon-button.selected { background: rgb(var(--surface-hover) / .6); color: var(--glass-text); }
.workbench-header.mobile-detail :is(a, button):focus-visible { outline-color: rgb(var(--focus)); }
.mobile-detail :deep(#header-management) { right: max(14px, var(--app-safe-right)); }
@media (display-mode: standalone) {
  /* 系统栏安全区保持实色，下面的操作胶囊仍沿用用户选择的玻璃材质。 */
  .mobile-detail .header-surface, .mobile-detail .header-background {
    display: block; height: var(--app-safe-top); background: rgb(var(--app-chrome-color)); opacity: 1; box-shadow: none;
  }
}
@media (prefers-reduced-motion: reduce) { .detail-nav-title { transition: none; transform: none; } }
@media (prefers-reduced-motion: reduce) {
  .account-trigger, .account-trigger > svg, .account-trigger > span { transition: none; }
  .account-trigger:active > svg { transform: none; }
}
</style>
