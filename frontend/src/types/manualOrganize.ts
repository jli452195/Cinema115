export interface OrganizeConfig {
  accountId: string
  sourceCID: string
  sourcePath: string
  targetCID: string
  targetPath: string
}
export interface DriveFile { id: string; name: string; directory: boolean; size: number }
export interface OrganizeFile extends DriveFile {
  parentId: string
  sourcePath: string
  mediaType: string
  season: number
  episode: number
  suffix: string
}
export interface OrganizeRow extends OrganizeFile { selected: boolean; endEpisode: number | '' }
export interface OrganizeItem {
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
