import { computed, nextTick, ref, watch } from 'vue'
import { useCloudDownloadBatch, type BatchStatus } from './useCloudDownloadBatch'
import { magnetKey } from '../lib/cloud-download-batch'
import { loadAnimeOrganizeFiles } from '../lib/anime-resource-api'
import { readDmhyMagnet } from '../lib/dmhy-search'
import { useCloudDownloadAction } from './useCloudDownloadAction'
import type { ConfirmOrganizeAction } from './useOrganizeDraftGuard'
import type { DownloadFiles, DownloadResourceInput, DownloadOrganizeResource } from '../types/organize'
import type { AnimeResourceItem as Item, AnimeResourceGroup as Group, AnimeResourceProvider } from '../types/animeResourceSearch'

interface AnimeResourceActionOptions {
  token: string
  provider?: AnimeResourceProvider
  tmdbId?: number
  kind?: 'movie' | 'tv'
  searching: () => boolean
  filtered: () => Item[]
  groups: () => Group[]
  updateMagnet?: (id: string, magnet: string) => void
  confirmOrganizeAction: ConfirmOrganizeAction
}

export function useAnimeResourceActions(options: AnimeResourceActionOptions, signal: AbortSignal, batch: ReturnType<typeof useCloudDownloadBatch>) {
  const { selected: batchSelected, count: batchCount } = batch
  const preparing = ref<{ id: string; kind: 'files' | 'magnet' }>()
  const preparingItem = computed(() => preparing.value?.kind === 'files' ? preparing.value.id : '')
  const readingMagnet = computed(() => preparing.value?.kind === 'magnet' ? preparing.value.id : '')
  const organizeEntries = ref<DownloadOrganizeResource[]>([])
  const organizeVisible = ref(false)
  const busy = computed(() => batch.running.value || !!preparing.value || organizeVisible.value)
  const interactionBusy = computed(() => options.searching() || busy.value)
  const submissions = ref<Record<string, BatchStatus & { organize?: boolean }>>({})
  const cloudDownload = useCloudDownloadAction(options.token, signal, submissions)
  const singleSubmitting = cloudDownload.busy
  function submission(item: Pick<Item, 'magnet'>) { return batch.statuses.value[magnetKey(item.magnet)] || submissions.value[magnetKey(item.magnet)] }
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
  const organizingBatch = ref(false), organizeHiddenCount = ref(0)
  const prepareNotices = ref<Record<string, string>>({})
  let organizeDraftScope = '', organizeDraftKey = '', organizeGeneration = 0
  let organizeController: AbortController | undefined
  // 单项或普通批量云下载接收了草稿中的资源后，整份旧草稿失效。
  watch(() => !organizeVisible.value && organizeEntries.value.some(entry => submission(entry.resource)?.success), success => { if (success) discardOrganize() }, { flush: 'sync' })
  function organizeScope() { return JSON.stringify([options.provider || 'mikan', options.kind || 'tv', options.tmdbId]) }
  function organizeSelectionKey(items: { magnet: string }[]) { return JSON.stringify([...new Set(items.map(item => magnetKey(item.magnet)))].sort()) }
  const batchDraftMatchesSelection = computed(() => organizingBatch.value && organizeEntries.value.length > 0 && organizeDraftScope === organizeScope() && (!batchCount.value || organizeDraftKey === organizeSelectionKey([...batchSelected.value.values()])))
  function hasOrganizeDraft(item: Item) {
    const key = magnetKey(item.magnet)
    return !!key && organizeDraftScope === organizeScope() && organizeEntries.value.some(entry => magnetKey(entry.resource.magnet) === key) && !organizeEntries.value.some(entry => submission(entry.resource)?.success)
  }
  function resumeOrganizeDraft(event: MouseEvent) {
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || !options.tmdbId || !organizeEntries.value.length || organizeDraftScope !== organizeScope() || organizeEntries.value.some(entry => submission(entry.resource)?.success || submission(entry.resource)?.busy)) return false
    ;(event.currentTarget as HTMLElement).focus({ preventScroll: true })
    organizeVisible.value = true
    return true
  }
  function resumeBatchOrganizeDraft(event: MouseEvent) { return organizingBatch.value && resumeOrganizeDraft(event) }
  function closeOrganize() { organizeVisible.value = false }
  function confirmCloudDownload(items: readonly Pick<Item, 'magnet'>[]) {
    const draftKeys = new Set(organizeEntries.value.map(entry => magnetKey(entry.resource.magnet)))
    if (!items.some(item => draftKeys.has(magnetKey(item.magnet)))) return true
    return options.confirmOrganizeAction('download')
  }
  function discardOrganize() {
    organizeGeneration++
    organizeController?.abort(); organizeController = undefined
    if (preparing.value?.kind === 'files') preparing.value = undefined
    organizeEntries.value = []
    organizingBatch.value = false; organizeHiddenCount.value = 0
    organizeDraftScope = ''; organizeDraftKey = ''
    closeOrganize()
  }
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
  async function openBatchOrganize(event: MouseEvent) {
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || !options.tmdbId) return
    // 用完整选择集合匹配草稿，不能先过滤掉原批次的待核对资源。
    if (batchDraftMatchesSelection.value) { resumeBatchOrganizeDraft(event); return }
    if (!batchCount.value) return
    const selected = [...batchSelected.value.values()] as Item[]
    const draftScope = organizeScope(), draftKey = organizeSelectionKey(selected)
    const entries = selected.filter(canSelect).map(item => organizeEntry(item))
    if (!entries.length) return
    const replacing = organizeEntries.value.length > 0, hiddenCount = hiddenSelectionCount.value
    const returnFocus = event.currentTarget as HTMLElement
    if (replacing && !await options.confirmOrganizeAction('replace')) return
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || draftScope !== organizeScope() || draftKey !== organizeSelectionKey([...batchSelected.value.values()])) return
    discardOrganize()
    const generation = organizeGeneration
    returnFocus.focus({ preventScroll: true })
    preparing.value = { id: entries[0]!.resource.id, kind: 'files' }
    try {
      if (replacing) await nextTick()
      if (signal.aborted || generation !== organizeGeneration) return
      organizeDraftScope = draftScope; organizeDraftKey = draftKey
      organizingBatch.value = true; organizeHiddenCount.value = hiddenCount; organizeEntries.value = entries
      if (generation === organizeGeneration) organizeVisible.value = true
    } finally { if (generation === organizeGeneration) preparing.value = undefined }
  }
  async function openOrganize(item: Item, event: MouseEvent) {
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || !options.tmdbId || !magnetKey(item.magnet) || submission(item)?.success || submission(item)?.busy) return
    if (hasOrganizeDraft(item)) { resumeOrganizeDraft(event); return }
    if (submission(item)?.uncertain) return
    const replacing = organizeEntries.value.length > 0
    const draftScope = organizeScope(), draftKey = organizeSelectionKey([item]), entry = organizeEntry(item)
    const returnFocus = event.currentTarget as HTMLElement
    if (replacing && !await options.confirmOrganizeAction('replace')) return
    if (signal.aborted || interactionBusy.value || singleSubmitting.value || draftScope !== organizeScope() || submission(item)?.success || submission(item)?.uncertain) return
    discardOrganize()
    const generation = organizeGeneration
    const controller = new AbortController()
    organizeController = controller
    returnFocus.focus({ preventScroll: true })
    preparing.value = { id: item.id, kind: 'files' }
    delete prepareNotices.value[item.id]
    try {
      if (replacing) await nextTick()
      if (signal.aborted || generation !== organizeGeneration) return
      const data = await loadAnimeOrganizeFiles(options.token, entry.source, AbortSignal.any([signal, controller.signal]))
      if (signal.aborted || generation !== organizeGeneration) return
      if (!data.files.length) { prepareNotices.value[item.id] = data.reason || '没有可整理的文件清单，可使用普通离线'; return }
      entry.files = data
      organizeDraftScope = draftScope; organizeDraftKey = draftKey
      organizingBatch.value = false; organizeHiddenCount.value = 0; organizeEntries.value = [entry]
      if (generation === organizeGeneration) organizeVisible.value = true
    } catch (e) { if (!signal.aborted && generation === organizeGeneration) prepareNotices.value[item.id] = e instanceof Error ? e.message : '读取文件清单失败，可使用普通离线' }
    finally {
      if (generation === organizeGeneration) { preparing.value = undefined; organizeController = undefined }
      if (!signal.aborted && generation === organizeGeneration) {
        await nextTick()
        if (!signal.aborted && generation === organizeGeneration && !organizeEntries.value.length && returnFocus.isConnected && document.activeElement === document.body) returnFocus.focus({ preventScroll: true })
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
    discardOrganize()
  }
  async function submit(item: Item) {
    if (signal.aborted || interactionBusy.value || !magnetKey(item.magnet) || submission(item)?.busy || submission(item)?.success || submission(item)?.uncertain) return
    if (!await confirmCloudDownload([item]) || signal.aborted || interactionBusy.value) return
    const key = magnetKey(item.magnet)
    delete batch.statuses.value[key]
    const status = await cloudDownload.submit(item.magnet, key)
    if (!status || signal.aborted) return
    if (status.uncertain) batch.statuses.value[key] = status
    if (status.success || status.uncertain) batchSelected.value.delete(key)
  }

  return { busy, preparingItem, readingMagnet, canReadMagnet, readMagnet, singleSubmitting, submissions, submission, canSelect, availableSelection, allFilteredSelected, hiddenSelectionCount, toggleResource, allowReselectAfterCheck, organizeEntries, organizeVisible, organizingBatch, organizeHiddenCount, prepareNotices, hasOrganizeDraft, batchDraftMatchesSelection, openBatchOrganize, resumeBatchOrganizeDraft, openOrganize, closeOrganize, organizeUncertain, organized, confirmCloudDownload, submit }
}
