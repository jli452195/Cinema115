import type { AnimeSourceProvider } from '../types/animeCatalog'
import { animeRequestKey } from '../lib/anime-config-api'
import type { AnimeSourceConfig, AnimePreviewPage, AnimePreviewRef, AnimePreviewSummary, AnimeSourceSample, AnimeSpec, AnimeBinding, MikanEntry, MikanSearch } from '../types/animeSubscription'

export interface SourceForm {
  firstEpisode: string
  provider: AnimeSourceProvider; ref: string; id: string; revision: number; role: 'main' | 'standby'; runWhenEnabled: boolean
  itemId: string; itemTitle: string; groupId: string; groupName: string
  lockedItem: boolean; lockedGroup: boolean
  keyword: string; searchedKeyword: string; search?: MikanSearch; entry?: MikanEntry; searchBusy: boolean; searchError: string; cooldown: number
  confirmed: boolean; confirmedFor: string; review?: AnimeBinding['review']
  mappingOffset: string
  templateChosen: boolean; match: string; episodePattern: string; episodeGroup: string
  health?: string; errorCode?: string | null
  ruleError?: { field: string; message: string }
  sample?: AnimeSourceSample; sampleBusy: boolean; sampleError: string; sampleCooldown: number
  minimum: string; exclude: string
  preview?: { ref: AnimePreviewRef; summary: AnimePreviewSummary; signature: string; page?: AnimePreviewPage }
  previewError: string; previewCooldown: number
}
export function newAnimeSource(role: SourceForm['role'], keyword = '', provider: AnimeSourceProvider = 'mikan'): SourceForm {
  return { firstEpisode: '', provider, ref: role === 'main' ? 'main' : animeRequestKey(), id: '', revision: 0, role, runWhenEnabled: true,
    itemId: '', itemTitle: '', groupId: '', groupName: '', lockedItem: false, lockedGroup: false,
    keyword, searchedKeyword: '', searchBusy: false, searchError: '', cooldown: 0,
    confirmed: false, confirmedFor: '',
    mappingOffset: '0',
    templateChosen: false, match: '', episodePattern: '', episodeGroup: '1', sampleBusy: false, sampleError: '', sampleCooldown: 0,
    minimum: '0', exclude: '', previewError: '', previewCooldown: 0 }
}
function episode(value: string, label: string, first = 0): number | null {
  if (!value.trim()) return null
  if (!/^(0|[1-9]\d{0,4})$/.test(value.trim()) || Number(value) < first) throw new Error(`${label}请填写 ${first} 至 99999 的整数`)
  return Number(value.trim())
}
function patterns(value: string) {
  const result = [...new Set(value.split(/\r?\n/).map(pattern => pattern.trim()).filter(Boolean))]
  if (result.length > 20 || result.some(pattern => Array.from(pattern).length > 512 || /[\u0000-\u001f\u007f]/.test(pattern))) throw new Error('匹配和排除规则各最多 20 条，每条最多 512 字，不能含控制字符')
  return result
}
export function animeSourceConfig(form: SourceForm, position: number, total?: number | null): AnimeSourceConfig {
  const firstEpisode = episode(form.firstEpisode, '订阅首话')
  if (total && firstEpisode != null && (!Number.isInteger(total) || total < 1 || firstEpisode + total - 1 > 99999)) throw new Error('订阅范围不能超过第 99999 话')
  const minimumEpisode = episode(form.minimum, '补番起点')
  const confirmed = animeBindingConfirmed(form)
  const complete = !!(form.itemId && form.groupId && confirmed && minimumEpisode !== null)
  const episodePattern = form.episodePattern.trim()
  if (Array.from(episodePattern).length > 2048) throw new Error('集号正则最多 2048 字')
  if (episodePattern && !/^[1-9]\d{0,3}$/.test(form.episodeGroup)) throw new Error('捕获组序号请填写从 1 开始的整数')
  return { ref: form.ref, id: form.id, expectedRevision: form.revision, role: form.role, position, status: complete ? 'paused' : 'draft', runWhenEnabled: form.runWhenEnabled, provider: form.provider,
    itemId: form.itemId || null, groupId: form.groupId || null, groupName: form.groupName || null,
    binding: { schemaVersion: 1, confirmed, scope: !confirmed ? null : total && firstEpisode != null ? { kind: 'range', first: firstEpisode, last: firstEpisode + total - 1 } : { kind: 'feed' }, chapterCorrespondence: null, ...(confirmed && form.review ? { review: form.review } : {}) },
    rules: { chapterFirstEpisode: firstEpisode, schemaVersion: 1, minimumEpisode, match: patterns(form.match), exclude: patterns(form.exclude), ...(episodePattern ? { episodePattern, episodeGroup: Number(form.episodeGroup) } : {}) } }
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
  if (!form.templateChosen) throw new Error('请选择一个匹配模板，或选择全部规格')
  // Validate inputs before recording the explicit review. This never supplies
  // a fictional free-text explanation to satisfy the old contract.
  animeSourceConfig({ ...form, minimum: '0', match: '', exclude: '' }, 0)
  suggestAnimeFirstEpisode(form)
  form.review = { method: 'source_review', itemId: form.itemId, groupId: form.groupId }
  form.confirmed = true; form.confirmedFor = animeBindingKey(form)
}
export function suggestAnimeFirstEpisode(form: SourceForm) {
  if (form.firstEpisode || !form.sample) return
  const numbers = form.sample.releases.filter(r => r.parse.kind === 'episode').map(r => r.parse.episode).filter((n): n is number => n != null && Number.isInteger(n) && n >= 0)
  if (numbers.includes(0)) form.firstEpisode = '0'
  else if (numbers.includes(1)) form.firstEpisode = '1'
  else if (form.sample.completeness === 'visible_end' && numbers.length) form.firstEpisode = String(Math.min(...numbers))
}
export function animeSpecLabel(spec?: AnimeSpec) {
  if (!spec) return ''
  const languages = [['zh-Hans', '简'], ['zh-Hant', '繁'], ['ja', '日'], ['en', '英']].filter(([key]) => spec.languages?.includes(key!)).map(([, label]) => label).join('')
  return [spec.resolution, languages, ({ hard: '内嵌', soft: '内封', external: '外挂' } as Record<string, string>)[spec.subtitleMode || ''], ({ h264: 'AVC', h265: 'HEVC', av1: 'AV1', vp9: 'VP9' } as Record<string, string>)[spec.codec || ''], spec.bitDepth ? `${spec.bitDepth}bit` : '', spec.container?.toUpperCase()].filter(Boolean).join(' ')
}
export function animeSourceSpecs(form: SourceForm) { const count = form.match.split(/\r?\n/).filter(pattern => pattern.trim()).length; return count ? `${count} 条匹配正则` : '全部规格' }

export function animeProviderName(provider: AnimeSourceProvider) { return provider === 'anibt' ? 'AniBT' : provider === 'garden' ? 'Anime Garden' : '蜜柑' }
export function animeSourceURL(form: SourceForm) { return form.provider === 'garden' ? `https://animes.garden/subject/${encodeURIComponent(form.itemId)}` : form.provider === 'anibt' ? `https://anibt.net/anime/${encodeURIComponent(form.itemId)}` : `https://mikanani.me/Home/Bangumi/${encodeURIComponent(form.itemId)}` }

export function animeOriginLabel(form: { provider: AnimeSourceProvider; groupId: string | null }) {
 if (form.provider !== 'garden') return ''
 const match = /^(dmhy|mikan|moe|ani)_(f|p)[1-9]\d*$/.exec(form.groupId || '')
 if (!match) return ''
 const names: Record<string, string> = { dmhy: '动漫花园', mikan: '蜜柑计划', moe: '萌番组', ani: 'ANi' }
 return `${names[match[1]!] || match[1]}${match[2] === 'p' ? ' · 发布者' : ''}`
}
