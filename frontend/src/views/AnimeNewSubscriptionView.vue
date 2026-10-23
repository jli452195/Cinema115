<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import AnimeFloatingDialog from '../components/AnimeFloatingDialog.vue'
import AnimeSubscriptionDialog from '../components/AnimeSubscriptionDialog.vue'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { requestMikanCatalogEntry } from '../composables/useAnimeCatalog'
import { requestAnimeCatalogStatuses } from '../composables/useAnimeCatalogStatus'
import { animeReturnPath } from '../composables/useAnimeSubscriptionBrowser'
import { animeConfigMessage } from '../lib/anime-config-api'
import { animeEditorEntry } from '../utils/animeEditorEntry'
import { animeCatalogSubscriptionTarget } from '../utils/animeCatalogSubscription'
import type { AnimeSourceProvider } from '../types/animeCatalog'
import '../styles/animeSubscriptionEditor.css'

const props = defineProps<{ token: string }>()
defineEmits<{ close: []; saved: [id: string]; open: [id: string] }>()
const route = useRoute(), router = useRouter(), ready = ref(false), error = ref('')
const browser = window
const sourceSeed = computed(() => { const initial = browser.history.state?.animeSource; return { provider: String(initial?.provider || route.query.source || ''), itemId: String(initial?.itemId || route.query.itemId || '') } })
const returnTo = computed(() => animeReturnPath(route.query.returnTo || window.history.state?.animeReturnTo) || '/anime')
let controller: AbortController | undefined
function cancel() { controller?.abort(); controller = undefined }
async function resolveEntry() {
  cancel(); error.value = ''; ready.value = false
  const origin = window.history.state?.animeSource
  const seed = origin?.bangumiId === String(route.query.bangumiId || '') ? origin : undefined
  const itemId = String(seed?.itemId || '')
  if (!['mikan', 'anibt', 'garden'].includes(seed?.provider) || !/^[1-9]\d{0,11}$/.test(itemId)) { ready.value = true; return }
  const provider = seed.provider as AnimeSourceProvider
  const active = new AbortController(); controller = active
  try {
    let status = (await requestAnimeCatalogStatuses(props.token, provider, [itemId], active.signal))[itemId]
    if (active.signal.aborted || controller !== active) return
    if (status?.subscriptions.length) { await router.replace(animeCatalogSubscriptionTarget(status, '', provider, itemId, returnTo.value)); return }
    if (route.query.bangumiId || provider !== 'mikan') { ready.value = true; return }
    if (status?.bangumiId) { await router.replace(animeEditorEntry(status.bangumiId, provider, itemId, returnTo.value)); return }
    const entry = await requestMikanCatalogEntry(props.token, itemId, active.signal)
    if (active.signal.aborted || controller !== active) return
    status = (await requestAnimeCatalogStatuses(props.token, provider, [itemId], active.signal, true))[itemId]
    if (active.signal.aborted || controller !== active) return
    if (status?.subscriptions.length) { await router.replace(animeCatalogSubscriptionTarget(status, '', provider, itemId, returnTo.value)); return }
    const ids = [...new Set(entry.bangumiIds.map(String).filter(id => /^[1-9]\d{0,11}$/.test(id)))]
    if (ids.length === 1) await router.replace(animeEditorEntry(ids[0]!, 'mikan', itemId, returnTo.value))
    else ready.value = true
  } catch (reason) { if (!active.signal.aborted && controller === active) error.value = animeConfigMessage(reason) }
}
watch([() => route.fullPath, () => props.token, portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => { void resolveEntry() }, { immediate: true })
onBeforeRouteLeave(cancel); onBeforeRouteUpdate(cancel); onBeforeUnmount(cancel)
</script>

<template>
  <AnimeSubscriptionDialog v-if="ready" :token="token" :bangumi-id="String(route.query.bangumiId || '')" :seed="sourceSeed" @close="$emit('close')" @saved="$emit('saved', $event)" @open="$emit('open', $event)" />
  <AnimeFloatingDialog v-else title="新增订阅" close-label="关闭新增订阅" size="config" @close="$emit('close')">
    <div v-if="error" class="ae-load-error" role="status"><p>{{ error }}</p><button type="button" class="ae-button" @click="resolveEntry">重新读取</button></div>
    <p v-else class="ae-loading" role="status" aria-busy="true">正在读取番剧与订阅来源…</p>
  </AnimeFloatingDialog>
</template>
