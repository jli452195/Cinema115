export interface CD2Input {
  enabled: boolean
  url: string
  token?: string
}

export interface CD2Config {
  id: string
  revision: number
  enabled: boolean
  url: string
  tokenConfigured: boolean
}

export interface CD2Permissions {
  list: boolean
  read: boolean
  createFolder: boolean
  rename: boolean
  move: boolean
  copy: boolean
  delete: boolean
}

export interface CD2Test {
  reachable: boolean
  authorized: boolean
  listable: boolean
  version?: string
  elapsedMs: number
  message: string
  permissions?: CD2Permissions
}

export interface CD2DirectoryPage {
  connectionId: string
  revision: number
  path: string
  parentPath: string | null
  readOnly?: boolean
  directories: { id: string; name: string; path: string; readOnly?: boolean }[]
  truncated: boolean
}

export interface CD2DirectorySelection {
  path: string
  connectionId: string
  revision: number
}

export async function cd2Request<T>(token: string, path = '', init: RequestInit = {}): Promise<T> {
  const signal = AbortSignal.any([...(init.signal ? [init.signal] : []), AbortSignal.timeout(35000)])
  const response = await fetch(`/api/admin/cd2${path}`, {
    ...init, signal, cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, ...(init.body === undefined ? {} : { 'Content-Type': 'application/json' }) },
  })
  const data = await response.json().catch(() => null)
  signal.throwIfAborted()
  if (!response.ok) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || `请求失败（HTTP ${response.status}）`)
  if (!data || typeof data !== 'object') throw new Error('接口未返回有效数据，请检查后端服务')
  return data as T
}
