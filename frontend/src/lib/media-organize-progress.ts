// 本地计时只解释服务端进展时间，不触发查询，也不据此判断任务已经停止。
export function mediaProgressSeconds(at: string | undefined, now: number): number | undefined {
  if (!at || at.startsWith('0001-')) return undefined
  const time = Date.parse(at)
  return Number.isFinite(time) ? Math.max(0, Math.floor((now - time) / 1000)) : undefined
}
