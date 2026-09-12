<script setup lang="ts">
import { ref } from 'vue'
import HomeView from './views/HomeView.vue'
import LoginView from './views/LoginView.vue'
import P115View from './views/P115View.vue'
import ShareView from './views/ShareView.vue'
import TelegramView from './views/TelegramView.vue'
import LogsView from './views/LogsView.vue'

const token = ref(localStorage.getItem('admin_token') ?? '')
const username = ref(localStorage.getItem('admin_username') ?? '管理员')
const currentView = ref<'home' | 'p115' | 'share' | 'telegram' | 'logs'>('home')
const currentCategory = ref<'p115' | 'telegram'>('p115')

function openCategory(category: 'p115' | 'telegram') {
  currentCategory.value = category
  currentView.value = 'home'
}

function handleLoggedIn(value: string) {
  token.value = value
  localStorage.setItem('admin_token', value)
  username.value = '管理员'
}

function logout() {
  token.value = ''
  openCategory('p115')
  localStorage.removeItem('admin_token')
  localStorage.removeItem('admin_username')
}
</script>

<template>
  <LoginView v-if="!token" @logged-in="handleLoggedIn" />
  <P115View v-else-if="currentView === 'p115'" :token="token" @back="openCategory('p115')" />
  <ShareView v-else-if="currentView === 'share'" :token="token" @back="openCategory('p115')" @open-telegram="openCategory('telegram')" />
  <TelegramView v-else-if="currentView === 'telegram'" :token="token" @back="openCategory('telegram')" @home="openCategory('p115')" @open-p115="openCategory('p115')" @open-logs="currentView = 'logs'" @logout="logout" />
  <LogsView v-else-if="currentView === 'logs'" :token="token" @home="openCategory('p115')" @open-p115="openCategory('p115')" @open-telegram="openCategory('telegram')" @logout="logout" />
  <HomeView v-else :username="username" :category="currentCategory" @category="openCategory" @open-p115="currentView = 'p115'" @open-share="currentView = 'share'" @open-telegram="currentView = 'telegram'" @open-logs="currentView = 'logs'" @logout="logout" />
</template>
