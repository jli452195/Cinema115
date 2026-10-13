<script setup lang="ts">
import { ref, useId } from 'vue'
import AuthPasswordField from './AuthPasswordField.vue'
import AuthSpinner from './AuthSpinner.vue'
import { useAuthRequest } from '../composables/useAuthRequest'

const props = defineProps<{
  endpoint: '/api/auth/login' | '/api/auth/admin/login'
  title: string
  description: string
  usernameLabel: string
  submitLabel: string
  initialUsername?: string
  initialPassword?: string
  usernameReadonly?: boolean
  passwordReadonly?: boolean
}>()
const emit = defineEmits<{ loggedIn: [token: string] }>()
const id = useId()
const username = ref(props.initialUsername || '')
const password = ref(props.initialPassword || '')
const message = ref('')
const loading = ref(false)
const { request, retrySeconds, isActive } = useAuthRequest()

async function login() {
  if (loading.value || retrySeconds.value) return
  loading.value = true
  message.value = ''
  try {
    const data = await request<{ token?: string }>(props.endpoint, { username: username.value, password: password.value })
    if (typeof data?.token !== 'string' || !data.token) throw new Error('登录响应异常，请重试')
    if (isActive()) emit('loggedIn', data.token)
  } catch (error) {
    if (isActive()) message.value = error instanceof Error && error.name === 'TimeoutError' ? '登录请求超时，请检查网络后重试' : error instanceof Error && error.name === 'TypeError' ? '暂时无法连接登录服务，请检查网络后重试' : error instanceof Error ? error.message : '暂时无法登录，请稍后重试'
  } finally { loading.value = false }
}
</script>

<template>
  <form class="auth-card" :aria-busy="loading" @submit.prevent="login">
    <div class="auth-heading">
      <h2>{{ title }}</h2>
      <p>{{ description }}</p>
    </div>
    <slot name="before-fields" />
    <fieldset class="auth-fields" :disabled="loading">
      <div class="auth-field">
        <label :for="`${id}-username`">{{ usernameLabel }}</label>
        <input :id="`${id}-username`" v-model="username" name="username" required autocomplete="username" autocapitalize="none" autocorrect="off" :spellcheck="false" maxlength="128" class="auth-input" :readonly="usernameReadonly" :placeholder="`输入${usernameLabel}`" />
      </div>
      <AuthPasswordField :id="`${id}-password`" v-model="password" label="密码" autocomplete="current-password" placeholder="输入密码" :readonly="passwordReadonly" />
    </fieldset>
    <div class="auth-actions">
      <p v-if="message" role="alert" class="auth-notice auth-notice-error !mb-0">{{ message }}</p>
      <button type="submit" class="auth-button" :disabled="loading || retrySeconds > 0"><AuthSpinner v-if="loading" />{{ loading ? '正在验证登录' : submitLabel }}</button>
    </div>
    <p v-if="retrySeconds" role="status" class="auth-cooldown">请在 {{ retrySeconds }} 秒后重试</p>
    <slot />
  </form>
</template>
