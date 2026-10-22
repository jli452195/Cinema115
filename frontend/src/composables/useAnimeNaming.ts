import { computed, ref, watch, type Ref } from 'vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingCandidate, AnimeNamingInput, AnimeNamingPlan, AnimeNamingTarget, AnimeSourceMapping } from '../types/animeSubscription'

export function sourceNaming(source: SourceForm, target: AnimeNamingTarget): AnimeSourceMapping {
  const integer = (raw: string, label: string, max = 99999) => {
    if (!/^\d+$/.test(raw) || Number(raw) > max) throw new Error(`${label}请填写有效整数`)
    return Number(raw)
  }
  const segments = [...source.mappingSegments]
  if (source.mappingOffset.trim()) {
    if (!/^-?\d{1,5}$/.test(source.mappingOffset)) throw new Error('集数偏移请填写 -99999 至 99999 的整数')
    const offset = Number(source.mappingOffset), base = Math.max(0, -offset), end = source.mappingLast ? integer(source.mappingLast, '映射终点') : null
    if (end != null && end < base) throw new Error('映射终点不能小于发布基准')
    segments.unshift({ first: base, last: end, sourceBase: base, targetBase: base + offset, targetSeason: target.season })
  }
  const overrides = source.mappingOverrides.map(o => o.action === 'ignore' ? { ...o, targetSeason: 0, targetEpisode: 0 } : { ...o })
  const valid = (n: number, min = 0, max = 99999) => Number.isInteger(n) && n >= min && n <= max
  for (const [i, s] of segments.entries()) {
    if (!valid(s.first) || !valid(s.sourceBase) || !valid(s.targetBase) || !valid(s.targetSeason, 0, 1000) || s.last != null && (!valid(s.last) || s.last < s.first) || s.first - s.sourceBase + s.targetBase < 0 || s.last != null && s.last - s.sourceBase + s.targetBase > 99999) throw new Error(`分段 ${i + 1} 请填写有效编号与范围`)
    for (const p of segments.slice(0, i)) {
      if (s.first <= (p.last ?? 99999) && p.first <= (s.last ?? 99999)) throw new Error('分段范围重叠，请先限定上一段终点')
      if (p.targetSeason === s.targetSeason && s.first - s.sourceBase + s.targetBase <= (p.last ?? 99999) - p.sourceBase + p.targetBase && p.first - p.sourceBase + p.targetBase <= (s.last ?? 99999) - s.sourceBase + s.targetBase) throw new Error('同一来源的分段不能指向重复目标集')
    }
  }
  const keys = new Set<string>(), targets = new Set<string>()
  for (const o of overrides) {
    const key = `${o.rawKind}:${o.rawNumber}`, dest = `${o.targetSeason}:${o.targetEpisode}`
    if (!o.rawNumber.trim() || /[\u0000-\u001f\u007f]/.test(o.rawNumber) || keys.has(key) || o.rawKind === 'episode' && (!/^(0|[1-9]\d{0,4})$/.test(o.rawNumber))) throw new Error('请填写唯一且有效的例外原始编号')
    keys.add(key)
    if (o.action === 'ignore') continue
    if (!valid(o.targetSeason, 0, 1000) || !valid(o.targetEpisode) || targets.has(dest)) throw new Error('例外目标需填写有效季集，且不能重复')
    targets.add(dest)
    for (const s of segments) {
      const n = o.targetEpisode - s.targetBase + s.sourceBase
      if (s.targetSeason === o.targetSeason && n >= s.first && n <= (s.last ?? 99999) && !(o.rawKind === 'episode' && o.rawNumber === String(n)) && !overrides.some(p => p.rawKind === 'episode' && p.rawNumber === String(n))) throw new Error('例外目标与普通集重复，请同时设置对应普通集的例外')
    }
  }
  return { sourceRef: source.ref, segments, overrides }
}

export function useAnimeNaming(token: Ref<string>, subject: Ref<{ id: string; title: string; bangumiUnavailable?: boolean } | undefined>, sources: Ref<SourceForm[]>, signal: () => AbortSignal, locked: Ref<boolean>) {
  const target = ref<AnimeNamingTarget>(), candidates = ref<AnimeNamingCandidate[]>([]), groups = ref<{ id: string; name: string }[]>([])
  const busy = ref(false), error = ref(''), keyword = ref(''), searching = ref(false), mode = ref<'tmdb' | 'original'>('tmdb'), picker = ref(false)
  let sequence = 0, searchSequence = 0, controller = new AbortController(), manualTarget = false
  const attemptedSeasons = new Set<string>()
  function reset(plan?: AnimeNamingPlan | null, existing = false) {
    sequence++; searchSequence++; controller.abort(); controller = new AbortController(); attemptedSeasons.clear(); target.value = plan?.target; candidates.value = []; groups.value = []; busy.value = false; error.value = ''; keyword.value = subject.value?.title || ''; searching.value = false; picker.value = false; manualTarget = existing
    mode.value = existing && !plan ? 'original' : 'tmdb'
    for (const source of sources.value) {
      const saved = plan?.sources.find(m => m.sourceRef === source.ref), first = saved?.segments[0]
      const primary = first?.first === first?.sourceBase && first?.targetSeason === plan?.target.season ? first : undefined
      if (saved) Object.assign(source, { mappingOffset: primary ? String(primary.targetBase - primary.sourceBase) : '', mappingLast: primary?.last == null ? '' : String(primary.last), mappingSegments: saved.segments.slice(primary ? 1 : 0).map(s => ({ ...s })), mappingOverrides: (saved.overrides || []).map(o => ({ ...o })) })
    }
  }
  function original() { if (locked.value) return; sequence++; searchSequence++; controller.abort(); mode.value = 'original'; target.value = undefined; error.value = ''; busy.value = false; searching.value = false; manualTarget = true; picker.value = false }
  function extraSeasons(season: number) {
    return [...new Set(sources.value.flatMap(source => [...source.mappingSegments.map(s => s.targetSeason), ...source.mappingOverrides.filter(o => o.action === 'map').map(o => o.targetSeason)]))].filter(n => Number.isInteger(n) && n >= 0 && n <= 1000 && n !== season)
  }
  async function load(id: string, season?: number, groupId = '', automatic = false) {
    if (locked.value || automatic && manualTarget) return
    if (!automatic) manualTarget = true
    const current = ++sequence, lifetime = signal(); controller.abort(); controller = new AbortController(); busy.value = true; error.value = ''; mode.value = 'tmdb'
    try {
      const data = await animeConfigRequest<AnimeNamingTarget>(token.value, 'naming/target', { method: 'POST', signal: AbortSignal.any([lifetime, controller.signal]), body: { tmdbId: id, groupId, ...(season != null ? { season, extraSeasons: extraSeasons(season) } : {}), ...(subject.value?.id && !subject.value.bangumiUnavailable && season == null ? { bangumiId: subject.value.id } : {}) } })
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
    return { tmdbId: t.tmdbId, groupId: t.groupId, season: t.season, fingerprint: t.fingerprint, loadedSeasons: t.seasons.filter(s => s.episodes != null).map(s => s.number), sources: sources.value.filter(s => s.groupId).map(s => sourceNaming(s, t)) }
  }
  const signature = computed(() => JSON.stringify({ mode: mode.value, target: target.value }))
  watch(() => target.value ? `${target.value.tmdbId}:${target.value.groupId}:${extraSeasons(target.value.season).join(',')}:${busy.value}` : '', () => {
    const t = target.value
    if (!t || busy.value || locked.value) return
    const missing = extraSeasons(t.season).some(n => t.seasons.some(s => s.number === n && s.episodes == null))
    const key = `${t.tmdbId}:${t.groupId}:${t.season}:${extraSeasons(t.season).join(',')}`
    if (missing && !attemptedSeasons.has(key)) { attemptedSeasons.add(key); void load(t.tmdbId, t.season, t.groupId) }
  })
  return { target, candidates, groups, busy, searching, error, keyword, mode, picker, signature, reset, original, load, search, readGroups, input, wasManuallyChanged: () => manualTarget }
}
