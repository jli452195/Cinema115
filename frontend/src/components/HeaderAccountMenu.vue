<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import ThemeSwitcher from './ThemeSwitcher.vue'
import { isPortalAdmin, portalUser } from '../composables/usePortalAuth'
import { websiteUnread } from '../composables/useWebsiteNotifications'
import { accountIcons, adminAccountGroups, isAccountLinkActive, type AccountLink } from '../utils/accountNavigation'

const emit = defineEmits<{ close: []; logout: [] }>()
const route = useRoute()
const root = ref<HTMLElement>()
const mainPanel = ref<HTMLElement>()
const subPanel = ref<HTMLElement>()
const activeCategory = ref<AccountLink>()
const compact = ref(false)
const flyoutTop = ref(0)
let compactViewport: MediaQueryList | undefined
let openTimer: ReturnType<typeof setTimeout> | undefined
let closeTimer: ReturnType<typeof setTimeout> | undefined
let positionFrame: number | undefined
const personalLinks = computed<AccountLink[]>(() => [
  ...(!isPortalAdmin.value ? [{ label: '我的播放', to: '/account/playback', icon: 'playback' as const }] : []),
  { label: '我的追更', to: '/follows', icon: 'follows' },
])
const groups = computed(() => [
  { label: '个人', links: personalLinks.value },
  ...(isPortalAdmin.value ? adminAccountGroups : []),
])

function isActive(link: AccountLink) {
  if (link.to === '/follows') return route.name === 'my-follows'
  return isAccountLinkActive(route, link)
}
function isChildActive(to: string) { return route.path === to || route.path.startsWith(`${to}/`) }
function submenuId(to: string) { return `account-submenu-${to.slice(1).replace(/\//g, '-')}` }
function categoryButton(to: string) {
  return root.value?.querySelector<HTMLButtonElement>(`button[data-category="${to}"]`)
}
function categoryRow(to: string) { return root.value?.querySelector<HTMLElement>(`.account-category[data-category="${to}"]`) }
function firstSubControl() { return subPanel.value?.querySelector<HTMLElement>('button, a[href]') }
function cancelPending() {
  clearTimeout(openTimer)
  clearTimeout(closeTimer)
}
function keepSubmenuOpen() { clearTimeout(closeTimer) }
async function openCategory(link: AccountLink, focus = false) {
  cancelPending()
  const moveFocus = focus || !!subPanel.value?.contains(document.activeElement)
  activeCategory.value = link
  await nextTick()
  positionFlyout()
  if (moveFocus) {
    firstSubControl()?.focus()
  }
}
async function closeCategory(returnFocus = false) {
  cancelPending()
  const to = activeCategory.value?.to
  activeCategory.value = undefined
  if (returnFocus && to) {
    await nextTick()
    categoryButton(to)?.focus({ preventScroll: true })
  }
}
function scheduleClose() {
  cancelPending()
  closeTimer = setTimeout(() => {
    // Pointer movement must not remove the panel being used with the keyboard.
    if (!subPanel.value?.contains(document.activeElement)) void closeCategory()
  }, 200)
}
function hoverCategory(link: AccountLink, event: PointerEvent) {
  if (compact.value || event.pointerType !== 'mouse' || !window.matchMedia('(hover: hover) and (pointer: fine)').matches) return
  cancelPending()
  openTimer = setTimeout(() => { void openCategory(link) }, 120)
}
function leaveCategory() { clearTimeout(openTimer) }
function onFocusIn(event: FocusEvent) {
  if (activeCategory.value && mainPanel.value?.contains(event.target as Node)
    && !categoryRow(activeCategory.value.to)?.contains(event.target as Node)) void closeCategory()
}
function onKeyDown(event: KeyboardEvent) {
  const target = event.target as HTMLElement
  const parent = target.closest<HTMLElement>('[data-category]')
  const inSubPanel = subPanel.value?.contains(target)
  if (activeCategory.value && (event.key === 'Escape'
    || (inSubPanel && event.key === (compact.value ? 'ArrowLeft' : 'ArrowRight'))
    || (inSubPanel && event.key === 'Tab' && event.shiftKey && target === firstSubControl()))) {
    event.preventDefault()
    event.stopPropagation()
    void closeCategory(true)
    return
  }
  if (!parent) return
  if (event.key === (compact.value ? 'ArrowRight' : 'ArrowLeft')) {
    const link = groups.value.flatMap(group => group.links).find(link => link.to === parent.dataset.category)
    if (link?.children) {
      event.preventDefault()
      void openCategory(link, true)
    }
  } else if (event.key === 'Tab' && !event.shiftKey && target === categoryButton(parent.dataset.category || '') && activeCategory.value?.to === parent.dataset.category) {
    event.preventDefault()
    firstSubControl()?.focus()
  }
}
function positionFlyout() {
  if (compact.value || !activeCategory.value || !root.value || !subPanel.value) return
  const row = categoryRow(activeCategory.value.to)?.getBoundingClientRect()
  if (!row) return
  const rootTop = root.value.getBoundingClientRect().top
  const height = subPanel.value.getBoundingClientRect().height
  const inset = subPanel.value.clientTop + parseFloat(getComputedStyle(subPanel.value).paddingTop)
  // Align the heading with its parent row, only lifting the panel to fit the viewport.
  flyoutTop.value = Math.max(8, Math.min(row.top - inset, window.innerHeight - height - 8)) - rootTop
}
function schedulePosition() {
  if (positionFrame !== undefined || !activeCategory.value || compact.value) return
  positionFrame = requestAnimationFrame(() => { positionFrame = undefined; positionFlyout() })
}

function logout() {
  emit('close')
  emit('logout')
}
function updateViewport() {
  compact.value = !!compactViewport?.matches
  void closeCategory(!!subPanel.value?.contains(document.activeElement))
}
onMounted(() => {
  compactViewport = window.matchMedia('(max-width: 639px), (pointer: coarse)')
  compact.value = compactViewport.matches
  compactViewport.addEventListener('change', updateViewport)
  window.addEventListener('resize', schedulePosition)
})
watch(isPortalAdmin, admin => { if (!admin) void closeCategory() })
onBeforeUnmount(() => {
  cancelPending()
  compactViewport?.removeEventListener('change', updateViewport)
  window.removeEventListener('resize', schedulePosition)
  if (positionFrame !== undefined) cancelAnimationFrame(positionFrame)
})
</script>

<template>
  <nav id="header-management" ref="root" aria-label="更多功能" class="account-menu" :class="{ 'is-compact': compact }" @keydown="onKeyDown" @focusin="onFocusIn" @pointerenter="keepSubmenuOpen" @pointerleave="scheduleClose">
    <div v-show="!compact || !activeCategory" ref="mainPanel" class="account-surface account-main floating-surface" @scroll.passive="schedulePosition">
      <div class="account-summary">
        <span class="account-avatar" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path :d="accountIcons.user" /></svg>
        </span>
        <div class="account-identity">
          <p class="account-name" :title="portalUser?.username">{{ portalUser?.username }}</p>
          <span class="account-role">{{ isPortalAdmin ? '管理员' : '普通用户' }}</span>
        </div>
      </div>

      <div class="account-group mobile-utilities" role="group" aria-label="通知与外观">
        <RouterLink to="/notifications" class="account-row ambient-glow" :class="{ 'is-active': route.path === '/notifications' }" :aria-current="route.path === '/notifications' ? 'page' : undefined" @click="emit('close')" @pointerenter="scheduleClose">
          <svg class="account-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="accountIcons.bell" /></svg>
          <span>站内通知</span>
          <span v-if="websiteUnread" class="account-unread" :aria-label="`${websiteUnread} 条未读`">{{ websiteUnread > 99 ? '99+' : websiteUnread }}</span>
        </RouterLink>
        <ThemeSwitcher show-label class="account-row ambient-glow" @pointerenter="scheduleClose" />
      </div>

      <div v-for="group in groups" :key="group.label" class="account-group" role="group" :aria-label="group.label">
        <template v-for="link in group.links" :key="link.to">
          <div v-if="link.children" class="account-row account-category ambient-glow" :class="{ 'is-active': isActive(link), 'is-expanded': activeCategory?.to === link.to }" :data-category="link.to" @pointerenter="hoverCategory(link, $event)" @pointerleave="leaveCategory">
            <RouterLink :to="link.to" class="account-category-link" :aria-current="route.path === link.to ? 'page' : undefined" @click="emit('close')">
              <svg class="account-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="accountIcons[link.icon]" /></svg>
              <span>{{ link.label }}</span>
            </RouterLink>
            <button type="button" class="account-expand" :data-category="link.to" :aria-label="`展开${link.label}子菜单`" :aria-expanded="activeCategory?.to === link.to" :aria-controls="submenuId(link.to)" @click="openCategory(link, compact || $event.detail === 0)">
              <svg class="account-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 5 7 7-7 7" /></svg>
            </button>
          </div>
          <RouterLink v-else :to="link.to" class="account-row ambient-glow" :class="{ 'is-active': isActive(link) }" :aria-current="isActive(link) ? 'page' : undefined" @click="emit('close')" @pointerenter="scheduleClose">
            <svg class="account-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="accountIcons[link.icon]" /></svg>
            <span>{{ link.label }}</span>
          </RouterLink>
        </template>
      </div>

      <div class="account-group">
        <button type="button" class="account-row account-logout" @click="logout" @pointerenter="scheduleClose">
          <svg class="account-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="accountIcons.logout" /></svg>
          <span>退出登录</span>
        </button>
      </div>
    </div>

    <div v-if="activeCategory" :id="submenuId(activeCategory.to)" class="account-flyout" :style="compact ? undefined : { top: `${flyoutTop}px` }" @pointerenter="cancelPending">
      <section ref="subPanel" class="account-surface account-submenu floating-surface" :aria-label="activeCategory.label" @focusin="cancelPending">
        <div class="account-submenu-heading">
          <button v-if="compact" type="button" class="account-back ambient-glow" aria-label="返回更多功能" @click="closeCategory(true)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m14 6-6 6 6 6" /></svg>
          </button>
          <h2 class="account-subtitle">{{ activeCategory.label }}</h2>
        </div>
        <RouterLink v-for="link in activeCategory.children" :key="link.to" :to="link.to" class="account-row ambient-glow" :class="{ 'is-active': isChildActive(link.to) }" :aria-current="isChildActive(link.to) ? 'page' : undefined" @click="emit('close')">
          <svg class="account-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="accountIcons[link.icon]" /></svg>
          <span>{{ link.label }}</span>
        </RouterLink>
      </section>
    </div>
  </nav>
</template>

<style scoped>
.account-menu {
  --account-hover: #edf1f7;
  --account-active: #e7edf6;
  --account-emphasis: #365b87;
  position: absolute;
  top: 100%;
  right: 1rem;
  width: 16rem;
  max-width: calc(100vw - 1.5rem);
  margin-top: 0.5rem;
}
.account-surface {
  --account-text: var(--floating-surface-text);
  --account-muted: var(--floating-surface-muted);
  --account-strong: var(--floating-surface-text);
  max-height: calc(100dvh - var(--workbench-header-height) - 1rem);
  padding: 0.5rem;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  border-radius: 1rem;
  color: var(--account-text);
  background: var(--floating-surface-container);
  -webkit-backdrop-filter: none;
  backdrop-filter: none;
}
.account-menu .ambient-glow {
  --ambient-glow-strength: 0.65;
  --ambient-glow-step: 1px;
  --ambient-glow-wide-step: 1px;
  --ambient-glow-primary-stop: 60%;
  --ambient-glow-secondary-stop: 55%;
}
.account-menu .ambient-glow::before,
.account-menu .ambient-glow::after { animation: none; }
.dark .account-menu {
  --account-hover: #2c3442;
  --account-active: #303e52;
  --account-emphasis: #b4cbed;
}
.account-flyout { position: absolute; top: 0; right: calc(100% + 0.5rem); width: 14.5rem; }
.account-submenu { animation: account-flyout-in 140ms ease-out; }
.account-flyout::after { content: ''; position: absolute; inset: 0 -0.5rem 0 auto; width: 0.5rem; }
.is-compact .account-flyout { position: relative; right: auto; width: 100%; }
.is-compact .account-flyout::after { display: none; }
.account-submenu-heading { display: flex; min-height: 2.75rem; align-items: center; gap: 0.25rem; margin-bottom: 0.375rem; padding-inline: 0.75rem 0.25rem; }
.is-compact .account-submenu-heading { padding-left: 0; }
.account-subtitle { min-width: 0; color: var(--account-strong); font-size: 0.875rem; font-weight: 600; }
.account-back { display: flex; flex: 0 0 2.75rem; height: 2.75rem; align-items: center; justify-content: center; border-radius: 0.625rem; color: var(--account-muted); }
.account-back svg { width: 1.125rem; height: 1.125rem; }
.account-chevron { flex: 0 0 0.875rem; width: 0.875rem; height: 0.875rem; margin-left: auto; color: var(--account-muted); transform: rotate(180deg); }
.is-compact .account-chevron { transform: none; }
.account-row.account-category { gap: 0; padding: 0; }
.account-category-link { display: flex; flex: 1; min-width: 0; min-height: 2.75rem; align-items: center; gap: 0.75rem; padding: 0.625rem 0.75rem; border-radius: 0.625rem; }
.account-expand { display: flex; flex: 0 0 2.75rem; height: 2.75rem; align-items: center; justify-content: center; border-radius: 0.625rem; color: var(--account-muted); }
.account-expand .account-chevron { margin: 0; }
.account-category.is-expanded:not(.is-active) { color: var(--account-strong); background: var(--account-hover); }
@keyframes account-flyout-in { from { opacity: 0; transform: translateX(4px); } to { opacity: 1; transform: translateX(0); } }
.account-summary { display: flex; align-items: center; gap: 0.75rem; padding: 0.625rem 0.75rem 0.875rem; }
.account-avatar { display: flex; flex: 0 0 2.25rem; height: 2.25rem; align-items: center; justify-content: center; border-radius: 0.75rem; color: #557299; background: linear-gradient(135deg, #e1e9f6, #edf1f8); }
.account-avatar svg { width: 1.25rem; height: 1.25rem; }
.dark .account-avatar { color: #b4cbed; background: linear-gradient(135deg, #3b4a63, #303c50); }
.account-identity { min-width: 0; }
.account-name { overflow: hidden; color: var(--account-strong); font-size: 0.875rem; font-weight: 600; line-height: 1.375rem; text-overflow: ellipsis; white-space: nowrap; }
.account-role { display: inline-block; margin-top: 0.125rem; padding: 0.0625rem 0.375rem; border-radius: 0.375rem; color: var(--account-muted); background: var(--account-hover); font-size: 0.625rem; line-height: 1rem; }
.account-group + .account-group { margin-top: 0.625rem; }
.account-row, .account-menu :deep(.theme-menu-button) { display: flex; width: 100%; min-height: 2.75rem; align-items: center; gap: 0.75rem; padding: 0.625rem 0.75rem; border-radius: 0.625rem; color: var(--account-text); background: transparent; font-size: 0.875rem; line-height: 1.5rem; text-align: left; transition: background-color 140ms, color 140ms; }
.account-icon, .account-menu :deep(.theme-menu-button svg) { flex: 0 0 1.125rem; width: 1.125rem; height: 1.125rem; color: var(--account-muted); transition: color 140ms; }
.account-row.is-active { color: var(--account-emphasis); background: var(--account-active); font-weight: 600; }
.account-row.is-active .account-icon { color: var(--account-emphasis); }
.account-unread { margin-left: auto; min-width: 1.25rem; padding: 0 0.375rem; border-radius: 0.375rem; color: var(--account-emphasis); background: var(--account-active); font-size: 0.6875rem; line-height: 1.25rem; text-align: center; }
.account-logout { color: var(--account-muted); }
.account-row:focus-visible, .account-back:focus-visible, .account-category-link:focus-visible, .account-expand:focus-visible, .account-menu :deep(.theme-menu-button:focus-visible) { outline: 2px solid var(--account-emphasis); outline-offset: -2px; }
@media (hover: hover) {
  .account-back:hover, .account-expand:hover { color: var(--account-emphasis); background: var(--account-hover); }
  .account-row:hover, .account-menu :deep(.theme-menu-button:hover) { color: var(--account-strong); background: var(--account-hover); }
  .account-row:hover .account-icon, .account-menu :deep(.theme-menu-button:hover svg) { color: var(--account-emphasis); }
  .account-row.is-active:hover { color: var(--account-emphasis); background: var(--account-active); }
  .account-logout:hover { color: #b91c1c; background: #fcecec; }
  .account-logout:hover .account-icon { color: inherit; }
  .dark .account-logout:hover { color: #fca5a5; background: #432c34; }
}
@media (hover: hover) and (pointer: fine) {
  .account-menu .ambient-glow::before, .account-menu .ambient-glow::after { transition: opacity 200ms ease-out; }
  .account-menu .ambient-glow:hover::before { opacity: calc(0.85 * var(--ambient-glow-strength)); }
  .account-menu .ambient-glow:hover::after { opacity: calc(0.75 * var(--ambient-glow-strength)); }
}
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .account-row.ambient-glow:not(.account-category) > span:not(.account-unread),
  .account-category-link > span,
  .account-menu :deep(.theme-menu-button > span) { transform-origin: left center; transition: transform 180ms ease-out; }

  .account-row.ambient-glow:not(.account-category) > .account-icon,
  .account-category-link > .account-icon,
  .account-back > svg,
  .account-menu :deep(.theme-menu-button > svg) { transform-origin: center; transition: color 140ms ease, transform 180ms ease-out; }

  .account-row.ambient-glow:not(.account-category):hover > span:not(.account-unread),
  .account-category:hover .account-category-link > span,
  .account-menu :deep(.theme-menu-button:hover > span) { transform: scale(1.035); }

  .account-row.ambient-glow:not(.account-category):hover > .account-icon,
  .account-category:hover .account-category-link > .account-icon,
  .account-back:hover > svg,
  .account-menu :deep(.theme-menu-button:hover > svg) { transform: scale(1.06); }
}
@media (min-width: 640px) {
  .account-menu { right: 2rem; }
  .mobile-utilities { display: none; }
  .mobile-utilities + .account-group { margin-top: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .account-submenu { animation: none; }
  .account-menu .ambient-glow::before, .account-menu .ambient-glow::after { transition: none; }
  .account-row, .account-icon, .account-menu :deep(.theme-menu-button), .account-menu :deep(.theme-menu-button svg) { transition: none; }
}
</style>
