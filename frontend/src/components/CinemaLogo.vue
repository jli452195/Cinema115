<script setup lang="ts">
defineProps<{ compact?: boolean; sidebar?: boolean; large?: boolean }>()
</script>

<template>
  <span class="cinema-logo" :class="{ 'is-compact': compact, 'in-sidebar': sidebar, 'is-large': large }" role="img" aria-label="Cinema115">
    <img class="cinema-logo-mark" src="/brand/cinema115-mark.svg" width="128" height="123" alt="" aria-hidden="true" draggable="false" />
    <span v-if="!compact" class="cinema-logo-wordmark" aria-hidden="true">Cinema<span class="cinema-logo-number">115</span></span>
  </span>
</template>

<style scoped>
@font-face {
  font-family: 'Cinema115 Brand';
  src: url('/brand/inter-medium.woff2') format('woff2');
  font-style: normal;
  font-weight: 500;
  font-display: swap;
}

.cinema-logo {
  --cinema-logo-mark-width: 40px;
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: calc(var(--cinema-logo-mark-width) * 0.2578125);
  vertical-align: middle;
  user-select: none;
}
.cinema-logo-mark { display: block; width: var(--cinema-logo-mark-width); height: auto; flex-shrink: 0; }
.cinema-logo-wordmark {
  color: var(--cinema-logo-ink, #192235);
  font-family: 'Cinema115 Brand', Inter, Arial, sans-serif;
  font-size: calc(var(--cinema-logo-mark-width) * 0.4609375);
  font-style: normal;
  font-weight: 500;
  letter-spacing: -0.03em;
  line-height: 1.20339;
  white-space: nowrap;
}
.cinema-logo-number { color: var(--cinema-logo-blue, #3165ee); }
.dark .cinema-logo-wordmark { color: var(--cinema-logo-ink, #f1f4fc); }
.dark .cinema-logo-number { color: var(--cinema-logo-blue, #7595ff); }
.cinema-logo.in-sidebar { --cinema-logo-mark-width: 36px; }
.cinema-logo.is-compact { --cinema-logo-mark-width: 32px; gap: 0; }
.cinema-logo.is-large { --cinema-logo-mark-width: 56px; }

@media (max-width: 639px) {
  .cinema-logo:not(.is-large) { --cinema-logo-mark-width: 32px; }
  .cinema-logo:not(.in-sidebar):not(.is-large) .cinema-logo-wordmark { display: none; }
}
</style>
