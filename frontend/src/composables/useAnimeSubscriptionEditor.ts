import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { invalidateAnimeSubscriptions } from './useAnimeSubscriptionBrowser'
import { useAnimeNaming } from './useAnimeNaming'
import { portalUser } from './usePortalAuth'
import { AnimeDiscoveryError, cachedAnime, requestAnime } from './useAnimeDiscovery'
import { animeCatalogItemSnapshot, requestMikanCatalogEntry } from './useAnimeCatalog'
import { AnimeConfigError, animeConfigMessage, animeConfigRequest, animeRequestKey } from '../lib/anime-config-api'
import { animeProviderName, animeBindingKey, animeSourceConfig, animeSourceReady, newAnimeSource, type SourceForm } from '../utils/animeSourceForm'
import type { AnimeSubjectResult } from '../types/anime'
import type { AnimePreviewResponse, AnimeSaveReceipt, AnimeSettingsView, AnimeSourceSample, AnimeSubscriptionView, MikanEntry, MikanSearch } from '../types/animeSubscription'

interface PendingWrite { path: string; method: string; key: string; body: unknown }
export function useAnimeSubscriptionEditor(token: Ref<string>, subscriptionID: Ref<string>, bangumiID: Ref<string>, seed?: Ref<{ provider: string; itemId: string }>) {
  const sources = ref<SourceForm[]>([]), activeRef = ref('main')
  const subject = ref<{ id: string; title: string; coverUrl: string; date: string; bangumiUnavailable?: boolean; needsSource?: boolean }>()
  const local = ref<AnimeSubscriptionView>(), settings = ref<AnimeSettingsView>()
  const archiveTotal = ref(''), defaultTotal = ref<number | null>(null)
  const loading = ref(true), loadError = ref(''), feedback = ref(''), settingsError = ref('')
  const previewBusy = ref(''), saveBusy = ref(false), settingsBusy = ref(false)
  const pendingSave = ref<PendingWrite>(), receipt = ref<AnimeSaveReceipt>()
  const duplicateID = ref(''), conflict = ref(false)
  const now = ref(Date.now())
  const removed = ref<{ id: string; expectedRevision: number }[]>([])
  const baseline = ref(''), draftKey = ref(animeRequestKey())
  let lifetime = new AbortController(), generation = 0
  const searchControllers = new Map<string, AbortController>()
  const sampleControllers = new Map<string, AbortController>()
  const samples = new Map<string, { data: AnimeSourceSample; until: number }>()
  const clock = window.setInterval(() => { now.value = Date.now() }, 10000)
  const active = computed(() => sources.value.find(source => source.ref === activeRef.value))
  const locked = computed(() => saveBusy.value || settingsBusy.value || !!pendingSave.value || !!receipt.value || loading.value || !!loadError.value)
  const naming = useAnimeNaming(token, subject, sources, () => lifetime.signal, locked)
  function rawForm() {
    const keys: (keyof SourceForm)[] = ['provider', 'mappingSource', 'mappingTarget', 'mappingSeason', 'mappingLast', 'mappingManual', 'mappingSegments', 'mappingOverrides', 'ref', 'id', 'role', 'runWhenEnabled', 'itemId', 'groupId', 'groupName', 'scope', 'first', 'last', 'evidence', 'confirmed', 'confirmedFor', 'review', 'identityNumbering', 'correspondence', 'sourceEpisode', 'chapterEpisode', 'templateChosen', 'match', 'episodePattern', 'episodeGroup', 'minimum', 'exclude', ]
    return JSON.stringify({ archiveTotal: archiveTotal.value, chapterStarts: sources.value.map(s => s.chapterFirst), naming: naming.signature.value, removed: removed.value, sources: sources.value.map(source => Object.fromEntries(keys.map(key => [key, source[key]]))) })
  }
  const dirty = computed(() => !!baseline.value && rawForm() !== baseline.value && !receipt.value)
  const submittedSources = computed(() => sources.value.filter(s => s.role === 'main' || s.id || s.itemId || s.groupId))
  const resumesArchived = computed(() => local.value?.status === 'archived' && submittedSources.value.some(source =>
    source.runWhenEnabled && !local.value?.sources.some(prior => prior.id === source.id && prior.runWhenEnabled)))
  const archiveSuggestion = computed(() => {
    const target = naming.target.value
    if (defaultTotal.value || !target || target.groupId || target.season < 1) return null
    const count = target.seasons.find(season => season.number === target.season)?.episodeCount
    if (!count || !Number.isInteger(count) || count < 1 || count > 99999) return null
    // A season is only a suggestion for ordinary whole-season numbering.
    // Never silently substitute it for a chapter, offset or custom arrangement.
    const enabled = submittedSources.value.filter(source => source.runWhenEnabled)
    if (!enabled.length || enabled.some(source =>
      !['0', '1'].includes(source.mappingSource) || source.mappingTarget !== source.mappingSource || Number(source.mappingSeason || target.season) !== target.season ||
      source.mappingSegments.length > 0 || source.mappingOverrides.length > 0 ||
      !!source.mappingLast && Number(source.mappingLast) !== count ||
      !!source.chapterFirst && Number(source.chapterFirst) !== 1 ||
      source.correspondence && (source.sourceEpisode !== '1' || source.chapterEpisode !== '1') ||
      source.scope === 'range' && (Number(source.first) !== 1 || Number(source.last) !== count)
    )) return null
    return count
  })
  function useTMDBTotal() {
    if (!locked.value && !archiveTotal.value && !naming.busy.value && !naming.searching.value && archiveSuggestion.value) archiveTotal.value = String(archiveSuggestion.value)
  }
  const startIssue = computed(() => {
    if (subject.value?.needsSource) return '请先选择番剧来源'
    if (!settings.value?.readiness.ready || !settings.value.settings.organizeCid) return '请在设置中补齐下载与整理目录'
    if (!submittedSources.value.some(s => s.role === 'main' && s.runWhenEnabled)) return '请启用主 RSS'
    if (!naming.target.value) return '请选择 TMDB 命名目标'
    try { if (!naming.input()) return '请选择 TMDB 命名目标' } catch (reason) { return animeConfigMessage(reason) }
    const incomplete = submittedSources.value.find(s => !animeSourceReady(s))
    if (incomplete) return `请完成${incomplete.role === 'main' ? '主 RSS' : '备用 RSS'}来源与规则`
    return ''
  })
  function entryRef(source = sources.value.find(s => s.role === 'main')): { id?: string; bangumiId?: string; editionKey?: string } {
    if (local.value) return { id: local.value.entryId }
    const id = subject.value?.id
    const linked = !subject.value?.bangumiUnavailable || source?.entry?.bangumiIds.includes(id || '')
    return { ...(id && linked ? { bangumiId: id } : {}), editionKey: 'original' }
  }
  function chapter() { return local.value ? { id: local.value.entry.id, bangumiId: local.value.entry.bangumiId, editionKey: local.value.entry.editionKey, revision: local.value.entry.revision, draftKey: '' } : { id: '', bangumiId: entryRef().bangumiId || null, editionKey: 'original', revision: 0, draftKey: draftKey.value } }
  function signature(source: SourceForm) {
    try {
      const { position: _p, role: _r, status: _s, runWhenEnabled: _e, groupName: _g, ...config } = animeSourceConfig(source, 0)
      return JSON.stringify({ config, chapter: chapter(), settings: settings.value?.settings.revision, subscriptionID: local.value?.id || '' })
    } catch { return '' }
  }
  function validPreview(source: SourceForm) { return !!source.preview && source.preview.signature === signature(source) && !source.preview.summary.expired && source.preview.summary.expiresAt > Math.max(now.value, Date.now()) }
  function setCooldown(source: SourceForm, reason: unknown, kind: 'search' | 'preview') {
    if (reason instanceof AnimeConfigError && reason.status === 429) {
      const until = Number(reason.details.retryAfterAt) || Date.now() + 60000
      if (kind === 'search') source.cooldown = until
      else source.previewCooldown = until
    }
  }
  async function initialize() {
    const current = ++generation
    archiveTotal.value = ''; defaultTotal.value = null
    lifetime.abort(); lifetime = new AbortController(); searchControllers.forEach(c => c.abort()); searchControllers.clear()
    sampleControllers.forEach(c => c.abort()); sampleControllers.clear(); samples.clear()
    sources.value = []; local.value = undefined; subject.value = undefined; settings.value = undefined; removed.value = []
    receipt.value = undefined; pendingSave.value = undefined; baseline.value = ''; draftKey.value = animeRequestKey()
    loading.value = true; saveBusy.value = false; settingsBusy.value = false; loadError.value = ''; feedback.value = ''; duplicateID.value = ''; conflict.value = false; settingsError.value = ''; previewBusy.value = ''
    if (!token.value || portalUser.value?.role !== 'admin') { loadError.value = '请使用管理员身份重新登录'; loading.value = false; return }
    const signal = lifetime.signal
    let seedIssue = ''
    async function newConfiguration(): Promise<AnimeSubjectResult & { sourceEntry?: MikanEntry; bangumiUnavailable?: boolean; needsSource?: boolean }> {
      let sourceEntry: MikanEntry | undefined
      if (seed?.value.provider || seed?.value.itemId) {
        if (!['mikan', 'anibt', 'garden'].includes(seed.value.provider) || !/^[1-9]\d{0,11}$/.test(seed.value.itemId)) throw new Error('订阅站点或条目无效，请返回该站点重新选择')
        try {
          sourceEntry = seed.value.provider === 'mikan'
            ? await requestMikanCatalogEntry(token.value, seed.value.itemId, signal)
            : (await animeConfigRequest<{ entry: MikanEntry }>(token.value, `sources/${seed.value.provider}/${seed.value.itemId}`, { signal })).entry
          if (sourceEntry.id !== seed.value.itemId) throw new Error('来源条目不一致，请重新选择')
        } catch (reason) {
          if (!bangumiID.value || signal.aborted || reason instanceof AnimeConfigError && [401, 403].includes(reason.status)) throw reason
          seedIssue = '入口来源暂时无法读取，可在 RSS 中重新选择站点。'
        }
      }
      if (/^[1-9]\d*$/.test(bangumiID.value)) {
        const cached = cachedAnime<AnimeSubjectResult>(`subject:${bangumiID.value}`)
        try {
          const result = cached?.fresh ? cached.data : await requestAnime<AnimeSubjectResult>(`subject:${bangumiID.value}`, `subjects/${bangumiID.value}`, token.value, signal)
          return { ...result, sourceEntry }
        } catch (reason) {
          if (!(reason instanceof AnimeDiscoveryError && reason.status === 404 && reason.code === 'UPSTREAM_NOT_FOUND') || signal.aborted) throw reason
          const card = sourceEntry && animeCatalogItemSnapshot(seed?.value.provider || '', sourceEntry.id)
          return { sourceEntry, bangumiUnavailable: true, needsSource: !sourceEntry,
            subject: { id: sourceEntry && !sourceEntry.bangumiIds.includes(bangumiID.value) ? '' : bangumiID.value, title: sourceEntry?.title || '选择番剧来源', originalTitle: '', coverUrl: sourceEntry?.coverUrl || card?.coverUrl || '', date: sourceEntry?.airDate || card?.date || '', weekday: null, url: '', synopsis: '', platform: '', episodes: null, totalEpisodes: null },
            metadata: { fetchedAt: 0, expiresAt: 0, stale: false, dataVersion: '', issue: null } }
        }
      }
      if (bangumiID.value || !sourceEntry) throw new Error('缺少有效番剧条目，请返回选番')
      const card = animeCatalogItemSnapshot(seed?.value.provider || '', sourceEntry.id)
      return { sourceEntry, subject: { id: '', title: sourceEntry.title, originalTitle: '', coverUrl: sourceEntry.coverUrl || card?.coverUrl || '', date: sourceEntry.airDate || card?.date || '', weekday: null, url: '', synopsis: '', platform: '', episodes: null, totalEpisodes: null }, metadata: { fetchedAt: 0, expiresAt: 0, stale: false, dataVersion: '', issue: null } }
    }
    const configPromise = subscriptionID.value
      ? animeConfigRequest<AnimeSubscriptionView>(token.value, `subscriptions/${encodeURIComponent(subscriptionID.value)}`, { signal })
      : newConfiguration()
    const results = await Promise.allSettled([configPromise, animeConfigRequest<AnimeSettingsView>(token.value, 'settings', { signal })])
    if (current !== generation || signal.aborted) return
    const [config, setting] = results
    if (setting.status === 'fulfilled') settings.value = setting.value
    else settingsError.value = animeConfigMessage(setting.reason)
    if (config.status === 'rejected') loadError.value = animeConfigMessage(config.reason)
    else if (subscriptionID.value) {
      const view = config.value as AnimeSubscriptionView
      {
        local.value = view
        archiveTotal.value = view.form?.totalEpisodes ? String(view.form.totalEpisodes) : ''
        defaultTotal.value = view.entry.metadata.totalEpisodes || null
        subject.value = { id: view.entry.bangumiId || '', title: view.entry.title, coverUrl: view.entry.metadata?.coverUrl || '', date: view.entry.metadata?.airDate || '', bangumiUnavailable: view.entry.metadata?.bangumiUnavailable }
        sources.value = view.sources.map(saved => {
          const form = newAnimeSource(saved.role, view.entry.title, saved.provider)
          Object.assign(form, { ref: saved.id, id: saved.id, revision: saved.revision, runWhenEnabled: saved.runWhenEnabled,
            chapterFirst: saved.rules.chapterFirstEpisode ? String(saved.rules.chapterFirstEpisode) : '',
            itemId: saved.itemId || '', itemTitle: saved.itemId ? `${animeProviderName(saved.provider)}条目 ${saved.itemId}` : '', groupId: saved.groupId || '', groupName: saved.groupName || '', lockedItem: !!saved.itemId, lockedGroup: !!saved.groupId,
            scope: saved.binding.scope?.kind || '', first: saved.binding.scope?.kind === 'range' ? String(saved.binding.scope.first) : '', last: saved.binding.scope?.kind === 'range' ? String(saved.binding.scope.last) : '', evidence: saved.binding.scope?.kind === 'confirmed_chapter' ? saved.binding.scope.evidence || '' : '', confirmed: saved.binding.confirmed, review: saved.binding.review,
            identityNumbering: saved.binding.chapterCorrespondence?.sourceEpisode === 1 && saved.binding.chapterCorrespondence.chapterEpisode === 1,
            correspondence: !!saved.binding.chapterCorrespondence && !(saved.binding.chapterCorrespondence.sourceEpisode === 1 && saved.binding.chapterCorrespondence.chapterEpisode === 1), sourceEpisode: saved.binding.chapterCorrespondence ? String(saved.binding.chapterCorrespondence.sourceEpisode) : '', chapterEpisode: saved.binding.chapterCorrespondence ? String(saved.binding.chapterCorrespondence.chapterEpisode) : '',
            templateChosen: true, match: (saved.rules.match || []).join('\n'), episodePattern: saved.rules.episodePattern || '', episodeGroup: String(saved.rules.episodeGroup || 1), health: saved.health, errorCode: saved.errorCode,
            minimum: saved.rules.minimumEpisode == null ? '' : String(saved.rules.minimumEpisode), exclude: (saved.rules.exclude || []).join('\n') })
          form.confirmedFor = animeBindingKey(form)
          return form
        })
      }
    } else {
      feedback.value = seedIssue
      const data = config.value as AnimeSubjectResult & { sourceEntry?: MikanEntry; bangumiUnavailable?: boolean; needsSource?: boolean }
      defaultTotal.value = data.subject.episodes || null
      subject.value = { id: data.subject.id, title: data.subject.title, coverUrl: data.subject.coverUrl, date: data.subject.date, bangumiUnavailable: data.bangumiUnavailable, needsSource: data.needsSource }
      sources.value = [newAnimeSource('main', data.needsSource ? '' : data.subject.title, seed?.value.provider === 'garden' ? 'garden' : seed?.value.provider === 'anibt' ? 'anibt' : 'mikan')]
      if (data.sourceEntry) {
        const form = sources.value[0]!
        form.itemId = data.sourceEntry.id; form.itemTitle = data.sourceEntry.title; form.entry = data.sourceEntry
      }
    }
    if (!sources.value.length && !loadError.value) sources.value = [newAnimeSource('main', subject.value?.title)]
    naming.reset(local.value?.naming, !!local.value)
    activeRef.value = sources.value[0]?.ref || ''; loading.value = false; baseline.value = rawForm()
    if (!local.value && !loadError.value && !subject.value?.needsSource) {
      const withoutNaming = () => {
        const value = JSON.parse(rawForm()); delete value.naming
        for (const source of value.sources) if (!source.mappingManual) { delete source.mappingSource; delete source.mappingTarget; delete source.mappingSeason }
        return JSON.stringify(value)
      }
      const originalInputs = withoutNaming()
      void naming.search(true).then(() => {
        if (current === generation && !signal.aborted && !receipt.value && !naming.wasManuallyChanged() && originalInputs === withoutNaming()) baseline.value = rawForm()
      })
    }
  }
  async function reloadSettings() {
    if (locked.value || previewBusy.value) return
    settingsBusy.value = true; settingsError.value = ''; const current = generation
    try { const result = await animeConfigRequest<AnimeSettingsView>(token.value, 'settings', { signal: lifetime.signal }); if (current === generation) settings.value = result }
    catch (reason) { if (current === generation) settingsError.value = animeConfigMessage(reason) }
    finally { if (current === generation) settingsBusy.value = false }
  }
  function addSource() {
    if (locked.value || previewBusy.value || sources.value.length >= 5) return
    const source = newAnimeSource('standby', subject.value?.title, sources.value[0]?.provider || 'mikan'); sources.value.push(source); activeRef.value = source.ref
  }
  function removeSource(source: SourceForm) {
    if (locked.value || previewBusy.value) return
    if (source.id) removed.value.push({ id: source.id, expectedRevision: source.revision })
    searchControllers.get(source.ref)?.abort()
    sampleControllers.get(source.ref)?.abort()
    sources.value = sources.value.filter(item => item !== source)
    if (source.role === 'main') sources.value.unshift(newAnimeSource('main', subject.value?.title, source.provider))
    activeRef.value = sources.value[0]?.ref || ''
  }
  async function search(source: SourceForm) {
    if (locked.value || source.cooldown > Date.now()) return
    const keyword = source.keyword.trim()
    if (!keyword || Array.from(keyword).length > (source.provider === 'anibt' ? 120 : 128) || /[\u0000-\u001f\u007f]/.test(keyword)) { source.searchError = `请输入 1 至 ${source.provider === 'anibt' ? 120 : 128} 字的番剧名称`; return }
    searchControllers.get(source.ref)?.abort(); const controller = new AbortController(); searchControllers.set(source.ref, controller)
    source.searchBusy = true; source.searchError = ''
    try {
      const result = await animeConfigRequest<MikanSearch>(token.value, `sources/${source.provider}/search`, { method: 'POST', body: { keyword, ...(subject.value?.id && !subject.value.bangumiUnavailable ? { bangumiId: subject.value.id } : {}) }, signal: AbortSignal.any([controller.signal, lifetime.signal]) })
      if (!controller.signal.aborted) { source.search = result; source.searchedKeyword = keyword }
    } catch (reason) { if (!controller.signal.aborted) { source.searchError = animeConfigMessage(reason); setCooldown(source, reason, 'search') } }
    finally { if (searchControllers.get(source.ref) === controller) source.searchBusy = false }
  }
  async function chooseEntry(source: SourceForm, id: string, title: string) {
    if (locked.value || previewBusy.value || source.lockedItem && source.itemId !== id || source.cooldown > Date.now()) return
    if (source.entry?.id === id) return
    searchControllers.get(source.ref)?.abort(); const controller = new AbortController(); searchControllers.set(source.ref, controller)
    source.searchBusy = true; source.searchError = ''
    try {
      const result = await animeConfigRequest<{ entry: MikanEntry }>(token.value, `sources/${source.provider}/${encodeURIComponent(id)}`, { signal: AbortSignal.any([controller.signal, lifetime.signal]) })
      if (controller.signal.aborted) return
      if (result.entry.id !== id) throw new Error('来源条目不一致，请重新选择')
      if (source.itemId !== id) { source.groupId = ''; source.groupName = ''; resetRules(source) }
      source.itemId = id; source.itemTitle = result.entry.title; source.entry = result.entry
    } catch (reason) { if (!controller.signal.aborted) { source.searchError = animeConfigMessage(reason); setCooldown(source, reason, 'search') } }
    finally { if (searchControllers.get(source.ref) === controller) source.searchBusy = false }
  }
  function cancelSourceEdit(source: SourceForm) {
    searchControllers.get(source.ref)?.abort(); sampleControllers.get(source.ref)?.abort()
  }
  function applySource(source: SourceForm, candidate: SourceForm) {
    if (locked.value || previewBusy.value) return
    const changed = animeBindingKey(source) !== animeBindingKey(candidate)
    cancelSourceEdit(source)
    if (changed && source.id) removed.value.push({ id: source.id, expectedRevision: source.revision })
    const position = sources.value.indexOf(source)
    if (position < 0) return
    const next: SourceForm = { ...candidate, ref: changed && source.id ? animeRequestKey() : source.ref,
      searchBusy: false, sampleBusy: false, id: changed ? '' : source.id, revision: changed ? 0 : source.revision,
      lockedItem: !changed && source.lockedItem, lockedGroup: !changed && source.lockedGroup }
    if (!changed) Object.assign(next, { preview: source.preview })
    sources.value[position] = next; activeRef.value = next.ref
    if (!local.value && subject.value?.bangumiUnavailable && next.role === 'main' && next.entry) {
      const refreshNaming = subject.value.needsSource || subject.value.title !== next.entry.title && !naming.wasManuallyChanged()
      const card = animeCatalogItemSnapshot(next.provider, next.itemId)
      subject.value = { ...subject.value, id: next.entry.bangumiIds.includes(bangumiID.value) ? bangumiID.value : '', title: next.entry.title, coverUrl: next.entry.coverUrl || card?.coverUrl || '', date: next.entry.airDate || card?.date || '', needsSource: false }
      if (refreshNaming) { naming.reset(); void naming.search(true) }
    }
  }
  function resetRules(source: SourceForm) {
    sampleControllers.get(source.ref)?.abort()
    Object.assign(source, { mappingSource: '0', mappingTarget: '0', mappingSeason: '', mappingLast: '', mappingManual: false, mappingSegments: [], mappingOverrides: [], scope: '', first: '', last: '', evidence: '', confirmed: false, confirmedFor: '', review: undefined, identityNumbering: false, correspondence: false, sourceEpisode: '', chapterEpisode: '', templateChosen: false, match: '', episodePattern: '', episodeGroup: '1', health: undefined, errorCode: undefined, ruleError: undefined, sample: undefined, sampleBusy: false, sampleError: '', sampleCooldown: 0, chapterFirst: '', minimum: '0', exclude: '', preview: undefined, previewError: '' })
  }
  function groupChanged(source: SourceForm) {
    source.groupName = source.entry?.groups.find(item => item.id === source.groupId)?.name || ''; resetRules(source)
    void readSample(source)
  }
  function openSource(source: SourceForm) {
    if (source.itemId && !source.entry) void chooseEntry(source, source.itemId, source.itemTitle)
    else if (!source.itemId && source.keyword.trim() && !source.search && !source.searchBusy) void search(source)
    if (source.groupId) void readSample(source)
  }
  async function readSample(source: SourceForm, retry = false) {
    if (locked.value || !source.itemId || !source.groupId || source.sampleBusy || source.sampleCooldown > Date.now()) return
    const key = `${source.provider}:${source.itemId}:${source.groupId}`, cached = samples.get(key)
    if (!retry && cached && cached.until > Date.now()) { source.sample = cached.data; source.sampleError = ''; return }
    sampleControllers.get(source.ref)?.abort(); const controller = new AbortController(); sampleControllers.set(source.ref, controller)
    source.sampleBusy = true; source.sampleError = ''; const current = generation
    try {
      const data = await animeConfigRequest<AnimeSourceSample>(token.value, 'source-bindings/preview', { method: 'POST', signal: AbortSignal.any([controller.signal, lifetime.signal]), body: { entry: entryRef(source), source: { ...(source.provider !== 'mikan' ? { provider: source.provider } : {}), itemId: source.itemId, groupId: source.groupId }, binding: { schemaVersion: 1, confirmed: false, scope: null, chapterCorrespondence: null } } })
      if (current !== generation || controller.signal.aborted || key !== `${source.provider}:${source.itemId}:${source.groupId}`) return
      if ((data.source.provider || 'mikan') !== source.provider || data.source.itemId !== source.itemId || data.source.groupId !== source.groupId || !Array.isArray(data.releases) || !Array.isArray(data.profiles)) throw new Error('来源样本无法核对，请重试读取')
      source.sample = data
      if (samples.size >= 15) samples.delete(samples.keys().next().value!)
      samples.set(key, { data, until: Math.min(Date.now() + 120000, data.source.expiresAt) })
    } catch (reason) {
      if (current === generation && !controller.signal.aborted) {
        source.sampleError = animeConfigMessage(reason)
        if (reason instanceof AnimeConfigError && reason.status === 429) source.sampleCooldown = Number(reason.details.retryAfterAt) || Date.now() + 60000
      }
    } finally { if (sampleControllers.get(source.ref) === controller) source.sampleBusy = false }
  }
  function previewRefs(except?: SourceForm) { return sources.value.filter(item => item !== except && validPreview(item)).map(item => item.preview!.ref) }
  function installPreview(data: AnimePreviewResponse, target: SourceForm, signatureBefore: string) {
    now.value = Date.now()
    for (const summary of data.sources) {
      const source = sources.value.find(item => item.ref === summary.sourceRef)
      if (!source) continue
      if (source === target && signature(source) !== signatureBefore) continue
      if (source !== target && !validPreview(source)) continue
      const old = source.preview
      source.preview = { ref: { batchId: data.batchId, sourceRef: source.ref, sourcePreviewId: summary.sourcePreviewId, loadRevision: summary.loadRevision }, summary,
        signature: source === target ? signatureBefore : old!.signature, page: source === target ? data.page : old?.page }
      if (source !== target && source.preview.page) source.preview.page.summary = summary
    }
    const issue = data.issues.find(item => item.sourceRef === target.ref)
    if (issue) target.previewError = '来源读取未完成，已有预览和其他来源保留。请稍后重新预览。'
    else if (!data.page) target.previewError = '本次未返回有效资源预览，请重新读取。'
  }
  async function runPreview(source: SourceForm) {
    if (locked.value || previewBusy.value || !settings.value || source.previewCooldown > Date.now()) return
    source.previewError = ''; previewBusy.value = source.ref
    const before = signature(source), current = generation
    try {
      if (!animeSourceReady(source)) throw new Error('请先确认来源与规则')
      const data = await animeConfigRequest<AnimePreviewResponse>(token.value, 'subscriptions/preview', { method: 'POST', signal: lifetime.signal, body: {
        config: { chapter: chapter(), subscriptionId: local.value?.id || '', revision: local.value?.revision || 0, settings: { revision: settings.value.settings.revision }, sources: submittedSources.value.map(animeSourceConfig) },
        previewSourceRef: source.ref, refresh: false, previewRefs: previewRefs(source), ...(source.preview ? { previous: source.preview.ref } : {}),
      } })
      if (current === generation) installPreview(data, source, before)
    } catch (reason) {
      if (current !== generation) return
      source.previewError = animeConfigMessage(reason); applyRuleError(reason); setCooldown(source, reason, 'preview')
      if (source.preview && reason instanceof AnimeConfigError && ['PREVIEW_EXPIRED', 'PREVIEW_CHANGED', 'REVISION_CONFLICT'].includes(reason.code)) source.preview = undefined
    } finally { if (current === generation) previewBusy.value = '' }
  }
  async function save() {
    if (naming.busy.value || naming.searching.value || locked.value || previewBusy.value || !settings.value) return
    feedback.value = ''; duplicateID.value = ''; conflict.value = false
    for (const source of sources.value) source.ruleError = undefined
    try {
      if (startIssue.value) throw new Error(startIssue.value)
      if (archiveTotal.value && !/^[1-9]\d{0,4}$/.test(archiveTotal.value)) throw new Error('篇章总集数请填写 1 至 99999 的整数，留空使用 Bangumi 数据')
      const body = { expectedRevision: local.value?.revision || 0, settingsRevision: settings.value.settings.revision,
        config: { form: { ...local.value?.form, schemaVersion: 1, totalEpisodes: archiveTotal.value ? Number(archiveTotal.value) : null }, naming: naming.input(), entry: entryRef(), sources: submittedSources.value.map(animeSourceConfig),
          removedSourceIds: removed.value } }
      pendingSave.value = { path: local.value ? `subscriptions/${local.value.id}` : 'subscriptions', method: local.value ? 'PATCH' : 'POST', key: animeRequestKey(), body: JSON.parse(JSON.stringify(body)) }
      await retrySave()
    } catch (reason) { feedback.value = animeConfigMessage(reason) }
  }
  function applyRuleError(reason: unknown) {
    if (!(reason instanceof AnimeConfigError) || reason.code !== 'SOURCE_RULE_INVALID') return
    const source = sources.value.find(source => source.ref === reason.details.sourceRef)
    if (source) { source.ruleError = { field: String(reason.details.field || ''), message: reason.message }; activeRef.value = source.ref }
  }
  async function retrySave() {
    const pending = pendingSave.value
    if (!pending || saveBusy.value) return
    saveBusy.value = true; feedback.value = ''; const current = generation
    try {
      const result = await animeConfigRequest<AnimeSaveReceipt>(token.value, pending.path, { ...pending, signal: lifetime.signal })
      if (current !== generation) return
      if (!result.id || !Number.isSafeInteger(result.revision) || !['enabled', 'paused', 'archived'].includes(result.status)) throw new AnimeConfigError('订阅回执不完整，请重试原请求核对', '', 0, {}, true)
      invalidateAnimeSubscriptions(result.id)
      receipt.value = result; pendingSave.value = undefined; baseline.value = rawForm()
    } catch (reason) {
      if (current !== generation) return
      feedback.value = animeConfigMessage(reason)
      applyRuleError(reason)
      if (!(reason instanceof AnimeConfigError && reason.uncertain)) pendingSave.value = undefined
      if (reason instanceof AnimeConfigError) {
        if (reason.code === 'SUBSCRIPTION_EXISTS') duplicateID.value = String(reason.details.existingId || '')
        conflict.value = ['REVISION_CONFLICT', 'DIRECTORY_BINDING_CHANGED', 'ACCOUNT_SNAPSHOT_CHANGED'].includes(reason.code)
      }
    } finally { if (current === generation) saveBusy.value = false }
  }
  watch([token, subscriptionID, bangumiID, () => seed?.value.provider, () => seed?.value.itemId, () => portalUser.value?.id, () => portalUser.value?.role], () => { void initialize() }, { immediate: true })
  onBeforeUnmount(() => { generation++; lifetime.abort(); searchControllers.forEach(c => c.abort()); sampleControllers.forEach(c => c.abort()); samples.clear(); window.clearInterval(clock) })
  return { archiveTotal, defaultTotal, archiveSuggestion, useTMDBTotal, naming, sources, activeRef, active, subject, local, settings, loading, loadError, feedback, settingsError, previewBusy, saveBusy, settingsBusy, pendingSave, receipt, duplicateID, conflict, now, locked, dirty, startIssue, resumesArchived,
    initialize, reloadSettings, addSource, removeSource, cancelSourceEdit, applySource, search, chooseEntry, groupChanged, openSource, readSample, validPreview, signature, runPreview, save, retrySave }
}
