// 字幕导入、压缩包解压和整理确认共用同一组限制。
export const maxSubtitleFiles = 1000
export const maxFileBytes = 16 * 1024 * 1024
export const maxTotalBytes = 64 * 1024 * 1024

export const subtitleLimitMessages = {
  count: `字幕数量超过 ${maxSubtitleFiles} 个，请减少选择。`,
  file: '单个字幕不能超过 16 MiB。',
  total: '字幕总大小不能超过 64 MiB，请减少选择。',
  empty: '不能导入空字幕文件。',
}

export function subtitleSelectionIssue(count: number, bytes: number): string {
  if (count > maxSubtitleFiles) return subtitleLimitMessages.count
  if (bytes > maxTotalBytes) return subtitleLimitMessages.total
  return ''
}
