<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import LoginLayout from '../components/LoginLayout.vue'
import LoginForm from '../components/LoginForm.vue'

defineProps<{ registeredUsername?: string }>()
const emit = defineEmits<{ loggedIn: [token: string] }>()
const route = useRoute()
</script>

<template>
  <LoginLayout subtitle="发现影视，管理你的观影生活">
    <LoginForm endpoint="/api/auth/login" title="欢迎回来" description="使用 Emby 账号登录个人中心" username-label="Emby 用户名" submit-label="登录个人中心" :initial-username="registeredUsername" @logged-in="emit('loggedIn', $event)">
      <template #before-fields>
        <p v-if="registeredUsername" role="status" class="auth-notice auth-notice-success">Emby 账号已创建，请使用刚设置的密码登录。</p>
      </template>
      <div class="auth-bottom-link">
        <RouterLink :to="{ path: '/register', query: route.query }" class="auth-link">有邀请码？创建 Emby 账号</RouterLink>
      </div>
    </LoginForm>
    <template #footer>
      <RouterLink :to="{ path: '/admin/login', query: route.query }" class="auth-link">管理员登录</RouterLink>
    </template>
  </LoginLayout>
</template>
