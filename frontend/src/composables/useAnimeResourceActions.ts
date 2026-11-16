import { computed, nextTick, ref } from 'vue'
import { useCloudDownloadBatch, type BatchStatus } from './useCloudDownloadBatch'
import { magnetKey } from '../lib/cloud-download-batch'
import { loadAnimeOrganizeFiles } from '../lib/anime-resource-api'
import { readDmhyMagnet } from '../lib/dmhy-search'
import { useCloudDownloadAction } from './useCloudDownloadAction'
import type { DownloadFiles, DownloadResourceInput, DownloadOrganizeResource } from '../types/organize'
import type { AnimeResourceItem as Item, AnimeResourceGroup as Group, AnimeResourceProvider } from '../types/animeResourceSearch'

interface AnimeResourceActionOptions {
  token: string
  provider?: AnimeResourceProvider
  tmdbId?: number
  searching: () => boolean
  filtered: () => Item[]
  groups: () => Group[]
  updateMagnet?: (id: string, magnet: string) => void
}

export function useAnimeResourceActions(options: AnimeResourceActionOptions, signal: AbortSignal, batch: ReturnType<typeof useCloudDownloadBatch>) {
  const { selected: batchSelected, count: batchCount } = batch
  const preparing = ref<{ id: string; kind: 'files' | 'magnet' }>()
  const preparingItem = computed(() => preparing.value?.kind === 'files' ? preparing.value.id : '')
  const readingMagnet = computed(() => preparing.value?.kind === 'magnet' ? preparing.value.id : '')
  const busy = computed(() => batch.running.value || !!preparing.value)
  const interactionBusy = computed(() => options.searching() || busy.value)
  const submissions = ref<Record<string, BatchStatus & { organize?: boolean }>>({})
  const cloudDownload = useCloudDownloadAction(options.token, signal, submissions)
  const singleSubmitting = cloudDownload.busy
  function submission(item: Item) { return batch.statuses.value[magnetKey(item.magnet)] || submissions.value[magnetKey(item.magnet)] }
  function canSelect(item: Item) { return batch.eligible(item) && !submission(item)?.success && !submission(item)?.busy && !submission(item)?.uncertain }
  const availableSelection = computed(() => [...new Map(options.filtered().filter(canSelect).map(item => [magnetKey(item.magnet), item])).values()])
  const allFilteredSelected = computed(() => availableSelection.value.length > 0 && availableSelection.value.every(item => batchSelected.value.has(magnetKey(item.magnet))))
  const hiddenSelectionCount = computed(() => {
    const shown = new Set(options.filtered().map(item => magnetKey(item.magnet)))
    return [...batchSelected.value.keys()].filter(key => !shown.has(key)).length
  })
  function toggleResource(item: Item) {
    if (interactionBusy.value || !canSelect(item)) return
    batch.toggle(item)
  }
  function allowReselectAfterCheck() {
    batch.allowReselectAfterCheck()
    for (const [key, state] of Object.entries(submissions.value)) if (state.uncertain) delete submissions.value[key]
  }
  const organizeEntries = ref<DownloadOrganizeResource[]>([])
  const organizingBatch = ref(false), organizeHiddenCount = ref(0)
  const prepareNotices = ref<Record<string, string>>({})
  function canReadMagnet(item: Item) { return options.provider === 'dmhy' && !magnetKey(item.magnet) && item.magnetIssue !== 'conflict' }
  async function readMagnet(item: Item, event: MouseEvent) {
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || !canReadMagnet(item) || !options.updateMagnet) return
    const returnFocus = event.currentTarget as HTMLElement
    const resourceRow = returnFocus.closest<HTMLElement>('.mikan-resource')
    preparing.value = { id: item.id, kind: 'magnet' }
    delete prepareNotices.value[item.id]
    try {
      const magnet = await readDmhyMagnet(options.token, item.id, signal)
      if (!signal.aborted) options.updateMagnet(item.id, magnet)
    } catch (reason) {
      if (!signal.aborted) prepareNotices.value[item.id] = reason instanceof Error ? reason.message : '读取原帖磁力失败，请查看原帖'
    } finally {
      preparing.value = undefined
      if (!signal.aborted) {
        await nextTick()
        // 成功后读取按钮被移除，将焦点留在同一资源的复制按钮。
        if (!returnFocus.isConnected) resourceRow?.querySelector<HTMLElement>('.mikan-copy-button')?.focus({ preventScroll: true })
      }
    }
  }
  function organizeSource(item: Item): DownloadResourceInput {
    const fansub = item.fansub || options.groups().find(group => (options.provider !== 'garden' || group.kind === 'fansub') && (group.id === item.groupId || item.title.startsWith(`[${group.name}]`) || item.title.startsWith(`【${group.name}】`)))?.name
    if (options.provider === 'dmhy') return { source: 'dmhy', title: item.title, fansub, providerId: item.id, magnet: item.magnet }
    if (options.provider !== 'garden') return { source: 'mikan', title: item.title, fansub, providerId: item.id, torrentUrl: item.torrentUrl, magnet: item.magnet }
    const [provider, providerId] = item.id.split(':')
    return { source: 'garden', title: item.title, fansub, provider, providerId: providerId || '', magnet: item.magnet }
  }
  function organizeEntry(item: Item, files?: DownloadFiles): DownloadOrganizeResource {
    return { resource: item, source: organizeSource(item), files }
  }
  function openBatchOrganize(event: MouseEvent) {
    if (organizeEntries.value.length || interactionBusy.value || singleSubmitting.value || !options.tmdbId || !batchCount.value) return
    const entries = ([...batchSelected.value.values()] as Item[]).filter(canSelect).map(item => organizeEntry(item))
    if (!entries.length) return
    ;(event.currentTarget as HTMLElement).focus({ preventScroll: true })
    organizingBatch.value = true; organizeHiddenCount.value = hiddenSelectionCount.value; organizeEntries.value = entries
  }
  async function openOrganize(item: Item, event: MouseEvent) {
    if (organizeEntries.value.length || interactionBusy.value || singleSubmitting.value || !options.tmdbId || !magnetKey(item.magnet) || submission(item)?.success || submission(item)?.uncertain) return
    const returnFocus = event.currentTarget as HTMLElement
    returnFocus.focus({ preventScroll: true })
    preparing.value = { id: item.id, kind: 'files' }
    delete prepareNotices.value[item.id]
    try {
      const data = await loadAnimeOrganizeFiles(options.token, organizeSource(item), signal)
      if (!data.files.length) { prepareNotices.value[item.id] = data.reason || '没有可整理的文件清单，可使用普通离线'; return }
      preparing.value = undefined
      await nextTick()
      if (signal.aborted) return
      returnFocus?.focus({ preventScroll: true })
      organizingBatch.value = false; organizeHiddenCount.value = 0; organizeEntries.value = [organizeEntry(item, data)]
    } catch (e) { if (!signal.aborted) prepareNotices.value[item.id] = e instanceof Error ? e.message : '读取文件清单失败，可使用普通离线' }
    finally {
      if (!signal.aborted) {
        preparing.value = undefined
        await nextTick()
        if (!organizeEntries.value.length && returnFocus.isConnected && document.activeElement === document.body) returnFocus.focus({ preventScroll: true })
      }
    }
  }
  function organizeUncertain(keys: string[]) {
    for (const key of keys) {
      submissions.value[key] = { organize: true, uncertain: true, message: '下载整理任务创建结果待确认，请在原编辑页重试确认或查看整理任务记录。' }
      batch.statuses.value[key] = submissions.value[key]!
    }
  }
  function organized(keys: string[]) {
    for (const key of keys) {
      submissions.value[key] = { organize: true, success: true }
      batch.statuses.value[key] = submissions.value[key]!
      batchSelected.value.delete(key)
    }
    organizeEntries.value = []
  }
  async function submit(item: Item) {
    if (signal.aborted || interactionBusy.value || !magnetKey(item.magnet) || submission(item)?.busy || submission(item)?.success || submission(item)?.uncertain) return
    const key = magnetKey(item.magnet)
    delete batch.statuses.value[key]
    const status = await cloudDownload.submit(item.magnet, key)
    if (!status || signal.aborted) return
    if (status.uncertain) batch.statuses.value[key] = status
    if (status.success || status.uncertain) batchSelected.value.delete(key)
  }

  return { busy, preparingItem, readingMagnet, canReadMagnet, readMagnet, singleSubmitting, submissions, submission, canSelect, availableSelection, allFilteredSelected, hiddenSelectionCount, toggleResource, allowReselectAfterCheck, organizeEntries, organizingBatch, organizeHiddenCount, prepareNotices, openBatchOrganize, openOrganize, organizeUncertain, organized, submit }
}
