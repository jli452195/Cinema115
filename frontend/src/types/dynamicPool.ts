import type { P115Account } from './p115Accounts'

export interface DynamicPoolSettings {
  maxSessions: number
  maxAttempts: number
  budgetSeconds: number
  leaseMinutes: number
  cooldownSeconds: number
  uidIntervalMs: number
  globalIntervalMs: number
  notificationBotId: string
}
export interface DynamicPoolMetrics {
  startedAt?: number
  requests: number; succeeded: number; failed: number; switches: number
  p50Ms: number; p95Ms: number; samples: number; peakUidSessions: number
  resolved: number; copyResolutions: number; directLinkHits: number
  switchedRequests?: number; copyReuses?: number; deviceConflicts?: number
  capacityDenials?: number; capacityTransfers?: number
}
export interface DynamicPoolBudget {
  requests: number; throttled: number; rejected: number; waitMs: number; waiting: number
  sourceRequests: number
  stages?: Record<string, number>; origins?: Record<string, number>
}
export interface DynamicPoolEvent { id: number; accountId: string; state: string; at: number }
export interface DynamicPoolSnapshot {
  settings: DynamicPoolSettings
  accounts: P115Account[]
  metrics: DynamicPoolMetrics
  api: DynamicPoolBudget
  events?: DynamicPoolEvent[]
}

export const poolHealthLabel = (state?: string) => ({
  ready: '最近检查通过', pending: '等待复查', unknown: '尚未检查', unconfigured: '未完成配置',
  invalid_credentials: '登录失效', invalid_directory: '目录失效', cooldown: '故障冷却', throttled: '限流冷却',
}[state || 'unknown'] || '等待复查')

export function poolUIDs(accounts: P115Account[], checkedOnly = false) {
  return new Set(accounts.filter(a => !a.primary && a.configured && a.uid && a.playbackParentCid
    && (a.pool?.mode || 'enabled') === 'enabled' && (!checkedOnly || a.pool?.state === 'ready')).map(a => a.uid))
}
