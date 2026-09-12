<script setup lang="ts">
import { ref } from 'vue'
import HomeView from './views/HomeView.vue'
import LoginView from './views/LoginView.vue'
import P115View from './views/P115View.vue'
import ShareView from './views/ShareView.vue'

const token = ref(localStorage.getItem('admin_token') ?? '')
const username = ref(localStorage.getItem('admin_username') ?? '管理员')
const currentView = ref<'home' | 'p115' | 'share'>('home')

function handleLoggedIn(value: string) {
  token.value = value
  localStorage.setItem('admin_token', value)
  username.value = '管理员'
}

function logout() {
  token.value = ''
  currentView.value = 'home'
  localStorage.removeItem('admin_token')
  localStorage.removeItem('admin_username')
}
</script>

<template>
  <LoginView v-if="!token" @logged-in="handleLoggedIn" />
  <P115View v-else-if="currentView === 'p115'" :token="token" @back="currentView = 'home'" />
  <ShareView v-else-if="currentView === 'share'" :token="token" @back="currentView = 'home'" />
  <HomeView v-else :username="username" @open-p115="currentView = 'p115'" @open-share="currentView = 'share'" @logout="logout" />
</template>
