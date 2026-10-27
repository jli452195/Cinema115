export interface TelegramUserStatus { apiId: number; configured: boolean; botConfigured: boolean; hasSession: boolean; authorized: boolean; name: string; username: string; stage: 'idle' | 'code' | 'password'; bot: string }
export interface TelegramChannel { id: string; title: string; username?: string }
export interface TelegramChannelConfig { account: TelegramUserStatus; channels: TelegramChannel[] }
export interface TelegramChannelPage { channels: TelegramChannel[]; selectionToken: string; nextCursor: string; config?: TelegramChannelConfig }
export interface BotSearchResult { text: string; items: { id: string; title: string; tmdbId?: string; size?: string; kind: 'resource' | 'page' }[]; links: string[]; remaining?: number; page?: number; empty: boolean; allLoaded?: boolean; cached?: boolean; expiresAt?: string; partial?: boolean; warning?: string }
export interface BotSearchStreamEvent { complete: boolean; result: BotSearchResult }
export interface BotSearchProgress { stage: 'queued' | 'waiting' | 'collecting'; position?: number }
export class TelegramSearchError extends Error {
  constructor(message: string, public code = '', public retryAt?: string) { super(message) }
}
const responseError = (data: { error?: string; code?: string; retryAt?: string } | null, fallback: string) => new TelegramSearchError(data?.error || fallback, data?.code, data?.retryAt)
export async function telegramUserRequest<T>(token: string, path: string, signal: AbortSignal, body?: object): Promise<T> {
  const timeout = path === 'search' || path === 'select' ? 140000 : 75000
  const response = await fetch(`/api/admin/telegram-user/${path}`, { method: body === undefined ? 'GET' : 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([signal, AbortSignal.timeout(timeout)]), body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw responseError(response.status === 401 ? null : data, response.status === 401 ? '管理员登录已过期，请重新登录' : 'Telegram 请求失败，请稍后重试')
  return data
}

export async function telegramUserSearchStream(token: string, signal: AbortSignal, body: object, onResult: (event: BotSearchStreamEvent) => void, onProgress?: (progress: BotSearchProgress) => void): Promise<void> {
  const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(140000)])
  const response = await fetch('/api/admin/telegram-user/search-stream', { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', Accept: 'text/event-stream' }, signal: requestSignal, body: JSON.stringify(body) })
  if (!response.ok || !response.body) {
    const data = await response.json().catch(() => null)
    throw responseError(response.status === 401 ? null : data, response.status === 401 ? '管理员登录已过期，请重新登录' : 'Telegram 搜索请求失败')
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let complete = false
  const consume = (block: string) => {
    let event = ''
    const data: string[] = []
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith('event:')) event = line.slice(6).trim()
      else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
    }
    if (!data.length) return
    const value = JSON.parse(data.join('\n'))
    if (event === 'error') throw responseError(value, 'Telegram 搜索请求失败')
    if (event === 'status') onProgress?.(value as BotSearchProgress)
    if (event === 'result') {
      const update = value as BotSearchStreamEvent
      complete ||= update.complete
      onResult(update)
    }
  }
  try {
    while (!complete) {
      const { done, value } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      buffer = buffer.replace(/\r\n/g, '\n')
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0 && !complete) {
        consume(buffer.slice(0, boundary))
        buffer = buffer.slice(boundary + 2)
        boundary = buffer.indexOf('\n\n')
      }
      if (done) break
    }
    if (!complete && buffer.trim()) consume(buffer)
    if (!complete) throw new TelegramSearchError('页面连接已中断；已开始的搜索仍在后台收尾，可重新打开查看', 'stream_interrupted')
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
