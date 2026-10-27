import { subtitleLanguage } from './subtitleImport'

export interface SubtitleVersion { id: string; title: string; originalTitle: string; englishTitle: string; year: string; source: string; name: string; description?: string; url: string }
export interface LibrarySubtitleFile { path: string; name: string; sha: string; size: number; revision: string }
export interface LibrarySubtitleSelection { versionId: string; path: string; videoId: string; language: string; file: LibrarySubtitleFile }
export const subtitleLanguages = [{ value: '', label: '未指定' }, { value: 'zh-CN', label: '简体' }, { value: 'zh-TW', label: '繁体' }, { value: 'zh-en', label: '中英' }, { value: 'ja', label: '日文' }, { value: 'en', label: '英文' }]
export function libraryLanguage(name: string) {
  return subtitleLanguage(name) || (/(?:^|[._\[ -])(?:jp|jpn|ja)(?:[._\] -]|$)|日文|日语|日語/i.test(name) ? 'ja' : '')
}
export function subtitleTargetName(video: string, file: string, language: string) {
  return video.replace(/\.[^.]+$/, '') + (language ? `.${language}` : '') + (file.match(/\.[^.]+$/)?.[0].toLowerCase() || '')
}
