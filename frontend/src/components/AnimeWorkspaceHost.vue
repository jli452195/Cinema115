<script setup lang="ts">
import { provide, shallowReactive } from 'vue'
import { routeLocationKey, useRoute, type RouteLocationNormalizedLoaded } from 'vue-router'
import AnimeCatalogView from '../views/AnimeCatalogView.vue'
import AnimeSubscriptionsView from '../views/AnimeSubscriptionsView.vue'
const props = defineProps<{ token: string; location: RouteLocationNormalizedLoaded }>()
defineEmits<{ logout: [] }>()
// Host controls read the retained host query while the address identifies an
// open modal. The host instance remains mounted across modal navigation.
const retained = {} as RouteLocationNormalizedLoaded
for (const key of Object.keys(useRoute())) Object.defineProperty(retained, key, { enumerable: true, get: () => Reflect.get(props.location, key) })
provide(routeLocationKey, shallowReactive(retained))
</script>
<template>
  <AnimeSubscriptionsView v-if="location.path.startsWith('/anime/subscriptions')" :token="token" @logout="$emit('logout')" />
  <AnimeCatalogView v-else :token="token" @logout="$emit('logout')" />
</template>
