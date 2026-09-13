export interface ScheduledEpisode { number: number; date: string }

// TMDB 只提供播出日期，按浏览器当地日期判断；日期未定的分集不参与缺集比较。
export function localDateKey(now = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}
export function episodeHasAired(episode: ScheduledEpisode, today = localDateKey()): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(episode.date) && episode.date <= today
}
// 连续缺集合并成区间，零散集数单独保留；特别篇的第 0 集也可正常显示。
export function formatEpisodeRanges(numbers: number[]): string {
  const sorted = [...new Set(numbers)].sort((a, b) => a - b)
  const ranges: string[] = []
  for (let index = 0; index < sorted.length; index++) {
    const start = sorted[index]!
    let end = start
    while (index + 1 < sorted.length && sorted[index + 1] === end + 1) end = sorted[++index]!
    ranges.push(start === end ? String(start) : `${start}-${end}`)
  }
  return ranges.join('、')
}
export function seasonLibraryProgress(episodes: ScheduledEpisode[], owned: ReadonlySet<number>, total: number, today = localDateKey()) {
  const unique = [...new Map(episodes.map(episode => [episode.number, episode])).values()].sort((a, b) => a.number - b.number)
  const aired = unique.filter(episode => episodeHasAired(episode, today))
  const missing = aired.filter(episode => !owned.has(episode.number)).map(episode => episode.number)
  const collected = aired.length - missing.length
  const missingSet = new Set(missing)
  const segments: { owned: boolean; count: number }[] = []
  // 连续同色合成一段，千集长剧不再为每集创建一个进度条节点。
  for (const episode of aired) {
    const owned = !missingSet.has(episode.number), previous = segments[segments.length - 1]
    if (previous?.owned === owned) previous.count++
    else segments.push({ owned, count: 1 })
  }
  const complete = aired.length > 0 && aired.length >= Math.max(total, unique.length) && missing.length === 0
  return { aired, missing, segments, missingLabel: formatEpisodeRanges(missing), collected, complete, caughtUp: aired.length > 0 && !missing.length && !complete }
}
