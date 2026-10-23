<script setup lang="ts">
import { computed, nextTick } from 'vue'
import { useRoute, useRouter, type RouteLocationNormalizedLoaded } from 'vue-router'
import AnimeWorkspaceHost from '../components/AnimeWorkspaceHost.vue'
import AnimeSubscriptionDialog from '../components/AnimeSubscriptionDialog.vue'
import AnimeNewSubscriptionView from './AnimeNewSubscriptionView.vue'
import { animeReturnPath } from '../composables/useAnimeSubscriptionBrowser'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const isNew = computed(() => route.name === 'anime-subscription-new')
const id = computed(() => String(route.params.id || ''))
const modal = computed(() => isNew.value || !!id.value)
const hostPath = computed(() => {
  if (!modal.value) return route.fullPath
  const target = animeReturnPath(route.query.returnTo || (isNew.value ? window.history.state?.animeReturnTo : undefined))
  if (target && (router.resolve(target).path === '/anime' || router.resolve(target).path === '/anime/subscriptions')) return target
  if (isNew.value) return '/anime'
  return router.resolve({ path: '/anime/subscriptions', query: { status: route.query.status, q: route.query.q, page: route.query.page, bangumiId: route.query.bangumiId } }).fullPath
})
const host = computed(() => router.resolve(hostPath.value) as RouteLocationNormalizedLoaded)
async function close() {
  const focusedID = id.value
  const sourceItem = window.history.state?.animeSource?.itemId
  const sourceSite = window.history.state?.animeSource?.provider
  await router.replace(hostPath.value)
  await nextTick()
  const trigger = [...document.querySelectorAll<HTMLElement>('[data-subscription-id], [data-mikan-id], [data-anibt-id], [data-garden-id]')].find(el => !!focusedID && el.dataset.subscriptionId === focusedID || !!sourceItem && (sourceSite === 'mikan' && el.dataset.mikanId === sourceItem || sourceSite === 'anibt' && el.dataset.anibtId === sourceItem || sourceSite === 'garden' && el.dataset.gardenId === sourceItem))
  trigger?.focus({ preventScroll: true })
}
function openExisting(id: string) { void router.replace({ path: `/anime/subscriptions/${encodeURIComponent(id)}`, query: { returnTo: hostPath.value } }) }
</script>
<template>
  <AnimeWorkspaceHost :token="token" :location="host" @logout="$emit('logout')" />
  <AnimeNewSubscriptionView v-if="isNew" :token="token" @close="close" @saved="close" @open="openExisting" />
  <AnimeSubscriptionDialog v-else-if="id" :key="id" :token="token" :id="id" @close="close" @saved="close" @open="openExisting" />
</template>
