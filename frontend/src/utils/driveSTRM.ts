import type { Mapping, Schedule } from '../types/driveSTRM'

export const active = (m: Mapping) => m.status === 'running' || m.queued && !m.lastError
export const pendingMetadata = (m: Mapping) => (m.metadataPending || 0) > 0
export const downloadingMetadata = (m: Mapping) => (m.metadataPending || 0) > (m.metadataFailed || 0)

export function initialSchedule(): Schedule {
  return { reconcileEnabled: true, reconcileIntervalHours: 24, fullScanEnabled: false, fullScanIntervalHours: 720 }
}

export function normalizeSchedule(value: Schedule): Schedule {
  return {
    reconcileEnabled: value.reconcileEnabled && value.reconcileIntervalHours > 0,
    reconcileIntervalHours: value.reconcileIntervalHours || 24,
    fullScanEnabled: value.fullScanEnabled && value.fullScanIntervalHours > 0,
    fullScanIntervalHours: value.fullScanIntervalHours || 720,
  }
}

export function effectiveSchedule(m: Mapping) { return normalizeSchedule(m.schedule || initialSchedule()) }

export function directoryInput(m: Mapping) {
  return { name: m.name, accountId: m.accountId, sourceCID: m.sourceCID, outputDir: m.outputDir, enabled: m.enabled }
}

export function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未检查' }

export function intervalLabel(hours: number) { return hours < 24 ? `每 ${hours} 小时` : hours === 24 ? '每天' : hours === 168 ? '每周' : `每 ${hours / 24} 天` }
