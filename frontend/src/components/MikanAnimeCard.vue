<script setup lang="ts">
import { computed } from 'vue'
import type { AnimeCatalogStatus, MikanCatalogItem } from '../types/animeCatalog'
import AnimePosterCard from './AnimePosterCard.vue'
import AnimeCatalogSubscriptionBadge from './AnimeCatalogSubscriptionBadge.vue'
import { animeCatalogCardLabel, animeCatalogSubscriptionTarget } from '../utils/animeCatalogSubscription'
const props = defineProps<{ item: MikanCatalogItem; returnTo: string; subscription?: AnimeCatalogStatus; priority?: boolean }>()
const target = computed(() => animeCatalogSubscriptionTarget(props.subscription, '', 'mikan', props.item.id, props.returnTo))
const cover = computed(() => /^\/api\/mikan\/image\/\d{6}\/[a-zA-Z0-9._-]+$/.test(props.item.coverUrl) ? props.item.coverUrl : '')
</script>

<template>
  <AnimePosterCard :to="target" class="mikan-anime-card" :data-mikan-id="item.id" :label="animeCatalogCardLabel(item.title, subscription)" :title="item.title" :cover-url="cover" :priority="priority">
    <template v-if="subscription?.subscriptions.length" #badge><AnimeCatalogSubscriptionBadge :state="subscription" /></template>
    <template #meta><span class="poster-meta-label">{{ item.updatedDate ? `资源更新 ${item.updatedDate}` : '蜜柑资源条目' }}</span></template>
  </AnimePosterCard>
</template>
