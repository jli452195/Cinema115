import { computed, ref, type Ref } from 'vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingCandidate, AnimeNamingInput, AnimeNamingPlan, AnimeNamingTarget, AnimeSourceMapping } from '../types/animeSubscription'

export function sourceNaming(source: SourceForm, target: AnimeNamingTarget): AnimeSourceMapping {
  const raw = source.mappingOffset.trim() || '0'
  if (!/^[+-]?\d{1,5}$/.test(raw)) throw new Error('集数偏移请填写 -99999 至 99999 的整数')
  const offset = Number(raw), base = Math.max(0, -offset)
  return { sourceRef: source.ref, segments: [{ first: base, last: null, sourceBase: base, targetBase: base + offset, targetSeason: target.season }], overrides: [] }
}

export function useAnimeNaming(token: Ref<string>, subject: Ref<{ id: string; title: string; bangumiUnavailable?: boolean } | undefined>, sources: Ref<SourceForm[]>, signal: () => AbortSignal, locked: Ref<boolean>) {
  const target = ref<AnimeNamingTarget>(), candidates = ref<AnimeNamingCandidate[]>([]), groups = ref<{ id: string; name: string }[]>([])
  const busy = ref(false), error = ref(''), keyword = ref(''), searching = ref(false), mode = ref<'tmdb' | 'original'>('tmdb'), picker = ref(false)
  let sequence = 0, searchSequence = 0, controller = new AbortController(), manualTarget = false
  function reset(plan?: AnimeNamingPlan | null, existing = false) {
    sequence++; searchSequence++; controller.abort(); controller = new AbortController(); target.value = plan?.target; candidates.value = []; groups.value = []; busy.value = false; error.value = ''; keyword.value = subject.value?.title || ''; searching.value = false; picker.value = false; manualTarget = existing
    mode.value = existing && !plan ? 'original' : 'tmdb'
    for (const source of sources.value) {
      const first = plan?.sources.find(m => m.sourceRef === source.ref)?.segments[0]
      source.mappingOffset = first ? String(first.targetBase - first.sourceBase) : '0'
    }
  }
  function original() { if (locked.value) return; sequence++; searchSequence++; controller.abort(); mode.value = 'original'; target.value = undefined; error.value = ''; busy.value = false; searching.value = false; manualTarget = true; picker.value = false }
  async function load(id: string, season?: number, groupId = '', automatic = false) {
    if (locked.value || automatic && manualTarget) return
    if (!automatic) manualTarget = true
    const current = ++sequence, lifetime = signal(); controller.abort(); controller = new AbortController(); busy.value = true; error.value = ''; mode.value = 'tmdb'
    try {
      const data = await animeConfigRequest<AnimeNamingTarget>(token.value, 'naming/target', { method: 'POST', signal: AbortSignal.any([lifetime, controller.signal]), body: { tmdbId: id, groupId, ...(season != null ? { season } : {}), ...(subject.value?.id && !subject.value.bangumiUnavailable && season == null ? { bangumiId: subject.value.id } : {}) } })
      if (current !== sequence || lifetime.aborted) return
      if (!data.fingerprint || !Array.isArray(data.seasons) || data.tmdbId !== id) throw new Error('TMDB 资料不完整，请重新读取')
      target.value = data; picker.value = false
    } catch (reason) { if (current === sequence && !lifetime.aborted) error.value = animeConfigMessage(reason) }
    finally { if (current === sequence) busy.value = false }
  }
  async function search(automatic = false) {
    if (locked.value || automatic && manualTarget || searching.value) return
    if (!automatic) manualTarget = true
    const current = sequence, searchID = ++searchSequence, lifetime = signal(); searching.value = true; error.value = ''
    try {
      const data = await animeConfigRequest<{ items: AnimeNamingCandidate[]; recommendedId: string }>(token.value, 'naming/search', { method: 'POST', signal: lifetime, body: automatic && subject.value?.id && !subject.value.bangumiUnavailable ? { bangumiId: subject.value.id } : { keyword: keyword.value.trim() || subject.value?.title || '' } })
      if (current !== sequence || lifetime.aborted || automatic && manualTarget) return
      candidates.value = Array.isArray(data.items) ? data.items : []
      if (automatic && data.recommendedId) await load(data.recommendedId, undefined, '', true)
      else if (candidates.value.length) picker.value = true
    } catch (reason) { if (current === sequence && !lifetime.aborted) error.value = animeConfigMessage(reason) }
    finally { if (searchID === searchSequence) searching.value = false }
  }
  async function readGroups() {
    if (!target.value || locked.value) return
    const current = sequence, lifetime = signal()
    try { const data = await animeConfigRequest<{ items: { id: string; name: string }[] }>(token.value, `naming/groups/${target.value.tmdbId}`, { signal: lifetime }); if (current === sequence && !lifetime.aborted) groups.value = Array.isArray(data.items) ? data.items : [] }
    catch (reason) { if (current === sequence && !lifetime.aborted) error.value = animeConfigMessage(reason) }
  }
  function input(): AnimeNamingInput | null {
    if (mode.value === 'original' || !target.value) return null
    const t = target.value
    return { tmdbId: t.tmdbId, groupId: t.groupId, season: t.season, fingerprint: t.fingerprint, sources: sources.value.filter(s => s.groupId).map(s => sourceNaming(s, t)) }
  }
  const signature = computed(() => JSON.stringify({ mode: mode.value, target: target.value }))
  return { target, candidates, groups, busy, searching, error, keyword, mode, picker, signature, reset, original, load, search, readGroups, input, wasManuallyChanged: () => manualTarget }
}
