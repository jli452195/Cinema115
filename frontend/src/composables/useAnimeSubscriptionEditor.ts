import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { invalidateAnimeSubscriptions } from './useAnimeSubscriptionBrowser'
import { useAnimeNaming } from './useAnimeNaming'
import { portalUser } from './usePortalAuth'
import { cachedAnime, requestAnime } from './useAnimeDiscovery'
import { AnimeConfigError, animeConfigMessage, animeConfigRequest, animeRequestKey } from '../lib/anime-config-api'
import { animeProviderName, animeBindingKey, animeSourceConfig, animeSourceReady, newAnimeSource, type SourceForm } from '../utils/animeSourceForm'
import type { AnimeSubjectResult } from '../types/anime'
import type { AnimePreviewPage, AnimePreviewResponse, AnimeSaveReceipt, AnimeSettings, AnimeSettingsView, AnimeSourceSample, AnimeSubscriptionView, MikanEntry, MikanSearch } from '../types/animeSubscription'

interface PendingWrite { path: string; method: string; key: string; body: unknown }
export function useAnimeSubscriptionEditor(token: Ref<string>, subscriptionID: Ref<string>, bangumiID: Ref<string>, seed?: Ref<{ provider: string; itemId: string }>) {
  const sources = ref<SourceForm[]>([]), activeRef = ref('main')
  const subject = ref<{ id: string; title: string; coverUrl: string; date: string }>()
  const local = ref<AnimeSubscriptionView>(), settings = ref<AnimeSettingsView>()
  const loading = ref(true), loadError = ref(''), feedback = ref(''), settingsError = ref('')
  const previewBusy = ref(''), saveBusy = ref(false), settingsBusy = ref(false)
  const pendingSave = ref<PendingWrite>(), pendingSettings = ref<PendingWrite>(), receipt = ref<AnimeSaveReceipt>()
  const duplicateID = ref(''), conflict = ref(false), directoryOpen = ref(false)
  const now = ref(Date.now()), budget = ref({ totalRawCount: 0, limit: 2000, remaining: 2000 })
  const removed = ref<{ id: string; expectedRevision: number }[]>([])
  const baseline = ref(''), draftKey = ref(animeRequestKey())
  let lifetime = new AbortController(), generation = 0
  const searchControllers = new Map<string, AbortController>()
  const sampleControllers = new Map<string, AbortController>()
  const samples = new Map<string, { data: AnimeSourceSample; until: number }>()
  const clock = window.setInterval(() => { now.value = Date.now() }, 10000)
  const active = computed(() => sources.value.find(source => source.ref === activeRef.value))
  const locked = computed(() => saveBusy.value || settingsBusy.value || !!pendingSave.value || !!pendingSettings.value || !!receipt.value || loading.value || !!loadError.value)
  const naming = useAnimeNaming(token, subject, sources, () => lifetime.signal, locked)
  function rawForm() {
    const keys: (keyof SourceForm)[] = ['provider', 'mappingSource', 'mappingTarget', 'mappingSeason', 'mappingLast', 'mappingManual', 'mappingSegments', 'mappingOverrides', 'ref', 'id', 'role', 'runWhenEnabled', 'itemId', 'groupId', 'groupName', 'scope', 'first', 'last', 'evidence', 'confirmed', 'confirmedFor', 'review', 'identityNumbering', 'correspondence', 'sourceEpisode', 'chapterEpisode', 'resolution', 'specMode', 'specProfiles', 'minimum', 'initialMode', 'include', 'exclude', 'selected', 'unresolved', 'recovery', 'dismissed']
    return JSON.stringify({ naming: naming.signature.value, removed: removed.value, sources: sources.value.map(source => Object.fromEntries(keys.map(key => [key, source[key]]))) })
  }
  const dirty = computed(() => !!baseline.value && rawForm() !== baseline.value && !receipt.value)
  const pausedReady = computed(() => !!settings.value?.readiness.ready && sources.value.some(s => s.role === 'main' && animeSourceReady(s)))
  function entryRef() { return local.value ? { id: local.value.entryId } : { ...(subject.value?.id ? { bangumiId: subject.value.id } : {}), editionKey: 'original' } }
  function chapter() { return local.value ? { id: local.value.entry.id, bangumiId: local.value.entry.bangumiId, editionKey: local.value.entry.editionKey, revision: local.value.entry.revision, draftKey: '' } : { id: '', bangumiId: subject.value?.id || null, editionKey: 'original', revision: 0, draftKey: draftKey.value } }
  function signature(source: SourceForm) {
    try {
      const { position: _p, role: _r, status: _s, runWhenEnabled: _e, groupName: _g, ...config } = animeSourceConfig(source, 0)
      return JSON.stringify({ config, chapter: chapter(), settings: settings.value?.settings.revision, subscriptionID: local.value?.id || '' })
    } catch { return '' }
  }
  function validPreview(source: SourceForm) { return !!source.preview && source.preview.signature === signature(source) && !source.preview.needsSync && !source.preview.summary.expired && source.preview.summary.expiresAt > Math.max(now.value, Date.now()) }
  function setCooldown(source: SourceForm, reason: unknown, kind: 'search' | 'preview') {
    if (reason instanceof AnimeConfigError && reason.status === 429) {
      const until = Number(reason.details.retryAfterAt) || Date.now() + 60000
      if (kind === 'search') source.cooldown = until
      else source.previewCooldown = until
    }
  }
  async function initialize() {
    const current = ++generation
    lifetime.abort(); lifetime = new AbortController(); searchControllers.forEach(c => c.abort()); searchControllers.clear()
    sampleControllers.forEach(c => c.abort()); sampleControllers.clear(); samples.clear()
    sources.value = []; local.value = undefined; subject.value = undefined; settings.value = undefined; removed.value = []
    receipt.value = undefined; pendingSave.value = undefined; pendingSettings.value = undefined; baseline.value = ''; draftKey.value = animeRequestKey()
    loading.value = true; saveBusy.value = false; settingsBusy.value = false; loadError.value = ''; feedback.value = ''; duplicateID.value = ''; conflict.value = false; settingsError.value = ''; previewBusy.value = ''; directoryOpen.value = false
    if (!token.value || portalUser.value?.role !== 'admin') { loadError.value = '请使用管理员身份重新登录'; loading.value = false; return }
    const signal = lifetime.signal
    let seedIssue = ''
    async function newConfiguration(): Promise<AnimeSubjectResult & { sourceEntry?: MikanEntry }> {
      let sourceEntry: MikanEntry | undefined
      if (seed?.value.provider || seed?.value.itemId) {
        if (!['mikan', 'anibt'].includes(seed.value.provider) || !/^[1-9]\d{0,11}$/.test(seed.value.itemId)) throw new Error('订阅站点或条目无效，请返回该站点重新选择')
        try {
          const result = await animeConfigRequest<{ entry: MikanEntry }>(token.value, `sources/${seed.value.provider}/${seed.value.itemId}`, { signal })
          if (result.entry.id !== seed.value.itemId) throw new Error('来源条目不一致，请重新选择')
          sourceEntry = result.entry
        } catch (reason) {
          if (!bangumiID.value || signal.aborted) throw reason
          seedIssue = '入口来源暂时无法读取，可在 RSS 中重新选择站点。'
        }
      }
      if (/^[1-9]\d*$/.test(bangumiID.value)) {
        const cached = cachedAnime<AnimeSubjectResult>(`subject:${bangumiID.value}`)
        const result = cached?.fresh ? cached.data : await requestAnime<AnimeSubjectResult>(`subject:${bangumiID.value}`, `subjects/${bangumiID.value}`, token.value, signal)
        return { ...result, sourceEntry }
      }
      if (bangumiID.value || !sourceEntry) throw new Error('缺少有效番剧条目，请返回选番')
      return { sourceEntry, subject: { id: '', title: sourceEntry.title, originalTitle: '', coverUrl: '', date: '', weekday: null, url: '', synopsis: '', platform: '', episodes: null, totalEpisodes: null }, metadata: { fetchedAt: 0, expiresAt: 0, stale: false, dataVersion: '', issue: null } }
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
      // Enabled configurations are not editable until their P2 lifecycle controls are implemented.
      if (!['draft', 'paused'].includes(view.status)) loadError.value = '当前配置状态暂不支持在此阶段编辑'
      else {
        local.value = view
        subject.value = { id: view.entry.bangumiId || '', title: view.entry.title, coverUrl: view.entry.metadata?.coverUrl || '', date: view.entry.metadata?.airDate || '' }
        sources.value = view.sources.map(saved => {
          const form = newAnimeSource(saved.role, view.entry.title, saved.provider)
          Object.assign(form, { ref: saved.id, id: saved.id, revision: saved.revision, runWhenEnabled: saved.runWhenEnabled,
            itemId: saved.itemId || '', itemTitle: saved.itemId ? `${animeProviderName(saved.provider)}条目 ${saved.itemId}` : '', groupId: saved.groupId || '', groupName: saved.groupName || '', lockedItem: !!saved.itemId, lockedGroup: !!saved.groupId,
            scope: saved.binding.scope?.kind || '', first: saved.binding.scope?.kind === 'range' ? String(saved.binding.scope.first) : '', last: saved.binding.scope?.kind === 'range' ? String(saved.binding.scope.last) : '', evidence: saved.binding.scope?.kind === 'confirmed_chapter' ? saved.binding.scope.evidence || '' : '', confirmed: saved.binding.confirmed, review: saved.binding.review,
            identityNumbering: saved.binding.chapterCorrespondence?.sourceEpisode === 1 && saved.binding.chapterCorrespondence.chapterEpisode === 1,
            correspondence: !!saved.binding.chapterCorrespondence && !(saved.binding.chapterCorrespondence.sourceEpisode === 1 && saved.binding.chapterCorrespondence.chapterEpisode === 1), sourceEpisode: saved.binding.chapterCorrespondence ? String(saved.binding.chapterCorrespondence.sourceEpisode) : '', chapterEpisode: saved.binding.chapterCorrespondence ? String(saved.binding.chapterCorrespondence.chapterEpisode) : '',
            specMode: saved.rules.specMode || '', specProfiles: saved.rules.specProfiles || [],
            resolution: saved.rules.resolution || '', minimum: saved.rules.minimumEpisode == null ? '' : String(saved.rules.minimumEpisode), initialMode: saved.rules.initialMode || 'from_episode', include: (saved.rules.include || []).join(', '), exclude: (saved.rules.exclude || []).join(', ') })
          form.confirmedFor = animeBindingKey(form)
          const selection = view.form?.formSelections?.find(item => item.sourceRef === saved.id)
          if (selection) { form.recovery = [...new Set([...selection.unresolvedReleaseIds, ...selection.selectedReleaseIds])]; form.dismissed = [...(selection.dismissedReleaseIds || [])] }
          return form
        })
      }
    } else {
      feedback.value = seedIssue
      const data = config.value as AnimeSubjectResult & { sourceEntry?: MikanEntry }
      subject.value = { id: data.subject.id, title: data.subject.title, coverUrl: data.subject.coverUrl, date: data.subject.date }
      sources.value = [newAnimeSource('main', data.subject.title, seed?.value.provider === 'anibt' ? 'anibt' : 'mikan')]
      if (data.sourceEntry) {
        const form = sources.value[0]!
        form.itemId = data.sourceEntry.id; form.itemTitle = data.sourceEntry.title; form.entry = data.sourceEntry
      }
    }
    if (!sources.value.length && !loadError.value) sources.value = [newAnimeSource('main', subject.value?.title)]
    naming.reset(local.value?.naming, !!local.value)
    activeRef.value = sources.value[0]?.ref || ''; loading.value = false; baseline.value = rawForm()
    if (!local.value && !loadError.value) void naming.search(true)
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
    if ((source.itemId || source.id) && !window.confirm('移除此来源配置？其他字幕组和已保存的资源不会被删除。')) return
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
      const result = await animeConfigRequest<MikanSearch>(token.value, `sources/${source.provider}/search`, { method: 'POST', body: { keyword, ...(subject.value?.id ? { bangumiId: subject.value.id } : {}) }, signal: AbortSignal.any([controller.signal, lifetime.signal]) })
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
    if (!changed) Object.assign(next, { preview: source.preview, selected: source.selected, unresolved: source.unresolved, recovery: source.recovery, dismissed: source.dismissed })
    sources.value[position] = next; activeRef.value = next.ref
  }
  function resetRules(source: SourceForm) {
    sampleControllers.get(source.ref)?.abort()
    Object.assign(source, { mappingSource: '1', mappingTarget: '1', mappingSeason: '', mappingLast: '', mappingManual: false, mappingSegments: [], mappingOverrides: [], scope: '', first: '', last: '', evidence: '', confirmed: false, confirmedFor: '', review: undefined, identityNumbering: false, correspondence: false, sourceEpisode: '', chapterEpisode: '', resolution: '', specMode: '', specProfiles: [], sample: undefined, sampleBusy: false, sampleError: '', sampleCooldown: 0, minimum: '1', initialMode: 'from_episode', include: '', exclude: '', preview: undefined, selected: [], unresolved: [], recovery: [], dismissed: [], previewError: '' })
  }
  function groupChanged(source: SourceForm) {
    source.groupName = source.entry?.groups.find(item => item.id === source.groupId)?.name || ''; resetRules(source)
    void readSample(source)
  }
  function openSource(source: SourceForm) {
    if (source.itemId && !source.entry) void chooseEntry(source, source.itemId, source.itemTitle)
    else if (!source.itemId && !source.search && !source.searchBusy) void search(source)
    if (source.groupId) void readSample(source)
  }
  async function readSample(source: SourceForm, retry = false) {
    if (locked.value || !source.itemId || !source.groupId || source.sampleBusy || source.sampleCooldown > Date.now()) return
    const key = `${source.provider}:${source.itemId}:${source.groupId}`, cached = samples.get(key)
    if (!retry && cached && cached.until > Date.now()) { source.sample = cached.data; source.sampleError = ''; return }
    sampleControllers.get(source.ref)?.abort(); const controller = new AbortController(); sampleControllers.set(source.ref, controller)
    source.sampleBusy = true; source.sampleError = ''; const current = generation
    try {
      const data = await animeConfigRequest<AnimeSourceSample>(token.value, 'source-bindings/preview', { method: 'POST', signal: AbortSignal.any([controller.signal, lifetime.signal]), body: { entry: entryRef(), source: { ...(source.provider === 'anibt' ? { provider: source.provider } : {}), itemId: source.itemId, groupId: source.groupId }, binding: { schemaVersion: 1, confirmed: false, scope: null, chapterCorrespondence: null } } })
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
  function installPreview(data: AnimePreviewResponse, target: SourceForm, signatureBefore: string, take: number) {
    now.value = Date.now()
    budget.value = data.budget
    for (const summary of data.sources) {
      const source = sources.value.find(item => item.ref === summary.sourceRef)
      if (!source) continue
      if (source === target && signature(source) !== signatureBefore) continue
      if (source !== target && !validPreview(source)) continue
      const old = source.preview
      source.preview = { ref: { batchId: data.batchId, sourceRef: source.ref, sourcePreviewId: summary.sourcePreviewId, loadRevision: summary.loadRevision }, summary,
        signature: source === target ? signatureBefore : old!.signature, take: source === target ? take : old!.take, page: source === target ? data.page : old?.page }
      if (source !== target && source.preview.page) source.preview.page.summary = summary
      if (source === target && data.page) { source.selected = [...data.page.selectedIds]; source.unresolved = data.page.pendingSelections.map(item => item.releaseId); source.recovery = source.recovery.filter(id => !source.selected.includes(id) && !source.unresolved.includes(id)); source.dismissed = source.dismissed.filter(id => !source.selected.includes(id) && !source.unresolved.includes(id)) }
    }
    const issue = data.issues.find(item => item.sourceRef === target.ref)
    if (issue) target.previewError = '来源读取未完成，已有预览和其他来源保留。请稍后重新预览。'
    else if (!data.page) target.previewError = '本次未返回有效资源预览，请重新读取。'
  }
  async function runPreview(source: SourceForm, action: 'generate' | 'load' | 'page' | 'select', options: { page?: number; selected?: string[]; dismissed?: string[] } = {}) {
    if (locked.value || previewBusy.value || !settings.value || source.previewCooldown > Date.now()) return
    source.previewError = ''; previewBusy.value = source.ref
    const before = signature(source), current = generation
    try {
      const preview = source.preview
      if (action !== 'generate' && !validPreview(source)) throw new Error('规则已更改或预览已过期，请重新预览')
      if (action === 'generate') {
        if (!animeSourceReady(source)) throw new Error('请先确认来源与规格，并填写补番起点')
        const configs = sources.value.map(animeSourceConfig)
        if (preview?.needsSync) {
          const latest = await animeConfigRequest<AnimePreviewPage>(token.value, `previews/${preview.ref.batchId}/releases?sourceRef=${encodeURIComponent(source.ref)}&page=1&pageSize=50`, { signal: lifetime.signal })
          if (current !== generation) return
          if (latest.summary.loadRevision > preview.ref.loadRevision) {
            const pending = new Set(latest.pendingSelections.map(item => item.releaseId))
            source.dismissed = [...new Set([...source.dismissed, ...source.selected, ...source.unresolved])].filter(id => !latest.selectedIds.includes(id) && !pending.has(id))
          }
          preview.ref.loadRevision = latest.summary.loadRevision; preview.page = latest; preview.summary = latest.summary
        }
        const take = preview?.take || 50
        const rememberedDismissals = [...source.dismissed]
        const data = await animeConfigRequest<AnimePreviewResponse>(token.value, 'subscriptions/preview', { method: 'POST', signal: lifetime.signal, body: {
          config: { chapter: chapter(), subscriptionId: local.value?.id || '', revision: local.value?.revision || 0, settings: { revision: settings.value.settings.revision }, sources: configs },
          previewSourceRef: source.ref, take, refresh: false, previewRefs: previewRefs(source), ...(preview ? { previous: preview.ref } : {}),
        } })
        if (current !== generation) return
        installPreview(data, source, before, take)
        // Restored exclusions are explicit input. A fresh scope must not silently select them again.
        const excluded = data.page?.selectedIds.filter(id => rememberedDismissals.includes(id)) || []
        if (excluded.length && source.preview) {
          source.dismissed = rememberedDismissals
          source.selected = source.selected.filter(id => !excluded.includes(id))
          source.preview.needsSync = true; source.preview.signature = ''
          const selected = await animeConfigRequest<AnimePreviewResponse>(token.value, `previews/${source.preview.ref.batchId}/selections`, { method: 'POST', signal: lifetime.signal, body: { sourceRef: source.ref, sourcePreviewId: source.preview.ref.sourcePreviewId, expectedLoadRevision: source.preview.ref.loadRevision, selectedReleaseIds: source.selected, dismissedReleaseIds: excluded } })
          if (current === generation) installPreview(selected, source, before, take)
        }
      } else if (action === 'page') {
        const page = await animeConfigRequest<AnimePreviewPage>(token.value, `previews/${preview!.ref.batchId}/releases?sourceRef=${encodeURIComponent(source.ref)}&page=${options.page || 1}&pageSize=50`, { signal: lifetime.signal })
        if (current === generation && signature(source) === before) { preview!.page = page; preview!.summary = page.summary; preview!.ref.loadRevision = page.summary.loadRevision; source.selected = [...page.selectedIds]; source.unresolved = page.pendingSelections.map(item => item.releaseId) }
      } else {
        const nextTake = Math.min(2000, preview!.take + Math.min(50, budget.value.remaining))
        if (action === 'load' && (nextTake <= preview!.take || !preview!.summary.unloadedHistory)) throw new Error('已到本次历史读取边界')
        const pageBefore = preview!.page?.page || 1
        const data = await animeConfigRequest<AnimePreviewResponse>(token.value, `previews/${preview!.ref.batchId}/${action === 'load' ? 'load' : 'selections'}`, { method: 'POST', signal: lifetime.signal, body: {
          sourceRef: source.ref, sourcePreviewId: preview!.ref.sourcePreviewId, expectedLoadRevision: preview!.ref.loadRevision,
          ...(action === 'load' ? { nextTake } : { selectedReleaseIds: options.selected || [], dismissedReleaseIds: options.dismissed || [] }),
        } })
        if (current !== generation) return
        installPreview(data, source, before, action === 'load' ? nextTake : preview!.take)
        if (action === 'select' && signature(source) === before) {
          source.dismissed = [...new Set([...source.dismissed, ...(options.dismissed || []), ...preview!.page!.selectedIds.filter(id => !source.selected.includes(id))])].filter(id => !source.selected.includes(id))
          if (pageBefore > 1 && source.preview) {
            const page = await animeConfigRequest<AnimePreviewPage>(token.value, `previews/${source.preview.ref.batchId}/releases?sourceRef=${encodeURIComponent(source.ref)}&page=${pageBefore}&pageSize=50`, { signal: lifetime.signal })
            if (current === generation && signature(source) === before) source.preview.page = page
          }
        }
      }
    } catch (reason) {
      if (current !== generation) return
      source.previewError = animeConfigMessage(reason); setCooldown(source, reason, 'preview')
      if (source.preview && (['select', 'load'].includes(action) || reason instanceof AnimeConfigError && ['PREVIEW_EXPIRED', 'PREVIEW_CHANGED', 'REVISION_CONFLICT'].includes(reason.code))) { source.preview.needsSync = true; source.preview.signature = '' }
    } finally { if (current === generation) previewBusy.value = '' }
  }
  function selectRelease(source: SourceForm, id: string, checked: boolean) {
    void runPreview(source, 'select', { selected: checked ? [...new Set([...source.selected, id])] : source.selected.filter(item => item !== id), dismissed: checked ? [] : [id] })
  }
  function dismissPending(source: SourceForm, id: string) { void runPreview(source, 'select', { selected: source.selected, dismissed: [id] }) }
  function dismissRecovery(source: SourceForm) { if (!locked.value && !previewBusy.value) source.recovery = [] }
  async function save(mode: 'draft' | 'paused') {
    if (naming.busy.value || naming.searching.value) return
    if (locked.value || previewBusy.value || sources.value.some(source => source.searchBusy) || !settings.value) return
    feedback.value = ''; duplicateID.value = ''; conflict.value = false
    try {
      const configs = sources.value.map(animeSourceConfig)
      if (mode === 'paused' && !pausedReady.value) throw new Error('请先补齐主 RSS 与主账号下载目录，也可以先保存草稿')
      const body = { saveMode: mode, expectedRevision: local.value?.revision || 0, settingsRevision: settings.value.settings.revision,
        config: { naming: naming.input(), entry: entryRef(), sources: configs, removedSourceIds: removed.value,
          formSelections: sources.value.map(source => ({ sourceRef: source.ref, selectedReleaseIds: validPreview(source) ? source.selected : [], unresolvedReleaseIds: [...new Set([...source.unresolved, ...source.recovery, ...(!validPreview(source) ? source.selected : [])])].filter(id => !(validPreview(source) && source.selected.includes(id)) && !source.dismissed.includes(id)), dismissedReleaseIds: source.dismissed })), previewRefs: previewRefs() } }
      pendingSave.value = { path: local.value ? `subscriptions/${local.value.id}` : 'subscriptions', method: local.value ? 'PATCH' : 'POST', key: animeRequestKey(), body: JSON.parse(JSON.stringify(body)) }
      await retrySave()
    } catch (reason) { feedback.value = animeConfigMessage(reason) }
  }
  async function retrySave() {
    const pending = pendingSave.value
    if (!pending || saveBusy.value) return
    saveBusy.value = true; feedback.value = ''; const current = generation
    try {
      const result = await animeConfigRequest<AnimeSaveReceipt>(token.value, pending.path, { ...pending, signal: lifetime.signal })
      if (current !== generation) return
      if (!result.id || !Number.isSafeInteger(result.revision) || !['draft', 'paused'].includes(result.status)) throw new AnimeConfigError('保存回执不完整，请重试原请求核对', '', 0, {}, true)
      invalidateAnimeSubscriptions(result.id)
      receipt.value = result; pendingSave.value = undefined; baseline.value = rawForm()
    } catch (reason) {
      if (current !== generation) return
      feedback.value = animeConfigMessage(reason)
      if (!(reason instanceof AnimeConfigError && reason.uncertain)) pendingSave.value = undefined
      if (reason instanceof AnimeConfigError) {
        if (reason.code === 'SUBSCRIPTION_EXISTS') duplicateID.value = String(reason.details.existingId || '')
        conflict.value = ['REVISION_CONFLICT', 'DIRECTORY_BINDING_CHANGED', 'ACCOUNT_SNAPSHOT_CHANGED'].includes(reason.code)
      }
    } finally { if (current === generation) saveBusy.value = false }
  }
  async function saveDirectory(cid?: string) {
    if (settingsBusy.value || saveBusy.value || previewBusy.value || !settings.value || pendingSave.value || receipt.value) return
    settingsError.value = ''
    if (!pendingSettings.value) {
      if (!cid || !settings.value.primaryAccount) return
      const primary = settings.value.primaryAccount
      pendingSettings.value = { path: 'settings', method: 'PATCH', key: animeRequestKey(), body: { expectedRevision: settings.value.settings.revision, downloadCid: cid, organizeCid: settings.value.settings.organizeCid, expectedPrimaryAccountId: primary.id, expectedPrimaryAccountRevision: primary.revision } }
    }
    settingsBusy.value = true; const current = generation
    try {
      const pending = pendingSettings.value
      const result = await animeConfigRequest<AnimeSettings>(token.value, pending.path, { ...pending, signal: lifetime.signal })
      if (current !== generation) return
      if (!Number.isSafeInteger(result.revision) || !result.accountId || !result.downloadCid) throw new AnimeConfigError('目录保存回执不完整，请重试原请求', '', 0, {}, true)
      settings.value = { ...settings.value, settings: result, readiness: { ready: true, issues: [] } }
      invalidateAnimeSubscriptions()
      pendingSettings.value = undefined; directoryOpen.value = false
    } catch (reason) {
      if (current !== generation) return
      settingsError.value = animeConfigMessage(reason)
      if (reason instanceof AnimeConfigError && reason.uncertain) directoryOpen.value = false
      else pendingSettings.value = undefined
    } finally { if (current === generation) settingsBusy.value = false }
  }
  watch([token, subscriptionID, bangumiID, () => seed?.value.provider, () => seed?.value.itemId, () => portalUser.value?.id, () => portalUser.value?.role], () => { void initialize() }, { immediate: true })
  onBeforeUnmount(() => { generation++; lifetime.abort(); searchControllers.forEach(c => c.abort()); sampleControllers.forEach(c => c.abort()); samples.clear(); window.clearInterval(clock) })
  return { naming, sources, activeRef, active, subject, local, settings, loading, loadError, feedback, settingsError, previewBusy, saveBusy, settingsBusy, pendingSave, pendingSettings, receipt, duplicateID, conflict, directoryOpen, now, budget, locked, dirty, pausedReady,
    initialize, reloadSettings, addSource, removeSource, cancelSourceEdit, applySource, search, chooseEntry, groupChanged, openSource, readSample, validPreview, signature, runPreview, selectRelease, dismissPending, dismissRecovery, save, retrySave, saveDirectory }
}
