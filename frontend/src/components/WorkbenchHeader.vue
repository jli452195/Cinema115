<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { isWebsiteAdmin, websiteUser } from '../composables/useWebsiteAuth'

const emit = defineEmits<{ logout: [] }>()
const route = useRoute()
const header = ref<HTMLElement>()
const accountButton = ref<HTMLButtonElement>()
const accountOpen = ref(false)
const movieActive = computed(() => route.path.startsWith('/movie') || route.path.startsWith('/collection/'))
const tvActive = computed(() => route.path === '/' || route.path.startsWith('/tv'))
const managementLinks = computed(() => isWebsiteAdmin.value ? [
  { label: '追更管理', to: '/follows/manage' },
  { label: '用户管理', to: '/users' },
  { label: '115 网盘', to: '/115' },
  { label: 'Telegram 配置', to: '/telegram' },
  { label: '查看日志', to: '/logs' },
  { label: 'Emby', to: '/emby' },
] : [])
const managementActive = computed(() => managementLinks.value.some(link => route.path.startsWith(link.to)))

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
  <header ref="header" class="workbench-header sticky top-0 z-40 w-full shrink-0 border-b border-slate-200/80 bg-white/95 backdrop-blur-md" @focusout="onFocusOut">
    <div class="header-inner relative mx-auto flex h-16 max-w-[1600px] items-center gap-2 px-4 sm:gap-5 sm:px-8 lg:h-[72px] lg:px-10">
      <RouterLink to="/" aria-label="Cinema115 首页" class="brand-link mr-auto flex shrink-0 items-center gap-2 rounded-lg text-base font-bold tracking-tight text-slate-900 sm:gap-3 sm:text-xl">
        <span class="brand-mark flex h-8 w-8 items-center justify-center rounded-xl bg-slate-900 text-amber-400 sm:h-9 sm:w-9">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="4"/><path d="M7 3v18M17 3v18M3 8h4M3 16h4M17 8h4M17 16h4"/><path d="m10 9 5 3-5 3Z" fill="currentColor" stroke="none"/></svg>
        </span>
        <span>Cinema<span class="font-medium text-slate-500">115</span></span>
      </RouterLink>
      <nav aria-label="影视导航" class="flex items-center gap-1 sm:gap-2">
        <RouterLink to="/movie" :aria-current="movieActive ? 'page' : undefined" class="top-link" :class="{ selected: movieActive }">电影</RouterLink>
        <RouterLink to="/tv" :aria-current="tvActive ? 'page' : undefined" class="top-link" :class="{ selected: tvActive }">剧集</RouterLink>
      </nav>
      <div class="flex items-center gap-1 border-l border-slate-200 pl-2 sm:gap-3 sm:pl-5">
        <RouterLink to="/search" class="icon-button" :class="{ 'bg-amber-50 text-amber-700': route.path === '/search' }" aria-label="搜索影视" :aria-current="route.path === '/search' ? 'page' : undefined">
          <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/></svg>
        </RouterLink>
        <button ref="accountButton" type="button" class="icon-button" :class="{ 'bg-amber-50 text-amber-700': managementActive || accountOpen }" aria-label="管理菜单" :aria-expanded="accountOpen" aria-controls="header-management" @click="toggleAccount">
          <svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="9" r="3"/><path d="M5.5 18a6.7 6.7 0 0 1 13 0"/></svg>
        </button>
      </div>
      <nav v-if="accountOpen" id="header-management" aria-label="管理功能" class="absolute right-4 top-full mt-2 w-52 rounded-xl border border-slate-200 bg-white p-2 shadow-xl shadow-slate-900/10 sm:right-8">
        <p class="px-3 py-2 text-xs text-slate-400">{{ websiteUser?.username }} · {{ isWebsiteAdmin ? '管理员' : '普通用户' }}</p>
        <RouterLink to="/follows" class="block rounded-lg px-3 py-2.5 text-sm text-slate-600 hover:bg-slate-100" :aria-current="route.name === 'my-follows' ? 'page' : undefined" @click="closePanels">我的追更</RouterLink>
        <RouterLink v-for="link in managementLinks" :key="link.to" :to="link.to" :aria-current="route.path.startsWith(link.to) ? 'page' : undefined" class="block rounded-lg px-3 py-2.5 text-sm hover:bg-slate-100" :class="route.path.startsWith(link.to) ? 'bg-amber-50 text-amber-700' : 'text-slate-600'" @click="closePanels">{{ link.label }}</RouterLink>
        <div class="my-1 border-t border-slate-100"></div>
        <button type="button" class="w-full rounded-lg px-3 py-2.5 text-left text-sm text-slate-500 hover:bg-red-50 hover:text-red-600" @click="closePanels(); emit('logout')">退出登录</button>
      </nav>
    </div>
  </header>
</template>

<style scoped>
.top-link { position: relative; border-radius: 0.6rem; padding: 0.6rem 0.65rem; font-size: 0.875rem; font-weight: 500; color: #64748b; white-space: nowrap; }
.top-link:hover { background: #f8fafc; color: #0f172a; }
.top-link.selected { background: #f1f5f9; color: #0f172a; }
.top-link.selected::after { content: ''; position: absolute; height: 2px; bottom: -1px; left: 25%; right: 25%; background: #f59e0b; border-radius: 2px; }
.icon-button { display: flex; height: 2.25rem; width: 2.25rem; align-items: center; justify-content: center; border-radius: 0.6rem; color: #64748b; }
.icon-button:hover { background: #f1f5f9; color: #0f172a; }
a:focus-visible, button:focus-visible { outline: 2px solid #f59e0b; outline-offset: 3px; }
@media (min-width: 640px) { .top-link { padding-inline: 1.25rem; } }
@media (max-width: 420px) {
  .header-inner { gap: 0.25rem; padding-inline: 0.75rem; }
  .brand-link { gap: 0.375rem; font-size: 0.875rem; }
  .brand-mark { height: 1.75rem; width: 1.75rem; }
  .top-link { padding-inline: 0.5rem; }
  .icon-button { width: 2rem; height: 2rem; }
}
</style>
