export interface TelegramUserStatus { apiId: number; configured: boolean; hasSession: boolean; authorized: boolean; name: string; username: string; stage: 'idle' | 'code' | 'password'; bot: string }
export interface BotSearchResult { text: string; items: { id: string; title: string; tmdbId?: string; size?: string; kind: 'resource' | 'page' }[]; links: string[]; remaining?: number; page?: number; empty: boolean; cached?: boolean; expiresAt?: string }
export async function telegramUserRequest<T>(token: string, path: string, signal: AbortSignal, body?: object): Promise<T> {
  const response = await fetch(`/api/admin/telegram-user/${path}`, { method: body === undefined ? 'GET' : 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(75000)]), body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error || 'Telegram 请求失败，请稍后重试')
  return data
}
