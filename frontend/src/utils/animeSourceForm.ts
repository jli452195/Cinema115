import type { AnimeSourceProvider } from '../types/animeCatalog'
import { animeRequestKey } from '../lib/anime-config-api'
import type { AnimeSourceConfig, AnimePreviewPage, AnimePreviewRef, AnimePreviewSummary, AnimeSourceSample, AnimeSpec, AnimeBinding, MikanEntry, MikanSearch, AnimeMappingSegment, AnimeMappingOverride } from '../types/animeSubscription'

export interface SourceForm {
  chapterFirst: string
  provider: AnimeSourceProvider; ref: string; id: string; revision: number; role: 'main' | 'standby'; runWhenEnabled: boolean
  itemId: string; itemTitle: string; groupId: string; groupName: string
  lockedItem: boolean; lockedGroup: boolean
  keyword: string; searchedKeyword: string; search?: MikanSearch; entry?: MikanEntry; searchBusy: boolean; searchError: string; cooldown: number
  scope: '' | 'feed' | 'range' | 'confirmed_chapter'; first: string; last: string; evidence: string; confirmed: boolean
  mappingSource: string; mappingTarget: string; mappingSeason: string; mappingLast: string; mappingManual: boolean; mappingSegments: AnimeMappingSegment[]; mappingOverrides: AnimeMappingOverride[]
  confirmedFor: string; review?: AnimeBinding['review']; identityNumbering: boolean
  correspondence: boolean; sourceEpisode: string; chapterEpisode: string
  specMode: '' | 'profiles' | 'any'; specProfiles: AnimeSpec[]
  sample?: AnimeSourceSample; sampleBusy: boolean; sampleError: string; sampleCooldown: number
  resolution: '' | '720p' | '1080p' | '2160p' | 'any'; minimum: string; include: string; exclude: string
  preview?: { ref: AnimePreviewRef; summary: AnimePreviewSummary; signature: string; take: number; page?: AnimePreviewPage; needsSync?: boolean }
  previewError: string; previewCooldown: number
}
export function newAnimeSource(role: SourceForm['role'], keyword = '', provider: AnimeSourceProvider = 'mikan'): SourceForm {
  return { chapterFirst: '', provider, ref: role === 'main' ? 'main' : animeRequestKey(), id: '', revision: 0, role, runWhenEnabled: true,
    itemId: '', itemTitle: '', groupId: '', groupName: '', lockedItem: false, lockedGroup: false,
    keyword, searchedKeyword: '', searchBusy: false, searchError: '', cooldown: 0,
    scope: '', first: '', last: '', evidence: '', confirmed: false, confirmedFor: '', identityNumbering: false, correspondence: false, sourceEpisode: '', chapterEpisode: '',
    mappingSource: '1', mappingTarget: '1', mappingSeason: '', mappingLast: '', mappingManual: false, mappingSegments: [], mappingOverrides: [],
    specMode: '', specProfiles: [], sampleBusy: false, sampleError: '', sampleCooldown: 0,
    resolution: '', minimum: '1', include: '', exclude: '', previewError: '', previewCooldown: 0 }
}
function episode(value: string, label: string): number | null {
  if (!value.trim()) return null
  if (!/^[1-9]\d{0,4}$/.test(value.trim())) throw new Error(`${label}请填写 1 至 99999 的整数`)
  return Number(value.trim())
}
function words(value: string) {
  const result = [...new Set(value.split(/[,，\n]/).map(word => word.trim().replace(/[A-Z]/g, s => s.toLowerCase())).filter(Boolean))]
  if (result.length > 10 || result.some(word => Array.from(word).length > 64 || /[\u0000-\u001f\u007f]/.test(word))) throw new Error('包含/排除词各最多 10 个，每个最多 64 字，不能含控制字符')
  return result
}
export function animeSourceConfig(form: SourceForm, position: number): AnimeSourceConfig {
  let scope: AnimeSourceConfig['binding']['scope'] = null
  if (form.scope === 'range') {
    const first = episode(form.first, '范围起点'), last = episode(form.last, '范围终点')
    if (first !== null || last !== null) {
      if (first === null || last === null || first > last) throw new Error('请完整填写编号范围，起点不能大于终点')
      scope = { kind: 'range', first, last }
    }
  } else if (form.scope === 'feed') scope = { kind: 'feed' }
  else if (form.scope === 'confirmed_chapter') {
    if (Array.from(form.evidence.trim()).length > 512 || /[\u0000-\u001f\u007f]/.test(form.evidence.trim())) throw new Error('核对说明最多 512 字，不能含控制字符')
    scope = { kind: 'confirmed_chapter', ...(form.evidence.trim() ? { evidence: form.evidence.trim() } : {}) }
  }
  let chapterCorrespondence: AnimeSourceConfig['binding']['chapterCorrespondence'] = null
  if (form.scope !== 'feed' && form.correspondence) {
    const sourceEpisode = episode(form.sourceEpisode, '字幕组编号'), chapterEpisode = episode(form.chapterEpisode, '篇章编号')
    if (sourceEpisode === null || chapterEpisode === null) throw new Error('请完整填写编号对应，或关闭编号对应')
    if (scope?.kind === 'range' && (scope.first - sourceEpisode + chapterEpisode < 1 || scope.last - sourceEpisode + chapterEpisode > 99999)) throw new Error('编号对应会超出篇章集号范围，请核对')
    chapterCorrespondence = { sourceEpisode, chapterEpisode }
  } else if (form.scope !== 'feed' && form.identityNumbering) chapterCorrespondence = { sourceEpisode: 1, chapterEpisode: 1 }
  const minimumEpisode = episode(form.minimum, '补番起点')
  const confirmed = animeBindingConfirmed(form)
  const specificationsReady = form.specMode === 'any' || form.specMode === 'profiles' && form.specProfiles.length > 0 || !form.specMode && !!form.resolution
  const complete = !!(form.itemId && form.groupId && scope && confirmed && specificationsReady && minimumEpisode)
  return { ref: form.ref, id: form.id, expectedRevision: form.revision, role: form.role, position, status: complete ? 'paused' : 'draft', runWhenEnabled: form.runWhenEnabled, provider: form.provider,
    itemId: form.itemId || null, groupId: form.groupId || null, groupName: form.groupName || null,
    binding: { schemaVersion: 1, confirmed, scope, chapterCorrespondence, ...(confirmed && form.review ? { review: form.review } : {}) },
    rules: { ...(form.chapterFirst.trim() ? { chapterFirstEpisode: episode(form.chapterFirst, '篇章首集') } : {}), schemaVersion: 1, resolution: form.specMode ? 'any' : form.resolution || null, minimumEpisode, include: words(form.include), exclude: words(form.exclude), specMode: form.specMode, specProfiles: form.specMode === 'profiles' ? form.specProfiles : [] } }
}
export function animeSourceReady(form: SourceForm) { try { return animeSourceConfig(form, 0).status === 'paused' } catch { return false } }
export function animeBindingKey(form: SourceForm) {
  // Range and numbering are separate rules. Editing them invalidates the
  // release preview, without undoing the user's source-identity selection.
  return JSON.stringify([form.provider, form.itemId, form.groupId])
}
export function animeBindingConfirmed(form: SourceForm) { return form.confirmed && form.confirmedFor === animeBindingKey(form) }
export function confirmAnimeSource(form: SourceForm) {
  if (!form.itemId || !form.groupId) throw new Error('请先选择资源与字幕组')
  if (!form.scope) form.scope = 'feed'
  if (!form.specMode && !form.resolution || form.specMode === 'profiles' && !form.specProfiles.length) throw new Error('请选择至少一种规格，或明确选择全部规格')
  // Validate inputs before recording the explicit review. This never supplies
  // a fictional free-text explanation to satisfy the old contract.
  animeSourceConfig({ ...form, scope: 'feed', correspondence: false, identityNumbering: false, minimum: '1', include: '', exclude: '' }, 0)
  form.evidence = ''; form.review = { method: 'source_review', itemId: form.itemId, groupId: form.groupId }
  form.confirmed = true; form.confirmedFor = animeBindingKey(form)
}
export function animeSpecKey(spec: AnimeSpec) { return JSON.stringify([spec.resolution || '', [...(spec.languages || [])].sort(), spec.subtitleMode || '', spec.codec || '', spec.container || '', spec.bitDepth || '']) }
export function animeSpecLabel(spec?: AnimeSpec) {
  if (!spec) return ''
  const languages = [['zh-Hans', '简'], ['zh-Hant', '繁'], ['ja', '日'], ['en', '英']].filter(([key]) => spec.languages?.includes(key!)).map(([, label]) => label).join('')
  return [spec.resolution, languages, ({ hard: '内嵌', soft: '内封', external: '外挂' } as Record<string, string>)[spec.subtitleMode || ''], ({ h264: 'AVC', h265: 'HEVC', av1: 'AV1', vp9: 'VP9' } as Record<string, string>)[spec.codec || ''], spec.bitDepth ? `${spec.bitDepth}bit` : '', spec.container?.toUpperCase()].filter(Boolean).join(' ')
}
export function animeSourceSpecs(form: SourceForm) { return form.specMode === 'profiles' ? form.specProfiles.map(animeSpecLabel).join('、') || '尚未选择规格' : form.specMode === 'any' ? '全部规格' : form.resolution === 'any' ? '不限画质（原规则）' : form.resolution || '尚未选择规格' }

export function animeProviderName(provider: AnimeSourceProvider) { return provider === 'anibt' ? 'AniBT' : provider === 'garden' ? 'Anime Garden' : '蜜柑' }
export function animeSourceURL(form: SourceForm) { return form.provider === 'garden' ? `https://animes.garden/subject/${encodeURIComponent(form.itemId)}` : form.provider === 'anibt' ? `https://anibt.net/anime/${encodeURIComponent(form.itemId)}` : `https://mikanani.me/Home/Bangumi/${encodeURIComponent(form.itemId)}` }

export function animeOriginLabel(form: { provider: AnimeSourceProvider; groupId: string | null }) {
 if (form.provider !== 'garden') return ''
 const match = /^(dmhy|mikan|moe|ani)_(f|p)[1-9]\d*$/.exec(form.groupId || '')
 if (!match) return ''
 const names: Record<string, string> = { dmhy: '动漫花园', mikan: '蜜柑计划', moe: '萌番组', ani: 'ANi' }
 return `${names[match[1]!] || match[1]}${match[2] === 'p' ? ' · 发布者' : ''}`
}
