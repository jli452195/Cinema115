import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { portalToken, portalUser } from './usePortalAuth'
import { invalidateAnimeSubscriptions } from './useAnimeSubscriptionBrowser'
import { AnimeConfigError, animeConfigMessage, animeConfigRequest, animeRequestKey } from '../lib/anime-config-api'
import type { AnimeSettings, AnimeSettingsView } from '../types/animeSubscription'

type DirectoryKind = 'download' | 'organize'
interface PendingDirectory { kind: DirectoryKind; accountId: string; cid: string; path: string; key: string; body: unknown }
// An uncertain save belongs to the signed-in administrator, not the dialog's
// lifetime. Reopening any entry point must recover this exact request.
const pending = ref<PendingDirectory>(), saving = ref(false)
let identity = 0
watch([portalToken, () => portalUser.value?.id, () => portalUser.value?.role], () => {
  identity++; pending.value = undefined; saving.value = false
}, { flush: 'sync' })

export function useAnimeStorageSettings(token: string, changed: (settings: AnimeSettingsView) => void) {
  const settings = ref<AnimeSettingsView>(), loading = ref(false), readError = ref(''), error = ref(''), notice = ref('')
  const owner = portalUser.value?.id, controller = new AbortController()
  let disposed = false
  const owns = () => !disposed && token === portalToken.value && owner === portalUser.value?.id && portalUser.value?.role === 'admin'
  async function readSettings() {
    if (loading.value || !owns()) return
    loading.value = true; readError.value = ''
    try {
      const result = await animeConfigRequest<AnimeSettingsView>(token, 'settings', { signal: controller.signal })
      if (owns()) settings.value = result
    } catch (reason) { if (owns()) readError.value = animeConfigMessage(reason) }
    finally { if (!disposed) loading.value = false }
  }
  async function confirmPending() {
    const request = pending.value, current = identity
    if (!request || saving.value || !owns()) return
    saving.value = true; error.value = ''; notice.value = ''
    try {
      const response = await animeConfigRequest<AnimeSettings | { settings: AnimeSettings }>(token, request.path, { method: 'PATCH', key: request.key, body: request.body })
      if (current !== identity) return
      const result = 'settings' in response ? response.settings : response
      if (!result || !Number.isSafeInteger(result.revision) || result.revision < 1 || result.accountId !== request.accountId || !result.downloadCid || (request.kind === 'download' ? result.downloadCid : result.organizeCid) !== request.cid) {
        throw new AnimeConfigError('目录保存回执无法确认，请核对原请求', 'INVALID_RECEIPT', 0, {}, true)
      }
      pending.value = undefined; invalidateAnimeSubscriptions()
      if (owns()) {
        if (settings.value) {
          const primary = settings.value.primaryAccount
          const ready = !!primary && primary.id === result.accountId && primary.revision === result.accountRevision && primary.uid === result.uid
          settings.value = { ...settings.value, settings: result, readiness: { ready, issues: ready ? [] : ['ACCOUNT_SNAPSHOT_CHANGED'] } }
        }
        notice.value = '目录已保存'
        await readSettings()
        if (owns() && settings.value) changed(settings.value)
      }
    } catch (reason) {
      if (current !== identity) return
      if (!(reason instanceof AnimeConfigError && reason.uncertain)) pending.value = undefined
      if (owns()) error.value = animeConfigMessage(reason)
    } finally { if (current === identity) saving.value = false }
  }
  function saveDirectory(kind: DirectoryKind, cid: string) {
    if (pending.value || saving.value || loading.value || !owns()) return Promise.resolve()
    const view = settings.value, primary = view?.primaryAccount
    if (!view || !primary || kind === 'organize' && !view.readiness.ready) return Promise.resolve()
    pending.value = { kind, accountId: primary.id, cid, key: animeRequestKey(),
      path: kind === 'organize' ? 'settings/organize-directory' : 'settings',
      body: kind === 'organize' ? { expectedRevision: view.settings.revision, cid }
        : { expectedRevision: view.settings.revision, downloadCid: cid, organizeCid: view.settings.organizeCid, expectedPrimaryAccountId: primary.id, expectedPrimaryAccountRevision: primary.revision } }
    return confirmPending()
  }
  onBeforeUnmount(() => { disposed = true; controller.abort() })
  return { settings, loading, readError, error, notice, pending, saving, locked: computed(() => saving.value || !!pending.value || loading.value), readSettings, saveDirectory, confirmPending }
}
