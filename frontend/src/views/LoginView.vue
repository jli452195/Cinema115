<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import LoginLayout from '../components/LoginLayout.vue'
import LoginForm from '../components/LoginForm.vue'

defineProps<{ registeredUsername?: string }>()
const emit = defineEmits<{ loggedIn: [token: string] }>()
const route = useRoute()
</script>

<template>
  <LoginLayout subtitle="发现与浏览你的影视世界">
    <LoginForm endpoint="/api/auth/login" title="登录个人中心" description="使用 Emby 账号和密码登录个人中心" username-label="Emby 用户名" submit-label="登录个人中心" :initial-username="registeredUsername" @logged-in="emit('loggedIn', $event)">
      <p v-if="registeredUsername" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">Emby 账号创建成功，请使用刚设置的密码登录。</p>
      <div class="space-y-2 text-center text-sm">
        <p class="text-slate-500 dark:text-slate-400">已有 Emby 账号？直接登录即可</p>
        <RouterLink to="/register" class="inline-block text-blue-600 hover:underline dark:text-blue-300">使用邀请码创建 Emby 账号</RouterLink>
      </div>
    </LoginForm>
    <template #footer>
      <RouterLink :to="{ path: '/admin/login', query: route.query }" class="rounded text-sm text-slate-400 transition hover:text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-blue-500">管理入口</RouterLink>
    </template>
  </LoginLayout>
</template>
