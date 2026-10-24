export type NamingKind = 'movie' | 'tv'
export interface NamingBlock { field: string; separator?: string; prefix?: string; suffix?: string; pad?: number }
export interface NamingTemplate { kind: NamingKind; directories: NamingBlock[][]; filename: NamingBlock[] }
export interface NamingField { id: string; label: string; group: string; tvOnly?: boolean; numeric?: boolean }
export interface NamingRecord { revision: number; template: NamingTemplate }
export interface NamingSettings {
  records: Record<NamingKind, NamingRecord>
  defaults: Record<NamingKind, NamingTemplate>
  catalog: Record<NamingKind, NamingField[]>
}
export interface NamingData {
  values: Record<string, string>
  season?: number
  episode?: number
  endEpisode?: number
  originalName?: string
  titleOnly?: boolean
}
export interface NamingResult { directory: string; stem: string; extension: string; name: string; relativePath: string }
export interface NamingPreview { items: { result?: NamingResult; error?: string }[] }
