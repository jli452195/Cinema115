export interface SubtitleVersion { id: string; title: string; originalTitle: string; englishTitle: string; year: string; source: string; name: string; description?: string; url: string }
export interface LibrarySubtitleFile { path: string; name: string; sha: string; size: number; revision: string }
export interface LibrarySubtitleSelection { versionId: string; path: string; videoId: string; language: string; version: string; file: LibrarySubtitleFile }
export const subtitleLanguages = [
  { value: '', label: '未指定' }, { value: 'zh-CN', label: '简体' }, { value: 'zh-TW', label: '繁体' },
  { value: 'zh-HK', label: '香港中文' }, { value: 'zh-SG', label: '新加坡中文' }, { value: 'zh-en', label: '中英' },
  { value: 'ja', label: '日文' }, { value: 'en', label: '英文' }, { value: 'zh', label: '中文' },
]
