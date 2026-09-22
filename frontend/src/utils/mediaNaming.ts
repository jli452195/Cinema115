/** 生成 Emby/TMDB 常用的刮削目录或文件名。 */
export function formatScrapeName(title: string, date: string, tmdbId: string | number): string {
  const cleanTitle = title.trim()
  const year = /^(\d{4})/.exec(date.trim())?.[1]
  return `${cleanTitle}${year ? ` (${year})` : ''} {tmdb-${String(tmdbId).trim()}}`
}
