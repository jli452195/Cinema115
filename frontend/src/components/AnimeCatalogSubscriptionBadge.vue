<script setup lang="ts">
import { computed } from 'vue'
import { IconArchive, IconBookmark, IconCheck, IconPlayerPause } from '@tabler/icons-vue'
import type { AnimeCatalogStatus } from '../types/animeCatalog'
import { animeCatalogSubscriptionLabel } from '../utils/animeCatalogSubscription'
const props = defineProps<{ state?: AnimeCatalogStatus }>()
const status = computed(() => props.state?.subscriptions.length === 1 ? props.state.subscriptions[0]!.status : 'multiple')
const icon = computed(() => status.value === 'enabled' ? IconCheck : status.value === 'paused' ? IconPlayerPause : status.value === 'archived' ? IconArchive : IconBookmark)
</script>

<template>
  <span v-if="state?.subscriptions.length" class="catalog-subscription-badge" :data-status="status"><component :is="icon" :size="12" aria-hidden="true" />{{ animeCatalogSubscriptionLabel(state) }}</span>
</template>

<style scoped>
.catalog-subscription-badge { position: absolute; top: 8px; right: 8px; display: inline-flex; align-items: center; gap: 4px; height: 22px; max-width: calc(100% - 16px); padding: 0 7px; border-radius: 6px; background: rgb(15 23 42 / .85); color: #fff; font-size: 10px; line-height: 1; white-space: nowrap; pointer-events: none; }
.catalog-subscription-badge svg { flex-shrink: 0; color: #93c5fd; }
.catalog-subscription-badge[data-status="enabled"] svg { color: #6ee7b7; }
.catalog-subscription-badge[data-status="paused"] svg { color: #fde68a; }
.catalog-subscription-badge[data-status="archived"] svg { color: #cbd5e1; }
</style>
