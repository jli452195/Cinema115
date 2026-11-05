export interface Settings {
  baseUrl: string; videoExtensions: string; metadataExtensions: string; minSizeMB: number
  eventIntervalSeconds: number
  metadataImageMode: 'cdn' | 'original'; metadataConcurrency: number; metadataBatchIntervalSeconds: number
}
export interface Schedule {
  reconcileEnabled: boolean; reconcileIntervalHours: number
  fullScanEnabled: boolean; fullScanIntervalHours: number
}
export interface Mapping {
  id: string; name: string; accountId: string; sourceCID: string; sourcePath: string; outputDir: string; enabled: boolean
  status: string; mode?: string; queued: boolean; lastScanAt?: string; lastError?: string
  metadataPending: number; metadataFailed: number
  schedule: Schedule
  stats: { videos: number; written: number; unchanged: number; metadataWritten: number; failed: number }
}
export interface State {
  mappings: Mapping[]; settings: Settings; settingsSaved: boolean; primaryAccountId: string; accountReady: boolean
  events?: { lastCheckedAt?: string; lastError?: string; consumers?: Record<string, { pendingBatches: number; lastError?: string }> }
  attachments?: { coolingUntil?: string; lastError?: string; probing?: boolean }
}
