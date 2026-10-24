import type { NumberingInfo } from './tvNumbering'
import type { NamingRecord } from './naming'
export interface DownloadResourceInput {
  title?: string
  fansub?: string
  source: 'mikan' | 'garden' | 'gying'
  provider?: string
  providerId: string
  magnet: string
  torrentUrl?: string
}
export interface DownloadFileSpec { resolution?: string; languages?: string[]; fansub?: string }
export interface DownloadFiles { manifestId?: string; files: OrganizeFile[]; subtitles?: OrganizeSubtitle[]; specs?: Record<string, DownloadFileSpec>; reason?: string }
export interface DownloadOrganizeResource {
  resource: { id: string; title: string; magnet: string; fansub?: string; publisher?: string }
  source: DownloadResourceInput
  files?: DownloadFiles
}
export interface DownloadBatchResult { id: string; jobs: OrganizeJob[] }
export interface OrganizeConfig {
  accountId: string
  sourceCID: string
  sourcePath: string
  targetCID: string
  targetPath: string
}
export interface DriveFile { id: string; name: string; directory: boolean; size: number }
export interface OrganizeCleanupItem extends DriveFile {
  parentId: string
  path: string
  status: string
  message?: string
}
export interface OrganizeCleanup {
  id: string
  accountId: string
  parentId: string
  parentPath: string
  mode: 'selected' | 'empty'
  status: string
  message?: string
  createdAt: string
  items: OrganizeCleanupItem[]
}
export interface OrganizeFile extends DriveFile {
  namingValues?: Record<string, string>
  titleOnly?: boolean
  parentId: string
  sourcePath: string
  mediaType: string
  season: number
  episode: number
  endEpisode?: number
}
export interface OrganizeRow extends OrganizeFile { selected: boolean; episodeSpan: number }
export interface OrganizeSubtitle { file: OrganizeFile; videoId?: string; tail?: string }
export interface OrganizeItem {
  stem?: string
  file: OrganizeFile
  directory: string
  name: string
  targetPath: string
  targetCID?: string
  conflict?: string
  status: string
  message?: string
}
export interface OrganizeJob {
	naming?: NamingRecord
	resumable?: boolean
  download?: { hash: string; directoryId?: string }
  numbering?: NumberingInfo
  id: string
  config: OrganizeConfig
  title: string
  status: string
  message?: string
  createdAt: string
  items: OrganizeItem[]
  stats?: {
    batchSize: number
    batchesTotal: number
    batchesCompleted: number
    directoryRequests: number
    renameRequests: number
    moveRequests: number
    directoryCreates: number
    httpRequests: number
    checkpoints: number
    httpMs: number
    waitMs: number
    directoryMs: number
    mutationMs: number
    saveMs: number
    elapsedMs: number
  }
}
