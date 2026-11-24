export interface ED2KSTRMSettings {
  baseUrl: string
  cacheAccountId?: string
  cacheCID?: string
  cachePath?: string
}

export interface ED2KSTRMTarget {
  id: string
  name: string
  outputDir: string
  assignedBotId?: string
}

export interface ED2KSTRMTask {
  id: string
  hash: string
  size: number
  name: string
  targetName: string
  status: 'queued' | 'waiting' | 'failed'
  message?: string
  createdAt: string
  updatedAt: string
}

export interface ED2KSTRMResource {
  hash: string
  sha1?: string
  size: number
  name: string
  status: string
  message?: string
  cacheState?: string
  updatedAt?: string | number
}

export interface ED2KSTRMState {
  settings: ED2KSTRMSettings
  settingsSaved: boolean
  accountReady: boolean
  primaryAccountId: string
  cacheReady?: boolean
  cacheSetupHint?: string
  credentialPaused: number
  targets: ED2KSTRMTarget[]
  tasks: ED2KSTRMTask[]
  resources: ED2KSTRMResource[]
}

export interface ED2KSTRMImportResult {
  resources: ED2KSTRMResource[]
  tasks: ED2KSTRMTask[]
}
