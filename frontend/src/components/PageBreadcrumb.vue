<script setup lang="ts">
import { RouterLink } from 'vue-router'
import NavigationIcon from './NavigationIcon.vue'

defineProps<{ parent: { label: string; to: string } }>()
</script>

<template>
  <nav aria-label="上级导航" class="page-parent-navigation">
    <RouterLink :to="parent.to" class="page-parent-link" :aria-label="`返回 ${parent.label}`" :title="`返回 ${parent.label}`">
      <NavigationIcon name="back" :size="16" /><span>返回 {{ parent.label }}</span>
    </RouterLink>
  </nav>
</template>

<style scoped>
.page-parent-navigation { min-width: 0; margin-left: -8px; }
.page-parent-link { display: inline-flex; min-width: 44px; max-width: 100%; min-height: 36px; align-items: center; gap: 6px; padding: 0 8px; border-radius: 10px; color: rgb(var(--color-slate-600)); font-size: 12px; line-height: 20px; }
.page-parent-link svg { flex-shrink: 0; }
.page-parent-link span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.page-parent-link:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; }
.dark .page-parent-link { color: rgb(var(--color-slate-300)); }
@media (max-width: 1023px) { .page-parent-link { min-height: 44px; } }
@media (hover: hover) { .page-parent-link:hover { background: rgb(116 143 180 / 8%); color: #476d9d; }.dark .page-parent-link:hover { color: #bad2f4; } }
@media (hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference) {
  .page-parent-link span { transform-origin: left center; transition: transform 180ms ease-out; }
  .page-parent-link svg { transition: transform 180ms ease-out; }
  .page-parent-link:hover span { transform: scale(1.035); }
  .page-parent-link:hover svg { transform: scale(1.06); }
}
</style>
