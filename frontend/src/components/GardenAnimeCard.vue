<script setup lang="ts">
import { computed } from 'vue'
import type { AnimeCatalogStatus, GardenCatalogItem } from '../types/animeCatalog'
import AnimePosterCard from './AnimePosterCard.vue'
import AnimeCatalogSubscriptionBadge from './AnimeCatalogSubscriptionBadge.vue'
import { animeCatalogCardLabel, animeCatalogSubscriptionTarget } from '../utils/animeCatalogSubscription'
const props = defineProps<{ item: GardenCatalogItem; returnTo: string; subscription?: AnimeCatalogStatus; priority?: boolean }>()
const cover = computed(() => /^\/api\/garden\/image\/[1-9]\d{0,7}\/poster\.jpeg$/.test(props.item.coverUrl) ? props.item.coverUrl : '')
const target = computed(() => animeCatalogSubscriptionTarget(props.subscription, props.item.bangumiId, 'garden', props.item.id, props.returnTo))
</script>

<template>
  <AnimePosterCard :to="target" class="garden-anime-card" :data-garden-id="item.id" :label="animeCatalogCardLabel(item.title, subscription)" :title="item.title" :subtitle="item.originalTitle !== item.title ? item.originalTitle : ''" :cover-url="cover" :format="item.platform" :priority="priority">
    <template v-if="subscription?.subscriptions.length" #badge><AnimeCatalogSubscriptionBadge :state="subscription" /></template>
    <template #meta><span class="poster-meta-label">{{ item.airDate ? `${item.airDate} 首播` : '首播日期待定' }}</span></template>
  </AnimePosterCard>
</template>
