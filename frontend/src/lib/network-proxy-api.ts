export interface NetworkProxyInput {
  enabled: boolean
  url: string
  username: string
  password?: string
  services: string[]
}
export interface NetworkProxyConfig extends Omit<NetworkProxyInput, 'password'> {
  passwordConfigured: boolean
  availableServices: { id: string; label: string }[]
}
export interface NetworkProxyTest {
  service: string
  proxied: boolean
  reachable: boolean
  status?: number
  elapsedMs: number
  message: string
}

export async function networkProxyRequest<T>(token: string, signal: AbortSignal, body?: NetworkProxyInput & { service?: string }, test = false): Promise<T> {
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(35000)])
  const response = await fetch(`/api/admin/network-proxy${test ? '/test' : ''}`, {
    method: body === undefined ? 'GET' : 'POST', cache: 'no-store', signal: requestSignal,
    headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  const data = await response.json().catch(() => null)
  requestSignal.throwIfAborted()
  if (!response.ok) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || `请求失败（HTTP ${response.status}）`)
  if (!data || typeof data !== 'object') throw new Error('接口未返回有效数据，请检查后端服务')
  return data as T
}
