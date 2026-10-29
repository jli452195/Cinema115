<script setup lang="ts">
import { RouterLink, RouterView, useRoute } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute()
const sections = [
  { name: 'media-organize', label: '整理管理' },
  { name: 'naming-settings', label: '命名模板' },
  { name: 'classification-settings', label: '分类规则' },
]
</script>

<template>
  <ManagementLayout title="自动整理" description="管理目录监控、整理记录、公共命名模板与分类规则。" @logout="$emit('logout')">
    <nav class="organize-sections" aria-label="整理功能">
      <RouterLink v-for="section in sections" :key="section.name" :to="route.name === section.name ? route.fullPath : { name: section.name }" :aria-current="route.name === section.name ? 'page' : undefined">{{ section.label }}</RouterLink>
    </nav>
    <RouterView v-slot="{ Component }"><component :is="Component" :token="token" /></RouterView>
  </ManagementLayout>
</template>

<style scoped>
.organize-sections { display: flex; align-items: center; gap: 26px; margin-bottom: 22px; overflow-x: auto; scrollbar-width: none; }
.organize-sections a { position: relative; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; min-height: 44px; padding: 0 3px 9px; white-space: nowrap; font-size: 14px; color: rgb(var(--color-slate-500)); }
.organize-sections a[aria-current=page] { color: #2563eb; font-weight: 600; }
.organize-sections a[aria-current=page]::after { content: ''; position: absolute; bottom: 0; left: 3px; right: 3px; height: 2px; border-radius: 2px; background: currentColor; }
.organize-sections a:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; border-radius: 4px; }
.dark .organize-sections a[aria-current=page] { color: #93c5fd; }
@media (hover: hover) { .organize-sections a:hover { color: #2563eb; }.dark .organize-sections a:hover { color: #93c5fd; } }
@media (max-width: 639px) { .organize-sections { gap: 22px; margin-bottom: 18px; }.organize-sections a { font-size: 13px; } }
</style>
