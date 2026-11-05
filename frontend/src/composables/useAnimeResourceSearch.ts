import { computed, reactive, ref, watch, type Ref } from 'vue'
import { searchGardenResources } from '../lib/garden-search'
import { searchMikanResources, readMikanBangumiResources } from '../lib/anime-resource-api'
import { matchesResourceSpec, resourceSpecOptions, type ResourceSpecSelection, type ResourceSpecOption } from '../utils/resourceSpecFilters'
import type { AnimeResourceItem as Item, AnimeResourceGroup as Group, AnimeResourceBangumi as Bangumi, AnimeResourceExpansion as Expansion, AnimeResourceResult as Result } from '../types/animeResourceSearch'

interface AnimeResourceSearchOptions {
  token: string
  title: string
  originalTitle: string
  provider?: 'mikan' | 'garden'
}
interface AnimeResourceSearchHooks {
  batchMode: Ref<boolean>
  blocked: () => boolean
  resetResultPosition: () => Promise<void>
  appendedResults: (previousVisible: number) => Promise<void>
}

export function useAnimeResourceSearch(options: AnimeResourceSearchOptions, signal: AbortSignal, hooks: AnimeResourceSearchHooks) {
  const isGarden = computed(() => options.provider === 'garden')
  const query = ref(options.title || options.originalTitle)
  const group = ref('')
  const groups = ref<Group[]>([])
  const result = ref<Result>()
  const busy = ref(false)
  const interactionBusy = computed(() => busy.value || hooks.blocked())
  const retainedItems = ref<Item[]>()
  const loadingMessage = ref('')
  const error = ref('')
  const filter = ref('')
  const resolution = ref('全部')
  const specFilters = reactive<ResourceSpecSelection>({ resolution: [], language: [], external: false })
  const specOptions = computed(() => resourceSpecOptions((result.value?.items || []).map(item => item.spec)))
  const specActive = computed(() => !!(specFilters.resolution.length || specFilters.language.length || specFilters.external))
  function resetSpecFilters() { specFilters.resolution = []; specFilters.language = []; specFilters.external = false }
  const visible = ref(50)
  const selectedBangumi = ref<Bangumi>()
  const searchSnapshot = ref<{ result: Result; query: string; group: string; groups: Group[]; filter: string; resolution: string; specFilters: ResourceSpecSelection; visible: number }>()
  // 字幕组检索只更新资源；保留同一关键词的番剧入口，避免顶部条目收缩。
  const bangumis = ref<Bangumi[]>([])
  const expansions = computed(() => (result.value?.more || []).filter(item => !group.value || item.groupId === group.value))
  function specChecked(option: ResourceSpecOption) { return option.group === 'external' ? specFilters.external : specFilters[option.group].includes(option.value) }
  function toggleSpecOption(option: ResourceSpecOption) {
    if (interactionBusy.value) return
    if (option.group === 'external') specFilters.external = !specFilters.external
    else {
      const values = specFilters[option.group]
      specFilters[option.group] = values.includes(option.value) ? values.filter(value => value !== option.value) : [...values, option.value]
    }
  }
  const resolutions = computed(() => ['全部', ...[...new Set(result.value?.items.map(item => item.resolution))].sort((a, b) => b.localeCompare(a, 'en', { numeric: true }))])
  const groupOptions = computed(() => [{ value: '', label: isGarden.value ? '全部发布方' : '全部字幕组' }, ...groups.value.map(item => ({ value: item.id, label: isGarden.value ? `${item.name} · ${item.originName} · ${item.kind === 'fansub' ? '字幕组' : '发布者'}` : item.name }))])
  const resolutionOptions = computed(() => resolutions.value.map(value => ({ value, label: value === '全部' ? '全部分辨率' : value })))
  const filtered = computed(() => {
    if (busy.value && retainedItems.value) return retainedItems.value
    const words = filter.value.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
    return (result.value?.items || []).filter(item => (!result.value?.bangumi || !group.value || item.groupId === group.value) && (hooks.batchMode.value ? matchesResourceSpec(item.spec, specFilters) : resolution.value === '全部' || item.resolution === resolution.value) && words.every(word => item.title.toLocaleLowerCase().includes(word)))
  })
  const siteURL = computed(() => {
    if (isGarden.value) {
      const params = new URLSearchParams({ search: result.value?.query || query.value })
      const selected = groups.value.find(item => item.id === group.value)
      if (selected?.origin) { params.set('provider', selected.origin); params.set(selected.kind || 'publisher', selected.name) }
      return `https://animes.garden/resources/1?${params}`
    }
    if (selectedBangumi.value) return selectedBangumi.value.url
    const params = new URLSearchParams({ searchstr: result.value?.query || query.value })
    if (result.value?.group) params.set('subgroupid', result.value.group)
    return `https://mikanani.me/Home/Search?${params}`
  })
  watch([filter, resolution], () => { visible.value = 50 }, { flush: 'sync' })
  watch(specFilters, () => { visible.value = 50 }, { flush: 'sync' })

  async function search(refresh = false, selectedGroup = '') {
    if (isGarden.value) { await searchGarden(refresh, selectedGroup); return }
    if (interactionBusy.value || !query.value.trim()) return
    const previousGroup = group.value
    const previousBangumi = selectedBangumi.value
    retainedItems.value = filtered.value
    loadingMessage.value = selectedGroup ? `正在读取 ${groups.value.find(item => item.id === selectedGroup)?.name || '该字幕组'} 的搜索结果…` : '正在搜索蜜柑…'
    busy.value = true
    error.value = ''
    group.value = selectedGroup
    // 保留列表、番剧和筛选节点，避免弹窗收缩、滚动跳动及选中按钮失焦。
    try {
      const q = query.value.trim()
      // 只有默认中文标题搜索才自动使用原名兜底，手动改词和字幕组筛选保留用户意图。
      const fallback = q === options.title.trim() && !selectedGroup ? options.originalTitle : ''
      const data = await searchMikanResources(options.token, { query: q, fallback, group: selectedGroup, refresh }, signal)
      if (!selectedGroup || data.query !== result.value?.query) bangumis.value = data.bangumis || []
      result.value = data
      resetSpecFilters()
      selectedBangumi.value = undefined
      query.value = data.query
      if (!selectedGroup) groups.value = data.groups
      searchSnapshot.value = undefined
      visible.value = 50
      resolution.value = '全部'
      await hooks.resetResultPosition()
    } catch (e) {
      group.value = previousGroup
      selectedBangumi.value = previousBangumi
      if (!signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑搜索超时，请稍后重试' : e instanceof Error ? e.message : '蜜柑搜索失败，请稍后重试'
    } finally { busy.value = false; retainedItems.value = undefined }
  }
  async function searchGarden(refresh = false, selectedGroup = '', page = 1) {
    if (interactionBusy.value || page === 1 && !query.value.trim()) return
    const q = page > 1 ? result.value?.query || query.value.trim() : query.value.trim()
    const selected = groups.value.find(item => item.id === selectedGroup)
    const previousVisible = Math.min(visible.value, filtered.value.length)
    let appended = false
    retainedItems.value = filtered.value; busy.value = true; error.value = ''; loadingMessage.value = '正在搜索 Garden…'
    try {
      const data = await searchGardenResources(options.token, { query: q, fallback: q === options.title.trim() && !selectedGroup && page === 1 ? options.originalTitle : '', group: selectedGroup, groupName: selected?.name || '', page, refresh }, signal)
      if (signal.aborted) return
      result.value = page > 1 && result.value ? { ...data, items: [...new Map([...result.value.items, ...data.items].map(item => [item.id, item])).values()] } : data
      if (!selectedGroup) groups.value = page > 1 ? [...new Map([...groups.value, ...data.groups].map(item => [item.id, item])).values()] : data.groups
      query.value = data.query; group.value = selectedGroup
      if (page > 1) { visible.value += 50; appended = true }
      else { visible.value = 50; resolution.value = '全部'; resetSpecFilters(); filter.value = ''; await hooks.resetResultPosition() }
    } catch (reason) { if (!signal.aborted) error.value = reason instanceof Error ? reason.message : 'Garden 搜索失败，请重试' }
    finally {
      busy.value = false; retainedItems.value = undefined
      if (appended && !signal.aborted) {
        await hooks.appendedResults(previousVisible)
      }
    }
  }
  function selectGroup(value: string) {
    if (interactionBusy.value) return
    if (isGarden.value) { void searchGarden(false, value); return }
    if (selectedBangumi.value) { group.value = value; visible.value = 50 }
    else void search(false, value)
  }
  function useTitle(value: string) { if (interactionBusy.value) return; query.value = value; void search() }
  function refreshCurrent() {
    if (isGarden.value) { void searchGarden(true, group.value); return }
    if (selectedBangumi.value) void loadBangumi(selectedBangumi.value, true)
    else void search(true, group.value)
  }
  function backToSearch() {
    if (interactionBusy.value || !searchSnapshot.value) return
    const saved = searchSnapshot.value
    result.value = saved.result
    query.value = saved.query
    group.value = saved.group
    groups.value = saved.groups
    filter.value = saved.filter
    resolution.value = saved.resolution
    Object.assign(specFilters, saved.specFilters)
    visible.value = saved.visible
    selectedBangumi.value = undefined
    searchSnapshot.value = undefined
    error.value = ''
    void hooks.resetResultPosition()
  }
  async function loadBangumi(card: Bangumi, refresh = false, expansion?: Expansion) {
    if (interactionBusy.value) return
    const previousBangumi = selectedBangumi.value
    const previousSnapshot = searchSnapshot.value
    retainedItems.value = filtered.value
    if (!selectedBangumi.value && result.value) {
      // 进入番剧前保留搜索结果及筛选，返回时无需重新搜索。
      searchSnapshot.value = { result: result.value, query: query.value, group: group.value, groups: groups.value, filter: filter.value, resolution: resolution.value, specFilters: { ...specFilters }, visible: visible.value }
    }
    selectedBangumi.value = card
    loadingMessage.value = expansion ? '正在加载该字幕组的更多资源…' : `正在读取 ${card.title} 的番剧资源…`
    busy.value = true
    error.value = ''
    try {
      const next = await readMikanBangumiResources(options.token, { id: card.id, group: expansion?.groupId || '', take: expansion?.take || 0, refresh }, signal)
      if (expansion && result.value) {
        // 原站返回该字幕组的累积列表；替换该组并按磁力去重，保留其他组及其加载进度。
        const rows = [...result.value.items.filter(item => item.groupId !== expansion.groupId), ...next.items]
        result.value = { ...result.value, items: [...new Map(rows.map(item => [item.id, item])).values()].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)), more: [...(result.value.more || []).filter(item => item.groupId !== expansion.groupId), ...(next.more || [])] }
      } else {
        result.value = next
        groups.value = next.groups
        if (previousBangumi?.id !== card.id) filter.value = ''
        group.value = ''
        resolution.value = '全部'
        resetSpecFilters()
        visible.value = 50
        await hooks.resetResultPosition()
      }
    } catch (e) {
      selectedBangumi.value = previousBangumi
      searchSnapshot.value = previousSnapshot
      if (!signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑番剧读取超时，可重试或返回搜索结果' : e instanceof Error ? e.message : '蜜柑番剧读取失败，请稍后重试'
    } finally { busy.value = false; retainedItems.value = undefined }
  }
  function selectBangumi(card: Bangumi) {
    if (selectedBangumi.value?.id !== card.id) void loadBangumi(card)
  }

  return { isGarden, interactionBusy, query, group, groups, result, busy, loadingMessage, error, filter, resolution, specFilters, specOptions, specActive, resetSpecFilters, visible, selectedBangumi, bangumis, expansions, groupOptions, resolutionOptions, filtered, siteURL, specChecked, toggleSpecOption, search, searchGarden, selectGroup, useTitle, refreshCurrent, backToSearch, loadBangumi, selectBangumi }
}
