<script setup lang="ts">
import '../styles/auth.css'

defineProps<{ subtitle: string }>()
</script>

<template>
  <main class="auth-page">
    <div class="auth-content">
      <header class="auth-brand">
        <div class="auth-brand-name">
          <svg class="auth-brand-logo" viewBox="0 0 48 48" fill="none" aria-hidden="true"><rect width="48" height="48" rx="13" fill="currentColor" /><rect x="10" y="13" width="28" height="22" rx="3" stroke="white" stroke-width="2" /><path d="M16 13v22M32 13v22M10 20h6M10 28h6M32 20h6M32 28h6" stroke="white" stroke-width="2" /><path d="m21 19 8 5-8 5V19Z" fill="white" /></svg>
          <h1>Cinema115</h1>
        </div>
        <p>{{ subtitle }}</p>
      </header>
      <slot />
      <nav v-if="$slots.footer" aria-label="登录入口" class="auth-footer"><slot name="footer" /></nav>
    </div>
  </main>
</template>
