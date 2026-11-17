export interface ED2KSTRMSettings {
  baseUrl: string
  outputDir: string
}

export interface ED2KSTRMResource {
  hash: string
  sha1?: string
  size: number
  name: string
  status: string
  message?: string
  strmPath?: string
  cacheState?: string
  updatedAt?: string | number
}

export interface ED2KSTRMState {
  settings: ED2KSTRMSettings
  settingsSaved: boolean
  accountReady: boolean
  primaryAccountId: string
  cacheCID?: string
  cachePath?: string
  resources: ED2KSTRMResource[]
}

export interface ED2KSTRMImportResult {
  resources: ED2KSTRMResource[]
}
