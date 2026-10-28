<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import { loginReturnPath } from './router'
import ReloadPrompt from './components/ReloadPrompt.vue'
import GlobalToast from './components/GlobalToast.vue'
import { portalToken as token, portalUser, portalAuthError, portalBrowseRoutes, ensurePortalSession, setPortalToken, clearPortalSession } from './composables/usePortalAuth'

const route = useRoute()
const router = useRouter()
const registeredUsername = ref('')
function handleRegistered(name: string) {
  registeredUsername.value = name
  void router.replace({ path: '/login', query: route.query })
}
const username = computed(() => portalUser.value?.username || '')
// 登录完成到路由重定向之间也不挂载普通用户无权访问的管理页面。
const canViewRoute = computed(() => portalUser.value?.role === 'admin' || portalBrowseRoutes.has(String(route.name)))
// 分类页只接收用户名，避免把令牌作为未声明属性落到页面 DOM 上。
const viewProps = computed(() => route.meta.categoryPage ? { username: username.value } : { token: token.value })

function openCategory(category: 'p115' | 'telegram') {
  void router.push(category === 'telegram' ? '/telegram' : '/115')
}
function openP115() { void router.push(route.name === 'p115-category' ? '/helper115' : '/115') }
function openTelegram() { void router.push(route.name === 'telegram-category' ? '/telegram/bot' : '/telegram') }
function back() { openCategory(route.name === 'telegram' ? 'telegram' : 'p115') }

async function handleLoggedIn(value: string) {
  const target = loginReturnPath(route.query.redirect)
  setPortalToken(value)
  await ensurePortalSession(true)
  registeredUsername.value = ''
  // Successful login pages follow the session watcher. Registration recovery
  // and verification failures still need the target's recovery screen.
  if (route.name === 'register' || portalAuthError.value) await router.replace(target)
}

let loggingOut = false
async function logout() {
  const destination = portalUser.value?.role === 'admin' ? '/admin/login' : '/login'
  loggingOut = true
  clearPortalSession()
  try { await router.replace(destination) }
  finally { loggingOut = false }
}
watch([token, portalUser], ([value, currentUser], [previousToken, previousUser]) => {
  if (!value && previousToken && !loggingOut && !route.meta.public) {
    const admin = previousUser?.role === 'admin' || !portalBrowseRoutes.has(String(route.name))
    void router.replace({ path: admin ? '/admin/login' : '/login', query: { redirect: route.fullPath } })
  } else if (value && currentUser && !loggingOut) {
    if (route.name === 'login' || route.name === 'admin-login') void router.replace(loginReturnPath(route.query.redirect))
    else if (!route.meta.public && !canViewRoute.value) void router.replace('/')
  }
})
function syncLogin(event: StorageEvent) {
  if (event.storageArea === localStorage && (event.key === 'admin_token' || event.key === null)) void ensurePortalSession(true, { background: true })
}
function resumeLogin() { if (!document.hidden) void ensurePortalSession(false, { background: true }) }
onMounted(() => { void ensurePortalSession() })
window.addEventListener('storage', syncLogin)
document.addEventListener('visibilitychange', resumeLogin)
const authTimer = window.setInterval(() => { if (!document.hidden) void ensurePortalSession(true, { background: true }) }, 60000)
onBeforeUnmount(() => {
  window.clearInterval(authTimer)
  window.removeEventListener('storage', syncLogin)
  document.removeEventListener('visibilitychange', resumeLogin)
})
</script>

<template>
  <RouterView v-if="route.meta.public" v-slot="{ Component }">
    <component :is="Component" :registered-username="route.name === 'login' ? registeredUsername : undefined" @registered="handleRegistered" @logged-in="handleLoggedIn" />
  </RouterView>
  <main v-else-if="!token || !portalUser || portalAuthError || !canViewRoute" class="flex min-h-screen flex-col items-center justify-center gap-4 bg-slate-50 p-6 text-sm text-slate-600 dark:bg-slate-950 dark:text-slate-300">
    <p>{{ portalAuthError || '正在验证登录状态…' }}</p>
    <button v-if="portalAuthError" class="text-blue-600" @click="ensurePortalSession(true)">重新验证</button>
    <button v-if="portalAuthError" class="text-slate-500" @click="logout">返回登录</button>
  </main>
  <RouterView v-else v-slot="{ Component }">
    <component :is="Component" v-bind="viewProps" @home="router.push('/')" @back="back" @category="openCategory" @open-dashboard="router.push('/')" @open-p115="openP115" @open-share="router.push('/115/share')" @open-telegram="openTelegram" @open-logs="router.push('/logs')" @logout="logout" />
  </RouterView>
  <ReloadPrompt />
  <GlobalToast />
</template>
