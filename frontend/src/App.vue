<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import LoginView from './views/LoginView.vue'
import { websiteToken as token, websiteUser, websiteAuthError, websiteBrowseRoutes, ensureWebsiteSession, setWebsiteToken, clearWebsiteSession } from './composables/useWebsiteAuth'

const route = useRoute()
const router = useRouter()
const username = computed(() => websiteUser.value?.username || '')
// 登录完成到路由重定向之间也不挂载普通用户无权访问的管理页面。
const canViewRoute = computed(() => websiteUser.value?.role === 'admin' || websiteBrowseRoutes.has(String(route.name)))
// 分类页只接收用户名，避免把令牌作为未声明属性落到页面 DOM 上。
const viewProps = computed(() => route.meta.categoryPage ? { username: username.value } : { token: token.value })

function openCategory(category: 'p115' | 'telegram') {
  void router.push(category === 'telegram' ? '/telegram' : '/115')
}
function openP115() { void router.push(route.name === 'p115-category' ? '/helper115' : '/115') }
function openTelegram() { void router.push(route.name === 'telegram-category' ? '/telegram/bot' : '/telegram') }
function back() { openCategory(route.name === 'telegram' ? 'telegram' : 'p115') }

async function handleLoggedIn(value: string) {
  const target = route.fullPath
  setWebsiteToken(value)
  await ensureWebsiteSession(true)
  await router.replace(websiteUser.value?.role !== 'admin' && !websiteBrowseRoutes.has(String(route.name)) ? '/' : target)
}

function logout() {
  clearWebsiteSession()
  void router.replace('/')
}
onMounted(() => { void ensureWebsiteSession() })
const authTimer = window.setInterval(() => { if (!document.hidden) void ensureWebsiteSession(true) }, 60000)
onBeforeUnmount(() => window.clearInterval(authTimer))
</script>

<template>
  <LoginView v-if="!token" @logged-in="handleLoggedIn" />
  <main v-else-if="!websiteUser || websiteAuthError || !canViewRoute" class="flex min-h-screen flex-col items-center justify-center gap-4 bg-slate-50 p-6 text-sm text-slate-600">
    <p>{{ websiteAuthError || '正在验证登录状态…' }}</p>
    <button v-if="websiteAuthError" class="text-blue-600" @click="ensureWebsiteSession(true)">重新验证</button>
    <button v-if="websiteAuthError" class="text-slate-500" @click="logout">返回登录</button>
  </main>
  <RouterView v-else v-slot="{ Component }">
    <component :is="Component" v-bind="viewProps" @home="router.push('/')" @back="back" @category="openCategory" @open-dashboard="router.push('/')" @open-p115="openP115" @open-share="router.push('/115/share')" @open-telegram="openTelegram" @open-logs="router.push('/logs')" @logout="logout" />
  </RouterView>
</template>
