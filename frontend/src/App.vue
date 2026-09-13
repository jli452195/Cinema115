<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import LoginView from './views/LoginView.vue'

const route = useRoute()
const router = useRouter()
const token = ref(localStorage.getItem('admin_token') ?? '')
const username = ref(localStorage.getItem('admin_username') ?? '管理员')
// 分类页只接收用户名，避免把令牌作为未声明属性落到页面 DOM 上。
const viewProps = computed(() => route.meta.categoryPage ? { username: username.value } : { token: token.value })

function openCategory(category: 'p115' | 'telegram') {
  void router.push(category === 'telegram' ? '/telegram' : '/')
}
function openP115() { void router.push(route.meta.categoryPage ? '/115/login' : '/') }
function openTelegram() { void router.push(route.meta.categoryPage ? '/telegram/bot' : '/telegram') }
function back() { openCategory(route.name === 'telegram' ? 'telegram' : 'p115') }

function handleLoggedIn(value: string) {
  token.value = value
  localStorage.setItem('admin_token', value)
  username.value = '管理员'
}

function logout() {
  token.value = ''
  localStorage.removeItem('admin_token')
  localStorage.removeItem('admin_username')
  void router.replace('/')
}
</script>

<template>
  <LoginView v-if="!token" @logged-in="handleLoggedIn" />
  <RouterView v-else v-slot="{ Component }">
    <component :is="Component" v-bind="viewProps" @home="openCategory('p115')" @back="back" @category="openCategory" @open-dashboard="router.push('/tv')" @open-p115="openP115" @open-share="router.push('/115/share')" @open-telegram="openTelegram" @open-logs="router.push('/logs')" @logout="logout" />
  </RouterView>
</template>
