<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import ThemeSwitcher from './ThemeSwitcher.vue'
import NavigationIcon from './NavigationIcon.vue'
import { isPortalAdmin, portalUser } from '../composables/usePortalAuth'
import { websiteUnread } from '../composables/useWebsiteNotifications'
import { isAccountLinkActive, type AccountLink } from '../utils/accountNavigation'

const emit = defineEmits<{ close: []; logout: [] }>()
const route = useRoute()
const personalLinks = computed<AccountLink[]>(() => [
  ...(!isPortalAdmin.value ? [{ label: '我的播放', to: '/account/playback', icon: 'playback' as const }] : []),
  { label: '我的追更', to: '/follows', icon: 'follows' },
])

function isActive(link: AccountLink) {
  if (link.to === '/follows') return route.name === 'my-follows'
  return isAccountLinkActive(route, link)
}

function logout() {
  emit('close')
  emit('logout')
}
</script>

<template>
  <nav id="header-management" aria-label="账户菜单" class="account-menu">
    <div class="account-surface account-main floating-surface">
      <div class="account-summary">
        <span class="account-avatar" aria-hidden="true">
          <NavigationIcon name="user" :size="20" />
        </span>
        <div class="account-identity">
          <p class="account-name" :title="portalUser?.username">{{ portalUser?.username }}</p>
          <span class="account-role">{{ isPortalAdmin ? '管理员' : '普通用户' }}</span>
        </div>
      </div>

      <div class="account-group mobile-utilities" role="group" aria-label="通知与外观">
        <RouterLink to="/notifications" class="account-row ambient-glow" :class="{ 'is-active': route.path === '/notifications' }" :aria-current="route.path === '/notifications' ? 'page' : undefined" @click="emit('close')">
          <NavigationIcon name="bell" class="account-icon" />
          <span>站内通知</span>
          <span v-if="websiteUnread" class="account-unread" :aria-label="`${websiteUnread} 条未读`">{{ websiteUnread > 99 ? '99+' : websiteUnread }}</span>
        </RouterLink>
        <ThemeSwitcher show-label class="account-row ambient-glow" />
      </div>

      <div class="account-group" role="group" aria-label="个人">
        <RouterLink v-for="link in personalLinks" :key="link.to" :to="link.to" class="account-row ambient-glow" :class="{ 'is-active': isActive(link) }" :aria-current="isActive(link) ? 'page' : undefined" @click="emit('close')">
          <NavigationIcon :name="link.icon" class="account-icon" />
          <span>{{ link.label }}</span>
        </RouterLink>
      </div>

      <div class="account-group">
        <button type="button" class="account-row account-logout" @click="logout">
          <NavigationIcon name="logout" class="account-icon" />
          <span>退出登录</span>
        </button>
      </div>
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
.account-row:focus-visible, .account-menu :deep(.theme-menu-button:focus-visible) { outline: 2px solid var(--account-emphasis); outline-offset: -2px; }
@media (hover: hover) {
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
  .account-row.ambient-glow > span:not(.account-unread),
  .account-menu :deep(.theme-menu-button > span) { transform-origin: left center; transition: transform 180ms ease-out; }

  .account-row.ambient-glow > .account-icon,
  .account-menu :deep(.theme-menu-button > svg) { transform-origin: center; transition: color 140ms ease, transform 180ms ease-out; }

  .account-row.ambient-glow:hover > span:not(.account-unread),
  .account-menu :deep(.theme-menu-button:hover > span) { transform: scale(1.035); }

  .account-row.ambient-glow:hover > .account-icon,
  .account-menu :deep(.theme-menu-button:hover > svg) { transform: scale(1.06); }
}
@media (min-width: 640px) {
  .account-menu { right: 2rem; }
  .mobile-utilities { display: none; }
  .mobile-utilities + .account-group { margin-top: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .account-menu .ambient-glow::before, .account-menu .ambient-glow::after { transition: none; }
  .account-row, .account-icon, .account-menu :deep(.theme-menu-button), .account-menu :deep(.theme-menu-button svg) { transition: none; }
}
</style>
