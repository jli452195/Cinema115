<script setup lang="ts">
import { computed } from 'vue'
import { IconStar } from '@tabler/icons-vue'
import type { AnimeCatalogStatus, AnibtCatalogItem } from '../types/animeCatalog'
import AnimePosterCard from './AnimePosterCard.vue'
import AnimeCatalogSubscriptionBadge from './AnimeCatalogSubscriptionBadge.vue'
import { animeCatalogCardLabel, animeCatalogSubscriptionTarget } from '../utils/animeCatalogSubscription'
const props = defineProps<{ item: AnibtCatalogItem; returnTo: string; subscription?: AnimeCatalogStatus; priority?: boolean }>()
const cover = computed(() => /^\/api\/anibt\/image\/[a-zA-Z0-9_-]{1,64}\/[a-f0-9]{64}\.webp$/.test(props.item.coverUrl) ? props.item.coverUrl : '')
const target = computed(() => animeCatalogSubscriptionTarget(props.subscription, props.item.bangumiId, 'anibt', props.item.id, props.returnTo))
const schedule = computed(() => props.item.airingAt && Number.isFinite(props.item.airingAt) ? new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false }).format(props.item.airingAt * 1000) : '')
</script>

<template>
  <AnimePosterCard :to="target" class="anibt-anime-card" :data-anibt-id="item.id" :label="animeCatalogCardLabel(item.title, subscription)" :title="item.title" :subtitle="item.originalTitle !== item.title ? item.originalTitle : ''" :cover-url="cover" :sticker="schedule" :format="item.format" :priority="priority">
    <template v-if="subscription?.subscriptions.length" #badge><AnimeCatalogSubscriptionBadge :state="subscription" /></template>
    <template #meta><span class="poster-meta-label">{{ item.episodes ? `全 ${item.episodes} 集` : '集数待定' }}</span><span v-if="item.rating != null && item.rating > 0" class="poster-rating"><IconStar :size="12" aria-hidden="true" />{{ item.rating.toFixed(1) }}<span class="sr-only">分</span></span></template>
    <template #status><span class="poster-resource-status" :class="{ 'has-resources': item.releaseCount > 0 }">{{ item.releaseCount ? `${item.releaseCount} 条资源` : '暂无资源' }}</span></template>
  </AnimePosterCard>
</template>
