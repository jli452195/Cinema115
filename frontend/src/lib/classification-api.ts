import type { ClassificationDiagnostic } from '../types/classification'

export async function classificationRequest<T>(token: string, path = '', init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/admin/classification${path}`, {
    ...init, cache: 'no-store', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw Object.assign(new Error(response.status === 401 ? '登录已过期，请重新登录' : data?.error || `请求失败（HTTP ${response.status}）`), { status: response.status, diagnostic: data?.diagnostic as ClassificationDiagnostic | undefined })
  if (!data) throw new Error('接口未返回有效数据，请重试')
  return data as T
}
