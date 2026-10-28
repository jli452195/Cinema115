import { computed, onBeforeUnmount, ref, toValue, watch, type MaybeRef } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { invalidateAnimeSubscriptions } from './useAnimeSubscriptionBrowser'
import { animeConfigRequest, AnimeConfigError, animeConfigMessage, animeRequestKey } from '../lib/anime-config-api'

interface PendingAction { path: string; body: unknown; key: string; label: string; method?: string }
const pending = ref<Record<string, PendingAction>>({})
let identity = 0
watch([() => portalUser.value?.id, () => portalUser.value?.role], () => { identity++; pending.value = {} }, { flush: 'sync' })

export function useAnimeSubscriptionActions<T extends { id: string; revision: number; status?: string } = { id: string; revision: number; status?: string }>(token: MaybeRef<string>, id: string, changed: (receipt: T) => Promise<void>, scope = id) {
  const loading = ref(false), error = ref(''), notice = ref('')
  const current = computed(() => pending.value[scope])
  const owner = portalUser.value?.id
  let disposed = false
  const owns = () => toValue(token) === portalToken.value && owner === portalUser.value?.id && portalUser.value?.role === 'admin'
  async function submit(request: PendingAction) {
    if (loading.value || !owns()) return
    const currentIdentity = identity
    loading.value = true; error.value = ''; notice.value = ''
    try {
      const receipt = await animeConfigRequest<T>(toValue(token), request.path, { method: request.method || 'POST', body: request.body, key: request.key })
      if (!receipt.id || !Number.isSafeInteger(receipt.revision) || receipt.revision < 1) throw new AnimeConfigError('操作回执无法确认，请核对原操作', 'INVALID_RECEIPT', 0, {}, true)
      if (currentIdentity !== identity) return
      delete pending.value[scope]; invalidateAnimeSubscriptions(id)
      if (!disposed) { notice.value = `${request.label}已完成`; await changed(receipt) }
    } catch (reason) {
      if (currentIdentity !== identity) return
      if (!(reason instanceof AnimeConfigError && reason.uncertain)) delete pending.value[scope]
      if (!disposed) error.value = animeConfigMessage(reason)
    } finally { if (!disposed) loading.value = false }
  }
  function execute(path: string, body: unknown, label: string, method = 'POST') {
    if (current.value || loading.value || !owns()) return Promise.resolve()
    // Retain exactly this key and body across close/reopen; never substitute a
    // newer revision into an uncertain request.
    const request = { path, method, body: JSON.parse(JSON.stringify(body)) as unknown, key: animeRequestKey(), label }
    pending.value[scope] = request
    return submit(request)
  }
  onBeforeUnmount(() => { disposed = true })
  return { loading, error, notice, pending: current, locked: computed(() => loading.value || !!current.value), execute, confirmPending: () => current.value ? submit(current.value) : Promise.resolve() }
}
