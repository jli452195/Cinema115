export interface ScheduledEpisode { number: number; date: string }
export interface DisplayProgressSegment { state: 'owned' | 'missing' | 'unknown'; count: number }

// TMDB 只提供播出日期，按浏览器当地日期判断。
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
  // 实际入库数量独立于播出日期，额外集不能抵消 TMDB 分集缺口。
  const ownedNumbers = [...owned].filter(number => Number.isInteger(number) && number >= 0).sort((a, b) => a - b)
  const undatedCount = unique.filter(episode => !/^\d{4}-\d{2}-\d{2}$/.test(episode.date)).length
  const missingSet = new Set(missing)
  const segments: { owned: boolean; count: number }[] = []
  // 连续同色合成一段，千集长剧不再为每集创建一个进度条节点。
  for (const episode of aired) {
    const owned = !missingSet.has(episode.number), previous = segments[segments.length - 1]
    if (previous?.owned === owned) previous.count++
    else segments.push({ owned, count: 1 })
  }
  const complete = aired.length > 0 && aired.length >= Math.max(total, unique.length) && missing.length === 0
  // 日期不完整时展示 TMDB 全部分集，仅将无日期的内部断档补充为缺集。
  // 已知未来日期仍然有效；额外集不扩展范围。使用实际编号以兼容特别篇第 0 集。
  const hasReliableDates = unique.some(episode => /^\d{4}-\d{2}-\d{2}$/.test(episode.date))
  const numberingMode = !hasReliableDates && total > 0 && unique.length > 0 && unique.every(episode => episode.number >= 1)
  const listedNumbers = new Set(unique.map(episode => episode.number))
  const listedOwned = ownedNumbers.filter(number => listedNumbers.has(number))
  const firstOwned = listedOwned[0] ?? Infinity
  const lastOwned = listedOwned[listedOwned.length - 1] ?? -Infinity
  const displayEpisodes = numberingMode ? unique : aired
  const displayMissing: number[] = []
  const displaySegments: DisplayProgressSegment[] = []
  let displayCollected = 0
  for (const episode of displayEpisodes) {
    const internalGap = !/^\d{4}-\d{2}-\d{2}$/.test(episode.date) && episode.number > firstOwned && episode.number < lastOwned
    const state = owned.has(episode.number) ? 'owned' : internalGap ? 'missing' : 'unknown'
    if (state === 'owned') displayCollected++
    if (state === 'missing') displayMissing.push(episode.number)
    const previous = displaySegments[displaySegments.length - 1]
    if (previous?.state === state) previous.count++
    else displaySegments.push({ state, count: 1 })
  }
  const displayTotal = displayEpisodes.length
  const displayComplete = numberingMode && displayTotal >= total && displayCollected >= total && displayMissing.length === 0
  const unlistedOwned = unique.length ? ownedNumbers.filter(number => !listedNumbers.has(number)) : []
  return { aired, missing, segments, missingLabel: formatEpisodeRanges(missing), collected, complete,
    ownedCount: ownedNumbers.length, ownedLabel: formatEpisodeRanges(ownedNumbers), undatedCount,
    extraOwned: ownedNumbers.length - collected, numberingMode, displayTotal, displayCollected, displayComplete,
    displayMissing, displayMissingLabel: formatEpisodeRanges(displayMissing), displaySegments, unlistedOwned,
    caughtUp: aired.length > 0 && !missing.length && !complete && undatedCount === 0 }
}
