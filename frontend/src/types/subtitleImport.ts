import type { DriveFile } from './manualOrganize'

export interface SubtitleVideo extends DriveFile { parentId: string; path: string }
export interface SubtitleSelection {
  localId: string
  originalName: string
  videoId: string
  language: string
  version: string
  size: number
  sha256: string
}
export interface SubtitleImportItem extends SubtitleSelection {
  video: SubtitleVideo
  name: string
  targetPath: string
  status: string
  message?: string
}
export interface SubtitleImport {
  id: string
  accountId: string
  jobId: string
  status: string
  message?: string
  createdAt: string
  items: SubtitleImportItem[]
}
