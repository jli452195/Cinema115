<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import LoginLayout from '../components/LoginLayout.vue'
import LoginForm from '../components/LoginForm.vue'

const emit = defineEmits<{ loggedIn: [token: string] }>()
const route = useRoute()
</script>

<template>
  <LoginLayout subtitle="管理后台">
    <LoginForm endpoint="/api/auth/admin/login" title="管理员登录" description="使用独立的后台管理员账号登录" username-label="管理员账号" submit-label="登录管理后台" @logged-in="emit('loggedIn', $event)" />
    <template #footer>
      <RouterLink :to="{ path: '/login', query: route.query }" class="auth-link">返回 Emby 用户登录</RouterLink>
    </template>
  </LoginLayout>
</template>
