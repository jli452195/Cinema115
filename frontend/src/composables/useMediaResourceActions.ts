import { computed, nextTick, ref } from 'vue'
import { magnetKey } from '../lib/cloud-download-batch'
import { p115Request } from '../lib/p115-api'
import type { DownloadFiles, DownloadOrganizeResource, DownloadResourceInput } from '../types/organize'
import type { ResourceLink, ResourceSearchItem } from '../types/resourceSearch'
import { normalize115Share } from '../utils/shareLink'
import { requestErrorMessage } from '../utils/requestErrorMessage'
import { useShareSTRMRun, type ShareSTRMResource } from './useShareSTRMRun'
import { useCloudDownloadAction, type CloudDownloadActionStatus } from './useCloudDownloadAction'

interface MediaResourceActionOptions {
  source: 'gying' | 'woniu'
  token: string
  kind: 'movie' | 'tv'
  tmdbId: string
}

interface CloudDownloadStatus extends CloudDownloadActionStatus { organize?: boolean }

export function useMediaResourceActions(options: MediaResourceActionOptions, signal: AbortSignal, searching: () => boolean) {
  const shareRuns = useShareSTRMRun(options.token, signal, { scope: `${options.source}/${options.kind}/${options.tmdbId}` })
  const cloudStatuses = ref<Record<string, CloudDownloadStatus>>({})
  const downloads = useCloudDownloadAction(options.token, signal, cloudStatuses)
  const cloudSubmitting = downloads.pending
  const organizeEntries = ref<DownloadOrganizeResource[]>([])
  const preparingOrganize = ref('')
  const organizeNotices = ref<Record<string, string>>({})
  const busy = computed(() => shareRuns.preparing.value > 0 || !!preparingOrganize.value || organizeEntries.value.length > 0)
  let organizeTrigger: HTMLElement | undefined

  function shareResource(item: ResourceSearchItem, link: ResourceLink): ShareSTRMResource {
    const normalized = normalize115Share(link.url)
    const password = link.password?.trim() || ''
    const shares = item.links.filter(canShareSTRM)
    const title = link.workTitle?.trim() || item.title
    return {
      id: shareResourceID(item, link),
      identity: `${options.source}/${options.kind}/${options.tmdbId}/${item.id}/${normalized}/${password}`,
      description: [options.source === 'woniu' ? '蜗牛' : '观影', shares.length > 1 ? '分享 ' + (shares.findIndex(entry => entry.url === link.url && entry.password === link.password) + 1) : '', title, link.size && !title.includes(link.size) ? link.size : ''].filter(Boolean).join(' · '),
      load: async () => [{ url: link.url, password }],
    }
  }
  function shareResourceID(item: ResourceSearchItem, link: ResourceLink) { return `${item.id}\u0000${normalize115Share(link.url)}\u0000${link.password?.trim() || ''}` }
  function canShareSTRM(link: ResourceLink) { return link.type === '115' && normalize115Share(link.url) !== '' }
  function restoreShareRuns(items: ResourceSearchItem[]) { void shareRuns.restore(items.flatMap(item => item.links.filter(canShareSTRM).map(link => shareResource(item, link)))) }
  function shareSTRM(item: ResourceSearchItem, link: ResourceLink) { void shareRuns.submit(shareResource(item, link)) }
  function stopShareRun() { void shareRuns.stop() }
  function canCloudDownload(link: ResourceLink) { return link.type === 'magnet' && link.category === 'torrent' }
  function cloudResourceID(item: ResourceSearchItem, link: ResourceLink) { return magnetKey(link.url) || `${item.id}\u0000${link.url}` }
  function canOrganize(link: ResourceLink) { return options.source === 'gying' && !!link.hasTorrent && canCloudDownload(link) && Number(options.tmdbId) > 0 }
  function downloadBlocked(item: ResourceSearchItem, link: ResourceLink) {
    const id = cloudResourceID(item, link), status = cloudStatuses.value[id]
    return signal.aborted || searching() || busy.value || cloudSubmitting.value[id] || status?.success || status?.uncertain
  }

  async function openOrganize(item: ResourceSearchItem, link: ResourceLink, event: MouseEvent) {
    if (!canOrganize(link) || downloadBlocked(item, link)) return
    const id = cloudResourceID(item, link)
    organizeTrigger = event.currentTarget as HTMLElement
    organizeTrigger.focus({ preventScroll: true })
    preparingOrganize.value = id
    delete organizeNotices.value[id]
    delete cloudStatuses.value[id]
    const [provider, providerId] = item.id.split(':')
    const source: DownloadResourceInput = { source: 'gying', title: link.workTitle || item.title, provider, providerId: providerId || '', magnet: link.url }
    try {
      const files = await p115Request<DownloadFiles>(options.token, 'download-organize/files', { method: 'POST', body: JSON.stringify(source), signal: AbortSignal.any([signal, AbortSignal.timeout(50000)]) })
      if (signal.aborted) return
      if (!files.files.length) { organizeNotices.value[id] = files.reason || '没有可整理的视频，可使用普通离线'; return }
      preparingOrganize.value = ''
      await nextTick()
      if (signal.aborted) return
      organizeTrigger.focus({ preventScroll: true })
      organizeEntries.value = [{ resource: { id, title: source.title!, magnet: link.url }, source, files }]
    } catch (reason) {
      if (!signal.aborted) {
        const message = requestErrorMessage(reason, '读取种子清单').replace(/。+$/, '')
        organizeNotices.value[id] = message.includes('普通离线') ? message : `${message}。可使用普通离线。`
      }
    } finally {
      preparingOrganize.value = ''
      if (!organizeEntries.value.length && !signal.aborted) {
        await nextTick()
        if (document.activeElement === document.body) organizeTrigger?.focus({ preventScroll: true })
      }
    }
  }

  function organized(keys: string[], uncertain = false) {
    for (const key of keys) cloudStatuses.value[key] = { success: !uncertain, uncertain, organize: true, message: uncertain ? '任务创建结果待核对，请查看整理任务记录。' : undefined }
  }

  async function closeOrganize() {
    organizeEntries.value = []
    await nextTick()
    if (signal.aborted || !organizeTrigger?.isConnected) return
    const target = organizeTrigger.matches(':disabled') ? organizeTrigger.closest('[data-resource-row]')?.querySelector<HTMLElement>('[data-gy-action="open"]') : organizeTrigger
    target?.focus({ preventScroll: true })
  }

  async function cloudDownload(item: ResourceSearchItem, link: ResourceLink) {
    if (!canCloudDownload(link) || downloadBlocked(item, link)) return
    const id = cloudResourceID(item, link)
    delete organizeNotices.value[id]
    return await downloads.submit(link.url, id)
  }

  return { busy, cloudBusy: downloads.busy, shareRuns, cloudSubmitting, cloudStatuses, organizeEntries, preparingOrganize, organizeNotices, shareResourceID, canShareSTRM, restoreShareRuns, shareSTRM, stopShareRun, canCloudDownload, cloudResourceID, canOrganize, downloadBlocked, openOrganize, organized, closeOrganize, cloudDownload }
}
