<script setup lang="ts">
import TabbedWorkspace from '../components/TabbedWorkspace.vue'
import { portalUser } from '../composables/usePortalAuth'
import { managementCategories } from '../utils/managementFeatures'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()

const { title, description, features } = managementCategories.telegram
const routeNames = {
  '/telegram/user': 'telegram-user',
  '/telegram/bot': 'telegram',
  '/telegram/relay': 'telegram-relay',
} as const
const tabs = features.map(feature => ({ name: routeNames[feature.to], label: feature.title, to: feature.to }))
</script>

<template>
  <TabbedWorkspace :title="title" :description="description" label="Telegram 配置导航" :tabs="tabs" fill-tabs-on-mobile mobile-back-to-origin @logout="$emit('logout')">
    <template #icon="{ tab }">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
        <template v-if="tab.name === 'telegram-user'">
          <path d="M2 21a8 8 0 0 1 13.292-6" />
          <circle cx="10" cy="8" r="5" />
          <path d="m16 19 2 2 4-4" />
        </template>
        <template v-else-if="tab.name === 'telegram'">
          <path d="M12 8V4H8" />
          <rect width="16" height="12" x="4" y="8" rx="2" />
          <path d="M2 14h2" />
          <path d="M20 14h2" />
          <path d="M15 13v2" />
          <path d="M9 13v2" />
        </template>
        <template v-else-if="tab.name === 'telegram-relay'">
          <polyline points="15 17 20 12 15 7" />
          <path d="M4 18v-2a4 4 0 0 1 4-4h12" />
        </template>
      </svg>
    </template>
    <template #default="{ component }"><component :is="component" :key="portalUser?.id" :token="token" /></template>
  </TabbedWorkspace>
</template>
