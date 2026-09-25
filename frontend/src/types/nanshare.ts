export interface NanShareConfigView {
  baseUrl: string
  username: string
  configured: boolean
  checkedAt: string
  session: { ok: boolean; message: string }
  re0: { ok: boolean; message: string }
  share: { ok: boolean; message: string }
}

export async function nanShareRequest<T>(token: string, path: string, signal: AbortSignal, method = 'GET', body?: object): Promise<T> {
  const response = await fetch(`/api/admin/nanshare/${path}`, {
    method,
    signal: AbortSignal.any([signal, AbortSignal.timeout(path === 'submit' ? 135000 : 100000)]),
    cache: 'no-store',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw Object.assign(new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `RE0 请求失败（HTTP ${response.status}）`), { status: response.status })
  return data as T
}

export interface RE0Resource {
  slug: string; key: string; name: string; remark: string; title: string
  pan_type: string; video_resolution: string[] | null; resource_sources: string[] | null
  share_size: string; publisher: string; publish_time: string
  subtitle_language: string[] | null; subtitle_type: string[] | null
  unlock_points: number | null; is_unlocked: boolean; unlocked_users_count: number
  is_official: boolean; validate_status: string
}
export interface RE0Submission { requestId: string; state: 'submitting' | 'accepted' | 'unknown' | 'failed'; message: string }
export interface RE0SearchResult { id: string; items: RE0Resource[]; submissions: Record<string, RE0Submission>; expiresAt: string }
export interface RE0ShareOptions { version: string; accountId: string; modes: ('normal' | 'reshare')[]; folders: { index: number; name: string; path: string }[] }
