<script setup lang="ts">
import '../styles/auth.css'
import CinemaLogo from './CinemaLogo.vue'

defineProps<{ subtitle: string }>()
</script>

<template>
  <main class="auth-page">
    <div class="auth-content">
      <header class="auth-brand">
        <div class="auth-brand-name">
          <h1><CinemaLogo large /></h1>
        </div>
        <p>{{ subtitle }}</p>
      </header>
      <slot />
      <nav v-if="$slots.footer" aria-label="登录入口" class="auth-footer"><slot name="footer" /></nav>
    </div>
  </main>
</template>
