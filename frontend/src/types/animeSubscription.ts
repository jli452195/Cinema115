import type { AnimeSourceProvider } from './animeCatalog'
export interface AnimeBinding {
  schemaVersion: 1
  confirmed: boolean
  scope: null | { kind: 'feed' } | { kind: 'range'; first: number; last: number } | { kind: 'confirmed_chapter'; evidence?: string }
  chapterCorrespondence: null | { sourceEpisode: number; chapterEpisode: number }
  review?: { method: 'source_review'; itemId: string; groupId: string }
}
export interface AnimeSpec { resolution?: string; languages?: string[]; subtitleMode?: string; codec?: string; container?: string; bitDepth?: string }
export interface AnimeRules {
  schemaVersion: 1
  resolution: null | '720p' | '1080p' | '2160p' | 'any'
  include: string[]
  exclude: string[]
  minimumEpisode: number | null
  initialMode: 'from_episode' | 'new_only' | 'selected_history'
  specMode?: 'profiles' | 'any' | ''
  specProfiles?: AnimeSpec[]
}
export interface AnimeSourceConfig {
  ref: string; id: string; expectedRevision: number
  role: 'main' | 'standby'; position: number; status: 'draft' | 'paused'
  runWhenEnabled: boolean; provider: AnimeSourceProvider
  itemId: string | null; groupId: string | null; groupName: string | null
  binding: AnimeBinding; rules: AnimeRules
}
export interface AnimeSettings {
  revision: number; accountId: string | null; accountRevision: number | null; uid: string | null
  downloadCid: string | null; downloadPath: string | null
  organizeCid: string | null; organizePath: string | null
}
export interface AnimeSettingsView {
  settings: AnimeSettings
  primaryAccount: { id: string; name: string; uid: string; revision: number } | null
  readiness: { ready: boolean; issues: string[] }
}
export interface MikanEntry { id: string; title: string; groups: { id: string; name: string; origin?: string; originName?: string; kind?: string }[]; groupsPartial?: boolean; bangumiIds: string[] }
export interface MikanSearch { items: { id: string; title: string; coverUrl: string; url: string }[]; truncated: boolean }
export interface AnimeSourceSample {
  source: { provider?: AnimeSourceProvider; itemId: string; groupId: string; title: string; groupName: string; expiresAt: number }
  releases: { postKey: string; title: string; parse: { episode: number | null; kind: string; spec: AnimeSpec }; specKey: string; sizeRaw: string; publishedRaw: string }[]
  profiles: { key: string; label: string; spec: AnimeSpec; count: number }[]
  profilesTruncated: boolean; profileReviewCount?: number; completeness: string; observedAt: number | null; specVersion: number
}
export interface AnimePreviewRef { batchId: string; sourceRef: string; sourcePreviewId: string; loadRevision: number; sourceId?: string }
export interface AnimeFormSelection { sourceRef: string; selectedReleaseIds: string[]; unresolvedReleaseIds: string[]; dismissedReleaseIds: string[] }
export interface AnimePreviewSummary {
  sourceRef: string; sourcePreviewId: string; loadRevision: number
  generatedAt: number; expiresAt: number; expired: boolean
  rawCount: number; loadedCount: number; matchedCount: number; reviewCount: number; selectedCount: number; pendingSelectionCount: number
  completeness: string; stopReason: string; loadedFirst: number | null; loadedLast: number | null
  gapsWithinLoadedMatches: { first: number; last: number }[]; unloadedHistory: boolean
}
export interface AnimePreviewItem {
  release: { id: string; title: string; parse: { kind: string; episode: number | null; rawEpisode: string; resolution: string | null; versionLabel: string; spec?: AnimeSpec } }
  observation: { title: string; url: string; sizeRaw: string }
  projection: { status: string; sourceEpisode: number | null; chapterEpisode: number | null }
  decision: string; reasonCode: string; selectable: boolean
}
export interface AnimePendingSelection { releaseId: string; reason: string; previousItem: AnimePreviewItem | null }
export interface AnimePreviewPage {
  items: AnimePreviewItem[]; total: number; page: number; pageSize: number; summary: AnimePreviewSummary
  selectedIds: string[]; pendingSelections: AnimePendingSelection[]
}
export interface AnimePreviewResponse {
	selectionNotice?: string
  batchId: string; budget: { totalRawCount: number; limit: number; remaining: number }
  sources: AnimePreviewSummary[]; issues: { sourceRef: string; code: string }[]; page?: AnimePreviewPage
}
export interface AnimeSubscriptionView {
  naming?: AnimeNamingPlan | null
  id: string; entryId: string; status: string; revision: number
  createdAt?: number; updatedAt?: number; dataVersion?: string
  firstEnabledAt?: number | null
  permittedActions?: { name: string; allowed: boolean; reasonCode?: string }[]
  jobStats?: { totalCount: number; queuedCount: number; downloadingCount: number; remoteCompletedCount: number; failedCount: number; needsReviewCount: number }
  completionStats?: { originalSavedCount: number; organizedCount: number }
  readiness?: { ready: boolean; issues: string[] }
  entry: { id: string; bangumiId: string | null; editionKey: string; title: string; originalTitle: string; revision: number; metadata: { coverUrl: string; airDate: string } }
  sources: (Omit<AnimeSourceConfig, 'ref' | 'status' | 'expectedRevision'> & { revision: number; configuredState: string; effectiveState?: string; checkedAt?: number | null; health?: string; errorCode?: string | null; nextCheckAt?: number | null })[]
  form: { formSelections: AnimeFormSelection[]; previewRefs: AnimePreviewRef[] }
}
export interface AnimeDownloadJob {
	organizationPlanned?: boolean
  organization?: { state: string; total: number; renamed: number; moved: number; targetPath: string; updatedAt: number }
  id: string; title: string; groupName: string; revision: number
  downloadState: string; fileState: string; workflowState: string; blockCode: string | null
  executionPath: string; directoryCid: string | null; createdAt: number; updatedAt: number
  observedAt: number; attemptedAt: number; nextRunAt: number | null; progress: number | null
  files: { id: string; name: string; kind: string; size: string }[]
  permittedActions: { reconcile: boolean; retry: boolean }
}
export interface AnimeDownloadPage { items: AnimeDownloadJob[]; total: number; page: number; pageSize: number; dataVersion: string }
export interface AnimeSubscriptionPage { items: AnimeSubscriptionView[]; total: number; page: number; pageSize: number; dataVersion: string }
export interface AnimeSaveReceipt { id: string; entryId: string; revision: number; status: string; sourceIds: Record<string, string>; sourceRevisions: Record<string, number>; requiresRepreview: boolean }

export interface AnimeMappingSegment { first: number; last: number | null; sourceBase: number; targetSeason: number; targetBase: number }
export interface AnimeMappingOverride { rawKind: 'episode' | 'special' | 'composite'; rawNumber: string; action: 'map' | 'ignore'; targetSeason: number; targetEpisode: number }
export interface AnimeSourceMapping { sourceRef: string; segments: AnimeMappingSegment[]; overrides: AnimeMappingOverride[] }
export interface AnimeNamingTarget {
  tmdbId: string; title: string; year: string; groupId: string; groupName: string; season: number; fingerprint: string
  seasons: { number: number; name: string; date: string; episodeCount: number; episodes: { id: string; number: number; name: string; date?: string; sourceSeason: number; sourceNumber: number }[] | null }[]
}
export interface AnimeNamingInput { tmdbId: string; groupId: string; season: number; fingerprint: string; loadedSeasons?: number[]; sources: AnimeSourceMapping[] }
export interface AnimeNamingPlan { schemaVersion: 1; target: AnimeNamingTarget; sources: AnimeSourceMapping[] }
export interface AnimeOrganizationItem { id: string; kind: string; originalPath: string; targetPath: string; status: string; reason: string; videoId?: string }
export interface AnimeOrganizationPreview { mode: string; revision: number; items: AnimeOrganizationItem[]; example?: boolean; cloudConflictChecked: boolean; ready?: number; needsReview?: number }
export interface AnimeNamingCandidate { id: number; title: string; originalTitle: string; date: string }
