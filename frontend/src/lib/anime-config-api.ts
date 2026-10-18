import { portalToken, portalUser } from '../composables/usePortalAuth'

export class AnimeConfigError extends Error {
  constructor(message: string, public code = '', public status = 0, public details: Record<string, unknown> = {}, public uncertain = false) { super(message) }
}
export function animeConfigMessage(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，输入已保留' }
// getRandomValues also works on LAN HTTP pages, where randomUUID is unavailable.
export function animeRequestKey() { return Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('') }

// No write is retried here: callers retain an immutable request and its key when the result is unknown.
export async function animeConfigRequest<T>(token: string, path: string, options: { method?: string; body?: unknown; key?: string; signal?: AbortSignal } = {}): Promise<T> {
  const actor = portalUser.value?.id
  if (!token || token !== portalToken.value || portalUser.value?.role !== 'admin') throw new AnimeConfigError('请使用管理员身份重新登录', 'ADMIN_REQUIRED', 403)
  const write = !!options.key
  const signal = AbortSignal.any([...(options.signal ? [options.signal] : []), AbortSignal.timeout(60000)])
  let response: Response
  try {
    response = await fetch(`/api/admin/anime/${path}`, {
      method: options.method || 'GET', signal, cache: 'no-store',
      headers: { Authorization: `Bearer ${token}`, ...(options.body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...(options.key ? { 'Idempotency-Key': options.key } : {}) },
      ...(options.body !== undefined ? { body: JSON.stringify(options.body) } : {}),
    })
  } catch {
    throw new AnimeConfigError(write ? '保存结果尚未确认，请重试原请求核对结果' : '读取失败或超时，输入已保留', '', 0, {}, write)
  }
  if (token !== portalToken.value || actor !== portalUser.value?.id || portalUser.value?.role !== 'admin') throw new AnimeConfigError('登录身份已变化，请重新打开页面', 'AUTH_CHANGED', 403, {}, write)
  const data = await response.json().catch(() => null)
  if (signal.aborted || token !== portalToken.value || actor !== portalUser.value?.id || portalUser.value?.role !== 'admin') throw new AnimeConfigError('请求已取消或登录身份已变化', 'AUTH_CHANGED', 403, {}, write)
  if (!response.ok) {
    const details = data?.details || {}
    const seconds = Number(response.headers.get('Retry-After'))
    if (response.status === 429 && !details.retryAfterAt) details.retryAfterAt = Date.now() + (Number.isFinite(seconds) && seconds > 0 ? seconds : 60) * 1000
    throw new AnimeConfigError(data?.error || '操作失败，输入已保留', data?.code || '', response.status, details, write && (response.status >= 500 || response.status === 408))
  }
  if (!data || typeof data !== 'object') throw new AnimeConfigError(write ? '保存回执无法确认，请重试原请求' : '来源数据无法读取', 'INVALID_RESPONSE', 0, {}, write)
  return data as T
}
